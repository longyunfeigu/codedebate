package provider

import "testing"

func TestCodexCliProvider_ParseJsonlOutput_ItemCompleted(t *testing.T) {
	p := NewCodexCliProvider()
	p.sessionEnabled = true

	output := `{"type":"thread.started","thread_id":"thread_123"}
{"type":"item.completed","item":{"type":"agent_message","text":"CodeDebate review output"}}`

	got := p.parseJsonlOutput(output)
	if got != "CodeDebate review output" {
		t.Fatalf("parseJsonlOutput() = %q, want %q", got, "CodeDebate review output")
	}
	if p.SessionID() != "thread_123" {
		t.Fatalf("SessionID() = %q, want %q", p.SessionID(), "thread_123")
	}
}

func TestCodexCliProvider_HandleCodexEvent_AgentMessageDeltaThenCompleted_NoDuplicate(t *testing.T) {
	p := NewCodexCliProvider()
	state := &codexStreamState{}

	first := p.handleCodexEvent(`{"type":"agent_message_delta","delta":"Hello"}`, state, true)
	if len(first) != 1 || first[0] != "Hello" {
		t.Fatalf("first chunks = %#v, want %#v", first, []string{"Hello"})
	}

	second := p.handleCodexEvent(`{"type":"item.completed","item":{"type":"agent_message","text":"Hello"}}`, state, true)
	if len(second) != 0 {
		t.Fatalf("second chunks = %#v, want no chunks", second)
	}
}

func TestCodexCliProvider_HandleCodexEvent_ExecCommandBegin_FormatsToolTrace(t *testing.T) {
	p := NewCodexCliProvider()
	state := &codexStreamState{}

	chunks := p.handleCodexEvent(`{"type":"exec_command_begin","parsed_cmd":["rg","-n","CodeDebate","README.md"]}`, state, true)
	if len(chunks) != 1 {
		t.Fatalf("chunks len = %d, want 1", len(chunks))
	}
	want := "\n[tool] Exec: rg -n CodeDebate README.md\n"
	if chunks[0] != want {
		t.Fatalf("chunks[0] = %q, want %q", chunks[0], want)
	}
}

func TestCodexCliProvider_HandleCodexEvent_TerminalInteraction_FormatsPrompt(t *testing.T) {
	p := NewCodexCliProvider()
	state := &codexStreamState{}

	chunks := p.handleCodexEvent(`{"type":"terminal_interaction","stdin":"Do you trust this directory?"}`, state, true)
	if len(chunks) != 1 {
		t.Fatalf("chunks len = %d, want 1", len(chunks))
	}
	want := "\n[input] Do you trust this directory?\n"
	if chunks[0] != want {
		t.Fatalf("chunks[0] = %q, want %q", chunks[0], want)
	}
}
