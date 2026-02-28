# gitlab - GitLab 平台实现

通过 `glab` CLI 和 GitLab API 实现 Platform 接口，操作 GitLab MR。

## 文件说明

| 文件 | 说明 |
|------|------|
| `gitlab.go` | 完整的 Platform 接口实现（双模式：CLI + API） |
| `gitlab_test.go` | 测试 |

## 外部依赖

- **glab CLI**（GitLab CLI），需已通过 `glab auth login` 或设置 `GITLAB_TOKEN` 环境变量完成认证
- 支持自托管 GitLab（通过 `host` 字段指定域名）

## 双模式操作

GitLab 实现有两种运行模式，根据是否传入 `repo` 参数自动切换：

| 场景 | repo 参数 | 模式 | 说明 |
|------|-----------|------|------|
| CLI 交互 (`codedebate review`) | 空 | CLI 模式 | 使用 `glab mr diff/view/note`，依赖 git 上下文 |
| Webhook 服务 (`codedebate serve`) | 非空 | API 模式 | 使用 `glab api` 直接调用 GitLab REST API |

### CLI 模式示例

在本地仓库目录中直接运行，依赖当前 git 上下文：

```bash
# GetDiff — 获取 MR diff
glab mr diff 123

# GetInfo — 获取 MR 信息（JSON 输出）
glab mr view 123 --output json
# → {"title": "feat: add auth", "description": "This MR implements..."}

# GetHeadCommitInfo — 获取提交信息（从 diff_refs 解析）
glab mr view 123 --output json
# → {"diff_refs": {"head_sha": "abc123", "base_sha": "def456", "start_sha": "789ghi"}}

# PostComment (global) — 发布全局评论
glab mr note 123 --unique --message "🔴 **Critical: SQL injection...**"
```

### API 模式示例

通过 `glab api` 调用 GitLab REST API，不需要 git 上下文（适用于 webhook 服务）：

```bash
# GetDiff — 获取 MR diff
glab api "projects/group%2Fproject/merge_requests/123/diffs"
# → [{"diff": "@@ -10,3 +10,5 @@...", "new_path": "src/auth.go", "old_path": "src/auth.go"}]

# GetInfo — 获取 MR 信息
glab api "projects/group%2Fproject/merge_requests/123"
# → {"title": "feat: add auth", "description": "...",
#    "diff_refs": {"head_sha": "abc123", "base_sha": "def456", "start_sha": "789ghi"}}

# GetChangedFiles — 获取变更文件列表
glab api "projects/group%2Fproject/merge_requests/123/diffs"
# → [{"new_path": "src/auth.go", "diff": "@@ -10,3 +10,5 @@..."},
#    {"new_path": "src/handler.go", "diff": "@@ -1,5 +1,8 @@..."}]

# GetExistingComments — 获取已有讨论（用于去重）
glab api "projects/group%2Fproject/merge_requests/123/discussions"
# → [{"notes": [{"body": "...", "position": {"new_path": "src/auth.go", "new_line": 10}}]}]

# PostComment (inline discussion) — 发布行内评论
glab api "projects/group%2Fproject/merge_requests/123/discussions" \
  --method POST --input - <<'EOF'
{
  "body": "🟠 **SQL injection vulnerability**\n\nThe query uses string concatenation...",
  "position": {
    "position_type": "text",
    "new_path": "src/auth.go",
    "new_line": 42,
    "head_sha": "abc123",
    "base_sha": "def456",
    "start_sha": "789ghi"
  }
}
EOF

# PostNote (global) — 发布全局 note
glab api "projects/group%2Fproject/merge_requests/123/notes" \
  --method POST --input - <<'EOF'
{"body": "**src/config.go:15**\n\n🟡 **Hardcoded credentials detected**..."}
EOF
```

## 评论发布流程

### PostReview — 批量提交（含 Draft Notes 降级）

```
PostReview(mrNum, classified, commitInfo, repo)
  │
  ├─ 1. GetExistingComments → 去重（IsDuplicateComment）
  │     重复评论 → result.Skipped++
  │
  ├─ 2. 按模式分组
  │     ├─ inline  → inlineEntries
  │     ├─ file    → fileEntries
  │     └─ global  → globalEntries
  │
  ├─ 3. 尝试 Draft Notes API（需要 GitLab Premium）
  │     │
  │     │  对每个 inline/file 评论:
  │     │  POST /projects/:id/merge_requests/:iid/draft_notes
  │     │  body: { note, position: { position_type, new_path, new_line, head_sha, base_sha, start_sha } }
  │     │
  │     ├─ 全部创建成功 → bulk_publish 批量发布
  │     │   POST /projects/:id/merge_requests/:iid/draft_notes/bulk_publish
  │     │   └─ 统计 inline/fileLevel 计数
  │     │
  │     └─ 任一创建失败 → Draft Notes API 不可用，进入降级
  │
  ├─ 4. 降级: 逐条 PostComment（三级降级策略）
  │     对每个 inline/file 评论调用 PostComment
  │
  └─ 5. 逐条发布 global 评论
        glab mr note {num} --unique --message "**path:line**\n\n{body}"
```

### PostComment — 单条三级降级

```
PostComment(mrNum, opts)
  │
  ├─ 第一级: inline（行内 Discussion）
  │   POST /projects/:id/merge_requests/:iid/discussions
  │   body: { body, position: { position_type: "text", new_path, new_line, head_sha, base_sha, start_sha } }
  │   └─ 成功 → CommentResult{Success: true, Inline: true}
  │
  ├─ 第二级: file（文件级 Discussion）
  │   POST /projects/:id/merge_requests/:iid/discussions
  │   body: { body: "**Line N:**\n\n" + body, position: { position_type: "file", new_path, head_sha, base_sha, start_sha } }
  │   └─ 成功 → CommentResult{Success: true, Inline: true}
  │
  └─ 第三级: global（全局评论）
      glab mr note {num} --unique --message "**path:line**\n\n{body}"
      └─ 成功 → CommentResult{Success: true, Inline: false}
```

## 自托管 GitLab

`New(host)` 接收自定义域名，影响以下行为：

- `getHost()` 返回自定义域名（默认 `gitlab.com`）
- `BuildMRURL` 使用自定义域名构建 URL
- API 路径中的项目路径通过 `encodeProject()` 进行 URL 编码（`group/project` → `group%2Fproject`）

### 配置示例

```yaml
# config.yaml
platform:
  type: gitlab
  host: "gitlab.company.com"
```

### 检测流程

```
detect.FromRemote("auto", "gitlab.company.com")
  │
  ├─ git remote get-url origin
  │   → git@gitlab.company.com:team/backend-service.git
  │
  ├─ 匹配 github.com → 否
  ├─ 匹配 gitlab.com → 否
  ├─ 匹配 gitlab.company.com → 是
  │
  └─ 返回 GitLabPlatform{host: "gitlab.company.com"}
```

### URL 构建

```go
p := gitlab.New("gitlab.company.com")

p.BuildMRURL("team/backend-service", "123")
// → "https://gitlab.company.com/team/backend-service/-/merge_requests/123"

// API 调用中项目路径会被编码
encodeProject("team/backend-service")
// → "team%2Fbackend-service"
```
