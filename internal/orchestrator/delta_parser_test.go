package orchestrator

import "testing"

func TestParseStructurizeDelta(t *testing.T) {
	resp := "```json\n" + `{
  "add": [{"severity":"high","file":"a.go","line":10,"title":"A","description":"desc"}],
  "retract": ["I1"],
  "update": [{"id":"I2","severity":"medium","description":"updated"}],
  "support": [{"issueRef":"claude:I3"}],
  "withdraw": [{"issueRef":"gpt4o:I4"}],
  "contest": [{"issueRef":"codex:I5"}]
}` + "\n```"

	parsed := ParseStructurizeDelta(resp)
	if parsed.ParseError != nil {
		t.Fatalf("unexpected parse error: %v", parsed.ParseError)
	}
	if parsed.Output == nil {
		t.Fatal("expected non-nil output")
	}
	if len(parsed.Output.Add) != 1 {
		t.Fatalf("expected 1 add, got %d", len(parsed.Output.Add))
	}
	if parsed.Output.Add[0].Line == nil || *parsed.Output.Add[0].Line != 10 {
		t.Fatalf("expected line=10, got %v", parsed.Output.Add[0].Line)
	}
	if len(parsed.Output.Retract) != 1 || parsed.Output.Retract[0] != "I1" {
		t.Fatalf("unexpected retract: %v", parsed.Output.Retract)
	}
	if len(parsed.Output.Update) != 1 || parsed.Output.Update[0].ID != "I2" {
		t.Fatalf("unexpected update: %+v", parsed.Output.Update)
	}
	if parsed.Output.Update[0].Severity == nil || *parsed.Output.Update[0].Severity != "medium" {
		t.Fatalf("unexpected update severity: %+v", parsed.Output.Update[0].Severity)
	}
	if len(parsed.Output.Support) != 1 || parsed.Output.Support[0].IssueRef != "claude:I3" {
		t.Fatalf("unexpected support: %+v", parsed.Output.Support)
	}
	if len(parsed.Output.Withdraw) != 1 || parsed.Output.Withdraw[0].IssueRef != "gpt4o:I4" {
		t.Fatalf("unexpected withdraw: %+v", parsed.Output.Withdraw)
	}
	if len(parsed.Output.Contest) != 1 || parsed.Output.Contest[0].IssueRef != "codex:I5" {
		t.Fatalf("unexpected contest: %+v", parsed.Output.Contest)
	}
}

func TestParseStructurizeDelta_Invalid(t *testing.T) {
	resp := "```json\n" + `{"add":[{"file":"a.go","title":"A","description":"desc"}],"retract":[],"update":[],"support":[],"withdraw":[],"contest":[]}` + "\n```"
	parsed := ParseStructurizeDelta(resp)
	if parsed.Output == nil {
		t.Fatal("expected output even when schema invalid (best-effort parse)")
	}
	if len(parsed.Output.Add) != 0 {
		t.Fatalf("expected invalid add to be filtered, got %d", len(parsed.Output.Add))
	}
	if len(parsed.SchemaErrors) == 0 {
		t.Fatal("expected schema errors")
	}
}

func TestParseStructurizeDelta_NoJSON(t *testing.T) {
	parsed := ParseStructurizeDelta("no json here")
	if parsed.ParseError == nil {
		t.Fatal("expected parse error")
	}
}

func TestParseStructurizeDelta_MissingCanonicalKeys(t *testing.T) {
	resp := "```json\n" + `{"add":[],"retract":[],"update":[]}` + "\n```"
	parsed := ParseStructurizeDelta(resp)
	if parsed.ParseError == nil {
		t.Fatal("expected parse error for missing support/withdraw/contest")
	}
}
