package orchestrator

import (
	"crypto/sha256"
	"fmt"
	"sort"
	"strings"
)

// CanonicalizeMergedIssues 将来自多个审查者的 issue 视图链接为"规范 issue"（canonical issues）。
//
// 在多审查者协作评审中，不同审查者可能独立发现同一问题并各自描述。
// 本函数负责将这些重复的 issue 合并为唯一的规范表示，同时保留比 RaisedBy 更丰富的归属信息
// （IntroducedBy、SupportedBy、WithdrawnBy、ContestedBy），最后再将 SupportedBy 投影回 RaisedBy
// 以保证对外接口的兼容性。
//
// 参数:
//   - issues: 来自各审查者的合并 issue 列表，可能包含对同一问题的重复描述
//
// 返回值:
//   - 去重合并后的规范 issue 列表，按严重程度降序、支持者数量降序、文件名和标题升序排列。
//     只保留至少有一个支持者的 issue（无人支持的 issue 被视为无效或已撤回）。
//
// 关键行为:
//  1. 先按"首次提及轮次 -> 严重程度 -> 文件 -> 标题"排序，确保更早、更严重的 issue 优先成为规范基准
//  2. 逐个尝试与已有规范 issue 匹配（基于文件+标题+描述的相似度），匹配成功则合并，否则新建
//  3. 最终过滤掉无支持者的 issue，并按展示优先级重新排序
func CanonicalizeMergedIssues(issues []MergedIssue) []MergedIssue {
	if len(issues) == 0 {
		return nil
	}

	// 创建副本避免修改调用方的原始切片
	ordered := append([]MergedIssue(nil), issues...)
	// 按"首次提及轮次 -> 严重程度 -> 文件名 -> 标题"排序，
	// 这样越早被提出、越严重的 issue 会先进入 canonical 列表，成为后续匹配的基准。
	// 使用 SliceStable 保持相同排序键的元素保持原有相对顺序，确保结果确定性。
	sort.SliceStable(ordered, func(i, j int) bool {
		ri := firstMentionRound(ordered[i])
		rj := firstMentionRound(ordered[j])
		if ri != rj {
			return ri < rj
		}
		si := severityOrder[ordered[i].Severity]
		sj := severityOrder[ordered[j].Severity]
		if si != sj {
			return si < sj
		}
		if ordered[i].File != ordered[j].File {
			return ordered[i].File < ordered[j].File
		}
		return ordered[i].Title < ordered[j].Title
	})

	// 贪心匹配阶段：遍历排序后的 issue，每个 issue 尝试匹配已有的 canonical issue。
	// 选择得分最高的匹配目标进行合并；若无匹配则作为新的 canonical issue 加入。
	var canonical []MergedIssue
	for _, issue := range ordered {
		normalized := normalizeMergedIssue(issue)
		bestIdx := -1
		bestScore := 0.0
		for i := range canonical {
			score, ok := canonicalMatchScore(canonical[i].ReviewIssue, normalized.ReviewIssue)
			if ok && score > bestScore {
				bestScore = score
				bestIdx = i
			}
		}
		// 未找到匹配的 canonical issue，当前 issue 自身成为新的规范 issue
		if bestIdx < 0 {
			canonical = append(canonical, normalized)
			continue
		}
		// 找到最佳匹配，将当前 issue 的信息合并到该 canonical issue 中
		mergeCanonicalIssue(&canonical[bestIdx], normalized)
	}

	// 最终化阶段：对每个 canonical issue 进行归属信息清理，
	// 并过滤掉没有任何支持者的 issue（这些 issue 可能已被全部撤回或质疑）
	final := make([]MergedIssue, 0, len(canonical))
	for _, issue := range canonical {
		finalized := finalizeCanonicalIssue(issue)
		// 没有支持者的 issue 不进入最终结果——
		// 这是核心过滤逻辑：只有被至少一个审查者支持的问题才值得展示
		if len(finalized.SupportedBy) == 0 {
			continue
		}
		final = append(final, finalized)
	}

	// 按展示优先级重新排序：严重程度 > 支持者数量（多人共识的 issue 更重要）> 文件名 > 标题
	sort.SliceStable(final, func(i, j int) bool {
		si := severityOrder[final[i].Severity]
		sj := severityOrder[final[j].Severity]
		if si != sj {
			return si < sj
		}
		// 支持者越多排越前面——多人独立发现同一问题意味着该问题更值得关注
		if len(final[i].SupportedBy) != len(final[j].SupportedBy) {
			return len(final[i].SupportedBy) > len(final[j].SupportedBy)
		}
		if final[i].File != final[j].File {
			return final[i].File < final[j].File
		}
		return final[i].Title < final[j].Title
	})

	return final
}

// ApplyCanonicalSignals 将审查者的显式态度信号（support/withdraw/contest）应用到已有的规范 issue 上。
//
// 在多轮审查中，审查者可以对已存在的 issue 表达新的态度：支持、撤回或质疑。
// 本函数负责将这些信号准确地应用到对应的 issue 上，更新其归属状态。
//
// 参数:
//   - issues: 当前的规范 issue 列表
//   - signals: 审查者的态度信号列表，每个信号通过 IssueRef 引用目标 issue
//
// 返回值:
//   - 应用信号后的 issue 列表，已过滤掉无支持者的 issue
//
// 关键行为:
//  1. 信号按"轮次 -> 审查者ID -> IssueRef -> Action"排序，保证应用顺序确定性
//  2. 每个信号只匹配第一个包含该 IssueRef 的 issue（break 语义）
//  3. 应用完所有信号后重新 finalize 并过滤无支持者的 issue
func ApplyCanonicalSignals(issues []MergedIssue, signals []CanonicalSignal) []MergedIssue {
	if len(issues) == 0 || len(signals) == 0 {
		return issues
	}

	// 创建副本，避免修改调用方的原始数据
	result := append([]MergedIssue(nil), issues...)
	signals = append([]CanonicalSignal(nil), signals...)
	// 对信号排序以保证应用顺序的确定性——
	// 同一轮次内按审查者和引用排序，确保不同运行得到一致结果
	sort.SliceStable(signals, func(i, j int) bool {
		if signals[i].Round != signals[j].Round {
			return signals[i].Round < signals[j].Round
		}
		if signals[i].ReviewerID != signals[j].ReviewerID {
			return signals[i].ReviewerID < signals[j].ReviewerID
		}
		if signals[i].IssueRef != signals[j].IssueRef {
			return signals[i].IssueRef < signals[j].IssueRef
		}
		return signals[i].Action < signals[j].Action
	})

	// 逐个信号匹配并应用到对应的 issue 上
	for si := range signals {
		for i := range result {
			if !issueContainsRef(result[i], signals[si].IssueRef) {
				continue
			}
			applySignalToIssue(&result[i], signals[si])
			// 只应用到第一个匹配的 issue 上，因为 IssueRef 应该是唯一的
			break
		}
	}

	// 重新 finalize 所有 issue，确保归属关系一致性
	for i := range result {
		result[i] = finalizeCanonicalIssue(result[i])
	}

	// 复用底层数组进行原地过滤，过滤掉已无支持者的 issue
	filtered := result[:0]
	for _, issue := range result {
		if len(issue.SupportedBy) == 0 {
			continue
		}
		filtered = append(filtered, issue)
	}
	return filtered
}

// BuildCanonicalIssueSummary 将规范 issue 列表格式化为 Markdown 表格字符串。
//
// 生成的表格包含：规范ID、严重程度、文件行号、标题、支持者、撤回者、质疑者、Issue 引用。
// 主要用于生成面向用户或下游系统的可读摘要。
//
// 参数:
//   - issues: 规范 issue 列表
//
// 返回值:
//   - Markdown 格式的表格字符串；若 issues 为空则返回空字符串
func BuildCanonicalIssueSummary(issues []MergedIssue) string {
	if len(issues) == 0 {
		return ""
	}

	var b strings.Builder
	// 输出 Markdown 表头
	b.WriteString("| Canonical ID | Severity | File:Line | Title | Supporters | Withdrawn | Contested | Issue Refs |\n")
	b.WriteString("|--------------|----------|-----------|-------|------------|-----------|-----------|------------|\n")
	for _, issue := range issues {
		// 对每个字段调用 sanitizePipe 转义管道符，防止破坏 Markdown 表格结构
		b.WriteString(fmt.Sprintf(
			"| %s | %s | %s | %s | %s | %s | %s | %s |\n",
			sanitizePipe(issue.CanonicalID),
			sanitizePipe(issue.Severity),
			sanitizePipe(issueFileLine(issue)),
			sanitizePipe(issue.Title),
			sanitizePipe(strings.Join(issue.SupportedBy, ", ")),
			sanitizePipe(strings.Join(issue.WithdrawnBy, ", ")),
			sanitizePipe(strings.Join(issue.ContestedBy, ", ")),
			sanitizePipe(strings.Join(issueRefs(issue), ", ")),
		))
	}
	return b.String()
}

// canonicalMatchScore 计算两个 ReviewIssue 之间的匹配得分，判断它们是否描述同一个问题。
//
// 这是规范化合并的核心匹配算法，基于"文件相同 + 标题/描述文本相似度 + 行号兼容性"
// 综合判断。算法设计意图是在精确率和召回率之间取得平衡：既不遗漏真正的重复问题，
// 也不把不同问题错误合并。
//
// 参数:
//   - a, b: 待比较的两个 ReviewIssue
//
// 返回值:
//   - float64: 匹配得分（越高越相似），仅在 ok=true 时有意义
//   - bool: 是否满足匹配条件（false 表示两者不应被视为同一问题）
//
// 匹配策略:
//   - 文件不同直接判定不匹配（不同文件的问题几乎不可能是同一个）
//   - 使用 Jaccard 相似度衡量标题和描述的词汇重叠程度
//   - 总分 = 标题相似度*0.6 + 描述相似度*0.35 + 分类奖励0.05
//     标题权重高于描述，因为标题是对问题的精炼概括，区分度更高
//   - 提供多组阈值组合，适应不同场景（标题完全相同、标题高度相似、描述高度相似等）
func canonicalMatchScore(a, b ReviewIssue) (float64, bool) {
	// 文件不同则直接不匹配——不同文件中的相似描述通常是不同问题
	if a.File != b.File {
		return 0, false
	}

	// 对标题和描述进行分词、转小写、过滤停用词，提取有语义意义的词汇
	titleA := filterStopWords(tokenize(strings.ToLower(a.Title)))
	titleB := filterStopWords(tokenize(strings.ToLower(b.Title)))
	// 描述只取前60个词，避免长描述中的无关内容干扰相似度计算
	descA := filterStopWords(firstN(tokenize(strings.ToLower(a.Description)), 60))
	descB := filterStopWords(firstN(tokenize(strings.ToLower(b.Description)), 60))

	// 使用 Jaccard 相似度（交集/并集）衡量词汇重叠程度
	titleSim := jaccardSimilarity(titleA, titleB)
	descSim := jaccardSimilarity(descA, descB)
	// 行号兼容性检查：如果两个 issue 指向的代码行范围差异太大且文本相似度不够高，
	// 则认为它们虽然描述类似但指向不同的代码位置，不应合并
	lineCompatible := canonicalLineCompatible(&a, &b, titleSim, descSim)
	if !lineCompatible {
		return 0, false
	}

	// 分类奖励：如果两个 issue 属于同一分类（如 "security"、"performance"），
	// 给予 0.05 的额外加分。较小的奖励值是因为分类本身不够精确，只作为辅助信号。
	categoryBonus := 0.0
	if strings.TrimSpace(a.Category) != "" && a.Category == b.Category {
		categoryBonus = 0.05
	}
	// 综合得分：标题占 60%，描述占 35%，分类占 5%。
	// 标题权重最高是因为审查者对同一问题的标题往往高度一致，而描述可能有较大差异。
	score := titleSim*0.6 + descSim*0.35 + categoryBonus

	// 多组阈值组合，每组对应一种典型的匹配场景：
	switch {
	// 场景1: 标题标准化后完全相同 + 描述有最低相似度（>=0.12）
	// 标题完全相同是强信号，只需描述有微弱关联即可确认。
	// 额外加 0.15 分作为"标题完全匹配"的奖励，提高其在多候选匹配中的优先级。
	case normalizedIssueTitle(a.Title) == normalizedIssueTitle(b.Title) && descSim >= 0.12:
		return score + 0.15, true
	// 场景2: 标题较高相似度（>=0.58）+ 描述一定相似度（>=0.18）
	// 平衡匹配：标题和描述都有合理的重叠
	case titleSim >= 0.58 && descSim >= 0.18:
		return score, true
	// 场景3: 标题中等相似度（>=0.45）+ 描述高相似度（>=0.34）
	// 允许标题差异较大但描述高度相似的情况——不同审查者可能用不同的标题描述同一问题
	case titleSim >= 0.45 && descSim >= 0.34:
		return score, true
	// 场景4: 标题高度相似（>=0.75）+ 描述有最低相似度（>=0.10）
	// 标题非常相似时，对描述的要求放宽——可能是简短描述或不同角度的阐述
	case titleSim >= 0.75 && descSim >= 0.10:
		return score, true
	default:
		// 所有阈值组合均不满足，判定为不匹配
		return 0, false
	}
}

// canonicalLineCompatible 判断两个 issue 的行号范围是否兼容（即是否可能指向同一段代码）。
//
// 行号兼容性是匹配判断的重要维度：即使标题和描述相似，如果指向完全不同的代码区域，
// 也很可能是不同的问题。但由于不同审查者可能对同一问题标注略有不同的行号范围，
// 需要允许一定的容差。
//
// 参数:
//   - a, b: 待比较的两个 ReviewIssue 指针
//   - titleSim: 标题的 Jaccard 相似度
//   - descSim: 描述的 Jaccard 相似度
//
// 返回值:
//   - bool: 行号是否兼容
//
// 兼容规则:
//   - 双方都没有行号：退化为纯文本匹配，要求标题或描述有一定相似度
//   - 只有一方有行号：要求更高的文本相似度（标题>=0.78 且 描述>=0.18）才允许匹配
//   - 双方都有行号：行范围重叠或间隔不超过 8 行则兼容；否则要求极高的标题相似度
func canonicalLineCompatible(a, b *ReviewIssue, titleSim, descSim float64) bool {
	// 双方都没有行号信息（文件级别的 issue），
	// 只能依赖文本相似度来判断，阈值相对宽松
	if a.Line == nil && b.Line == nil {
		return titleSim >= 0.45 || descSim >= 0.35
	}
	// 只有一方有行号——信息不对称，需要更严格的文本相似度来弥补行号信息的缺失。
	// 标题 0.78 是较高的阈值，避免因为行号信息缺失导致错误合并。
	if a.Line == nil || b.Line == nil {
		return titleSim >= 0.78 && descSim >= 0.18
	}

	// 双方都有行号，计算行范围
	aStart, aEnd := issueLineRange(a)
	bStart, bEnd := issueLineRange(b)
	// 允许 8 行的容差：不同审查者对同一问题可能标注略有不同的起止行。
	// 8 行的容差足以覆盖一个函数签名或一小段逻辑块的差异，
	// 同时不至于将相距较远的不同问题错误关联。
	if aStart <= bEnd+8 && bStart <= aEnd+8 {
		return true
	}

	// 行号不兼容但标题极度相似（>=0.88）：可能是同一问题但审查者标注了不同位置
	// （例如问题定义处和使用处），仍然允许匹配
	if titleSim >= 0.88 && descSim >= 0.15 {
		return true
	}
	return false
}

// mergeCanonicalIssue 将源 issue (src) 的信息合并到目标 canonical issue (dst) 中。
//
// 合并策略优先保留更严重的 issue 的核心字段（标题、描述、严重程度等），
// 同时累积所有归属信息（支持者、撤回者等）。
//
// 参数:
//   - dst: 目标 canonical issue（会被就地修改）
//   - src: 要合并进来的 issue
//
// 合并规则:
//   - 若 src 更严重，用 src 的 ReviewIssue 整体替换 dst 的（保留更好的问题描述）
//   - 若 dst 更严重或同级，仅补充 dst 缺失的行号、修复建议和分类
//   - 归属列表始终累加（去重在 normalize/finalize 阶段处理）
func mergeCanonicalIssue(dst *MergedIssue, src MergedIssue) {
	// 比较严重程度：severityOrder 值越小越严重
	if severityOrder[src.Severity] < severityOrder[dst.Severity] {
		// src 更严重，整体替换核心 ReviewIssue 字段。
		// 这样做的原因是：更严重的 issue 通常包含更详细、更准确的问题描述。
		dst.ReviewIssue = src.ReviewIssue
	} else {
		// dst 已经是更严重或同级的版本，只从 src 中补充 dst 缺失的信息
		if dst.Line == nil && src.Line != nil {
			dst.Line = src.Line
		}
		if dst.SuggestedFix == "" && src.SuggestedFix != "" {
			dst.SuggestedFix = src.SuggestedFix
		}
		if strings.TrimSpace(dst.Category) == "" && strings.TrimSpace(src.Category) != "" {
			dst.Category = src.Category
		}
	}

	// 归属信息始终累加合并——后续的 normalize/finalize 步骤会负责去重和排序
	dst.Descriptions = append(dst.Descriptions, src.Descriptions...)
	dst.IntroducedBy = append(dst.IntroducedBy, src.IntroducedBy...)
	dst.SupportedBy = append(dst.SupportedBy, src.SupportedBy...)
	dst.WithdrawnBy = append(dst.WithdrawnBy, src.WithdrawnBy...)
	dst.ContestedBy = append(dst.ContestedBy, src.ContestedBy...)
	dst.Mentions = append(dst.Mentions, src.Mentions...)
}

// applySignalToIssue 将单个审查者的态度信号应用到指定 issue 上。
//
// 信号是审查者在后续轮次中对已有 issue 的显式态度变更。
// 每种操作都会同时更新多个归属列表，确保状态一致性。
//
// 参数:
//   - issue: 要更新的 issue（会被就地修改）
//   - signal: 审查者的态度信号
//
// 操作语义:
//   - support: 支持该 issue（加入 SupportedBy/RaisedBy，从 WithdrawnBy/ContestedBy 移除）
//   - withdraw: 撤回对该 issue 的支持（从 SupportedBy/RaisedBy 移除，加入 WithdrawnBy）
//   - contest: 质疑该 issue（从 SupportedBy/RaisedBy 移除，加入 ContestedBy）
func applySignalToIssue(issue *MergedIssue, signal CanonicalSignal) {
	switch signal.Action {
	case "support":
		// 支持操作：该审查者认为这个问题确实存在
		issue.SupportedBy = append(issue.SupportedBy, signal.ReviewerID)
		issue.RaisedBy = append(issue.RaisedBy, signal.ReviewerID)
		// 支持与撤回/质疑互斥，移除之前可能存在的撤回或质疑状态
		issue.WithdrawnBy = removeString(issue.WithdrawnBy, signal.ReviewerID)
		issue.ContestedBy = removeString(issue.ContestedBy, signal.ReviewerID)
	case "withdraw":
		// 撤回操作：该审查者不再认为这个问题值得关注
		issue.SupportedBy = removeString(issue.SupportedBy, signal.ReviewerID)
		issue.RaisedBy = removeString(issue.RaisedBy, signal.ReviewerID)
		issue.WithdrawnBy = append(issue.WithdrawnBy, signal.ReviewerID)
		issue.ContestedBy = removeString(issue.ContestedBy, signal.ReviewerID)
	case "contest":
		// 质疑操作：该审查者认为这个问题描述不准确或不是真正的问题
		issue.SupportedBy = removeString(issue.SupportedBy, signal.ReviewerID)
		issue.RaisedBy = removeString(issue.RaisedBy, signal.ReviewerID)
		issue.WithdrawnBy = removeString(issue.WithdrawnBy, signal.ReviewerID)
		issue.ContestedBy = append(issue.ContestedBy, signal.ReviewerID)
	default:
		// 未知操作类型，忽略
		return
	}
	// 无论何种操作，都记录一条 Mention 以保留完整的审查历史轨迹
	issue.Mentions = append(issue.Mentions, IssueMention{
		ReviewerID: signal.ReviewerID,
		Round:      signal.Round,
		Status:     signal.Action,
	})
}

// normalizeMergedIssue 对 MergedIssue 的所有列表字段进行去重和排序标准化。
//
// 这是一个幂等操作，可以在任何时候安全调用。它确保所有归属列表中
// 不含重复项且已排序，同时处理 SupportedBy/RaisedBy/IntroducedBy 之间的推导关系。
//
// 参数:
//   - issue: 要标准化的 MergedIssue（值传递，不修改原始对象）
//
// 返回值:
//   - 标准化后的 MergedIssue
//
// 推导逻辑:
//   - 若 SupportedBy 为空但 RaisedBy 有值，将 RaisedBy 复制到 SupportedBy
//     （向后兼容：旧数据只有 RaisedBy 没有 SupportedBy）
//   - 若 IntroducedBy 为空，从 SupportedBy 推导
//     （未记录谁首次引入时，默认所有支持者都是引入者）
//   - RaisedBy 始终等于 SupportedBy（对外暴露统一的"提出者"语义）
func normalizeMergedIssue(issue MergedIssue) MergedIssue {
	issue.Descriptions = uniqueStrings(issue.Descriptions)
	issue.IntroducedBy = uniqueSorted(issue.IntroducedBy)
	issue.SupportedBy = uniqueSorted(issue.SupportedBy)
	issue.WithdrawnBy = uniqueSorted(issue.WithdrawnBy)
	issue.ContestedBy = uniqueSorted(issue.ContestedBy)
	issue.Mentions = uniqueMentions(issue.Mentions)

	// 向后兼容：旧流程可能只填充了 RaisedBy 而没有 SupportedBy
	if len(issue.SupportedBy) == 0 && len(issue.RaisedBy) > 0 {
		issue.SupportedBy = uniqueSorted(issue.RaisedBy)
	}
	// 如果没有明确的"引入者"信息，退化为"所有支持者都是引入者"
	if len(issue.IntroducedBy) == 0 {
		issue.IntroducedBy = uniqueSorted(issue.SupportedBy)
	}
	// RaisedBy 始终投影自 SupportedBy，保证对外接口的一致性
	issue.RaisedBy = uniqueSorted(issue.SupportedBy)
	return issue
}

// finalizeCanonicalIssue 对 canonical issue 进行最终处理，生成规范ID并精确计算归属关系。
//
// 与 normalizeMergedIssue 不同，本函数额外处理了：
//   - 基于 Mentions 历史精确计算 IntroducedBy（找到最早轮次的审查者）
//   - 从 WithdrawnBy 中排除仍在 SupportedBy 中的审查者（避免矛盾状态）
//   - 生成基于内容哈希的 CanonicalID
//
// 参数:
//   - issue: 要最终化的 MergedIssue（值传递）
//
// 返回值:
//   - 最终化后的 MergedIssue，包含正确的 CanonicalID 和清理后的归属列表
func finalizeCanonicalIssue(issue MergedIssue) MergedIssue {
	// 先进行基础标准化
	issue = normalizeMergedIssue(issue)

	// 从 Mentions 历史中精确计算 IntroducedBy：
	// 找到最早提及轮次，将该轮次的所有审查者标记为"引入者"
	if len(issue.Mentions) > 0 {
		earliest := firstMentionRound(issue)
		introduced := make([]string, 0, len(issue.Mentions))
		for _, mention := range issue.Mentions {
			if mention.Round == earliest {
				introduced = append(introduced, mention.ReviewerID)
			}
		}
		if len(introduced) > 0 {
			issue.IntroducedBy = uniqueSorted(introduced)
		}
	}

	// 兜底：如果仍然没有 IntroducedBy 信息，使用 SupportedBy
	if len(issue.IntroducedBy) == 0 {
		issue.IntroducedBy = uniqueSorted(issue.SupportedBy)
	}

	// 清理矛盾状态：如果某审查者同时出现在 WithdrawnBy 和 SupportedBy 中，
	// 说明其后来又重新支持了该 issue，应从 WithdrawnBy 中移除
	issue.WithdrawnBy = difference(uniqueSorted(issue.WithdrawnBy), uniqueSorted(issue.SupportedBy))
	issue.RaisedBy = uniqueSorted(issue.SupportedBy)
	// 基于 issue 核心内容生成确定性的哈希ID，用于跨轮次引用同一 issue
	issue.CanonicalID = buildCanonicalID(issue.ReviewIssue)
	issue.Descriptions = uniqueStrings(issue.Descriptions)
	issue.Mentions = uniqueMentions(issue.Mentions)
	return issue
}

// buildCanonicalID 根据 ReviewIssue 的核心内容生成一个确定性的哈希标识符。
//
// ID 基于文件名、分类、标准化标题和截断后的标准化描述的 SHA-256 哈希前 8 字节。
// 设计意图是：对于描述同一问题的不同表述，在标准化后能生成相同的 ID，
// 从而实现跨轮次、跨审查者的 issue 引用和追踪。
//
// 参数:
//   - issue: 用于生成 ID 的 ReviewIssue
//
// 返回值:
//   - 16 字符的十六进制字符串（SHA-256 前 8 字节）
func buildCanonicalID(issue ReviewIssue) string {
	// 将文件、分类、标准化标题和截断描述用管道符连接作为哈希输入。
	// 所有字段都经过 ToLower 和 TrimSpace 处理，确保大小写和空格差异不影响 ID 生成。
	key := strings.Join([]string{
		strings.TrimSpace(strings.ToLower(issue.File)),
		strings.TrimSpace(strings.ToLower(issue.Category)),
		normalizedIssueTitle(issue.Title),
		// 描述截断到 160 字符：既保留足够的区分度，又避免描述末尾的细微差异影响 ID
		truncateCanonicalText(normalizedIssueDescription(issue.Description), 160),
	}, "|")
	sum := sha256.Sum256([]byte(key))
	// 取前 8 字节（64 位），碰撞概率极低，同时保持 ID 简短易读
	return fmt.Sprintf("%x", sum[:8])
}

// normalizedIssueTitle 将标题标准化为去停用词、小写、空格分隔的词序列。
//
// 标准化后的标题用于"精确标题匹配"判断和哈希 ID 生成，
// 消除了大小写、标点和常见停用词的干扰。
//
// 参数:
//   - s: 原始标题字符串
//
// 返回值:
//   - 标准化后的标题字符串
func normalizedIssueTitle(s string) string {
	return strings.Join(filterStopWords(tokenize(strings.ToLower(s))), " ")
}

// normalizedIssueDescription 将描述标准化为去停用词、小写、截断的词序列。
//
// 只取前 60 个词，因为描述的开头通常包含最关键的信息，
// 后面可能是冗长的解释或代码片段，这些内容会降低相似度计算的准确性。
//
// 参数:
//   - s: 原始描述字符串
//
// 返回值:
//   - 标准化后的描述字符串
func normalizedIssueDescription(s string) string {
	return strings.Join(filterStopWords(firstN(tokenize(strings.ToLower(s)), 60)), " ")
}

// truncateCanonicalText 将字符串截断到指定的最大 rune 数量。
//
// 使用 rune 而非 byte 来截断，正确处理多字节 Unicode 字符（如中文）。
//
// 参数:
//   - s: 待截断的字符串
//   - max: 最大 rune 数量；若 <=0 则返回空字符串
//
// 返回值:
//   - 截断后的字符串；若原字符串长度不超过 max 则原样返回
func truncateCanonicalText(s string, max int) string {
	if max <= 0 {
		return ""
	}
	runes := []rune(s)
	if len(runes) <= max {
		return s
	}
	return string(runes[:max])
}

// issueLineRange 提取 ReviewIssue 的行范围（起始行和结束行）。
//
// 参数:
//   - issue: ReviewIssue 指针
//
// 返回值:
//   - start: 起始行号；若 Line 为 nil 则返回 0
//   - end: 结束行号；若 EndLine 为 nil 或小于 start 则等于 start
func issueLineRange(issue *ReviewIssue) (int, int) {
	if issue.Line == nil {
		return 0, 0
	}
	start := *issue.Line
	end := start
	// 若有明确的 EndLine 且合法（>= start），使用它；否则视为单行 issue
	if issue.EndLine != nil && *issue.EndLine >= start {
		end = *issue.EndLine
	}
	return start, end
}

// firstMentionRound 返回 issue 被首次提及的轮次号。
//
// 用于确定 issue 的"引入时间"，在排序和 IntroducedBy 计算中使用。
// 轮次号为 0 被视为无效值，会被跳过。
//
// 参数:
//   - issue: 包含 Mentions 历史的 MergedIssue
//
// 返回值:
//   - 最早的有效轮次号；若无 Mentions 则返回 0
func firstMentionRound(issue MergedIssue) int {
	if len(issue.Mentions) == 0 {
		return 0
	}
	minRound := issue.Mentions[0].Round
	for _, mention := range issue.Mentions[1:] {
		// 跳过轮次为 0 的无效记录，取最小的正整数轮次
		if minRound == 0 || (mention.Round > 0 && mention.Round < minRound) {
			minRound = mention.Round
		}
	}
	return minRound
}

// issueFileLine 返回 issue 的"文件:行号"格式字符串。
//
// 若 issue 没有行号信息，则只返回文件名。主要用于 Markdown 摘要表格的展示。
//
// 参数:
//   - issue: MergedIssue
//
// 返回值:
//   - "file:line" 或 "file" 格式的字符串
func issueFileLine(issue MergedIssue) string {
	if issue.Line == nil {
		return issue.File
	}
	return fmt.Sprintf("%s:%d", issue.File, *issue.Line)
}

// issueRefs 提取 issue 的所有本地引用标识符，格式为 "reviewerID:localIssueID"。
//
// 这些引用用于在后续轮次中通过 CanonicalSignal 精确定位和操作特定 issue。
//
// 参数:
//   - issue: MergedIssue
//
// 返回值:
//   - 去重并排序后的引用标识符列表
func issueRefs(issue MergedIssue) []string {
	refs := make([]string, 0, len(issue.Mentions))
	for _, mention := range issue.Mentions {
		// 跳过没有本地 issue ID 的 Mention（可能是纯状态变更记录）
		if mention.LocalIssueID == "" {
			continue
		}
		refs = append(refs, issueRef(mention.ReviewerID, mention.LocalIssueID))
	}
	return uniqueSorted(refs)
}

// issueContainsRef 检查 issue 的引用列表中是否包含指定的引用标识符。
//
// 参数:
//   - issue: 要检查的 MergedIssue
//   - ref: 目标引用标识符（"reviewerID:localIssueID" 格式）
//
// 返回值:
//   - bool: 是否包含该引用
func issueContainsRef(issue MergedIssue, ref string) bool {
	for _, candidate := range issueRefs(issue) {
		if candidate == ref {
			return true
		}
	}
	return false
}

// issueRef 构造 "reviewerID:localIssueID" 格式的引用标识符。
//
// 参数:
//   - reviewerID: 审查者标识符
//   - localIssueID: 审查者本地的 issue 标识符
//
// 返回值:
//   - 组合后的引用字符串；若任一参数为空白则返回空字符串
func issueRef(reviewerID, localIssueID string) string {
	// 防止无效数据生成不完整的引用
	if strings.TrimSpace(reviewerID) == "" || strings.TrimSpace(localIssueID) == "" {
		return ""
	}
	return reviewerID + ":" + localIssueID
}

// uniqueStrings 对字符串切片去重，保留首次出现的顺序。
//
// 同时会忽略空白字符串和仅含空格的字符串。
//
// 参数:
//   - items: 原始字符串切片
//
// 返回值:
//   - 去重后的字符串切片，保持首次出现顺序
func uniqueStrings(items []string) []string {
	seen := make(map[string]struct{}, len(items))
	result := make([]string, 0, len(items))
	for _, item := range items {
		item = strings.TrimSpace(item)
		// 跳过空白字符串，避免无意义数据污染结果
		if item == "" {
			continue
		}
		if _, ok := seen[item]; ok {
			continue
		}
		seen[item] = struct{}{}
		result = append(result, item)
	}
	return result
}

// removeString 从字符串切片中移除所有与 target 相等的元素。
//
// 参数:
//   - items: 原始字符串切片
//   - target: 要移除的目标字符串
//
// 返回值:
//   - 移除后的新切片；若原切片为空则返回 nil
func removeString(items []string, target string) []string {
	if len(items) == 0 {
		return nil
	}
	result := make([]string, 0, len(items))
	for _, item := range items {
		if item == target {
			continue
		}
		result = append(result, item)
	}
	return result
}

// uniqueSorted 对字符串切片进行去重并按字典序排序。
//
// 组合了 uniqueStrings（去重+去空白）和 sort.Strings（排序）两个操作，
// 是归属列表标准化的常用操作。
//
// 参数:
//   - items: 原始字符串切片
//
// 返回值:
//   - 去重并排序后的字符串切片
func uniqueSorted(items []string) []string {
	items = uniqueStrings(items)
	sort.Strings(items)
	return items
}

// difference 计算两个字符串切片的差集（items - remove）。
//
// 返回 items 中存在但 remove 中不存在的元素。
//
// 参数:
//   - items: 被减集合
//   - remove: 要排除的元素集合
//
// 返回值:
//   - 差集结果；若 items 为空则返回 nil
func difference(items, remove []string) []string {
	if len(items) == 0 {
		return nil
	}
	// 将 remove 转为 map 以实现 O(1) 查找
	blocked := make(map[string]struct{}, len(remove))
	for _, item := range remove {
		blocked[item] = struct{}{}
	}
	result := make([]string, 0, len(items))
	for _, item := range items {
		if _, ok := blocked[item]; ok {
			continue
		}
		result = append(result, item)
	}
	return result
}

// uniqueMentions 对 IssueMention 切片进行去重和排序。
//
// 去重键由 (ReviewerID, LocalIssueID, Round, Status) 四元组唯一确定。
// 排序按"轮次 -> 审查者ID -> 本地IssueID -> 状态"的优先级，
// 确保 Mentions 列表的确定性和可读性。
//
// 参数:
//   - mentions: 原始 IssueMention 切片
//
// 返回值:
//   - 去重并排序后的 IssueMention 切片；若输入为空则返回 nil
func uniqueMentions(mentions []IssueMention) []IssueMention {
	if len(mentions) == 0 {
		return nil
	}
	// 使用四元组拼接字符串作为去重键
	seen := make(map[string]struct{}, len(mentions))
	result := make([]IssueMention, 0, len(mentions))
	for _, mention := range mentions {
		key := fmt.Sprintf("%s|%s|%d|%s", mention.ReviewerID, mention.LocalIssueID, mention.Round, mention.Status)
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		result = append(result, mention)
	}
	// 按时间（轮次）优先排序，便于按时间线回溯审查历史
	sort.Slice(result, func(i, j int) bool {
		if result[i].Round != result[j].Round {
			return result[i].Round < result[j].Round
		}
		if result[i].ReviewerID != result[j].ReviewerID {
			return result[i].ReviewerID < result[j].ReviewerID
		}
		if result[i].LocalIssueID != result[j].LocalIssueID {
			return result[i].LocalIssueID < result[j].LocalIssueID
		}
		return result[i].Status < result[j].Status
	})
	return result
}
