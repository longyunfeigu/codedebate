// openai.go 实现了基于 OpenAI Chat Completions API 的无状态 AI 提供者。
// 直接通过 net/http 调用 API，无需外部 SDK 依赖。
// 支持自定义 baseURL，兼容 Azure OpenAI、Ollama 等 OpenAI 兼容 API。
package provider

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
)

const defaultOpenAIBaseURL = "https://api.openai.com/v1"

// OpenAIProvider 通过 OpenAI Chat Completions API 进行交互的无状态提供者。
// 每次调用发送完整消息历史，不维护会话状态。
type OpenAIProvider struct {
	apiKey          string
	model           string
	baseURL         string
	client          *http.Client
	reasoningEffort string // 推理深度（none|low|medium|high|xhigh），空值表示不发送此参数
}

// NewOpenAIProvider 创建一个新的 OpenAI API 提供者。
// 如果 baseURL 为空，则使用默认的 https://api.openai.com。
func NewOpenAIProvider(apiKey, model, baseURL string) *OpenAIProvider {
	if baseURL == "" {
		baseURL = defaultOpenAIBaseURL
	}
	// 去除尾部斜杠，保证 URL 拼接一致
	baseURL = strings.TrimRight(baseURL, "/")
	return &OpenAIProvider{
		apiKey:  apiKey,
		model:   model,
		baseURL: baseURL,
		client:  &http.Client{},
	}
}

// Name 返回提供者名称 "openai"。
func (p *OpenAIProvider) Name() string { return "openai" }

// openaiRequest 是发送给 OpenAI API 的请求体结构。
type openaiRequest struct {
	Model           string          `json:"model"`
	Messages        []openaiMessage `json:"messages"`
	Stream          bool            `json:"stream"`
	MaxTokens       int             `json:"max_tokens,omitempty"`            // 最大输出 token 数（0 表示使用模型默认值）
	MaxCompletion   int             `json:"max_completion_tokens,omitempty"` // gpt-5 系列使用的最大输出 token 参数
	ReasoningEffort string          `json:"reasoning_effort,omitempty"`      // 推理深度，仅推理模型有效
}

// openaiMessage 是 OpenAI API 消息格式。
type openaiMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

// openaiResponse 是 OpenAI API 非流式响应的结构。
type openaiResponse struct {
	Choices []openaiChoice `json:"choices"`
	Error   *openaiError   `json:"error,omitempty"`
}

type openaiChoice struct {
	Message openaiMessage `json:"message"`
}

type openaiError struct {
	Message string `json:"message"`
	Type    string `json:"type"`
}

// openaiStreamChunk 是 SSE 流中每个 data chunk 的结构。
type openaiStreamChunk struct {
	Choices []openaiStreamChoice `json:"choices"`
}

type openaiStreamChoice struct {
	Delta openaiDelta `json:"delta"`
}

type openaiDelta struct {
	Content string `json:"content"`
}

// buildMessages 将 systemPrompt 和 messages 合并为 OpenAI API 消息格式。
func buildMessages(messages []Message, systemPrompt string) []openaiMessage {
	var msgs []openaiMessage
	if systemPrompt != "" {
		msgs = append(msgs, openaiMessage{Role: "system", Content: systemPrompt})
	}
	for _, m := range messages {
		msgs = append(msgs, openaiMessage{Role: m.Role, Content: m.Content})
	}
	return msgs
}

// Chat 发送消息并等待完整响应（同步）。使用 WithRetry 进行重试。
func (p *OpenAIProvider) Chat(ctx context.Context, messages []Message, systemPrompt string, opts *ChatOptions) (string, error) {
	return WithRetry(func() (string, error) {
		return p.doChat(ctx, messages, systemPrompt, opts)
	}, nil)
}

// doChat 执行实际的 API 调用（非流式）。
func (p *OpenAIProvider) doChat(ctx context.Context, messages []Message, systemPrompt string, opts *ChatOptions) (string, error) {
	reqBody := openaiRequest{
		Model:           p.model,
		Messages:        buildMessages(messages, systemPrompt),
		Stream:          false,
		ReasoningEffort: p.reasoningEffort,
	}
	if opts != nil && opts.MaxTokens > 0 {
		if usesMaxCompletionTokens(p.model) {
			reqBody.MaxCompletion = opts.MaxTokens
		} else {
			reqBody.MaxTokens = opts.MaxTokens
		}
	}

	body, err := json.Marshal(reqBody)
	if err != nil {
		return "", fmt.Errorf("failed to marshal request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, p.baseURL+"/chat/completions", bytes.NewReader(body))
	if err != nil {
		return "", fmt.Errorf("failed to create request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+p.apiKey)

	resp, err := p.client.Do(req)
	if err != nil {
		return "", fmt.Errorf("openai api request failed: %w", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("failed to read response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("openai api error (HTTP %d): %s", resp.StatusCode, string(respBody))
	}

	var result openaiResponse
	if err := json.Unmarshal(respBody, &result); err != nil {
		return "", fmt.Errorf("failed to parse response: %w", err)
	}

	if result.Error != nil {
		return "", fmt.Errorf("openai api error: %s", result.Error.Message)
	}

	if len(result.Choices) == 0 {
		return "", fmt.Errorf("openai api returned no choices")
	}

	return result.Choices[0].Message.Content, nil
}

// usesMaxCompletionTokens 判断模型是否应使用 max_completion_tokens 参数。
// 以下模型系列使用 max_completion_tokens 而非 max_tokens：
//   - o1* / o3* / o4*（推理模型，如 o1-mini, o3, o3-mini, o4-mini）
//   - gpt-4.1 系列
//   - gpt-5 系列
func usesMaxCompletionTokens(model string) bool {
	model = strings.ToLower(strings.TrimSpace(model))
	for _, prefix := range []string{"o1", "o3", "o4", "gpt-5", "gpt-4.1"} {
		if strings.HasPrefix(model, prefix) {
			return true
		}
	}
	return false
}

// ChatStream 发送消息并以流式方式返回响应片段（异步）。
// 使用 SSE (Server-Sent Events) 协议解析流式响应。
func (p *OpenAIProvider) ChatStream(ctx context.Context, messages []Message, systemPrompt string) (<-chan string, <-chan error) {
	chunks := make(chan string, 64)
	errs := make(chan error, 1)

	go func() {
		defer close(chunks)
		defer close(errs)

		reqBody := openaiRequest{
			Model:           p.model,
			Messages:        buildMessages(messages, systemPrompt),
			Stream:          true,
			ReasoningEffort: p.reasoningEffort,
		}

		body, err := json.Marshal(reqBody)
		if err != nil {
			errs <- fmt.Errorf("failed to marshal request: %w", err)
			return
		}

		req, err := http.NewRequestWithContext(ctx, http.MethodPost, p.baseURL+"/chat/completions", bytes.NewReader(body))
		if err != nil {
			errs <- fmt.Errorf("failed to create request: %w", err)
			return
		}
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Authorization", "Bearer "+p.apiKey)

		resp, err := p.client.Do(req)
		if err != nil {
			errs <- fmt.Errorf("openai api stream request failed: %w", err)
			return
		}
		defer resp.Body.Close()

		if resp.StatusCode != http.StatusOK {
			respBody, _ := io.ReadAll(resp.Body)
			errs <- fmt.Errorf("openai api error (HTTP %d): %s", resp.StatusCode, string(respBody))
			return
		}

		scanner := bufio.NewScanner(resp.Body)
		for scanner.Scan() {
			line := scanner.Text()

			// 跳过空行和非 data 行
			if !strings.HasPrefix(line, "data: ") {
				continue
			}

			data := strings.TrimPrefix(line, "data: ")

			// 流结束标记
			if data == "[DONE]" {
				return
			}

			var chunk openaiStreamChunk
			if err := json.Unmarshal([]byte(data), &chunk); err != nil {
				continue // 跳过无法解析的 chunk
			}

			if len(chunk.Choices) > 0 && chunk.Choices[0].Delta.Content != "" {
				select {
				case chunks <- chunk.Choices[0].Delta.Content:
				case <-ctx.Done():
					errs <- ctx.Err()
					return
				}
			}
		}

		if err := scanner.Err(); err != nil {
			errs <- fmt.Errorf("stream read error: %w", err)
		}
	}()

	return chunks, errs
}
