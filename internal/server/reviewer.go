package server

import (
	"context"
	"fmt"
	"log/slog"
	"strings"

	"github.com/longyunfeigu/codedebate/internal/display"
	"github.com/longyunfeigu/codedebate/internal/orchestrator"
	"github.com/longyunfeigu/codedebate/internal/platform"
	"github.com/longyunfeigu/codedebate/internal/review"
	"github.com/longyunfeigu/codedebate/internal/reviewpost"
)

type reviewPlatform interface {
	platform.Named
	platform.MRMetadataProvider
	platform.MRCommenter
	platform.IssueCommenter
	platform.HistoryProvider
}

const codedebateSummaryMarker = reviewpost.CodeDebateSummaryMarker

// RunServerReview 执行 server 模式下的审查流程。
func RunServerReview(ctx context.Context, event *MergeRequestEvent, plat reviewPlatform, runner *review.Runner, checkoutHost string, diffExclude []string, logger *slog.Logger) error {
	repo := event.Project.PathWithNamespace
	mrID := fmt.Sprintf("%d", event.ObjectAttributes.IID)

	logger.Info("starting review", "repo", repo, "mr", mrID)

	job, err := review.BuildServerMRJob(review.MRRef{
		ID:   mrID,
		Repo: repo,
		URL:  event.ObjectAttributes.URL,
	}, plat, diffExclude)
	if err != nil {
		if review.IsNoReviewableChanges(err) {
			logger.Info("review skipped", "repo", repo, "mr", mrID, "reason", err.Error())
			return nil
		}
		return err
	}

	prepared, err := runner.Prepare(*job, review.RunOptions{
		HistoryProvider:  plat,
		Commenter:        plat,
		CheckoutPlatform: plat.Name(),
		CheckoutHost:     checkoutHost,
	})
	if err != nil {
		return err
	}
	defer prepared.Close()

	result, err := prepared.Run(ctx, display.NewNoopDisplay(logger))
	if err != nil {
		return fmt.Errorf("review failed: %w", err)
	}

	totalInput, totalOutput, totalCost := summarizeTokenUsage(result.TokenUsage)
	logger.Info("review completed",
		"repo", repo,
		"mr", mrID,
		"issues", len(result.ParsedIssues),
		"input_tokens", totalInput,
		"output_tokens", totalOutput,
		"estimated_cost", totalCost,
	)

	if len(result.ParsedIssues) > 0 {
		platIssues := convertIssuesToPlatform(result.ParsedIssues)
		postResult := plat.PostIssuesAsComments(mrID, platIssues, repo)
		logger.Info("posted comments",
			"posted", postResult.Posted, "inline", postResult.Inline, "file_level", postResult.FileLevel,
			"global", postResult.Global, "failed", postResult.Failed, "skipped", postResult.Skipped)
	}

	if shouldPostServerSummary(result.FinalConclusion, plat) {
		summaryBody := buildServerSummaryNoteBody(result.FinalConclusion)
		if err := upsertServerSummaryNote(plat, mrID, repo, summaryBody); err != nil {
			logger.Error("failed to post summary note", "error", err)
		} else {
			logger.Info("summary note posted", "mr", mrID)
		}
	} else if strings.TrimSpace(result.FinalConclusion) != "" {
		logger.Info("skipped summary note, platform unsupported", "platform", plat.Name())
	}

	return nil
}

// convertIssuesToPlatform 将 MergedIssue 列表转换为平台通用的评论格式。
func convertIssuesToPlatform(issues []orchestrator.MergedIssue) []platform.IssueForComment {
	return reviewpost.ConvertIssuesToPlatform(issues)
}

func shouldPostServerSummary(finalConclusion string, plat platform.Named) bool {
	return strings.TrimSpace(finalConclusion) != "" && supportsServerSummaryPosting(plat)
}

func buildServerSummaryNoteBody(finalConclusion string) string {
	return reviewpost.BuildSummaryNoteBody(finalConclusion)
}

func upsertServerSummaryNote(plat platform.Named, mrID, repo, body string) error {
	return reviewpost.UpsertSummaryNote(plat, mrID, repo, body)
}

func supportsServerSummaryPosting(plat platform.Named) bool {
	return reviewpost.SupportsSummaryPosting(plat)
}

func summarizeTokenUsage(usage []orchestrator.TokenUsage) (input int, output int, cost float64) {
	for _, u := range usage {
		input += u.InputTokens
		output += u.OutputTokens
		cost += u.EstimatedCost
	}
	return input, output, cost
}
