// Package orchestrator 提供代码审查辩论编排的核心逻辑。
// 本文件（issueparser.go）负责从 AI 审查者的自由文本响应中解析出结构化的审查问题，
// 并提供问题去重、相似度计算、焦点区域提取和调用链格式化等功能。
package orchestrator

import (
	"encoding/json"
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"unicode"

	"github.com/longyunfeigu/codedebate/internal/schema"
)

var (
	// validSeverities 定义有效的问题严重程度级别。
	// critical=阻塞合并, high=应该修复, medium=值得修复, low=次要, nitpick=风格问题
	validSeverities = map[string]bool{
		"critical": true,
		"high":     true,
		"medium":   true,
		"low":      true,
		"nitpick":  true,
	}

	// severityOrder 定义严重程度的排序优先级，数值越小越严重。
	// 用于合并重复问题时保留最高严重程度，以及最终排序时将关键问题排在前面。
	severityOrder = map[string]int{
		"critical": 0,
		"high":     1,
		"medium":   2,
		"low":      3,
		"nitpick":  4,
	}

	// validVerdicts 定义有效的审查结论类型。
	// approve=批准, request_changes=要求修改, comment=仅评论
	validVerdicts = map[string]bool{
		"approve":         true,
		"request_changes": true,
		"comment":         true,
	}

	// stopWords 定义英文停用词集合，在计算文本相似度时过滤掉这些常见词。
	// 过滤停用词可以提高Jaccard相似度计算的准确性，
	// 因为这些高频词对语义区分没有贡献，会稀释真正有意义的关键词的匹配权重。
	stopWords = map[string]bool{
		"the": true, "a": true, "in": true, "of": true,
		"is": true, "to": true, "and": true, "for": true,
		"with": true, "this": true, "that": true, "it": true,
	}

	// jsonFenceRe 匹配 Markdown 中 ```json ... ``` 格式的 JSON 代码块。
	// (?s) 使 '.' 匹配换行符，(.*?) 使用非贪婪匹配以获取最短的完整代码块。
	jsonFenceRe = regexp.MustCompile("(?s)```json\\s*(.*?)\\s*```")

	// rawJSONRe 匹配包含 "issues" 数组的原始 JSON 对象（未被代码块包裹的情况）。
	// 使用 [\s\S]* 而非 .* 来跨行匹配，确保能捕获多行 JSON 内容。
	rawJSONRe = regexp.MustCompile(`(?s)\{[\s\S]*"issues"\s*:\s*\[[\s\S]*\][\s\S]*\}`)

	// rawDeltaJSONRe 匹配包含 delta 操作关键字段的 JSON 对象（未被代码块包裹的情况）。
	// 匹配 add/retract/update/support/withdraw/contest 中任一关键字，
	// 用于在 ledger 模式下识别增量 delta JSON。
	rawDeltaJSONRe = regexp.MustCompile(`(?s)\{[\s\S]*"(add|retract|update|support|withdraw|contest)"\s*:[\s\S]*\}`)

	// focusRe 匹配 "## Suggested Review Focus" Markdown 章节标题，
	// 并捕获该章节下方的全部内容，直到遇到下一个 ## 标题或文本结尾。
	// 用于从分析器的输出中提取建议的审查关注重点。
	focusRe = regexp.MustCompile(`(?s)## Suggested Review Focus\s*\n(.*?)(?:\n##|\z)`)

	// fileLineRe 匹配文件路径中嵌入的行号范围信息。
	// 格式示例："src/main.py:37" 或 "src/main.py:37-48"。
	// 捕获组：(1)文件路径 (2)起始行号 (3)可选的结束行号。
	// 这允许审查者在 file 字段中直接附带行号，简化输出格式。
	fileLineRe = regexp.MustCompile(`^(.+):(\d+)(?:-(\d+))?$`)
)

// ParseResult 包含从审查者响应中解析结构化输出的完整结果。
// 将解析的各个阶段（JSON 提取、schema 校验、语法解析、字段验证）的结果
// 统一封装在一个结构体中，便于调用方根据不同的错误类型决定是否重试。
type ParseResult struct {
	Output       *ReviewerOutput          // 成功解析后的结构化审查输出；解析失败时为 nil
	RawJSON      string                   // 从响应文本中提取到的原始 JSON 字符串，用于调试和重试反馈
	SchemaErrors []schema.ValidationError // JSON Schema 校验发现的结构性错误列表
	ParseError   error                    // JSON 语法级解析错误（如格式不合法、缺少必要字段等）
}

// DeltaParseResult 包含从审查者响应中解析增量 delta 的完整结果。
// 与 ParseResult 结构类似，但 Output 字段为 StructurizeDelta 类型，
// 用于 ledger 模式下解析单轮的增量变化（新增/撤回/更新/支持/反对/争议问题）。
type DeltaParseResult struct {
	Output       *StructurizeDelta        // 成功解析后的增量 delta；解析失败时为 nil
	RawJSON      string                   // 从响应文本中提取到的原始 JSON 字符串
	SchemaErrors []schema.ValidationError // JSON Schema 校验发现的结构性错误列表
	ParseError   error                    // JSON 语法级解析错误
}

// ParseReviewerOutput 从审查者的自由文本响应中解析出结构化的 ReviewerOutput。
//
// 解析策略采用两级回退机制：
//  1. 优先查找 ```json ... ``` Markdown 代码块中的 JSON 内容
//  2. 回退方案：在整段文本中搜索包含 "issues" 数组的原始 JSON 对象
//
// 参数：
//   - response: 审查者的完整响应文本，可能包含 Markdown 格式的讨论内容和嵌入的 JSON
//
// 返回值：
//   - *ParseResult: 包含解析结果和各阶段的错误信息。
//     即使 schema 校验失败，仍会尝试手动解析以尽可能提取有效问题。
//     调用方可通过检查 ParseError 和 SchemaErrors 判断解析质量，决定是否需要重试。
//
// 对每个问题执行严格的字段验证：
//   - severity 必须在 validSeverities 中定义
//   - file 不能为空
//   - title 和 description 不能为空（去除空白后）
//   - 不合格的问题会被静默跳过，而非导致整体解析失败
func ParseReviewerOutput(response string) *ParseResult {
	result := &ParseResult{}

	// 从响应文本中提取 JSON 字符串（优先代码块，回退到原始 JSON 匹配）
	result.RawJSON = extractJSON(response, rawJSONRe)

	if result.RawJSON == "" {
		result.ParseError = fmt.Errorf("no JSON block found in response")
		return result
	}

	// 在深度解析之前先用 JSON Schema 校验结构有效性，
	// 提前发现字段缺失、类型错误等问题，即使校验失败也继续尝试手动解析
	vr := schema.ValidateIssuesJSON(result.RawJSON)
	if !vr.Valid {
		result.SchemaErrors = vr.Errors
		// 不提前返回：即使 schema 校验失败，手动解析仍可能提取出部分有效 issue，
		// 避免因个别字段问题导致整批结果被丢弃
	}

	// 先解析为通用结构体（issues 使用 json.RawMessage），
	// 这样可以逐个解析每条 issue 并跳过格式不合法的条目，
	// 而不是因为一条 issue 格式错误导致整个 JSON 解析失败
	var raw struct {
		Issues  []json.RawMessage `json:"issues"`
		Verdict string            `json:"verdict"`
		Summary string            `json:"summary"`
	}
	if err := json.Unmarshal([]byte(result.RawJSON), &raw); err != nil {
		result.ParseError = fmt.Errorf("JSON syntax error: %w", err)
		return result
	}

	// issues 字段不存在（null）与空数组 [] 含义不同：
	// null 表示模型输出格式不符合预期，需要报错让调用方重试
	if raw.Issues == nil {
		result.ParseError = fmt.Errorf("missing 'issues' key in JSON")
		return result
	}

	// 对 verdict 进行容错处理：如果模型输出了无效的 verdict，
	// 默认降级为 "comment"（最保守的结论），避免错误地批准或拒绝 PR
	verdict := raw.Verdict
	if !validVerdicts[verdict] {
		verdict = "comment"
	}

	var issues []ReviewIssue
	for _, rawIssue := range raw.Issues {
		// 将每条 issue 解析为 map[string]interface{} 而非强类型结构体，
		// 以便灵活处理模型可能输出的额外字段或类型不一致的情况
		var m map[string]interface{}
		if err := json.Unmarshal(rawIssue, &m); err != nil {
			continue
		}

		// 严重程度是问题分类和排序的关键字段，必须是预定义的有效值之一
		severity, _ := m["severity"].(string)
		if !validSeverities[severity] {
			continue
		}

		// 文件路径是将问题定位到代码位置的必要字段
		file, _ := m["file"].(string)
		if file == "" {
			continue
		}

		// 从文件路径中提取嵌入的行号范围。
		// 有些模型会将行号直接附加在文件路径后面（如 "file.py:37-48"），
		// 这里将其拆分出来，使 file 字段保持纯路径，行号信息存入专用字段
		var fileLine, fileEndLine *int
		if match := fileLineRe.FindStringSubmatch(file); match != nil {
			file = match[1]
			if v, err := strconv.Atoi(match[2]); err == nil && v > 0 {
				fileLine = &v
			}
			if match[3] != "" {
				if v, err := strconv.Atoi(match[3]); err == nil && v > 0 {
					fileEndLine = &v
				}
			}
		}

		// title 和 description 是问题的核心描述字段，缺少任何一个都无法有效呈现问题
		title, _ := m["title"].(string)
		title = strings.TrimSpace(title)
		if title == "" {
			continue
		}

		description, _ := m["description"].(string)
		description = strings.TrimSpace(description)
		if description == "" {
			continue
		}

		// category 为可选字段，默认归类为 "general"，
		// 确保所有问题都有分类标签以支持后续的分类统计和展示
		category, _ := m["category"].(string)
		if category == "" {
			category = "general"
		}

		issue := ReviewIssue{
			Severity:    severity,
			Category:    category,
			File:        file,
			Title:       title,
			Description: description,
		}

		// 可选字段：起始行号。
		// 优先使用 JSON 中显式的 "line" 字段（模型明确指定），
		// 其次使用从文件路径中提取的行号作为 fallback。
		// JSON 数字会被解析为 float64，需要转换为 int
		if lineVal, ok := m["line"].(float64); ok && lineVal > 0 {
			line := int(lineVal)
			issue.Line = &line
		} else if fileLine != nil {
			issue.Line = fileLine
		}

		// 可选字段：结束行号。
		// 与起始行号同理，优先使用 JSON 中的 "endLine" 字段。
		// 额外校验 endLine >= startLine，避免无意义的倒序行号范围
		if endVal, ok := m["endLine"].(float64); ok && endVal > 0 {
			endLine := int(endVal)
			if issue.Line != nil && endLine >= *issue.Line {
				issue.EndLine = &endLine
			}
		} else if fileEndLine != nil && issue.Line != nil && *fileEndLine >= *issue.Line {
			issue.EndLine = fileEndLine
		}

		// 以下为完全可选的补充字段，缺失不影响问题的有效性
		if sf, ok := m["suggestedFix"].(string); ok {
			issue.SuggestedFix = sf
		}
		if cs, ok := m["codeSnippet"].(string); ok {
			issue.CodeSnippet = cs
		}
		// raisedBy 字段记录模型自称该问题由哪些审查者提出，
		// 注意这是模型输出的声称值，最终归属由编排器根据实际发言决定
		if rb, ok := m["raisedBy"].([]interface{}); ok {
			for _, v := range rb {
				if s, ok := v.(string); ok {
					issue.ClaimedBy = append(issue.ClaimedBy, s)
				}
			}
		}

		issues = append(issues, issue)
	}

	result.Output = &ReviewerOutput{
		Issues:  issues,
		Verdict: verdict,
		Summary: raw.Summary,
	}
	return result
}

// ParseStructurizeDelta 从审查者的响应文本中解析单轮增量 delta（ledger 模式）。
//
// 增量 delta 是 ledger 模式下审查者表达意见变化的格式，包含六种操作：
//   - add: 新发现的问题
//   - retract: 撤回自己之前提出的问题（通过 issue ID）
//   - update: 更新已有问题的部分字段
//   - support: 明确支持其他审查者提出的问题
//   - withdraw: 撤回对某个问题的支持
//   - contest: 对某个问题表示争议/反对
//
// 参数：
//   - response: 审查者的完整响应文本
//
// 返回值：
//   - *DeltaParseResult: 包含解析后的增量 delta 和错误信息。
//     所有六个操作字段都必须存在（可以为空数组但不能缺失），否则返回 ParseError。
func ParseStructurizeDelta(response string) *DeltaParseResult {
	result := &DeltaParseResult{}

	// 使用 delta 专用的正则表达式提取 JSON，
	// 因为 delta JSON 的特征关键字与普通 issues JSON 不同
	result.RawJSON = extractJSON(response, rawDeltaJSONRe)

	if result.RawJSON == "" {
		result.ParseError = fmt.Errorf("no JSON block found in response")
		return result
	}

	// 使用 issues_delta schema 校验 delta JSON 的结构有效性
	vr := schema.ValidateJSON("issues_delta", result.RawJSON)
	if !vr.Valid {
		result.SchemaErrors = vr.Errors
	}

	// 将 JSON 解析为通用结构体，add/update/support/withdraw/contest 使用 json.RawMessage
	// 以便逐条解析并跳过格式不合法的条目
	var raw struct {
		Add      []json.RawMessage `json:"add"`
		Retract  []string          `json:"retract"`
		Update   []json.RawMessage `json:"update"`
		Support  []json.RawMessage `json:"support"`
		Withdraw []json.RawMessage `json:"withdraw"`
		Contest  []json.RawMessage `json:"contest"`
	}
	if err := json.Unmarshal([]byte(result.RawJSON), &raw); err != nil {
		result.ParseError = fmt.Errorf("JSON syntax error: %w", err)
		return result
	}

	// 所有六个操作字段都必须存在（JSON 中为 null 表示字段缺失），
	// 这是协议完整性的要求——缺少任何一个都说明模型没有按照预期格式输出
	if raw.Add == nil || raw.Retract == nil || raw.Update == nil || raw.Support == nil || raw.Withdraw == nil || raw.Contest == nil {
		result.ParseError = fmt.Errorf("missing one of required keys: add/retract/update/support/withdraw/contest")
		return result
	}

	// 预分配各操作类型的切片，使用 make(..., 0, cap) 避免 nil 切片，
	// 确保序列化为 JSON 时输出 [] 而非 null
	out := &StructurizeDelta{
		Retract:  make([]string, 0, len(raw.Retract)),
		Support:  make([]DeltaIssueRefAction, 0, len(raw.Support)),
		Withdraw: make([]DeltaIssueRefAction, 0, len(raw.Withdraw)),
		Contest:  make([]DeltaIssueRefAction, 0, len(raw.Contest)),
	}

	// 解析 retract 列表：审查者撤回自己之前提出的问题
	for _, id := range raw.Retract {
		id = strings.TrimSpace(id)
		if id == "" {
			continue
		}
		out.Retract = append(out.Retract, id)
	}

	// 解析 add 列表：审查者新发现的问题，需要与 ParseReviewerOutput 类似的字段验证
	for _, rawAdd := range raw.Add {
		var m map[string]any
		if err := json.Unmarshal(rawAdd, &m); err != nil {
			continue
		}

		// 与 ParseReviewerOutput 中的验证逻辑一致：severity/file/title/description 为必填字段
		severity := parseStringField(m, "severity")
		if !validSeverities[severity] {
			continue
		}
		file := parseStringField(m, "file")
		title := parseStringField(m, "title")
		description := parseStringField(m, "description")
		if file == "" || title == "" || description == "" {
			continue
		}

		add := DeltaAddIssue{
			Severity:    severity,
			Category:    parseStringField(m, "category"),
			File:        file,
			Title:       title,
			Description: description,
		}
		// suggestedFix 和 line 为可选字段
		if suggestedFix, ok := parseOptionalStringField(m, "suggestedFix"); ok {
			add.SuggestedFix = suggestedFix
		}
		if line, ok := parsePositiveIntField(m, "line"); ok {
			add.Line = &line
		}
		out.Add = append(out.Add, add)
	}

	// 解析 update 列表：更新已有问题的部分字段
	for _, rawUpdate := range raw.Update {
		var m map[string]any
		if err := json.Unmarshal(rawUpdate, &m); err != nil {
			continue
		}

		update := DeltaUpdateIssue{
			ID: parseStringField(m, "id"),
		}
		// ID 是更新操作的必填字段，用于定位要更新的具体问题
		if update.ID == "" {
			continue
		}

		// 以下字段均为可选更新：只有模型明确提供的字段才会被更新，
		// 使用指针类型区分"未提供"和"设为空值"
		if severity, ok := parseOptionalStringField(m, "severity"); ok {
			if !validSeverities[severity] {
				continue
			}
			update.Severity = &severity
		}
		if category, ok := parseOptionalStringField(m, "category"); ok {
			update.Category = &category
		}
		if file, ok := parseOptionalStringField(m, "file"); ok && file != "" {
			update.File = &file
		}
		if title, ok := parseOptionalStringField(m, "title"); ok && title != "" {
			update.Title = &title
		}
		if description, ok := parseOptionalStringField(m, "description"); ok && description != "" {
			update.Description = &description
		}
		if suggestedFix, ok := parseOptionalStringField(m, "suggestedFix"); ok {
			update.SuggestedFix = &suggestedFix
		}
		if line, ok := parsePositiveIntField(m, "line"); ok {
			update.Line = &line
		}

		out.Update = append(out.Update, update)
	}

	// 解析 support/withdraw/contest 列表，这三种操作结构相同（都只包含 issueRef），
	// 复用 parseDeltaIssueRefActions 进行统一解析
	for _, action := range parseDeltaIssueRefActions(raw.Support) {
		out.Support = append(out.Support, action)
	}
	for _, action := range parseDeltaIssueRefActions(raw.Withdraw) {
		out.Withdraw = append(out.Withdraw, action)
	}
	for _, action := range parseDeltaIssueRefActions(raw.Contest) {
		out.Contest = append(out.Contest, action)
	}

	result.Output = out
	return result
}

// parseDeltaIssueRefActions 将一组 JSON 原始消息解析为 DeltaIssueRefAction 列表。
// 每条消息应包含一个 "issueRef" 字符串字段，格式为 "reviewerID:localIssueID"。
// 自动去重：如果同一个 issueRef 出现多次，只保留第一次出现的。
//
// 参数：
//   - items: JSON 原始消息列表
//
// 返回值：
//   - []DeltaIssueRefAction: 去重后的操作列表，保持原始顺序
func parseDeltaIssueRefActions(items []json.RawMessage) []DeltaIssueRefAction {
	actions := make([]DeltaIssueRefAction, 0, len(items))
	// 使用 seen 集合去重，防止同一轮中模型对同一个 issue 重复表态
	seen := make(map[string]struct{}, len(items))
	for _, rawItem := range items {
		var m map[string]any
		if err := json.Unmarshal(rawItem, &m); err != nil {
			continue
		}
		ref := parseStringField(m, "issueRef")
		if ref == "" {
			continue
		}
		// 跳过重复的 issueRef，避免同一个 issue 被重复计票
		if _, ok := seen[ref]; ok {
			continue
		}
		seen[ref] = struct{}{}
		actions = append(actions, DeltaIssueRefAction{IssueRef: ref})
	}
	return actions
}

// extractJSON 从响应文本中提取 JSON 字符串。
// 采用两级回退策略：
//  1. 优先尝试匹配 ```json ... ``` Markdown 代码块格式（模型格式化输出的常见方式）
//  2. 回退到使用调用方指定的正则表达式匹配裸 JSON 对象（处理模型未用代码块包裹的情况）
//
// 参数：
//   - response: 待提取的完整响应文本
//   - rawPattern: 用于回退匹配的正则表达式（针对不同 JSON 结构使用不同的模式）
//
// 返回值：
//   - 提取到的 JSON 字符串；未找到时返回空字符串
func extractJSON(response string, rawPattern *regexp.Regexp) string {
	// 优先尝试匹配 ```json 代码块格式，因为这是更可靠的 JSON 边界标识
	if m := jsonFenceRe.FindStringSubmatch(response); len(m) > 1 {
		return m[1]
	}
	// 回退方案：尝试匹配未包裹在代码块中的原始 JSON 对象，
	// 这种情况下边界判断依赖特定的关键字模式，准确度稍低
	if m := rawPattern.FindString(response); m != "" {
		return m
	}
	return ""
}

// parseStringField 从 map 中安全提取字符串字段并去除首尾空白。
// 如果字段不存在或类型不是字符串，返回空字符串。
// 这是一个容错性的辅助函数，避免在每次字段提取时都写类型断言和错误处理。
//
// 参数：
//   - m: 从 JSON 解析出的 map
//   - key: 要提取的字段名
//
// 返回值：
//   - 去除空白后的字符串值；字段不存在或类型不匹配时返回空字符串
func parseStringField(m map[string]any, key string) string {
	s, _ := m[key].(string)
	return strings.TrimSpace(s)
}

// parseOptionalStringField 从 map 中提取可选字符串字段。
// 与 parseStringField 的区别在于能区分"字段不存在"和"字段存在但值为空"：
//   - 字段不存在 → 返回 ("", false)
//   - 字段存在但类型不匹配 → 返回 ("", false)
//   - 字段存在且为字符串 → 返回 (trimmed_value, true)
//
// 这种区分对于 delta update 操作很重要：只有明确提供的字段才应该被更新。
//
// 参数：
//   - m: 从 JSON 解析出的 map
//   - key: 要提取的字段名
//
// 返回值：
//   - (string, bool): 字段值和是否存在的标志
func parseOptionalStringField(m map[string]any, key string) (string, bool) {
	v, ok := m[key]
	if !ok {
		return "", false
	}
	s, ok := v.(string)
	if !ok {
		return "", false
	}
	return strings.TrimSpace(s), true
}

// parsePositiveIntField 从 map 中提取正整数字段。
// JSON 中所有数字都被解析为 float64，本函数将其安全转换为正整数。
// 拒绝零、负数和带小数部分的值（如 3.5），因为行号必须是正整数。
//
// 参数：
//   - m: 从 JSON 解析出的 map
//   - key: 要提取的字段名
//
// 返回值：
//   - (int, bool): 正整数值和是否成功提取的标志。
//     字段不存在、类型不匹配、值 <= 0 或有小数部分时返回 (0, false)
func parsePositiveIntField(m map[string]any, key string) (int, bool) {
	v, ok := m[key]
	if !ok {
		return 0, false
	}
	f, ok := v.(float64)
	if !ok {
		return 0, false
	}
	i := int(f)
	// 拒绝非正整数：i <= 0 排除零和负数，float64(i) != f 排除带小数部分的值
	if i <= 0 || float64(i) != f {
		return 0, false
	}
	return i, true
}

// ParseFocusAreas 从分析器输出中提取建议的审查关注重点列表。
// 在分析器的 Markdown 输出中查找 "## Suggested Review Focus" 章节，
// 解析该章节下的列表项（支持 '-' 和 '*' 两种 Markdown 列表标记）。
//
// 参数：
//   - analysis: 分析器的完整 Markdown 输出文本
//
// 返回值：
//   - []string: 提取到的关注重点字符串切片，每个元素对应一个列表项（已去除标记和空白）。
//     未找到匹配的章节或无有效列表项时返回 nil。
func ParseFocusAreas(analysis string) []string {
	m := focusRe.FindStringSubmatch(analysis)
	if len(m) < 2 {
		return nil
	}

	lines := strings.Split(strings.TrimSpace(m[1]), "\n")
	var areas []string
	for _, line := range lines {
		// 去除行首缩进
		line = strings.TrimLeft(line, " \t")
		// 去除 Markdown 列表标记（- 或 *），使返回值为纯文本内容
		if len(line) > 0 && (line[0] == '-' || line[0] == '*') {
			line = strings.TrimSpace(line[1:])
		}
		line = strings.TrimSpace(line)
		if line != "" {
			areas = append(areas, line)
		}
	}
	return areas
}

// DeduplicateIssues 对来自多个审查者的问题进行跨审查者去重合并。
// 当多个审查者指出同一问题时，将它们合并为一个 MergedIssue。
//
// 合并条件（三个条件必须同时满足）：
//  1. 相同文件路径（完全匹配）
//  2. 行号范围重叠或相邻（5行以内）
//  3. 标题和描述的加权 Jaccard 相似度 > 0.35
//
// 合并规则：
//   - 保留最高严重程度（critical > high > medium > low > nitpick）
//   - 记录所有提出该问题的审查者 ID（RaisedBy、SupportedBy）
//   - 保留所有描述文本（Descriptions），用于后续生成更全面的问题说明
//   - 如果已有的修复建议为空，则使用新发现的修复建议
//
// 参数：
//   - issuesByReviewer: 按审查者 ID 分组的问题列表
//
// 返回值：
//   - []MergedIssue: 去重合并后的问题列表，按严重程度降序排列（critical 在前）
func DeduplicateIssues(issuesByReviewer map[string][]ReviewIssue) []MergedIssue {
	var merged []MergedIssue

	for reviewerID, issues := range issuesByReviewer {
		for _, issue := range issues {
			found := false
			// 遍历已合并的问题列表，寻找可以合并的目标
			for i := range merged {
				if isSimilarIssue(&merged[i].ReviewIssue, &issue) {
					// 找到相似问题，将当前审查者的信息合并进去
					merged[i].RaisedBy = append(merged[i].RaisedBy, reviewerID)
					merged[i].SupportedBy = append(merged[i].SupportedBy, reviewerID)
					merged[i].Descriptions = append(merged[i].Descriptions, issue.Description)
					merged[i].Mentions = append(merged[i].Mentions, IssueMention{
						ReviewerID: reviewerID,
						Status:     "active",
					})
					// 保留最高严重程度（数值越小越严重）
					if severityOrder[issue.Severity] < severityOrder[merged[i].Severity] {
						merged[i].Severity = issue.Severity
					}
					// 补充修复建议：仅在当前合并问题无修复建议时采纳新的
					if merged[i].SuggestedFix == "" && issue.SuggestedFix != "" {
						merged[i].SuggestedFix = issue.SuggestedFix
					}
					found = true
					break
				}
			}
			// 没有找到相似问题，作为新的独立问题加入合并列表
			if !found {
				merged = append(merged, MergedIssue{
					ReviewIssue:  issue,
					RaisedBy:     []string{reviewerID},
					IntroducedBy: []string{reviewerID},
					SupportedBy:  []string{reviewerID},
					Descriptions: []string{issue.Description},
					Mentions: []IssueMention{{
						ReviewerID: reviewerID,
						Status:     "active",
					}},
				})
			}
		}
	}

	// 按严重程度排序，确保最关键的问题出现在报告最前面
	sort.Slice(merged, func(i, j int) bool {
		return severityOrder[merged[i].Severity] < severityOrder[merged[j].Severity]
	})

	// 对每个合并后的问题执行规范化处理：去重各列表字段、计算 canonicalID 等
	for i := range merged {
		merged[i] = finalizeCanonicalIssue(merged[i])
	}

	return merged
}

// isSimilarIssue 判断两个问题是否足够相似以进行合并。
// 使用多层过滤策略：先用文件路径和行号进行快速筛选（低计算开销），
// 再用文本相似度进行精确判断（高计算开销）。
//
// 判断逻辑：
//  1. 文件路径完全匹配（不同文件的问题一定不是重复的）
//  2. 行号范围重叠或相邻（5行以内）
//  3. 加权文本相似度 > 0.35（标题权重 0.7 + 描述权重 0.3）
//
// 参数：
//   - a, b: 待比较的两个 ReviewIssue 指针
//
// 返回值：
//   - bool: true 表示两个问题足够相似应该合并
func isSimilarIssue(a, b *ReviewIssue) bool {
	// 第一层过滤：文件路径必须完全一致
	if a.File != b.File {
		return false
	}

	// 第二层过滤：行号范围是否重叠或邻近
	if !linesOverlap(a, b) {
		return false
	}

	// 第三层过滤：基于文本内容的相似度计算
	// 标题通常更能代表问题的核心含义，给予 0.7 的高权重
	wordsA := filterStopWords(tokenize(strings.ToLower(a.Title)))
	wordsB := filterStopWords(tokenize(strings.ToLower(b.Title)))
	titleSim := jaccardSimilarity(wordsA, wordsB)

	// 描述通常较长，只取前 50 个词以控制计算量，同时避免冗余细节干扰相似度
	descWordsA := filterStopWords(firstN(tokenize(strings.ToLower(a.Description)), 50))
	descWordsB := filterStopWords(firstN(tokenize(strings.ToLower(b.Description)), 50))
	descSim := jaccardSimilarity(descWordsA, descWordsB)

	// 加权公式：标题占 70%，描述占 30%，阈值 0.35
	// 标题权重高是因为标题更凝练地表达了问题的核心，
	// 而描述可能包含不同审查者各自的详细分析，差异较大
	return titleSim*0.7+descSim*0.3 > 0.35
}

// linesOverlap 检查两个问题的行号范围是否重叠或在 5 行的邻近范围内。
// 采用三种情况分别处理，平衡准确性和容错性：
//   - 两边都有行号：要求范围重叠或相邻（5行内），这是最精确的判断
//   - 两边都没有行号：返回 true，因为无法用行号排除，交由文本相似度决定
//   - 仅一边有行号：返回 false，保守处理避免将同一文件中不同位置的问题误合并
//
// 参数：
//   - a, b: 待比较的两个 ReviewIssue 指针
//
// 返回值：
//   - bool: true 表示行号范围兼容（可继续检查文本相似度）
func linesOverlap(a, b *ReviewIssue) bool {
	// 都没有行号：无法基于位置排除，返回 true 让文本相似度来决定
	if a.Line == nil && b.Line == nil {
		return true
	}
	// 只有一边有行号：保守地认为不重叠，
	// 因为同一文件中可能存在多个不同位置的类似问题
	if a.Line == nil || b.Line == nil {
		return false
	}

	// 计算各自的行号范围：如果没有 EndLine，则范围退化为单行
	aStart := *a.Line
	aEnd := aStart
	if a.EndLine != nil {
		aEnd = *a.EndLine
	}
	bStart := *b.Line
	bEnd := bStart
	if b.EndLine != nil {
		bEnd = *b.EndLine
	}
	// 判断两个范围是否重叠或在 5 行邻近距离内。
	// 使用 +5 的容差是因为不同审查者对同一段代码的行号标注可能有轻微偏差
	return aStart <= bEnd+5 && bStart <= aEnd+5
}

// jaccardSimilarity 计算两个词列表之间的 Jaccard 相似系数。
// Jaccard 系数定义为两个集合的交集大小除以并集大小：J(A,B) = |A ∩ B| / |A ∪ B|。
// 值域为 [0, 1]，其中 1 表示两个集合完全相同，0 表示没有任何交集。
//
// 选择 Jaccard 而非余弦相似度是因为：
//   - 实现简单，无需维护词频向量
//   - 对于短文本（标题通常 5-15 个词），集合相似度已经足够有效
//   - 不需要 IDF 权重等额外计算
//
// 参数：
//   - a, b: 两个已分词并过滤停用词后的词列表
//
// 返回值：
//   - float64: Jaccard 相似系数，范围 [0, 1]。两个空集合的相似度定义为 0
func jaccardSimilarity(a, b []string) float64 {
	// 两个空集合的并集为空，分母为 0，特殊处理返回 0
	if len(a) == 0 && len(b) == 0 {
		return 0
	}

	// 将两个列表转换为集合（map），自动去除重复词
	setA := make(map[string]bool, len(a))
	for _, w := range a {
		setA[w] = true
	}
	setB := make(map[string]bool, len(b))
	for _, w := range b {
		setB[w] = true
	}

	// 计算交集大小
	intersection := 0
	for w := range setA {
		if setB[w] {
			intersection++
		}
	}

	// 并集大小 = |A| + |B| - |A ∩ B|（利用容斥原理）
	union := len(setA) + len(setB) - intersection
	if union == 0 {
		return 0
	}
	return float64(intersection) / float64(union)
}

// filterStopWords 从词列表中过滤掉英文停用词和空字符串。
// 停用词（如 the、a、in、of 等）在几乎所有文本中都会出现，
// 对区分不同问题的语义没有贡献，反而会增加无意义的"交集"从而抬高相似度。
//
// 参数：
//   - words: 待过滤的词列表
//
// 返回值：
//   - []string: 过滤后的词列表
func filterStopWords(words []string) []string {
	var result []string
	for _, w := range words {
		if w != "" && !stopWords[w] {
			result = append(result, w)
		}
	}
	return result
}

// tokenize 将文本分割为词列表，分隔符为任何非字母非数字字符。
// 使用 unicode.IsLetter 和 unicode.IsNumber 而非 ASCII 范围判断，
// 以正确处理多语言文本中的 Unicode 字符。
// 副作用：所有标点符号会被去除，如 "vulnerability." 和 "vulnerability" 会被视为同一个词。
//
// 参数：
//   - s: 待分词的文本
//
// 返回值：
//   - []string: 分词后的词列表
func tokenize(s string) []string {
	return strings.FieldsFunc(s, func(r rune) bool {
		return !unicode.IsLetter(r) && !unicode.IsNumber(r)
	})
}

// firstN 返回字符串切片的前 n 个元素。
// 用于限制参与相似度计算的文本长度，避免过长的描述文本增加不必要的计算开销。
// 如果切片长度不足 n，原样返回整个切片（不做填充）。
//
// 参数：
//   - s: 输入的字符串切片
//   - n: 要保留的最大元素个数
//
// 返回值：
//   - []string: 截取后的切片（与原切片共享底层数组）
func firstN(s []string, n int) []string {
	if len(s) <= n {
		return s
	}
	return s[:n]
}

// DeduplicateMergedIssues 对已经是 MergedIssue 格式的扁平列表进行去重合并。
// 与 DeduplicateIssues 的区别在于输入已经是合并后的 MergedIssue，
// 而非按审查者分组的原始问题。适用于需要对来自不同来源的已合并问题再次去重的场景。
//
// 复用 isSimilarIssue() 进行相似度判断，合并规则与 DeduplicateIssues 一致：
//   - 保留最高严重程度
//   - 合并 RaisedBy（去重）、Descriptions、Mentions 等列表字段
//   - 补充缺失的修复建议
//
// 参数：
//   - issues: 待去重的 MergedIssue 列表
//
// 返回值：
//   - []MergedIssue: 去重合并后的问题列表，按严重程度降序排列
func DeduplicateMergedIssues(issues []MergedIssue) []MergedIssue {
	var merged []MergedIssue

	for _, issue := range issues {
		found := false
		for i := range merged {
			if isSimilarIssue(&merged[i].ReviewIssue, &issue.ReviewIssue) {
				// 合并 RaisedBy 并去重，避免同一审查者被重复记录
				seen := make(map[string]bool)
				for _, r := range merged[i].RaisedBy {
					seen[r] = true
				}
				for _, r := range issue.RaisedBy {
					if !seen[r] {
						merged[i].RaisedBy = append(merged[i].RaisedBy, r)
					}
				}
				// 其他列表字段直接追加，由 finalizeCanonicalIssue 统一去重
				merged[i].SupportedBy = append(merged[i].SupportedBy, issue.SupportedBy...)
				merged[i].IntroducedBy = append(merged[i].IntroducedBy, issue.IntroducedBy...)
				merged[i].WithdrawnBy = append(merged[i].WithdrawnBy, issue.WithdrawnBy...)
				merged[i].ContestedBy = append(merged[i].ContestedBy, issue.ContestedBy...)
				merged[i].Descriptions = append(merged[i].Descriptions, issue.Descriptions...)
				merged[i].Mentions = append(merged[i].Mentions, issue.Mentions...)
				// 保留最高严重程度
				if severityOrder[issue.Severity] < severityOrder[merged[i].Severity] {
					merged[i].Severity = issue.Severity
				}
				// 补充修复建议
				if merged[i].SuggestedFix == "" && issue.SuggestedFix != "" {
					merged[i].SuggestedFix = issue.SuggestedFix
				}
				found = true
				break
			}
		}
		if !found {
			merged = append(merged, issue)
		}
	}

	// 按严重程度排序，critical 排在最前面
	sort.Slice(merged, func(i, j int) bool {
		return severityOrder[merged[i].Severity] < severityOrder[merged[j].Severity]
	})

	// 对每个合并后的问题执行规范化和最终处理
	for i := range merged {
		merged[i] = finalizeCanonicalIssue(merged[i])
	}

	return merged
}

// FormatCallChainForReviewer 将原始引用数据格式化为审查者可阅读的 Markdown 调用链文档。
// 为每个被修改的符号（函数、变量等），列出其在代码仓库中被引用的位置，
// 帮助审查者理解变更的影响范围——例如修改了某个函数后，哪些调用方可能受到影响。
//
// 格式化规则：
//   - 每个符号最多展示 10 个引用位置（避免输出过长占用 token 预算）
//   - 每个引用位置的上下文内容截取前 150 个字符（足够看到调用语句的核心部分）
//   - 输出格式为结构化 Markdown，包含标题层级和编号列表
//
// 参数：
//   - references: 原始引用数据列表，每个元素包含一个符号及其被引用的位置
//
// 返回值：
//   - string: 格式化后的 Markdown 文本。无引用数据时返回空字符串
func FormatCallChainForReviewer(references []RawReference) string {
	if len(references) == 0 {
		return ""
	}

	var sections []string
	for _, ref := range references {
		callers := ref.FoundInFiles
		// 限制每个符号最多展示 10 个引用位置，
		// 避免高频使用的工具函数输出过多引用导致 token 浪费
		if len(callers) > 10 {
			callers = callers[:10]
		}

		var callerLines []string
		for i, f := range callers {
			content := f.Content
			// 截取上下文内容的前 150 个字符，
			// 保留足够的信息让审查者看到调用方式，同时控制总输出长度
			if len(content) > 150 {
				content = content[:150]
			}
			callerLines = append(callerLines, fmt.Sprintf("%d. %s:%d\n   > %s", i+1, f.File, f.Line, content))
		}

		section := fmt.Sprintf("### Callers of `%s`\nFound in %d locations:\n\n%s",
			ref.Symbol, len(ref.FoundInFiles), strings.Join(callerLines, "\n\n"))
		sections = append(sections, section)
	}

	return "## Call Chain Context\n\n" + strings.Join(sections, "\n\n---\n\n")
}
