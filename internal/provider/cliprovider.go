package provider

import (
	"fmt"
	"sync"
)

// CliSessionHelper 为 CLI 类提供者（claude-code、codex-cli）提供共享的会话管理。
//
// 会话 ID 不是预先生成的，而是由各 CLI 的首次响应中提取：
//   - Claude Code: 从 stream-json 事件的 session_id 字段获取
//   - Codex CLI:   从 JSONL 的 thread.started 事件的 thread_id 字段获取
//
// 所有字段的访问都通过 mutex 保护，因为流式读取在独立的 goroutine 中进行。
type CliSessionHelper struct {
	mu           sync.Mutex
	sessionID    string // 由 CLI 响应设置，初始为空
	firstMessage bool   // 是否还未发送过消息
	sessionName  string // 会话名称，用于 prompt 中标识
}

// Start 开始一个新会话。sessionID 初始为空，等待首次 CLI 响应来设置。
func (h *CliSessionHelper) Start(name string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.sessionID = ""
	h.firstMessage = true
	h.sessionName = name
}

// End 结束当前会话，清除所有状态。
func (h *CliSessionHelper) End() {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.sessionID = ""
	h.firstMessage = true
	h.sessionName = ""
}

// SessionSnapshot 原子快照，解决 TOCTOU 竞态问题。
//
// 问题：如果分别调用 SessionID() 和 IsFirstMessage()，两次锁之间
// 可能有其他 goroutine 修改了状态，导致读到不一致的值。
// 解决：一次性在同一把锁下读取所有相关字段。
type SessionSnapshot struct {
	ID           string
	FirstMessage bool
	Name         string
}

// ShouldSendFull 根据快照判断是否需要发送完整历史。
// 当没有活跃会话（ID 为空）或是首条消息时，返回 true。
func (s SessionSnapshot) ShouldSendFull() bool {
	return !(s.ID != "" && !s.FirstMessage)
}

// Snapshot 原子地获取会话的所有状态，用于后续的 prompt 构建和参数构建。
func (h *CliSessionHelper) Snapshot() SessionSnapshot {
	h.mu.Lock()
	defer h.mu.Unlock()
	return SessionSnapshot{
		ID:           h.sessionID,
		FirstMessage: h.firstMessage,
		Name:         h.sessionName,
	}
}

// SessionID 返回当前会话 ID。
func (h *CliSessionHelper) SessionID() string {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.sessionID
}

// SetSessionID 设置会话 ID（由 provider 解析 CLI 响应后调用）。
func (h *CliSessionHelper) SetSessionID(id string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.sessionID = id
}

// IsFirstMessage 返回下一条消息是否为会话中的第一条。
func (h *CliSessionHelper) IsFirstMessage() bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.firstMessage
}

// ShouldSendFullHistory 判断是否需要发送完整历史。
func (h *CliSessionHelper) ShouldSendFullHistory() bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	return !(h.sessionID != "" && !h.firstMessage)
}

// MarkMessageSent 标记已发送一条消息，后续消息将走增量模式。
func (h *CliSessionHelper) MarkMessageSent() {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.firstMessage = false
}

// BuildPrompt 构建完整的 prompt（包含系统提示 + 所有消息历史）。
// 用于首次调用或非会话模式。
func (h *CliSessionHelper) BuildPrompt(messages []Message, systemPrompt string) string {
	h.mu.Lock()
	name := h.sessionName
	first := h.firstMessage
	h.mu.Unlock()

	var prompt string
	if name != "" && first {
		prompt += fmt.Sprintf("[%s]\n\n", name)
	}
	if systemPrompt != "" {
		prompt += fmt.Sprintf("System: %s\n\n", systemPrompt)
	}
	for _, msg := range messages {
		prompt += fmt.Sprintf("%s: %s\n\n", msg.Role, msg.Content)
	}
	return prompt
}

// BuildPromptLastOnly 仅返回最后一条 user 消息的内容。
// 用于会话续传模式，因为 CLI 已记住之前的上下文。
func (h *CliSessionHelper) BuildPromptLastOnly(messages []Message) string {
	for i := len(messages) - 1; i >= 0; i-- {
		if messages[i].Role == "user" {
			return messages[i].Content
		}
	}
	return ""
}
