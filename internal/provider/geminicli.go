// Package provider 的 geminicli.go 文件实现了基于 Gemini CLI 的 AI 提供者。
// Gemini CLI 是 Google 提供的命令行工具，CodeDebate 通过子进程方式调用它，
// 将提示词通过 stdin 传入，从 stdout 读取 JSON/NDJSON 格式的响应。
// 支持会话管理（通过 session_id 实现多轮对话）和流式输出。
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

// GeminiCliProvider 实现了基于 Gemini CLI 命令行工具的 AIProvider 和 SessionProvider 接口。
// 它通过创建子进程调用 `gemini` 命令，使用 JSON/NDJSON 格式进行数据交换。
// 支持会话恢复（resume）功能，通过 session_id 维持多轮对话的上下文。
type GeminiCliProvider struct {
	cwd                 string           // CLI 命令的工作目录
	timeout             time.Duration    // 无活动超时时间
	session             CliSessionHelper // 会话辅助器
	sessionEnabled      bool             // 是否启用会话模式
	skipPermissions     bool             // 是否跳过 CLI 的权限确认提示（-y 参数）
	modelName           string           // 底层模型名称，传给 --model 参数
	promptSizeThreshold int              // 大 prompt 写临时文件的阈值（字节）
}

// NewGeminiCliProvider 创建一个新的 GeminiCliProvider 实例。
func NewGeminiCliProvider() *GeminiCliProvider {
	return &GeminiCliProvider{
		timeout:         15 * time.Minute,
		skipPermissions: true,
	}
}

func (p *GeminiCliProvider) SetCwd(cwd string) { p.cwd = cwd }
func (p *GeminiCliProvider) Name() string       { return "gemini-cli" }

// --- SessionProvider 接口实现 ---
func (p *GeminiCliProvider) StartSession(name string) {
	p.sessionEnabled = true
	p.session.Start(name)
}
func (p *GeminiCliProvider) EndSession() {
	p.sessionEnabled = false
	p.session.End()
}
func (p *GeminiCliProvider) SessionID() string             { return p.session.SessionID() }
func (p *GeminiCliProvider) IsFirstMessage() bool          { return p.session.IsFirstMessage() }
func (p *GeminiCliProvider) MarkMessageSent()              { p.session.MarkMessageSent() }
func (p *GeminiCliProvider) ShouldSendFullHistory() bool   { return p.session.ShouldSendFullHistory() }

// Chat 实现同步聊天接口，带重试机制。
func (p *GeminiCliProvider) Chat(ctx context.Context, messages []Message, systemPrompt string, opts *ChatOptions) (string, error) {
	return WithRetry(func() (string, error) {
		snap := p.session.Snapshot()
		var prompt string
		if p.sessionEnabled && !snap.ShouldSendFull() {
			prompt = p.session.BuildPromptLastOnly(messages)
		} else {
			prompt = p.session.BuildPrompt(messages, systemPrompt)
		}
		prepared := PreparePromptForCli(prompt, p.promptSizeThreshold)
		defer prepared.Cleanup()
		result, err := p.runGemini(ctx, prepared.Prompt, snap)
		if err != nil {
			return "", err
		}
		p.session.MarkMessageSent()
		return result, nil
	}, nil)
}

// ChatStream 实现流式聊天接口。
func (p *GeminiCliProvider) ChatStream(ctx context.Context, messages []Message, systemPrompt string) (<-chan string, <-chan error) {
	snap := p.session.Snapshot()
	var prompt string
	if p.sessionEnabled && !snap.ShouldSendFull() {
		prompt = p.session.BuildPromptLastOnly(messages)
	} else {
		prompt = p.session.BuildPrompt(messages, systemPrompt)
	}

	prepared := PreparePromptForCli(prompt, p.promptSizeThreshold)

	chunks := make(chan string, 64)
	errs := make(chan error, 1)

	go func() {
		defer close(chunks)
		defer close(errs)
		defer prepared.Cleanup()

		err := p.runGeminiStream(ctx, prepared.Prompt, snap, chunks)
		if err != nil {
			errs <- err
			return
		}
		p.session.MarkMessageSent()
	}()

	return chunks, errs
}

// buildArgs 构建 Gemini CLI 的命令行参数。
//
// 非流式：gemini -y -o json -p -
// 流式：  gemini -y -o stream-json -p -
// 会话续传：追加 --resume <session_id>
// 模型名：--model <name>
func (p *GeminiCliProvider) buildArgs(streaming bool, snap SessionSnapshot) []string {
	args := []string{}
	if p.skipPermissions {
		args = append(args, "-y")
	}
	if streaming {
		args = append(args, "-o", "stream-json")
	} else {
		args = append(args, "-o", "json")
	}
	if p.modelName != "" {
		args = append(args, "--model", p.modelName)
	}
	if p.sessionEnabled && snap.ID != "" {
		args = append(args, "--resume", snap.ID)
	}
	// 从 stdin 读取 prompt
	args = append(args, "-p", "-")
	return args
}

// --- Gemini CLI JSON 响应类型 ---

// geminiResponse 是 gemini -o json 的非流式响应结构。
type geminiResponse struct {
	SessionID string `json:"session_id,omitempty"`
	Response  string `json:"response,omitempty"`
}

// geminiStreamEvent 是 gemini -o stream-json 的 NDJSON 事件。
type geminiStreamEvent struct {
	Type      string `json:"type"`                 // "init" | "message"
	SessionID string `json:"session_id,omitempty"` // init 事件中的会话 ID
	Role      string `json:"role,omitempty"`       // message 事件中的角色
	Content   string `json:"content,omitempty"`    // message 事件中的文本内容
}

// runGemini 以同步方式运行 Gemini CLI。
func (p *GeminiCliProvider) runGemini(ctx context.Context, prompt string, snap SessionSnapshot) (string, error) {
	args := p.buildArgs(false, snap)
	cmd := exec.CommandContext(ctx, "gemini", args...)
	if p.cwd != "" {
		cmd.Dir = p.cwd
	}
	cmd.Stdin = strings.NewReader(prompt)

	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("gemini CLI failed: %w: %s", err, stderr.String())
	}

	// 尝试解析 JSON 响应
	var resp geminiResponse
	if err := json.Unmarshal(stdout.Bytes(), &resp); err != nil {
		// 降级为纯文本
		util.Warnf("gemini CLI output is not valid JSON, falling back to plain text: %v", err)
		return strings.TrimSpace(stdout.String()), nil
	}

	if resp.SessionID != "" && p.sessionEnabled {
		p.session.SetSessionID(resp.SessionID)
	}
	return resp.Response, nil
}

// runGeminiStream 以流式方式运行 Gemini CLI，解析 NDJSON 输出。
func (p *GeminiCliProvider) runGeminiStream(ctx context.Context, prompt string, snap SessionSnapshot, chunks chan<- string) error {
	args := p.buildArgs(true, snap)
	cmd := exec.CommandContext(ctx, "gemini", args...)
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
		return fmt.Errorf("failed to start gemini CLI: %w", err)
	}

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

	// 后台读取 stderr 以更新活动时间戳
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

	// 逐行读取 stdout 并解析 NDJSON 事件
	var lineBuf string
	buf := make([]byte, 4096)
	for {
		n, readErr := stdoutPipe.Read(buf)
		if n > 0 {
			updateActivity()
			lineBuf += string(buf[:n])

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

				p.handleGeminiStreamLine(line, chunks)
			}
		}
		if readErr != nil {
			break
		}
	}

	// 处理缓冲区中剩余的数据
	if trimmed := strings.TrimSpace(lineBuf); trimmed != "" {
		p.handleGeminiStreamLine(trimmed, chunks)
	}

	if err := cmd.Wait(); err != nil {
		return fmt.Errorf("gemini CLI exited with error: %w", err)
	}
	return nil
}

// handleGeminiStreamLine 解析单行 NDJSON 事件。
func (p *GeminiCliProvider) handleGeminiStreamLine(line string, chunks chan<- string) {
	var event geminiStreamEvent
	if err := json.Unmarshal([]byte(line), &event); err != nil {
		util.Warnf("gemini stream: failed to parse NDJSON line: %v", err)
		return
	}

	switch event.Type {
	case "init":
		if event.SessionID != "" && p.sessionEnabled {
			p.session.SetSessionID(event.SessionID)
		}
	case "message":
		if event.Role == "assistant" && event.Content != "" {
			chunks <- event.Content
		}
	}
}
