package platform

import (
	"strings"
	"testing"
)

func TestFormatIssueBody_ContainsStructuredMeta(t *testing.T) {
	line := 42
	issue := IssueForComment{
		File:        "main.go",
		Line:        &line,
		Title:       "SQL injection risk",
		Description: "User input not sanitized",
		Severity:    "high",
	}

	body := FormatIssueBodyWithMeta(issue, "run-1", "head-1")
	meta, ok := ParseCodeDebateMeta(body)
	if !ok {
		t.Fatal("expected structured codedebate meta")
	}
	if meta.IssueKey == "" {
		t.Fatal("expected non-empty issue key")
	}
	if meta.Status != "active" {
		t.Fatalf("expected active status, got %q", meta.Status)
	}
	if !strings.Contains(body, "SQL injection risk") {
		t.Fatal("expected title in formatted body")
	}
}

func TestIsDuplicateComment_StripsCodeDebateMetaBeforeBodyMatch(t *testing.T) {
	line := 10
	issue := IssueForComment{
		File:        "main.go",
		Line:        &line,
		Title:       "Missing guard",
		Description: "The nil guard was removed",
		Severity:    "high",
	}

	newBody := FormatIssueBodyWithMeta(issue, "run-1", "head-1")
	legacyBody := BuildIssueMarker(issue.File, issue.Line, issue.Severity, issue.Title) + "\n" + FormatIssueDisplayBody(issue)

	if !IsDuplicateComment(
		ReviewCommentInput{Path: "main.go", Line: &line, Body: newBody},
		[]ExistingComment{{Path: "main.go", Line: &line, Body: legacyBody}},
	) {
		t.Fatal("expected duplicate via body match after stripping codedebate markers")
	}
}

func TestBuildIssueKey_IncludesLineNumber(t *testing.T) {
	line10 := 10
	line20 := 20

	issue10 := IssueForComment{
		File:        "main.go",
		Line:        &line10,
		Title:       "Missing guard",
		Description: "The nil guard was removed",
		Severity:    "high",
	}
	issue20 := IssueForComment{
		File:        "main.go",
		Line:        &line20,
		Title:       "Missing guard",
		Description: "The nil guard was removed",
		Severity:    "high",
	}

	if BuildIssueKey(issue10) == BuildIssueKey(issue20) {
		t.Fatal("expected different issue keys for different lines")
	}
}

func TestPlanLifecycle(t *testing.T) {
	line10 := 10
	line20 := 20

	existing := []ExistingComment{
		{
			ID:      "1",
			Path:    "a.go",
			Line:    &line10,
			Source:  "inline",
			IsCodeDebate: true,
			Meta: &CodeDebateCommentMeta{
				IssueKey:   "same-noop",
				Status:     "active",
				BodyHash:   "body-a",
				AnchorHash: "anchor-a",
			},
		},
		{
			ID:      "2",
			Path:    "b.go",
			Line:    &line10,
			Source:  "inline",
			IsCodeDebate: true,
			Meta: &CodeDebateCommentMeta{
				IssueKey:   "same-update",
				Status:     "active",
				BodyHash:   "body-old",
				AnchorHash: "anchor-b",
			},
		},
		{
			ID:      "3",
			Path:    "c.go",
			Line:    &line10,
			Source:  "inline",
			IsCodeDebate: true,
			Meta: &CodeDebateCommentMeta{
				IssueKey:   "same-moved",
				Status:     "active",
				BodyHash:   "body-c",
				AnchorHash: "anchor-old",
			},
		},
		{
			ID:      "4",
			Path:    "d.go",
			Line:    &line10,
			Source:  "inline",
			IsCodeDebate: true,
			Meta: &CodeDebateCommentMeta{
				IssueKey:   "to-resolve",
				Status:     "active",
				BodyHash:   "body-d",
				AnchorHash: "anchor-d",
			},
		},
	}

	desired := []DesiredComment{
		{IssueKey: "same-noop", Path: "a.go", Line: &line10, Source: "inline", BodyHash: "body-a", AnchorHash: "anchor-a"},
		{IssueKey: "same-update", Path: "b.go", Line: &line10, Source: "inline", BodyHash: "body-new", AnchorHash: "anchor-b"},
		{IssueKey: "same-moved", Path: "c.go", Line: &line20, Source: "inline", BodyHash: "body-c", AnchorHash: "anchor-new"},
		{IssueKey: "brand-new", Path: "e.go", Line: &line10, Source: "inline", BodyHash: "body-e", AnchorHash: "anchor-e"},
	}

	plan := PlanLifecycle(existing, desired)

	if len(plan.Noop) != 1 {
		t.Fatalf("expected 1 noop, got %d", len(plan.Noop))
	}
	if len(plan.Update) != 1 {
		t.Fatalf("expected 1 update, got %d", len(plan.Update))
	}
	if len(plan.Supersede) != 1 {
		t.Fatalf("expected 1 supersede, got %d", len(plan.Supersede))
	}
	if len(plan.Resolve) != 1 {
		t.Fatalf("expected 1 resolve, got %d", len(plan.Resolve))
	}
	if len(plan.Create) != 2 {
		t.Fatalf("expected 2 creates (moved + new), got %d", len(plan.Create))
	}
}

func TestPlanLifecycle_DifferentLinesStayDistinct(t *testing.T) {
	line10 := 10
	line20 := 20

	issue10 := IssueForComment{
		File:        "main.go",
		Line:        &line10,
		Title:       "Missing guard",
		Description: "The nil guard was removed",
		Severity:    "high",
	}
	issue20 := IssueForComment{
		File:        "main.go",
		Line:        &line20,
		Title:       "Missing guard",
		Description: "The nil guard was removed",
		Severity:    "high",
	}

	body10 := FormatIssueBodyWithMeta(issue10, "run-1", "head-1")
	meta10, ok := ParseCodeDebateMeta(body10)
	if !ok {
		t.Fatal("expected meta for existing line-10 comment")
	}
	body20 := FormatIssueBodyWithMeta(issue20, "run-2", "head-2")

	existing := []ExistingComment{
		{
			ID:      "1",
			Path:    "main.go",
			Line:    &line10,
			Body:    body10,
			Source:  "inline",
			IsCodeDebate: true,
			Meta:    meta10,
		},
	}

	desired := BuildDesiredComments([]ClassifiedComment{{
		Input: ReviewCommentInput{
			Path: "main.go",
			Line: &line20,
			Body: body20,
		},
		Mode: "inline",
	}}, "run-2", "head-2")

	plan := PlanLifecycle(existing, desired)
	if len(plan.Noop) != 0 {
		t.Fatalf("expected 0 noop comments, got %d", len(plan.Noop))
	}
	if len(plan.Update) != 0 {
		t.Fatalf("expected 0 updated comments, got %d", len(plan.Update))
	}
	if len(plan.Supersede) != 0 {
		t.Fatalf("expected 0 superseded comments, got %d", len(plan.Supersede))
	}
	if len(plan.Resolve) != 1 {
		t.Fatalf("expected line-10 comment to resolve, got %d resolves", len(plan.Resolve))
	}
	if len(plan.Create) != 1 {
		t.Fatalf("expected line-20 comment to create, got %d creates", len(plan.Create))
	}
}

func TestRenderResolvedBody_UpdatesStatus(t *testing.T) {
	line := 12
	issue := IssueForComment{
		File:        "main.go",
		Line:        &line,
		Title:       "Missing guard",
		Description: "The nil guard was removed",
		Severity:    "high",
	}
	body := FormatIssueBodyWithMeta(issue, "run-1", "head-1")
	meta, _ := ParseCodeDebateMeta(body)

	rendered := RenderResolvedBody(ExistingComment{
		Path:    "main.go",
		Line:    &line,
		Body:    body,
		Source:  "inline",
		IsCodeDebate: true,
		Meta:    meta,
	}, "run-2", "head-2")

	updatedMeta, ok := ParseCodeDebateMeta(rendered)
	if !ok {
		t.Fatal("expected structured meta in resolved body")
	}
	if updatedMeta.Status != "resolved" {
		t.Fatalf("expected resolved status, got %q", updatedMeta.Status)
	}
	if !strings.Contains(rendered, "not reproduced in the latest review run") {
		t.Fatal("expected resolved explanation in body")
	}
}
