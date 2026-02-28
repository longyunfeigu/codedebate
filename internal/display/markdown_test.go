package display

import (
	"strings"
	"testing"

	"github.com/longyunfeigu/codedebate/internal/orchestrator"
)

func TestFormatMarkdownFromResult_DefaultReportOmitsTranscript(t *testing.T) {
	report := FormatMarkdownFromResult(&DebateResultForMarkdown{
		PRNumber:        "MR !89",
		FinalConclusion: "## 共识点\n- Ship after fixing the blocking issues.\n\n如果你希望我把 Not verifiable 的部分变成可核实结论，我可以继续看。",
		Context: &orchestrator.GatheredContext{
			Summary: "## Overview\nTouches auth and routing.",
			AffectedModules: []orchestrator.AffectedModule{
				{
					Name:          "Authentication",
					Path:          "internal/auth",
					AffectedFiles: []string{"internal/auth/handler.go", "internal/auth/service.go"},
					ImpactLevel:   "core",
				},
				{
					Name:          "API Router",
					Path:          "internal/api",
					AffectedFiles: []string{"internal/api/router.go", "internal/auth/service.go"},
					ImpactLevel:   "moderate",
				},
			},
			RelatedPRs: []orchestrator.RelatedPR{
				{Number: 38, Title: "refactor: extract auth middleware"},
			},
		},
		Analysis: "## Summary\n### Frontend\nThe change adds document rename support.\n\nIf you'd like, I can inspect more files.",
		Messages: []MessageForMarkdown{
			{ReviewerID: "claude", Round: 1, Content: "[tool] Read: /tmp/prompt\n\nReviewer trace"},
		},
		Summaries: []SummaryForMarkdown{
			{ReviewerID: "claude", Summary: "## Key Points\nBackend refactor is incomplete.\n\n如果你希望我继续审阅，我可以补更多结论。"},
		},
		ParsedIssues: []MergedIssueForMarkdown{
			{
				Severity:     "high",
				Title:        "Half-finished backend cleanup",
				File:         "backend/application/services/document_service.py",
				Line:         252,
				Description:  "The DB-only rename flow still contains stale vector-store branches.",
				SuggestedFix: "Remove the dead branch or complete the async sync path.",
				RaisedBy:     []string{"claude"},
			},
		},
	})

	if strings.Contains(report, "## Debate Appendix") {
		t.Fatalf("default report should omit debate transcript:\n%s", report)
	}
	if strings.Contains(report, "[tool] Read") {
		t.Fatalf("default report should not leak tool trace:\n%s", report)
	}
	if strings.Contains(report, "如果你希望") || strings.Contains(report, "If you'd like") {
		t.Fatalf("default report should strip follow-up offers:\n%s", report)
	}
	if strings.Contains(report, "### 最终结论") {
		t.Fatalf("final conclusion should not repeat the section title:\n%s", report)
	}
	if !strings.Contains(report, "### 共识点") {
		t.Fatalf("embedded final conclusion headings should be rebased:\n%s", report)
	}
	if !strings.Contains(report, "### Summary") || !strings.Contains(report, "#### Frontend") {
		t.Fatalf("embedded analysis headings should be rebased:\n%s", report)
	}
	if !strings.Contains(report, "### claude\n\n#### Key Points") {
		t.Fatalf("embedded summary headings should be rebased under reviewer subsection:\n%s", report)
	}
	if !strings.Contains(report, "## System Context") ||
		!strings.Contains(report, "### Affected Modules") ||
		!strings.Contains(report, "### Affected Files") ||
		!strings.Contains(report, "### Related Changes") {
		t.Fatalf("system context sections should be present:\n%s", report)
	}
	if !strings.Contains(report, "#### Authentication") ||
		!strings.Contains(report, "- Path: `internal/auth`") ||
		!strings.Contains(report, "- `internal/api/router.go`") ||
		!strings.Contains(report, "- #38: refactor: extract auth middleware") {
		t.Fatalf("system context details should include modules, files, and related changes:\n%s", report)
	}
	if !strings.Contains(report, "### Context Summary\n\n#### Overview") {
		t.Fatalf("context summary headings should be rebased:\n%s", report)
	}

	finalIdx := strings.Index(report, "## Final Conclusion")
	contextIdx := strings.Index(report, "## System Context")
	issuesIdx := strings.Index(report, "## Issues (1)")
	analysisIdx := strings.Index(report, "## Analysis")
	summariesIdx := strings.Index(report, "## Reviewer Summaries")
	if !(finalIdx >= 0 && contextIdx > finalIdx && issuesIdx > contextIdx && analysisIdx > issuesIdx && summariesIdx > analysisIdx) {
		t.Fatalf("unexpected report section order:\n%s", report)
	}
	if !strings.Contains(report, "Why: The DB-only rename flow still contains stale vector-store branches.") {
		t.Fatalf("issues section should include the issue description:\n%s", report)
	}
}

func TestFormatMarkdownFromResultWithOptions_GroupsTranscriptByRound(t *testing.T) {
	report := FormatMarkdownFromResultWithOptions(&DebateResultForMarkdown{
		PRNumber: "MR !89",
		Messages: []MessageForMarkdown{
			{ReviewerID: "claude", Content: "[tool] Read: /tmp/prompt\n\nFirst round finding"},
			{ReviewerID: "codex", Content: "First round rebuttal"},
			{ReviewerID: "claude", Content: "## Follow-up\nSecond round follow-up\n\nIf you want, I can inspect more files."},
			{ReviewerID: "codex", Content: "[tool] Read: /tmp/other\n\nSecond round agreement"},
		},
	}, MarkdownOptions{
		IncludeDebateTranscript: true,
	})

	if !strings.Contains(report, "## Debate Appendix") {
		t.Fatalf("expected transcript appendix:\n%s", report)
	}
	if !strings.Contains(report, "### Round 1") || !strings.Contains(report, "### Round 2") {
		t.Fatalf("expected round grouping in appendix:\n%s", report)
	}
	if strings.Contains(report, "[tool] Read") {
		t.Fatalf("appendix should strip tool trace lines:\n%s", report)
	}
	if strings.Contains(report, "If you want, I can inspect more files.") {
		t.Fatalf("appendix should strip follow-up offer paragraphs:\n%s", report)
	}
	if !strings.Contains(report, "### Round 2\n\n#### claude\n\n##### Follow-up") {
		t.Fatalf("appendix should rebase embedded headings under reviewer subsection:\n%s", report)
	}

	round1Idx := strings.Index(report, "### Round 1")
	round2Idx := strings.Index(report, "### Round 2")
	claudeIdx := strings.Index(report, "#### claude")
	codexIdx := strings.Index(report, "#### codex")
	if !(round1Idx >= 0 && round2Idx > round1Idx && claudeIdx > round1Idx && codexIdx > claudeIdx) {
		t.Fatalf("unexpected appendix ordering:\n%s", report)
	}
	if !strings.Contains(report, "First round finding") || !strings.Contains(report, "Second round agreement") {
		t.Fatalf("appendix should keep reviewer content after sanitizing traces:\n%s", report)
	}
}
