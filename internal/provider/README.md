# provider - AI 提供者抽象层

CodeDebate 是一个多模型对抗式代码审查工具，`provider` 包定义了统一的 AI 提供者接口，并提供三种具体实现：Claude Code CLI、Codex CLI、OpenAI API。通过该抽象层，CodeDebate 的编排器（orchestrator）可以同时调度多个不同的 AI 模型，对同一份代码变更进行独立审查，再交叉辩论，最终汇总出高质量的审查意见。

## 文件说明

| 文件 | 说明 |
|------|------|
| `provider.go` | 核心接口定义：`AIProvider`、`SessionProvider`、`Message`、`ChatOptions`、`SetCwdIfSupported` |
| `cliprovider.go` | CLI 提供者共享的会话管理器 `CliSessionHelper`，含原子快照和 prompt 构建 |
| `claudecode.go` | Claude Code CLI 实现：通过 `os/exec` 调用 `claude` 命令，支持 JSON/stream-json 输出，含工具调用追踪 |
| `codexcli.go` | Codex CLI 实现：通过 `os/exec` 调用 `codex` 命令，支持 JSONL 事件流 |
| `openai.go` | OpenAI API 实现：直接 `net/http` 调用 Chat Completions API，支持 SSE 流式，支持推理深度控制 |
| `promptfile.go` | 大 prompt 处理：超过阈值（默认 100KB）时写入临时文件，CLI 通过文件读取指令获取内容 |
| `retry.go` | 泛型指数退避重试机制，自动识别瞬时错误 |
| `factory.go` | 工厂函数 `CreateProvider` + `MockProvider` 测试桩 |

## 核心接口

```go
// Message 表示一条对话消息。
type Message struct {
    Role    string // "system" | "user" | "assistant"
    Content string
}

// ChatOptions 控制单次调用的行为选项。
type ChatOptions struct {
    DisableTools bool // 禁用 CLI 工具调用（用于纯 JSON 输出场景）
    MaxTokens    int  // 最大输出 token 数（0 表示使用模型默认值）
}

// AIProvider 是所有 AI 提供者的核心接口。
// CodeDebate 通过此接口与不同的 AI 后端交互。
type AIProvider interface {
    Name() string
    // Chat 发送消息并等待完整响应（同步）。
    Chat(ctx context.Context, messages []Message, systemPrompt string, opts *ChatOptions) (string, error)
    // ChatStream 发送消息并以流式方式返回响应片段（异步）。
    // 返回两个 channel：chunks 接收文本片段，errs 接收错误。
    ChatStream(ctx context.Context, messages []Message, systemPrompt string) (<-chan string, <-chan error)
}

// SessionProvider 在 AIProvider 基础上扩展了会话管理能力。
// CLI 提供者通过会话复用来避免每次调用都发送完整历史，节省 token。
type SessionProvider interface {
    AIProvider
    StartSession(name string)        // 开始新会话
    EndSession()                     // 结束当前会话
    SessionID() string               // 获取当前会话 ID（由 CLI 响应返回）
    IsFirstMessage() bool            // 是否为会话中的第一条消息
    MarkMessageSent()                // 标记已发送一条消息
    ShouldSendFullHistory() bool     // 是否需要发送完整历史（首条消息或无会话时）
}

// SetCwdIfSupported 在 provider 支持 SetCwd 时设置工作目录。
// 通过鸭子类型检测 provider 是否实现了 cwdSetter 接口，
// 用于让 CLI provider 在正确的项目目录下执行审查。
func SetCwdIfSupported(p AIProvider, cwd string)
```

## Message 数据结构示例

```go
// 对话消息示例 — CodeDebate 审查场景下的典型消息序列
messages := []Message{
    {Role: "system", Content: "You are a security-focused code reviewer..."},
    {Role: "user", Content: "Please review this diff:\n```diff\n+func handler(w http.ResponseWriter, r *http.Request) {\n+    query := r.URL.Query().Get(\"id\")\n+    db.Exec(\"SELECT * FROM users WHERE id = \" + query)\n..."},
    {Role: "assistant", Content: "I've identified several security concerns:\n1. SQL injection vulnerability..."},
}

// 在多轮辩论中，消息会不断追加：
// orchestrator 将其他 reviewer 的意见作为新的 user 消息加入
messages = append(messages, Message{
    Role:    "user",
    Content: "Other reviewer's opinion:\n> The SQL injection is critical...\nDo you agree?",
})
```

## ChatOptions 使用示例

```go
// 标准调用（默认行为）
response, err := provider.Chat(ctx, msgs, systemPrompt, nil)

// 禁用工具调用（用于获取纯 JSON 输出，如结构化问题提取）
opts := &ChatOptions{DisableTools: true}
response, err := provider.Chat(ctx, msgs, systemPrompt, opts)

// 限制最大输出 token 数
opts := &ChatOptions{MaxTokens: 4096}
response, err := provider.Chat(ctx, msgs, systemPrompt, opts)
```

## 工厂路由

`CreateProvider` 根据模型名称匹配规则，创建对应的 `AIProvider` 实例：

```
CreateProvider(model, modelName, reasoningEffort, cfg)
  |
  |-  cfg.Mock == true                    -> MockProvider        (全局 mock 模式，所有模型)
  |-  model == "claude-code"              -> ClaudeCodeProvider  (claude CLI 子进程)
  |     设置: skipPermissions, modelName, promptSizeThreshold
  |-  model == "codex-cli"                -> CodexCliProvider    (codex CLI 子进程)
  |     设置: skipPermissions, modelName, promptSizeThreshold
  |-  strings.HasPrefix(model, "gpt-")    -> OpenAIProvider      // gpt-4o, gpt-4o-mini, gpt-5.2, ...
  |-  strings.HasPrefix(model, "o1-")     -> OpenAIProvider      // o1-preview, o1-mini
  |-  strings.HasPrefix(model, "o3-")     -> OpenAIProvider      // o3-mini
  |-  strings.HasPrefix(model, "o4-")     -> OpenAIProvider      // o4-mini
  |     设置: reasoningEffort
  |-  strings.HasPrefix(model, "mock")    -> MockProvider        // 测试桩
  '-- default                             -> error
```

**工厂参数说明**：
- `model`：提供者标识（如 `"claude-code"`、`"gpt-4o"`），决定创建哪种 Provider
- `modelName`：底层模型名称（如 `"claude-sonnet-4-5-20250514"`），对 CLI 提供者通过 `--model` 参数传递
- `reasoningEffort`：推理深度（`"low"` | `"medium"` | `"high"` 等），仅 OpenAI 推理模型有效
- `cfg`：CodeDebate 配置，包含 API key、base_url、skip_permissions、prompt_size_threshold 等

**配置读取**：对于 OpenAI 类模型，工厂函数从 `cfg.Providers["openai"]` 中读取 `api_key` 和可选的 `base_url`（支持 Azure OpenAI / Ollama 等兼容 API）。对于 CLI 类模型，读取 `cfg.Defaults.SkipPermissions` 控制是否跳过权限确认，读取 `cfg.Defaults.PromptSizeThreshold` 控制大 prompt 阈值。

## 大 Prompt 处理（promptfile.go）

当 prompt 内容超过阈值（默认 100KB）时，直接通过 stdin 传递可能导致性能问题或超出系统限制。`PreparePromptForCli` 提供了透明的大 prompt 处理机制：

```
PreparePromptForCli(prompt, threshold)
  |
  |- len(prompt) <= threshold  -> 直接返回原始 prompt
  '- len(prompt) > threshold   -> 写入临时文件，返回文件读取指令
       |
       |- 创建临时文件 /tmp/codedebate-prompt-*.txt
       |- 将 prompt 写入文件
       '- 返回: "Read the file /tmp/codedebate-prompt-xxx.txt for the full review context..."
```

调用者必须 `defer prepared.Cleanup()` 确保临时文件被删除。两个 CLI Provider（Claude Code 和 Codex）都使用此机制。

## Claude Code Provider

### CLI 调用方式

```bash
# 首次调用（新会话）— 传入 system prompt，通过 stdin 发送完整 prompt
echo "Please review this diff..." | claude -p - \
  --strict-mcp-config \
  --output-format stream-json \
  --model claude-sonnet-4-5-20250514 \
  --system-prompt "You are a security reviewer..." \
  --dangerously-skip-permissions

# 后续调用（复用会话）— 使用 --resume 续传，只发送增量消息
echo "Here are other reviewers' opinions..." | claude -p - \
  --strict-mcp-config \
  --output-format stream-json \
  --model claude-sonnet-4-5-20250514 \
  --resume <session-id> \
  --dangerously-skip-permissions
```

**参数说明**：
- `-p -`：pipe 模式，从 stdin 读取 prompt（避免参数过长导致 E2BIG 错误）
- `--strict-mcp-config`：不加载用户配置的 MCP servers，节省内存和启动时间
- `--output-format json`：非流式模式，输出完整 JSON 数组
- `--output-format stream-json`：流式模式，输出 JSONL（每行一个事件）
- `--model <name>`：指定底层模型名称（由工厂的 `modelName` 参数传入）
- `--resume <id>`：复用已有会话，CLI 记住之前的上下文
- `--system-prompt`：系统提示词，仅首次调用时传入
- `--dangerously-skip-permissions`：跳过交互式权限确认（非交互模式必须）

### 环境变量过滤

`filterClaudeEnv()` 从子进程的环境变量中移除 `CLAUDECODE=...`，避免嵌套调用时 Claude CLI 报错 "cannot launch inside another session"。当 CodeDebate 本身在 Claude Code 环境中运行时（如在 Claude Code 终端中执行），子进程会继承这个环境变量，必须清除。

### JSON 响应格式

```
非流式模式（--output-format json）：输出为 JSON 数组

  [
    {"type":"system", ...},
    {"type":"assistant", "message":{"content":[{"type":"text","text":"Review..."}]}, "session_id":"abc-123"},
    {"type":"result", "subtype":"success", "result":"Complete review text...", "session_id":"abc-123"}
  ]

流式模式（--output-format stream-json）：逐行 JSONL 输出

  {"type":"assistant","message":{"content":[{"type":"text","text":"Looking at the diff..."}]},"session_id":"abc-123"}
  {"type":"assistant","message":{"content":[{"type":"text","text":"Looking at the diff...\nI found a bug"},{"type":"tool_use","id":"tool_1","name":"Read","input":{"file_path":"/src/main.go"}}]},"session_id":"abc-123"}
  {"type":"result","subtype":"success","result":"Complete review...","session_id":"abc-123"}
```

**事件类型**：
- `system`：系统初始化消息
- `assistant`：AI 生成的消息，文本在 `message.content[].text` 中，工具调用在 `message.content[].type == "tool_use"` 中
- `result`：最终结果，完整文本在 `result` 字段中
- 所有事件都可能携带 `session_id`，Provider 从中提取并保存

### 为什么同时需要 `assistant` 和 `result` 两种类型

一次对话中可能产生**多条** `assistant` 消息，特别是涉及工具调用时：

```
assistant: 我来帮你读取文件          ← 文本
assistant: [tool_use: Read file]     ← 调用工具
assistant: [tool_result: 文件内容]    ← 工具返回
assistant: 我来修改第10行            ← 文本
assistant: [tool_use: Edit file]     ← 又调用工具
assistant: [tool_result: 修改成功]    ← 工具返回
assistant: 修改完成了                 ← 文本
```

而 `result` 是整个交互结束后的**唯一一条**汇总消息：

```json
{"type": "result", "subtype": "success", "result": "修改完成了", "session_id": "abc-123", "cost_usd": 0.003, "total_tokens": 1500, "is_error": false}
```

两者职责不同：

| 维度 | `assistant` | `result` |
|------|-------------|----------|
| 数量 | 一次对话中可能有多条 | 始终只有一条，在流的末尾 |
| 内容 | 中间过程的文本片段、工具调用 | 最终完整结果文本 |
| 元数据 | 仅携带 `session_id` | 携带 `session_id`、费用、token 用量、错误状态等 |
| 用途 | 流式展示中间过程 | 提供终止信号 + 最终答案提取 + 聚合元数据 |

简单类比：`assistant` 消息相当于 HTTP chunked streaming 的数据块，`result` 相当于最终响应摘要。CodeDebate 的 `ClaudeCodeProvider` 在解析时，从 `assistant` 事件中提取流式文本片段，从 `result` 事件中提取最终完整结果和 `session_id`。

### 流式模式的增量文本提取与工具调用追踪

流式模式下，每条 `assistant` 事件包含的是**截至目前的完整文本**，而非增量。Provider 通过 `claudeStreamState` 跟踪状态，使用 `diffAssistantText` 提取增量文本：

```
assistant 事件 1: content=[{text:"Looking"}]
  -> diffAssistantText("", "Looking") = "Looking"  (发送 "Looking")

assistant 事件 2: content=[{text:"Looking at the diff"}]
  -> diffAssistantText("Looking", "Looking at the diff") = " at the diff"  (只发送增量)

assistant 事件 3: content=[{text:"Looking at the diff"}, {tool_use: Read, input:{file_path:"/src/main.go"}}]
  -> 文本无变化，不发送
  -> 检测到新的 tool_use，发送: "\n[tool] Read: /src/main.go\n"
```

**工具调用追踪**：当 Claude 在审查过程中调用工具（如读取文件、搜索代码），Provider 会格式化工具信息并发送到 chunks channel，让用户看到审查过程：

```
[tool] Read: /src/main.go
[tool] Grep: pattern "TODO" in src/
[tool] Bash: git log --oneline -5
```

`summarizeClaudeToolInput` 从工具输入中提取关键信息（如 `command`、`file_path`、`pattern` 等常见字段），截断到 160 字符以保持可读性。通过 `seenToolUseIDs` 去重，避免同一工具调用被重复输出。

### 核心流程

```
Chat() / ChatStream()
  |
  |- 1. Snapshot() 原子读取会话状态（解决 TOCTOU 竞态）
  |- 2. 构建 prompt
  |     |- ShouldSendFull() == true  -> BuildPrompt()（系统提示 + 全部消息历史）
  |     '- ShouldSendFull() == false -> BuildPromptLastOnly()（仅最后一条 user 消息）
  |- 3. PreparePromptForCli() 处理大 prompt（超过阈值写临时文件）
  |- 4. buildArgs() 构建 CLI 参数
  |     |- 首次：--strict-mcp-config --system-prompt "..." --output-format stream-json [--model ...]
  |     '- 续传：--strict-mcp-config --resume <session-id> --output-format stream-json [--model ...]
  |- 5. filterClaudeEnv() 清理 CLAUDECODE 环境变量
  |- 6. os/exec 执行 claude 命令，stdin 传入 prompt
  |- 7. 解析输出
  |     |- Chat:       json.Unmarshal JSON 数组 -> 提取 session_id + result text
  |     '- ChatStream: 逐行读取 JSONL -> diffAssistantText 增量提取 + 工具调用追踪 -> 发送到 channel
  |- 8. SetSessionID() 保存会话 ID，MarkMessageSent() 标记非首次
  '- 9. WithRetry 包装（仅 Chat 同步模式），Cleanup() 清理临时文件
```

## Codex CLI Provider

### CLI 调用方式

```bash
# 首次调用（新会话）— 通过 stdin 发送完整 prompt（系统提示拼在 prompt 内）
echo "System: You are a security reviewer...\n\nuser: Please review..." | codex exec \
  --json \
  --model o3-mini \
  --dangerously-bypass-approvals-and-sandbox \
  -

# 后续调用（复用会话）— 使用 exec resume 子命令续传
echo "Here are other reviewers' opinions..." | codex exec resume <thread-id> \
  --json \
  --model o3-mini \
  --dangerously-bypass-approvals-and-sandbox \
  -
```

**参数说明**：
- `exec`：执行子命令，末尾的 `-` 表示从 stdin 读取 prompt
- `exec resume <thread-id>`：会话续传是 `exec` 的子命令，thread-id 是位置参数
- `--json`：输出 JSONL 格式（每行一个事件），Codex 只有这一种结构化输出格式
- `--model <name>`：指定底层模型名称（由工厂的 `modelName` 参数传入）
- `--dangerously-bypass-approvals-and-sandbox`：跳过权限确认和沙箱限制
- **无 `--system-prompt` 参数**：系统提示词拼进 stdin 的 prompt 一起发送

### JSONL 响应格式

```
{"type":"thread.started","thread_id":"thread_xyz"}
{"type":"item.completed","item":{"type":"agent_message","text":"Looking at the diff..."}}
{"type":"item.completed","item":{"type":"agent_message","text":"I found a SQL injection..."}}
```

**事件类型**：
- `thread.started`：线程启动事件，包含 `thread_id`，Provider 从中提取并保存会话 ID
- `item.completed`（`item.type == "agent_message"`）：AI 生成的消息，文本在 `item.text` 中
- **无 `result` 终止事件**：进程退出即表示响应结束

### 核心流程

```
Chat() / ChatStream()
  |
  |- 1. Snapshot() 原子读取会话状态（解决 TOCTOU 竞态）
  |- 2. 构建 prompt
  |     |- sessionEnabled && !ShouldSendFull() -> BuildPromptLastOnly()（仅最后一条 user 消息）
  |     '- 否则                                -> BuildPrompt()（系统提示拼在 prompt 内 + 全部消息历史）
  |- 3. PreparePromptForCli() 处理大 prompt（超过阈值写临时文件）
  |- 4. buildArgs() 构建 CLI 参数
  |     |- 首次：exec --json [--model ...] -
  |     '- 续传：exec resume <thread-id> --json [--model ...] -
  |- 5. os/exec 执行 codex 命令，stdin 传入 prompt
  |- 6. 解析输出
  |     |- Chat:       进程结束后一次性解析 JSONL -> 提取 thread_id + item.text
  |     '- ChatStream: 逐行读取 JSONL -> 解析 thread.started/item.completed 事件 -> 发送到 channel
  |- 7. SetSessionID() 保存 thread_id，MarkMessageSent() 标记非首次
  '- 8. WithRetry 包装（仅 Chat 同步模式），Cleanup() 清理临时文件
```

## Claude Code 与 Codex CLI 的调用差异

两者整体架构相同（子进程 + stdin 传 prompt + stdout 读结果），但在 CLI 接口设计和事件协议上有明显差异：

### 命令结构

```bash
# Claude Code 用 flags
claude -p - --strict-mcp-config --resume <session-id> --output-format stream-json --model <name>

# Codex CLI 用子命令
codex exec resume <thread-id> --json --model <name> -
```

Claude Code 的会话续传是 `--resume` flag，Codex 是 `exec resume` 子命令 + 位置参数。

### 系统提示词传递

Claude Code 有专门的 `--system-prompt` 参数，系统提示和用户消息分开传递，CLI 内部区分两者的角色。Codex CLI 没有这个参数，CodeDebate 通过 `BuildPrompt()` 将系统提示拼进 stdin 的 prompt 文本中：

```
// BuildPrompt 输出：
"[session-name]\n\nSystem: You are a security reviewer...\n\nuser: Please review..."
```

### 输出格式

Claude Code 支持两种格式：
- `--output-format json` → 非流式，完整 JSON 数组 `[event, event, ...]`
- `--output-format stream-json` → 流式 JSONL

Codex CLI 只有一种：`--json` → 始终输出 JSONL。同步 `Chat()` 也是等进程结束后解析 JSONL，没有 JSON 数组模式。

### 事件协议

| 维度 | Claude Code | Codex CLI |
|------|------------|-----------|
| 会话 ID 来源 | 任意事件的 `session_id` 字段 | `thread.started` 事件的 `thread_id` |
| 文本位置 | `message.content[].text`（嵌套数组） | `item.text`（扁平结构） |
| 终止信号 | `result` 事件（携带完整结果 + 元数据） | 无，进程退出即结束 |
| 错误指示 | `result.is_error` 字段 | CLI 退出码非零 |
| 流式文本 | 增量提取（diffAssistantText） | 直接发送 item.text |
| 工具追踪 | 格式化 tool_use 块并发送 | 无 |

### 共享层：CliSessionHelper

虽然 CLI 接口差异不小，但会话管理的核心逻辑是复用的。两个 Provider 都内嵌 `CliSessionHelper`，共享以下能力：

```
CliSessionHelper（共享）
  ├─ Start() / End()          — 会话生命周期
  ├─ Snapshot()               — 原子读取状态（解决 TOCTOU）
  ├─ BuildPrompt()            — 构建完整 prompt
  ├─ BuildPromptLastOnly()    — 构建增量 prompt
  ├─ SetSessionID()           — 保存会话 ID（无论是 session_id 还是 thread_id）
  └─ MarkMessageSent()        — 标记非首次

PreparePromptForCli（共享）      — 大 prompt 写临时文件处理

ClaudeCodeProvider（独有）         CodexCliProvider（独有）
  ├─ buildArgs()                    ├─ buildArgs()
  ├─ filterClaudeEnv()              ├─ sessionEnabled 控制
  ├─ handleStreamEvent()            ├─ 内联事件解析
  ├─ emitAssistantMessage()         └─ 仅解析 JSONL
  ├─ diffAssistantText()
  ├─ formatToolUseChunk()
  └─ 解析 JSON 数组 / JSONL
```

每个 Provider 只需要实现自己特有的部分：CLI 参数构建和事件解析。会话状态管理、prompt 构建、并发安全、大 prompt 处理这些通用逻辑全部委托给共享模块。

## OpenAI Provider

### API 请求/响应格式

```
POST {baseURL}/chat/completions

请求体（非流式）:
  {
    "model": "gpt-4o",
    "messages": [
      {"role": "system", "content": "You are a code reviewer..."},
      {"role": "user", "content": "Review this diff:\n```diff\n+func..."}
    ],
    "stream": false,
    "reasoning_effort": "high",
    "max_completion_tokens": 4096
  }

响应体（非流式）:
  {
    "choices": [
      {"message": {"role": "assistant", "content": "I found a bug..."}}
    ]
  }
```

```
请求体（流式）:
  {
    "model": "gpt-4o",
    "messages": [...],
    "stream": true,
    "reasoning_effort": "high"
  }

SSE 流式响应（每行一个 Server-Sent Event）:
  data: {"choices":[{"delta":{"content":"I found"}}]}
  data: {"choices":[{"delta":{"content":" a potential"}}]}
  data: {"choices":[{"delta":{"content":" SQL injection"}}]}
  data: [DONE]
```

### MaxTokens 参数适配

不同 OpenAI 模型系列使用不同的 token 限制参数名：

| 模型系列 | 参数名 | 示例 |
|---------|--------|------|
| gpt-4o, gpt-4o-mini | `max_tokens` | `"max_tokens": 4096` |
| o1-*, o3-*, o4-* | `max_completion_tokens` | `"max_completion_tokens": 4096` |
| gpt-4.1-*, gpt-5-* | `max_completion_tokens` | `"max_completion_tokens": 4096` |

`usesMaxCompletionTokens(model)` 函数根据模型前缀判断使用哪个参数。当 `ChatOptions.MaxTokens > 0` 时，自动选择正确的请求字段。

### 推理深度控制

`reasoningEffort` 参数控制推理模型（o1/o3/o4 系列）的思考深度。由工厂函数从配置中传入，通过 `reasoning_effort` 字段发送给 API。

### 核心流程

```
Chat() / ChatStream()
  |
  |- 1. buildMessages()：将 systemPrompt 前置 + 合并 messages
  |     [system prompt, user msg1, assistant msg1, user msg2, ...]
  |- 2. 构建请求体
  |     |- 设置 model、messages、stream
  |     |- 设置 reasoning_effort（如果配置了推理深度）
  |     |- 根据模型类型选择 max_tokens 或 max_completion_tokens
  |- 3. POST /chat/completions
  |     |- Chat:       stream: false -> json.Unmarshal 完整响应
  |     '- ChatStream: stream: true  -> bufio.Scanner 逐行解析 SSE data: 事件
  |- 4. 支持自定义 baseURL（Azure OpenAI / Ollama 等兼容 API）
  '- 5. WithRetry 包装（仅 Chat 同步模式）
```

**与 CLI Provider 的区别**：OpenAI Provider 是无状态的，每次调用发送完整消息历史，不维护会话。因此它只实现 `AIProvider` 接口，不实现 `SessionProvider`。

## 为什么需要 SessionProvider

### 问题：CLI 提供者的多轮对话成本爆炸

CodeDebate 的辩论式审查流程中，每个审查者需要进行**多轮对话**（预分析 → 第1轮独立审查 → 第2轮辩论 → ... → 最终总结）。

对于 OpenAI API 这种**无状态**提供者，每次调用必须发送完整的消息历史：

```
第1轮: [system_prompt + diff + 分析结果]                    → 发送 ~5K tokens
第2轮: [system_prompt + diff + 分析结果 + 第1轮所有人的意见]  → 发送 ~15K tokens
第3轮: [system_prompt + diff + 分析结果 + 第1轮 + 第2轮...]  → 发送 ~30K tokens
总结:  [system_prompt + diff + 全部历史 + 总结请求]           → 发送 ~40K tokens
                                                           ──────────────
                                                           累计输入 ~90K tokens
```

问题在于：每一轮都在**重复发送之前已经发过的内容**，token 费用随轮次线性增长。

### 解决：CLI 的 `--resume` 会话复用

Claude Code CLI 和 Codex CLI 都支持**会话续传**（`--resume <session_id>`）。CLI 进程内部会记住之前的对话上下文，后续调用只需要发送**增量消息**：

```
第1轮: [system_prompt + diff + 分析结果]     → 发送 ~5K tokens（首次，完整发送）
第2轮: [仅本轮其他审查者的新意见]              → 发送 ~3K tokens（增量）
第3轮: [仅本轮其他审查者的新意见]              → 发送 ~3K tokens（增量）
总结:  [总结请求]                             → 发送 ~0.5K tokens（增量）
                                             ──────────────
                                             累计输入 ~11.5K tokens（节省 ~87%）
```

### SessionProvider 的设计

`SessionProvider` 在 `AIProvider` 基础上扩展了会话管理能力，让编排器能够**透明地**利用 CLI 的会话复用：

```go
type SessionProvider interface {
    AIProvider                        // 继承基础的 Chat/ChatStream 能力
    StartSession(name string)         // 开始新会话
    EndSession()                      // 结束会话，释放资源
    SessionID() string                // 获取 CLI 返回的会话 ID
    IsFirstMessage() bool             // 是否为首条消息（决定是否发送完整历史）
    MarkMessageSent()                 // 标记已发送，后续走增量模式
    ShouldSendFullHistory() bool      // 综合判断：无会话或首条消息 → true
}
```

**关键设计决策**：不是所有提供者都需要会话。OpenAI API 是无状态的，天然不支持会话续传。因此 `SessionProvider` 是一个**可选接口**，编排器通过类型断言来判断是否启用会话模式：

```go
// 只有支持会话的提供者才启动会话
if sp, ok := reviewer.Provider.(provider.SessionProvider); ok {
    sp.StartSession("CodeDebate | PR #42 | reviewer:security")
}
```

### 编排器如何使用 SessionProvider

在 `orchestrator.go` 中，SessionProvider 影响三个关键流程：

**1. 会话生命周期管理**

```
RunStreaming()
  ├─ startSessions(label)      ← 为所有 CLI 提供者启动会话
  │    ├─ reviewer1.StartSession("CodeDebate | PR #42 | reviewer:security")
  │    ├─ reviewer2.StartSession("CodeDebate | PR #42 | reviewer:logic")
  │    ├─ analyzer.StartSession("CodeDebate | PR #42 | analyzer")
  │    └─ summarizer.StartSession("CodeDebate | PR #42 | summarizer")
  │
  ├─ 阶段1: 预分析
  ├─ 阶段2: 多轮辩论
  ├─ 阶段3: 总结
  │
  └─ defer endAllSessions()    ← 无论成功失败，都清理所有会话
```

**2. 消息构建策略分叉**

编排器根据是否有 SessionProvider 选择不同的消息构建策略：

```
buildMessages(reviewerID)
  │
  ├─ 第1轮（所有提供者相同）:
  │    └─ buildFirstRoundMessages()  → 完整的审查 prompt
  │
  └─ 第2轮及之后（策略分叉）:
       │
       ├─ 有 SessionProvider:
       │    └─ buildSessionDebateMessages()
       │         → 只发送本轮其他审查者的新消息（~3K tokens）
       │         → CLI 通过 --resume 自动拥有之前的上下文
       │
       └─ 无 SessionProvider（如 OpenAI）:
            └─ buildFullContextDebateMessages()
                 → 发送完整上下文 + 所有历史消息（~30K tokens）
                 → 每次从头构建完整对话
```

**3. CLI 参数的自动适配**

在 `ClaudeCodeProvider` 内部，会话状态决定了 CLI 的调用方式：

```
Chat() / ChatStream()
  │
  ├─ snap := session.Snapshot()     ← 原子读取会话状态
  │
  ├─ snap.ShouldSendFull() == true（首次或无会话）:
  │    ├─ prompt = BuildPrompt()    ← 系统提示 + 全部消息
  │    └─ args: --system-prompt "..." --output-format stream-json
  │
  └─ snap.ShouldSendFull() == false（续传）:
       ├─ prompt = BuildPromptLastOnly()  ← 仅最后一条 user 消息
       └─ args: --resume <session-id> --output-format stream-json
```

### 对比总结

| 维度 | 无 SessionProvider（OpenAI） | 有 SessionProvider（Claude/Codex CLI） |
|------|------|------|
| 状态 | 无状态，每次发完整历史 | 有状态，CLI 记住上下文 |
| 第 N 轮输入量 | O(N) — 随轮次线性增长 | O(1) — 只发增量消息 |
| 3轮辩论总 token | ~90K | ~11.5K（节省 ~87%） |
| 延迟 | 随历史增长而增加 | 基本恒定 |
| 接口 | 仅实现 `AIProvider` | 实现 `AIProvider` + `SessionProvider` |
| 消息构建 | `buildFullContextDebateMessages` | `buildSessionDebateMessages` |

## SessionProvider 生命周期

```
StartSession("CodeDebate | PR #42 | reviewer:security")
  |
  |-  session.Start(name)
  |     '- sessionID = "", firstMessage = true, sessionName = name
  |
  |-  第 1 轮调用: Chat(ctx, messages, systemPrompt, nil)
  |     |- Snapshot() -> {ID:"", FirstMessage:true}
  |     |- ShouldSendFull() == true  -> 发送完整历史 + 系统提示
  |     |- PreparePromptForCli()     -> 大 prompt 处理
  |     |- CLI 响应包含 session_id -> SetSessionID("abc-123")
  |     '- MarkMessageSent()        -> firstMessage = false
  |
  |-  第 2 轮调用: Chat(ctx, newMessages, systemPrompt, nil)
  |     |- Snapshot() -> {ID:"abc-123", FirstMessage:false}
  |     |- ShouldSendFull() == false -> 仅发送最后一条 user 消息
  |     '- CLI 使用 --resume abc-123 续传会话
  |
  |-  第 N 轮调用: ...（同第 2 轮）
  |
  '-- EndSession()
        '- session.End()
             '- sessionID = "", firstMessage = true, sessionName = ""
```

## 会话快照与 TOCTOU 竞态问题

`CliSessionHelper` 使用 `Snapshot()` 方法解决 TOCTOU（Time Of Check To Time Of Use）竞态问题。

### 问题：分两次读取会话状态

如果分别调用 `SessionID()` 和 `IsFirstMessage()`，两次加锁之间可能有其他 goroutine 修改了状态：

```
goroutine A (Chat)          goroutine B (辩论结束，清理会话)
-----------------           -----------------------------------
SessionID()
  lock()
  读 id = "abc123"
  unlock()
                            End()
                              lock()
                              id = ""
                              firstMessage = true
                              unlock()
IsFirstMessage()
  lock()
  读 first = true
  unlock()

结果：id="abc123" + first=true -> 不一致！
id 说"有旧会话"，first 说"是新会话"，两个值来自不同时刻。
后续用 --resume abc123 续传会报错，因为会话已经被 End() 清掉了。
```

### 解决：Snapshot() 一次性读取

```go
func (h *CliSessionHelper) Snapshot() SessionSnapshot {
    h.mu.Lock()
    defer h.mu.Unlock()
    return SessionSnapshot{
        ID:           h.sessionID,    // 在同一把锁下
        FirstMessage: h.firstMessage, // 一次性读完
        Name:         h.sessionName,
    }
}
```

```
goroutine A (Chat)          goroutine B
-----------------           ----------
Snapshot()
  lock()
  读 id = "abc123"          End() -> 想加锁？被 A 占着，等！
  读 first = false
  unlock()                  -> 现在才能执行 End()

结果：id="abc123" + first=false -> 一致！
```

**原则：如果多个字段之间有逻辑关联，必须在同一把锁内一次性读取，不能分开读。**

## 重试机制

`WithRetry` 是一个泛型函数，对暂时性错误自动重试并采用指数退避策略：

```go
func WithRetry[T any](fn func() (T, error), opts *RetryOptions) (T, error)

type RetryOptions struct {
    MaxAttempts int              // 最大尝试次数（默认 3）
    BackoffMs   []int            // 退避间隔序列，毫秒（默认 [1000, 2000, 4000]）
    ShouldRetry func(error) bool // 自定义错误判断函数
}
```

### 重试流程示例

```
WithRetry(fn, nil)  // 使用默认配置：最多 3 次，退避 1s -> 2s -> 4s
  |
  |- 第 1 次失败 (connection reset) -> 瞬时错误，等待 1s 后重试
  |- 第 2 次失败 (429 rate limit)   -> 瞬时错误，等待 2s 后重试
  |- 第 3 次成功                    -> 返回结果

WithRetry(fn, nil)  // 遇到非瞬时错误
  |
  |- 第 1 次失败 (401 unauthorized) -> 非瞬时错误，立即返回错误，不再重试
```

### 瞬时错误判定规则

```
isTransientError(err) 通过错误消息的字符串匹配来判断：

  可重试（瞬时错误）：
    "timeout"          - 网络超时
    "connection reset" - 连接被重置
    "econnreset"       - POSIX 连接重置错误码
    "rate limit"       - 速率限制
    "429"              - HTTP 429 Too Many Requests
    "502"              - HTTP 502 Bad Gateway
    "503"              - HTTP 503 Service Unavailable

  不可重试（立即返回）：
    HTTP 400 Bad Request     - 请求参数错误
    HTTP 401 Unauthorized    - 认证失败
    HTTP 404 Not Found       - 资源不存在
    其他非瞬时错误           - 直接返回，不浪费重试次数
```
