# cmd - CLI 命令层

定义 CodeDebate 所有 Cobra 子命令及其执行逻辑。CodeDebate 是一个多模型对抗式代码审查工具，通过多个 AI 审查者独立审查代码变更，再经过结构化辩论产出全面的审查结果。

## 文件说明

| 文件 | 说明 |
|------|------|
| `root.go` | 根命令定义，注册 `review`、`init`、`serve` 子命令 |
| `review.go` | `codedebate review` - 核心审查命令，支持 PR/MR、本地 diff、分支对比等多种模式 |
| `init.go` | `codedebate init` - 生成默认配置文件到 `~/.codedebate/config.yaml` |
| `serve.go` | `codedebate serve` - 启动 GitLab Webhook HTTP 服务器，自动审查 MR |

## CLI 使用示例

```bash
# ====== 基础用法 ======

# 审查 GitHub PR（自动检测平台）
codedebate review 42

# 审查 GitLab MR（通过完整 URL）
codedebate review https://gitlab.com/group/project/-/merge_requests/123

# 审查 GitHub PR（通过完整 URL）
codedebate review https://github.com/owner/repo/pull/42

# ====== 本地审查模式 ======

# 审查本地未提交的变更（git diff HEAD）
# 如果没有未提交变更，自动回退到最后一次提交（git diff HEAD~1 HEAD）
codedebate review --local

# 分支对比审查（当前分支 vs main）
codedebate review --branch main

# 分支对比审查（当前分支 vs develop）
codedebate review --branch develop

# 指定文件审查
codedebate review --files "src/auth.go,src/handler.go"

# ====== 输出控制 ======

# 审查但不发布评论到 PR/MR
codedebate review 42 --no-post

# 保存结果到 Markdown 文件
codedebate review 42 -o review.md

# 保存结果为 JSON 格式
codedebate review 42 -o result.json -f json

# ====== 高级用法 ======

# 指定配置文件
codedebate review 42 --config ./my-config.yaml

# 覆盖最大辩论轮数
codedebate review 42 --rounds 3

# 禁用收敛检测（强制跑满所有轮数）
codedebate review 42 --no-converge

# 跳过上下文收集（加快审查速度）
codedebate review 42 --skip-context

# 仅使用指定的审查者
codedebate review 42 --reviewers "claude,codex"

# 使用所有配置的审查者
codedebate review 42 --all

# ====== Webhook 服务器 ======

# 启动 GitLab MR 自动审查服务
codedebate serve --webhook-secret "my-secret"

# 指定监听地址和 GitLab 域名
codedebate serve --addr :9090 --gitlab-host gitlab.company.com --webhook-secret "my-secret"

# ====== 初始化 ======

# 生成默认配置文件到 ~/.codedebate/config.yaml
codedebate init
```

## Flag 参考表

### codedebate review

| Flag | 短写 | 类型 | 默认值 | 说明 |
|------|-------|------|--------|------|
| `--config` | `-c` | string | `~/.codedebate/config.yaml` | 配置文件路径 |
| `--rounds` | `-r` | int | 配置文件值 | 最大辩论轮数（覆盖配置） |
| `--output` | `-o` | string | - | 输出到文件 |
| `--format` | `-f` | string | `markdown` | 输出格式 (`markdown` / `json`) |
| `--local` | `-l` | bool | false | 审查本地未提交的变更 |
| `--branch` | `-b` | string | - | 审查当前分支 vs 指定基准分支 |
| `--files` | - | []string | - | 审查指定文件列表 |
| `--reviewers` | - | string | 全部 | 逗号分隔的审查者 ID |
| `--all` | `-a` | bool | false | 使用所有审查者 |
| `--no-converge` | - | bool | false | 禁用收敛检测 |
| `--show-tool-trace` | - | bool | false | 显示 analyzer/reviewer 的完整过程输出（默认隐藏） |
| `--verbose` | `-v` | bool | false | `--show-tool-trace` 的别名 |
| `--no-post` | - | bool | false | 不发布评论到 PR/MR |
| `--no-post-summary` | - | bool | false | 不发布总结 note 到 PR/MR |
| `--skip-context` | - | bool | false | 跳过上下文收集 |

### codedebate serve

| Flag | 短写 | 类型 | 默认值 | 说明 |
|------|-------|------|--------|------|
| `--config` | `-c` | string | `~/.codedebate/config.yaml` | 配置文件路径 |
| `--addr` | - | string | `:8080` | 监听地址（env: `CODEDEBATE_ADDR`） |
| `--webhook-secret` | - | string | - | GitLab webhook 密钥（必填，env: `CODEDEBATE_WEBHOOK_SECRET`） |
| `--max-concurrent` | - | int | `3` | 最大并发审查数 |
| `--gitlab-host` | - | string | `gitlab.com` | GitLab 域名（env: `GITLAB_HOST`） |

## 核心流程：codedebate review

```
runReview(cmd, args)
  |
  +-- 1. 加载配置 ----------------------------------------- config.LoadConfig(configPath)
  |     读取 YAML -> 解析结构体 -> 展开环境变量 -> 校验
  |     返回 *config.CodeDebateConfig
  |
  +-- 2. 检测平台 ----------------------------------------- detect.FromRemote(type, host)
  |     从 git remote URL 自动识别 GitHub / GitLab
  |     返回 platform.Platform 接口实例
  |
  +-- 3. 解析审查目标 ------------------------------------- resolveTarget(cmd, args, d, plat)
  |     +-- --local       -> resolveLocalTarget()          git diff HEAD (或 HEAD~1 HEAD)
  |     +-- --branch base -> resolveBranchTarget(base)     git diff base...branch
  |     +-- --files       -> 直接构建 reviewTarget         文件列表模式
  |     +-- PR/MR 编号    -> resolveMRTarget(input, plat)  通过 Platform API 获取 diff
  |     +-- PR/MR URL     -> resolveMRTarget(input, plat)  解析 URL + 获取 diff
  |     返回 *reviewTarget{Type, Label, Prompt, Repo}
  |
  +-- 4. 创建 Provider 实例 ------------------------------- provider.CreateProvider(model, cfg)
  |     +-- Reviewers[]   多个审查者，各自的 model + prompt
  |     +-- Analyzer      预分析器 (分析变更概况)
  |     +-- Summarizer    总结器 (生成最终结论)
  |     +-- ContextGatherer 上下文收集器 (可选，收集调用链/历史/文档)
  |
  +-- 5. 编排执行 ----------------------------------------- orchestrator.RunStreaming(ctx, label, prompt, d)
  |     阶段1: 并行执行上下文收集 + 代码预分析
  |     阶段2: 多轮辩论（每轮所有审查者并行执行 -> 可选收敛检测）
  |     阶段3: 收集总结 -> 生成最终结论 -> 提取结构化问题
  |     返回 *orchestrator.DebateResult
  |
  +-- 6. 显示结果 ----------------------------------------- display
  |     +-- d.FinalConclusion(result.FinalConclusion)      终端输出最终结论
  |     +-- d.IssuesTable(result.ParsedIssues)             以表格展示结构化问题
  |     +-- d.TokenUsage(result.TokenUsage, ...)           显示 Token 消耗统计
  |
  +-- 7. 发布评论/总结到 PR/MR (可选) --------------------- platform
  |     条件: !noPost && type=="pr" && len(issues)>0 && plat!=nil
  |     convertIssuesToPlatform(issues) -> []platform.IssueForComment
  |     plat.PostIssuesAsComments(prNum, platIssues, repo) -> ReviewResult
  |     输出: "Posted N comments (M inline, ...)"
  |     条件: !noPost && !noPostSummary && type=="pr" && finalConclusion!=""
  |     upsertSummaryNote(prNum, repo, "<!-- codedebate:summary -->", body)
  |
  +-- 8. 保存到文件 (可选) -------------------------------- saveOutput(path, format, result)
        --output 指定路径，支持 markdown / json 两种格式
```

## 跨模块数据流

展示数据如何在各模块之间流转和变换：

```
config.yaml -------> config.LoadConfig() --------> *config.CodeDebateConfig
                                                       |
                                                       | (提取 platform.Type, platform.Host)
                                                       v
git remote URL ----> detect.FromRemote() ---------> platform.Platform (github.Client / gitlab.Client)
                                                       |
                                                       | plat.GetDiff(mrID, repo)
                                                       | plat.GetInfo(mrID, repo)
                                                       v
PR/MR diff + info -> resolveTarget() -------------> *reviewTarget
                                                       |  .Type   = "pr" | "local" | "branch" | "files"
                                                       |  .Label  = "PR #42" | "MR !123" | "Local Changes"
                                                       |  .Prompt = 包含 diff 的完整审查提示词
                                                       |  .Repo   = "owner/repo"
                                                       v
CodeDebateConfig -------> provider.CreateProvider() ---> []provider.AIProvider
                     (为每个 reviewer/analyzer/       |
                      summarizer 创建实例)            |
                                                       v
所有输入 -----------> orchestrator.RunStreaming() --> *orchestrator.DebateResult
                     (多轮辩论 + 收敛检测 + 总结)       |
                                                       |  .FinalConclusion  最终结论 (string)
                                                       |  .ParsedIssues    结构化问题 ([]MergedIssue)
                                                       |  .TokenUsage      Token 用量 ([]TokenUsage)
                                                       |  .Messages        对话历史 ([]DebateMessage)
                                                       |  .ConvergedAtRound 收敛轮次 (*int)
                                                       v
                          +------------------------+---+---+---------------------+
                          |                        |       |                     |
                          v                        v       v                     v
               display.Display             platform.Post   saveOutput()   TokenUsage
               (CLI 终端输出)            IssuesAsComments   (写入文件)     (成本统计)
               - FinalConclusion        (发布到 PR/MR)     - markdown
               - IssuesTable                               - json
               - TokenUsage
```

### 关键数据结构变换

```
                    reviewTarget.Prompt
                          |
                          v
             +-----------+---+-----------+
             |                           |
             v                           v
  ContextGatherer.Gather()      Analyzer.ChatStream()
     |                              |
     v                              v
  *GatheredContext               analysis string
  - Summary                     (预分析报告)
  - RawReferences
  - AffectedModules
  - RelatedPRs
             |                           |
             +----------+  +------------+
                        |  |
                        v  v
              Reviewer[].ChatStream()   (每轮并行)
                        |
                        v
              []DebateMessage           (对话历史)
                        |
          +-------------+-------------+
          |                           |
          v                           v
  collectSummaries()         structurizeIssues()
          |                           |
          v                           v
  []DebateSummary              []MergedIssue
          |                    (去重、合并、排序)
          v                           |
  getFinalConclusion()                |
          |                           |
          v                           v
  FinalConclusion (string)    convertIssuesToPlatform()
                                      |
                                      v
                              []platform.IssueForComment
                                      |
                                      v
                              plat.PostIssuesAsComments()
                                      |
                                      v
                              platform.ReviewResult
                              - Posted, Inline, FileLevel
                              - Global, Failed, Skipped
```

## 核心流程：codedebate serve

```
runServe(cmd, args)
  |
  +-- 1. 加载配置 ----------------------------------------- config.LoadConfig(configPath)
  |
  +-- 2. 解析运行参数 ------------------------------------- flag > 环境变量 > 默认值
  |     +-- addr:           --addr          | CODEDEBATE_ADDR           | :8080
  |     +-- webhook-secret: --webhook-secret | CODEDEBATE_WEBHOOK_SECRET | (必填，无默认值)
  |     +-- gitlab-host:    --gitlab-host   | GITLAB_HOST          | gitlab.com
  |     +-- max-concurrent: --max-concurrent |                      | 3
  |
  +-- 3. 创建 server.Server ------------------------------ server.New(ServerConfig{...})
  |     ServerConfig {
  |       CodeDebateConfig:   *config.CodeDebateConfig  // CodeDebate 配置
  |       Addr:          ":8080"               // 监听地址
  |       WebhookSecret: "xxx"                 // Webhook 签名验证密钥
  |       MaxConcurrent: 3                     // 最大并发审查数
  |       GitLabHost:    "gitlab.com"          // GitLab 域名
  |     }
  |
  +-- 4. 启动 HTTP 服务（goroutine） ---------------------- srv.Start()
  |     POST /webhook -> 验证签名 -> 解析 MR 事件 -> 触发 codedebate review
  |
  +-- 5. 等待信号关闭 ------------------------------------ signal.Notify(SIGINT, SIGTERM)
        收到信号 -> srv.Shutdown(ctx)  (30s 超时优雅关闭)
```

## 核心流程：codedebate init

```
runInit(cmd, args)
  |
  +-- 1. 获取 home 目录 ---------------------------------- os.UserHomeDir()
  |
  +-- 2. 检查配置文件是否已存在 --------------------------- os.Stat(~/.codedebate/config.yaml)
  |     +-- 已存在 -> 提示 "Overwrite? (y/N):"
  |     +-- 用户选 N -> 中止
  |     +-- 用户选 Y -> 继续覆盖
  |
  +-- 3. 创建目录 ---------------------------------------- os.MkdirAll(~/.codedebate/, 0755)
  |
  +-- 4. 写入默认配置模板 --------------------------------- os.WriteFile(config.yaml, defaultConfig, 0644)
  |
  +-- 5. 输出提示 ---------------------------------------- "Config file created: ~/.codedebate/config.yaml"
```

### codedebate init 生成的默认配置模板

```yaml
# CodeDebate Configuration
# Multi-model adversarial code review

providers:
  claude-code:
    enabled: true
  codex-cli:
    enabled: true
  # openai:
  #   api_key: ${OPENAI_API_KEY}
  #   base_url: https://api.openai.com/v1  # optional, for Azure/Ollama compatibility

defaults:
  max_rounds: 5
  output_format: markdown
  check_convergence: true

analyzer:
  model: claude-code
  prompt: |
    You are a senior code analyst. Analyze the PR diff and provide:
    1. A summary of what the changes do
    2. Key areas of concern
    3. Potential impact on the codebase

    ## Suggested Review Focus
    - List specific areas reviewers should focus on

summarizer:
  model: claude-code
  prompt: |
    You are a senior engineering lead synthesizing code review feedback.
    Provide balanced, actionable conclusions.

reviewers:
  claude:
    model: claude-code
    prompt: |
      You are a thorough code reviewer focused on correctness, security,
      and maintainability. Review every changed file systematically.
  codex:
    model: codex-cli
    prompt: |
      You are a pragmatic code reviewer focused on performance, edge cases,
      and real-world reliability. Challenge assumptions and find issues others miss.
  # openai:
  #   model: gpt-4o
  #   prompt: |
  #     You are an experienced code reviewer focused on design patterns,
  #     API correctness, and code clarity. Provide actionable suggestions.

# Platform: auto-detected from git remote (github or gitlab)
# platform:
#   type: auto
#   host: gitlab.example.com  # for self-hosted GitLab

# Optional: context gathering
# contextGatherer:
#   enabled: true
#   callChain:
#     maxDepth: 2
#     maxFilesToAnalyze: 20
#   history:
#     maxDays: 30
#     maxPRs: 10
#   docs:
#     patterns: ["docs", "README.md", "ARCHITECTURE.md"]
#     maxSize: 50000
```
