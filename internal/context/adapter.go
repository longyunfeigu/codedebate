package context

import (
	"context"

	"github.com/longyunfeigu/codedebate/internal/config"
	"github.com/longyunfeigu/codedebate/internal/orchestrator"
	"github.com/longyunfeigu/codedebate/internal/platform"
	"github.com/longyunfeigu/codedebate/internal/provider"
)

// ContextGathererAdapter 使用适配器模式包装 ContextGatherer，
// 使其实现 orchestrator.ContextGathererInterface 接口。
type ContextGathererAdapter struct {
	inner *ContextGatherer
}

// NewContextGathererAdapter 创建一个满足 orchestrator.ContextGathererInterface 接口的适配器。
// historyProvider 可为 nil（当平台检测失败或不在 PR 模式时）。
func NewContextGathererAdapter(p provider.AIProvider, cfg *config.ContextGathererConfig, historyProvider platform.HistoryProvider) *ContextGathererAdapter {
	return &ContextGathererAdapter{
		inner: NewContextGatherer(p, cfg, historyProvider),
	}
}

// Gather 实现 orchestrator.ContextGathererInterface 接口。
func (a *ContextGathererAdapter) Gather(ctx context.Context, diff, prNumber, baseBranch string) (*orchestrator.GatheredContext, error) {
	gc, err := a.inner.Gather(ctx, diff, prNumber, baseBranch)
	if err != nil {
		return nil, err
	}
	return convertToOrchestrator(gc), nil
}

// SetCwd 设置上下文收集器的工作目录。
func (a *ContextGathererAdapter) SetCwd(cwd string) {
	a.inner.SetCwd(cwd)
}

func convertToOrchestrator(gc *GatheredContext) *orchestrator.GatheredContext {
	result := &orchestrator.GatheredContext{
		Summary: gc.Summary,
	}

	for _, mod := range gc.AffectedModules {
		result.AffectedModules = append(result.AffectedModules, orchestrator.AffectedModule{
			Name:          mod.Name,
			Path:          mod.Path,
			AffectedFiles: mod.AffectedFiles,
			ImpactLevel:   mod.ImpactLevel,
		})
	}

	for _, pr := range gc.RelatedPRs {
		result.RelatedPRs = append(result.RelatedPRs, orchestrator.RelatedPR{
			Number: pr.Number,
			Title:  pr.Title,
		})
	}

	for _, ref := range gc.RawReferences {
		oRef := orchestrator.RawReference{
			Symbol: ref.Symbol,
		}
		for _, loc := range ref.FoundInFiles {
			oRef.FoundInFiles = append(oRef.FoundInFiles, orchestrator.ReferenceLocation{
				File:    loc.File,
				Line:    loc.Line,
				Content: loc.Content,
			})
		}
		result.RawReferences = append(result.RawReferences, oRef)
	}

	return result
}
