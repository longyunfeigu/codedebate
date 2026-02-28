package orchestrator

import (
	"fmt"
	"sort"
	"strings"
)

// maxLedgerSummaryIssues 限制 BuildSummary 输出的最大问题数量。
// 当活跃问题超过此阈值时，仅展示按严重程度排序后的前 N 条，
// 避免摘要过长导致 prompt 占用过多 token。
const maxLedgerSummaryIssues = 100

// IssueLedger 跟踪单个审查者（reviewer）在多轮审查中的所有问题记录。
// 每个审查者拥有独立的 ledger 实例，问题 ID 在该 ledger 内部自增分配。
// 它是多轮审查状态管理的核心数据结构：审查者每一轮产生的增量（delta）
// 都会通过 ApplyDelta 方法合入 ledger，最终通过 ToMergedIssues 或
// ToCanonicalInputs 输出给下游的合并/规范化流程。
type IssueLedger struct {
	// ReviewerID 标识此 ledger 所属的审查者，用于在多审查者合并时区分来源。
	ReviewerID string
	// Issues 以问题 ID 为键存储所有问题记录（包括已撤回的），
	// 使用 map 结构便于通过 ID 快速定位问题进行更新或撤回操作。
	Issues map[string]*LedgerIssue
	// nextID 是内部自增计数器，用于为新增问题生成唯一 ID（格式如 "I1", "I2"...）。
	// 不导出是因为 ID 分配逻辑完全由 ledger 内部控制，外部不应干预。
	nextID int
}

// LedgerIssue 表示 ledger 中的一条问题记录。
// 它记录了问题的完整生命周期信息：从哪一轮被发现（Round）、
// 最后一次被提及的轮次（LastRound）、当前状态（active/retracted），
// 以及所有提及该问题的历史记录（Mentions）。
type LedgerIssue struct {
	// ID 是问题在当前 ledger 中的唯一标识，格式为 "I{n}"，由 nextIssueID 生成。
	ID string
	// Status 表示问题的当前状态："active" 表示问题仍然存在，"retracted" 表示审查者撤回了该问题。
	Status string // active | retracted
	// Severity 表示问题的严重程度，取值为 critical/high/medium/low/nitpick，
	// 用于排序和展示优先级。
	Severity string
	// Category 表示问题的分类（如 security、performance、general 等），
	// 用于对问题进行归类展示和统计。
	Category string
	// File 表示问题所在的文件路径。
	File string
	// Line 表示问题所在的行号，使用指针类型是因为行号可选——
	// 有些问题是文件级别或项目级别的，不一定关联到具体行。
	Line *int
	// Title 是问题的简短标题，用于摘要表格展示。
	Title string
	// Description 是问题的详细描述，包含具体的代码分析和改进建议。
	Description string
	// SuggestedFix 是审查者建议的修复方案，可选字段。
	SuggestedFix string
	// Round 记录问题首次被发现的审查轮次，用于追溯问题来源。
	Round int
	// LastRound 记录问题最后一次被提及（包括更新、撤回）的轮次，
	// 用于判断问题是否在最新轮次中仍然被关注。
	LastRound int
	// Mentions 记录了该问题在各轮审查中的所有提及历史，
	// 包含每次提及时的审查者、轮次和状态变化，是问题生命周期的完整审计日志。
	Mentions []IssueMention
}

// NewIssueLedger 创建并返回一个新的 IssueLedger 实例。
//
// 参数:
//   - reviewerID: 此 ledger 所属审查者的唯一标识
//
// 返回:
//   - *IssueLedger: 初始化好的 ledger，Issues map 已就绪，ID 计数器从 1 开始
func NewIssueLedger(reviewerID string) *IssueLedger {
	return &IssueLedger{
		ReviewerID: reviewerID,
		Issues:     make(map[string]*LedgerIssue),
		nextID:     1,
	}
}

// nextIssueID 生成下一个问题 ID 并递增内部计数器。
// ID 格式为 "I{n}"（如 "I1"、"I2"），保证在同一个 ledger 内唯一。
//
// 返回:
//   - string: 格式为 "I{n}" 的问题 ID
func (l *IssueLedger) nextIssueID() string {
	id := fmt.Sprintf("I%d", l.nextID)
	l.nextID++
	return id
}

// ApplyDelta 将模型输出的结构化增量（delta）应用到 ledger 状态中。
// 这是 ledger 状态演进的核心方法，每一轮审查结束后被调用一次。
// delta 包含三类操作：新增问题（Add）、撤回问题（Retract）和更新问题（Update）。
//
// 参数:
//   - delta: 模型输出的结构化增量，包含本轮的问题变更。如果为 nil 则不做任何操作。
//   - round: 当前审查轮次编号，用于标记问题的时间线信息。
//
// 关键行为:
//   - Add: 为每个新问题分配唯一 ID，设置状态为 "active"，并创建首条提及记录。
//   - Retract: 将指定 ID 的问题标记为 "retracted"，并追加撤回提及记录。
//   - Update: 仅更新非 nil 的字段（部分更新语义），并追加更新提及记录。
func (l *IssueLedger) ApplyDelta(delta *StructurizeDelta, round int) {
	// nil 检查：ledger 或 delta 为空时直接返回，避免空指针异常
	if l == nil || delta == nil {
		return
	}

	// 处理新增问题：为每个问题分配 ledger 内部 ID 并初始化完整记录
	for _, add := range delta.Add {
		id := l.nextIssueID()
		// 分类为空时回退到 "general"，确保下游处理不会遇到空分类
		category := strings.TrimSpace(add.Category)
		if category == "" {
			category = "general"
		}
		l.Issues[id] = &LedgerIssue{
			ID:           id,
			Status:       "active",
			Severity:     add.Severity,
			Category:     category,
			File:         add.File,
			Line:         add.Line,
			Title:        add.Title,
			Description:  add.Description,
			SuggestedFix: add.SuggestedFix,
			Round:        round,
			LastRound:    round,
			// 创建首条提及记录，标记该问题由此审查者在此轮发现
			Mentions: []IssueMention{{
				ReviewerID:   l.ReviewerID,
				LocalIssueID: id,
				Round:        round,
				Status:       "active",
			}},
		}
	}

	// 处理撤回操作：审查者认为之前提出的问题不再成立（如误报或已修复）
	for _, id := range delta.Retract {
		issue, ok := l.Issues[id]
		if !ok {
			// 跳过不存在的 ID，容忍模型输出中可能出现的无效引用
			continue
		}
		issue.Status = "retracted"
		issue.LastRound = round
		// 追加撤回提及记录，保留完整的状态变更历史
		issue.Mentions = append(issue.Mentions, IssueMention{
			ReviewerID:   l.ReviewerID,
			LocalIssueID: id,
			Round:        round,
			Status:       "retracted",
		})
	}

	// 处理字段更新：使用指针语义实现部分更新，只修改模型明确指定要变更的字段
	for _, update := range delta.Update {
		issue, ok := l.Issues[update.ID]
		if !ok {
			// 跳过不存在的 ID，容忍模型输出中可能出现的无效引用
			continue
		}
		// 以下每个 nil 检查实现「仅更新显式提供的字段」语义，
		// 避免未提供的字段被零值覆盖
		if update.Severity != nil {
			issue.Severity = *update.Severity
		}
		if update.Category != nil {
			issue.Category = *update.Category
		}
		if update.File != nil {
			issue.File = *update.File
		}
		if update.Line != nil {
			issue.Line = update.Line
		}
		if update.Title != nil {
			issue.Title = *update.Title
		}
		if update.Description != nil {
			issue.Description = *update.Description
		}
		if update.SuggestedFix != nil {
			issue.SuggestedFix = *update.SuggestedFix
		}
		issue.LastRound = round
		// 追加更新提及记录，保留问题的完整变更轨迹
		issue.Mentions = append(issue.Mentions, IssueMention{
			ReviewerID:   l.ReviewerID,
			LocalIssueID: issue.ID,
			Round:        round,
			Status:       issue.Status,
		})
	}
}

// BuildSummary 返回当前 ledger 中所有活跃问题的紧凑 Markdown 表格摘要。
// 该摘要用于在多轮审查的 prompt 中向模型展示当前问题状态，
// 帮助模型在后续轮次中做出一致的判断。
//
// 返回:
//   - string: Markdown 格式的表格字符串。如果没有活跃问题则返回空字符串。
//
// 关键行为:
//   - 问题按严重程度排序（critical > high > medium > low > nitpick），
//     确保最重要的问题出现在前面。
//   - 超过 maxLedgerSummaryIssues 条时截断，并附加提示信息。
func (l *IssueLedger) BuildSummary() string {
	// nil 和空检查：无问题时直接返回空字符串，避免生成空表格
	if l == nil || len(l.Issues) == 0 {
		return ""
	}

	active := l.activeIssues()
	if len(active) == 0 {
		return ""
	}

	// 超过上限时截断，防止摘要过长挤占 prompt 空间
	truncated := false
	total := len(active)
	if len(active) > maxLedgerSummaryIssues {
		active = active[:maxLedgerSummaryIssues]
		truncated = true
	}

	// 构建 Markdown 表格，包含 ID、严重程度、文件位置和标题四列
	var b strings.Builder
	b.WriteString("| ID | Severity | File:Line | Title |\n")
	b.WriteString("|----|----------|-----------|-------|\n")
	for _, issue := range active {
		// 有行号时拼接 "file:line" 格式，无行号时仅显示文件路径
		fileLine := issue.File
		if issue.Line != nil {
			fileLine = fmt.Sprintf("%s:%d", issue.File, *issue.Line)
		}
		b.WriteString(fmt.Sprintf("| %s | %s | %s | %s |\n",
			issue.ID,
			issue.Severity,
			sanitizePipe(fileLine),
			sanitizePipe(issue.Title),
		))
	}

	// 附加统计信息：截断时告知总数，未截断时显示活跃问题总数
	if truncated {
		b.WriteString(fmt.Sprintf("(showing %d of %d active issues; truncated by severity)\n", len(active), total))
	} else {
		b.WriteString(fmt.Sprintf("(%d active issues)\n", total))
	}

	return b.String()
}

// ToMergedIssues 将 ledger 中的活跃问题转换为 MergedIssue 格式。
// 此方法用于将单个审查者的问题输出给多审查者合并流程，
// 仅包含活跃（未撤回）的问题。
//
// 返回:
//   - []MergedIssue: 活跃问题的 MergedIssue 列表。如果没有活跃问题则返回 nil。
//
// 关键行为:
//   - 每个问题的 RaisedBy、IntroducedBy、SupportedBy 均设为当前审查者，
//     因为单一 ledger 内的活跃问题天然由该审查者提出和支持。
//   - Mentions 使用切片复制而非直接引用，避免下游修改影响 ledger 内部状态。
func (l *IssueLedger) ToMergedIssues() []MergedIssue {
	if l == nil || len(l.Issues) == 0 {
		return nil
	}

	active := l.activeIssues()
	result := make([]MergedIssue, 0, len(active))
	for _, issue := range active {
		// 防御性处理：确保分类不为空
		category := issue.Category
		if strings.TrimSpace(category) == "" {
			category = "general"
		}
		result = append(result, MergedIssue{
			ReviewIssue: ReviewIssue{
				Severity:     issue.Severity,
				Category:     category,
				File:         issue.File,
				Line:         issue.Line,
				Title:        issue.Title,
				Description:  issue.Description,
				SuggestedFix: issue.SuggestedFix,
			},
			// 活跃问题：提出者、引入者、支持者均为当前审查者
			RaisedBy:     []string{l.ReviewerID},
			IntroducedBy: []string{l.ReviewerID},
			SupportedBy:  []string{l.ReviewerID},
			Descriptions: []string{issue.Description},
			// 使用 append([]IssueMention(nil), ...) 创建切片副本，
			// 避免与 ledger 内部共享底层数组导致数据竞争
			Mentions: append([]IssueMention(nil), issue.Mentions...),
		})
	}
	return result
}

// ToCanonicalInputs 将 ledger 中的所有问题（包括已撤回的）转换为规范化输入格式。
// 与 ToMergedIssues 不同，此方法包含撤回的问题，
// 因为规范化流程需要知道哪些问题被撤回，以便在多审查者合并时正确处理冲突。
//
// 返回:
//   - []MergedIssue: 所有问题的 MergedIssue 列表，按轮次和 ID 排序。
//     如果 ledger 为空则返回 nil。
//
// 关键行为:
//   - 已撤回的问题设置 WithdrawnBy（而非 SupportedBy 和 RaisedBy），
//     让规范化流程知道该审查者已不再支持此问题。
//   - 问题按首次出现的轮次排序，轮次相同时按 ID 排序，保证输出顺序稳定。
func (l *IssueLedger) ToCanonicalInputs() []MergedIssue {
	if l == nil || len(l.Issues) == 0 {
		return nil
	}

	// 将 map 中的问题收集到切片中以便排序
	issues := make([]*LedgerIssue, 0, len(l.Issues))
	for _, issue := range l.Issues {
		issues = append(issues, issue)
	}
	// 按轮次升序排列，同轮次内按 ID 升序，确保输出的确定性
	sort.Slice(issues, func(i, j int) bool {
		if issues[i].Round != issues[j].Round {
			return issues[i].Round < issues[j].Round
		}
		return issues[i].ID < issues[j].ID
	})

	result := make([]MergedIssue, 0, len(issues))
	for _, issue := range issues {
		// 防御性处理：确保分类不为空
		category := issue.Category
		if strings.TrimSpace(category) == "" {
			category = "general"
		}

		// 根据问题状态设置不同的归属列表：
		// 已撤回 -> 仅设 withdrawnBy，表示审查者不再坚持此问题
		// 活跃   -> 设 supportedBy 和 raisedBy，表示审查者支持此问题
		supportedBy := []string(nil)
		withdrawnBy := []string(nil)
		raisedBy := []string(nil)
		if issue.Status == "retracted" {
			withdrawnBy = []string{l.ReviewerID}
		} else {
			supportedBy = []string{l.ReviewerID}
			raisedBy = []string{l.ReviewerID}
		}

		result = append(result, MergedIssue{
			ReviewIssue: ReviewIssue{
				Severity:     issue.Severity,
				Category:     category,
				File:         issue.File,
				Line:         issue.Line,
				Title:        issue.Title,
				Description:  issue.Description,
				SuggestedFix: issue.SuggestedFix,
			},
			RaisedBy: raisedBy,
			// IntroducedBy 始终设为当前审查者，因为无论撤回与否，
			// 问题最初都是由该审查者引入的
			IntroducedBy: []string{l.ReviewerID},
			SupportedBy:  supportedBy,
			WithdrawnBy:  withdrawnBy,
			Descriptions: []string{issue.Description},
			// 使用 append([]IssueMention(nil), ...) 创建切片副本
			Mentions: append([]IssueMention(nil), issue.Mentions...),
		})
	}

	return result
}

// activeIssues 返回 ledger 中所有状态为活跃（非 retracted）的问题，
// 按严重程度、轮次和 ID 排序后返回。
//
// 返回:
//   - []*LedgerIssue: 排序后的活跃问题切片
//
// 排序规则（优先级从高到低）:
//   1. 严重程度：依据 severityOrder（critical=0 < high=1 < medium=2 < low=3 < nitpick=4），
//      数值越小越靠前，确保最严重的问题最先展示。
//   2. 轮次：早期发现的问题排在前面，体现"先发现先处理"的原则。
//   3. ID：同轮次同严重程度时按 ID 字典序排列，保证输出确定性。
func (l *IssueLedger) activeIssues() []*LedgerIssue {
	active := make([]*LedgerIssue, 0, len(l.Issues))
	for _, issue := range l.Issues {
		if issue.Status == "retracted" {
			continue
		}
		active = append(active, issue)
	}

	sort.Slice(active, func(i, j int) bool {
		si := severityOrder[active[i].Severity]
		sj := severityOrder[active[j].Severity]
		if si != sj {
			return si < sj
		}
		if active[i].Round != active[j].Round {
			return active[i].Round < active[j].Round
		}
		return active[i].ID < active[j].ID
	})

	return active
}

// sanitizePipe 清理字符串中可能破坏 Markdown 表格格式的字符。
// 管道符 "|" 是 Markdown 表格的列分隔符，必须转义；
// 换行符会破坏表格行结构，替换为空格保持单行。
//
// 参数:
//   - s: 需要清理的原始字符串
//
// 返回:
//   - string: 清理后可安全嵌入 Markdown 表格单元格的字符串
func sanitizePipe(s string) string {
	s = strings.ReplaceAll(s, "|", "\\|")
	s = strings.ReplaceAll(s, "\n", " ")
	return strings.TrimSpace(s)
}
