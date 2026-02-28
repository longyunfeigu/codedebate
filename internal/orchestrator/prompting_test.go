package orchestrator

import (
	"strings"
	"testing"
)

func TestBuildFirstRoundMessages_IncludesPreviousCommentsSection(t *testing.T) {
	run := (&DebateOrchestrator{options: OrchestratorOptions{
		PreviousComments: "1. [high] `auth.go:42` - Missing validation",
	}}).newRun("Review this diff")

	messages := run.buildFirstRoundMessages("reviewer-a")
	if len(messages) != 1 {
		t.Fatalf("messages len = %d, want 1", len(messages))
	}
	if !strings.Contains(messages[0].Content, "Previous Review Findings") {
		t.Fatalf("first round prompt missing previous comments section: %q", messages[0].Content)
	}
	if !strings.Contains(messages[0].Content, "Only raise NEW issues if they are critical/blocking") {
		t.Fatalf("first round prompt missing previous-comments guidance: %q", messages[0].Content)
	}
}

func TestBuildSessionDebateMessages_LateRoundUsesConvergencePrompt(t *testing.T) {
	run := (&DebateOrchestrator{options: OrchestratorOptions{MaxRounds: 5}}).newRun("Review this diff")
	run.currentRound = 3

	messages := run.buildSessionDebateMessages("reviewer-a", 1, []DebateMessage{{ReviewerID: "reviewer-b", Content: "I still think auth is broken"}})
	if len(messages) != 1 {
		t.Fatalf("messages len = %d, want 1", len(messages))
	}
	if !strings.Contains(messages[0].Content, "Convergence mode") {
		t.Fatalf("session debate prompt missing convergence mode: %q", messages[0].Content)
	}
	if !strings.Contains(messages[0].Content, "Do NOT introduce new issues unless they are clearly critical/blocking") {
		t.Fatalf("session debate prompt missing new-issue guardrail: %q", messages[0].Content)
	}
}

func TestBuildFullContextDebateMessages_LateRoundUsesConvergencePrompt(t *testing.T) {
	run := (&DebateOrchestrator{options: OrchestratorOptions{MaxRounds: 5}}).newRun("Review this diff")
	run.analysis = "analysis"
	run.currentRound = 4

	messages := run.buildFullContextDebateMessages("reviewer-a", []string{"reviewer-b"}, nil)
	if len(messages) == 0 {
		t.Fatal("expected at least one message")
	}
	if !strings.Contains(messages[0].Content, "Late-round convergence mode") {
		t.Fatalf("full-context debate prompt missing convergence mode: %q", messages[0].Content)
	}
	if !strings.Contains(messages[0].Content, "Do NOT add minor new issues") {
		t.Fatalf("full-context debate prompt missing scope guardrail: %q", messages[0].Content)
	}
}
