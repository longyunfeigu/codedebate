package display

import (
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/longyunfeigu/codedebate/internal/orchestrator"
)

// NoopDisplay 实现 orchestrator.DisplayCallbacks 接口。
// 用于 Server 模式，通过 logger 记录关键事件，不输出终端 UI。
type NoopDisplay struct {
	Logger *slog.Logger
}

// NewNoopDisplay 创建一个 NoopDisplay 实例。
func NewNoopDisplay(logger *slog.Logger) *NoopDisplay {
	return &NoopDisplay{Logger: logger}
}

func (d *NoopDisplay) OnWaiting(reviewerID string) {
	phase, step := classifyPhase(reviewerID)
	d.Logger.Info("phase waiting", "phase", phase, "step", step, "reviewer", reviewerID)
}

func (d *NoopDisplay) OnMessageChunk(reviewerID string, chunk string) {
	truncated := truncateForLog(chunk, 160)
	if strings.TrimSpace(truncated) == "" {
		return
	}
	d.Logger.Debug("message chunk", "reviewer", reviewerID, "chunk", truncated)
}

func (d *NoopDisplay) OnMessage(reviewerID string, content string) {
	truncated := truncateForLog(content, 200)
	d.Logger.Debug("message", "reviewer", reviewerID, "content", truncated)
}

func (d *NoopDisplay) OnParallelStatus(round int, statuses []orchestrator.ReviewerStatus) {
	parts := make([]string, 0, len(statuses))
	nowMillis := time.Now().UnixMilli()
	for _, s := range statuses {
		switch s.Status {
		case "done":
			parts = append(parts, fmt.Sprintf("%s:done(%.1fs,in=%d,out=%d,cost=%.4f)",
				s.ReviewerID, s.Duration, s.InputTokens, s.OutputTokens, s.EstimatedCost))
		case "thinking":
			elapsed := 0.0
			if s.StartTime > 0 {
				elapsed = float64(nowMillis-s.StartTime) / 1000.0
			}
			parts = append(parts, fmt.Sprintf("%s:thinking(%.1fs,in=%d)",
				s.ReviewerID, elapsed, s.InputTokens))
		default:
			parts = append(parts, fmt.Sprintf("%s:%s", s.ReviewerID, s.Status))
		}
	}
	d.Logger.Info("parallel status", "round", round, "reviewers", len(statuses), "status_detail", strings.Join(parts, " | "))
}

func (d *NoopDisplay) OnSummaryStatus(statuses []orchestrator.ReviewerStatus) {
	parts := make([]string, 0, len(statuses))
	nowMillis := time.Now().UnixMilli()
	for _, s := range statuses {
		switch s.Status {
		case "done":
			parts = append(parts, fmt.Sprintf("%s:done(%.1fs,in=%d,out=%d,cost=%.4f)",
				s.ReviewerID, s.Duration, s.InputTokens, s.OutputTokens, s.EstimatedCost))
		case "thinking":
			elapsed := 0.0
			if s.StartTime > 0 {
				elapsed = float64(nowMillis-s.StartTime) / 1000.0
			}
			parts = append(parts, fmt.Sprintf("%s:thinking(%.1fs,in=%d)",
				s.ReviewerID, elapsed, s.InputTokens))
		default:
			parts = append(parts, fmt.Sprintf("%s:%s", s.ReviewerID, s.Status))
		}
	}
	d.Logger.Info("summary status", "reviewers", len(statuses), "status_detail", strings.Join(parts, " | "))
}

func (d *NoopDisplay) OnRoundComplete(round int, converged bool) {
	d.Logger.Info("round complete", "round", round, "converged", converged)
}

func (d *NoopDisplay) OnConvergenceJudgment(verdict string, reasoning string) {
	d.Logger.Info("convergence judgment", "verdict", verdict)
}

func (d *NoopDisplay) OnContextGathered(ctx *orchestrator.GatheredContext) {
	modules := 0
	prs := 0
	if ctx != nil {
		modules = len(ctx.AffectedModules)
		prs = len(ctx.RelatedPRs)
	}
	d.Logger.Info("context gathered", "modules", modules, "related_prs", prs)
}

func truncateForLog(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen] + "..."
}

func classifyPhase(reviewerID string) (phase string, step string) {
	switch {
	case reviewerID == "context-gatherer":
		return "phase_1", "system_context"
	case reviewerID == "analyzer":
		return "phase_1", "analyzer"
	case strings.HasPrefix(reviewerID, "round-"):
		return "phase_2", reviewerID
	case reviewerID == "convergence-check":
		return "phase_2", "convergence_check"
	case reviewerID == "reviewer-summaries":
		return "phase_3", "reviewer_summaries"
	case reviewerID == "final-conclusion" || reviewerID == "summarizer":
		return "phase_3", "final_conclusion"
	case reviewerID == "structurizer":
		return "phase_3", "structurizer"
	default:
		return "phase_unknown", reviewerID
	}
}
