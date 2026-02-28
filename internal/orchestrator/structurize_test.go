package orchestrator

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"testing"

	"github.com/longyunfeigu/codedebate/internal/provider"
)

// mockProvider 用于控制 summarizer 返回的内容
type mockProvider struct {
	mu        sync.Mutex
	responses []string
	callCount int
}

func (p *mockProvider) Name() string { return "mock" }
func (p *mockProvider) Chat(_ context.Context, _ []provider.Message, _ string, _ *provider.ChatOptions) (string, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.callCount >= len(p.responses) {
		return "", fmt.Errorf("no more responses")
	}
	resp := p.responses[p.callCount]
	p.callCount++
	return resp, nil
}
func (p *mockProvider) ChatStream(_ context.Context, _ []provider.Message, _ string) (<-chan string, <-chan error) {
	ch := make(chan string, 1)
	errs := make(chan error, 1)
	close(ch)
	close(errs)
	return ch, errs
}
func (p *mockProvider) StartSession(_ string) error { return nil }
func (p *mockProvider) EndSession() error           { return nil }
func (p *mockProvider) HasSession() bool            { return false }
func (p *mockProvider) ShouldSendFullHistory() bool { return true }
func (p *mockProvider) LastSeenIndex() int          { return 0 }
func (p *mockProvider) SetLastSeenIndex(_ int)      {}

// noopDisplay 实现 DisplayCallbacks 的空操作版本
type noopDisplay struct{}

func (d *noopDisplay) OnWaiting(_ string)                         {}
func (d *noopDisplay) OnMessageChunk(_ string, _ string)          {}
func (d *noopDisplay) OnMessage(_ string, _ string)               {}
func (d *noopDisplay) OnParallelStatus(_ int, _ []ReviewerStatus) {}
func (d *noopDisplay) OnSummaryStatus(_ []ReviewerStatus)         {}
func (d *noopDisplay) OnRoundComplete(_ int, _ bool)              {}
func (d *noopDisplay) OnConvergenceJudgment(_ string, _ string)   {}
func (d *noopDisplay) OnContextGathered(_ *GatheredContext)       {}

func makeOrchestrator(summarizerResponses []string, history []DebateMessage) *DebateOrchestrator {
	mp := &mockProvider{responses: summarizerResponses}
	o := &DebateOrchestrator{
		summarizer: Reviewer{
			ID:           "summarizer",
			Provider:     mp,
			SystemPrompt: "You extract structured issues.",
		},
		conversationHistory: history,
		tokenUsage:          make(map[string]*tokenCount),
	}
	return o
}

// --- 测试 structurizeIssues 成功场景 ---

func TestStructurizeIssues_ValidJSON(t *testing.T) {
	// 模拟 summarizer 返回正确的 JSON
	jsonResp := "```json\n" + `{
  "issues": [
    {
      "severity": "high",
      "category": "security",
      "file": "base_parser.py",
      "line": 42,
      "title": "Deferred import of settings",
      "description": "Hidden dependency in base class",
      "suggestedFix": "Inject via constructor",
      "raisedBy": ["claude", "gpt4o"]
    },
    {
      "severity": "medium",
      "category": "architecture",
      "file": "markdown_parser.py",
      "title": "Duplicated fallback pattern",
      "description": "Same 10-line block copy-pasted across 3 parsers",
      "raisedBy": ["claude"]
    }
  ]
}` + "\n```"

	history := []DebateMessage{
		{ReviewerID: "claude", Content: "Round 3 review with findings..."},
		{ReviewerID: "gpt4o", Content: "Round 3 review with findings..."},
	}

	o := makeOrchestrator([]string{jsonResp}, history)
	issues := o.structurizeIssues(context.Background(), &noopDisplay{})

	if len(issues) != 2 {
		t.Fatalf("expected 2 issues, got %d", len(issues))
	}
	if issues[0].Severity != "high" {
		t.Errorf("issues[0].Severity = %q, want %q", issues[0].Severity, "high")
	}
	if issues[0].File != "base_parser.py" {
		t.Errorf("issues[0].File = %q, want %q", issues[0].File, "base_parser.py")
	}
	if len(issues[0].RaisedBy) != 2 {
		t.Errorf("issues[0].RaisedBy = %v, want 2 reviewers", issues[0].RaisedBy)
	}
	if issues[1].File != "markdown_parser.py" {
		t.Errorf("issues[1].File = %q, want %q", issues[1].File, "markdown_parser.py")
	}
}

// --- 测试 summarizer 第一次没返回 JSON，重试成功 ---

func TestStructurizeIssues_RetrySuccess(t *testing.T) {
	badResp := "I found several issues in the code review. Here's a summary..."
	goodResp := "```json\n" + `{"issues": [{"severity": "low", "category": "style", "file": "main.go", "title": "Unused import", "description": "Import os is unused", "raisedBy": ["claude"]}]}` + "\n```"

	history := []DebateMessage{
		{ReviewerID: "claude", Content: "Some review content"},
	}

	o := makeOrchestrator([]string{badResp, goodResp}, history)
	issues := o.structurizeIssues(context.Background(), &noopDisplay{})

	if len(issues) != 1 {
		t.Fatalf("expected 1 issue after retry, got %d", len(issues))
	}
	if issues[0].Title != "Unused import" {
		t.Errorf("issues[0].Title = %q, want %q", issues[0].Title, "Unused import")
	}
}

func TestStructurizeIssues_SchemaErrorsRetryAndKeepBest(t *testing.T) {
	firstResp := "```json\n" + `{
  "issues": [
    {"severity": "high", "file": "a.go", "title": "Valid one", "description": "valid desc"},
    {"severity": "invalid-severity", "file": "b.go", "title": "Invalid one", "description": "invalid desc"}
  ]
}` + "\n```"
	secondResp := "```json\n" + `{
  "issues": [
    {"severity": "high", "file": "a.go", "title": "Valid one", "description": "valid desc"},
    {"severity": "medium", "file": "c.go", "title": "Valid two", "description": "another desc"}
  ]
}` + "\n```"

	mp := &mockProvider{responses: []string{firstResp, secondResp}}
	o := &DebateOrchestrator{
		summarizer: Reviewer{ID: "summarizer", Provider: mp, SystemPrompt: "extract"},
		conversationHistory: []DebateMessage{
			{ReviewerID: "claude", Content: "review text"},
		},
		tokenUsage: make(map[string]*tokenCount),
	}

	issues := o.structurizeIssues(context.Background(), &noopDisplay{})
	if len(issues) != 2 {
		t.Fatalf("expected 2 issues after retry, got %d", len(issues))
	}
	if mp.callCount != 2 {
		t.Fatalf("expected 2 calls (retry after schema error), got %d", mp.callCount)
	}
}

// --- 测试 summarizer 3 次都没返回有效 JSON ---

func TestStructurizeIssues_AllRetriesFail(t *testing.T) {
	history := []DebateMessage{
		{ReviewerID: "claude", Content: "Some review content"},
	}

	o := makeOrchestrator([]string{
		"No JSON here",
		"Still no JSON",
		"Third time still no JSON",
	}, history)
	issues := o.structurizeIssues(context.Background(), &noopDisplay{})

	if issues != nil {
		t.Errorf("expected nil issues after all retries fail, got %d issues", len(issues))
	}
}

// --- 测试空对话历史 ---

func TestStructurizeIssues_EmptyHistory(t *testing.T) {
	o := makeOrchestrator([]string{"should not be called"}, nil)
	issues := o.structurizeIssues(context.Background(), &noopDisplay{})

	if issues != nil {
		t.Errorf("expected nil issues for empty history, got %v", issues)
	}
}

// --- 测试只有 user 消息（过滤后为空） ---

func TestStructurizeIssues_OnlyUserMessages(t *testing.T) {
	history := []DebateMessage{
		{ReviewerID: "user", Content: "Please review this code"},
	}
	o := makeOrchestrator([]string{"should not be called"}, history)
	issues := o.structurizeIssues(context.Background(), &noopDisplay{})

	if issues != nil {
		t.Errorf("expected nil issues when only user messages, got %v", issues)
	}
}

// --- 测试多轮消息全部包含在 reviewText 中，不会丢失早期轮次 ---

func TestStructurizeIssues_IncludesAllRounds(t *testing.T) {
	var receivedPrompt string
	mp := &mockProvider{
		responses: []string{
			"```json\n" + `{"issues": [{"severity": "low", "category": "style", "file": "a.go", "title": "test", "description": "test desc", "raisedBy": ["claude"]}]}` + "\n```",
		},
	}

	captureProvider := &promptCaptureProvider{inner: mp, captured: &receivedPrompt}

	o := &DebateOrchestrator{
		summarizer: Reviewer{
			ID:           "summarizer",
			Provider:     captureProvider,
			SystemPrompt: "extract",
		},
		conversationHistory: []DebateMessage{
			{ReviewerID: "claude", Content: "Round 1: found SQL injection and XSS"},
			{ReviewerID: "gpt4o", Content: "Round 1: found N+1 query"},
			{ReviewerID: "claude", Content: "Round 2: agree on N+1, also found JWT issue"},
			{ReviewerID: "gpt4o", Content: "Round 2: agree on SQL injection"},
		},
		tokenUsage: make(map[string]*tokenCount),
	}

	o.structurizeIssues(context.Background(), &noopDisplay{})

	if receivedPrompt == "" {
		t.Fatal("no prompt was captured")
	}
	// 验证所有轮次的内容都被包含
	if !containsStr(receivedPrompt, "SQL injection and XSS") {
		t.Error("expected prompt to contain Round 1 content from claude")
	}
	if !containsStr(receivedPrompt, "found N+1 query") {
		t.Error("expected prompt to contain Round 1 content from gpt4o")
	}
	if !containsStr(receivedPrompt, "JWT issue") {
		t.Error("expected prompt to contain Round 2 content from claude")
	}
	if !containsStr(receivedPrompt, "agree on SQL injection") {
		t.Error("expected prompt to contain Round 2 content from gpt4o")
	}
	// 验证多轮消息带有 Round 标注
	if !containsStr(receivedPrompt, "Round 1") {
		t.Error("expected prompt to contain Round labels for multi-round reviewers")
	}
	if !containsStr(receivedPrompt, "Round 2") {
		t.Error("expected prompt to contain Round 2 label")
	}
}

// --- 测试单轮场景不带 Round 标注 ---

func TestStructurizeIssues_SingleRoundNoRoundLabel(t *testing.T) {
	var receivedPrompt string
	mp := &mockProvider{
		responses: []string{
			"```json\n" + `{"issues": [{"severity": "low", "category": "style", "file": "a.go", "title": "test", "description": "desc", "raisedBy": ["claude"]}]}` + "\n```",
		},
	}

	captureProvider := &promptCaptureProvider{inner: mp, captured: &receivedPrompt}

	o := &DebateOrchestrator{
		summarizer: Reviewer{
			ID:           "summarizer",
			Provider:     captureProvider,
			SystemPrompt: "extract",
		},
		conversationHistory: []DebateMessage{
			{ReviewerID: "claude", Content: "Only one round of review"},
		},
		tokenUsage: make(map[string]*tokenCount),
	}

	o.structurizeIssues(context.Background(), &noopDisplay{})

	if receivedPrompt == "" {
		t.Fatal("no prompt was captured")
	}
	// 单轮不应该有 Round 标注
	if containsStr(receivedPrompt, "Round 1") {
		t.Error("single-round reviewer should NOT have Round labels")
	}
	if !containsStr(receivedPrompt, "Only one round of review") {
		t.Error("expected prompt to contain the review content")
	}
}

// --- 测试 JSON 中字段不全的 issue 被过滤 ---

func TestStructurizeIssues_FiltersInvalidIssues(t *testing.T) {
	jsonResp := "```json\n" + `{
  "issues": [
    {"severity": "high", "category": "security", "file": "a.go", "title": "Valid issue", "description": "Valid desc"},
    {"severity": "invalid-severity", "file": "b.go", "title": "Bad severity", "description": "desc"},
    {"severity": "high", "file": "", "title": "No file", "description": "desc"},
    {"severity": "high", "file": "c.go", "title": "", "description": "No title"},
    {"severity": "high", "file": "d.go", "title": "No desc", "description": ""}
  ]
}` + "\n```"

	history := []DebateMessage{
		{ReviewerID: "claude", Content: "review"},
	}

	o := makeOrchestrator([]string{jsonResp}, history)
	issues := o.structurizeIssues(context.Background(), &noopDisplay{})

	if len(issues) != 1 {
		t.Fatalf("expected 1 valid issue (4 filtered), got %d", len(issues))
	}
	if issues[0].Title != "Valid issue" {
		t.Errorf("expected the valid issue to survive, got %q", issues[0].Title)
	}
}

func TestStructurizeIssues_ReviewerOrderDeterministic(t *testing.T) {
	var captured string
	resp := "```json\n" + `{"issues":[{"severity":"low","file":"a.go","title":"t","description":"d"}]}` + "\n```"
	mp := &mockProvider{responses: []string{resp}}
	captureProvider := &promptCaptureProvider{inner: mp, captured: &captured}

	o := &DebateOrchestrator{
		summarizer: Reviewer{
			ID:           "summarizer",
			Provider:     captureProvider,
			SystemPrompt: "extract",
		},
		conversationHistory: []DebateMessage{
			{ReviewerID: "z", Content: "z review"},
			{ReviewerID: "a", Content: "a review"},
		},
		tokenUsage: make(map[string]*tokenCount),
	}

	_ = o.structurizeIssues(context.Background(), &noopDisplay{})
	if !strings.Contains(captured, "Use the exact reviewer IDs: a, z") {
		t.Fatalf("expected sorted reviewer IDs in prompt, got: %s", captured)
	}
	if idxA, idxZ := strings.Index(captured, "[a]:"), strings.Index(captured, "[z]:"); idxA == -1 || idxZ == -1 || idxA > idxZ {
		t.Fatalf("expected reviewer sections in sorted order ([a] before [z]); prompt: %s", captured)
	}
}

// --- 测试 analyzer/summarizer 消息也会被收集（潜在问题） ---

func TestStructurizeIssues_IncludesNonReviewerMessages(t *testing.T) {
	// conversationHistory 里如果有 analyzer 的消息，也会进入 lastMessages
	jsonResp := "```json\n" + `{"issues": [{"severity": "low", "category": "style", "file": "a.go", "title": "test", "description": "desc", "raisedBy": ["claude"]}]}` + "\n```"

	history := []DebateMessage{
		{ReviewerID: "analyzer", Content: "Analysis results..."},
		{ReviewerID: "claude", Content: "Review content"},
		{ReviewerID: "summarizer", Content: "Previous summary"},
	}

	o := makeOrchestrator([]string{jsonResp}, history)
	issues := o.structurizeIssues(context.Background(), &noopDisplay{})

	// 只要能成功提取就行，关键是验证 analyzer/summarizer 消息不会导致崩溃
	if issues == nil {
		t.Error("expected issues to be extracted even with analyzer/summarizer messages in history")
	}
}

// --- 测试真实世界场景：reviewer 输出纯 markdown（无 JSON） ---

func TestStructurizeIssues_RealWorldMarkdownOnly(t *testing.T) {
	// 模拟 reviewer 只输出 markdown 格式（没有 JSON），summarizer 也返回 markdown
	markdownResp := `## Issues Found

### 1. Deferred import in base_parser.py
- Severity: High
- The base class imports settings inside a method...

### 2. DRY violation
- Same pattern duplicated across 3 files...`

	history := []DebateMessage{
		{ReviewerID: "claude", Content: "Detailed markdown review..."},
	}

	o := makeOrchestrator([]string{
		markdownResp,
		markdownResp,
		markdownResp,
	}, history)
	issues := o.structurizeIssues(context.Background(), &noopDisplay{})

	if issues != nil {
		t.Errorf("expected nil when summarizer only returns markdown, got %d issues", len(issues))
	}
}

// --- 辅助类型 ---

// promptCaptureProvider 包装一个 provider，捕获发送的 prompt
type promptCaptureProvider struct {
	inner    provider.AIProvider
	captured *string
}

func (p *promptCaptureProvider) Name() string { return "capture" }
func (p *promptCaptureProvider) Chat(ctx context.Context, msgs []provider.Message, sys string, opts *provider.ChatOptions) (string, error) {
	if len(msgs) > 0 {
		*p.captured = msgs[0].Content
	}
	return p.inner.Chat(ctx, msgs, sys, opts)
}
func (p *promptCaptureProvider) ChatStream(ctx context.Context, msgs []provider.Message, sys string) (<-chan string, <-chan error) {
	return p.inner.ChatStream(ctx, msgs, sys)
}
func (p *promptCaptureProvider) StartSession(_ string) error { return nil }
func (p *promptCaptureProvider) EndSession() error           { return nil }
func (p *promptCaptureProvider) HasSession() bool            { return false }
func (p *promptCaptureProvider) ShouldSendFullHistory() bool { return true }
func (p *promptCaptureProvider) LastSeenIndex() int          { return 0 }
func (p *promptCaptureProvider) SetLastSeenIndex(_ int)      {}

func containsStr(s, substr string) bool {
	return len(s) >= len(substr) && findSubstring(s, substr)
}

func findSubstring(s, sub string) bool {
	for i := 0; i <= len(s)-len(sub); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}
