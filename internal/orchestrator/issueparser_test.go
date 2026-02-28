package orchestrator

import (
	"math"
	"testing"
)

// ---------------------------------------------------------------------------
// TestParseReviewerOutput
// ---------------------------------------------------------------------------

func TestParseReviewerOutput(t *testing.T) {
	t.Run("valid JSON inside json code fence", func(t *testing.T) {
		response := "Here is my review:\n```json\n" + `{
  "issues": [
    {
      "severity": "high",
      "category": "security",
      "file": "auth.go",
      "line": 42,
      "title": "SQL injection risk",
      "description": "User input is not sanitized before query."
    }
  ],
  "verdict": "request_changes",
  "summary": "Found a security issue."
}` + "\n```\nThat's all."

		pr := ParseReviewerOutput(response)
		if pr.Output == nil {
			t.Fatal("expected non-nil output")
		}
		if len(pr.Output.Issues) != 1 {
			t.Fatalf("expected 1 issue, got %d", len(pr.Output.Issues))
		}
		issue := pr.Output.Issues[0]
		if issue.Severity != "high" {
			t.Errorf("severity = %q, want %q", issue.Severity, "high")
		}
		if issue.File != "auth.go" {
			t.Errorf("file = %q, want %q", issue.File, "auth.go")
		}
		if issue.Title != "SQL injection risk" {
			t.Errorf("title = %q, want %q", issue.Title, "SQL injection risk")
		}
		if issue.Line == nil || *issue.Line != 42 {
			t.Errorf("line = %v, want 42", issue.Line)
		}
		if pr.Output.Verdict != "request_changes" {
			t.Errorf("verdict = %q, want %q", pr.Output.Verdict, "request_changes")
		}
		if pr.Output.Summary != "Found a security issue." {
			t.Errorf("summary = %q, want %q", pr.Output.Summary, "Found a security issue.")
		}
		if pr.ParseError != nil {
			t.Errorf("unexpected parse error: %v", pr.ParseError)
		}
	})

	t.Run("raw JSON object without code fence", func(t *testing.T) {
		response := `Some preamble text. {"issues": [{"severity":"medium","category":"style","file":"main.go","title":"Naming convention","description":"Variable name is unclear."}], "verdict":"comment", "summary":"Minor style issue."}`

		pr := ParseReviewerOutput(response)
		if pr.Output == nil {
			t.Fatal("expected non-nil output")
		}
		if len(pr.Output.Issues) != 1 {
			t.Fatalf("expected 1 issue, got %d", len(pr.Output.Issues))
		}
		if pr.Output.Issues[0].Severity != "medium" {
			t.Errorf("severity = %q, want %q", pr.Output.Issues[0].Severity, "medium")
		}
	})

	t.Run("invalid JSON returns parse error", func(t *testing.T) {
		response := "```json\n{not valid json}\n```"
		pr := ParseReviewerOutput(response)
		if pr.ParseError == nil {
			t.Error("expected parse error for invalid JSON")
		}
	})

	t.Run("no JSON block returns parse error", func(t *testing.T) {
		response := "This is just plain text with no JSON."
		pr := ParseReviewerOutput(response)
		if pr.ParseError == nil {
			t.Error("expected parse error for missing JSON")
		}
	})

	t.Run("missing required field: no title", func(t *testing.T) {
		response := "```json\n" + `{"issues":[{"severity":"high","file":"a.go","title":"","description":"desc"}],"verdict":"comment","summary":"s"}` + "\n```"
		pr := ParseReviewerOutput(response)
		if pr.Output == nil {
			t.Fatal("expected non-nil output")
		}
		if len(pr.Output.Issues) != 0 {
			t.Errorf("expected 0 issues (title empty), got %d", len(pr.Output.Issues))
		}
	})

	t.Run("missing required field: no file", func(t *testing.T) {
		response := "```json\n" + `{"issues":[{"severity":"high","file":"","title":"Bug","description":"desc"}],"verdict":"comment","summary":"s"}` + "\n```"
		pr := ParseReviewerOutput(response)
		if pr.Output == nil {
			t.Fatal("expected non-nil output")
		}
		if len(pr.Output.Issues) != 0 {
			t.Errorf("expected 0 issues (file empty), got %d", len(pr.Output.Issues))
		}
	})

	t.Run("missing required field: invalid severity", func(t *testing.T) {
		response := "```json\n" + `{"issues":[{"severity":"urgent","file":"a.go","title":"Bug","description":"desc"}],"verdict":"comment","summary":"s"}` + "\n```"
		pr := ParseReviewerOutput(response)
		if pr.Output == nil {
			t.Fatal("expected non-nil output")
		}
		if len(pr.Output.Issues) != 0 {
			t.Errorf("expected 0 issues (invalid severity), got %d", len(pr.Output.Issues))
		}
		// Should also have schema errors
		if len(pr.SchemaErrors) == 0 {
			t.Error("expected schema validation errors for invalid severity")
		}
	})

	t.Run("missing required field: no description", func(t *testing.T) {
		response := "```json\n" + `{"issues":[{"severity":"high","file":"a.go","title":"Bug","description":""}],"verdict":"comment","summary":"s"}` + "\n```"
		pr := ParseReviewerOutput(response)
		if pr.Output == nil {
			t.Fatal("expected non-nil output")
		}
		if len(pr.Output.Issues) != 0 {
			t.Errorf("expected 0 issues (description empty), got %d", len(pr.Output.Issues))
		}
	})

	t.Run("all valid severity values accepted", func(t *testing.T) {
		severities := []string{"critical", "high", "medium", "low", "nitpick"}
		for _, sev := range severities {
			response := "```json\n" + `{"issues":[{"severity":"` + sev + `","file":"x.go","title":"T","description":"D"}],"verdict":"comment","summary":"s"}` + "\n```"
			pr := ParseReviewerOutput(response)
			if pr.Output == nil {
				t.Fatalf("expected non-nil output for severity %q", sev)
			}
			if len(pr.Output.Issues) != 1 {
				t.Errorf("severity %q: expected 1 issue, got %d", sev, len(pr.Output.Issues))
			}
		}
	})

	t.Run("optional fields: line, endLine, suggestedFix, codeSnippet", func(t *testing.T) {
		response := "```json\n" + `{
  "issues": [{
    "severity": "low",
    "category": "style",
    "file": "util.go",
    "line": 10,
    "endLine": 15,
    "title": "Refactor loop",
    "description": "Could use range.",
    "suggestedFix": "Use for range instead",
    "codeSnippet": "for i := 0; i < len(s); i++ {"
  }],
  "verdict": "approve",
  "summary": "Minor suggestion."
}` + "\n```"

		pr := ParseReviewerOutput(response)
		if pr.Output == nil {
			t.Fatal("expected non-nil output")
		}
		if len(pr.Output.Issues) != 1 {
			t.Fatalf("expected 1 issue, got %d", len(pr.Output.Issues))
		}
		issue := pr.Output.Issues[0]
		if issue.Line == nil || *issue.Line != 10 {
			t.Errorf("line = %v, want 10", issue.Line)
		}
		if issue.EndLine == nil || *issue.EndLine != 15 {
			t.Errorf("endLine = %v, want 15", issue.EndLine)
		}
		if issue.SuggestedFix != "Use for range instead" {
			t.Errorf("suggestedFix = %q", issue.SuggestedFix)
		}
		if issue.CodeSnippet != "for i := 0; i < len(s); i++ {" {
			t.Errorf("codeSnippet = %q", issue.CodeSnippet)
		}
	})

	t.Run("endLine ignored if less than line", func(t *testing.T) {
		response := "```json\n" + `{"issues":[{"severity":"low","file":"a.go","line":20,"endLine":10,"title":"T","description":"D"}],"verdict":"comment","summary":"s"}` + "\n```"
		pr := ParseReviewerOutput(response)
		if pr.Output == nil {
			t.Fatal("expected non-nil output")
		}
		if len(pr.Output.Issues) != 1 {
			t.Fatalf("expected 1 issue, got %d", len(pr.Output.Issues))
		}
		if pr.Output.Issues[0].EndLine != nil {
			t.Errorf("endLine should be nil when less than line, got %d", *pr.Output.Issues[0].EndLine)
		}
	})

	t.Run("invalid verdict defaults to comment", func(t *testing.T) {
		response := "```json\n" + `{"issues":[],"verdict":"invalid_verdict","summary":"s"}` + "\n```"
		pr := ParseReviewerOutput(response)
		if pr.Output == nil {
			t.Fatal("expected non-nil output")
		}
		if pr.Output.Verdict != "comment" {
			t.Errorf("verdict = %q, want %q", pr.Output.Verdict, "comment")
		}
	})

	t.Run("default category is general", func(t *testing.T) {
		response := "```json\n" + `{"issues":[{"severity":"low","file":"a.go","title":"T","description":"D"}],"verdict":"comment","summary":"s"}` + "\n```"
		pr := ParseReviewerOutput(response)
		if pr.Output == nil || len(pr.Output.Issues) != 1 {
			t.Fatal("unexpected parse result")
		}
		if pr.Output.Issues[0].Category != "general" {
			t.Errorf("category = %q, want %q", pr.Output.Issues[0].Category, "general")
		}
	})

	t.Run("raisedBy field parsed from JSON", func(t *testing.T) {
		response := "```json\n" + `{"issues":[{"severity":"low","file":"a.go","title":"T","description":"D","raisedBy":["reviewer-1","reviewer-2"]}],"verdict":"comment","summary":"s"}` + "\n```"
		pr := ParseReviewerOutput(response)
		if pr.Output == nil || len(pr.Output.Issues) != 1 {
			t.Fatal("unexpected parse result")
		}
		if len(pr.Output.Issues[0].ClaimedBy) != 2 {
			t.Errorf("raisedBy len = %d, want 2", len(pr.Output.Issues[0].ClaimedBy))
		}
	})

	t.Run("line range extracted from file path when no line field", func(t *testing.T) {
		response := "```json\n" + `{"issues":[{"severity":"high","file":"backend/services/base.py:37-48","title":"Bug","description":"Issue found"}],"verdict":"comment","summary":"s"}` + "\n```"
		pr := ParseReviewerOutput(response)
		if pr.Output == nil || len(pr.Output.Issues) != 1 {
			t.Fatal("unexpected parse result")
		}
		issue := pr.Output.Issues[0]
		if issue.File != "backend/services/base.py" {
			t.Errorf("file = %q, want %q", issue.File, "backend/services/base.py")
		}
		if issue.Line == nil || *issue.Line != 37 {
			t.Errorf("line = %v, want 37", issue.Line)
		}
		if issue.EndLine == nil || *issue.EndLine != 48 {
			t.Errorf("endLine = %v, want 48", issue.EndLine)
		}
	})

	t.Run("single line extracted from file path", func(t *testing.T) {
		response := "```json\n" + `{"issues":[{"severity":"low","file":"main.go:100","title":"T","description":"D"}],"verdict":"comment","summary":"s"}` + "\n```"
		pr := ParseReviewerOutput(response)
		if pr.Output == nil || len(pr.Output.Issues) != 1 {
			t.Fatal("unexpected parse result")
		}
		issue := pr.Output.Issues[0]
		if issue.File != "main.go" {
			t.Errorf("file = %q, want %q", issue.File, "main.go")
		}
		if issue.Line == nil || *issue.Line != 100 {
			t.Errorf("line = %v, want 100", issue.Line)
		}
		if issue.EndLine != nil {
			t.Errorf("endLine = %v, want nil", issue.EndLine)
		}
	})

	t.Run("explicit line field takes priority over path-embedded line", func(t *testing.T) {
		response := "```json\n" + `{"issues":[{"severity":"low","file":"main.go:100-200","line":50,"endLine":60,"title":"T","description":"D"}],"verdict":"comment","summary":"s"}` + "\n```"
		pr := ParseReviewerOutput(response)
		if pr.Output == nil || len(pr.Output.Issues) != 1 {
			t.Fatal("unexpected parse result")
		}
		issue := pr.Output.Issues[0]
		if issue.File != "main.go" {
			t.Errorf("file = %q, want %q", issue.File, "main.go")
		}
		if issue.Line == nil || *issue.Line != 50 {
			t.Errorf("line = %v, want 50 (explicit field should take priority)", issue.Line)
		}
		if issue.EndLine == nil || *issue.EndLine != 60 {
			t.Errorf("endLine = %v, want 60 (explicit field should take priority)", issue.EndLine)
		}
	})

	t.Run("mixed valid and invalid issues filters correctly", func(t *testing.T) {
		response := "```json\n" + `{"issues":[
  {"severity":"high","file":"a.go","title":"Valid","description":"Good issue"},
  {"severity":"invalid","file":"b.go","title":"Bad severity","description":"D"},
  {"severity":"low","file":"","title":"No file","description":"D"},
  {"severity":"medium","file":"c.go","title":"Also valid","description":"Another good one"}
],"verdict":"comment","summary":"s"}` + "\n```"
		pr := ParseReviewerOutput(response)
		if pr.Output == nil {
			t.Fatal("expected non-nil output")
		}
		if len(pr.Output.Issues) != 2 {
			t.Errorf("expected 2 valid issues, got %d", len(pr.Output.Issues))
		}
		// Should have schema errors for the invalid issues
		if len(pr.SchemaErrors) == 0 {
			t.Error("expected schema validation errors for mixed valid/invalid")
		}
	})

	t.Run("ParseResult contains RawJSON", func(t *testing.T) {
		response := "```json\n{\"issues\":[]}\n```"
		pr := ParseReviewerOutput(response)
		if pr.RawJSON == "" {
			t.Error("expected non-empty RawJSON")
		}
	})

	t.Run("ParseResult schema errors for missing required fields", func(t *testing.T) {
		response := "```json\n" + `{"issues":[{"severity":"high"}]}` + "\n```"
		pr := ParseReviewerOutput(response)
		if len(pr.SchemaErrors) == 0 {
			t.Error("expected schema errors for missing required fields")
		}
	})
}

// ---------------------------------------------------------------------------
// TestParseFocusAreas
// ---------------------------------------------------------------------------

func TestParseFocusAreas(t *testing.T) {
	t.Run("extracts bullet items from focus section", func(t *testing.T) {
		analysis := `## Analysis
Some analysis text here.

## Suggested Review Focus
- Check authentication flow
- Review database queries for N+1
* Verify error handling in API layer

## Other Section
More text.`

		areas := ParseFocusAreas(analysis)
		if len(areas) != 3 {
			t.Fatalf("expected 3 focus areas, got %d: %v", len(areas), areas)
		}
		expected := []string{
			"Check authentication flow",
			"Review database queries for N+1",
			"Verify error handling in API layer",
		}
		for i, want := range expected {
			if areas[i] != want {
				t.Errorf("area[%d] = %q, want %q", i, areas[i], want)
			}
		}
	})

	t.Run("focus section at end of string", func(t *testing.T) {
		analysis := `## Suggested Review Focus
- Item one
- Item two`

		areas := ParseFocusAreas(analysis)
		if len(areas) != 2 {
			t.Fatalf("expected 2 focus areas, got %d", len(areas))
		}
	})

	t.Run("no focus section returns nil", func(t *testing.T) {
		analysis := "## Analysis\nSome text without the focus section."
		areas := ParseFocusAreas(analysis)
		if areas != nil {
			t.Errorf("expected nil, got %v", areas)
		}
	})

	t.Run("empty string returns nil", func(t *testing.T) {
		areas := ParseFocusAreas("")
		if areas != nil {
			t.Errorf("expected nil, got %v", areas)
		}
	})

	t.Run("focus section with only whitespace returns nil", func(t *testing.T) {
		analysis := "## Suggested Review Focus\n   \n   \n"
		areas := ParseFocusAreas(analysis)
		if areas != nil {
			t.Errorf("expected nil for whitespace-only focus section, got %v", areas)
		}
	})
}

// ---------------------------------------------------------------------------
// TestDeduplicateIssues
// ---------------------------------------------------------------------------

func TestDeduplicateIssues(t *testing.T) {
	intPtr := func(v int) *int { return &v }

	t.Run("similar issues from different reviewers merged", func(t *testing.T) {
		issues := map[string][]ReviewIssue{
			"reviewer-1": {
				{
					Severity:    "high",
					File:        "auth.go",
					Line:        intPtr(10),
					Title:       "SQL injection vulnerability found",
					Description: "User input not sanitized in query builder module",
					Category:    "security",
				},
			},
			"reviewer-2": {
				{
					Severity:    "medium",
					File:        "auth.go",
					Line:        intPtr(12),
					Title:       "SQL injection vulnerability detected",
					Description: "User input not sanitized in query builder module properly",
					Category:    "security",
				},
			},
		}

		merged := DeduplicateIssues(issues)
		if len(merged) != 1 {
			t.Fatalf("expected 1 merged issue, got %d", len(merged))
		}
		if len(merged[0].RaisedBy) != 2 {
			t.Errorf("expected 2 reviewers in RaisedBy, got %d", len(merged[0].RaisedBy))
		}
		// Should keep highest severity
		if merged[0].Severity != "high" {
			t.Errorf("severity = %q, want %q (highest)", merged[0].Severity, "high")
		}
	})

	t.Run("different issues not merged", func(t *testing.T) {
		issues := map[string][]ReviewIssue{
			"reviewer-1": {
				{
					Severity:    "high",
					File:        "auth.go",
					Line:        intPtr(10),
					Title:       "SQL injection vulnerability",
					Description: "User input not sanitized in query builder",
					Category:    "security",
				},
			},
			"reviewer-2": {
				{
					Severity:    "low",
					File:        "util.go",
					Line:        intPtr(50),
					Title:       "Unused variable cleanup",
					Description: "Remove dead code and unused variables",
					Category:    "style",
				},
			},
		}

		merged := DeduplicateIssues(issues)
		if len(merged) != 2 {
			t.Fatalf("expected 2 separate issues, got %d", len(merged))
		}
	})

	t.Run("same file overlapping lines within 5 treated as similar", func(t *testing.T) {
		issues := map[string][]ReviewIssue{
			"reviewer-1": {
				{
					Severity:    "medium",
					File:        "handler.go",
					Line:        intPtr(20),
					Title:       "Error handling missing in handler function",
					Description: "The handler function does not properly handle errors from downstream calls",
					Category:    "reliability",
				},
			},
			"reviewer-2": {
				{
					Severity:    "medium",
					File:        "handler.go",
					Line:        intPtr(24),
					Title:       "Error handling missing in handler",
					Description: "The handler function does not properly handle errors from downstream service",
					Category:    "reliability",
				},
			},
		}

		merged := DeduplicateIssues(issues)
		if len(merged) != 1 {
			t.Fatalf("expected 1 merged issue (overlapping lines within 5), got %d", len(merged))
		}
	})

	t.Run("same file non-overlapping lines not merged", func(t *testing.T) {
		issues := map[string][]ReviewIssue{
			"reviewer-1": {
				{
					Severity:    "medium",
					File:        "handler.go",
					Line:        intPtr(10),
					Title:       "Error handling missing in handler function",
					Description: "The handler function does not properly handle errors from downstream calls",
					Category:    "reliability",
				},
			},
			"reviewer-2": {
				{
					Severity:    "medium",
					File:        "handler.go",
					Line:        intPtr(100),
					Title:       "Error handling missing in handler function",
					Description: "The handler function does not properly handle errors from downstream calls",
					Category:    "reliability",
				},
			},
		}

		merged := DeduplicateIssues(issues)
		if len(merged) != 2 {
			t.Fatalf("expected 2 separate issues (non-overlapping lines), got %d", len(merged))
		}
	})

	t.Run("severity merging keeps highest", func(t *testing.T) {
		tests := []struct {
			name     string
			sev1     string
			sev2     string
			wantBest string
		}{
			{"critical beats high", "high", "critical", "critical"},
			{"high beats medium", "medium", "high", "high"},
			{"medium beats low", "low", "medium", "medium"},
			{"low beats nitpick", "nitpick", "low", "low"},
			{"critical beats nitpick", "nitpick", "critical", "critical"},
		}

		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				issues := map[string][]ReviewIssue{
					"reviewer-1": {
						{
							Severity:    tt.sev1,
							File:        "x.go",
							Title:       "Duplicate error handling problem found",
							Description: "The error handling approach is problematic and needs to be fixed immediately",
							Category:    "reliability",
						},
					},
					"reviewer-2": {
						{
							Severity:    tt.sev2,
							File:        "x.go",
							Title:       "Duplicate error handling problem detected",
							Description: "The error handling approach is problematic and requires immediate attention",
							Category:    "reliability",
						},
					},
				}

				merged := DeduplicateIssues(issues)
				if len(merged) != 1 {
					t.Fatalf("expected 1 merged issue, got %d", len(merged))
				}
				if merged[0].Severity != tt.wantBest {
					t.Errorf("severity = %q, want %q", merged[0].Severity, tt.wantBest)
				}
			})
		}
	})

	t.Run("output sorted by severity critical first", func(t *testing.T) {
		issues := map[string][]ReviewIssue{
			"reviewer-1": {
				{Severity: "low", File: "a.go", Title: "Low priority cleanup task", Description: "Clean up unused imports and dead code", Category: "style"},
				{Severity: "critical", File: "b.go", Title: "Critical security vulnerability found", Description: "Remote code execution via unsanitized input", Category: "security"},
				{Severity: "medium", File: "c.go", Title: "Medium importance refactoring needed", Description: "Extract common logic into shared utility function", Category: "quality"},
			},
		}

		merged := DeduplicateIssues(issues)
		if len(merged) != 3 {
			t.Fatalf("expected 3 issues, got %d", len(merged))
		}
		expectedOrder := []string{"critical", "medium", "low"}
		for i, want := range expectedOrder {
			if merged[i].Severity != want {
				t.Errorf("merged[%d].Severity = %q, want %q", i, merged[i].Severity, want)
			}
		}
	})

	t.Run("nil line info does not prevent merging", func(t *testing.T) {
		issues := map[string][]ReviewIssue{
			"reviewer-1": {
				{
					Severity:    "high",
					File:        "api.go",
					Title:       "Missing authentication check in endpoint handler",
					Description: "The endpoint handler lacks proper authentication verification",
					Category:    "security",
				},
			},
			"reviewer-2": {
				{
					Severity:    "high",
					File:        "api.go",
					Title:       "Missing authentication check in endpoint",
					Description: "The endpoint handler lacks proper authentication verification steps",
					Category:    "security",
				},
			},
		}

		merged := DeduplicateIssues(issues)
		// Both have nil Line, so linesOverlap returns true; titles are similar
		if len(merged) != 1 {
			t.Fatalf("expected 1 merged issue (nil lines), got %d", len(merged))
		}
	})

	t.Run("one side missing line should not merge", func(t *testing.T) {
		issues := map[string][]ReviewIssue{
			"reviewer-1": {
				{
					Severity:    "high",
					File:        "api.go",
					Line:        intPtr(42),
					Title:       "Missing authorization check in admin endpoint",
					Description: "Admin endpoint executes sensitive action without role guard",
					Category:    "security",
				},
			},
			"reviewer-2": {
				{
					Severity:    "high",
					File:        "api.go",
					Title:       "Missing authorization check in admin endpoint",
					Description: "Admin endpoint executes sensitive action without role guard",
					Category:    "security",
				},
			},
		}

		merged := DeduplicateIssues(issues)
		if len(merged) != 2 {
			t.Fatalf("expected 2 issues (one side has no line), got %d", len(merged))
		}
	})

	t.Run("empty input returns empty", func(t *testing.T) {
		merged := DeduplicateIssues(map[string][]ReviewIssue{})
		if len(merged) != 0 {
			t.Errorf("expected 0 issues, got %d", len(merged))
		}
	})

	t.Run("suggestedFix preserved from second reviewer if first has none", func(t *testing.T) {
		issues := map[string][]ReviewIssue{
			"reviewer-1": {
				{
					Severity:    "high",
					File:        "db.go",
					Title:       "SQL injection vulnerability in database query",
					Description: "User input is concatenated directly into SQL query string",
					Category:    "security",
				},
			},
			"reviewer-2": {
				{
					Severity:     "high",
					File:         "db.go",
					Title:        "SQL injection vulnerability in database query builder",
					Description:  "User input is concatenated directly into SQL query strings",
					Category:     "security",
					SuggestedFix: "Use parameterized queries",
				},
			},
		}

		merged := DeduplicateIssues(issues)
		if len(merged) != 1 {
			t.Fatalf("expected 1 merged issue, got %d", len(merged))
		}
		if merged[0].SuggestedFix != "Use parameterized queries" {
			t.Errorf("suggestedFix = %q, want %q", merged[0].SuggestedFix, "Use parameterized queries")
		}
	})
}

// ---------------------------------------------------------------------------
// TestJaccardSimilarity
// ---------------------------------------------------------------------------

func TestJaccardSimilarity(t *testing.T) {
	t.Run("identical word sets return 1.0", func(t *testing.T) {
		words := []string{"hello", "world"}
		sim := jaccardSimilarity(words, words)
		if sim != 1.0 {
			t.Errorf("similarity = %f, want 1.0", sim)
		}
	})

	t.Run("completely different words return 0.0", func(t *testing.T) {
		a := []string{"hello", "world"}
		b := []string{"foo", "bar"}
		sim := jaccardSimilarity(a, b)
		if sim != 0.0 {
			t.Errorf("similarity = %f, want 0.0", sim)
		}
	})

	t.Run("partial overlap returns between 0 and 1", func(t *testing.T) {
		a := []string{"hello", "world", "foo"}
		b := []string{"hello", "world", "bar"}
		sim := jaccardSimilarity(a, b)
		// intersection={hello,world}=2, union={hello,world,foo,bar}=4 => 0.5
		if math.Abs(sim-0.5) > 0.001 {
			t.Errorf("similarity = %f, want ~0.5", sim)
		}
	})

	t.Run("both empty returns 0", func(t *testing.T) {
		sim := jaccardSimilarity(nil, nil)
		if sim != 0.0 {
			t.Errorf("similarity = %f, want 0.0", sim)
		}
	})

	t.Run("one empty returns 0", func(t *testing.T) {
		a := []string{"hello"}
		sim := jaccardSimilarity(a, nil)
		if sim != 0.0 {
			t.Errorf("similarity = %f, want 0.0", sim)
		}
	})

	t.Run("duplicate words in input handled correctly", func(t *testing.T) {
		a := []string{"hello", "hello", "world"}
		b := []string{"hello", "world"}
		sim := jaccardSimilarity(a, b)
		// Sets: {hello, world} vs {hello, world} => 1.0
		if sim != 1.0 {
			t.Errorf("similarity = %f, want 1.0", sim)
		}
	})
}

// ---------------------------------------------------------------------------
// TestFormatCallChainForReviewer
// ---------------------------------------------------------------------------

func TestFormatCallChainForReviewer(t *testing.T) {
	t.Run("empty references returns empty string", func(t *testing.T) {
		result := FormatCallChainForReviewer(nil)
		if result != "" {
			t.Errorf("expected empty string, got %q", result)
		}
	})

	t.Run("empty slice returns empty string", func(t *testing.T) {
		result := FormatCallChainForReviewer([]RawReference{})
		if result != "" {
			t.Errorf("expected empty string, got %q", result)
		}
	})

	t.Run("single reference formatted correctly", func(t *testing.T) {
		refs := []RawReference{
			{
				Symbol: "handleRequest",
				FoundInFiles: []ReferenceLocation{
					{File: "server.go", Line: 42, Content: "handleRequest(ctx, req)"},
					{File: "router.go", Line: 15, Content: "r.handleRequest(w, r)"},
				},
			},
		}

		result := FormatCallChainForReviewer(refs)
		if result == "" {
			t.Fatal("expected non-empty result")
		}

		// Check header
		if !contains(result, "## Call Chain Context") {
			t.Error("missing header '## Call Chain Context'")
		}
		// Check symbol
		if !contains(result, "`handleRequest`") {
			t.Error("missing symbol name")
		}
		// Check location count
		if !contains(result, "Found in 2 locations") {
			t.Error("missing location count")
		}
		// Check file references
		if !contains(result, "server.go:42") {
			t.Error("missing server.go reference")
		}
		if !contains(result, "router.go:15") {
			t.Error("missing router.go reference")
		}
	})

	t.Run("multiple references separated by divider", func(t *testing.T) {
		refs := []RawReference{
			{
				Symbol: "funcA",
				FoundInFiles: []ReferenceLocation{
					{File: "a.go", Line: 1, Content: "funcA()"},
				},
			},
			{
				Symbol: "funcB",
				FoundInFiles: []ReferenceLocation{
					{File: "b.go", Line: 2, Content: "funcB()"},
				},
			},
		}

		result := FormatCallChainForReviewer(refs)
		if !contains(result, "---") {
			t.Error("missing divider between sections")
		}
		if !contains(result, "`funcA`") {
			t.Error("missing funcA section")
		}
		if !contains(result, "`funcB`") {
			t.Error("missing funcB section")
		}
	})

	t.Run("more than 10 callers truncated to 10", func(t *testing.T) {
		var locations []ReferenceLocation
		for i := 0; i < 15; i++ {
			locations = append(locations, ReferenceLocation{
				File:    "file.go",
				Line:    i + 1,
				Content: "call()",
			})
		}
		refs := []RawReference{
			{Symbol: "call", FoundInFiles: locations},
		}

		result := FormatCallChainForReviewer(refs)
		// Should say "Found in 15 locations" (original count)
		if !contains(result, "Found in 15 locations") {
			t.Error("should show original count of 15 locations")
		}
		// But only list 10 entries (numbered 1-10)
		if !contains(result, "10.") {
			t.Error("should have entry #10")
		}
		if contains(result, "11.") {
			t.Error("should not have entry #11 (truncated)")
		}
	})

	t.Run("long content truncated to 150 chars", func(t *testing.T) {
		longContent := ""
		for i := 0; i < 200; i++ {
			longContent += "x"
		}
		refs := []RawReference{
			{
				Symbol: "fn",
				FoundInFiles: []ReferenceLocation{
					{File: "a.go", Line: 1, Content: longContent},
				},
			},
		}

		result := FormatCallChainForReviewer(refs)
		// The content in output should be truncated to 150 chars
		// The full 200-char string should not appear
		if contains(result, longContent) {
			t.Error("long content should be truncated")
		}
	})
}

// ---------------------------------------------------------------------------
// TestLinesOverlap (helper, unexported but tested indirectly and directly)
// ---------------------------------------------------------------------------

func TestLinesOverlap(t *testing.T) {
	intPtr := func(v int) *int { return &v }

	tests := []struct {
		name string
		a, b ReviewIssue
		want bool
	}{
		{
			name: "both nil lines overlap",
			a:    ReviewIssue{},
			b:    ReviewIssue{},
			want: true,
		},
		{
			name: "one nil line overlaps",
			a:    ReviewIssue{Line: intPtr(10)},
			b:    ReviewIssue{},
			want: false,
		},
		{
			name: "exact same line",
			a:    ReviewIssue{Line: intPtr(10)},
			b:    ReviewIssue{Line: intPtr(10)},
			want: true,
		},
		{
			name: "within 5 lines",
			a:    ReviewIssue{Line: intPtr(10)},
			b:    ReviewIssue{Line: intPtr(15)},
			want: true,
		},
		{
			name: "exactly 5 lines apart",
			a:    ReviewIssue{Line: intPtr(10)},
			b:    ReviewIssue{Line: intPtr(15)},
			want: true,
		},
		{
			name: "6 lines apart does not overlap",
			a:    ReviewIssue{Line: intPtr(10)},
			b:    ReviewIssue{Line: intPtr(16)},
			want: false,
		},
		{
			name: "ranges overlapping",
			a:    ReviewIssue{Line: intPtr(10), EndLine: intPtr(20)},
			b:    ReviewIssue{Line: intPtr(15), EndLine: intPtr(25)},
			want: true,
		},
		{
			name: "ranges far apart",
			a:    ReviewIssue{Line: intPtr(10), EndLine: intPtr(15)},
			b:    ReviewIssue{Line: intPtr(30), EndLine: intPtr(40)},
			want: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := linesOverlap(&tt.a, &tt.b)
			if got != tt.want {
				t.Errorf("linesOverlap() = %v, want %v", got, tt.want)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// TestFilterStopWords
// ---------------------------------------------------------------------------

func TestFilterStopWords(t *testing.T) {
	t.Run("removes stop words", func(t *testing.T) {
		words := []string{"the", "quick", "brown", "fox", "is", "a", "fast", "animal"}
		got := filterStopWords(words)
		expected := []string{"quick", "brown", "fox", "fast", "animal"}
		if len(got) != len(expected) {
			t.Fatalf("len = %d, want %d", len(got), len(expected))
		}
		for i := range expected {
			if got[i] != expected[i] {
				t.Errorf("got[%d] = %q, want %q", i, got[i], expected[i])
			}
		}
	})

	t.Run("empty input returns nil", func(t *testing.T) {
		got := filterStopWords(nil)
		if got != nil {
			t.Errorf("expected nil, got %v", got)
		}
	})
}

// ---------------------------------------------------------------------------
// TestTokenize
// ---------------------------------------------------------------------------

func TestTokenize(t *testing.T) {
	t.Run("strips punctuation and keeps alnum words", func(t *testing.T) {
		got := tokenize("Potential vulnerability. Please fix SQL-injection, now!")
		want := []string{"Potential", "vulnerability", "Please", "fix", "SQL", "injection", "now"}
		if len(got) != len(want) {
			t.Fatalf("len = %d, want %d, got=%v", len(got), len(want), got)
		}
		for i := range want {
			if got[i] != want[i] {
				t.Fatalf("got[%d] = %q, want %q", i, got[i], want[i])
			}
		}
	})
}

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

func contains(s, substr string) bool {
	return len(s) >= len(substr) && containsSubstring(s, substr)
}

func containsSubstring(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}
