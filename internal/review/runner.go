package review

import (
	"context"
	"fmt"
	"regexp"
	"sort"
	"strings"

	"github.com/longyunfeigu/codedebate/internal/checkout"
	"github.com/longyunfeigu/codedebate/internal/config"
	appctx "github.com/longyunfeigu/codedebate/internal/context"
	"github.com/longyunfeigu/codedebate/internal/orchestrator"
	"github.com/longyunfeigu/codedebate/internal/platform"
	"github.com/longyunfeigu/codedebate/internal/provider"
	"github.com/longyunfeigu/codedebate/internal/util"
)

// RunOptions 控制一次 review 执行的装配细节。
type RunOptions struct {
	ReviewerIDs        []string
	SkipContext        bool
	MaxRoundsOverride  int
	DisableConvergence bool
	HistoryProvider    platform.HistoryProvider
	Commenter          platform.MRCommenter
	CheckoutPlatform   string
	CheckoutHost       string
}

var codedebateIssueTitlePattern = regexp.MustCompile(`\*\*(.+?)\*\*`)

// PreparedRun 是完成依赖装配、可直接执行的 review 任务。
type PreparedRun struct {
	job              Job
	reviewerIDs      []string
	maxRounds        int
	checkConvergence bool
	hasContext       bool
	orchestrator     *orchestrator.DebateOrchestrator
	release          func()
}

// Job 返回本次执行的 review 任务。
func (p *PreparedRun) Job() Job {
	return p.job
}

// ReviewerIDs 返回实际参与执行的 reviewer 列表。
func (p *PreparedRun) ReviewerIDs() []string {
	return append([]string(nil), p.reviewerIDs...)
}

// MaxRounds 返回实际生效的最大轮数。
func (p *PreparedRun) MaxRounds() int {
	return p.maxRounds
}

// CheckConvergence 返回是否启用共识检测。
func (p *PreparedRun) CheckConvergence() bool {
	return p.checkConvergence
}

// HasContext 返回本次执行是否启用了上下文收集。
func (p *PreparedRun) HasContext() bool {
	return p.hasContext
}

// Run 执行完整 review。
func (p *PreparedRun) Run(ctx context.Context, display orchestrator.DisplayCallbacks) (*orchestrator.DebateResult, error) {
	return p.orchestrator.RunStreaming(ctx, p.job.Label, p.job.Prompt, display)
}

// Close 释放本次执行持有的临时资源，例如 checkout worktree。
func (p *PreparedRun) Close() {
	if p.release != nil {
		p.release()
	}
}

// Runner 负责把配置与任务装配为可执行的 review。
type Runner struct {
	cfg         *config.CodeDebateConfig
	checkoutMgr *checkout.Manager
}

// NewRunner 创建共享 review runner。
func NewRunner(cfg *config.CodeDebateConfig, checkoutMgr *checkout.Manager) *Runner {
	if checkoutMgr == nil && cfg != nil {
		checkoutMgr = checkout.NewManager(cfg.Checkout)
	}
	return &Runner{
		cfg:         cfg,
		checkoutMgr: checkoutMgr,
	}
}

// StartCleanup 启动 checkout 缓存清理任务。
func (r *Runner) StartCleanup(ctx context.Context) {
	if r == nil || r.checkoutMgr == nil {
		return
	}
	r.checkoutMgr.StartCleanup(ctx)
}

// Wait 等待所有活跃 checkout 释放。
func (r *Runner) Wait() {
	if r == nil || r.checkoutMgr == nil {
		return
	}
	r.checkoutMgr.Wait()
}

// Prepare 根据任务和选项装配一次可执行的 review。
func (r *Runner) Prepare(job Job, opts RunOptions) (*PreparedRun, error) {
	if r.cfg == nil {
		return nil, fmt.Errorf("review runner requires config")
	}

	selectedIDs, err := r.resolveReviewerIDs(opts.ReviewerIDs)
	if err != nil {
		return nil, err
	}

	reviewers, err := r.buildReviewers(selectedIDs)
	if err != nil {
		return nil, err
	}
	analyzer, analyzerProvider, err := r.buildSpecialReviewer("analyzer", r.cfg.Analyzer)
	if err != nil {
		return nil, err
	}
	summarizer, summarizerProvider, err := r.buildSpecialReviewer("summarizer", r.cfg.Summarizer)
	if err != nil {
		return nil, err
	}

	var (
		contextGatherer orchestrator.ContextGathererInterface
		checkoutResult  checkout.Result
		release         func()
	)

	if job.Type == "pr" && r.checkoutMgr != nil && strings.TrimSpace(job.Repo) != "" {
		checkoutResult = r.checkoutMgr.Checkout(checkout.Params{
			Platform: opts.CheckoutPlatform,
			Repo:     job.Repo,
			MRNumber: job.MRNumber,
			Host:     opts.CheckoutHost,
		})
		if checkoutResult.Available {
			release = checkoutResult.Release
		}
	}

	cwd := "."
	if checkoutResult.Available {
		cwd = checkoutResult.RepoDir
	}
	for i := range reviewers {
		provider.SetCwdIfSupported(reviewers[i].Provider, cwd)
	}
	provider.SetCwdIfSupported(analyzerProvider, cwd)
	provider.SetCwdIfSupported(summarizerProvider, cwd)

	hasContext := !opts.SkipContext && r.cfg.ContextGatherer != nil && r.cfg.ContextGatherer.Enabled
	if hasContext {
		contextModel := r.cfg.ContextGatherer.Model
		if contextModel == "" {
			contextModel = r.cfg.Analyzer.Model
		}
		contextProvider, err := provider.CreateProvider(contextModel, "", "", "", r.cfg)
		if err != nil {
			util.Warnf("Failed to create context gatherer provider: %v", err)
			hasContext = false
		} else {
			contextGatherer = appctx.NewContextGathererAdapter(contextProvider, r.cfg.ContextGatherer, opts.HistoryProvider)
			if cg, ok := contextGatherer.(interface{ SetCwd(string) }); ok {
				cg.SetCwd(cwd)
			}
		}
	}

	if checkoutResult.Available {
		job.Prompt += "\n\nNote: The full repository source code is available in your working directory.\nYou can browse files, read implementations, and examine the broader codebase context beyond the diff."
	}

	previousComments := loadPreviousComments(job, opts.Commenter)

	maxRounds := r.cfg.Defaults.MaxRounds
	if opts.MaxRoundsOverride > 0 {
		maxRounds = opts.MaxRoundsOverride
	}
	isSolo := len(reviewers) == 1
	if isSolo {
		maxRounds = 1
	}
	checkConvergence := !isSolo && !opts.DisableConvergence && r.cfg.Defaults.CheckConvergence

	orch := orchestrator.New(orchestrator.OrchestratorConfig{
		Reviewers:       reviewers,
		Analyzer:        analyzer,
		Summarizer:      summarizer,
		ContextGatherer: contextGatherer,
		Options: orchestrator.OrchestratorOptions{
			MaxRounds:        maxRounds,
			CheckConvergence: checkConvergence,
			Language:         r.cfg.Defaults.Language,
			StructurizeMode:  r.cfg.Defaults.StructurizeMode,
			PreviousComments: previousComments,
		},
	})

	return &PreparedRun{
		job:              job,
		reviewerIDs:      selectedIDs,
		maxRounds:        maxRounds,
		checkConvergence: checkConvergence,
		hasContext:       hasContext,
		orchestrator:     orch,
		release:          release,
	}, nil
}

func (r *Runner) resolveReviewerIDs(requested []string) ([]string, error) {
	if len(requested) == 0 {
		ids := make([]string, 0, len(r.cfg.Reviewers))
		for id := range r.cfg.Reviewers {
			ids = append(ids, id)
		}
		return ids, nil
	}

	ids := make([]string, 0, len(requested))
	for _, id := range requested {
		trimmed := strings.TrimSpace(id)
		if trimmed == "" {
			continue
		}
		if _, ok := r.cfg.Reviewers[trimmed]; !ok {
			return nil, fmt.Errorf("unknown reviewer: %s", trimmed)
		}
		ids = append(ids, trimmed)
	}
	if len(ids) == 0 {
		return nil, fmt.Errorf("no reviewers selected")
	}
	return ids, nil
}

func (r *Runner) buildReviewers(ids []string) ([]orchestrator.Reviewer, error) {
	reviewers := make([]orchestrator.Reviewer, 0, len(ids))
	for _, id := range ids {
		rc := r.cfg.Reviewers[id]
		p, err := provider.CreateProvider(rc.Model, rc.ModelName, rc.ReasoningEffort, rc.Provider, r.cfg)
		if err != nil {
			return nil, fmt.Errorf("failed to create provider for reviewer %s: %w", id, err)
		}
		reviewers = append(reviewers, orchestrator.Reviewer{
			ID:           id,
			Provider:     p,
			SystemPrompt: rc.Prompt,
		})
	}
	return reviewers, nil
}

func (r *Runner) buildSpecialReviewer(id string, rc config.ReviewerConfig) (orchestrator.Reviewer, provider.AIProvider, error) {
	p, err := provider.CreateProvider(rc.Model, rc.ModelName, rc.ReasoningEffort, rc.Provider, r.cfg)
	if err != nil {
		return orchestrator.Reviewer{}, nil, fmt.Errorf("failed to create %s provider: %w", id, err)
	}
	return orchestrator.Reviewer{
		ID:           id,
		Provider:     p,
		SystemPrompt: rc.Prompt,
	}, p, nil
}

func loadPreviousComments(job Job, commenter platform.MRCommenter) string {
	if commenter == nil || job.Type != "pr" {
		return ""
	}
	if strings.TrimSpace(job.MRNumber) == "" || strings.TrimSpace(job.Repo) == "" {
		return ""
	}
	return formatPreviousComments(commenter.GetExistingComments(job.MRNumber, job.Repo))
}

func formatPreviousComments(comments []platform.ExistingComment) string {
	type finding struct {
		issueKey string
		path     string
		line     int
		text     string
	}

	findings := make([]finding, 0, len(comments))
	seen := make(map[string]struct{}, len(comments))
	for _, comment := range comments {
		if !comment.IsCodeDebate || comment.Meta == nil || !strings.EqualFold(strings.TrimSpace(comment.Meta.Status), "active") {
			continue
		}
		key := strings.TrimSpace(comment.Meta.IssueKey)
		if key == "" {
			key = fmt.Sprintf("%s|%s|%s", strings.TrimSpace(comment.Path), formatCommentLine(comment), platform.BodyHash(comment.Body))
		}
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}

		severity := extractCommentSeverity(comment.Body)
		location := formatCommentLocation(comment)
		title := extractCommentTitle(comment.Body)
		findings = append(findings, finding{
			issueKey: key,
			path:     strings.TrimSpace(comment.Path),
			line:     sortLine(comment),
			text:     fmt.Sprintf("[%s] %s - %s", severity, location, title),
		})
	}

	if len(findings) == 0 {
		return ""
	}

	sort.SliceStable(findings, func(i, j int) bool {
		if findings[i].path != findings[j].path {
			return findings[i].path < findings[j].path
		}
		if findings[i].line != findings[j].line {
			return findings[i].line < findings[j].line
		}
		return findings[i].issueKey < findings[j].issueKey
	})

	var b strings.Builder
	for i, item := range findings {
		fmt.Fprintf(&b, "%d. %s\n", i+1, item.text)
	}
	return strings.TrimSpace(b.String())
}

func extractCommentSeverity(body string) string {
	trimmed := strings.TrimSpace(platform.StripCodeDebateMeta(body))
	switch {
	case strings.HasPrefix(trimmed, "🔴"):
		return "critical"
	case strings.HasPrefix(trimmed, "🟠"):
		return "high"
	case strings.HasPrefix(trimmed, "🟡"):
		return "medium"
	case strings.HasPrefix(trimmed, "🟢"):
		return "low"
	default:
		return "unspecified"
	}
}

func extractCommentTitle(body string) string {
	trimmed := strings.TrimSpace(platform.StripCodeDebateMeta(body))
	if trimmed == "" {
		return "Existing CodeDebate finding"
	}
	if match := codedebateIssueTitlePattern.FindStringSubmatch(trimmed); len(match) == 2 {
		return strings.TrimSpace(match[1])
	}
	for _, line := range strings.Split(trimmed, "\n") {
		line = strings.TrimSpace(line)
		line = strings.TrimLeft(line, "🔴🟠🟡🟢⚪")
		line = strings.TrimSpace(strings.Trim(line, "`*_ -"))
		if line != "" {
			return line
		}
	}
	return "Existing CodeDebate finding"
}

func formatCommentLocation(comment platform.ExistingComment) string {
	line := commentLine(comment)
	path := strings.TrimSpace(comment.Path)
	switch {
	case path != "" && line != nil:
		return fmt.Sprintf("`%s:%d`", path, *line)
	case path != "":
		return fmt.Sprintf("`%s`", path)
	case line != nil:
		return fmt.Sprintf("line %d", *line)
	default:
		return "general comment"
	}
}

func formatCommentLine(comment platform.ExistingComment) string {
	line := commentLine(comment)
	if line == nil {
		return "0"
	}
	return fmt.Sprintf("%d", *line)
}

func commentLine(comment platform.ExistingComment) *int {
	if comment.Line != nil {
		return comment.Line
	}
	return comment.OldLine
}

func sortLine(comment platform.ExistingComment) int {
	line := commentLine(comment)
	if line == nil {
		return int(^uint(0) >> 1)
	}
	return *line
}
