// Package provider 的 codexcli.go 文件实现了基于 OpenAI Codex CLI 的 AI 提供者。
// Codex CLI 是 OpenAI 提供的命令行工具，CodeDebate 通过子进程方式调用它，
// 将提示词通过 stdin 传入，从 stdout 读取 JSONL 格式的响应。
// 支持会话管理（通过 thread_id 实现多轮对话）和流式输出。
package provider

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"strings"
	"sync"
	"time"

	"github.com/longyunfeigu/codedebate/internal/util"
)

// CodexCliProvider 实现了基于 Codex CLI 命令行工具的 AIProvider 和 SessionProvider 接口。
// 它通过创建子进程调用 `codex` 命令，使用 JSONL 格式进行数据交换。
// 支持会话恢复（resume）功能，通过 thread_id 维持多轮对话的上下文。
type CodexCliProvider struct {
	cwd                 string           // CLI 命令的工作目录，决定 codex 在哪个目录下执行
	timeout             time.Duration    // 无活动超时时间，超过此时间将终止 CLI 进程
	session             CliSessionHelper // 会话辅助器，管理会话状态和提示词构建
	sessionEnabled      bool             // 是否启用会话模式（支持多轮对话）
	skipPermissions     bool             // 是否跳过 CLI 的权限确认提示
	modelName           string           // 底层模型名称，传给 --model 参数
	promptSizeThreshold int              // 大 prompt 写临时文件的阈值（字节），0 表示使用默认值
}

// NewCodexCliProvider 创建一个新的 CodexCliProvider 实例。
// 默认超时时间为 15 分钟，默认跳过权限提示。
func NewCodexCliProvider() *CodexCliProvider {
	return &CodexCliProvider{
		timeout:         15 * time.Minute,
		skipPermissions: true,
	}
}

// SetCwd 设置 CLI 调用时的工作目录。
// 这决定了 Codex CLI 在哪个目录下执行代码审查，通常设置为被审查项目的根目录。
func (p *CodexCliProvider) SetCwd(cwd string) {
	p.cwd = cwd
}

// Name 返回提供者名称 "codex-cli"。
func (p *CodexCliProvider) Name() string { return "codex-cli" }

// StartSession 启动一个新的会话。
// 会话 ID 初始为空，将在收到第一个响应中的 thread.started JSONL 事件后自动设置。
// 后续请求将使用此 thread_id 来恢复会话（resume），实现多轮对话。
func (p *CodexCliProvider) StartSession(name string) {
	p.sessionEnabled = true
	p.session.Start(name)
}

// EndSession 结束当前会话，清理会话状态。
func (p *CodexCliProvider) EndSession() {
	p.sessionEnabled = false
	p.session.End()
}

// SessionID 返回当前会话的 thread_id。
func (p *CodexCliProvider) SessionID() string { return p.session.SessionID() }

// IsFirstMessage 返回当前是否是会话中的第一条消息。
func (p *CodexCliProvider) IsFirstMessage() bool { return p.session.IsFirstMessage() }

// MarkMessageSent 标记一条消息已发送，更新会话状态。
func (p *CodexCliProvider) MarkMessageSent() { p.session.MarkMessageSent() }

// ShouldSendFullHistory 返回是否需要发送完整的消息历史（首次消息时需要）。
func (p *CodexCliProvider) ShouldSendFullHistory() bool { return p.session.ShouldSendFullHistory() }

// Chat 实现同步聊天接口，将消息发送给 Codex CLI 并等待完整响应。
// 内置重试机制（WithRetry），对暂时性错误（超时、限流等）自动重试。
// 在会话模式下，非首次消息只发送最新消息（利用 CLI 的 resume 功能保持上下文）。
func (p *CodexCliProvider) Chat(ctx context.Context, messages []Message, systemPrompt string, opts *ChatOptions) (string, error) {
	return WithRetry(func() (string, error) {
		// 获取会话状态的原子快照，避免并发读取不一致
		snap := p.session.Snapshot()
		var prompt string
		if p.sessionEnabled && !snap.ShouldSendFull() {
			// 会话已建立且非首次消息：只发送最新一条消息，CLI 通过 thread_id 恢复上下文
			prompt = p.session.BuildPromptLastOnly(messages)
		} else {
			// 首次消息或无会话：发送完整的消息历史和系统提示词
			prompt = p.session.BuildPrompt(messages, systemPrompt)
		}
		prepared := PreparePromptForCli(prompt, p.promptSizeThreshold)
		defer prepared.Cleanup()
		result, err := p.runCodex(ctx, prepared.Prompt, snap)
		if err != nil {
			return "", err
		}
		p.session.MarkMessageSent()
		return result, nil
	}, nil)
}

// ChatStream 实现流式聊天接口，将消息发送给 Codex CLI 并通过 channel 逐步返回响应片段。
// 流式模式下，响应内容会被实时解析并发送，适合需要即时反馈的场景。
func (p *CodexCliProvider) ChatStream(ctx context.Context, messages []Message, systemPrompt string) (<-chan string, <-chan error) {
	// 获取会话状态的原子快照
	snap := p.session.Snapshot()
	var prompt string
	if p.sessionEnabled && !snap.ShouldSendFull() {
		// 会话续接模式：只发送最新消息
		prompt = p.session.BuildPromptLastOnly(messages)
	} else {
		// 完整模式：发送所有消息历史
		prompt = p.session.BuildPrompt(messages, systemPrompt)
	}

	prepared := PreparePromptForCli(prompt, p.promptSizeThreshold)

	chunks := make(chan string, 64) // 响应片段缓冲 channel
	errs := make(chan error, 1)     // 错误 channel

	go func() {
		defer close(chunks)
		defer close(errs)
		defer prepared.Cleanup()

		err := p.runCodexStream(ctx, prepared.Prompt, snap, chunks)
		if err != nil {
			errs <- err
			return
		}
		p.session.MarkMessageSent()
	}()

	return chunks, errs
}

// buildArgs 构建 Codex CLI 的命令行参数。
// 使用 SessionSnapshot 的原子快照来避免并发读取会话状态时的数据不一致。
//
// 生成的命令格式：
//   - 新会话：codex exec --json [--dangerously-bypass-approvals-and-sandbox] -
//   - 恢复会话：codex exec resume <thread_id> --json [--dangerously-bypass-approvals-and-sandbox] -
//
// 末尾的 "-" 表示从 stdin 读取输入（提示词）。
func (p *CodexCliProvider) buildArgs(snap SessionSnapshot) []string {
	baseArgs := []string{"--json"} // 输出 JSONL 格式，便于程序解析
	if p.skipPermissions {
		// 跳过所有权限确认和沙箱限制，允许非交互式运行
		baseArgs = append(baseArgs, "--dangerously-bypass-approvals-and-sandbox")
	}
	if p.modelName != "" {
		baseArgs = append(baseArgs, "--model", p.modelName)
	}
	if p.sessionEnabled && snap.ID != "" {
		// 已有会话 ID：使用 resume 子命令恢复之前的对话线程
		return append([]string{"exec", "resume", snap.ID}, append(baseArgs, "-")...)
	}
	// 无会话 ID：启动新的执行
	return append([]string{"exec"}, append(baseArgs, "-")...)
}

// codexEvent 表示 Codex CLI 输出的一个 JSONL 事件。
// Codex CLI 以 JSONL（每行一个 JSON 对象）格式输出事件流，主要事件类型包括：
//   - "thread.started"：线程启动事件，包含 thread_id 用于后续会话恢复
//   - "item.completed"：内容完成事件，包含 AI 生成的消息文本
type codexEvent struct {
	Type     string         `json:"type"`                // 事件类型
	ThreadID string         `json:"thread_id,omitempty"` // 线程 ID（仅 thread.started 事件包含）
	Item     *codexItemData `json:"item,omitempty"`      // 事件数据（仅 item.completed 事件包含）
}

// codexItemData 表示 Codex 事件中的数据项内容。
type codexItemData struct {
	Type string `json:"type"` // 数据项类型（如 "agent_message"）
	Text string `json:"text"` // AI 生成的文本内容
}

type codexStreamState struct {
	lastAgentText string
}

// parseJsonlOutput 解析 Codex CLI 的 JSONL 输出，提取会话 ID 和 AI 生成的消息文本。
// 逐行解析 JSON 对象，处理两种关键事件：
//   - thread.started：提取 thread_id 并保存到会话中，用于后续的会话恢复
//   - item.completed（agent_message 类型）：提取 AI 的回复文本并拼接
func (p *CodexCliProvider) parseJsonlOutput(output string) string {
	var text strings.Builder
	state := &codexStreamState{}
	for _, line := range strings.Split(output, "\n") {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" {
			continue
		}
		for _, chunk := range p.handleCodexEvent(trimmed, state, false) {
			text.WriteString(chunk)
		}
	}
	return text.String()
}

func (p *CodexCliProvider) handleCodexEvent(line string, state *codexStreamState, includeToolTrace bool) []string {
	var payload map[string]any
	if err := json.Unmarshal([]byte(line), &payload); err != nil {
		util.Warnf("codex: failed to parse JSONL line: %v", err)
		return nil
	}

	eventType := strings.TrimSpace(getString(payload, "type"))
	if eventType == "" {
		return nil
	}

	if isCodexThreadStartedEvent(eventType) && p.sessionEnabled {
		if threadID := strings.TrimSpace(getString(payload, "thread_id")); threadID != "" {
			p.session.SetSessionID(threadID)
		}
	}

	if chunk := extractCodexMessageChunk(payload, eventType, state); chunk != "" {
		return []string{chunk}
	}

	if !includeToolTrace {
		return nil
	}
	if chunk := formatCodexToolTrace(payload, eventType); chunk != "" {
		return []string{chunk}
	}
	return nil
}

func isCodexThreadStartedEvent(eventType string) bool {
	return eventType == "thread.started" || eventType == "thread_started"
}

func extractCodexMessageChunk(payload map[string]any, eventType string, state *codexStreamState) string {
	switch eventType {
	case "agent_message_delta", "agent_message_content_delta":
		if delta := strings.TrimSpace(firstNonEmpty(
			getString(payload, "delta"),
			getString(payload, "text"),
		)); delta != "" {
			state.lastAgentText += delta
			return delta
		}
	case "agent_message", "item.completed", "item_completed", "raw_response_item":
		message := payload
		if eventType == "item.completed" || eventType == "item_completed" {
			item, ok := getMap(payload, "item")
			if !ok {
				return ""
			}
			itemType := strings.TrimSpace(getString(item, "type"))
			if itemType != "" && itemType != "agent_message" {
				return ""
			}
			message = item
		}
		if eventType == "raw_response_item" {
			if item, ok := getMap(payload, "response_item"); ok {
				message = item
			}
		}
		text := strings.TrimSpace(extractCodexMessageText(message))
		if text == "" {
			return ""
		}
		delta := diffAssistantText(state.lastAgentText, text)
		state.lastAgentText = text
		return delta
	}
	return ""
}

func extractCodexMessageText(value map[string]any) string {
	for _, key := range []string{"text", "raw_content", "summary_text"} {
		if text := strings.TrimSpace(getString(value, key)); text != "" {
			return text
		}
	}
	for _, key := range []string{"message", "item", "output_item", "response_item"} {
		if nested, ok := getMap(value, key); ok {
			if text := strings.TrimSpace(extractCodexMessageText(nested)); text != "" {
				return text
			}
		}
	}
	for _, key := range []string{"content", "content_items", "output_items"} {
		if text := strings.TrimSpace(extractCodexTextArray(value[key])); text != "" {
			return text
		}
	}
	return ""
}

func extractCodexTextArray(value any) string {
	items, ok := value.([]any)
	if !ok {
		return ""
	}
	var parts []string
	for _, item := range items {
		switch v := item.(type) {
		case string:
			if s := strings.TrimSpace(v); s != "" {
				parts = append(parts, s)
			}
		case map[string]any:
			if text := strings.TrimSpace(extractCodexMessageText(v)); text != "" {
				parts = append(parts, text)
			}
		}
	}
	return strings.Join(parts, "")
}

func formatCodexToolTrace(payload map[string]any, eventType string) string {
	switch eventType {
	case "exec_command_begin":
		if cmd := strings.TrimSpace(formatCodexParsedCommand(payload["parsed_cmd"])); cmd != "" {
			return fmt.Sprintf("\n[tool] Exec: %s\n", cmd)
		}
		if cmd := strings.TrimSpace(firstNonEmpty(
			getString(payload, "command"),
			getString(payload, "input_text"),
			getString(payload, "aggregated_output"),
		)); cmd != "" {
			return fmt.Sprintf("\n[tool] Exec: %s\n", cmd)
		}
	case "mcp_tool_call_begin":
		if invocation, ok := getMap(payload, "invocation"); ok {
			server := strings.TrimSpace(firstNonEmpty(
				getString(invocation, "server_name"),
				getString(payload, "server_name"),
			))
			tool := strings.TrimSpace(firstNonEmpty(
				getString(invocation, "tool_name"),
				getString(invocation, "name"),
				getString(payload, "tool_name"),
			))
			if server != "" || tool != "" {
				return fmt.Sprintf("\n[tool] MCP: %s\n", strings.TrimSpace(strings.Join([]string{server, tool}, ".")))
			}
		}
	case "web_search_begin":
		if queries, ok := payload["queries"].([]any); ok {
			for _, query := range queries {
				if q, ok := query.(map[string]any); ok {
					if text := strings.TrimSpace(getString(q, "q")); text != "" {
						return fmt.Sprintf("\n[tool] WebSearch: %s\n", text)
					}
				}
			}
		}
		if text := strings.TrimSpace(firstNonEmpty(getString(payload, "query"), getString(payload, "q"))); text != "" {
			return fmt.Sprintf("\n[tool] WebSearch: %s\n", text)
		}
	case "view_image_tool_call":
		return "\n[tool] ViewImage\n"
	case "image_generation_begin":
		return "\n[tool] ImageGeneration\n"
	case "terminal_interaction":
		if prompt := strings.TrimSpace(firstNonEmpty(
			getString(payload, "stdin"),
			getString(payload, "text"),
			getString(payload, "prompt"),
		)); prompt != "" {
			return fmt.Sprintf("\n[input] %s\n", prompt)
		}
		return "\n[input] Codex requested terminal interaction\n"
	case "request_user_input", "elicitation_request", "exec_approval_request", "apply_patch_approval_request":
		if prompt := strings.TrimSpace(firstNonEmpty(
			getString(payload, "reason"),
			getString(payload, "text"),
			getString(payload, "prompt"),
		)); prompt != "" {
			return fmt.Sprintf("\n[input] %s\n", prompt)
		}
		return "\n[input] Codex requested user input\n"
	}
	return ""
}

func formatCodexParsedCommand(value any) string {
	switch v := value.(type) {
	case string:
		return v
	case []any:
		parts := make([]string, 0, len(v))
		for _, part := range v {
			if s, ok := part.(string); ok && strings.TrimSpace(s) != "" {
				parts = append(parts, s)
			}
		}
		return strings.Join(parts, " ")
	case map[string]any:
		if cmd, ok := v["cmd"]; ok {
			return formatCodexParsedCommand(cmd)
		}
	}
	return ""
}

func getMap(value map[string]any, key string) (map[string]any, bool) {
	nested, ok := value[key].(map[string]any)
	return nested, ok
}

func getString(value map[string]any, key string) string {
	raw, ok := value[key]
	if !ok || raw == nil {
		return ""
	}
	switch v := raw.(type) {
	case string:
		return v
	case json.Number:
		return v.String()
	case float64:
		return fmt.Sprintf("%.0f", v)
	default:
		return ""
	}
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}

// runCodex 以同步方式运行 Codex CLI，等待命令完成后返回完整的响应文本。
// 通过 stdin 传入提示词，从 stdout 读取 JSONL 输出并解析。
// 执行过程：创建子进程 -> 写入 stdin -> 等待完成 -> 解析 JSONL 输出。
func (p *CodexCliProvider) runCodex(ctx context.Context, prompt string, snap SessionSnapshot) (string, error) {
	args := p.buildArgs(snap)
	cmd := exec.CommandContext(ctx, "codex", args...)
	if p.cwd != "" {
		cmd.Dir = p.cwd
	}

	// 将提示词通过 stdin 传递给 codex 命令
	cmd.Stdin = strings.NewReader(prompt)

	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("codex CLI failed: %w: %s", err, stderr.String())
	}

	// 解析 JSONL 输出，提取 AI 响应文本
	return p.parseJsonlOutput(stdout.String()), nil
}

// runCodexStream 以流式方式运行 Codex CLI，实时解析 JSONL 输出并将响应片段发送到 chunks channel。
// 相比 runCodex 的同步模式，流式模式可以在 CLI 仍在运行时就开始处理输出，
// 适合长时间运行的审查任务。
//
// 主要机制：
//  1. 通过 pipe 连接子进程的 stdout/stderr
//  2. 后台 goroutine 监控无活动超时，超时后强制终止进程
//  3. 后台 goroutine 读取 stderr 以更新活动时间戳
//  4. 主循环逐行读取 stdout，解析 JSONL 事件并分发到 chunks channel
func (p *CodexCliProvider) runCodexStream(ctx context.Context, prompt string, snap SessionSnapshot, chunks chan<- string) error {
	args := p.buildArgs(snap)
	cmd := exec.CommandContext(ctx, "codex", args...)
	if p.cwd != "" {
		cmd.Dir = p.cwd
	}

	// 将提示词通过 stdin 传递给 codex 命令
	cmd.Stdin = strings.NewReader(prompt)

	// 创建 stdout 和 stderr 的管道，用于流式读取输出
	stdoutPipe, err := cmd.StdoutPipe()
	if err != nil {
		return fmt.Errorf("failed to create stdout pipe: %w", err)
	}
	stderrPipe, err := cmd.StderrPipe()
	if err != nil {
		return fmt.Errorf("failed to create stderr pipe: %w", err)
	}

	if err := cmd.Start(); err != nil {
		return fmt.Errorf("failed to start codex CLI: %w", err)
	}

	// 用互斥锁保护最后活动时间戳，因为多个 goroutine 会并发访问
	var mu sync.Mutex
	lastActivity := time.Now()

	updateActivity := func() {
		mu.Lock()
		lastActivity = time.Now()
		mu.Unlock()
	}

	// 无活动超时监控 goroutine：每 10 秒检查一次，如果超过设定超时则强制终止进程
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
						// 超时，强制终止 CLI 进程
						cmd.Process.Kill()
						return
					}
				}
			}
		}()
	}

	// 后台读取 stderr：虽然不处理 stderr 内容，但用于更新活动时间戳，
	// 防止 CLI 在输出大量 stderr 日志时被误判为无活动而超时
	go func() {
		buf := make([]byte, 4096)
		for {
			_, readErr := stderrPipe.Read(buf)
			if readErr != nil {
				return
			}
			updateActivity()
		}
	}()

	// 主循环：从 stdout 流式读取数据，按行缓冲并解析 JSONL 事件
	state := &codexStreamState{}
	var lineBuf string
	buf := make([]byte, 4096)
	for {
		n, readErr := stdoutPipe.Read(buf)
		if n > 0 {
			updateActivity()
			lineBuf += string(buf[:n])

			// 处理缓冲区中所有完整的行（以换行符分隔的 JSONL 事件）
			for {
				idx := strings.Index(lineBuf, "\n")
				if idx < 0 {
					break // 没有完整行，等待更多数据
				}
				line := strings.TrimSpace(lineBuf[:idx])
				lineBuf = lineBuf[idx+1:]

				if line == "" {
					continue
				}
				for _, chunk := range p.handleCodexEvent(line, state, true) {
					chunks <- chunk
				}
			}
		}
		if readErr != nil {
			break // 读取结束（EOF 或错误）
		}
	}

	// 处理缓冲区中可能残留的最后一行数据（未以换行符结尾的情况）
	if trimmed := strings.TrimSpace(lineBuf); trimmed != "" {
		for _, chunk := range p.handleCodexEvent(trimmed, state, true) {
			chunks <- chunk
		}
	}

	// 等待子进程完全退出
	if err := cmd.Wait(); err != nil {
		return fmt.Errorf("codex CLI exited with error: %w", err)
	}
	return nil
}
