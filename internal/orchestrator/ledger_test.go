package orchestrator

import (
	"fmt"
	"strings"
	"testing"
)

func intPtr(v int) *int { return &v }

func TestLedger_AddIssues(t *testing.T) {
	ledger := NewIssueLedger("r1")
	ledger.ApplyDelta(&StructurizeDelta{
		Add: []DeltaAddIssue{
			{Severity: "high", File: "a.go", Line: intPtr(10), Title: "A", Description: "desc A"},
			{Severity: "medium", File: "b.go", Title: "B", Description: "desc B"},
		},
	}, 1)

	if len(ledger.Issues) != 2 {
		t.Fatalf("expected 2 issues, got %d", len(ledger.Issues))
	}
	if _, ok := ledger.Issues["I1"]; !ok {
		t.Fatal("expected I1 to exist")
	}
	if _, ok := ledger.Issues["I2"]; !ok {
		t.Fatal("expected I2 to exist")
	}
}

func TestLedger_RetractIssue(t *testing.T) {
	ledger := NewIssueLedger("r1")
	ledger.ApplyDelta(&StructurizeDelta{Add: []DeltaAddIssue{{Severity: "high", File: "a.go", Title: "A", Description: "desc"}}}, 1)
	ledger.ApplyDelta(&StructurizeDelta{Retract: []string{"I1"}}, 2)

	if ledger.Issues["I1"].Status != "retracted" {
		t.Fatalf("expected I1 to be retracted, got %q", ledger.Issues["I1"].Status)
	}
}

func TestLedger_UpdateIssue(t *testing.T) {
	ledger := NewIssueLedger("r1")
	ledger.ApplyDelta(&StructurizeDelta{Add: []DeltaAddIssue{{Severity: "low", File: "a.go", Title: "A", Description: "desc"}}}, 1)

	high := "high"
	desc := "updated"
	line := 42
	ledger.ApplyDelta(&StructurizeDelta{Update: []DeltaUpdateIssue{{ID: "I1", Severity: &high, Description: &desc, Line: &line}}}, 2)

	issue := ledger.Issues["I1"]
	if issue.Severity != "high" {
		t.Fatalf("severity = %q, want high", issue.Severity)
	}
	if issue.Description != "updated" {
		t.Fatalf("description = %q, want updated", issue.Description)
	}
	if issue.Line == nil || *issue.Line != 42 {
		t.Fatalf("line = %v, want 42", issue.Line)
	}
}

func TestLedger_BuildSummary(t *testing.T) {
	ledger := NewIssueLedger("r1")
	ledger.ApplyDelta(&StructurizeDelta{Add: []DeltaAddIssue{{Severity: "high", File: "a.go", Line: intPtr(10), Title: "Issue A", Description: "desc"}}}, 1)

	summary := ledger.BuildSummary()
	if !strings.Contains(summary, "| ID | Severity | File:Line | Title |") {
		t.Fatalf("summary missing table header: %s", summary)
	}
	if !strings.Contains(summary, "I1") || !strings.Contains(summary, "a.go:10") {
		t.Fatalf("summary missing issue row: %s", summary)
	}
	if !strings.Contains(summary, "(1 active issues)") {
		t.Fatalf("summary missing issue count: %s", summary)
	}
}

func TestLedger_BuildSummary_Limit(t *testing.T) {
	ledger := NewIssueLedger("r1")
	adds := make([]DeltaAddIssue, 0, 105)
	for i := 0; i < 105; i++ {
		adds = append(adds, DeltaAddIssue{
			Severity:    "low",
			File:        fmt.Sprintf("f%d.go", i),
			Title:       fmt.Sprintf("issue %d", i),
			Description: "desc",
		})
	}
	ledger.ApplyDelta(&StructurizeDelta{Add: adds}, 1)

	summary := ledger.BuildSummary()
	if !strings.Contains(summary, "showing 100 of 105") {
		t.Fatalf("expected truncation marker, got: %s", summary)
	}
}

func TestLedger_ToMergedIssues(t *testing.T) {
	ledger := NewIssueLedger("r1")
	ledger.ApplyDelta(&StructurizeDelta{Add: []DeltaAddIssue{{Severity: "high", File: "a.go", Title: "A", Description: "desc"}}}, 1)
	ledger.ApplyDelta(&StructurizeDelta{Add: []DeltaAddIssue{{Severity: "medium", File: "b.go", Title: "B", Description: "desc"}}}, 2)
	ledger.ApplyDelta(&StructurizeDelta{Retract: []string{"I1"}}, 3)

	issues := ledger.ToMergedIssues()
	if len(issues) != 1 {
		t.Fatalf("expected 1 active issue, got %d", len(issues))
	}
	if issues[0].Title != "B" {
		t.Fatalf("expected remaining issue to be B, got %q", issues[0].Title)
	}
	if len(issues[0].RaisedBy) != 1 || issues[0].RaisedBy[0] != "r1" {
		t.Fatalf("unexpected raisedBy: %v", issues[0].RaisedBy)
	}
}

func TestLedger_UnknownID(t *testing.T) {
	ledger := NewIssueLedger("r1")
	ledger.ApplyDelta(&StructurizeDelta{Add: []DeltaAddIssue{{Severity: "high", File: "a.go", Title: "A", Description: "desc"}}}, 1)

	changed := "changed"
	ledger.ApplyDelta(&StructurizeDelta{
		Retract: []string{"I999"},
		Update:  []DeltaUpdateIssue{{ID: "I888", Title: &changed}},
	}, 2)

	if ledger.Issues["I1"].Title != "A" {
		t.Fatalf("unexpected mutation from unknown ID update: %q", ledger.Issues["I1"].Title)
	}
	if ledger.Issues["I1"].Status != "active" {
		t.Fatalf("unexpected status from unknown ID retract: %q", ledger.Issues["I1"].Status)
	}
}
