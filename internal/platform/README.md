# platform - 多平台抽象层

定义 GitHub/GitLab 的统一接口，以及 diff 解析、评论格式化等共享工具。

## 目录结构

```
platform/
├── platform.go      # 统一接口 + 共享类型
├── diffparse.go     # Diff 解析、评论分类、去重
├── format.go        # 问题格式化（Markdown + 严重度徽章）
├── detect/          # 平台自动检测（从 git remote URL）
├── github/          # GitHub 实现（gh CLI）
└── gitlab/          # GitLab 实现（glab CLI）
```

## 完整接口定义

```go
// MRProvider 获取 MR/PR 的元数据和 diff 信息。
type MRProvider interface {
    GetDiff(mrID, repo string) (string, error)
    GetInfo(mrID, repo string) (*MRInfo, error)
    GetHeadCommitInfo(mrID, repo string) (*CommitInfo, error)
    GetChangedFiles(mrID, repo string) ([]DiffFile, error)
}

// MRCommenter 发布和查询 MR/PR 评论。
type MRCommenter interface {
    PostComment(mrID string, opts PostCommentOpts) CommentResult
    PostReview(mrID string, classified []ClassifiedComment, commitInfo CommitInfo, repo string) ReviewResult
    GetExistingComments(mrID, repo string) []ExistingComment
}

// IssueCommenter 提供将结构化问题发布为评论的高级入口。
type IssueCommenter interface {
    PostIssuesAsComments(mrID string, issues []IssueForComment, repo string) ReviewResult
}

// RepoDetector 检测和解析仓库信息。
type RepoDetector interface {
    DetectRepoFromRemote() (string, error)
    ParseMRURL(url string) (repo, mrID string, err error)
    BuildMRURL(repo, mrID string) string
}

// HistoryProvider 查询 MR/PR 历史信息（用于 context gathering）。
type HistoryProvider interface {
    GetMRDetails(mrNumber int, cwd string) (*MRDetail, error)
    GetMRsForCommit(commitSHA string, cwd string) ([]int, error)
}

// Platform 组合了所有子接口，代表一个完整的代码托管平台。
type Platform interface {
    Name() string // "github" | "gitlab"
    MRProvider
    MRCommenter
    IssueCommenter
    RepoDetector
    HistoryProvider
}
```

## 核心数据结构示例

### MRInfo

包含 MR/PR 的基本元数据（标题、描述、HEAD SHA）：

```go
&MRInfo{
    Title:       "feat: add user authentication",
    Description: "This PR implements JWT-based auth...",
    HeadSHA:     "abc123def456",
}
```

### CommitInfo

包含评论发布所需的提交 SHA 信息。GitHub 和 GitLab 有本质区别：

```go
// GitHub: 只需 HeadSHA
CommitInfo{HeadSHA: "abc123"}

// GitLab: 需要 3 个 SHA（用于 Discussions API 定位评论位置）
CommitInfo{
    HeadSHA:  "abc123",  // MR 的最新提交
    BaseSHA:  "def456",  // 目标分支的基准提交
    StartSHA: "789ghi",  // MR 创建时的起始提交
}
```

### DiffFile

表示 MR/PR 中的单个变更文件及其补丁内容：

```go
[]DiffFile{
    {Filename: "src/auth.go", Patch: "@@ -10,3 +10,5 @@\n func Login()..."},
    {Filename: "src/handler.go", Patch: "@@ -1,5 +1,8 @@\n+import \"jwt\"..."},
}
```

### IssueForComment

将代码审查问题转换为评审评论的输入结构：

```go
IssueForComment{
    File:         "src/auth.go",
    Line:         intPtr(42),
    Title:        "SQL injection vulnerability",
    Description:  "The query uses string concatenation...",
    Severity:     "high",
    SuggestedFix: "Use parameterized queries",
    RaisedBy:     "security-reviewer, perf-reviewer",
}
```

### ClassifiedComment

经过分类后的评论，根据 diff 信息决定发布模式。三种模式：

```go
// inline: 文件在 diff 中且行号匹配 — 显示为代码行内评论
ClassifiedComment{Input: comment, Mode: "inline"}

// file: 文件在 diff 中但行号不在 diff 范围 — 显示为文件级评论
ClassifiedComment{Input: comment, Mode: "file"}

// global: 文件不在 diff 中 — 显示为 MR/PR 全局评论
ClassifiedComment{Input: comment, Mode: "global"}
```

### ReviewResult

汇总批量发布评审评论的结果统计：

```go
ReviewResult{
    Posted:    8,
    Inline:    5,   // 行内评论
    FileLevel: 2,   // 文件级评论
    Global:    1,   // 全局评论
    Failed:    0,
    Skipped:   1,   // 去重跳过
}
```

## 评论分类与发布流程

```
PostIssuesAsComments(mrID, issues, repo)
  │
  ├─ 1. GetHeadCommitInfo(mrID, repo) → CommitInfo
  │     获取提交 SHA（GitHub 1 个，GitLab 3 个）
  │
  ├─ 2. FormatIssueBody(issue) → Markdown 正文
  │     将每个 IssueForComment 格式化为带徽章的 Markdown
  │
  ├─ 3. GetChangedFiles(mrID, repo) → []DiffFile
  │     获取变更文件列表及 patch 内容
  │
  ├─ 4. ParseDiffLines(patch) → map[int]bool
  │     从 patch 解析出右侧（新文件）的有效行号集合
  │
  ├─ 5. ClassifyCommentsByDiff(comments, diffInfo)
  │     ├─ 文件在 diff 且行号匹配  → inline
  │     ├─ 文件在 diff 但行号不在   → file
  │     └─ 文件不在 diff            → global
  │
  ├─ 6. IsDuplicateComment(comment, existing)
  │     对比前 100 字符 + path + line 去重
  │
  └─ 7. PostReview(mrID, classified, commitInfo, repo)
        ├─ 批量提交 inline + file 评论
        ├─ 批量失败 → 逐条 PostComment 降级
        └─ 逐条发布 global 评论
```

## FormatIssueBody 输出示例

给定上方的 `IssueForComment` 示例，`FormatIssueBody` 生成的 Markdown：

```markdown
🟠 **SQL injection vulnerability**

The query uses string concatenation...

**Suggested fix:** Use parameterized queries

_Raised by: security-reviewer, perf-reviewer_
```

## 严重度徽章映射

`SeverityToBadge` 将严重等级字符串转换为对应的 emoji 徽章：

```
"critical" → 🔴
"high"     → 🟠
"medium"   → 🟡
"low"      → 🟢
其他       → ⚪
```

## HistoryProvider：GitHub 与 GitLab 的实现差异

`HistoryProvider` 只有两个方法，但两个平台在 CLI 工具、API 端点、数据结构上都有差异。

### GetMRsForCommit — 通过 commit SHA 反查 PR/MR

**GitHub**（`github.go:491`）：

```bash
gh api repos/{owner}/{repo}/commits/{sha}/pulls --jq ".[].number"
```

`--jq` 直接在 CLI 层过滤 JSON，stdout 输出纯数字（每行一个），然后逐行 `strconv.Atoi`。

**GitLab**（`gitlab.go:714`）：

```bash
glab api projects/{encoded_project}/repository/commits/{sha}/merge_requests
```

返回完整 JSON 数组，需要 `json.Unmarshal` 后手动提取 `iid` 字段。GitLab 的 API 路径多了 `repository/` 层级，且项目路径需要 URL 编码（`group/subgroup/project` → `group%2Fsubgroup%2Fproject`）。

### GetMRDetails — 获取 PR/MR 详情

**GitHub**（`github.go:451`）— 一次调用拿到所有信息：

```bash
gh pr view 38 --json number,title,author,mergedAt,files
```

`gh` 的 `--json` 参数支持指定返回字段，文件列表也包含在内。

**GitLab**（`gitlab.go:659`）— 需要两次 API 调用：

```bash
# 第一次：MR 基本信息（标题、作者、合并时间）
glab api projects/{encoded}/merge_requests/{iid}

# 第二次：变更文件列表（GitLab MR API 不包含文件列表）
glab api projects/{encoded}/merge_requests/{iid}/diffs
```

GitLab 的 MR 详情 API 不返回文件列表，必须单独调 `/diffs` 端点获取。

### 对比总结

| 维度 | GitHub | GitLab |
|------|--------|--------|
| CLI 工具 | `gh` | `glab` |
| commit → PR API | `repos/{repo}/commits/{sha}/pulls` | `projects/{encoded}/repository/commits/{sha}/merge_requests` |
| PR/MR 编号字段 | `number`（全局唯一） | `iid`（项目内唯一） |
| 获取详情的 API 调用次数 | 1 次（`gh pr view --json`） | 2 次（MR 信息 + diffs 分开） |
| 文件列表来源 | `files[].path`（在详情 API 中） | 需额外调 `/diffs` → `[].new_path` |
| 作者字段 | `author.login` | `author.username` |
| 项目标识 | `owner/repo` 原样传递 | 需要 `url.PathEscape` 编码（支持嵌套组） |

### glab 如何知道是哪个 repo

GitLab 实现中有两种调用模式：

**CLI 模式**（不传 repo）— 依赖 git 上下文：

```go
exec.Command("glab", "mr", "diff", mrID)
exec.Command("glab", "mr", "view", mrID, "--output", "json")
```

`glab` 自动从当前目录的 `git remote get-url origin` 推断项目路径。前提是进程必须在 git 仓库目录下运行。

**API 模式**（显式传 repo）— 不依赖 git 上下文：

```go
exec.Command("glab", "api",
    fmt.Sprintf("projects/%s/merge_requests/%s/diffs", encodeProject(repo), mrID),
)
```

项目路径直接编码到 API URL 中，不需要 git 上下文。适用于 server/webhook 模式。

代码中通过 `resolveRepo` 统一处理这两种情况：

```go
func (g *GitLabPlatform) resolveRepo(repo string) (string, error) {
    if repo != "" {
        return repo, nil               // 调用方给了就直接用（server 模式）
    }
    return g.DetectRepoFromRemote()     // 没给就从 git remote 推断（本地模式）
}
```

`gh` 也有同样的机制：有 `--repo` 参数就用显式指定的，没有就从 git remote 推断。

### 为什么通过 CLI 工具而不是直接调 HTTP API

两个平台都是通过 CLI 工具（`gh`/`glab`）间接调 REST API，而不是直接用 `net/http`。好处是 CLI 工具自动处理了：

- **认证** — token 的存储和注入（`gh auth`、`glab auth`），不需要 CodeDebate 管理密钥
- **分页** — `gh api --paginate` 自动处理多页结果
- **重试** — CLI 内置对 429/5xx 的重试逻辑
- **主机配置** — GitLab 自托管实例的 host 配置由 `glab` 管理
