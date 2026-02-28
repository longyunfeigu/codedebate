package context

import (
	stdctx "context"
	"strings"
	"testing"
	"time"

	"github.com/longyunfeigu/codedebate/internal/provider"
)

type cancelAwareProvider struct{}

func (p *cancelAwareProvider) Name() string { return "cancel-aware" }

func (p *cancelAwareProvider) Chat(ctx stdctx.Context, _ []provider.Message, _ string, _ *provider.ChatOptions) (string, error) {
	select {
	case <-ctx.Done():
		return "", ctx.Err()
	case <-time.After(2 * time.Second):
		return "unexpected success", nil
	}
}

func (p *cancelAwareProvider) ChatStream(_ stdctx.Context, _ []provider.Message, _ string) (<-chan string, <-chan error) {
	ch := make(chan string)
	errCh := make(chan error, 1)
	close(ch)
	close(errCh)
	return ch, errCh
}

func TestGather_UsesCallerContextForAIChat(t *testing.T) {
	t.Parallel()

	gatherer := NewContextGatherer(&cancelAwareProvider{}, nil, nil)
	gatherer.SetCwd(t.TempDir())

	ctx, cancel := stdctx.WithCancel(stdctx.Background())
	cancel()

	result, err := gatherer.Gather(ctx, "diff --git a/a.go b/a.go\n", "mr-1", "main")
	if err != nil {
		t.Fatalf("Gather() returned unexpected error: %v", err)
	}
	if result == nil {
		t.Fatal("Gather() returned nil result")
	}
	if !strings.Contains(strings.ToLower(result.Summary), "context canceled") {
		t.Fatalf("expected fallback summary to include context cancellation, got: %q", result.Summary)
	}
}
