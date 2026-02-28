package provider

import (
	"context"
	"fmt"
	"strings"
	"sync"

	"github.com/longyunfeigu/codedebate/internal/config"
)

// CreateProvider 是 AI 提供者的工厂函数，根据模型名称和配置创建对应的 AIProvider 实例。
// 支持的模型名称：
//   - "claude-code"：使用 Claude Code CLI 作为后端
//   - "codex-cli"：使用 OpenAI Codex CLI 作为后端
//   - "gemini-cli"：使用 Gemini CLI 作为后端
//   - "mock*"：使用模拟提供者（用于测试）
//
// providerName 为可选的显式 provider 名称（如 "openrouter"、"minimax"），
// 用于指定 OpenAI-compatible API provider。
// modelName 为可选的底层模型名称（如 "claude-sonnet-4-5-20250514"），
// 对 CLI 提供者会通过 --model 参数传递。
// 如果全局 Mock 模式开启，则所有模型都会被替换为 MockProvider。
func CreateProvider(model, modelName, reasoningEffort, providerName string, cfg *config.CodeDebateConfig) (AIProvider, error) {
	// 全局模拟模式：将所有模型替换为 MockProvider，用于测试和开发
	if cfg.Mock {
		return NewMockProvider(), nil
	}

	// 从配置中读取是否跳过 CLI 权限提示
	skipPerms := cfg.Defaults.SkipPermissions != nil && *cfg.Defaults.SkipPermissions

	switch {
	case model == "claude-code":
		// 创建 Claude Code CLI 提供者，通过调用 claude 命令行工具进行交互
		p := NewClaudeCodeProvider()
		p.skipPermissions = skipPerms
		p.modelName = modelName
		p.promptSizeThreshold = cfg.Defaults.PromptSizeThreshold
		return p, nil
	case model == "codex-cli":
		// 创建 Codex CLI 提供者，通过调用 codex 命令行工具进行交互
		p := NewCodexCliProvider()
		p.skipPermissions = skipPerms
		p.modelName = modelName
		p.promptSizeThreshold = cfg.Defaults.PromptSizeThreshold
		return p, nil
	case model == "gemini-cli":
		// 创建 Gemini CLI 提供者，通过调用 gemini 命令行工具进行交互
		p := NewGeminiCliProvider()
		p.skipPermissions = skipPerms
		p.modelName = modelName
		p.promptSizeThreshold = cfg.Defaults.PromptSizeThreshold
		return p, nil
	case providerName != "":
		// 显式指定 provider 名称：使用对应的 OpenAI-compatible API
		provCfg, exists := cfg.Providers[providerName]
		if !exists {
			return nil, fmt.Errorf("provider %q not found in config (providers.%s)", providerName, providerName)
		}
		if provCfg.APIKey == "" {
			return nil, fmt.Errorf("provider %q requires api_key in config (providers.%s.api_key)", providerName, providerName)
		}
		p := NewOpenAIProvider(provCfg.APIKey, model, provCfg.BaseURL)
		p.reasoningEffort = reasoningEffort
		return p, nil
	case strings.HasPrefix(model, "gpt-"),
		strings.HasPrefix(model, "o1-"),
		strings.HasPrefix(model, "o3-"),
		strings.HasPrefix(model, "o4-"):
		// OpenAI 模型（gpt-4o, gpt-5.2, o1-*, o3-*, o4-* 等），使用 OpenAI API 直接调用
		provCfg := cfg.Providers["openai"]
		if provCfg.APIKey == "" {
			return nil, fmt.Errorf("openai provider requires api_key in config (providers.openai.api_key)")
		}
		p := NewOpenAIProvider(provCfg.APIKey, model, provCfg.BaseURL)
		p.reasoningEffort = reasoningEffort
		return p, nil
	case strings.HasPrefix(model, "mock"):
		// 以 "mock" 开头的模型名称均使用模拟提供者
		return NewMockProvider(), nil
	default:
		return nil, fmt.Errorf("unknown provider model: %s", model)
	}
}

// MockProvider 是用于测试的模拟 AI 提供者。
// 它不调用任何外部 CLI 工具，而是返回预设的固定响应，
// 通过轮询方式循环返回不同的测试响应。线程安全。
type MockProvider struct {
	mu        sync.Mutex // 保护并发访问的互斥锁
	responses []string   // 预设的模拟响应列表
	callCount int        // 调用计数器，用于轮询选择响应
}

// NewMockProvider 创建一个带有默认测试响应的 MockProvider。
// 包含三个预设响应，模拟真实的审查、分析和汇总结果。
func NewMockProvider() *MockProvider {
	return &MockProvider{
		responses: []string{
			"This is a mock response for testing.",
			"Mock review: The code looks good overall.",
			"Mock summary: No critical issues found.",
		},
	}
}

// Name 返回提供者名称 "mock"。
func (p *MockProvider) Name() string { return "mock" }

// Chat 实现同步聊天接口。按轮询顺序返回预设响应，线程安全。
func (p *MockProvider) Chat(_ context.Context, _ []Message, _ string, _ *ChatOptions) (string, error) {
	p.mu.Lock()
	defer p.mu.Unlock()

	// 通过取模运算循环使用预设响应
	idx := p.callCount % len(p.responses)
	p.callCount++
	return p.responses[idx], nil
}

// ChatStream 实现流式聊天接口。将预设响应作为单个数据块发送到 channel 中。
func (p *MockProvider) ChatStream(_ context.Context, _ []Message, _ string) (<-chan string, <-chan error) {
	chunks := make(chan string, 1)
	errs := make(chan error, 1)

	go func() {
		defer close(chunks)
		defer close(errs)

		p.mu.Lock()
		idx := p.callCount % len(p.responses)
		p.callCount++
		resp := p.responses[idx]
		p.mu.Unlock()

		chunks <- resp
	}()

	return chunks, errs
}

// 以下是 MockProvider 对 SessionProvider 接口的空实现。
// MockProvider 不需要真正的会话管理，因此所有方法都是空操作或返回固定值。
func (p *MockProvider) StartSession(_ string)        {} // 启动会话（空操作）
func (p *MockProvider) EndSession()                   {} // 结束会话（空操作）
func (p *MockProvider) SessionID() string             { return "mock-session" } // 返回固定的模拟会话 ID
func (p *MockProvider) IsFirstMessage() bool          { return true }           // 始终返回 true
func (p *MockProvider) MarkMessageSent()              {}                        // 标记消息已发送（空操作）
func (p *MockProvider) ShouldSendFullHistory() bool   { return true }           // 始终发送完整历史
