# config - 配置管理

加载、解析和验证 CodeDebate 的 YAML 配置文件。支持环境变量替换、默认值设置和完整性校验，是整个多模型对抗式代码审查系统的配置入口。

## 文件说明

| 文件 | 说明 |
|------|------|
| `config.go` | 配置结构体定义、加载、环境变量展开、默认值设置、校验逻辑 |
| `config_test.go` | 配置加载、环境变量替换、校验规则的单元测试 |

## 完整配置示例

```yaml
# CodeDebate 配置文件示例
# 路径: ~/.codedebate/config.yaml

# ============================================================
# AI 提供者配置
# 定义可用的 AI 后端，审查者通过 model 字段引用
# ============================================================
providers:
  claude-code:
    enabled: true                          # 启用 Claude Code CLI 提供者
  codex-cli:
    enabled: true                          # 启用 Codex CLI 提供者
  openai:
    enabled: true
    api_key: "${OPENAI_API_KEY}"           # 支持 ${ENV_VAR} 语法引用环境变量
    base_url: "https://api.openai.com/v1"  # 可自定义 API 端点（Azure/Ollama 兼容）

# ============================================================
# 平台配置（可选，默认自动检测）
# ============================================================
platform:
  type: auto          # auto | github | gitlab
  host: ""            # 自托管 GitLab 域名, 如 "gitlab.company.com"

# ============================================================
# 全局默认参数
# ============================================================
defaults:
  max_rounds: 3                # 最大辩论轮数（必须 > 0）
  output_format: markdown      # 输出格式
  check_convergence: true      # 是否启用收敛检测（提前终止辩论）
  skip_permissions: true       # 跳过 CLI 权限提示（非交互模式需要）

# ============================================================
# 审查者配置
# 每个审查者使用独立的 AI 模型和系统提示词
# ============================================================
reviewers:
  security-reviewer:
    model: claude-code
    prompt: |
      You are a security-focused code reviewer. Look for:
      - SQL injection, XSS, CSRF vulnerabilities
      - Authentication and authorization issues
      - Sensitive data exposure
      - Input validation gaps
  performance-reviewer:
    model: gpt-4o
    prompt: |
      You are a performance-focused code reviewer. Look for:
      - O(n^2) or worse algorithmic complexity
      - Memory leaks and excessive allocations
      - Missing caching opportunities
      - Database N+1 query patterns

# ============================================================
# 预分析器
# 在辩论前分析代码变更，为审查者提供关注重点
# ============================================================
analyzer:
  model: claude-code
  prompt: |
    You are a senior code analyst. Analyze the PR diff and provide:
    1. A summary of what the changes do
    2. Key areas of concern
    3. Potential impact on the codebase

    ## Suggested Review Focus
    - List specific areas reviewers should focus on

# ============================================================
# 总结器
# 在辩论结束后综合各审查者意见，生成最终结论
# ============================================================
summarizer:
  model: gpt-4o
  prompt: |
    You are a senior engineering lead synthesizing code review feedback.
    Provide balanced, actionable conclusions.

# ============================================================
# 上下文收集器（可选）
# 在审查前自动收集相关代码上下文
# ============================================================
contextGatherer:
  enabled: true
  model: gpt-4o                # 用于上下文分析的模型（为空时使用 analyzer 的模型）
  history:
    maxDays: 30                # 回溯历史天数
    maxPRs: 5                  # 最多分析的历史 PR 数量
  docs:
    patterns: ["*.md", "docs/**"]   # 文档文件匹配模式
    maxSize: 10000                  # 单个文档最大字节数
```

## 类型层次结构

```
CodeDebateConfig
|
+-- Providers: map[string]CLIProviderConfig
|   |
|   +-- CLIProviderConfig
|       +-- Enabled: bool              // 是否启用该提供者
|       +-- APIKey: string             // API 密钥，支持 ${ENV_VAR} 替换
|       +-- BaseURL: string            // 自定义 API 端点（Azure/Ollama 兼容）
|
+-- Platform: *PlatformConfig          // 可选，nil 时自动检测
|   +-- Type: string                   // "auto"(默认) | "github" | "gitlab"
|   +-- Host: string                   // 自托管域名，如 "gitlab.company.com"
|
+-- Defaults: DefaultsConfig
|   +-- MaxRounds: int                 // 最大辩论轮数（必须 > 0）
|   +-- OutputFormat: string           // 输出格式，如 "markdown"
|   +-- CheckConvergence: bool         // 是否启用收敛检测
|   +-- SkipPermissions: *bool         // 跳过 CLI 权限提示（默认 true，指针类型用于区分未设置）
|
+-- Reviewers: map[string]ReviewerConfig
|   |
|   +-- ReviewerConfig
|       +-- Model: string              // 模型标识: "claude-code" | "codex-cli" | "gpt-4o" | "o1-*" | "o3-*"
|       +-- Prompt: string             // 系统提示词，引导审查角度和风格
|
+-- Analyzer: ReviewerConfig           // 预分析器（必填）
|   +-- Model: string                  // 分析器使用的模型
|   +-- Prompt: string                 // 分析器的系统提示词
|
+-- Summarizer: ReviewerConfig         // 总结器（必填）
|   +-- Model: string                  // 总结器使用的模型
|   +-- Prompt: string                 // 总结器的系统提示词
|
+-- ContextGatherer: *ContextGathererConfig   // 上下文收集器（可选，nil 表示禁用）
|   +-- Enabled: bool                  // 是否启用上下文收集
|   +-- Model: string                  // 用于分析的模型（空则使用 Analyzer 的模型）
|   +-- History: *HistoryConfig        // Git 历史记录分析配置
|   |   +-- MaxDays: int               // 回溯天数
|   |   +-- MaxPRs: int                // 最多分析的 PR 数
|   +-- Docs: *DocsConfig              // 文档收集配置
|       +-- Patterns: []string         // 匹配模式，如 ["*.md", "docs/**"]
|       +-- MaxSize: int               // 单个文档最大字节数
|
+-- Mock: bool                         // 全局模拟模式（测试用，所有模型替换为 MockProvider）
```

## 环境变量替换

配置文件中使用 `${VAR_NAME}` 语法引用环境变量，在加载时自动替换为 `os.Getenv("VAR_NAME")` 的值。支持替换的字段范围：

```yaml
# 提供者配置中的所有字符串字段
providers:
  openai:
    api_key: "${OPENAI_API_KEY}"           # -> os.Getenv("OPENAI_API_KEY")
    base_url: "${OPENAI_BASE_URL}"         # -> os.Getenv("OPENAI_BASE_URL")

# 审查者配置中的 model 和 prompt 字段
reviewers:
  custom:
    model: "${REVIEWER_MODEL}"             # -> os.Getenv("REVIEWER_MODEL")
    prompt: "${REVIEWER_PROMPT}"           # -> os.Getenv("REVIEWER_PROMPT")

# 分析器和总结器的 model 和 prompt 字段
analyzer:
  model: "${ANALYZER_MODEL}"               # -> os.Getenv("ANALYZER_MODEL")
  prompt: "${ANALYZER_PROMPT}"             # -> os.Getenv("ANALYZER_PROMPT")

summarizer:
  model: "${SUMMARIZER_MODEL}"             # -> os.Getenv("SUMMARIZER_MODEL")
  prompt: "${SUMMARIZER_PROMPT}"           # -> os.Getenv("SUMMARIZER_PROMPT")
```

替换由正则表达式 `\$\{(\w+)\}` 匹配，通过 `expandEnvVarsInConfig()` 遍历以下路径执行：

| 遍历路径 | 替换的字段 |
|----------|-----------|
| `cfg.Providers[*]` | `APIKey`, `BaseURL` |
| `cfg.Reviewers[*]` | `Model`, `Prompt` |
| `cfg.Analyzer` | `Model`, `Prompt` |
| `cfg.Summarizer` | `Model`, `Prompt` |

## 核心流程：LoadConfig

```
LoadConfig(configPath)
  |
  +-- 1. 确定配置文件路径 --------------------------------- GetConfigPath(customPath)
  |     +-- customPath != "" -> 使用自定义路径
  |     +-- customPath == "" -> ~/.codedebate/config.yaml (默认)
  |
  +-- 2. 读取文件 ----------------------------------------- os.ReadFile(path)
  |     失败 -> error: "config file not found: <path>"
  |
  +-- 3. 解析 YAML --------------------------------------- yaml.Unmarshal(data, &cfg)
  |     失败 -> error: "failed to parse config: <err>"
  |
  +-- 4. 展开环境变量 ------------------------------------- expandEnvVarsInConfig(&cfg)
  |     遍历所有 providers / reviewers / analyzer / summarizer 中的字符串字段
  |     将 ${VAR_NAME} 替换为 os.Getenv("VAR_NAME")
  |
  +-- 5. 设置默认值 --------------------------------------- applyDefaults
  |     +-- cfg.Defaults.SkipPermissions == nil -> 设为 true (指针)
  |
  +-- 6. 校验配置完整性 ----------------------------------- validateConfig(&cfg)
  |     +-- defaults.max_rounds > 0?
  |     +-- len(reviewers) >= 1?
  |     +-- 每个 reviewer 都有 model + prompt?
  |     +-- summarizer 有 model + prompt?
  |     +-- analyzer 有 model + prompt?
  |     任一校验失败 -> 返回对应 error
  |
  +-- 返回 *CodeDebateConfig, nil
```

## 校验规则

`validateConfig()` 执行以下校验，任一失败立即返回错误：

| 校验项 | 条件 | 错误信息 |
|--------|------|---------|
| 最大辩论轮数 | `cfg.Defaults.MaxRounds <= 0` | `config error: defaults.max_rounds must be > 0` |
| 审查者数量 | `len(cfg.Reviewers) == 0` | `config error: at least one reviewer must be defined` |
| 审查者模型 | `reviewers.<id>.Model == ""` | `config error: reviewers.<id> is missing a "model" field` |
| 审查者提示词 | `reviewers.<id>.Prompt` 为空白 | `config error: reviewers.<id> is missing a "prompt" field` |
| 总结器模型 | `cfg.Summarizer.Model == ""` | `config error: summarizer is missing a "model" field` |
| 总结器提示词 | `cfg.Summarizer.Prompt` 为空白 | `config error: summarizer is missing a "prompt" field` |
| 分析器模型 | `cfg.Analyzer.Model == ""` | `config error: analyzer is missing a "model" field` |
| 分析器提示词 | `cfg.Analyzer.Prompt` 为空白 | `config error: analyzer is missing a "prompt" field` |

校验顺序：`max_rounds` -> `reviewers 数量` -> `每个 reviewer` -> `summarizer` -> `analyzer`

`validateReviewerConfig(name, rc)` 是通用的单个角色校验函数，被 reviewers/summarizer/analyzer 共用。它使用 `strings.TrimSpace()` 去除 prompt 前后空白后再判断是否为空。

## 默认值

在 `LoadConfig()` 中，解析完 YAML 和展开环境变量后，对以下字段应用默认值：

| 字段 | 条件 | 默认值 | 说明 |
|------|------|--------|------|
| `Defaults.SkipPermissions` | 值为 `nil`（未在配置文件中设置） | `true` | CLI 提供者（claude-code/codex-cli）在非交互模式下需要跳过权限提示，否则会阻塞执行 |

其他字段的默认值由 `codedebate init` 生成的配置模板提供（参见上方完整配置示例），Go 零值（0、""、false、nil）在未配置时生效，会被 `validateConfig()` 捕获并报错。

## 模型标识对应关系

配置中的 `model` 字段决定使用哪个 AI 提供者，由 `provider.CreateProvider()` 工厂函数匹配：

| model 值 | 提供者 | 说明 |
|-----------|--------|------|
| `claude-code` | ClaudeCodeProvider | 调用 `claude` CLI 工具 |
| `codex-cli` | CodexCliProvider | 调用 `codex` CLI 工具 |
| `gpt-4o`, `gpt-*` | OpenAIProvider | 调用 OpenAI API，需要 `providers.openai.api_key` |
| `o1-*`, `o3-*` | OpenAIProvider | OpenAI 推理模型，需要 `providers.openai.api_key` |
| `mock*` | MockProvider | 测试用，返回预设响应 |
