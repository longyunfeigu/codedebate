# detect - 平台自动检测

从 git remote URL 或用户配置自动识别当前项目的代码托管平台（GitHub 或 GitLab）。

## 文件说明

| 文件 | 说明 |
|------|------|
| `detect.go` | 平台检测逻辑：正则匹配 + 自定义域名支持 |
| `detect_test.go` | 测试覆盖所有 URL 格式和边界情况 |

## 支持的 URL 格式

```
┌──────────────────────────────────────────────────────────┬──────────┐
│ URL                                                      │ 平台     │
├──────────────────────────────────────────────────────────┼──────────┤
│ https://github.com/owner/repo.git                        │ GitHub   │
│ git@github.com:owner/repo.git                            │ GitHub   │
│ https://gitlab.com/group/project.git                     │ GitLab   │
│ git@gitlab.com:group/project.git                         │ GitLab   │
│ https://gitlab.company.com/group/project.git             │ GitLab*  │
│ git@gitlab.company.com:group/project.git                 │ GitLab*  │
└──────────────────────────────────────────────────────────┴──────────┘
* 需配置 platform.host = "gitlab.company.com"
```

## 核心检测流程

```
FromRemote(platformType, customHost)
  │
  ├─ platformType == "github"
  │   └─ 直接返回 GitHubPlatform
  │
  ├─ platformType == "gitlab"
  │   └─ 直接返回 GitLabPlatform(customHost)
  │
  └─ platformType == "auto" 或 ""（默认自动检测）
      │
      ├─ 执行 git remote get-url origin
      │   获取当前仓库的 origin remote URL
      │
      ├─ 正则匹配 github.com[:/]
      │   └─ 匹配 → 返回 GitHubPlatform
      │
      ├─ 正则匹配 gitlab.com[:/]
      │   └─ 匹配 → 返回 GitLabPlatform("")
      │
      ├─ customHost 非空时，正则匹配 {customHost}[:/]
      │   └─ 匹配 → 返回 GitLabPlatform(customHost)
      │
      └─ 均不匹配 → 返回错误
```

## 调用示例

```go
// 自动检测（推荐）
p, err := detect.FromRemote("auto", "")

// 强制指定 GitHub
p, err := detect.FromRemote("github", "")

// 自托管 GitLab
p, err := detect.FromRemote("auto", "gitlab.company.com")
// 或
p, err := detect.FromRemote("gitlab", "gitlab.company.com")
```
