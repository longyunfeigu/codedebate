package orchestrator

import (
	"context"
	"fmt"
	"regexp"
	"strings"
	"sync"
	"testing"

	"github.com/longyunfeigu/codedebate/internal/provider"
)

type deltaRoutingProvider struct {
	mu        sync.Mutex
	responses map[string]string
	calls     int
}

func (p *deltaRoutingProvider) Name() string { return "delta-routing" }

func (p *deltaRoutingProvider) Chat(_ context.Context, msgs []provider.Message, _ string, _ *provider.ChatOptions) (string, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.calls++

	prompt := ""
	if len(msgs) > 0 {
		prompt = msgs[0].Content
	}
	key := deltaKeyFromPrompt(prompt)
	resp, ok := p.responses[key]
	if !ok {
		resp = p.responses["default"]
	}
	if resp == "__ERR__" {
		return "", fmt.Errorf("forced error for key %s", key)
	}
	return resp, nil
}

func (p *deltaRoutingProvider) ChatStream(_ context.Context, _ []provider.Message, _ string) (<-chan string, <-chan error) {
	chunks := make(chan string, 1)
	errs := make(chan error, 1)
	close(chunks)
	close(errs)
	return chunks, errs
}

func (p *deltaRoutingProvider) StartSession(_ string) error { return nil }
func (p *deltaRoutingProvider) EndSession() error           { return nil }
func (p *deltaRoutingProvider) HasSession() bool            { return false }
func (p *deltaRoutingProvider) ShouldSendFullHistory() bool { return true }
func (p *deltaRoutingProvider) LastSeenIndex() int          { return 0 }
func (p *deltaRoutingProvider) SetLastSeenIndex(_ int)      {}

var (
	reviewerRe = regexp.MustCompile(`reviewer "([^"]+)"`)
	roundRe    = regexp.MustCompile(`round (\d+)`)
)

func deltaKeyFromPrompt(prompt string) string {
	reviewer := ""
	if m := reviewerRe.FindStringSubmatch(prompt); len(m) > 1 {
		reviewer = strings.TrimSpace(m[1])
	}
	round := ""
	if m := roundRe.FindStringSubmatch(strings.ToLower(prompt)); len(m) > 1 {
		round = m[1]
	}
	if reviewer != "" && round != "" {
		return reviewer + ":" + round
	}
	if reviewer != "" {
		return reviewer
	}
	return "default"
}

func TestIncrementalStructurize_TwoRounds(t *testing.T) {
	provider := &deltaRoutingProvider{responses: map[string]string{
		"r1:1": "```json\n" + `{"add":[{"severity":"high","file":"a.go","title":"Issue A","description":"desc"}],"retract":[],"update":[],"support":[],"withdraw":[],"contest":[]}` + "\n```",
		"r2:1": "```json\n" + `{"add":[{"severity":"medium","file":"b.go","title":"Issue B","description":"desc"}],"retract":[],"update":[],"support":[],"withdraw":[],"contest":[]}` + "\n```",
		"r1:2": "```json\n" + `{"add":[],"retract":["I1"],"update":[],"support":[],"withdraw":[],"contest":[]}` + "\n```",
		"r2:2": "```json\n" + `{"add":[],"retract":[],"update":[{"id":"I1","severity":"high","description":"upgraded"}],"support":[],"withdraw":[],"contest":[]}` + "\n```",
	}}

	o := &DebateOrchestrator{
		reviewers: []Reviewer{{ID: "r1"}, {ID: "r2"}},
		summarizer: Reviewer{
			ID:       "summarizer",
			Provider: provider,
		},
		options:    OrchestratorOptions{StructurizeMode: "ledger"},
		tokenUsage: make(map[string]*tokenCount),
	}
	o.initIssueLedgers()

	o.extractRoundIssueDeltas(context.Background(), 1, map[string]string{
		"r1": "round1",
		"r2": "round1",
	}, &noopDisplay{})
	o.extractRoundIssueDeltas(context.Background(), 2, map[string]string{
		"r1": "round2",
		"r2": "round2",
	}, &noopDisplay{})

	issues := DeduplicateMergedIssues(o.mergeAllLedgers())
	if len(issues) != 1 {
		t.Fatalf("expected 1 issue after retract/update, got %d", len(issues))
	}
	if issues[0].Title != "Issue B" {
		t.Fatalf("unexpected title: %q", issues[0].Title)
	}
	if issues[0].Severity != "high" {
		t.Fatalf("expected updated severity high, got %q", issues[0].Severity)
	}
}

func TestIncrementalStructurize_ParseFailure(t *testing.T) {
	provider := &deltaRoutingProvider{responses: map[string]string{
		"r1:1": "not json",
		"r2:1": "```json\n" + `{"add":[{"severity":"medium","file":"b.go","title":"Issue B","description":"desc"}],"retract":[],"update":[],"support":[],"withdraw":[],"contest":[]}` + "\n```",
	}}

	o := &DebateOrchestrator{
		reviewers: []Reviewer{{ID: "r1"}, {ID: "r2"}},
		summarizer: Reviewer{
			ID:       "summarizer",
			Provider: provider,
		},
		options:    OrchestratorOptions{StructurizeMode: "ledger"},
		tokenUsage: make(map[string]*tokenCount),
	}
	o.initIssueLedgers()
	o.extractRoundIssueDeltas(context.Background(), 1, map[string]string{
		"r1": "round1",
		"r2": "round1",
	}, &noopDisplay{})

	issues := DeduplicateMergedIssues(o.mergeAllLedgers())
	if len(issues) != 1 {
		t.Fatalf("expected 1 issue despite one reviewer parse failure, got %d", len(issues))
	}
	if len(issues[0].RaisedBy) != 1 || issues[0].RaisedBy[0] != "r2" {
		t.Fatalf("unexpected raisedBy: %v", issues[0].RaisedBy)
	}
}

func TestIncrementalStructurize_CanonicalSupportAction(t *testing.T) {
	provider := &deltaRoutingProvider{responses: map[string]string{
		"r1:1": "```json\n" + `{"add":[{"severity":"high","file":"auth.go","title":"Missing authorization check","description":"Admin endpoint executes sensitive action without a role guard"}],"retract":[],"update":[],"support":[],"withdraw":[],"contest":[]}` + "\n```",
		"r2:1": "```json\n" + `{"add":[],"retract":[],"update":[],"support":[],"withdraw":[],"contest":[]}` + "\n```",
		"r1:2": "```json\n" + `{"add":[],"retract":[],"update":[],"support":[],"withdraw":[],"contest":[]}` + "\n```",
		"r2:2": "```json\n" + `{"add":[],"retract":[],"update":[],"support":[{"issueRef":"r1:I1"}],"withdraw":[],"contest":[]}` + "\n```",
	}}

	o := &DebateOrchestrator{
		reviewers: []Reviewer{{ID: "r1"}, {ID: "r2"}},
		summarizer: Reviewer{
			ID:       "summarizer",
			Provider: provider,
		},
		options:    OrchestratorOptions{StructurizeMode: "ledger"},
		tokenUsage: make(map[string]*tokenCount),
	}
	o.initIssueLedgers()

	o.extractRoundIssueDeltas(context.Background(), 1, map[string]string{
		"r1": "round1",
		"r2": "round1",
	}, &noopDisplay{})
	o.extractRoundIssueDeltas(context.Background(), 2, map[string]string{
		"r1": "round2",
		"r2": "round2",
	}, &noopDisplay{})

	issues := o.structurizeIssuesFromLedgers(context.Background(), &noopDisplay{})
	if len(issues) != 1 {
		t.Fatalf("expected 1 canonical issue, got %d", len(issues))
	}
	if len(issues[0].SupportedBy) != 2 {
		t.Fatalf("expected support from both reviewers, got %v", issues[0].SupportedBy)
	}
	if len(issues[0].RaisedBy) != 2 {
		t.Fatalf("expected RaisedBy to mirror supporters, got %v", issues[0].RaisedBy)
	}
}

func TestIncrementalStructurize_FallbackToLegacy(t *testing.T) {
	provider := &deltaRoutingProvider{responses: map[string]string{
		"default": "```json\n" + `{"issues":[{"severity":"high","file":"x.go","title":"Legacy","description":"legacy desc","raisedBy":["summarizer"]}]}` + "\n```",
	}}

	o := &DebateOrchestrator{
		summarizer: Reviewer{
			ID:       "summarizer",
			Provider: provider,
		},
		conversationHistory: []DebateMessage{{ReviewerID: "r1", Content: "review content"}},
		tokenUsage:          make(map[string]*tokenCount),
		issueLedgers:        map[string]*IssueLedger{"r1": NewIssueLedger("r1")},
	}

	issues := o.structurizeIssuesFromLedgers(context.Background(), &noopDisplay{})
	if len(issues) != 1 {
		t.Fatalf("expected fallback legacy issue, got %d", len(issues))
	}
	if issues[0].Title != "Legacy" {
		t.Fatalf("unexpected title from fallback: %q", issues[0].Title)
	}
}
