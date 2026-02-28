package display

import (
	"io"
	"log/slog"
	"testing"

	"github.com/longyunfeigu/codedebate/internal/orchestrator"
)

func TestNoopDisplayCallbacks(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	d := NewNoopDisplay(logger)

	// 验证所有回调方法都不 panic
	t.Run("OnWaiting", func(t *testing.T) {
		d.OnWaiting("test-reviewer")
	})

	t.Run("OnMessage", func(t *testing.T) {
		d.OnMessage("test-reviewer", "short message")
	})

	t.Run("OnMessageChunk", func(t *testing.T) {
		d.OnMessageChunk("test-reviewer", "chunk")
	})

	t.Run("OnMessage long content", func(t *testing.T) {
		longMsg := ""
		for i := 0; i < 300; i++ {
			longMsg += "x"
		}
		d.OnMessage("test-reviewer", longMsg)
	})

	t.Run("OnParallelStatus", func(t *testing.T) {
		d.OnParallelStatus(1, []orchestrator.ReviewerStatus{
			{ReviewerID: "r1", Status: "thinking"},
			{ReviewerID: "r2", Status: "done"},
		})
	})

	t.Run("OnSummaryStatus", func(t *testing.T) {
		d.OnSummaryStatus([]orchestrator.ReviewerStatus{
			{ReviewerID: "r1", Status: "thinking"},
			{ReviewerID: "r2", Status: "done"},
		})
	})

	t.Run("OnRoundComplete", func(t *testing.T) {
		d.OnRoundComplete(1, false)
		d.OnRoundComplete(2, true)
	})

	t.Run("OnConvergenceJudgment", func(t *testing.T) {
		d.OnConvergenceJudgment("converged", "all reviewers agree")
	})

	t.Run("OnContextGathered nil", func(t *testing.T) {
		d.OnContextGathered(nil)
	})

	t.Run("OnContextGathered with data", func(t *testing.T) {
		d.OnContextGathered(&orchestrator.GatheredContext{
			AffectedModules: []orchestrator.AffectedModule{{Name: "mod1"}},
			RelatedPRs:      []orchestrator.RelatedPR{{Number: 1, Title: "PR 1"}},
		})
	})
}

// TestNoopDisplayImplementsInterface 验证 NoopDisplay 实现了 DisplayCallbacks 接口。
func TestNoopDisplayImplementsInterface(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	var _ orchestrator.DisplayCallbacks = NewNoopDisplay(logger)
}
