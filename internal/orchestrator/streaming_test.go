package orchestrator

import (
	"context"
	"sync"
	"testing"

	"github.com/longyunfeigu/codedebate/internal/provider"
)

type streamingProvider struct {
	chunks []string
}

func (p *streamingProvider) Name() string { return "streaming-mock" }

func (p *streamingProvider) Chat(_ context.Context, _ []provider.Message, _ string, _ *provider.ChatOptions) (string, error) {
	return "", nil
}

func (p *streamingProvider) ChatStream(_ context.Context, _ []provider.Message, _ string) (<-chan string, <-chan error) {
	ch := make(chan string, len(p.chunks))
	errCh := make(chan error, 1)
	for _, chunk := range p.chunks {
		ch <- chunk
	}
	close(ch)
	errCh <- nil
	close(errCh)
	return ch, errCh
}

type streamingDisplay struct {
	mu           sync.Mutex
	chunks       []string
	finals       []string
	lastStatuses []ReviewerStatus
}

func (d *streamingDisplay) OnWaiting(_ string) {}

func (d *streamingDisplay) OnMessageChunk(_ string, chunk string) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.chunks = append(d.chunks, chunk)
}

func (d *streamingDisplay) OnMessage(_ string, content string) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.finals = append(d.finals, content)
}

func (d *streamingDisplay) OnParallelStatus(_ int, statuses []ReviewerStatus) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.lastStatuses = append([]ReviewerStatus(nil), statuses...)
}

func (d *streamingDisplay) OnSummaryStatus(_ []ReviewerStatus) {}

func (d *streamingDisplay) OnRoundComplete(_ int, _ bool)            {}
func (d *streamingDisplay) OnConvergenceJudgment(_ string, _ string) {}
func (d *streamingDisplay) OnContextGathered(_ *GatheredContext)     {}

func TestRunDebateRound_StreamsReviewerChunksAndReportsMetrics(t *testing.T) {
	o := &DebateOrchestrator{
		reviewers: []Reviewer{
			{
				ID:           "reviewer-a",
				Provider:     &streamingProvider{chunks: []string{"part 1 ", "part 2"}},
				SystemPrompt: "You are reviewer A.",
			},
		},
		options: OrchestratorOptions{MaxRounds: 1},
	}

	run := o.newRun("Review this diff")
	display := &streamingDisplay{}

	outputs, err := run.runDebateRound(context.Background(), 1, display)
	if err != nil {
		t.Fatalf("runDebateRound returned error: %v", err)
	}

	if got := outputs["reviewer-a"]; got != "part 1 part 2" {
		t.Fatalf("round output = %q, want %q", got, "part 1 part 2")
	}

	if len(display.chunks) != 2 {
		t.Fatalf("expected 2 streamed chunks, got %d", len(display.chunks))
	}
	if display.chunks[0] != "part 1 " || display.chunks[1] != "part 2" {
		t.Fatalf("unexpected streamed chunks: %#v", display.chunks)
	}

	if len(display.finals) != 1 || display.finals[0] != "part 1 part 2" {
		t.Fatalf("unexpected final messages: %#v", display.finals)
	}

	if len(display.lastStatuses) != 1 {
		t.Fatalf("expected 1 reviewer status, got %d", len(display.lastStatuses))
	}
	status := display.lastStatuses[0]
	if status.Status != "done" {
		t.Fatalf("status = %q, want done", status.Status)
	}
	if status.InputTokens <= 0 {
		t.Fatalf("expected positive input token estimate, got %d", status.InputTokens)
	}
	if status.OutputTokens <= 0 {
		t.Fatalf("expected positive output token estimate, got %d", status.OutputTokens)
	}
	if status.EstimatedCost <= 0 {
		t.Fatalf("expected positive estimated cost, got %f", status.EstimatedCost)
	}

	if len(run.conversationHistory) != 1 {
		t.Fatalf("conversationHistory length = %d, want 1", len(run.conversationHistory))
	}
	if run.conversationHistory[0].Content != "part 1 part 2" {
		t.Fatalf("conversationHistory content = %q, want %q", run.conversationHistory[0].Content, "part 1 part 2")
	}
}
