package provider

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestBuildMessages(t *testing.T) {
	messages := []Message{
		{Role: "user", Content: "Hello"},
		{Role: "assistant", Content: "Hi there"},
		{Role: "user", Content: "How are you?"},
	}

	t.Run("with system prompt", func(t *testing.T) {
		msgs := buildMessages(messages, "You are helpful.")
		if len(msgs) != 4 {
			t.Fatalf("expected 4 messages, got %d", len(msgs))
		}
		if msgs[0].Role != "system" || msgs[0].Content != "You are helpful." {
			t.Errorf("first message should be system prompt, got %+v", msgs[0])
		}
		if msgs[1].Role != "user" || msgs[1].Content != "Hello" {
			t.Errorf("second message mismatch: %+v", msgs[1])
		}
	})

	t.Run("without system prompt", func(t *testing.T) {
		msgs := buildMessages(messages, "")
		if len(msgs) != 3 {
			t.Fatalf("expected 3 messages, got %d", len(msgs))
		}
		if msgs[0].Role != "user" {
			t.Errorf("first message should be user, got %s", msgs[0].Role)
		}
	})
}

func TestOpenAIProviderName(t *testing.T) {
	p := NewOpenAIProvider("key", "gpt-4o", "")
	if p.Name() != "openai" {
		t.Errorf("expected name 'openai', got %q", p.Name())
	}
}

func TestDefaultBaseURL(t *testing.T) {
	p := NewOpenAIProvider("key", "gpt-4o", "")
	if p.baseURL != "https://api.openai.com/v1" {
		t.Errorf("expected default base URL 'https://api.openai.com/v1', got %q", p.baseURL)
	}
}

func TestCustomBaseURL(t *testing.T) {
	p := NewOpenAIProvider("key", "gpt-4o", "https://custom.api.com/")
	if p.baseURL != "https://custom.api.com" {
		t.Errorf("expected trailing slash trimmed, got %q", p.baseURL)
	}
}

func TestChatResponse(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// 验证请求
		if r.Method != http.MethodPost {
			t.Errorf("expected POST, got %s", r.Method)
		}
		if r.URL.Path != "/chat/completions" {
			t.Errorf("unexpected path: %s", r.URL.Path)
		}
		if r.Header.Get("Authorization") != "Bearer test-key" {
			t.Errorf("unexpected auth header: %s", r.Header.Get("Authorization"))
		}

		// 验证请求体
		var req openaiRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Fatalf("failed to decode request: %v", err)
		}
		if req.Model != "gpt-4o" {
			t.Errorf("expected model gpt-4o, got %s", req.Model)
		}
		if req.Stream {
			t.Error("expected stream=false for Chat")
		}
		if len(req.Messages) != 2 {
			t.Errorf("expected 2 messages, got %d", len(req.Messages))
		}

		resp := openaiResponse{
			Choices: []openaiChoice{
				{Message: openaiMessage{Role: "assistant", Content: "Hello from GPT!"}},
			},
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	p := NewOpenAIProvider("test-key", "gpt-4o", server.URL)
	result, err := p.Chat(context.Background(), []Message{
		{Role: "user", Content: "Hi"},
	}, "Be helpful.", nil)

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result != "Hello from GPT!" {
		t.Errorf("expected 'Hello from GPT!', got %q", result)
	}
}

func TestChatAPIError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		w.Write([]byte(`{"error":{"message":"Invalid API key","type":"invalid_request_error"}}`))
	}))
	defer server.Close()

	p := NewOpenAIProvider("bad-key", "gpt-4o", server.URL)
	_, err := p.Chat(context.Background(), []Message{
		{Role: "user", Content: "Hi"},
	}, "", nil)

	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !strings.Contains(err.Error(), "401") {
		t.Errorf("expected HTTP 401 in error, got: %v", err)
	}
}

func TestChatNoChoices(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		resp := openaiResponse{Choices: []openaiChoice{}}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	p := NewOpenAIProvider("key", "gpt-4o", server.URL)
	_, err := p.Chat(context.Background(), []Message{
		{Role: "user", Content: "Hi"},
	}, "", nil)

	if err == nil {
		t.Fatal("expected error for no choices")
	}
	if !strings.Contains(err.Error(), "no choices") {
		t.Errorf("expected 'no choices' error, got: %v", err)
	}
}

func TestChatStreamResponse(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req openaiRequest
		json.NewDecoder(r.Body).Decode(&req)
		if !req.Stream {
			t.Error("expected stream=true for ChatStream")
		}

		w.Header().Set("Content-Type", "text/event-stream")
		flusher, ok := w.(http.Flusher)
		if !ok {
			t.Fatal("expected ResponseWriter to be a Flusher")
		}

		chunks := []string{"Hello", " from", " streaming", "!"}
		for _, c := range chunks {
			chunk := openaiStreamChunk{
				Choices: []openaiStreamChoice{
					{Delta: openaiDelta{Content: c}},
				},
			}
			data, _ := json.Marshal(chunk)
			fmt.Fprintf(w, "data: %s\n\n", data)
			flusher.Flush()
		}
		fmt.Fprintf(w, "data: [DONE]\n\n")
		flusher.Flush()
	}))
	defer server.Close()

	p := NewOpenAIProvider("test-key", "gpt-4o", server.URL)
	chunks, errs := p.ChatStream(context.Background(), []Message{
		{Role: "user", Content: "Hi"},
	}, "Be helpful.")

	var result strings.Builder
	for chunk := range chunks {
		result.WriteString(chunk)
	}

	// 检查是否有错误
	for err := range errs {
		t.Fatalf("unexpected stream error: %v", err)
	}

	if result.String() != "Hello from streaming!" {
		t.Errorf("expected 'Hello from streaming!', got %q", result.String())
	}
}

func TestChatStreamHTTPError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusTooManyRequests)
		w.Write([]byte(`{"error":{"message":"Rate limit exceeded"}}`))
	}))
	defer server.Close()

	p := NewOpenAIProvider("key", "gpt-4o", server.URL)
	chunks, errs := p.ChatStream(context.Background(), []Message{
		{Role: "user", Content: "Hi"},
	}, "")

	// Drain chunks
	for range chunks {
	}

	var gotErr error
	for err := range errs {
		gotErr = err
	}

	if gotErr == nil {
		t.Fatal("expected error for HTTP 429")
	}
	if !strings.Contains(gotErr.Error(), "429") {
		t.Errorf("expected 429 in error, got: %v", gotErr)
	}
}

func TestReasoningEffortInRequest(t *testing.T) {
	t.Run("reasoning_effort included when set", func(t *testing.T) {
		var gotReq openaiRequest
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			json.NewDecoder(r.Body).Decode(&gotReq)
			resp := openaiResponse{
				Choices: []openaiChoice{
					{Message: openaiMessage{Role: "assistant", Content: "ok"}},
				},
			}
			json.NewEncoder(w).Encode(resp)
		}))
		defer server.Close()

		p := NewOpenAIProvider("key", "gpt-5.2", server.URL)
		p.reasoningEffort = "medium"

		_, err := p.Chat(context.Background(), []Message{
			{Role: "user", Content: "test"},
		}, "", nil)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if gotReq.ReasoningEffort != "medium" {
			t.Errorf("expected reasoning_effort=medium, got %q", gotReq.ReasoningEffort)
		}
	})

	t.Run("reasoning_effort omitted when empty", func(t *testing.T) {
		var rawBody map[string]interface{}
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			json.NewDecoder(r.Body).Decode(&rawBody)
			resp := openaiResponse{
				Choices: []openaiChoice{
					{Message: openaiMessage{Role: "assistant", Content: "ok"}},
				},
			}
			json.NewEncoder(w).Encode(resp)
		}))
		defer server.Close()

		p := NewOpenAIProvider("key", "gpt-4o", server.URL)

		_, err := p.Chat(context.Background(), []Message{
			{Role: "user", Content: "test"},
		}, "", nil)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if _, exists := rawBody["reasoning_effort"]; exists {
			t.Error("reasoning_effort should be omitted from JSON when empty")
		}
	})

	t.Run("reasoning_effort in stream request", func(t *testing.T) {
		var gotReq openaiRequest
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			json.NewDecoder(r.Body).Decode(&gotReq)
			w.Header().Set("Content-Type", "text/event-stream")
			fmt.Fprintf(w, "data: [DONE]\n\n")
		}))
		defer server.Close()

		p := NewOpenAIProvider("key", "gpt-5.2", server.URL)
		p.reasoningEffort = "high"

		chunks, errs := p.ChatStream(context.Background(), []Message{
			{Role: "user", Content: "test"},
		}, "")
		for range chunks {
		}
		for range errs {
		}

		if gotReq.ReasoningEffort != "high" {
			t.Errorf("expected reasoning_effort=high in stream, got %q", gotReq.ReasoningEffort)
		}
	})
}

func TestChatMaxTokensParamSelection(t *testing.T) {
	tests := []struct {
		name              string
		model             string
		wantMaxTokens     bool
		wantMaxCompletion bool
	}{
		{
			name:              "legacy model uses max_tokens",
			model:             "gpt-4o",
			wantMaxTokens:     true,
			wantMaxCompletion: false,
		},
		{
			name:              "gpt-5 model uses max_completion_tokens",
			model:             "gpt-5.2",
			wantMaxTokens:     false,
			wantMaxCompletion: true,
		},
		{
			name:              "o1-mini uses max_completion_tokens",
			model:             "o1-mini",
			wantMaxTokens:     false,
			wantMaxCompletion: true,
		},
		{
			name:              "o3 uses max_completion_tokens",
			model:             "o3",
			wantMaxTokens:     false,
			wantMaxCompletion: true,
		},
		{
			name:              "o3-mini uses max_completion_tokens",
			model:             "o3-mini",
			wantMaxTokens:     false,
			wantMaxCompletion: true,
		},
		{
			name:              "gpt-4.1 uses max_completion_tokens",
			model:             "gpt-4.1-mini",
			wantMaxTokens:     false,
			wantMaxCompletion: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var rawBody map[string]interface{}
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if err := json.NewDecoder(r.Body).Decode(&rawBody); err != nil {
					t.Fatalf("failed to decode request body: %v", err)
				}
				resp := openaiResponse{
					Choices: []openaiChoice{
						{Message: openaiMessage{Role: "assistant", Content: "ok"}},
					},
				}
				json.NewEncoder(w).Encode(resp)
			}))
			defer server.Close()

			p := NewOpenAIProvider("key", tt.model, server.URL)
			_, err := p.Chat(context.Background(), []Message{{Role: "user", Content: "test"}}, "", &ChatOptions{MaxTokens: 2048})
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}

			_, hasMaxTokens := rawBody["max_tokens"]
			_, hasMaxCompletion := rawBody["max_completion_tokens"]
			if hasMaxTokens != tt.wantMaxTokens {
				t.Fatalf("max_tokens present = %v, want %v; body=%v", hasMaxTokens, tt.wantMaxTokens, rawBody)
			}
			if hasMaxCompletion != tt.wantMaxCompletion {
				t.Fatalf("max_completion_tokens present = %v, want %v; body=%v", hasMaxCompletion, tt.wantMaxCompletion, rawBody)
			}
		})
	}
}
