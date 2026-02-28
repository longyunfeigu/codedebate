package provider

import "testing"

func TestClaudeHandleStreamEvent_StreamsAssistantTextIncrementally(t *testing.T) {
	p := NewClaudeCodeProvider()
	state := &claudeStreamState{seenToolUseIDs: make(map[string]struct{})}
	chunks := make(chan string, 8)

	p.handleStreamEvent(`{"type":"assistant","message":{"content":[{"type":"text","text":"Look"}]}}`, state, chunks)
	p.handleStreamEvent(`{"type":"assistant","message":{"content":[{"type":"text","text":"Looking deeper"}]}}`, state, chunks)
	close(chunks)

	var got []string
	for chunk := range chunks {
		got = append(got, chunk)
	}

	if len(got) != 2 {
		t.Fatalf("expected 2 chunks, got %d: %#v", len(got), got)
	}
	if got[0] != "Look" {
		t.Fatalf("expected first chunk %q, got %q", "Look", got[0])
	}
	if got[1] != "ing deeper" {
		t.Fatalf("expected second chunk %q, got %q", "ing deeper", got[1])
	}
}

func TestClaudeHandleStreamEvent_EmitsToolUseProgress(t *testing.T) {
	p := NewClaudeCodeProvider()
	state := &claudeStreamState{seenToolUseIDs: make(map[string]struct{})}
	chunks := make(chan string, 8)

	p.handleStreamEvent(`{"type":"assistant","message":{"content":[{"type":"tool_use","id":"toolu_1","name":"Bash","input":{"command":"rg -n \"ChatStream\" internal"}}]}}`, state, chunks)
	p.handleStreamEvent(`{"type":"assistant","message":{"content":[{"type":"tool_use","id":"toolu_1","name":"Bash","input":{"command":"rg -n \"ChatStream\" internal"}}]}}`, state, chunks)
	close(chunks)

	var got []string
	for chunk := range chunks {
		got = append(got, chunk)
	}

	if len(got) != 1 {
		t.Fatalf("expected 1 tool chunk, got %d: %#v", len(got), got)
	}
	want := "\n[tool] Bash: rg -n \"ChatStream\" internal\n"
	if got[0] != want {
		t.Fatalf("expected %q, got %q", want, got[0])
	}
}

func TestClaudeHandleStreamEvent_UsesResultAsFallback(t *testing.T) {
	p := NewClaudeCodeProvider()
	state := &claudeStreamState{seenToolUseIDs: make(map[string]struct{})}
	chunks := make(chan string, 8)

	p.handleStreamEvent(`{"type":"result","subtype":"success","result":"Final answer"}`, state, chunks)
	close(chunks)

	var got []string
	for chunk := range chunks {
		got = append(got, chunk)
	}

	if len(got) != 1 {
		t.Fatalf("expected 1 fallback chunk, got %d: %#v", len(got), got)
	}
	if got[0] != "Final answer" {
		t.Fatalf("expected fallback result %q, got %q", "Final answer", got[0])
	}
}
