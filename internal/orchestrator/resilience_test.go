package orchestrator

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/longyunfeigu/codedebate/internal/provider"
)

type scriptedProvider struct {
	chatResponse string
	chatErr      error
	streamChunks []string
	streamErr    error
}

func (p *scriptedProvider) Name() string { return "scripted" }

func (p *scriptedProvider) Chat(_ context.Context, _ []provider.Message, _ string, _ *provider.ChatOptions) (string, error) {
	if p.chatErr != nil {
		return "", p.chatErr
	}
	return p.chatResponse, nil
}

func (p *scriptedProvider) ChatStream(_ context.Context, _ []provider.Message, _ string) (<-chan string, <-chan error) {
	ch := make(chan string, len(p.streamChunks))
	errCh := make(chan error, 1)
	for _, chunk := range p.streamChunks {
		ch <- chunk
	}
	close(ch)
	if p.streamErr != nil {
		errCh <- p.streamErr
	}
	close(errCh)
	return ch, errCh
}

func TestRunDebateRound_ReviewerFailureDoesNotAbortRound(t *testing.T) {
	run := (&DebateOrchestrator{
		reviewers: []Reviewer{
			{ID: "reviewer-a", Provider: &scriptedProvider{streamChunks: []string{"looks good"}}, SystemPrompt: "sys"},
			{ID: "reviewer-b", Provider: &scriptedProvider{streamErr: errors.New("boom")}, SystemPrompt: "sys"},
		},
		options: OrchestratorOptions{MaxRounds: 2},
	}).newRun("Review this diff")

	display := &streamingDisplay{}
	outputs, err := run.runDebateRound(context.Background(), 1, display)
	if err != nil {
		t.Fatalf("runDebateRound returned error: %v", err)
	}

	if got := outputs["reviewer-a"]; got != "looks good" {
		t.Fatalf("outputs[reviewer-a] = %q, want %q", got, "looks good")
	}
	if _, ok := outputs["reviewer-b"]; ok {
		t.Fatalf("unexpected output for failed reviewer: %#v", outputs)
	}
	if len(run.conversationHistory) != 1 || run.conversationHistory[0].ReviewerID != "reviewer-a" {
		t.Fatalf("conversationHistory = %#v, want only reviewer-a message", run.conversationHistory)
	}
	if len(run.activeReviewers()) != 1 || run.activeReviewers()[0].ID != "reviewer-a" {
		t.Fatalf("active reviewers = %#v, want only reviewer-a", run.activeReviewers())
	}

	if len(display.lastStatuses) != 2 {
		t.Fatalf("expected 2 reviewer statuses, got %d", len(display.lastStatuses))
	}
	statusByReviewer := make(map[string]string, len(display.lastStatuses))
	for _, status := range display.lastStatuses {
		statusByReviewer[status.ReviewerID] = status.Status
	}
	if statusByReviewer["reviewer-a"] != "done" {
		t.Fatalf("reviewer-a status = %q, want done", statusByReviewer["reviewer-a"])
	}
	if statusByReviewer["reviewer-b"] != "failed" {
		t.Fatalf("reviewer-b status = %q, want failed", statusByReviewer["reviewer-b"])
	}
}

func TestRunAnalysisPhase_AnalyzerFailureDoesNotAbort(t *testing.T) {
	run := (&DebateOrchestrator{
		analyzer: Reviewer{
			ID:           "analyzer",
			Provider:     &scriptedProvider{streamErr: errors.New("analyzer boom")},
			SystemPrompt: "sys",
		},
	}).newRun("Review this diff")

	if err := run.runAnalysisPhase(context.Background(), "PR #1", "Review this diff", &noopDisplay{}); err != nil {
		t.Fatalf("runAnalysisPhase returned error: %v", err)
	}
	if run.analysis != "" {
		t.Fatalf("analysis = %q, want empty string on analyzer failure", run.analysis)
	}
}

func TestCollectSummaries_SkipsFailedReviewers(t *testing.T) {
	run := (&DebateOrchestrator{
		reviewers: []Reviewer{
			{ID: "reviewer-a", Provider: &scriptedProvider{streamChunks: []string{"summary a"}}, SystemPrompt: "sys"},
			{ID: "reviewer-b", Provider: &scriptedProvider{streamErr: errors.New("should be skipped")}, SystemPrompt: "sys"},
		},
	}).newRun("Review this diff")

	run.conversationHistory = []DebateMessage{
		{ReviewerID: "reviewer-a", Round: 1, Content: "review output"},
	}
	run.lastSeenIndex["reviewer-a"] = 0
	run.lastSeenIndex["reviewer-b"] = 0
	run.failedReviewers["reviewer-b"] = errors.New("round failure")

	summaries, err := run.collectSummaries(context.Background(), &noopDisplay{})
	if err != nil {
		t.Fatalf("collectSummaries returned error: %v", err)
	}
	if len(summaries) != 1 {
		t.Fatalf("expected 1 summary, got %d", len(summaries))
	}
	if summaries[0].ReviewerID != "reviewer-a" || summaries[0].Summary != "summary a" {
		t.Fatalf("unexpected summaries: %#v", summaries)
	}
}

func TestRunSummaryPhase_FallsBackWhenFinalConclusionFails(t *testing.T) {
	run := (&DebateOrchestrator{
		reviewers: []Reviewer{
			{ID: "reviewer-a", Provider: &scriptedProvider{streamChunks: []string{"summary a"}}, SystemPrompt: "sys"},
		},
		summarizer: Reviewer{
			ID: "summarizer",
			Provider: &scriptedProvider{
				chatResponse: "```json\n{\"issues\":[{\"severity\":\"low\",\"file\":\"main.go\",\"title\":\"Minor issue\",\"description\":\"desc\",\"raisedBy\":[\"reviewer-a\"]}]}\n```",
				streamErr:    errors.New("summary synthesis boom"),
			},
			SystemPrompt: "sys",
		},
	}).newRun("Review this diff")

	run.conversationHistory = []DebateMessage{
		{ReviewerID: "reviewer-a", Round: 1, Content: "review output"},
	}
	run.lastSeenIndex["reviewer-a"] = 0

	result, err := run.runSummaryPhase(context.Background(), "PR #1", &noopDisplay{}, nil)
	if err != nil {
		t.Fatalf("runSummaryPhase returned error: %v", err)
	}
	if !strings.Contains(result.FinalConclusion, "Partial Review Result") {
		t.Fatalf("final conclusion should use fallback text, got %q", result.FinalConclusion)
	}
	if len(result.Summaries) != 1 {
		t.Fatalf("expected 1 reviewer summary, got %d", len(result.Summaries))
	}
	if len(result.ParsedIssues) != 1 {
		t.Fatalf("expected structurized issues to still be returned, got %d", len(result.ParsedIssues))
	}
}
