# 配置说明

## 配置文件

配置文件位于 `~/.codedebate/config.yaml`，通过 `codedebate init` 生成默认配置。

```yaml
# 平台配置（可选）
platform:
  type: auto           # auto | github | gitlab（默认 auto，从 git remote 自动检测）
  host: ""             # 自托管 GitLab 地址，如 gitlab.company.com

# AI 提供者配置
providers:
  claude-code:
    enabled: true
  codex-cli:
    enabled: true
  openai:
    api_key: ${OPENAI_API_KEY}
    base_url: ""       # 可选，自定义 API 端点（支持 Azure OpenAI、Ollama 等兼容 API）

# 默认设置
defaults:
  max_rounds: 3          # 最大辩论轮数
  output_format: markdown # 输出格式
  check_convergence: true # 启用收敛检测

# 分析器：预分析变更，提取关注点
analyzer:
  model: claude-code
  prompt: |
    You are a senior code analyst...

# 总结者：判断收敛、生成最终结论、提取结构化问题
# ⚠️ 重要：summarizer 必须使用 API 模型（如 gpt-4o），不要用 CLI 模型（如 claude-code）。
# 原因：structurizeIssues 阶段要求 summarizer 严格输出 JSON 格式，
# CLI 模型（claude-code/codex-cli）会在响应中附加额外文本/工具调用格式，
# 导致 JSON 解析失败 → ParsedIssues 为空 → 无法自动发布评论到 PR/MR。
summarizer:
  model: gpt-4o   # 必须用 API 模型，不能用 claude-code/codex-cli
  prompt: |
    You are a senior engineering lead...

# 审查者：执行实际的代码审查
reviewers:
  claude:
    model: claude-code
    prompt: |
      You are a thorough code reviewer...
  codex:
    model: codex-cli
    prompt: |
      You are a pragmatic code reviewer...
  gpt4o:
    model: gpt-4o
    prompt: |
      You are an experienced code reviewer...

# 可选：上下文收集配置
contextGatherer:
  enabled: true
  callChain:
    maxDepth: 2            # 调用链最大深度
    maxFilesToAnalyze: 20  # 最大分析文件数
  history:
    maxDays: 30            # 查找历史 PR/MR 的天数范围
    maxPRs: 10             # 最大相关 PR/MR 数
  docs:
    patterns:              # 文档搜索模式
      - "docs"
      - "README.md"
    maxSize: 50000         # 单文件最大大小（字节）
```

## 环境变量

配置文件中支持 `${VAR}` 语法引用环境变量：

```yaml
providers:
  claude-code:
    api_key: ${ANTHROPIC_API_KEY}
  openai:
    api_key: ${OPENAI_API_KEY}
```

Webhook 服务相关环境变量：

| 变量 | 说明 | 默认值 |
|------|------|--------|
| `CODEDEBATE_ADDR` | 服务监听地址 | `:8080` |
| `CODEDEBATE_WEBHOOK_SECRET` | GitLab Webhook Secret | 无（必填） |
| `GITLAB_HOST` | 自托管 GitLab 地址 | `gitlab.com` |

## AI 提供者

| 提供者 | 类型 | 模型示例 | 说明 |
|--------|------|----------|------|
| **claude-code** | CLI | claude-code | 通过 `claude` CLI 调用，支持会话复用 |
| **codex-cli** | CLI | codex-cli | 通过 `codex` CLI 调用，支持会话复用 |
| **openai** | API | gpt-4o, o1-*, o3-* | 直接 HTTP 调用 OpenAI API，支持 SSE 流式 |
| **gemini** | API | gemini-2.5-pro 等 | 通过 Gemini API 调用 |

OpenAI 提供者支持自定义 `base_url`，兼容 Azure OpenAI、Ollama 等 OpenAI 兼容 API。

## 三个 AI 角色

| 角色 | 职责 | 时机 |
|------|------|------|
| **Analyzer（分析器）** | 预分析变更，提取摘要和建议关注点 | 与上下文收集并行，在第 1 轮之前 |
| **Reviewers（审查者）** | 执行代码审查和辩论，输出结构化问题 | 多轮辩论，第 2 轮起可看到他人反馈 |
| **Summarizer（总结者）** | 收敛判断、最终结论生成、问题结构化提取 | 每轮后（收敛检测）+ 辩论结束后 |
