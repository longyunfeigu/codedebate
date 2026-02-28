package orchestrator

import (
	"strings"
	"testing"
)

func TestCanonicalizeMergedIssues_MergesSupportAcrossReviewers(t *testing.T) {
	line10 := 10
	line12 := 12

	issues := []MergedIssue{
		{
			ReviewIssue: ReviewIssue{
				Severity:    "high",
				Category:    "security",
				File:        "auth.go",
				Line:        &line10,
				Title:       "SQL injection vulnerability found",
				Description: "User input is concatenated into the SQL query without sanitization",
			},
			RaisedBy:     []string{"claude"},
			IntroducedBy: []string{"claude"},
			SupportedBy:  []string{"claude"},
			Descriptions: []string{"User input is concatenated into the SQL query without sanitization"},
			Mentions: []IssueMention{{
				ReviewerID:   "claude",
				LocalIssueID: "I1",
				Round:        1,
				Status:       "active",
			}},
		},
		{
			ReviewIssue: ReviewIssue{
				Severity:    "medium",
				Category:    "security",
				File:        "auth.go",
				Line:        &line12,
				Title:       "SQL injection vulnerability detected",
				Description: "User input is concatenated into the SQL query without any sanitization",
			},
			RaisedBy:     []string{"gpt4o"},
			IntroducedBy: []string{"gpt4o"},
			SupportedBy:  []string{"gpt4o"},
			Descriptions: []string{"User input is concatenated into the SQL query without any sanitization"},
			Mentions: []IssueMention{{
				ReviewerID:   "gpt4o",
				LocalIssueID: "I2",
				Round:        2,
				Status:       "active",
			}},
		},
	}

	canonical := CanonicalizeMergedIssues(issues)
	if len(canonical) != 1 {
		t.Fatalf("expected 1 canonical issue, got %d", len(canonical))
	}
	if len(canonical[0].SupportedBy) != 2 {
		t.Fatalf("expected 2 supporters, got %v", canonical[0].SupportedBy)
	}
	if len(canonical[0].RaisedBy) != 2 {
		t.Fatalf("expected RaisedBy to mirror supporters, got %v", canonical[0].RaisedBy)
	}
	if canonical[0].Severity != "high" {
		t.Fatalf("expected highest severity to win, got %q", canonical[0].Severity)
	}
	if canonical[0].CanonicalID == "" {
		t.Fatal("expected canonical id to be populated")
	}
}

func TestCanonicalizeMergedIssues_TracksIntroducedAndWithdrawn(t *testing.T) {
	line := 42
	issues := []MergedIssue{
		{
			ReviewIssue: ReviewIssue{
				Severity:    "high",
				Category:    "correctness",
				File:        "svc.go",
				Line:        &line,
				Title:       "Nil guard removed",
				Description: "The nil guard was removed before dereferencing the pointer",
			},
			RaisedBy:     []string{"claude"},
			IntroducedBy: []string{"claude"},
			SupportedBy:  []string{"claude"},
			Descriptions: []string{"The nil guard was removed before dereferencing the pointer"},
			Mentions: []IssueMention{{
				ReviewerID:   "claude",
				LocalIssueID: "I1",
				Round:        1,
				Status:       "active",
			}},
		},
		{
			ReviewIssue: ReviewIssue{
				Severity:    "high",
				Category:    "correctness",
				File:        "svc.go",
				Line:        &line,
				Title:       "Nil guard removed",
				Description: "The nil guard was removed before dereferencing the pointer",
			},
			IntroducedBy: []string{"gpt4o"},
			WithdrawnBy:  []string{"gpt4o"},
			Descriptions: []string{"The nil guard was removed before dereferencing the pointer"},
			Mentions: []IssueMention{
				{ReviewerID: "gpt4o", LocalIssueID: "I2", Round: 1, Status: "active"},
				{ReviewerID: "gpt4o", LocalIssueID: "I2", Round: 2, Status: "retracted"},
			},
		},
	}

	canonical := CanonicalizeMergedIssues(issues)
	if len(canonical) != 1 {
		t.Fatalf("expected 1 canonical issue, got %d", len(canonical))
	}
	if len(canonical[0].IntroducedBy) != 2 {
		t.Fatalf("expected both reviewers to be introducers in round 1, got %v", canonical[0].IntroducedBy)
	}
	if len(canonical[0].SupportedBy) != 1 || canonical[0].SupportedBy[0] != "claude" {
		t.Fatalf("expected claude to remain supporter, got %v", canonical[0].SupportedBy)
	}
	if len(canonical[0].WithdrawnBy) != 1 || canonical[0].WithdrawnBy[0] != "gpt4o" {
		t.Fatalf("expected gpt4o to be withdrawn, got %v", canonical[0].WithdrawnBy)
	}
}

func TestStructurizeIssuesFromLedgers_UsesCanonicalAggregation(t *testing.T) {
	o := &debateRun{
		issueLedgers: map[string]*IssueLedger{
			"claude": NewIssueLedger("claude"),
			"gpt4o":  NewIssueLedger("gpt4o"),
		},
	}

	o.issueLedgers["claude"].ApplyDelta(&StructurizeDelta{
		Add: []DeltaAddIssue{{
			Severity:    "high",
			Category:    "security",
			File:        "auth.go",
			Line:        intPtr(42),
			Title:       "Missing authorization check in admin endpoint",
			Description: "Admin endpoint executes sensitive action without a role guard",
		}},
	}, 1)

	o.issueLedgers["gpt4o"].ApplyDelta(&StructurizeDelta{
		Add: []DeltaAddIssue{{
			Severity:    "high",
			Category:    "security",
			File:        "auth.go",
			Title:       "Missing authorization check in admin endpoint",
			Description: "Admin endpoint executes sensitive action without a role guard",
		}},
	}, 1)

	issues := o.structurizeIssuesFromLedgers(nil, &noopDisplay{})
	if len(issues) != 1 {
		t.Fatalf("expected 1 canonical issue, got %d", len(issues))
	}
	if len(issues[0].SupportedBy) != 2 {
		t.Fatalf("expected two supporters, got %v", issues[0].SupportedBy)
	}
	if len(issues[0].RaisedBy) != 2 {
		t.Fatalf("expected RaisedBy to contain both reviewers, got %v", issues[0].RaisedBy)
	}
}

func TestApplyCanonicalSignals_UpdatesSupportStates(t *testing.T) {
	line := 42
	base := CanonicalizeMergedIssues([]MergedIssue{
		{
			ReviewIssue: ReviewIssue{
				Severity:    "high",
				Category:    "security",
				File:        "auth.go",
				Line:        &line,
				Title:       "Missing authorization check",
				Description: "Admin endpoint executes sensitive action without a role guard",
			},
			RaisedBy:     []string{"claude"},
			IntroducedBy: []string{"claude"},
			SupportedBy:  []string{"claude"},
			Descriptions: []string{"Admin endpoint executes sensitive action without a role guard"},
			Mentions: []IssueMention{{
				ReviewerID:   "claude",
				LocalIssueID: "I1",
				Round:        1,
				Status:       "active",
			}},
		},
	})
	if len(base) != 1 {
		t.Fatalf("expected 1 base issue, got %d", len(base))
	}

	updated := ApplyCanonicalSignals(base, []CanonicalSignal{
		{ReviewerID: "gpt4o", IssueRef: "claude:I1", Round: 2, Action: "support"},
		{ReviewerID: "codex", IssueRef: "claude:I1", Round: 2, Action: "contest"},
		{ReviewerID: "claude", IssueRef: "claude:I1", Round: 3, Action: "withdraw"},
	})
	if len(updated) != 1 {
		t.Fatalf("expected 1 updated issue, got %d", len(updated))
	}

	issue := updated[0]
	if len(issue.SupportedBy) != 1 || issue.SupportedBy[0] != "gpt4o" {
		t.Fatalf("expected gpt4o to remain sole supporter, got %v", issue.SupportedBy)
	}
	if len(issue.WithdrawnBy) != 1 || issue.WithdrawnBy[0] != "claude" {
		t.Fatalf("expected claude to be withdrawn, got %v", issue.WithdrawnBy)
	}
	if len(issue.ContestedBy) != 1 || issue.ContestedBy[0] != "codex" {
		t.Fatalf("expected codex to contest, got %v", issue.ContestedBy)
	}
	if len(issue.RaisedBy) != 1 || issue.RaisedBy[0] != "gpt4o" {
		t.Fatalf("expected RaisedBy to mirror current supporters, got %v", issue.RaisedBy)
	}
}

func TestBuildCanonicalIssueSummary_IncludesIssueRefs(t *testing.T) {
	line := 7
	issues := CanonicalizeMergedIssues([]MergedIssue{
		{
			ReviewIssue: ReviewIssue{
				Severity:    "medium",
				Category:    "correctness",
				File:        "svc.go",
				Line:        &line,
				Title:       "Nil guard removed",
				Description: "The nil guard was removed before dereferencing the pointer",
			},
			RaisedBy:     []string{"claude"},
			IntroducedBy: []string{"claude"},
			SupportedBy:  []string{"claude"},
			Descriptions: []string{"The nil guard was removed before dereferencing the pointer"},
			Mentions: []IssueMention{{
				ReviewerID:   "claude",
				LocalIssueID: "I2",
				Round:        1,
				Status:       "active",
			}},
		},
	})

	summary := BuildCanonicalIssueSummary(issues)
	if !strings.Contains(summary, "claude:I2") {
		t.Fatalf("expected issueRef in summary, got:\n%s", summary)
	}
	if !strings.Contains(summary, issues[0].CanonicalID) {
		t.Fatalf("expected canonical id in summary, got:\n%s", summary)
	}
}

func TestApplyCanonicalSignals_WithdrawLastSupportDropsIssue(t *testing.T) {
	line := 11
	base := CanonicalizeMergedIssues([]MergedIssue{
		{
			ReviewIssue: ReviewIssue{
				Severity:    "medium",
				Category:    "correctness",
				File:        "svc.go",
				Line:        &line,
				Title:       "Nil guard removed",
				Description: "The nil guard was removed before dereferencing the pointer",
			},
			RaisedBy:     []string{"claude"},
			IntroducedBy: []string{"claude"},
			SupportedBy:  []string{"claude"},
			Descriptions: []string{"The nil guard was removed before dereferencing the pointer"},
			Mentions: []IssueMention{{
				ReviewerID:   "claude",
				LocalIssueID: "I1",
				Round:        1,
				Status:       "active",
			}},
		},
	})

	updated := ApplyCanonicalSignals(base, []CanonicalSignal{
		{ReviewerID: "claude", IssueRef: "claude:I1", Round: 2, Action: "withdraw"},
	})
	if len(updated) != 0 {
		t.Fatalf("expected issue to disappear after last support is withdrawn, got %+v", updated)
	}
}
