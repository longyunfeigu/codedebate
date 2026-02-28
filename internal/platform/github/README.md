# github - GitHub 平台实现

通过 `gh` CLI 实现 Platform 接口，操作 GitHub PR。

## 文件说明

| 文件 | 说明 |
|------|------|
| `github.go` | 完整的 Platform 接口实现（所有方法通过 gh CLI 调用） |
| `github_test.go` | 测试 |

## 外部依赖

- **gh CLI**（GitHub CLI），需已通过 `gh auth login` 完成认证
- 所有操作均通过 `gh` 命令执行，不直接调用 GitHub REST API

## 操作与 gh 命令映射

| 方法 | gh 命令 |
|------|---------|
| `GetDiff` | `gh pr diff <num> -R <repo>` |
| `GetInfo` | `gh pr view <num> --json title,body -R <repo>` |
| `GetHeadCommitInfo` | `gh pr view <num> --json headRefOid --jq .headRefOid -R <repo>` |
| `GetChangedFiles` | `gh api repos/<repo>/pulls/<num>/files --paginate` |
| `GetExistingComments` | `gh api repos/<repo>/pulls/<num>/comments --paginate` |
| `PostReview` | `gh api repos/<repo>/pulls/<num>/reviews --input -`（批量） |
| `PostComment` | 三级降级策略（见下方） |
| `PostNote` | `gh api repos/<repo>/issues/<num>/comments --method POST --input -` |
| `UpsertSummaryNote` | `gh api repos/<repo>/issues/<num>/comments`（列举）+ `gh api repos/<repo>/issues/comments/<id> --method PATCH --input -`（更新） |
| `GetMRDetails` | `gh pr view <num> --json number,title,author,mergedAt,files` |

## gh CLI 调用示例

```bash
# GetDiff — 获取 PR diff
gh pr diff 42 -R owner/repo

# GetInfo — 获取 PR 标题和描述
gh pr view 42 --json title,body -R owner/repo
# → {"title": "feat: add auth", "body": "This PR implements..."}

# GetHeadCommitInfo — 获取 HEAD 提交 SHA
gh pr view 42 --json headRefOid --jq .headRefOid -R owner/repo
# → abc123def456

# GetChangedFiles — 获取变更文件列表（含 patch）
gh api repos/owner/repo/pulls/42/files --paginate
# → [{"filename": "src/auth.go", "patch": "@@ -10,3 +10,5 @@..."}, ...]

# GetExistingComments — 获取已有评论（用于去重）
gh api repos/owner/repo/pulls/42/comments --paginate --jq '.[]'
# → {"path": "src/auth.go", "line": 10, "body": "Previous comment..."}

# PostReview — 批量提交评审评论
gh api repos/owner/repo/pulls/42/reviews --input - <<'EOF'
{
  "commit_id": "abc123",
  "event": "COMMENT",
  "comments": [
    {"path": "src/auth.go", "line": 42, "side": "RIGHT", "body": "..."},
    {"path": "src/handler.go", "body": "...", "subject_type": "file"}
  ]
}
EOF
```

## 评论发布流程（三级降级）

### PostReview — 批量提交

```
PostReview(prNum, classified, commitInfo, repo)
  │
  ├─ 1. GetExistingComments → 去重（IsDuplicateComment）
  │     重复评论 → result.Skipped++
  │
  ├─ 2. 按模式分组
  │     ├─ inline + file → 收集为 reviewComments
  │     └─ global → 单独处理
  │
  ├─ 3. 批量提交 reviewComments
  │     POST /repos/{repo}/pulls/{num}/reviews
  │     body: { commit_id, event: "COMMENT", comments: [...] }
  │     │
  │     ├─ 成功 → 统计 inline/fileLevel 计数
  │     └─ 失败 → 降级为逐条 PostComment
  │
  └─ 4. 逐条发布 global 评论
        gh pr comment {num} --body-file - --repo {repo}
```

### PostComment — 单条三级降级

```
PostComment(prNum, opts)
  │
  ├─ 第一级: inline（行内评论）
  │   POST /repos/{repo}/pulls/{num}/comments
  │   body: { body, commit_id, path, line, side: "RIGHT" }
  │   └─ 成功 → CommentResult{Success: true, Inline: true}
  │
  ├─ 第二级: file（文件级评论）
  │   POST /repos/{repo}/pulls/{num}/comments
  │   body: { body: "**Line N:**\n\n" + body, commit_id, path, subject_type: "file" }
  │   └─ 成功 → CommentResult{Success: true, Inline: true}
  │
  └─ 第三级: global（全局 PR 评论）
      gh pr comment {num} --body-file -
      body: "**path:line**\n\n" + body
      └─ 成功 → CommentResult{Success: true, Inline: false}
```
