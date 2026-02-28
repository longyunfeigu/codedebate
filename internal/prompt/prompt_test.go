package prompt

import (
	"strings"
	"testing"
)

func TestRender(t *testing.T) {
	t.Run("renders template with data", func(t *testing.T) {
		result, err := Render("reviewer_summary.tmpl", nil)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if !strings.Contains(result, "summarize your key points") {
			t.Errorf("unexpected result: %s", result)
		}
	})

	t.Run("renders template with map data", func(t *testing.T) {
		result, err := Render("convergence_system.tmpl", nil)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if !strings.Contains(result, "pragmatic consensus judge") {
			t.Errorf("unexpected result: %s", result)
		}
	})

	t.Run("renders structurize_issues with variables", func(t *testing.T) {
		result, err := Render("structurize_issues.tmpl", map[string]any{
			"ReviewText":  "test review",
			"ReviewerIDs": "r1, r2",
			"Schema":      `{"type": "object"}`,
		})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if !strings.Contains(result, "test review") {
			t.Error("missing ReviewText in output")
		}
		if !strings.Contains(result, "r1, r2") {
			t.Error("missing ReviewerIDs in output")
		}
		if !strings.Contains(result, "```json") {
			t.Error("missing JSON code fence in output")
		}
		if !strings.Contains(result, `"type": "object"`) {
			t.Error("missing Schema in output")
		}
	})

	t.Run("renders structurize_retry with validation errors", func(t *testing.T) {
		result, err := Render("structurize_retry.tmpl", map[string]any{
			"ValidationErrors": "- issues.0.severity: must be one of: critical, high",
			"ReviewText":       "test review text",
			"ReviewerIDs":      "r1, r2",
			"Schema":           `{"type": "object"}`,
		})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if !strings.Contains(result, "validation errors") {
			t.Error("missing validation errors header")
		}
		if !strings.Contains(result, "issues.0.severity") {
			t.Error("missing validation error details")
		}
		if !strings.Contains(result, "test review text") {
			t.Error("missing ReviewText")
		}
	})

	t.Run("renders structurize_delta with optional ledger summary", func(t *testing.T) {
		result, err := Render("structurize_delta.tmpl", map[string]any{
			"ReviewerID":       "r1",
			"Round":            2,
			"RoundContent":     "new round findings",
			"LedgerSummary":    "| ID | Severity | File:Line | Title |",
			"CanonicalSummary": "| Canonical ID | Issue Refs |\n| c1 | claude:I1 |",
			"Schema":           `{"type":"object"}`,
		})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if !strings.Contains(result, "reviewer \"r1\"") {
			t.Error("missing ReviewerID")
		}
		if !strings.Contains(result, "round 2") {
			t.Error("missing round")
		}
		if !strings.Contains(result, "new round findings") {
			t.Error("missing round content")
		}
		if !strings.Contains(result, "Previously tracked active issues") {
			t.Error("missing ledger summary section")
		}
		if !strings.Contains(result, "Canonical issues already tracked across reviewers") {
			t.Error("missing canonical summary section")
		}
		if !strings.Contains(result, `"support"`) {
			t.Error("missing support instructions")
		}
		if !strings.Contains(result, "issueRef") {
			t.Error("missing issueRef instructions")
		}
	})

	t.Run("renders reviewer_first_round with all sections", func(t *testing.T) {
		result, err := Render("reviewer_first_round.tmpl", map[string]any{
			"TaskPrompt":              "Review this PR",
			"ContextSection":          "\n## System Context\nSome context\n\n",
			"FocusSection":            "\nFocus on security\n",
			"CallChainSection":        "\n## Call Chain\nSome calls\n",
			"PreviousCommentsSection": "\n## Previous Review Findings\n1. [high] `auth.go:42` - Missing validation\n",
			"Analysis":                "Analysis result",
			"ReviewerID":              "claude",
		})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if !strings.Contains(result, "Review this PR") {
			t.Error("missing TaskPrompt")
		}
		if !strings.Contains(result, "System Context") {
			t.Error("missing ContextSection")
		}
		if !strings.Contains(result, "You are [claude]") {
			t.Error("missing ReviewerID")
		}
		if !strings.Contains(result, "single uninterrupted audit") {
			t.Error("missing proactive execution rule")
		}
		if !strings.Contains(result, "Confirmed by workspace cross-check") {
			t.Error("missing evidence source labeling rule")
		}
		if !strings.Contains(result, "Previous Review Findings") {
			t.Error("missing PreviousCommentsSection")
		}
	})

	t.Run("renders reviewer_debate_session with early-round audit rules", func(t *testing.T) {
		result, err := Render("reviewer_debate_session.tmpl", map[string]any{
			"ReviewerID": "codex",
			"NewContent": "- reviewer-a: check auth\n",
			"Round":      2,
			"MaxRounds":  5,
		})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if !strings.Contains(result, "Use it to increase coverage and verify contested points") {
			t.Error("missing debate continuation rule")
		}
		if !strings.Contains(result, "Never end with an offer to continue investigating later") {
			t.Error("missing no-handoff ending rule")
		}
	})

	t.Run("renders reviewer_debate_session with late-round convergence rules", func(t *testing.T) {
		result, err := Render("reviewer_debate_session.tmpl", map[string]any{
			"ReviewerID": "codex",
			"NewContent": "- reviewer-a: check auth\n",
			"Round":      3,
			"MaxRounds":  5,
		})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if !strings.Contains(result, "Convergence mode") {
			t.Error("missing late-round convergence mode")
		}
		if !strings.Contains(result, "Do NOT introduce new issues unless they are clearly critical/blocking") {
			t.Error("missing late-round new-issue guardrail")
		}
	})

	t.Run("renders reviewer_debate_full with no handoff rules", func(t *testing.T) {
		result, err := Render("reviewer_debate_full.tmpl", map[string]any{
			"TaskPrompt": "Review this PR",
			"Analysis":   "analysis",
			"ReviewerID": "claude",
			"OtherLabel": "[codex]",
			"PluralS":    "",
			"OtherWord":  "is",
			"Round":      2,
			"MaxRounds":  5,
		})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if !strings.Contains(result, "Treat this round as a continuation of the same uninterrupted audit") {
			t.Error("missing full-context audit continuation rule")
		}
		if !strings.Contains(result, "Never end with an offer to continue investigating later") {
			t.Error("missing full-context no-handoff ending rule")
		}
	})

	t.Run("renders reviewer_debate_full with late-round convergence rules", func(t *testing.T) {
		result, err := Render("reviewer_debate_full.tmpl", map[string]any{
			"TaskPrompt": "Review this PR",
			"Analysis":   "analysis",
			"ReviewerID": "claude",
			"OtherLabel": "[codex]",
			"PluralS":    "",
			"OtherWord":  "is",
			"Round":      4,
			"MaxRounds":  5,
		})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if !strings.Contains(result, "Late-round convergence mode") {
			t.Error("missing full-context late-round guidance")
		}
		if !strings.Contains(result, "Do NOT add minor new issues") {
			t.Error("missing full-context scope guardrail")
		}
	})

	t.Run("renders convergence_check with variables", func(t *testing.T) {
		result, err := Render("convergence_check.tmpl", map[string]any{
			"ReviewerCount":   2,
			"RoundsCompleted": 3,
			"MaxRounds":       5,
			"MessagesText":    "reviewer messages here",
		})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if !strings.Contains(result, "2 reviewers") {
			t.Error("missing ReviewerCount")
		}
		if !strings.Contains(result, "Round 3") {
			t.Error("missing RoundsCompleted")
		}
		if !strings.Contains(result, "practical late-round standard") {
			t.Error("missing late-round convergence guidance")
		}
	})

	t.Run("renders final_conclusion", func(t *testing.T) {
		result, err := Render("final_conclusion.tmpl", map[string]any{
			"ReviewerCount": 3,
			"SummaryText":   "reviewer summaries here",
			"DebateText":    "debate transcript here",
		})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if !strings.Contains(result, "3 reviewers") {
			t.Error("missing ReviewerCount")
		}
		if !strings.Contains(result, "reviewer summaries here") {
			t.Error("missing SummaryText")
		}
		if !strings.Contains(result, "debate transcript here") {
			t.Error("missing DebateText")
		}
		if !strings.Contains(result, "Do not offer optional follow-up investigation") {
			t.Error("missing no-follow-up rule")
		}
	})

	t.Run("renders reviewer_summary with no follow-up rule", func(t *testing.T) {
		result, err := Render("reviewer_summary.tmpl", nil)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if !strings.Contains(result, "Do not ask the user whether you should continue reviewing") {
			t.Error("missing reviewer summary no-handoff rule")
		}
	})

	t.Run("renders server_review", func(t *testing.T) {
		result, err := Render("server_review.tmpl", map[string]any{
			"MRURL":        "https://gitlab.com/mr/1",
			"Title":        "Fix bug",
			"Description":  "Bug fix description",
			"Diff":         "+added line",
			"HasLocalRepo": true,
		})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if !strings.Contains(result, "https://gitlab.com/mr/1") {
			t.Error("missing MRURL")
		}
		if !strings.Contains(result, "Fix bug") {
			t.Error("missing Title")
		}
		if !strings.Contains(result, "full repository source code is available") {
			t.Error("missing local repo note")
		}
	})

	t.Run("unknown template returns error", func(t *testing.T) {
		_, err := Render("nonexistent.tmpl", nil)
		if err == nil {
			t.Error("expected error for unknown template")
		}
	})
}

func TestMustRender(t *testing.T) {
	t.Run("succeeds for valid template", func(t *testing.T) {
		result := MustRender("reviewer_summary.tmpl", nil)
		if result == "" {
			t.Error("expected non-empty result")
		}
	})

	t.Run("panics for unknown template", func(t *testing.T) {
		defer func() {
			if r := recover(); r == nil {
				t.Error("expected panic for unknown template")
			}
		}()
		MustRender("nonexistent.tmpl", nil)
	})
}
