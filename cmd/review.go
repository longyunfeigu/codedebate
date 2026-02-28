package cmd

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/signal"
	"regexp"
	"strings"

	"github.com/fatih/color"
	"github.com/longyunfeigu/codedebate/internal/checkout"
	"github.com/longyunfeigu/codedebate/internal/config"
	"github.com/longyunfeigu/codedebate/internal/display"
	"github.com/longyunfeigu/codedebate/internal/orchestrator"
	"github.com/longyunfeigu/codedebate/internal/platform"
	"github.com/longyunfeigu/codedebate/internal/platform/detect"
	"github.com/longyunfeigu/codedebate/internal/review"
	"github.com/longyunfeigu/codedebate/internal/reviewpost"
	"github.com/longyunfeigu/codedebate/internal/util"
	"github.com/spf13/cobra"
)

// reviewCmd 定义了 "review" 子命令，这是 CodeDebate 的核心功能命令。
// 它接受一个可选的位置参数（PR/MR 编号或 URL），支持多种审查模式：
//   - 直接指定 PR/MR 编号或 URL 进行审查
//   - 使用 --local 审查本地未提交的变更
//   - 使用 --branch 审查当前分支与基准分支的差异
//   - 使用 --files 审查指定的文件列表
var reviewCmd = &cobra.Command{
	Use:   "review [pr-number-or-url]",
	Short: "Review code changes with multiple AI reviewers",
	Args:  cobra.MaximumNArgs(1),
	RunE:  runReview,
}

func init() {
	f := reviewCmd.Flags()
	f.StringP("config", "c", "", "Path to config file")
	f.IntP("rounds", "r", 0, "Maximum debate rounds (overrides config)")
	f.StringP("output", "o", "", "Output to file")
	f.StringP("format", "f", "markdown", "Output format (markdown|json)")
	f.Bool("no-converge", false, "Disable convergence detection")
	f.Bool("show-tool-trace", false, "Show full analyzer/reviewer trace during review")
	f.BoolP("verbose", "v", false, "Alias for --show-tool-trace")
	f.BoolP("local", "l", false, "Review local uncommitted changes")
	f.StringP("branch", "b", "", "Review current branch vs base")
	f.StringSlice("files", nil, "Review specific files")
	f.String("reviewers", "", "Comma-separated reviewer IDs")
	f.BoolP("all", "a", false, "Use all reviewers")
	f.Bool("skip-context", false, "Skip context gathering")
	f.Bool("no-post", false, "Skip posting review comments")
	f.Bool("no-post-summary", false, "Skip posting review summary note")
}

const codedebateSummaryMarker = reviewpost.CodeDebateSummaryMarker

func runReview(cmd *cobra.Command, args []string) error {
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt)
	defer cancel()

	d := display.New()
	showToolTrace, _ := cmd.Flags().GetBool("show-tool-trace")
	verbose, _ := cmd.Flags().GetBool("verbose")
	d.SetShowToolTrace(showToolTrace || verbose)
	d.SpinnerStart("Loading configuration...")

	configPath, _ := cmd.Flags().GetString("config")
	cfg, err := config.LoadConfig(configPath)
	if err != nil {
		d.SpinnerFail("Configuration error")
		return err
	}
	d.SpinnerSucceed("Configuration loaded")

	var platformType, platformHost string
	if cfg.Platform != nil {
		platformType = cfg.Platform.Type
		platformHost = cfg.Platform.Host
	}
	plat, platErr := detect.FromRemote(platformType, platformHost)
	if platErr != nil {
		util.Warnf("Platform detection failed: %v", platErr)
	}

	target, err := resolveTarget(cmd, args, plat, plat, cfg.Defaults.DiffExclude)
	if err != nil {
		if review.IsNoReviewableChanges(err) {
			util.Infof("%v", err)
			return nil
		}
		return err
	}

	allIDs := make([]string, 0, len(cfg.Reviewers))
	for id := range cfg.Reviewers {
		allIDs = append(allIDs, id)
	}
	selectedIDs, err := selectReviewerIDs(cmd, allIDs)
	if err != nil {
		return err
	}

	skipContext, _ := cmd.Flags().GetBool("skip-context")
	noConverge, _ := cmd.Flags().GetBool("no-converge")
	maxRounds, _ := cmd.Flags().GetInt("rounds")

	platformName := ""
	if plat != nil {
		platformName = plat.Name()
	}
	checkoutHost := ""
	if cfg.Platform != nil {
		checkoutHost = cfg.Platform.Host
	}

	runner := review.NewRunner(cfg, checkout.NewManager(cfg.Checkout))
	d.SpinnerStart("Preparing review...")
	prepared, err := runner.Prepare(*target, review.RunOptions{
		ReviewerIDs:        selectedIDs,
		SkipContext:        skipContext,
		MaxRoundsOverride:  maxRounds,
		DisableConvergence: noConverge,
		HistoryProvider:    plat,
		Commenter:          plat,
		CheckoutPlatform:   platformName,
		CheckoutHost:       checkoutHost,
	})
	if err != nil {
		d.SpinnerFail("Failed to prepare review")
		return err
	}
	defer prepared.Close()
	d.SpinnerSucceed("Review prepared")

	d.ReviewHeader(prepared.Job().Label, prepared.ReviewerIDs(), prepared.MaxRounds(), prepared.CheckConvergence(), prepared.HasContext())

	result, err := prepared.Run(ctx, d)
	if err != nil {
		return fmt.Errorf("review failed: %w", err)
	}

	d.FinalConclusion(result.FinalConclusion)
	if len(result.ParsedIssues) > 0 {
		d.IssuesTable(result.ParsedIssues)
	}

	noPost, _ := cmd.Flags().GetBool("no-post")
	if !noPost && target.Type == "pr" && len(result.ParsedIssues) > 0 && plat != nil {
		prNum := extractPRNumber(target.Label)
		if prNum != "" {
			d.SpinnerStart("Posting review comments...")
			platIssues := convertIssuesToPlatform(result.ParsedIssues)
			postResult := plat.PostIssuesAsComments(prNum, platIssues, target.Repo)
			d.SpinnerSucceed(fmt.Sprintf("Posted %d comments (%d inline, %d file-level, %d global, %d failed, %d skipped)",
				postResult.Posted, postResult.Inline, postResult.FileLevel, postResult.Global, postResult.Failed, postResult.Skipped))
		}
	} else if !noPost && target.Type == "pr" {
		if plat == nil {
			util.Warnf("Skipped posting comments: platform not detected (check platform config)")
		}
		if len(result.ParsedIssues) == 0 {
			util.Warnf("Skipped posting comments: no structured issues parsed from reviewers")
		}
	}

	noPostSummary, _ := cmd.Flags().GetBool("no-post-summary")
	if shouldPostSummary(noPost, noPostSummary, target.Type, result.FinalConclusion, plat) {
		prNum := extractPRNumber(target.Label)
		if prNum == "" {
			util.Warnf("Skipped posting summary: failed to extract PR/MR number from label %q", target.Label)
		} else {
			d.SpinnerStart("Posting review summary...")
			summaryBody := buildSummaryNoteBody(result.FinalConclusion)
			if err := upsertSummaryNote(plat, prNum, target.Repo, summaryBody); err != nil {
				d.SpinnerFail("Failed to post review summary")
				util.Warnf("Failed posting review summary: %v", err)
			} else {
				d.SpinnerSucceed("Review summary posted")
			}
		}
	} else if !noPost && !noPostSummary && target.Type == "pr" {
		if plat == nil {
			util.Warnf("Skipped posting summary: platform not detected (check platform config)")
		}
		if plat != nil && !supportsSummaryPosting(plat) {
			util.Warnf("Skipped posting summary: platform %q does not support summary posting", plat.Name())
		}
		if strings.TrimSpace(result.FinalConclusion) == "" {
			util.Warnf("Skipped posting summary: final conclusion is empty")
		}
	}

	d.TokenUsage(result.TokenUsage, result.ConvergedAtRound)

	outputFile, _ := cmd.Flags().GetString("output")
	if outputFile != "" {
		format, _ := cmd.Flags().GetString("format")
		if err := saveOutput(outputFile, format, result, showToolTrace || verbose); err != nil {
			return fmt.Errorf("failed to save output: %w", err)
		}
		color.Green("\n  Output saved to: %s", outputFile)
	}

	return nil
}

// resolveTarget 根据命令行参数确定审查目标。
func resolveTarget(cmd *cobra.Command, args []string, resolver review.MRInputResolver, metadata platform.MRMetadataProvider, diffExclude []string) (*review.Job, error) {
	isLocal, _ := cmd.Flags().GetBool("local")
	branchBase, _ := cmd.Flags().GetString("branch")
	files, _ := cmd.Flags().GetStringSlice("files")

	if isLocal {
		return review.BuildLocalJob(diffExclude)
	}
	if cmd.Flags().Changed("branch") {
		if branchBase == "" {
			branchBase = "main"
		}
		return review.BuildBranchJob(branchBase, diffExclude)
	}
	if len(files) > 0 {
		return review.BuildFilesJob(files), nil
	}
	if len(args) > 0 {
		return review.BuildMRJobFromInput(args[0], resolver, metadata, diffExclude)
	}

	return nil, fmt.Errorf("please specify a PR/MR number or use --local, --branch, or --files")
}

func selectReviewerIDs(cmd *cobra.Command, allIDs []string) ([]string, error) {
	reviewersFlag, _ := cmd.Flags().GetString("reviewers")
	useAll, _ := cmd.Flags().GetBool("all")

	if reviewersFlag != "" {
		ids := strings.Split(reviewersFlag, ",")
		for i := range ids {
			ids[i] = strings.TrimSpace(ids[i])
		}
		idSet := make(map[string]bool)
		for _, id := range allIDs {
			idSet[id] = true
		}
		for _, id := range ids {
			if !idSet[id] {
				return nil, fmt.Errorf("unknown reviewer: %s (available: %s)", id, strings.Join(allIDs, ", "))
			}
		}
		return ids, nil
	}

	if useAll {
		return allIDs, nil
	}

	return allIDs, nil
}

func extractPRNumber(label string) string {
	re := regexp.MustCompile(`\d+`)
	if m := re.FindString(label); m != "" {
		return m
	}
	return ""
}

func saveOutput(path, format string, result *orchestrator.DebateResult, includeTranscript bool) error {
	var data []byte
	var err error

	if format == "json" {
		data, err = json.MarshalIndent(result, "", "  ")
	} else {
		data = []byte(display.FormatMarkdownWithOptions(result, display.MarkdownOptions{
			IncludeDebateTranscript: includeTranscript,
		}))
	}
	if err != nil {
		return err
	}

	return os.WriteFile(path, data, 0644)
}

// convertIssuesToPlatform 将编排器产出的 MergedIssue 列表转换为平台通用的评论格式。
func convertIssuesToPlatform(issues []orchestrator.MergedIssue) []platform.IssueForComment {
	return reviewpost.ConvertIssuesToPlatform(issues)
}

func shouldPostSummary(noPost, noPostSummary bool, targetType, finalConclusion string, plat platform.Named) bool {
	return !noPost &&
		!noPostSummary &&
		targetType == "pr" &&
		strings.TrimSpace(finalConclusion) != "" &&
		plat != nil &&
		supportsSummaryPosting(plat)
}

func buildSummaryNoteBody(finalConclusion string) string {
	return reviewpost.BuildSummaryNoteBody(finalConclusion)
}

func upsertSummaryNote(plat platform.Named, mrID, repo, body string) error {
	return reviewpost.UpsertSummaryNote(plat, mrID, repo, body)
}

func supportsSummaryPosting(plat platform.Named) bool {
	return reviewpost.SupportsSummaryPosting(plat)
}
