# context - 上下文收集器

在审查前自动收集代码上下文（调用链、PR/MR 历史、项目文档），通过 AI 分析生成结构化摘要，帮助审查者更全面地理解代码变更的系统影响。

## 文件说明

| 文件 | 说明 |
|------|------|
| `gatherer.go` | 主编排：提取变更文件 → 并行收集 → AI 分析 → 解析结果。包含 `GathererOptions`、`ContextGatherer`、`parseAIResponse` |
| `types.go` | 所有数据类型定义：`GatheredContext`、`AffectedModule`、`CallChainItem`、`RelatedPR`、`DesignPattern`、`RawReference` 等 |
| `reference.go` | 调用链分析：正则提取符号 (`ExtractSymbolsFromDiff`)、ripgrep 搜索引用 (`FindReferences`)、格式化输出 |
| `history.go` | 历史 PR/MR 收集：`git log --since` 提取 PR 编号，通过 `HistoryProvider` 获取详情和重叠文件 |
| `docs.go` | 文档收集：递归查找 .md 文件（跳过 node_modules/.git/dist/vendor） |
| `prompt.go` | 构建 AI 分析 prompt：整合 diff、文件列表、符号引用、PR 历史、项目文档 |
| `adapter.go` | 适配器：将 context 包类型转换为 orchestrator 包类型（见下方设计说明） |
| `reference_test.go` | 符号提取的单元测试 |

## 适配器模式：为什么需要 adapter.go

### 问题：循环依赖

`orchestrator` 包需要调用上下文收集，`context` 包需要用 `provider.AIProvider` 做 AI 分析。如果让 `orchestrator` 直接导入 `context` 包的类型，依赖关系就会变成：

```
orchestrator → context（使用 GatheredContext 类型）
context → orchestrator（？不行，Go 禁止循环导入）
```

实际上 `context` 包不需要导入 `orchestrator`，但两个包各自定义了 `GatheredContext` 等结构体。问题在于 `orchestrator` 不能直接使用 `context.GatheredContext`——否则 `orchestrator` 就依赖了 `context`，而未来如果 `context` 需要引用 `orchestrator` 的任何类型，就会形成环。

### 解决方案

让 `orchestrator` 只定义接口和自己的数据类型，不导入 `context` 包：

```
orchestrator 包（不导入 context）
  ├─ ContextGathererInterface   ← 接口，只要求 Gather() 方法
  ├─ GatheredContext             ← orchestrator 自己的结构体
  └─ 通过接口调用，不关心具体实现

context 包（导入 orchestrator）
  ├─ ContextGatherer            ← 真正干活的实现
  ├─ GatheredContext             ← context 自己的结构体（字段几乎一样）
  └─ ContextGathererAdapter     ← 适配器，做类型转换
```

依赖方向始终是单向的：

```
cmd/review.go → context.NewContextGathererAdapter()
                    ↓
              context 包 → orchestrator 包（导入接口和类型定义）
              context 包 → provider 包（导入 AIProvider）
              orchestrator 包 → provider 包（导入 AIProvider）
```

### adapter.go 做了什么

`ContextGathererAdapter` 实现 `orchestrator.ContextGathererInterface` 接口，内部委托给真正的 `ContextGatherer`，然后通过 `convertToOrchestrator()` 逐字段复制，将 `context.GatheredContext` 转成 `orchestrator.GatheredContext`：

```
orchestrator 调用:
  adapter.Gather(diff, prNumber, baseBranch)
    └─ inner.Gather(...)                    ← 真正执行收集
         → context.GatheredContext
    └─ convertToOrchestrator(gc)            ← 逐字段复制到 orchestrator 的类型
         → orchestrator.GatheredContext
```

两个 `GatheredContext` 结构体字段几乎一样，`convertToOrchestrator` 就是机械的字段映射。看起来啰嗦，但这是 Go 没有跨包结构体隐式转换的代价，换来的是干净的单向依赖图。

### 为什么不把类型提到公共包

另一个方案是把 `GatheredContext` 提到一个 `types` 或 `shared` 包里，两边都导入。但目前只有这一个类型需要共享，单独建包属于过度设计。如果未来有更多共享类型，可以再抽取。

## GatheredContext 完整数据示例

以下 JSON 展示了 `Gather()` 方法返回的完整 `GatheredContext` 结构：

```json
{
  "affectedModules": [
    {
      "name": "Authentication Module",
      "path": "internal/auth",
      "description": "Handles user login, JWT token generation and validation",
      "affectedFiles": ["internal/auth/handler.go", "internal/auth/jwt.go"],
      "totalFiles": 5,
      "impactLevel": "core"
    },
    {
      "name": "API Router",
      "path": "internal/router",
      "description": "HTTP routing and middleware",
      "affectedFiles": ["internal/router/routes.go"],
      "totalFiles": 3,
      "impactLevel": "moderate"
    }
  ],
  "callChain": [
    {
      "symbol": "HandleLogin",
      "file": "internal/auth/handler.go",
      "callers": [
        {"symbol": "setupRoutes", "file": "internal/router/routes.go", "context": "API endpoint registration"},
        {"symbol": "TestHandleLogin", "file": "internal/auth/handler_test.go", "context": "Unit test"}
      ]
    }
  ],
  "relatedPRs": [
    {
      "number": 38,
      "title": "refactor: extract auth middleware",
      "author": "alice",
      "mergedAt": "2024-01-10",
      "overlappingFiles": ["internal/auth/handler.go"],
      "relevance": "direct"
    }
  ],
  "designPatterns": [
    {
      "pattern": "Repository Pattern",
      "location": "internal/auth/repository.go",
      "description": "Data access is abstracted through repository interfaces",
      "source": "inferred"
    }
  ],
  "summary": "This PR modifies the core authentication module...",
  "gatheredAt": "2024-01-15T10:25:00Z",
  "prNumber": "42",
  "baseBranch": "main",
  "rawReferences": [
    {
      "symbol": "HandleLogin",
      "foundInFiles": [
        {"file": "internal/router/routes.go", "line": 15, "content": "router.POST(\"/login\", auth.HandleLogin)"},
        {"file": "internal/auth/handler_test.go", "line": 8, "content": "func TestHandleLogin(t *testing.T) {"}
      ]
    }
  ]
}
```

数据来源说明：

```
GatheredContext
  ├─ affectedModules  ← AI 分析生成（aiAnalysisResult.AffectedModules）
  ├─ callChain        ← AI 分析生成（aiAnalysisResult.CallChain）
  ├─ relatedPRs       ← CollectHistory() 直接收集（非 AI 生成）
  ├─ designPatterns   ← AI 分析生成（aiAnalysisResult.DesignPatterns）
  ├─ summary          ← AI 分析生成（aiAnalysisResult.Summary）
  ├─ gatheredAt       ← time.Now()
  ├─ prNumber         ← 调用方传入
  ├─ baseBranch       ← 调用方传入
  └─ rawReferences    ← CollectReferences() 直接收集（ripgrep 原始结果）
```

## 核心流程：Gather

```
Gather(diff, prNumber, baseBranch)
  │
  ├─ 1. extractChangedFiles(diff)
  │     正则匹配 "diff --git a/X b/X" / "--- a/X" / "+++ b/X"
  │     去重，排除 /dev/null
  │     → []string{"internal/auth/handler.go", "internal/auth/jwt.go", ...}
  │
  ├─ 2. 并行收集 (三个 goroutine + sync.WaitGroup)
  │     │
  │     ├─ goroutine 1: CollectReferences(diff, cwd)
  │     │   ExtractSymbolsFromDiff(diff)  从 +行 提取函数/类型/类名
  │     │   FindReferences(symbols, cwd)  rg 全局搜索每个符号
  │     │   → []RawReference
  │     │
  │     ├─ goroutine 2: CollectHistory(changedFiles, maxDays, maxPRs, cwd, provider)
  │     │   git log --since="30 days ago" -- file1 file2 ...
  │     │   提取提交消息中的 #N → PR 编号
  │     │   historyProvider.GetMRDetails(prNum) → 标题/作者/文件列表
  │     │   计算与当前 PR 的文件重叠 → relevance: "direct" | "same-module"
  │     │   → []RelatedPR
  │     │
  │     └─ goroutine 3: CollectDocs(patterns, maxSize, cwd)
  │         递归搜索 docs/, README.md, ARCHITECTURE.md, DESIGN.md
  │         跳过 node_modules, .git, dist, vendor
  │         单文件最大 50000 字节
  │         → []RawDoc
  │
  ├─ 3. BuildAnalysisPrompt(diff, files, refs, prs, docs)
  │     组装结构化 prompt（见下方 AI 分析 prompt 结构）
  │
  ├─ 4. AI Chat 分析
  │     system prompt: "You are a senior software architect..."
  │     请求 AI 返回 JSON 格式的结构化分析结果
  │     失败时: 返回部分上下文（relatedPRs + rawReferences + 错误消息），不中断流程
  │
  └─ 5. parseAIResponse(response)
        尝试顺序:
          1. 匹配 ```json ... ``` 代码块 → json.Unmarshal
          2. 匹配任意 {...} JSON 对象 → json.Unmarshal
          3. 都失败 → 将响应文本截断到 1000 字符作为 summary
        → aiAnalysisResult{AffectedModules, CallChain, DesignPatterns, Summary}
```

## 符号提取示例

`ExtractSymbolsFromDiff` 从 diff 的 `+` 行中提取符号名称：

```
Go 代码:
  +func HandleLogin(w http.ResponseWriter, r *http.Request) {  → "HandleLogin"
  +func (s *AuthService) Validate(token string) error {        → "Validate"
  +type UserClaims struct {                                     → "UserClaims"

JS/TS 代码:
  +export function authenticateUser(req, res) {                → "authenticateUser"
  +export class AuthController {                                → "AuthController"
  +function validateToken(token) {                              → "validateToken"
  +const fetchUser = async (id) => {                            → "fetchUser"
```

支持的正则模式（`symbolPatterns`）：

| 语言 | 模式 | 示例 |
|------|------|------|
| Go | `func Name(` | `func HandleLogin(` |
| Go | `func (r Type) Name(` | `func (s *AuthService) Validate(` |
| Go | `type Name struct/interface` | `type UserClaims struct` |
| JS/TS | `function name(` / `async function name(` | `function validateToken(` |
| JS/TS | `const/let/var name = (` / `= async(` | `const handler = (` |
| JS/TS | `const name = (...) =>` | `const fetchUser = async (id) =>` |
| 通用 | `class Name` | `class AuthController` |
| JS/TS | `export const/function/class Name` | `export function authenticateUser(` |

过滤规则：
- 长度 <= 2 的标识符跳过
- 常见关键字跳过：`get`, `set`, `new`, `for`, `if`, `do`, `var`, `nil`, `err`, `ok`
- 自动去重

## 前置知识：Unified Diff 格式

CodeDebate 的 context 模块大量解析 `git diff` 输出，理解 unified diff 格式有助于理解 `extractChangedFiles` 和 `ExtractSymbolsFromDiff` 的实现。

一个典型的 unified diff 长这样：

```diff
diff --git a/internal/auth/handler.go b/internal/auth/handler.go
index abc1234..def5678 100644
--- a/internal/auth/handler.go
+++ b/internal/auth/handler.go
@@ -10,6 +10,8 @@ package auth
 import "net/http"

 // HandleLogin 处理用户登录
+// 新增：支持 OAuth2 认证
+func HandleOAuth(w http.ResponseWriter, r *http.Request) {
+    // OAuth 逻辑
+}
-func oldHelper() {}
```

逐行解读：

| 行 | 含义 |
|------|------|
| `diff --git a/X b/X` | diff 头部，标识被比较的文件路径。`extractChangedFiles` 从这一行提取文件名 |
| `index abc1234..def5678` | 两个版本的 Git blob SHA，`100644` 表示普通文件权限 |
| `--- a/internal/auth/handler.go` | 旧版本文件路径（删除时为 `--- /dev/null`） |
| `+++ b/internal/auth/handler.go` | 新版本文件路径（新建时旧版为 `/dev/null`） |
| `@@ -10,6 +10,8 @@` | **hunk 头**：旧文件从第 10 行开始取 6 行，新文件从第 10 行开始取 8 行 |
| 空格开头的行 | 上下文行（未修改的代码，用于定位） |
| `+` 开头的行 | **新增的行**。`ExtractSymbolsFromDiff` 只扫描这些行来提取函数/类型名 |
| `-` 开头的行 | **删除的行** |

**为什么叫 "unified"？** 因为早期的 diff 是分开显示两个文件的（normal diff），而 unified diff 把新旧版本合并在一起显示，用 `+`/`-` 前缀区分，更紧凑易读。这是 `git diff` 的默认输出格式。

## ripgrep 搜索示例

`FindReferences` 对每个提取到的符号调用 ripgrep：

```bash
# FindReferences 内部调用（每个符号一次）
rg -n -H --no-heading "HandleLogin"
#  -n: 显示行号
#  -H: 显示文件名
#  --no-heading: 不按文件分组，每行一条结果

# ripgrep 输出格式: "文件:行号:内容"
internal/router/routes.go:15:	router.POST("/login", auth.HandleLogin)
internal/auth/handler_test.go:8:func TestHandleLogin(t *testing.T) {
internal/auth/handler.go:12:func HandleLogin(w http.ResponseWriter, r *http.Request) {
```

解析后生成 `RawReference`：

```go
RawReference{
    Symbol: "HandleLogin",
    FoundInFiles: []ReferenceLocation{
        {File: "internal/router/routes.go",      Line: 15, Content: "router.POST(\"/login\", auth.HandleLogin)"},
        {File: "internal/auth/handler_test.go",   Line: 8,  Content: "func TestHandleLogin(t *testing.T) {"},
        {File: "internal/auth/handler.go",         Line: 12, Content: "func HandleLogin(w http.ResponseWriter, r *http.Request) {"},
    },
}
```

### ripgrep 是什么

ripgrep（命令 `rg`）是一个现代化的代码搜索工具，可以理解为 `grep` 的高性能替代品。CodeDebate 选择 ripgrep 而非 grep 来搜索符号引用，主要原因：

| 特性 | grep | ripgrep |
|------|------|---------|
| 速度 | 较慢（逐行扫描） | 极快（利用 Rust + 并行 + 内存映射） |
| `.gitignore` | 不识别 | 自动跳过 `.gitignore` 中的文件 |
| 二进制文件 | 默认搜索（产生乱码） | 自动跳过二进制文件 |
| Unicode | 部分支持 | 完整支持 |
| 递归搜索 | 需要 `grep -r` | 默认递归 |

CodeDebate 中 `FindReferences` 的调用方式：

```bash
rg -n -H --no-heading "HandleLogin"
```

- `-n`：输出行号（解析后存入 `ReferenceLocation.Line`）
- `-H`：输出文件名（解析后存入 `ReferenceLocation.File`）
- `--no-heading`：不按文件分组，每行独立输出 `文件:行号:内容`，方便逐行解析

在大型项目中，ripgrep 可以在毫秒级别完成全仓库搜索，这使得 CodeDebate 能快速找到每个变更符号在整个代码库中的所有引用位置。

## History：审查历史上下文

`history.go` 解决的核心问题：**让 AI 审查者了解代码的"前世今生"，而不是只看眼前的 diff。**

### 没有历史上下文 vs 有历史上下文

假设当前 PR #42 修改了 `internal/auth/handler.go`，添加了 OAuth2 支持：

**没有历史上下文的审查**：
> "这个 handler 为什么要同时支持 JWT 和 OAuth2 两种认证？看起来冗余，建议统一。"

AI 审查者不知道 JWT 是 3 周前 PR #38 刚重构过的核心逻辑，贸然建议删除会造成回退。

**有历史上下文的审查**：
> "PR #38（3 周前，作者 alice）刚完成了 auth middleware 的重构，将 JWT 认证提取为独立中间件。当前 PR 在此基础上添加 OAuth2 是合理的渐进式改进。建议确保两种认证方式共用相同的 session 管理逻辑。"

### 场景对照表

| 场景 | 没有 History | 有 History |
|------|-------------|-----------|
| 最近重构过的模块 | 可能建议重复重构 | 知道已重构，给出渐进式建议 |
| 同一 bug 的多次修复 | 不知道之前修过 | 提醒"这个 bug 已经修过 2 次，需要根治" |
| 关联 PR 的功能 | 审查孤立，遗漏上下文 | 串联多个 PR，理解完整功能演进 |
| 新人的代码 | 不知道团队已有约定 | 参考历史 PR 中团队的编码风格 |

### HistoryProvider 是什么

`HistoryProvider` 是 `platform` 包定义的接口，只有两个方法：

```go
type HistoryProvider interface {
    GetMRDetails(mrNumber int, cwd string) (*MRDetail, error)
    GetMRsForCommit(commitSHA string, cwd string) ([]int, error)
}
```

- `GetMRsForCommit` — 给一个 commit SHA，问平台"这个 commit 属于哪些 PR/MR"
- `GetMRDetails` — 给一个 PR 编号，拿回标题、作者、合并时间、涉及的文件列表

它是 `Platform` 大接口的子接口之一：

```
Platform（完整平台能力）
  ├─ MRProvider       — 获取 diff、PR 信息
  ├─ MRCommenter      — 发评论
  ├─ IssueCommenter   — 发结构化问题评论
  ├─ RepoDetector     — 检测仓库、解析 URL
  └─ HistoryProvider  — 查历史 PR（history.go 用的就是这个）
```

GitHub 和 GitLab 各有一份具体实现。`history.go` 只依赖 `HistoryProvider` 这个小接口而不是整个 `Platform`，这样做的好处：

- **最小依赖** — `CollectHistory` 只要求"能查 PR 历史"，不关心能不能发评论、能不能拿 diff
- **可测试** — mock 两个方法就够了，不需要 mock 整个平台
- **可空** — `historyProvider` 可以传 nil（比如本地运行、不在 PR 上下文中），`CollectHistory` 会自动回退到 git commit message 解析

### 函数说明

| 函数 | 作用 |
|------|------|
| `CollectHistory` | **入口函数**。编排整个流程：先尝试 API 方式发现 PR，不行就回退到 commit message 解析，最后批量获取详情 |
| `extractCommitSHAs` | 调 `git log --pretty=format:%H` 拿到改过指定文件的 commit SHA 列表 |
| `discoverPRNumbers` | 对每个 SHA 调平台 API（`GetMRsForCommit`）反查关联的 PR 编号，边查边去重，有上限就停 |
| `extractPRNumbersFromMessages` | **回退方案**。调 `git log --pretty=format:%s` 拿 commit message，用 `findPRNumbers` 从中解析 `#N` |
| `findPRNumbers` | 纯字符串扫描：碰到 `#` 后面跟数字就提取为 PR 编号 |
| `getPRDetails` | 调平台 API（`GetMRDetails`）拿单个 PR 的标题/作者/文件列表，然后计算与当前 PR 的文件重叠度，判定 `direct` 还是 `same-module` |

### 完整流程（以具体例子说明）

假设当前 PR #42 修改了 `internal/auth/handler.go` 和 `internal/auth/jwt.go`：

```
CollectHistory(["internal/auth/handler.go", "internal/auth/jwt.go"], 30, 10, ".", historyProvider)
  │
  │  ┌─────────────────────────────────────────────────────┐
  │  │ 优先策略：commit SHA + 平台 API                      │
  │  └─────────────────────────────────────────────────────┘
  │
  ├─ 1. extractCommitSHAs()
  │     执行: git log --since=30 days ago --pretty=format:%H --max-count=30 -- handler.go jwt.go
  │     返回: ["a1b2c3d4", "e5f6g7h8", "i9j0k1l2"]
  │
  ├─ 2. discoverPRNumbers()
  │     对每个 SHA 调平台 API（最多 15 次调用，攒够 10 个 PR 就停）:
  │       GetMRsForCommit("a1b2c3d4") → [38]
  │       GetMRsForCommit("e5f6g7h8") → [38]     ← 重复，跳过
  │       GetMRsForCommit("i9j0k1l2") → [35]
  │     去重后: [38, 35]
  │
  │  ┌─────────────────────────────────────────────────────┐
  │  │ 回退策略：如果上面返回 0 个结果才执行                  │
  │  └─────────────────────────────────────────────────────┘
  │
  ├─ (回退) extractPRNumbersFromMessages()
  │     执行: git log --since=30 days ago --pretty=format:%s --name-only -- handler.go jwt.go
  │     输出:
  │       feat: extract auth middleware (#38)
  │       fix: token expiry bug (#35)
  │     findPRNumbers 扫描每行，碰到 # 后跟数字就提取: → [38, 35]
  │
  │  ┌─────────────────────────────────────────────────────┐
  │  │ 获取详情 + 计算关联度                                 │
  │  └─────────────────────────────────────────────────────┘
  │
  └─ 3. getPRDetails() 对每个 PR 编号:
        GetMRDetails(38) → {Title: "refactor: extract auth middleware", Author: "alice",
                            Files: ["internal/auth/handler.go", "internal/auth/middleware.go"]}

        当前 PR 改了: {handler.go, jwt.go}
        PR #38 改了:  {handler.go, middleware.go}
        重叠文件:     {handler.go}  → relevance: "direct"

        GetMRDetails(35) → {Title: "fix: token expiry bug", Author: "bob",
                            Files: ["internal/auth/token.go"]}

        重叠文件: 无 → relevance: "same-module"

最终输出:
  []RelatedPR{
      {Number: 38, Title: "refactor: extract auth middleware", Author: "alice",
       OverlappingFiles: ["internal/auth/handler.go"], Relevance: "direct"},
      {Number: 35, Title: "fix: token expiry bug", Author: "bob",
       OverlappingFiles: [], Relevance: "same-module"},
  }
```

### 两种发现 PR 的策略对比

| 维度 | API 方式（优先） | commit message 解析（回退） |
|------|-----------------|---------------------------|
| 方法 | SHA → `GetMRsForCommit` → PR 编号 | commit message 里找 `#N` |
| 适用场景 | 所有 merge 策略（squash、rebase、merge commit） | 仅当 commit message 包含 `#N` 时有效 |
| 局限 | 需要平台 API 可用 | squash merge 可能丢失原始 PR 编号 |
| 成本 | 每个 SHA 一次 API 调用（上限 15 次） | 纯本地 git 操作，零网络开销 |

`CollectHistory` 优先用 API 方式，因为它不依赖 commit message 的格式，能覆盖 squash merge、rebase merge 等所有合并策略。只有当 API 方式返回 0 个结果时（`historyProvider` 为 nil 或所有 API 调用都失败），才回退到 commit message 解析。

## AI 分析 prompt 结构

`BuildAnalysisPrompt` 生成发送给 AI 的结构化 prompt：

```
BuildAnalysisPrompt 生成的 prompt 结构:
├── system: "You are a senior software architect. Analyze the PR context and respond in JSON format only."
└── user prompt:
    ├── ## PR Diff
    │   diff 内容（截取前 10000 字符，超出部分标记 "... (truncated)"）
    ├── ## Changed Files
    │   变更文件列表（Markdown 无序列表）
    ├── ## Code References (grep results)
    │   符号引用关系（每个符号最多 20 条，内容截断到 100 字符）
    ├── ## Related Recent PRs
    │   相关 PR 历史（编号、标题、作者、关联程度）
    ├── ## Project Documentation
    │   项目文档内容（每份截取前 2000 字符）
    └── 分析要求:
        1. Affected Modules（受影响模块 + impactLevel）
        2. Call Chain Analysis（调用链 + 调用上下文）
        3. Design Patterns（设计模式 + 来源）
        4. Summary（2-3 段审查者摘要）
        → 要求以 JSON 格式返回
```

## 默认配置

`NewContextGatherer` 的默认值（可通过 `config.ContextGathererConfig` 覆盖非零值）：

```go
GathererOptions{
    History: {
        MaxDays: 30,               // 查询历史 PR 的天数范围
        MaxPRs:  10,               // 最多返回的关联 PR 数
    },
    Docs: {
        Patterns: []string{"docs", "README.md", "ARCHITECTURE.md", "DESIGN.md"},
        MaxSize:  50000,           // 单个文档最大字节数
    },
}
```

## 外部依赖

| 依赖 | 用途 | 缺失时行为 |
|------|------|-----------|
| `rg`（ripgrep） | 跨文件搜索符号引用 | 引用收集静默跳过（`cmd.Output()` 返回 error），不影响其他收集 |
| `git` | 查询历史提交中的 PR 编号 | 历史 PR 收集返回空列表 |
| `platform.HistoryProvider` | 获取 PR/MR 详情（标题、作者、文件列表） | 传入 nil 时 `getPRDetails` 直接返回 nil |
| `provider.AIProvider` | 执行上下文分析（Chat 接口） | AI 分析失败时返回部分上下文（含 rawReferences + relatedPRs），不中断流程 |
