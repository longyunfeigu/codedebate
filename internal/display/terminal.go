// Package display 负责处理 CodeDebate 代码审查过程中的所有终端输出和显示逻辑。
// 包括进度旋转动画、审查状态展示、结果表格、Token 用量统计等。
package display

import (
	"encoding/json"
	"fmt"
	"math/rand"
	"os"
	"regexp"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/briandowns/spinner"
	"github.com/fatih/color"
	"github.com/longyunfeigu/codedebate/internal/orchestrator"
	"github.com/mattn/go-isatty"
	"golang.org/x/term"
)

// Display 负责管理审查过程中的所有终端输出。
// 包含旋转动画器（spinner）用于显示等待状态，
// 以及当前审查者和轮次信息用于格式化输出。
type Display struct {
	spin               *spinner.Spinner // 终端旋转动画器，用于显示等待/处理中状态
	isTTY              bool             // stdout 是否为 TTY（非 TTY 时禁用 spinner 避免刷屏）
	termWidth          int              // 终端列宽，用于截断 spinner 文本防止换行
	currentReviewer    string           // 当前正在展示输出的审查者 ID
	currentRound       int              // 当前审查轮次
	maxRounds          int              // 最大审查轮次数
	showToolTrace      bool             // 是否显示 analyzer/reviewer 的完整过程输出
	traceHintShown     bool             // 默认摘要模式下，是否已提示可用 --show-tool-trace 查看明细
	mu                 sync.Mutex
	streamedMessage    map[string]bool
	announcedReviewers map[string]bool
	currentPhase       string
}

// New 创建一个新的 Display 实例。
// 初始化旋转动画器（使用字符集 14，120ms 刷新间隔），初始轮次设为 1。
func New() *Display {
	s := spinner.New(spinner.CharSets[14], 120*time.Millisecond)
	fd := int(os.Stderr.Fd())
	tty := isatty.IsTerminal(os.Stderr.Fd()) || isatty.IsCygwinTerminal(os.Stderr.Fd())

	width := 80
	if tty {
		if w, _, err := term.GetSize(fd); err == nil && w > 0 {
			width = w
		}
	}

	return &Display{
		spin:               s,
		isTTY:              tty,
		termWidth:          width,
		currentRound:       1,
		showToolTrace:      false,
		traceHintShown:     false,
		streamedMessage:    make(map[string]bool),
		announcedReviewers: make(map[string]bool),
	}
}

// SetShowToolTrace 控制是否显示 analyzer/reviewer 的完整过程输出。
// 默认关闭（仅展示摘要结果）；开启后会实时渲染完整 Markdown 内容。
func (d *Display) SetShowToolTrace(show bool) {
	d.showToolTrace = show
}

// --- 旋转动画方法 ---

// SpinnerStart 启动旋转动画并显示指定文本。
// 用于在等待 AI 响应等耗时操作时向用户显示进度。
func (d *Display) SpinnerStart(text string) {
	if !d.isTTY {
		fmt.Fprintf(os.Stderr, "  %s\n", text)
		return
	}
	d.spin.Suffix = "  " + text
	d.spin.Start()
}

// SpinnerSucceed 停止旋转动画并显示绿色成功消息。
func (d *Display) SpinnerSucceed(text string) {
	if d.isTTY {
		d.spin.Stop()
	}
	color.Green("  %s %s", color.GreenString("✓"), text)
}

// SpinnerFail 停止旋转动画并显示红色失败消息。
func (d *Display) SpinnerFail(text string) {
	if d.isTTY {
		d.spin.Stop()
	}
	color.Red("  %s %s", color.RedString("✗"), text)
}

// SpinnerStop 停止旋转动画但不打印任何消息。
func (d *Display) SpinnerStop() {
	if d.isTTY {
		d.spin.Stop()
	}
}

// --- 审查生命周期方法 ---

// SetMaxRounds 更新最大轮次数，用于显示目的。
func (d *Display) SetMaxRounds(maxRounds int) {
	d.maxRounds = maxRounds
}

// ReviewHeader 打印审查头部信息，包含配置详情。
// 展示审查目标、审查者列表、最大轮次数以及是否启用收敛检查和上下文收集。
func (d *Display) ReviewHeader(label string, reviewerIDs []string, maxRounds int, checkConvergence, contextEnabled bool) {
	d.maxRounds = maxRounds

	fmt.Println()
	color.Cyan("  %s", strings.Repeat("=", 50))
	color.New(color.FgCyan, color.Bold).Printf("  CodeDebate Code Review\n")
	color.Cyan("  %s", strings.Repeat("=", 50))
	fmt.Println()

	color.White("  Target:      %s", label)
	color.White("  Reviewers:   %s", strings.Join(reviewerIDs, ", "))
	color.White("  Max Rounds:  %d", maxRounds)
	if d.showToolTrace {
		color.White("  Trace:       enabled")
	} else {
		color.White("  Trace:       summary-only (use --show-tool-trace)")
	}

	if checkConvergence {
		color.White("  Convergence: enabled")
	}
	if contextEnabled {
		color.White("  Context:     enabled")
	}

	fmt.Println()
}

// --- DisplayCallbacks 接口方法 ---
// 以下方法实现了 orchestrator 的回调接口，用于在审查过程的各个阶段更新终端显示。

// OnWaiting 在等待审查者、分析器或摘要器响应时显示旋转动画。
// 根据不同的 reviewerID 显示不同的等待提示文本，并附带随机冷笑话缓解等待焦虑。
func (d *Display) OnWaiting(reviewerID string) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.SpinnerStop()

	var label string
	switch {
	case reviewerID == "context-gatherer":
		d.printPhaseHeader("Phase 1/3", "System Context", reviewerID)
		label = "Gathering system context"
	case reviewerID == "analyzer":
		d.printPhaseHeader("Phase 1/3", "Analyzer", reviewerID)
		label = "Analyzing changes"
	case reviewerID == "reviewer-summaries":
		d.printPhaseHeader("Phase 3/3", "Reviewer Summaries", reviewerID)
		label = "Collecting reviewer summaries"
	case reviewerID == "final-conclusion":
		d.printPhaseHeader("Phase 3/3", "Final Conclusion", reviewerID)
		label = "Synthesizing final conclusion"
	case reviewerID == "summarizer":
		d.printPhaseHeader("Phase 3/3", "Final Conclusion", reviewerID)
		label = "Generating final summary"
	case reviewerID == "convergence-check":
		d.printPhaseHeader("Phase 2/3", "Convergence Check", reviewerID)
		label = "Evaluating if reviewers reached consensus"
	case reviewerID == "structurizer":
		d.printPhaseHeader("Phase 3/3", "Issue Structurizer", reviewerID)
		label = "Extracting structured issues"
	case strings.HasPrefix(reviewerID, "round-"):
		roundNum := strings.TrimPrefix(reviewerID, "round-")
		d.printPhaseHeader("Phase 2/3", fmt.Sprintf("Debate Round %s/%d", roundNum, d.maxRounds), reviewerID)
		label = fmt.Sprintf("Round %s: Starting parallel review", roundNum)
	default:
		label = fmt.Sprintf("%s is thinking", reviewerID)
	}

	if !d.isTTY {
		fmt.Fprintf(os.Stderr, "  %s...\n", label)
		return
	}

	prefix := fmt.Sprintf("  %s... | ", label)
	d.spin.Suffix = d.buildSpinnerSuffix(prefix, getRandomJoke())
	d.spin.Start()
}

// OnMessage 显示审查者的响应内容。
// 当审查者切换时打印新的审查者标题头，然后渲染 Markdown 格式的响应内容。
func (d *Display) OnMessage(reviewerID string, content string) {
	d.mu.Lock()
	defer d.mu.Unlock()

	if !d.showToolTrace {
		// analyzer 是流式 chunk 回调，默认模式下不逐块展示，避免刷屏
		if reviewerID == "analyzer" {
			return
		}
		d.SpinnerStop()
		if !d.traceHintShown {
			fmt.Println(color.HiBlackString("  Detailed trace is hidden (use --show-tool-trace or -v)."))
			d.traceHintShown = true
		}
		return
	}

	d.SpinnerStop()

	if d.streamedMessage[reviewerID] {
		if content != "" && !strings.HasSuffix(content, "\n") {
			fmt.Println()
		}
		delete(d.streamedMessage, reviewerID)
		return
	}

	if reviewerID != d.currentReviewer {
		d.printReviewerHeader(reviewerID)
	}

	rendered := RenderTerminalMarkdown(content)
	fmt.Print(rendered)
}

// OnMessageChunk 实时渲染流式输出的 chunk。
// reviewer 和 analyzer 的 ChatStream 会在这里逐块透传，改善长耗时阶段的停顿感。
func (d *Display) OnMessageChunk(reviewerID string, chunk string) {
	d.mu.Lock()
	defer d.mu.Unlock()

	if !d.showToolTrace || chunk == "" {
		return
	}

	d.SpinnerStop()
	if reviewerID != d.currentReviewer {
		d.printReviewerHeader(reviewerID)
	}

	d.streamedMessage[reviewerID] = true
	fmt.Print(chunk)
}

// OnParallelStatus 更新旋转动画以显示并行执行的进度。
// 展示每个审查者的状态（已完成/思考中/等待中）和耗时。
func (d *Display) OnParallelStatus(round int, statuses []orchestrator.ReviewerStatus) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.showToolTrace {
		for _, status := range statuses {
			if status.Status != "thinking" || d.announcedReviewers[status.ReviewerID] {
				continue
			}
			d.SpinnerStop()
			fmt.Println(color.HiBlackString("  [%s] waiting for first chunk...", status.ReviewerID))
			d.announcedReviewers[status.ReviewerID] = true
		}
	}
	if !d.isTTY {
		return
	}
	statusLine := formatParallelStatus(round, statuses)
	prefix := fmt.Sprintf("  %s | ", statusLine)
	d.spin.Suffix = d.buildSpinnerSuffix(prefix, getRandomJoke())
}

func (d *Display) OnSummaryStatus(statuses []orchestrator.ReviewerStatus) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if !d.isTTY {
		return
	}
	statusLine := formatSummaryStatus(statuses)
	prefix := fmt.Sprintf("  %s | ", statusLine)
	d.spin.Suffix = d.buildSpinnerSuffix(prefix, getRandomJoke())
}

// OnRoundComplete 显示审查轮次完成状态。
// 如果审查者达成共识（converged=true），显示绿色的 CONVERGED 标记并提示提前结束；
// 否则显示红色的 NOT CONVERGED 标记，继续下一轮。
func (d *Display) OnRoundComplete(round int, converged bool) {
	d.mu.Lock()
	defer d.mu.Unlock()
	fmt.Println()
	if converged {
		fmt.Printf("%s %s\n", color.YellowString("└─ Verdict:"), color.New(color.FgGreen, color.Bold).Sprint("CONVERGED"))
		color.New(color.FgGreen, color.Bold).Printf("\n  Round %d/%d - CONSENSUS REACHED\n", round, d.maxRounds)
		color.Green("   Stopping early to save tokens.\n")
	} else {
		fmt.Printf("%s %s\n", color.YellowString("└─ Verdict:"), color.New(color.FgRed, color.Bold).Sprint("NOT CONVERGED"))
		fmt.Printf("\n%s\n\n", color.HiBlackString("── Round %d/%d complete ──", round, d.maxRounds))
	}
	d.currentRound = round + 1
}

// OnConvergenceJudgment 展示收敛判断者的推理过程。
// 以灰色文本逐行显示判断理由，帮助用户理解为何审查提前结束或继续。
func (d *Display) OnConvergenceJudgment(verdict string, reasoning string) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if reasoning == "" {
		return
	}
	lines := strings.Split(reasoning, "\n")
	for _, line := range lines {
		fmt.Println(color.HiBlackString("│ %s", line))
	}
}

// OnContextGathered 展示收集到的系统上下文信息。
// 包括受影响模块（按影响级别着色）、关联 PR 列表和 AI 生成的上下文摘要。
// 当 AffectedModules 为空但 Summary 含 JSON 时，尝试二次解析并格式化展示。
func (d *Display) OnContextGathered(ctx *orchestrator.GatheredContext) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.SpinnerStop()

	color.New(color.FgMagenta, color.Bold).Printf("\n%s\n", strings.Repeat("─", 50))
	color.New(color.FgMagenta, color.Bold).Printf("  System Context\n")
	color.New(color.FgMagenta, color.Bold).Printf("%s\n\n", strings.Repeat("─", 50))

	// 兜底：如果 AffectedModules 为空但 Summary 包含 JSON，尝试二次解析
	if len(ctx.AffectedModules) == 0 && ctx.Summary != "" && looksLikeJSON(ctx.Summary) {
		if parsed := tryParseContextJSON(ctx.Summary); parsed != nil {
			if len(parsed.AffectedModules) > 0 {
				ctx.AffectedModules = parsed.AffectedModules
			}
			if parsed.Summary != "" {
				ctx.Summary = parsed.Summary
			}
		}
	}

	if len(ctx.AffectedModules) > 0 {
		fmt.Println(color.HiBlackString("Affected Modules:"))
		for _, mod := range ctx.AffectedModules {
			var impact string
			switch mod.ImpactLevel {
			case "core":
				impact = color.RedString("●")
			case "moderate":
				impact = color.YellowString("●")
			default:
				impact = color.GreenString("●")
			}
			fmt.Printf("  %s %s (%d files)\n", impact, color.HiBlackString(mod.Name), len(mod.AffectedFiles))
			printAffectedFiles(mod.AffectedFiles, 8)
		}
		fmt.Println()
	}

	if len(ctx.RelatedPRs) > 0 {
		fmt.Println(color.HiBlackString("Related Changes:"))
		limit := len(ctx.RelatedPRs)
		if limit > 5 {
			limit = 5
		}
		for _, pr := range ctx.RelatedPRs[:limit] {
			fmt.Printf("  %s #%d: %s\n", color.HiBlackString("•"), pr.Number, color.HiBlackString(pr.Title))
		}
		fmt.Println()
	}

	if ctx.Summary != "" {
		rendered := RenderTerminalMarkdown(ctx.Summary)
		fmt.Print(rendered)
	}
}

// looksLikeJSON 快速判断字符串是否可能是 JSON 对象。
func looksLikeJSON(s string) bool {
	trimmed := strings.TrimSpace(s)
	return strings.HasPrefix(trimmed, "{") || strings.Contains(trimmed, `"affectedModules"`)
}

// contextJSONFallback 是二次解析 Summary 中 JSON 时使用的结构。
type contextJSONFallback struct {
	AffectedModules []struct {
		Name          string   `json:"name"`
		Path          string   `json:"path"`
		AffectedFiles []string `json:"affectedFiles"`
		ImpactLevel   string   `json:"impactLevel"`
	} `json:"affectedModules"`
	Summary string `json:"summary"`
}

// tryParseContextJSON 尝试从可能包含 JSON 的文本中解析出上下文数据。
// 支持原始 JSON、```json 代码块包裹的 JSON，以及被截断的不完整 JSON。
func tryParseContextJSON(text string) *orchestrator.GatheredContext {
	jsonStr := text

	// 尝试从 ```json 代码块中提取
	if idx := strings.Index(text, "```json"); idx >= 0 {
		start := idx + 7
		if end := strings.Index(text[start:], "```"); end >= 0 {
			jsonStr = strings.TrimSpace(text[start : start+end])
		}
	} else if idx := strings.Index(text, "```"); idx >= 0 {
		start := idx + 3
		// 跳过可能的语言标记行
		if nl := strings.IndexByte(text[start:], '\n'); nl >= 0 {
			start += nl + 1
		}
		if end := strings.Index(text[start:], "```"); end >= 0 {
			jsonStr = strings.TrimSpace(text[start : start+end])
		}
	}

	// 先尝试完整解析
	var fb contextJSONFallback
	if err := json.Unmarshal([]byte(jsonStr), &fb); err == nil && len(fb.AffectedModules) > 0 {
		return fallbackToContext(&fb)
	}

	// 完整解析失败（可能 JSON 被截断），尝试逐个提取 affectedModules 中的完整对象
	modules := extractModulesFromTruncatedJSON(jsonStr)
	if len(modules) > 0 {
		result := &orchestrator.GatheredContext{}
		for _, mod := range modules {
			result.AffectedModules = append(result.AffectedModules, orchestrator.AffectedModule{
				Name:          mod.Name,
				Path:          mod.Path,
				AffectedFiles: mod.AffectedFiles,
				ImpactLevel:   mod.ImpactLevel,
			})
		}
		return result
	}

	return nil
}

// fallbackToContext 将解析成功的 contextJSONFallback 转为 GatheredContext。
func fallbackToContext(fb *contextJSONFallback) *orchestrator.GatheredContext {
	result := &orchestrator.GatheredContext{
		Summary: fb.Summary,
	}
	for _, mod := range fb.AffectedModules {
		result.AffectedModules = append(result.AffectedModules, orchestrator.AffectedModule{
			Name:          mod.Name,
			Path:          mod.Path,
			AffectedFiles: mod.AffectedFiles,
			ImpactLevel:   mod.ImpactLevel,
		})
	}
	return result
}

// truncatedModuleJSON 用于从截断 JSON 中提取单个 module 对象。
type truncatedModuleJSON struct {
	Name          string   `json:"name"`
	Path          string   `json:"path"`
	AffectedFiles []string `json:"affectedFiles"`
	ImpactLevel   string   `json:"impactLevel"`
}

// extractModulesFromTruncatedJSON 从可能被截断的 JSON 中尽量提取完整的 module 对象。
// 逐个匹配 {...} 块并尝试解析，跳过解析失败的（通常是最后一个被截断的）。
func extractModulesFromTruncatedJSON(text string) []truncatedModuleJSON {
	var modules []truncatedModuleJSON
	// 在 affectedModules 数组内查找每个 {...} 对象
	idx := strings.Index(text, `"affectedModules"`)
	if idx < 0 {
		return nil
	}
	rest := text[idx:]

	depth := 0
	start := -1
	for i, ch := range rest {
		if ch == '{' {
			if depth == 0 {
				start = i
			}
			depth++
		} else if ch == '}' {
			depth--
			if depth == 0 && start >= 0 {
				objStr := rest[start : i+1]
				var mod truncatedModuleJSON
				if err := json.Unmarshal([]byte(objStr), &mod); err == nil && mod.Name != "" {
					modules = append(modules, mod)
				}
				start = -1
			}
		}
	}
	return modules
}

// printAffectedFiles 按模块打印受影响文件列表，限制条数避免终端输出过长。
func printAffectedFiles(files []string, max int) {
	if len(files) == 0 {
		return
	}
	limit := len(files)
	if limit > max {
		limit = max
	}
	for _, f := range files[:limit] {
		fmt.Printf("      - %s\n", color.HiBlackString(f))
	}
	if len(files) > max {
		fmt.Printf("      - %s\n", color.HiBlackString("... +%d more", len(files)-max))
	}
}

// --- 结果展示方法 ---

// FinalConclusion 显示最终审查结论。
// 使用绿色粗体的双线分隔框突出显示，并渲染 Markdown 格式的结论文本。
func (d *Display) FinalConclusion(text string) {
	d.SpinnerStop()

	color.New(color.FgGreen, color.Bold).Printf("\n%s\n", strings.Repeat("═", 50))
	color.New(color.FgGreen, color.Bold).Printf("  Final Conclusion\n")
	color.New(color.FgGreen, color.Bold).Printf("%s\n\n", strings.Repeat("═", 50))

	rendered := RenderTerminalMarkdown(text)
	fmt.Print(rendered)
}

// IssuesTable 以表格形式展示审查过程中发现的结构化问题。
// 按严重等级着色显示（critical=红色粗体, high=红色, medium=黄色, low=蓝色, nitpick=灰色），
// 包含问题标题、文件位置、提出者和修复建议（如果有）。
func (d *Display) IssuesTable(issues []orchestrator.MergedIssue) {
	totalRaw := 0
	for _, issue := range issues {
		totalRaw += len(issue.RaisedBy)
	}

	color.New(color.FgMagenta, color.Bold).Printf("\n%s\n", strings.Repeat("─", 50))
	color.New(color.FgMagenta, color.Bold).Printf("  Issues Found (%d unique, %d total across reviewers)\n", len(issues), totalRaw)
	color.New(color.FgMagenta, color.Bold).Printf("%s\n\n", strings.Repeat("─", 50))

	severityColor := map[string]func(string, ...interface{}) string{
		"critical": color.New(color.FgRed, color.Bold).Sprintf,
		"high":     color.RedString,
		"medium":   color.YellowString,
		"low":      color.BlueString,
		"nitpick":  color.HiBlackString,
	}

	for i, issue := range issues {
		colorFn, ok := severityColor[issue.Severity]
		if !ok {
			colorFn = color.WhiteString
		}

		location := issue.File
		if issue.Line != nil && *issue.Line > 0 {
			location = fmt.Sprintf("%s:%d", issue.File, *issue.Line)
		}

		reviewers := make([]string, len(issue.RaisedBy))
		for j, r := range issue.RaisedBy {
			reviewers[j] = color.CyanString(r)
		}

		fmt.Println(colorFn("  %2d. [%-8s] %s", i+1, strings.ToUpper(issue.Severity), issue.Title))
		fmt.Printf("      %s  [%s]\n", color.HiBlackString(location), strings.Join(reviewers, ", "))
		if issue.SuggestedFix != "" {
			fix := issue.SuggestedFix
			if len(fix) > 100 {
				fix = fix[:100] + "..."
			}
			color.Green("      Fix: %s", fix)
		}
		fmt.Println()
	}
}

// TokenUsage 展示 Token 使用量统计信息。
// 逐行显示每个审查者的输入/输出 Token 数，最后汇总显示总量和估算费用。
// 如果审查提前收敛，还会显示收敛轮次。
func (d *Display) TokenUsage(usage []orchestrator.TokenUsage, convergedAt *int) {
	fmt.Println(color.HiBlackString("\n%s", strings.Repeat("─", 50)))
	fmt.Println(color.HiBlackString("  Token Usage (Estimated)"))
	fmt.Println(color.HiBlackString("%s", strings.Repeat("─", 50)))

	var totalInput, totalOutput int
	var totalCost float64

	for _, u := range usage {
		totalInput += u.InputTokens
		totalOutput += u.OutputTokens
		totalCost += u.EstimatedCost

		pad := 12 - len(u.ReviewerID)
		if pad < 1 {
			pad = 1
		}
		fmt.Println(color.HiBlackString("  %s%s%8s in  %8s out",
			u.ReviewerID, strings.Repeat(" ", pad),
			formatNumber(u.InputTokens), formatNumber(u.OutputTokens)))
	}

	fmt.Println(color.HiBlackString("%s", strings.Repeat("─", 50)))
	color.Yellow("  Total%s%8s in  %8s out  ~$%.4f",
		strings.Repeat(" ", 6), formatNumber(totalInput), formatNumber(totalOutput), totalCost)

	if convergedAt != nil {
		color.Green("\n  ✓ Converged at round %d", *convergedAt)
	}
	fmt.Println()
}

// --- 辅助函数 ---

// formatParallelStatus 格式化并行审查的状态显示文本。
// 每个审查者用不同颜色标识状态：绿色=已完成（附耗时），黄色=思考中，灰色=等待中。
func formatParallelStatus(round int, statuses []orchestrator.ReviewerStatus) string {
	parts := make([]string, len(statuses))
	nowMillis := time.Now().UnixMilli()
	for i, s := range statuses {
		switch s.Status {
		case "done":
			parts[i] = color.GreenString("✓ %s", s.ReviewerID) +
				color.HiBlackString(" (%.1fs, %s/%s, ~$%.4f)",
					s.Duration,
					formatNumber(s.InputTokens),
					formatNumber(s.OutputTokens),
					s.EstimatedCost,
				)
		case "failed":
			parts[i] = color.RedString("✗ %s", s.ReviewerID) +
				color.HiBlackString(" (%.1fs)", s.Duration)
		case "thinking":
			elapsed := 0.0
			if s.StartTime > 0 {
				elapsed = float64(nowMillis-s.StartTime) / 1000.0
			}
			parts[i] = color.YellowString("⋯ %s", s.ReviewerID) +
				color.HiBlackString(" (%.1fs)", elapsed)
		default:
			parts[i] = color.HiBlackString("○ %s", s.ReviewerID)
		}
	}
	return fmt.Sprintf("Round %d: [%s]", round, strings.Join(parts, " | "))
}

func formatSummaryStatus(statuses []orchestrator.ReviewerStatus) string {
	parts := make([]string, len(statuses))
	nowMillis := time.Now().UnixMilli()
	for i, s := range statuses {
		switch s.Status {
		case "done":
			parts[i] = color.GreenString("✓ %s", s.ReviewerID) +
				color.HiBlackString(" (%.1fs, %s/%s, ~$%.4f)",
					s.Duration,
					formatNumber(s.InputTokens),
					formatNumber(s.OutputTokens),
					s.EstimatedCost,
				)
		case "failed":
			parts[i] = color.RedString("✗ %s", s.ReviewerID) +
				color.HiBlackString(" (%.1fs)", s.Duration)
		case "thinking":
			elapsed := 0.0
			if s.StartTime > 0 {
				elapsed = float64(nowMillis-s.StartTime) / 1000.0
			}
			parts[i] = color.YellowString("⋯ %s", s.ReviewerID) +
				color.HiBlackString(" (%.1fs)", elapsed)
		default:
			parts[i] = color.HiBlackString("○ %s", s.ReviewerID)
		}
	}
	return fmt.Sprintf("Reviewer summaries: [%s]", strings.Join(parts, " | "))
}

func (d *Display) printReviewerHeader(reviewerID string) {
	d.currentReviewer = reviewerID
	if reviewerID == "analyzer" {
		color.New(color.FgMagenta, color.Bold).Printf("\n%s\n", strings.Repeat("─", 50))
		color.New(color.FgMagenta, color.Bold).Printf("  Analysis\n")
		color.New(color.FgMagenta, color.Bold).Printf("%s\n\n", strings.Repeat("─", 50))
		return
	}
	if reviewerID == "summarizer" {
		color.New(color.FgGreen, color.Bold).Printf("\n%s\n", strings.Repeat("─", 50))
		color.New(color.FgGreen, color.Bold).Printf("  Summary Synthesizer\n")
		color.New(color.FgGreen, color.Bold).Printf("%s\n\n", strings.Repeat("─", 50))
		return
	}
	if strings.HasPrefix(reviewerID, "summary:") {
		summaryReviewer := strings.TrimPrefix(reviewerID, "summary:")
		color.New(color.FgMagenta, color.Bold).Printf("\n┌─ Summary · %s\n", summaryReviewer)
		color.Magenta("│")
		return
	}
	color.New(color.FgCyan, color.Bold).Printf("\n┌─ %s ", reviewerID)
	fmt.Printf("%s\n", color.HiBlackString("[Round %d/%d]", d.currentRound, d.maxRounds))
	color.Cyan("│")
}

func (d *Display) printPhaseHeader(phaseLabel, title, phaseID string) {
	if d.currentPhase == phaseID {
		return
	}
	d.currentPhase = phaseID
	d.currentReviewer = ""
	d.announcedReviewers = make(map[string]bool)

	fmt.Println()
	color.New(color.FgHiBlack, color.Bold).Printf("  %s", phaseLabel)
	if title != "" {
		fmt.Printf("  %s\n", color.WhiteString(title))
	} else {
		fmt.Println()
	}
	color.HiBlack("  %s", strings.Repeat("─", 50))
}

// formatNumber 将整数格式化为带千分位逗号的字符串。
// 例如：1234 -> "1,234"，1234567 -> "1,234,567"
func formatNumber(n int) string {
	if n < 1000 {
		return fmt.Sprintf("%d", n)
	}
	if n < 1000000 {
		return fmt.Sprintf("%d,%03d", n/1000, n%1000)
	}
	return fmt.Sprintf("%d,%03d,%03d", n/1000000, (n/1000)%1000, n%1000)
}

// coldJokes 是等待 AI 响应时显示的程序员冷笑话集合。
// 在旋转动画旁随机展示，缓解用户等待时的无聊感。
var coldJokes = []string{
	"Why do programmers confuse Halloween and Christmas? Because Oct 31 = Dec 25",
	`A SQL query walks into a bar, walks up to two tables and asks: "Can I join you?"`,
	"Why do programmers hate nature? It has too many bugs.",
	"There are only 10 types of people: those who understand binary and those who don't",
	"Why do Java developers wear glasses? Because they can't C#",
	"Why did the developer go broke? Because he used up all his cache.",
	"99 little bugs in the code, take one down, patch it around... 127 little bugs in the code.",
	"There's no place like 127.0.0.1",
	"Why did the functions stop calling each other? They had too many arguments.",
	"I would tell you a UDP joke, but you might not get it.",
	"How many programmers does it take to change a light bulb? None, that's a hardware problem.",
	"The best thing about a boolean is that even if you're wrong, you're only off by a bit.",
	"In order to understand recursion, you must first understand recursion.",
	"There are two hard things in computer science: cache invalidation, naming things, and off-by-one errors.",
	"What's the object-oriented way to become wealthy? Inheritance.",
	"Debugging: Being the detective in a crime movie where you are also the murderer.",
	"It works on my machine! Then we'll ship your machine.",
	"Copy-paste is not a design pattern.",
	"Real programmers count from 0.",
	`Git commit -m "fixed it for real this time"`,
}

// getRandomJoke 从冷笑话集合中随机选取一条返回。
func getRandomJoke() string {
	return coldJokes[rand.Intn(len(coldJokes))]
}

// ansiRe 匹配 ANSI 转义序列，用于计算可见字符长度。
var ansiRe = regexp.MustCompile(`\x1b\[[0-9;]*m`)

// visibleLen 返回去除 ANSI 转义序列后的可见字符数。
func visibleLen(s string) int {
	return utf8.RuneCountInString(ansiRe.ReplaceAllString(s, ""))
}

// buildSpinnerSuffix 构造 spinner 后缀文本，自动截断 joke 以确保总长度不超过终端宽度。
// prefix 是已含 ANSI 码的前半段文本（如 "  Analyzing changes... | "），joke 是纯文本。
func (d *Display) buildSpinnerSuffix(prefix string, joke string) string {
	// spinner 字符本身占 1 列
	available := d.termWidth - 1 - visibleLen(prefix)
	if available <= 3 {
		return prefix
	}
	jokeRunes := []rune(joke)
	if len(jokeRunes) > available {
		joke = string(jokeRunes[:available-3]) + "..."
	}
	return prefix + color.HiBlackString(joke)
}
