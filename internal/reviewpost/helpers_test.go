package reviewpost

import (
	"strings"
	"testing"

	"github.com/longyunfeigu/codedebate/internal/orchestrator"
	"github.com/longyunfeigu/codedebate/internal/platform"
)

func intPtr(v int) *int { return &v }

func TestConvertIssuesToPlatform(t *testing.T) {
	issues := []orchestrator.MergedIssue{
		{
			ReviewIssue: orchestrator.ReviewIssue{
				Severity:     "high",
				File:         "main.go",
				Line:         intPtr(42),
				Title:        "SQL injection",
				Description:  "User input not sanitized",
				SuggestedFix: "Use parameterized queries",
			},
			RaisedBy: []string{"claude", "gpt4o"},
		},
		{
			ReviewIssue: orchestrator.ReviewIssue{
				Severity: "low",
				File:     "util.go",
				Title:    "Unused variable",
			},
			RaisedBy: nil,
		},
	}

	result := ConvertIssuesToPlatform(issues)

	if len(result) != 2 {
		t.Fatalf("expected 2 issues, got %d", len(result))
	}

	if result[0].File != "main.go" || result[0].Line == nil || *result[0].Line != 42 {
		t.Errorf("issue 0: file=%q line=%v", result[0].File, result[0].Line)
	}
	if result[0].RaisedBy != "claude, gpt4o" {
		t.Errorf("issue 0: raisedBy=%q, want 'claude, gpt4o'", result[0].RaisedBy)
	}
	if result[1].RaisedBy != "" {
		t.Errorf("issue 1: raisedBy=%q, want empty", result[1].RaisedBy)
	}
}

func TestBuildSummaryNoteBody(t *testing.T) {
	body := BuildSummaryNoteBody("All good, LGTM.")

	if !strings.HasPrefix(body, CodeDebateSummaryMarker) {
		t.Error("expected body to start with summary marker")
	}
	if !strings.Contains(body, "## CodeDebate Code Review Summary") {
		t.Error("expected body to contain header")
	}
	if !strings.Contains(body, "All good, LGTM.") {
		t.Error("expected body to contain conclusion")
	}
}

func TestSupportsSummaryPosting_Nil(t *testing.T) {
	if SupportsSummaryPosting(nil) {
		t.Error("expected nil platform to not support summary posting")
	}
}

// mockPlatform is a minimal implementation of platform.Platform for testing.
type mockPlatform struct{ platform.Platform }

func (m *mockPlatform) Name() string { return "test-mock" }

func TestSupportsSummaryPosting_NoSummaryInterface(t *testing.T) {
	plat := &mockPlatform{}
	if SupportsSummaryPosting(plat) {
		t.Error("expected platform without summary interface to return false")
	}
}

func TestUpsertSummaryNote_NoSummaryInterface(t *testing.T) {
	plat := &mockPlatform{}
	err := UpsertSummaryNote(plat, "1", "repo", "body")
	if err == nil {
		t.Error("expected error for platform without summary interface")
	}
}
