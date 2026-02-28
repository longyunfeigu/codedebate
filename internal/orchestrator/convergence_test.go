package orchestrator

import (
	"context"
	"testing"

	"github.com/longyunfeigu/codedebate/internal/provider"
)

type convergenceMockProvider struct {
	response       string
	capturedPrompt string
}

func (p *convergenceMockProvider) Name() string { return "convergence-mock" }

func (p *convergenceMockProvider) Chat(_ context.Context, messages []provider.Message, _ string, _ *provider.ChatOptions) (string, error) {
	if len(messages) > 0 {
		p.capturedPrompt = messages[0].Content
	}
	return p.response, nil
}

func (p *convergenceMockProvider) ChatStream(_ context.Context, _ []provider.Message, _ string) (<-chan string, <-chan error) {
	ch := make(chan string, 1)
	errs := make(chan error, 1)
	close(ch)
	close(errs)
	return ch, errs
}

type convergenceDisplay struct {
	verdict   string
	reasoning string
}

func (d *convergenceDisplay) OnWaiting(_ string)                         {}
func (d *convergenceDisplay) OnMessageChunk(_ string, _ string)          {}
func (d *convergenceDisplay) OnMessage(_ string, _ string)               {}
func (d *convergenceDisplay) OnParallelStatus(_ int, _ []ReviewerStatus) {}
func (d *convergenceDisplay) OnSummaryStatus(_ []ReviewerStatus)         {}
func (d *convergenceDisplay) OnRoundComplete(_ int, _ bool)              {}
func (d *convergenceDisplay) OnContextGathered(_ *GatheredContext)       {}
func (d *convergenceDisplay) OnConvergenceJudgment(verdict, reasoning string) {
	d.verdict = verdict
	d.reasoning = reasoning
}

func TestCheckConvergence_EmptyResponseDoesNotPanic(t *testing.T) {
	mp := &convergenceMockProvider{response: ""}
	o := &DebateOrchestrator{
		reviewers: []Reviewer{
			{ID: "r1"},
			{ID: "r2"},
		},
		summarizer: Reviewer{
			ID:       "summarizer",
			Provider: mp,
		},
		conversationHistory: []DebateMessage{
			{ReviewerID: "r1", Content: "round1 message from r1"},
			{ReviewerID: "r2", Content: "round1 message from r2"},
			{ReviewerID: "r1", Content: "round2 message from r1"},
			{ReviewerID: "r2", Content: "round2 message from r2"},
		},
	}
	d := &convergenceDisplay{}

	converged, err := o.checkConvergence(context.Background(), d)
	if err != nil {
		t.Fatalf("checkConvergence returned error: %v", err)
	}
	if converged {
		t.Fatal("expected not converged for empty response")
	}
	if d.verdict != "NOT_CONVERGED" {
		t.Fatalf("display verdict = %q, want NOT_CONVERGED", d.verdict)
	}
}

func TestCheckConvergence_UsesAllRoundsContext(t *testing.T) {
	mp := &convergenceMockProvider{response: "All key disagreements are resolved.\nCONVERGED"}
	o := &DebateOrchestrator{
		reviewers: []Reviewer{
			{ID: "r1"},
			{ID: "r2"},
		},
		summarizer: Reviewer{
			ID:       "summarizer",
			Provider: mp,
		},
		conversationHistory: []DebateMessage{
			{ReviewerID: "r1", Content: "R1: SQL injection risk"},
			{ReviewerID: "r2", Content: "R1: I disagree initially"},
			{ReviewerID: "r1", Content: "R2: here's concrete exploit path"},
			{ReviewerID: "r2", Content: "R2: agreed, blocker confirmed"},
		},
	}
	d := &convergenceDisplay{}

	converged, err := o.checkConvergence(context.Background(), d)
	if err != nil {
		t.Fatalf("checkConvergence returned error: %v", err)
	}
	if !converged {
		t.Fatal("expected converged=true")
	}
	if d.verdict != "CONVERGED" {
		t.Fatalf("display verdict = %q, want CONVERGED", d.verdict)
	}

	if mp.capturedPrompt == "" {
		t.Fatal("expected convergence prompt to be captured")
	}
	if !containsStr(mp.capturedPrompt, "R1: SQL injection risk") {
		t.Fatal("expected round 1 message to be included in convergence prompt")
	}
	if !containsStr(mp.capturedPrompt, "R2: agreed, blocker confirmed") {
		t.Fatal("expected round 2 message to be included in convergence prompt")
	}
}

func TestCheckConvergence_Round1CanConverge(t *testing.T) {
	mp := &convergenceMockProvider{response: "Both reviewers independently found the same SQL injection issue and agree it's critical.\nCONVERGED"}
	o := &DebateOrchestrator{
		reviewers: []Reviewer{
			{ID: "r1"},
			{ID: "r2"},
		},
		summarizer: Reviewer{
			ID:       "summarizer",
			Provider: mp,
		},
		// Only 1 round of messages (round 1)
		conversationHistory: []DebateMessage{
			{ReviewerID: "r1", Content: "Found SQL injection risk in auth.go:42"},
			{ReviewerID: "r2", Content: "SQL injection vulnerability in auth.go line 42"},
		},
	}
	d := &convergenceDisplay{}

	converged, err := o.checkConvergence(context.Background(), d)
	if err != nil {
		t.Fatalf("checkConvergence returned error: %v", err)
	}
	if !converged {
		t.Fatal("expected round-1 convergence when reviewers independently agree")
	}
	if d.verdict != "CONVERGED" {
		t.Fatalf("display verdict = %q, want CONVERGED", d.verdict)
	}

	// Verify IsFirstRound context was passed to template
	if !containsStr(mp.capturedPrompt, "Round 1") {
		t.Fatal("expected prompt to mention Round 1")
	}
	if !containsStr(mp.capturedPrompt, "independently") {
		t.Fatal("expected prompt to contain round-1 specific guidance about independent review")
	}
}

func TestCheckConvergence_Round1NotConverged(t *testing.T) {
	mp := &convergenceMockProvider{response: "Reviewers found completely different issues.\nNOT_CONVERGED"}
	o := &DebateOrchestrator{
		reviewers: []Reviewer{
			{ID: "r1"},
			{ID: "r2"},
		},
		summarizer: Reviewer{
			ID:       "summarizer",
			Provider: mp,
		},
		conversationHistory: []DebateMessage{
			{ReviewerID: "r1", Content: "Found performance issue in database queries"},
			{ReviewerID: "r2", Content: "Found security issue in authentication"},
		},
	}
	d := &convergenceDisplay{}

	converged, err := o.checkConvergence(context.Background(), d)
	if err != nil {
		t.Fatalf("checkConvergence returned error: %v", err)
	}
	if converged {
		t.Fatal("expected not converged when reviewers found different issues")
	}
}
