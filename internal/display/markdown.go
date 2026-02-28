package display

import (
	"fmt"
	"sort"
	"strings"

	"github.com/charmbracelet/glamour"
	"github.com/longyunfeigu/codedebate/internal/orchestrator"
)

// MessageForMarkdown 表示用于 Markdown 导出的辩论消息。
// 包含审查者标识和消息内容。
type MessageForMarkdown struct {
	ReviewerID string // 审查者标识
	Round      int    // 辩论轮次（0 表示未知，需要在导出时推断）
	Content    string // 消息内容
}

// SummaryForMarkdown 表示用于 Markdown 导出的审查者摘要。
type SummaryForMarkdown struct {
	ReviewerID string // 审查者标识
	Summary    string // 摘要内容
}

// MergedIssueForMarkdown 是 MergedIssue 的 Markdown 导出版本。
// 使用本地类型避免与 orchestrator 包的循环导入。
type MergedIssueForMarkdown struct {
	Severity     string   // 严重等级
	Title        string   // 问题标题
	File         string   // 问题所在文件
	Line         int      // 问题所在行号
	Description  string   // 问题描述
	SuggestedFix string   // 建议的修复方案
	RaisedBy     []string // 提出该问题的审查者列表
}

// DebateResultForMarkdown 包含生成 Markdown 报告所需的所有字段。
// 使用本地类型（而非直接引用 orchestrator 的类型）以避免循环导入。
// 这个结构体是将审查结果导出为 Markdown 文件的数据载体。
type DebateResultForMarkdown struct {
	PRNumber         string // PR 编号或标签（如 "Local Changes"）
	Analysis         string // 分析文本
	Context          *orchestrator.GatheredContext
	FinalConclusion  string                    // 最终结论
	Messages         []MessageForMarkdown      // 辩论消息列表
	Summaries        []SummaryForMarkdown      // 审查者摘要列表
	TokenUsage       []orchestrator.TokenUsage // Token 使用量统计
	ConvergedAtRound *int                      // 收敛轮次（nil 表示未收敛）
	ParsedIssues     []MergedIssueForMarkdown  // 结构化问题列表
}

// MarkdownOptions 控制 Markdown 导出的详细程度。
type MarkdownOptions struct {
	IncludeDebateTranscript bool // 是否导出按轮次分组的辩论附录
}

// FormatMarkdownFromResult 从 DebateResultForMarkdown 生成默认的 Markdown 审查报告。
// 默认报告优先展示交付结果，不包含完整辩论转录。
func FormatMarkdownFromResult(r *DebateResultForMarkdown) string {
	return FormatMarkdownFromResultWithOptions(r, MarkdownOptions{})
}

// FormatMarkdownFromResultWithOptions 从 DebateResultForMarkdown 生成 Markdown 审查报告。
// 默认结构优先服务阅读体验：最终结论、问题列表、分析、摘要，辩论转录仅在显式启用时作为附录输出。
func FormatMarkdownFromResultWithOptions(r *DebateResultForMarkdown, opts MarkdownOptions) string {
	var b strings.Builder

	isLocal := r.PRNumber == "Local Changes" || strings.HasPrefix(r.PRNumber, "Last Commit")
	if isLocal {
		fmt.Fprintf(&b, "# %s Review\n\n", r.PRNumber)
	} else {
		fmt.Fprintf(&b, "# Code Review: %s\n\n", r.PRNumber)
	}

	if strings.TrimSpace(r.FinalConclusion) != "" {
		fmt.Fprintf(&b, "## Final Conclusion\n\n%s\n\n", formatEmbeddedMarkdown(stripLeadingHeading(r.FinalConclusion, "final conclusion", "最终结论", "结论"), 3))
	}

	if context := normalizeGatheredContext(r.Context); context != nil {
		writeSystemContextSection(&b, context)
	}

	// Issues table
	if len(r.ParsedIssues) > 0 {
		fmt.Fprintf(&b, "## Issues (%d)\n\n", len(r.ParsedIssues))
		for i, issue := range r.ParsedIssues {
			location := issue.File
			if issue.Line > 0 {
				location = fmt.Sprintf("%s:%d", issue.File, issue.Line)
			}
			fmt.Fprintf(&b, "%d. **[%s]** %s\n", i+1, strings.ToUpper(issue.Severity), issue.Title)
			fmt.Fprintf(&b, "   - Location: `%s`\n", location)
			fmt.Fprintf(&b, "   - Found by: %s\n", strings.Join(issue.RaisedBy, ", "))
			if strings.TrimSpace(issue.Description) != "" {
				fmt.Fprintf(&b, "   - Why: %s\n", issue.Description)
			}
			if issue.SuggestedFix != "" {
				fmt.Fprintf(&b, "   - Fix: %s\n", issue.SuggestedFix)
			}
			fmt.Fprintf(&b, "\n")
		}
	}

	if strings.TrimSpace(r.Analysis) != "" {
		fmt.Fprintf(&b, "## Analysis\n\n%s\n\n", formatEmbeddedMarkdown(r.Analysis, 3))
	}

	if len(r.Summaries) > 0 {
		fmt.Fprintf(&b, "## Reviewer Summaries\n\n")
		for _, s := range r.Summaries {
			fmt.Fprintf(&b, "### %s\n\n%s\n\n", s.ReviewerID, formatEmbeddedMarkdown(s.Summary, 4))
		}
	}

	if opts.IncludeDebateTranscript {
		rounds := groupMessagesByRound(r.Messages)
		if len(rounds) > 0 {
			fmt.Fprintf(&b, "## Debate Appendix\n\n")
			for _, round := range rounds {
				fmt.Fprintf(&b, "### Round %d\n\n", round.number)
				for _, msg := range round.messages {
					content := sanitizeTranscriptContent(msg.Content)
					if content == "" {
						continue
					}
					fmt.Fprintf(&b, "#### %s\n\n%s\n\n", msg.ReviewerID, content)
				}
			}
		}
	}

	// Token usage
	if len(r.TokenUsage) > 0 {
		fmt.Fprintf(&b, "## Token Usage\n\n")
		fmt.Fprintf(&b, "| Reviewer | Input | Output |\n")
		fmt.Fprintf(&b, "|----------|------:|-------:|\n")
		var totalIn, totalOut int
		for _, u := range r.TokenUsage {
			totalIn += u.InputTokens
			totalOut += u.OutputTokens
			fmt.Fprintf(&b, "| %s | %s | %s |\n", u.ReviewerID, formatNumber(u.InputTokens), formatNumber(u.OutputTokens))
		}
		fmt.Fprintf(&b, "| **Total** | **%s** | **%s** |\n\n", formatNumber(totalIn), formatNumber(totalOut))

		if r.ConvergedAtRound != nil {
			fmt.Fprintf(&b, "Converged at round %d.\n", *r.ConvergedAtRound)
		}
	}

	return b.String()
}

type transcriptRound struct {
	number   int
	messages []MessageForMarkdown
}

func groupMessagesByRound(messages []MessageForMarkdown) []transcriptRound {
	normalized := normalizeMessageRounds(messages)
	byRound := make(map[int][]MessageForMarkdown)
	var order []int
	for _, msg := range normalized {
		if strings.TrimSpace(msg.Content) == "" {
			continue
		}
		if _, ok := byRound[msg.Round]; !ok {
			order = append(order, msg.Round)
		}
		byRound[msg.Round] = append(byRound[msg.Round], msg)
	}

	rounds := make([]transcriptRound, 0, len(order))
	for _, round := range order {
		rounds = append(rounds, transcriptRound{
			number:   round,
			messages: byRound[round],
		})
	}
	return rounds
}

func normalizeMessageRounds(messages []MessageForMarkdown) []MessageForMarkdown {
	normalized := make([]MessageForMarkdown, len(messages))
	copy(normalized, messages)

	seenByReviewer := make(map[string]int)
	for i, msg := range normalized {
		if msg.Round <= 0 {
			seenByReviewer[msg.ReviewerID]++
			normalized[i].Round = seenByReviewer[msg.ReviewerID]
			continue
		}
		if msg.Round > seenByReviewer[msg.ReviewerID] {
			seenByReviewer[msg.ReviewerID] = msg.Round
		}
	}

	return normalized
}

func sanitizeTranscriptContent(content string) string {
	lines := strings.Split(content, "\n")
	filtered := make([]string, 0, len(lines))
	lastBlank := false
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "[tool] ") {
			continue
		}
		if trimmed == "" {
			if lastBlank {
				continue
			}
			lastBlank = true
			filtered = append(filtered, "")
			continue
		}
		lastBlank = false
		filtered = append(filtered, line)
	}
	return formatEmbeddedMarkdown(strings.TrimSpace(strings.Join(filtered, "\n")), 5)
}

func normalizeGatheredContext(ctx *orchestrator.GatheredContext) *orchestrator.GatheredContext {
	if ctx == nil {
		return nil
	}

	normalized := *ctx
	if len(normalized.AffectedModules) == 0 && normalized.Summary != "" && looksLikeJSON(normalized.Summary) {
		if parsed := tryParseContextJSON(normalized.Summary); parsed != nil {
			if len(parsed.AffectedModules) > 0 {
				normalized.AffectedModules = parsed.AffectedModules
			}
			if parsed.Summary != "" {
				normalized.Summary = parsed.Summary
			}
		}
	}

	if len(normalized.AffectedModules) == 0 && len(normalized.RelatedPRs) == 0 && strings.TrimSpace(normalized.Summary) == "" {
		return nil
	}
	return &normalized
}

func writeSystemContextSection(b *strings.Builder, ctx *orchestrator.GatheredContext) {
	fmt.Fprintf(b, "## System Context\n\n")

	if len(ctx.AffectedModules) > 0 {
		fmt.Fprintf(b, "### Affected Modules\n\n")
		for _, mod := range ctx.AffectedModules {
			impact := mod.ImpactLevel
			if impact == "" {
				impact = "unknown"
			}
			fmt.Fprintf(b, "#### %s\n\n", mod.Name)
			if mod.Path != "" {
				fmt.Fprintf(b, "- Path: `%s`\n", mod.Path)
			}
			fmt.Fprintf(b, "- Impact: `%s`\n", impact)
			fmt.Fprintf(b, "- File Count: `%d`\n", len(mod.AffectedFiles))
			if len(mod.AffectedFiles) > 0 {
				fmt.Fprintf(b, "- Affected Files:\n")
				for _, file := range mod.AffectedFiles {
					fmt.Fprintf(b, "  - `%s`\n", file)
				}
			}
			fmt.Fprintf(b, "\n")
		}
	}

	if files := collectAffectedFiles(ctx.AffectedModules); len(files) > 0 {
		fmt.Fprintf(b, "### Affected Files\n\n")
		for _, file := range files {
			fmt.Fprintf(b, "- `%s`\n", file)
		}
		fmt.Fprintf(b, "\n")
	}

	if len(ctx.RelatedPRs) > 0 {
		fmt.Fprintf(b, "### Related Changes\n\n")
		for _, pr := range ctx.RelatedPRs {
			fmt.Fprintf(b, "- #%d: %s\n", pr.Number, pr.Title)
		}
		fmt.Fprintf(b, "\n")
	}

	if strings.TrimSpace(ctx.Summary) != "" {
		fmt.Fprintf(b, "### Context Summary\n\n%s\n\n", formatEmbeddedMarkdown(ctx.Summary, 4))
	}
}

func collectAffectedFiles(modules []orchestrator.AffectedModule) []string {
	if len(modules) == 0 {
		return nil
	}

	seen := make(map[string]struct{})
	var files []string
	for _, mod := range modules {
		for _, file := range mod.AffectedFiles {
			if file == "" {
				continue
			}
			if _, ok := seen[file]; ok {
				continue
			}
			seen[file] = struct{}{}
			files = append(files, file)
		}
	}
	sort.Strings(files)
	return files
}

func formatEmbeddedMarkdown(content string, minLevel int) string {
	return rebaseMarkdownHeadings(sanitizeReportNarrative(content), minLevel)
}

func stripLeadingHeading(content string, titles ...string) string {
	if strings.TrimSpace(content) == "" || len(titles) == 0 {
		return strings.TrimSpace(content)
	}

	allowed := make(map[string]struct{}, len(titles))
	for _, title := range titles {
		allowed[normalizeHeadingTitle(title)] = struct{}{}
	}

	lines := strings.Split(strings.TrimSpace(content), "\n")
	for i, line := range lines {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" {
			continue
		}
		_, title, ok := parseMarkdownHeading(trimmed)
		if !ok {
			return strings.TrimSpace(strings.Join(lines, "\n"))
		}
		if _, match := allowed[normalizeHeadingTitle(title)]; !match {
			return strings.TrimSpace(strings.Join(lines, "\n"))
		}
		return strings.TrimSpace(strings.Join(lines[i+1:], "\n"))
	}

	return strings.TrimSpace(content)
}

func sanitizeReportNarrative(content string) string {
	paragraphs := strings.Split(strings.TrimSpace(content), "\n\n")
	filtered := make([]string, 0, len(paragraphs))
	for _, paragraph := range paragraphs {
		if isFollowUpOfferParagraph(paragraph) {
			continue
		}
		filtered = append(filtered, paragraph)
	}
	return strings.TrimSpace(strings.Join(filtered, "\n\n"))
}

func normalizeHeadingTitle(title string) string {
	return strings.Join(strings.Fields(strings.ToLower(strings.TrimSpace(title))), " ")
}

func rebaseMarkdownHeadings(content string, minLevel int) string {
	if strings.TrimSpace(content) == "" {
		return ""
	}

	lines := strings.Split(content, "\n")
	inFence := false
	for i, line := range lines {
		trimmed := strings.TrimLeft(line, " \t")
		if strings.HasPrefix(trimmed, "```") {
			inFence = !inFence
			continue
		}
		if inFence {
			continue
		}
		level, title, ok := parseMarkdownHeading(trimmed)
		if !ok {
			continue
		}
		newLevel := level + 1
		if newLevel < minLevel {
			newLevel = minLevel
		}
		if newLevel > 6 {
			newLevel = 6
		}
		indent := line[:len(line)-len(trimmed)]
		lines[i] = indent + strings.Repeat("#", newLevel) + " " + title
	}

	return strings.TrimSpace(strings.Join(lines, "\n"))
}

func parseMarkdownHeading(line string) (level int, title string, ok bool) {
	if line == "" || line[0] != '#' {
		return 0, "", false
	}
	for level < len(line) && level < 6 && line[level] == '#' {
		level++
	}
	if level == 0 || level >= len(line) || line[level] != ' ' {
		return 0, "", false
	}
	return level, strings.TrimSpace(line[level+1:]), true
}

func isFollowUpOfferParagraph(paragraph string) bool {
	trimmed := strings.TrimSpace(paragraph)
	trimmed = strings.TrimLeft(trimmed, "-*0123456789. ")
	lower := strings.ToLower(trimmed)

	switch {
	case strings.HasPrefix(trimmed, "如果你希望"),
		strings.HasPrefix(trimmed, "如果需要我"),
		strings.HasPrefix(trimmed, "若你希望"),
		strings.HasPrefix(trimmed, "如需我"),
		strings.HasPrefix(lower, "if you want"),
		strings.HasPrefix(lower, "if you'd like"),
		strings.HasPrefix(lower, "if needed, i can"),
		strings.HasPrefix(lower, "i can also inspect"),
		strings.HasPrefix(lower, "i can also review"),
		strings.HasPrefix(lower, "i can inspect"),
		strings.HasPrefix(lower, "i can review"),
		strings.HasPrefix(lower, "i can open"):
		return true
	default:
		return false
	}
}

// FormatMarkdown 从 orchestrator 的 DebateResult 生成默认的 Markdown 报告。
func FormatMarkdown(result *orchestrator.DebateResult) string {
	return FormatMarkdownWithOptions(result, MarkdownOptions{})
}

// FormatMarkdownWithOptions 从 orchestrator 的 DebateResult 生成 Markdown 报告。
func FormatMarkdownWithOptions(result *orchestrator.DebateResult, opts MarkdownOptions) string {
	// 将 orchestrator 类型转换为本地 Markdown 导出类型
	messages := make([]MessageForMarkdown, len(result.Messages))
	for i, m := range result.Messages {
		messages[i] = MessageForMarkdown{ReviewerID: m.ReviewerID, Round: m.Round, Content: m.Content}
	}
	summaries := make([]SummaryForMarkdown, len(result.Summaries))
	for i, s := range result.Summaries {
		summaries[i] = SummaryForMarkdown{ReviewerID: s.ReviewerID, Summary: s.Summary}
	}
	issues := make([]MergedIssueForMarkdown, len(result.ParsedIssues))
	for i, iss := range result.ParsedIssues {
		line := 0
		if iss.Line != nil {
			line = *iss.Line
		}
		issues[i] = MergedIssueForMarkdown{
			Severity:     iss.Severity,
			Title:        iss.Title,
			File:         iss.File,
			Line:         line,
			Description:  iss.Description,
			SuggestedFix: iss.SuggestedFix,
			RaisedBy:     iss.RaisedBy,
		}
	}

	r := &DebateResultForMarkdown{
		PRNumber:         result.PRNumber,
		Analysis:         result.Analysis,
		Context:          result.Context,
		FinalConclusion:  result.FinalConclusion,
		Messages:         messages,
		Summaries:        summaries,
		TokenUsage:       result.TokenUsage,
		ConvergedAtRound: result.ConvergedAtRound,
		ParsedIssues:     issues,
	}
	return FormatMarkdownFromResultWithOptions(r, opts)
}

// RenderTerminalMarkdown 使用 glamour 库将 Markdown 文本渲染为终端友好的格式。
// 自动适配终端的颜色主题（深色/浅色），设置 120 字符的自动换行宽度。
// 如果 glamour 渲染失败（例如不支持的终端），则回退返回原始文本。
func RenderTerminalMarkdown(text string) string {
	r, err := glamour.NewTermRenderer(
		glamour.WithAutoStyle(),
		glamour.WithWordWrap(120),
	)
	if err != nil {
		return text
	}

	out, err := r.Render(text)
	if err != nil {
		return text
	}
	return out
}
