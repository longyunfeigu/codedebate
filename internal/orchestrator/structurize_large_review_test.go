package orchestrator

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/longyunfeigu/codedebate/internal/provider"
)

type largePromptCaptureProvider struct {
	response  string
	callCount int
	prompt    string
	opts      provider.ChatOptions
}

func (p *largePromptCaptureProvider) Name() string { return "large-prompt-capture" }

func (p *largePromptCaptureProvider) Chat(_ context.Context, msgs []provider.Message, _ string, opts *provider.ChatOptions) (string, error) {
	p.callCount++
	if len(msgs) > 0 {
		p.prompt = msgs[0].Content
	}
	if opts != nil {
		p.opts = *opts
	}
	return p.response, nil
}

func (p *largePromptCaptureProvider) ChatStream(_ context.Context, _ []provider.Message, _ string) (<-chan string, <-chan error) {
	chunks := make(chan string, 1)
	errs := make(chan error, 1)
	close(chunks)
	close(errs)
	return chunks, errs
}

func TestStructurizeIssues_LargeReviewScript_PromptCarriesFullPayload(t *testing.T) {
	resp := "```json\n" + `{
  "issues": [
    {
      "severity": "high",
      "file": "backend/infrastructure/external/vector_store/azure_adapter.py",
      "line": 364,
      "title": "Paged iterator consumed in event loop",
      "description": "list(results_iter) runs in async event loop and may block network I/O.",
      "raisedBy": ["claude", "gpt4o"]
    }
  ]
}` + "\n```"

	p := &largePromptCaptureProvider{response: resp}
	history := realLargeReviewHistory(t)
	o := &DebateOrchestrator{
		summarizer: Reviewer{
			ID:           "summarizer",
			Provider:     p,
			SystemPrompt: "You extract structured issues.",
		},
		conversationHistory: history,
		tokenUsage:          make(map[string]*tokenCount),
	}

	issues := o.structurizeIssues(context.Background(), &noopDisplay{})

	if len(issues) != 1 {
		t.Fatalf("expected 1 issue, got %d", len(issues))
	}
	if p.callCount != 1 {
		t.Fatalf("expected 1 summarizer call, got %d", p.callCount)
	}
	if p.opts.MaxTokens != 32768 {
		t.Fatalf("expected MaxTokens=32768, got %d", p.opts.MaxTokens)
	}
	if !p.opts.DisableTools {
		t.Fatal("expected DisableTools=true for structurizer chat options")
	}

	if len(history) < 2 {
		t.Fatalf("expected at least 2 reviewer messages in history, got %d", len(history))
	}
	assertPromptContainsExcerpt(t, p.prompt, history[0].Content)
	assertPromptContainsExcerpt(t, p.prompt, history[1].Content)
	if !strings.Contains(p.prompt, "Use the exact reviewer IDs: claude, gpt4o") {
		t.Fatal("expected prompt to contain reviewer IDs rule")
	}

	historyBytes := len(history[0].Content) + len(history[1].Content)
	if len(p.prompt) <= historyBytes {
		t.Fatalf("expected prompt to be larger than concatenated history payload; prompt=%d history=%d", len(p.prompt), historyBytes)
	}
	t.Logf("large structurizer prompt bytes=%d", len(p.prompt))
}

func TestStructurizeIssues_LargeReviewScript_PromptSizeScalesWithHistory(t *testing.T) {
	resp := "```json\n" + `{
  "issues": [
    {
      "severity": "medium",
      "file": "backend/infrastructure/tasks/tasks/task_base.py",
      "title": "Embedding path mismatch",
      "description": "API and tasks paths use different embedding resolution strategies.",
      "raisedBy": ["gpt4o"]
    }
  ]
}` + "\n```"

	smallProvider := &largePromptCaptureProvider{response: resp}
	largeProvider := &largePromptCaptureProvider{response: resp}

	small := &DebateOrchestrator{
		summarizer: Reviewer{
			ID:           "summarizer",
			Provider:     smallProvider,
			SystemPrompt: "You extract structured issues.",
		},
		conversationHistory: []DebateMessage{
			{ReviewerID: "claude", Content: "Small review: one issue in azure_adapter.py line 364."},
			{ReviewerID: "gpt4o", Content: "Small review: one issue in task_base.py line 693."},
		},
		tokenUsage: make(map[string]*tokenCount),
	}

	large := &DebateOrchestrator{
		summarizer: Reviewer{
			ID:           "summarizer",
			Provider:     largeProvider,
			SystemPrompt: "You extract structured issues.",
		},
		conversationHistory: realLargeReviewHistory(t),
		tokenUsage:          make(map[string]*tokenCount),
	}

	if issues := small.structurizeIssues(context.Background(), &noopDisplay{}); len(issues) != 1 {
		t.Fatalf("small history: expected 1 issue, got %d", len(issues))
	}
	if issues := large.structurizeIssues(context.Background(), &noopDisplay{}); len(issues) != 1 {
		t.Fatalf("large history: expected 1 issue, got %d", len(issues))
	}

	smallSize := len(smallProvider.prompt)
	largeSize := len(largeProvider.prompt)
	if largeSize <= smallSize*3 {
		t.Fatalf("expected large prompt to be >3x small prompt; small=%d large=%d", smallSize, largeSize)
	}
	t.Logf("prompt size growth: small=%d bytes, large=%d bytes, ratio=%.2f",
		smallSize, largeSize, float64(largeSize)/float64(smallSize))
}

func realLargeReviewHistory(t *testing.T) []DebateMessage {
	t.Helper()
	b, err := os.ReadFile("testdata/structurize_real_input.txt")
	if err != nil {
		t.Fatalf("read real input testdata failed: %v", err)
	}

	raw := string(b)
	claudeSection := raw
	gpt4oSection := raw

	claudeIdx := strings.Index(raw, "[claude] Round 1:")
	gpt4oIdx := strings.Index(raw, "[gpt4o] Round 1:")
	if claudeIdx >= 0 && gpt4oIdx > claudeIdx {
		claudeSection = strings.TrimSpace(raw[claudeIdx:gpt4oIdx])
		gpt4oSection = strings.TrimSpace(raw[gpt4oIdx:])
	}

	return []DebateMessage{
		{ReviewerID: "claude", Content: claudeSection},
		{ReviewerID: "gpt4o", Content: gpt4oSection},
	}
}

func assertPromptContainsExcerpt(t *testing.T, prompt, source string) {
	t.Helper()
	excerpt := firstStableExcerpt(source)
	if excerpt == "" {
		t.Fatal("failed to build non-empty excerpt from source")
	}
	if !strings.Contains(prompt, excerpt) {
		t.Fatalf("expected prompt to contain source excerpt: %q", excerpt)
	}
}

func firstStableExcerpt(source string) string {
	for _, line := range strings.Split(source, "\n") {
		line = strings.TrimSpace(line)
		if len(line) >= 40 {
			return line
		}
	}
	source = strings.TrimSpace(source)
	if len(source) > 120 {
		return source[:120]
	}
	return source
}

// TestStructurizeIssues_RealOpenAI_LargeReview 使用真实 OpenAI API 对大型 review 输入进行结构化提取。
// 需要设置环境变量：OPENAI_API_KEY（必须），OPENAI_BASE_URL（可选），OPENAI_MODEL（可选，默认 gpt-4o）。
// 运行方式：OPENAI_API_KEY=sk-xxx go test -run TestStructurizeIssues_RealOpenAI_LargeReview -v -timeout 120s
func TestStructurizeIssues_RealOpenAI_LargeReview(t *testing.T) {
	apiKey := os.Getenv("OPENAI_API_KEY")
	if apiKey == "" {
		t.Skip("OPENAI_API_KEY not set, skipping real OpenAI integration test")
	}
	baseURL := os.Getenv("OPENAI_BASE_URL")
	model := os.Getenv("OPENAI_MODEL")
	if model == "" {
		model = "gpt-4o"
	}

	p := provider.NewOpenAIProvider(apiKey, model, baseURL)
	history := realLargeReviewHistory(t)

	o := &DebateOrchestrator{
		summarizer: Reviewer{
			ID:           "summarizer",
			Provider:     p,
			SystemPrompt: "You extract structured issues.",
		},
		conversationHistory: history,
		tokenUsage:          make(map[string]*tokenCount),
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()

	issues := o.structurizeIssues(ctx, &noopDisplay{})

	if len(issues) == 0 {
		t.Fatal("expected at least 1 issue from real OpenAI structurization, got 0")
	}

	t.Logf("real OpenAI returned %d issues (model=%s)", len(issues), model)
	for i, issue := range issues {
		t.Logf("  [%d] severity=%s file=%s title=%s raisedBy=%v",
			i, issue.Severity, issue.File, issue.Title, issue.RaisedBy)
	}
}
