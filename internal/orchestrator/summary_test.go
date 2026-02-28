package orchestrator

import (
	"context"
	"testing"

	"github.com/longyunfeigu/codedebate/internal/provider"
)

type summarySessionProvider struct {
	lastMessages       []provider.Message
	lastStreamMessages []provider.Message
	response           string
	streamChunks       []string
}

func (p *summarySessionProvider) Name() string { return "summary-session" }

func (p *summarySessionProvider) Chat(_ context.Context, messages []provider.Message, _ string, _ *provider.ChatOptions) (string, error) {
	p.lastMessages = append([]provider.Message(nil), messages...)
	return p.response, nil
}

func (p *summarySessionProvider) ChatStream(_ context.Context, messages []provider.Message, _ string) (<-chan string, <-chan error) {
	p.lastStreamMessages = append([]provider.Message(nil), messages...)
	chunks := append([]string(nil), p.streamChunks...)
	if len(chunks) == 0 && p.response != "" {
		chunks = []string{p.response}
	}
	ch := make(chan string, len(chunks))
	errs := make(chan error, 1)
	for _, chunk := range chunks {
		ch <- chunk
	}
	close(ch)
	close(errs)
	return ch, errs
}

func (p *summarySessionProvider) StartSession(_ string)       {}
func (p *summarySessionProvider) EndSession()                 {}
func (p *summarySessionProvider) SessionID() string           { return "sid" }
func (p *summarySessionProvider) IsFirstMessage() bool        { return false }
func (p *summarySessionProvider) MarkMessageSent()            {}
func (p *summarySessionProvider) ShouldSendFullHistory() bool { return false }

func TestCollectSummaries_SessionProviderEmbedsContextInSingleMessage(t *testing.T) {
	sp := &summarySessionProvider{response: "final summary"}
	other := &mockProvider{responses: []string{"other summary"}}

	o := &DebateOrchestrator{
		reviewers: []Reviewer{
			{ID: "r1", Provider: sp, SystemPrompt: "sys"},
			{ID: "r2", Provider: other, SystemPrompt: "sys"},
		},
		conversationHistory: []DebateMessage{
			{ReviewerID: "r1", Content: "R1 from r1"},
			{ReviewerID: "r2", Content: "R1 from r2"},
			{ReviewerID: "r1", Content: "R2 from r1"},
			{ReviewerID: "r2", Content: "R2 from r2"},
		},
		lastSeenIndex: map[string]int{
			"r1": 3,
			"r2": 3,
		},
		tokenUsage: make(map[string]*tokenCount),
	}

	summaries, err := o.collectSummaries(context.Background(), &noopDisplay{})
	if err != nil {
		t.Fatalf("collectSummaries returned error: %v", err)
	}
	if len(summaries) != 2 {
		t.Fatalf("expected 2 summaries, got %d", len(summaries))
	}

	if len(sp.lastStreamMessages) != 1 {
		t.Fatalf("session provider should receive exactly 1 merged message, got %d", len(sp.lastStreamMessages))
	}
	content := sp.lastStreamMessages[0].Content
	if !containsStr(content, "Latest debate context:") {
		t.Fatalf("expected merged summary prompt to include latest context, got: %s", content)
	}
	if !containsStr(content, "Please summarize your key points and conclusions") {
		t.Fatalf("expected merged summary prompt to include summary instruction, got: %s", content)
	}
	if !containsStr(content, "R2 from r2") {
		t.Fatalf("expected merged summary prompt to include latest round context, got: %s", content)
	}
}

type finalConclusionDisplay struct {
	chunks []string
	finals []string
}

func (d *finalConclusionDisplay) OnWaiting(_ string) {}
func (d *finalConclusionDisplay) OnMessageChunk(_ string, chunk string) {
	d.chunks = append(d.chunks, chunk)
}
func (d *finalConclusionDisplay) OnMessage(_ string, content string) {
	d.finals = append(d.finals, content)
}
func (d *finalConclusionDisplay) OnParallelStatus(_ int, _ []ReviewerStatus) {}
func (d *finalConclusionDisplay) OnSummaryStatus(_ []ReviewerStatus)         {}
func (d *finalConclusionDisplay) OnRoundComplete(_ int, _ bool)              {}
func (d *finalConclusionDisplay) OnConvergenceJudgment(_ string, _ string)   {}
func (d *finalConclusionDisplay) OnContextGathered(_ *GatheredContext)       {}

type summaryStatusDisplay struct {
	statusSnapshots [][]ReviewerStatus
}

func (d *summaryStatusDisplay) OnWaiting(_ string)                         {}
func (d *summaryStatusDisplay) OnMessageChunk(_ string, _ string)          {}
func (d *summaryStatusDisplay) OnMessage(_ string, _ string)               {}
func (d *summaryStatusDisplay) OnParallelStatus(_ int, _ []ReviewerStatus) {}
func (d *summaryStatusDisplay) OnSummaryStatus(statuses []ReviewerStatus) {
	snapshot := append([]ReviewerStatus(nil), statuses...)
	d.statusSnapshots = append(d.statusSnapshots, snapshot)
}
func (d *summaryStatusDisplay) OnRoundComplete(_ int, _ bool)            {}
func (d *summaryStatusDisplay) OnConvergenceJudgment(_ string, _ string) {}
func (d *summaryStatusDisplay) OnContextGathered(_ *GatheredContext)     {}

func TestGetFinalConclusion_StreamsChunks(t *testing.T) {
	sp := &summarySessionProvider{
		streamChunks: []string{"## Points of Consensus\n", "- finding A\n"},
	}

	run := (&DebateOrchestrator{
		summarizer: Reviewer{
			ID:           "summarizer",
			Provider:     sp,
			SystemPrompt: "sys",
		},
		tokenUsage: make(map[string]*tokenCount),
	}).newRun("prompt")

	run.conversationHistory = []DebateMessage{
		{ReviewerID: "r1", Content: "review one"},
		{ReviewerID: "r2", Content: "review two"},
	}

	display := &finalConclusionDisplay{}
	summaries := []DebateSummary{
		{ReviewerID: "r1", Summary: "summary one"},
		{ReviewerID: "r2", Summary: "summary two"},
	}

	got, err := run.getFinalConclusion(context.Background(), summaries, display)
	if err != nil {
		t.Fatalf("getFinalConclusion returned error: %v", err)
	}

	want := "## Points of Consensus\n- finding A\n"
	if got != want {
		t.Fatalf("final conclusion = %q, want %q", got, want)
	}
	if len(display.chunks) != 2 {
		t.Fatalf("expected 2 streamed chunks, got %d", len(display.chunks))
	}
	if len(display.finals) != 1 || display.finals[0] != want {
		t.Fatalf("unexpected final messages: %#v", display.finals)
	}
	if tc := run.tokenUsage["summarizer"]; tc == nil || tc.output == 0 {
		t.Fatalf("expected summarizer token usage to be recorded, got %#v", tc)
	}
}

func TestCollectSummaries_ReportsSummaryStatuses(t *testing.T) {
	r1 := &summarySessionProvider{streamChunks: []string{"summary one"}}
	r2 := &summarySessionProvider{streamChunks: []string{"summary two"}}
	o := &DebateOrchestrator{
		reviewers: []Reviewer{
			{ID: "r1", Provider: r1, SystemPrompt: "sys"},
			{ID: "r2", Provider: r2, SystemPrompt: "sys"},
		},
		conversationHistory: []DebateMessage{
			{ReviewerID: "r1", Content: "R1 from r1"},
			{ReviewerID: "r2", Content: "R1 from r2"},
		},
		lastSeenIndex: map[string]int{
			"r1": 1,
			"r2": 1,
		},
		tokenUsage: make(map[string]*tokenCount),
	}

	display := &summaryStatusDisplay{}
	summaries, err := o.collectSummaries(context.Background(), display)
	if err != nil {
		t.Fatalf("collectSummaries returned error: %v", err)
	}
	if len(summaries) != 2 {
		t.Fatalf("expected 2 summaries, got %d", len(summaries))
	}
	if len(display.statusSnapshots) == 0 {
		t.Fatal("expected summary status snapshots to be reported")
	}

	last := display.statusSnapshots[len(display.statusSnapshots)-1]
	if len(last) != 2 {
		t.Fatalf("expected 2 reviewer statuses in final snapshot, got %d", len(last))
	}
	for _, status := range last {
		if status.Status != "done" {
			t.Fatalf("expected final summary status to be done, got %+v", status)
		}
		if status.OutputTokens <= 0 {
			t.Fatalf("expected output tokens to be recorded, got %+v", status)
		}
	}
}
