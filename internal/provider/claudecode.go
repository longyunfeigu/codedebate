package provider

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"sync"
	"time"

	"github.com/longyunfeigu/codedebate/internal/util"
)

// ClaudeCodeProvider 通过调用 Claude Code CLI 实现 AIProvider 和 SessionProvider。
//
// 核心机制：
//   - 通过 os/exec 调用 `claude` 命令，prompt 通过 stdin 传入（避免参数过长导致 E2BIG）
//   - 非流式模式使用 --output-format json，输出为 JSON 数组 [system, assistant, result]
//   - 流式模式使用 --output-format stream-json，输出为 JSONL（每行一个事件）
//   - 会话复用通过 --resume <session_id> 实现，session_id 从首次响应中提取
type ClaudeCodeProvider struct {
	cwd                 string           // CLI 的工作目录
	timeout             time.Duration    // 无活动超时时间（默认 15 分钟）
	session             CliSessionHelper // 会话状态管理
	skipPermissions     bool             // 是否跳过 CLI 的权限确认（非交互模式必须为 true）
	modelName           string           // 底层模型名称，传给 --model 参数（如 "claude-sonnet-4-5-20250514"）
	promptSizeThreshold int              // 大 prompt 写临时文件的阈值（字节），0 表示使用默认值
}

func NewClaudeCodeProvider() *ClaudeCodeProvider {
	return &ClaudeCodeProvider{
		timeout:         15 * time.Minute,
		skipPermissions: true,
	}
}

func (p *ClaudeCodeProvider) SetCwd(cwd string) {
	p.cwd = cwd
}

func (p *ClaudeCodeProvider) Name() string { return "claude-code" }

// --- SessionProvider 接口实现，委托给 CliSessionHelper ---
func (p *ClaudeCodeProvider) StartSession(name string)    { p.session.Start(name) }
func (p *ClaudeCodeProvider) EndSession()                 { p.session.End() }
func (p *ClaudeCodeProvider) SessionID() string           { return p.session.SessionID() }
func (p *ClaudeCodeProvider) IsFirstMessage() bool        { return p.session.IsFirstMessage() }
func (p *ClaudeCodeProvider) MarkMessageSent()            { p.session.MarkMessageSent() }
func (p *ClaudeCodeProvider) ShouldSendFullHistory() bool { return p.session.ShouldSendFullHistory() }

// Chat 同步调用 Claude CLI 并返回完整响应。带指数退避重试。
func (p *ClaudeCodeProvider) Chat(ctx context.Context, messages []Message, systemPrompt string, _ *ChatOptions) (string, error) {
	return WithRetry(func() (string, error) {
		// 原子读取会话状态，避免 TOCTOU 竞态
		snap := p.session.Snapshot()
		var prompt string
		if snap.ShouldSendFull() {
			prompt = p.session.BuildPrompt(messages, systemPrompt)
		} else {
			// 会话续传：只发最后一条 user 消息
			prompt = p.session.BuildPromptLastOnly(messages)
		}
		prepared := PreparePromptForCli(prompt, p.promptSizeThreshold)
		defer prepared.Cleanup()
		result, err := p.runClaude(ctx, prepared.Prompt, systemPrompt, snap)
		if err != nil {
			return "", err
		}
		p.session.MarkMessageSent()
		return result, nil
	}, nil)
}

// ChatStream 流式调用 Claude CLI，通过 channel 逐块返回响应。
func (p *ClaudeCodeProvider) ChatStream(ctx context.Context, messages []Message, systemPrompt string) (<-chan string, <-chan error) {
	snap := p.session.Snapshot()
	var prompt string
	if snap.ShouldSendFull() {
		prompt = p.session.BuildPrompt(messages, systemPrompt)
	} else {
		prompt = p.session.BuildPromptLastOnly(messages)
	}

	prepared := PreparePromptForCli(prompt, p.promptSizeThreshold)

	chunks := make(chan string, 64)
	errs := make(chan error, 1)

	go func() {
		defer close(chunks)
		defer close(errs)
		defer prepared.Cleanup()

		err := p.runClaudeStream(ctx, prepared.Prompt, systemPrompt, snap, chunks)
		if err != nil {
			errs <- err
			return
		}
		p.session.MarkMessageSent()
	}()

	return chunks, errs
}

// buildArgs 构建 claude CLI 的命令行参数。
//
// 参数说明：
//
//	-p -                      : 从 stdin 读取 prompt（pipe 模式）
//	--dangerously-skip-permissions : 跳过交互式权限确认（可通过配置关闭）
//	--output-format json/stream-json : 结构化输出格式
//	--resume <id>             : 复用已有会话（非首次消息时）
//	--system-prompt <prompt>  : 系统提示（仅首次消息时传入）
func (p *ClaudeCodeProvider) buildArgs(systemPrompt string, streaming bool, snap SessionSnapshot) []string {
	args := []string{"-p", "-",
		"--strict-mcp-config", // 不加载用户配置的 MCP servers，节省内存
	}

	if p.skipPermissions {
		args = append(args, "--dangerously-skip-permissions")
	}

	if streaming {
		args = append(args, "--output-format", "stream-json")
	} else {
		args = append(args, "--output-format", "json")
	}

	if p.modelName != "" {
		args = append(args, "--model", p.modelName)
	}

	if snap.ID != "" && !snap.FirstMessage {
		// 续传已有会话（系统提示从首次调用中记住）
		args = append(args, "--resume", snap.ID)
	} else if systemPrompt != "" {
		// 首次消息或无会话：传入系统提示
		args = append(args, "--system-prompt", systemPrompt)
	}

	return args
}

// filterClaudeEnv 从环境变量中移除 CLAUDECODE，
// 避免子进程因 "cannot launch inside another session" 而失败。
func filterClaudeEnv() []string {
	var env []string
	for _, e := range os.Environ() {
		if !strings.HasPrefix(e, "CLAUDECODE=") {
			env = append(env, e)
		}
	}
	return env
}

// --- Claude CLI 的 JSON 事件类型 ---
//
// --output-format json 输出格式：JSON 数组 [system_event, assistant_event, result_event]
// --output-format stream-json 输出格式：JSONL（每行一个事件）
//
// assistant 事件: {"type":"assistant","message":{"content":[{"text":"..."}]},"session_id":"..."}
// result 事件:    {"type":"result","subtype":"success","result":"...","session_id":"..."}

type claudeEvent struct {
	Type      string         `json:"type"`
	Subtype   string         `json:"subtype,omitempty"`
	SessionID string         `json:"session_id,omitempty"` // 会话 ID，从响应中提取
	Result    string         `json:"result,omitempty"`     // result 事件的最终文本
	IsError   bool           `json:"is_error,omitempty"`
	Message   *claudeMessage `json:"message,omitempty"` // assistant 事件的消息体
}

type claudeMessage struct {
	Content []claudeContent `json:"content"`
}

type claudeContent struct {
	Type      string          `json:"type"` // "text" | "tool_use" | "tool_result"
	Text      string          `json:"text,omitempty"`
	ID        string          `json:"id,omitempty"`
	Name      string          `json:"name,omitempty"`
	Input     json.RawMessage `json:"input,omitempty"`
	ToolUseID string          `json:"tool_use_id,omitempty"`
	Content   json.RawMessage `json:"content,omitempty"`
	IsError   bool            `json:"is_error,omitempty"`
}

type claudeStreamState struct {
	lastAssistantText string
	textEmitted       bool
	seenToolUseIDs    map[string]struct{}
}

// runClaude 以非流式模式执行 claude CLI（--output-format json）。
// 输出为 JSON 数组，依次解析提取 session_id 和 result。
func (p *ClaudeCodeProvider) runClaude(ctx context.Context, prompt, systemPrompt string, snap SessionSnapshot) (string, error) {
	args := p.buildArgs(systemPrompt, false, snap)
	cmd := exec.CommandContext(ctx, "claude", args...)
	cmd.Env = filterClaudeEnv()
	if p.cwd != "" {
		cmd.Dir = p.cwd
	}

	cmd.Stdin = strings.NewReader(prompt)

	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("claude CLI failed: %w: %s", err, stderr.String())
	}

	// 解析 JSON 数组输出
	var events []claudeEvent
	if err := json.Unmarshal(stdout.Bytes(), &events); err != nil {
		util.Warnf("claude CLI output is not valid JSON array, falling back to plain text: %v", err)
		return strings.TrimSpace(stdout.String()), nil
	}

	// 从事件中提取 session_id 和 result
	for _, ev := range events {
		if ev.SessionID != "" {
			p.session.SetSessionID(ev.SessionID)
		}
		if ev.Type == "result" {
			if ev.IsError {
				return "", fmt.Errorf("claude returned error: %s", ev.Result)
			}
			return ev.Result, nil
		}
	}

	// 兜底：从 assistant 事件中提取文本
	for _, ev := range events {
		if ev.Type == "assistant" && ev.Message != nil {
			return extractTextFromContent(ev.Message.Content), nil
		}
	}

	return strings.TrimSpace(stdout.String()), nil
}

// runClaudeStream 以流式模式执行 claude CLI（--output-format stream-json）。
// 输出为 JSONL（每行一个 JSON 事件），边读取边解析并发送到 chunks channel。
func (p *ClaudeCodeProvider) runClaudeStream(ctx context.Context, prompt, systemPrompt string, snap SessionSnapshot, chunks chan<- string) error {
	args := p.buildArgs(systemPrompt, true, snap)
	cmd := exec.CommandContext(ctx, "claude", args...)
	cmd.Env = filterClaudeEnv()
	if p.cwd != "" {
		cmd.Dir = p.cwd
	}

	cmd.Stdin = strings.NewReader(prompt)

	stdoutPipe, err := cmd.StdoutPipe()
	if err != nil {
		return fmt.Errorf("failed to create stdout pipe: %w", err)
	}
	stderrPipe, err := cmd.StderrPipe()
	if err != nil {
		return fmt.Errorf("failed to create stderr pipe: %w", err)
	}

	if err := cmd.Start(); err != nil {
		return fmt.Errorf("failed to start claude CLI: %w", err)
	}

	// --- 无活动超时机制 ---
	// 如果 CLI 长时间无 stdout/stderr 输出，主动杀掉进程
	var mu sync.Mutex
	lastActivity := time.Now()

	updateActivity := func() {
		mu.Lock()
		lastActivity = time.Now()
		mu.Unlock()
	}

	done := make(chan struct{})
	defer close(done)

	if p.timeout > 0 {
		go func() {
			ticker := time.NewTicker(10 * time.Second)
			defer ticker.Stop()
			for {
				select {
				case <-done:
					return
				case <-ticker.C:
					mu.Lock()
					elapsed := time.Since(lastActivity)
					mu.Unlock()
					if elapsed > p.timeout {
						cmd.Process.Kill()
						return
					}
				}
			}
		}()
	}

	// 后台读取 stderr（更新活动时间戳 + 捕获错误信息）
	var stderrBuf bytes.Buffer
	go func() {
		buf := make([]byte, 4096)
		for {
			n, readErr := stderrPipe.Read(buf)
			if n > 0 {
				stderrBuf.Write(buf[:n])
			}
			if readErr != nil {
				return
			}
			updateActivity()
		}
	}()

	// 逐行读取 stdout 并解析 JSONL 事件
	state := &claudeStreamState{
		seenToolUseIDs: make(map[string]struct{}),
	}
	var lineBuf string
	buf := make([]byte, 4096)
	for {
		n, readErr := stdoutPipe.Read(buf)
		if n > 0 {
			updateActivity()
			lineBuf += string(buf[:n])

			// 处理所有完整的行
			for {
				idx := strings.Index(lineBuf, "\n")
				if idx < 0 {
					break
				}
				line := strings.TrimSpace(lineBuf[:idx])
				lineBuf = lineBuf[idx+1:]

				if line == "" {
					continue
				}

				p.handleStreamEvent(line, state, chunks)
			}
		}
		if readErr != nil {
			break
		}
	}

	// 处理缓冲区中剩余的不完整行
	if trimmed := strings.TrimSpace(lineBuf); trimmed != "" {
		p.handleStreamEvent(trimmed, state, chunks)
	}

	if err := cmd.Wait(); err != nil {
		stderr := strings.TrimSpace(stderrBuf.String())
		if stderr != "" {
			return fmt.Errorf("claude CLI exited with error: %w: %s", err, stderr)
		}
		return fmt.Errorf("claude CLI exited with error: %w", err)
	}
	return nil
}

// handleStreamEvent 解析单行 JSONL 事件并分发处理。
func (p *ClaudeCodeProvider) handleStreamEvent(line string, state *claudeStreamState, chunks chan<- string) {
	var event claudeEvent
	if err := json.Unmarshal([]byte(line), &event); err != nil {
		util.Warnf("claude stream: failed to parse JSONL line: %v", err)
		return
	}

	// 从任何携带 session_id 的事件中捕获会话 ID
	if event.SessionID != "" {
		p.session.SetSessionID(event.SessionID)
	}

	switch event.Type {
	case "assistant":
		if event.Message != nil {
			p.emitAssistantMessage(event.Message.Content, state, chunks)
		}
	case "result":
		if event.IsError {
			util.Warnf("claude stream returned error result: %s", event.Result)
		}
		if !state.textEmitted {
			if text := strings.TrimSpace(event.Result); text != "" {
				chunks <- text
				state.textEmitted = true
				state.lastAssistantText = text
			}
		}
	}
}

func (p *ClaudeCodeProvider) emitAssistantMessage(content []claudeContent, state *claudeStreamState, chunks chan<- string) {
	if delta := diffAssistantText(state.lastAssistantText, extractTextFromContent(content)); delta != "" {
		chunks <- delta
		state.textEmitted = true
		state.lastAssistantText = extractTextFromContent(content)
	}

	for _, block := range content {
		if block.Type != "tool_use" {
			continue
		}
		if block.ID != "" {
			if _, seen := state.seenToolUseIDs[block.ID]; seen {
				continue
			}
			state.seenToolUseIDs[block.ID] = struct{}{}
		}
		if chunk := formatToolUseChunk(block); chunk != "" {
			chunks <- chunk
		}
	}
}

func diffAssistantText(previous, current string) string {
	current = strings.TrimSpace(current)
	previous = strings.TrimSpace(previous)
	if current == "" || current == previous {
		return ""
	}
	if previous != "" && strings.HasPrefix(current, previous) {
		return current[len(previous):]
	}
	return current
}

func formatToolUseChunk(block claudeContent) string {
	name := strings.TrimSpace(block.Name)
	if name == "" {
		name = "tool"
	}
	detail := summarizeClaudeToolInput(block.Input)
	if detail == "" {
		return fmt.Sprintf("\n[tool] %s\n", name)
	}
	return fmt.Sprintf("\n[tool] %s: %s\n", name, detail)
}

func summarizeClaudeToolInput(input json.RawMessage) string {
	if len(input) == 0 {
		return ""
	}

	var decoded any
	if err := json.Unmarshal(input, &decoded); err != nil {
		return truncateForToolTrace(strings.TrimSpace(string(input)), 160)
	}

	switch v := decoded.(type) {
	case map[string]any:
		keys := []string{
			"command", "cmd", "description", "prompt", "query", "url",
			"file_path", "path", "paths", "pattern", "glob", "tool",
		}
		for _, key := range keys {
			if raw, ok := v[key]; ok {
				if formatted := formatToolValue(raw); formatted != "" {
					return formatted
				}
			}
		}
		if len(v) == 0 {
			return ""
		}
		b, err := json.Marshal(v)
		if err != nil {
			return ""
		}
		return truncateForToolTrace(string(b), 160)
	case []any:
		b, err := json.Marshal(v)
		if err != nil {
			return ""
		}
		return truncateForToolTrace(string(b), 160)
	case string:
		return truncateForToolTrace(v, 160)
	default:
		return truncateForToolTrace(fmt.Sprint(v), 160)
	}
}

func formatToolValue(v any) string {
	switch value := v.(type) {
	case string:
		return truncateForToolTrace(value, 160)
	case []any:
		parts := make([]string, 0, len(value))
		for _, item := range value {
			text := strings.TrimSpace(fmt.Sprint(item))
			if text != "" {
				parts = append(parts, text)
			}
		}
		return truncateForToolTrace(strings.Join(parts, ", "), 160)
	default:
		return truncateForToolTrace(fmt.Sprint(value), 160)
	}
}

func truncateForToolTrace(s string, limit int) string {
	s = strings.Join(strings.Fields(strings.TrimSpace(s)), " ")
	if s == "" || limit <= 0 {
		return s
	}
	runes := []rune(s)
	if len(runes) <= limit {
		return s
	}
	return string(runes[:limit-3]) + "..."
}

// extractTextFromContent 从 Claude 消息的 content 数组中拼接所有文本块。
func extractTextFromContent(content []claudeContent) string {
	var parts []string
	for _, c := range content {
		if c.Type == "text" && c.Text != "" {
			parts = append(parts, c.Text)
		}
	}
	return strings.Join(parts, "")
}
