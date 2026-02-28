# server - GitLab Webhook 服务器

HTTP 服务器，接收 GitLab MR Webhook 事件并自动触发代码审查。服务端模式下不依赖 CLI 交互，审查结果以评论形式推送回 GitLab。

## 文件说明

| 文件 | 说明 |
|------|------|
| `server.go` | HTTP 服务器：路由注册、并发控制（信号量）、取消重跑（mutex + map）、优雅关闭 |
| `webhook.go` | 数据结构定义、Webhook 事件解析、Secret 验证（constant-time compare）、触发条件过滤 |
| `reviewer.go` | 服务端审查流程：构建 prompt、创建 orchestrator、执行审查、发布评论和摘要 |
| `server_test.go` | Server 集成测试 |
| `webhook_test.go` | Webhook 解析和过滤逻辑的单元测试 |

## ServerConfig 数据结构

```go
ServerConfig{
    CodeDebateConfig:   cfg,            // *config.CodeDebateConfig - 审查配置（reviewers/analyzer/summarizer）
    Addr:          ":8080",        // 监听地址
    WebhookSecret: "my-secret",    // GitLab webhook 密钥，用于 X-Gitlab-Token 验证
    MaxConcurrent: 3,              // 最大并发审查数（默认 3，通过信号量控制）
    GitLabHost:    "gitlab.com",   // GitLab 域名，用于创建 GitLabPlatform 实例
}
```

内部 Server 结构体：

```go
Server{
    cfg:      ServerConfig,                  // 配置
    sem:      chan struct{},                 // 并发信号量，容量 = MaxConcurrent
    mu:       sync.Mutex,                   // 保护 inFlight map
    inFlight: map[string]*inFlightEntry,    // "group/project/42" -> entry，取消重跑
    logger:   *log.Logger,                  // [codedebate] 前缀的日志器
    server:   *http.Server,                 // 底层 HTTP Server
}
```

## Webhook Payload 示例

GitLab MR 事件（`MergeRequestEvent` 结构体对应的 JSON）：

```json
{
  "object_kind": "merge_request",
  "project": {
    "id": 123,
    "path_with_namespace": "group/project",
    "web_url": "https://gitlab.com/group/project"
  },
  "object_attributes": {
    "iid": 42,
    "title": "feat: add user authentication",
    "description": "This MR implements JWT-based auth...",
    "state": "opened",
    "action": "open",
    "source_branch": "feat/auth",
    "target_branch": "main",
    "url": "https://gitlab.com/group/project/-/merge_requests/42"
  }
}
```

对应 Go 类型映射：

```
MergeRequestEvent
  ├─ ObjectKind       string       → "merge_request"
  ├─ Project          ProjectInfo
  │    ├─ ID                int    → 123
  │    ├─ PathWithNamespace string → "group/project"
  │    └─ WebURL            string → "https://gitlab.com/group/project"
  └─ ObjectAttributes MRAttributes
       ├─ IID          int    → 42
       ├─ Title        string → "feat: add user authentication"
       ├─ Description  string → "This MR implements JWT-based auth..."
       ├─ State        string → "opened"
       ├─ Action       string → "open"
       ├─ SourceBranch string → "feat/auth"
       ├─ TargetBranch string → "main"
       └─ URL          string → "https://gitlab.com/.../merge_requests/42"
```

## HTTP 请求/响应示例

```
POST /webhook/gitlab
Headers:
  X-Gitlab-Token: my-secret
  Content-Type: application/json
Body: { MergeRequestEvent JSON }

响应场景:
  方法错误:          405 Method Not Allowed   (仅接受 POST)
  Secret 错误:       401 Unauthorized
  JSON 解析失败:     400 Bad Request
  不满足触发条件:    200 OK         {"status": "skipped"}
  触发审查:          202 Accepted   {"status": "accepted"}
                     (审查在后台 goroutine 异步执行)

GET /health
Response: 200 OK {"status": "ok"}
```

## ShouldTriggerReview 决策表

```
┌───────────────┬──────────┬────────────────────────────┬──────────┐
│ object_kind   │ state    │ action                     │ 触发?    │
├───────────────┼──────────┼────────────────────────────┼──────────┤
│ merge_request │ opened   │ open                       │ Yes      │
│ merge_request │ opened   │ reopen                     │ Yes      │
│ merge_request │ opened   │ update                     │ Yes      │
│ merge_request │ opened   │ approved                   │ No       │
│ merge_request │ merged   │ merge                      │ No       │
│ merge_request │ closed   │ close                      │ No       │
│ push          │ -        │ -                          │ No       │
│ merge_request │ opened   │ open (title: "Draft:...")  │ No       │
│ merge_request │ opened   │ open (title: "WIP:...")    │ No       │
└───────────────┴──────────┴────────────────────────────┴──────────┘

判断逻辑（按顺序短路）：
  1. object_kind != "merge_request"           → false
  2. state != "opened"                        → false
  3. action not in {open, reopen, update}     → false
  4. title 以 "Draft:" 或 "WIP:" 开头         → false
  5. 以上均通过                                → true
```

## 核心流程：请求处理

```
POST /webhook/gitlab
  │
  ├─ 0. Method Check
  │     r.Method != POST → 405 Method Not Allowed
  │
  ├─ 1. ValidateWebhookRequest
  │     r.Header.Get("X-Gitlab-Token") vs cfg.WebhookSecret
  │     使用 crypto/subtle.ConstantTimeCompare (防时序攻击)
  │     token 或 secret 为空 → false
  │     不匹配 → 401 Unauthorized
  │
  ├─ 2. ParseWebhookEvent
  │     io.ReadAll(r.Body) → json.Unmarshal → MergeRequestEvent
  │     失败 → 400 Bad Request
  │
  ├─ 3. ShouldTriggerReview
  │     见上方决策表
  │     不满足 → 200 OK {"status": "skipped"}
  │     (日志记录: kind, action, state, title)
  │
  ├─ 4. 返回 202 Accepted {"status": "accepted"}（立即响应）
  │
  └─ 5. go s.triggerReview(event)  ← 异步执行
        │
        ├─ 取消重跑: mutex 保护 inFlight map
        │       同一 MR 正在审查中 → 取消旧 review，等待清理完成
        │       审查完成后 defer 从 map 中移除（仅移除自己）
        │
        ├─ 并发控制: select { case sem <- struct{}{}: ... }
        │           信号量已满 → 直接丢弃（非阻塞），日志记录
        │           获取信号量 → defer 释放
        │
        ├─ 创建 gitlab.New(cfg.GitLabHost) 平台实例
        │
        ├─ 设置 10 分钟超时: context.WithTimeout
        │
        └─ RunServerReview(ctx, event, plat, cfg, logger)
```

## 核心流程：RunServerReview

```
RunServerReview(ctx, event, plat, cfg, logger)
  │
  ├─ 1. plat.GetDiff(mrID, repo)   获取 MR 的完整 diff
  │     plat.GetInfo(mrID, repo)   获取 MR 标题和描述
  │
  ├─ 2. 构建 review prompt
  │     格式: "Please review {url}.\n\nTitle: {title}\n\nDescription:\n{desc}\n\nDiff:\n```diff\n{diff}```"
  │
  ├─ 3. 创建 reviewers / analyzer / summarizer
  │     遍历 cfg.Reviewers → provider.CreateProvider → orchestrator.Reviewer
  │     与 cmd/review.go 相同逻辑
  │     关键差异: ContextGatherer = nil（服务端无本地文件系统，无法 ripgrep）
  │
  ├─ 4. 创建 orchestrator 并执行
  │     单审查者时: maxRounds=1, checkConvergence=false
  │     多审查者时: 使用 cfg.Defaults 配置
  │     显示层: display.NewNoopDisplay(logger)（仅日志，无终端 UI）
  │     orch.RunStreaming(ctx, label, prompt, noopDisplay)
  │
  ├─ 5. 发布行内评论
  │     convertIssuesToPlatform(result.ParsedIssues) → platform.IssueForComment
  │     plat.PostIssuesAsComments(mrID, issues, repo)
  │     日志记录: posted/inline/file-level/global/failed/skipped 数量
  │
  └─ 6. 发布审查摘要
        plat.(PostNote接口).PostNote(mrID, repo, "## CodeDebate Code Review Summary\n\n" + conclusion)
        通过类型断言检查平台是否支持 PostNote
```

## 保护机制

| 机制 | 实现方式 | 说明 |
|------|---------|------|
| **Secret 验证** | `crypto/subtle.ConstantTimeCompare` | 防止时序攻击，token 或 secret 为空时直接拒绝 |
| **并发控制** | `chan struct{}` 信号量（容量 = MaxConcurrent） | 超过上限时 select default 直接丢弃，不阻塞 |
| **取消重跑** | `sync.Mutex` + `map[string]*inFlightEntry` | key 格式 `"path_with_namespace/iid"`，同一 MR 新 webhook 取消旧 review 后重新执行 |
| **超时** | `context.WithTimeout(10 * time.Minute)` | 单次审查最多 10 分钟 |
| **优雅关闭** | `server.Shutdown(ctx)` | 外部通过 SIGINT/SIGTERM 触发，等待进行中的请求完成 |
| **异步执行** | `go s.triggerReview(event)` | Webhook 请求立即返回 202，审查在后台执行 |
