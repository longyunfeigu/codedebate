// Package orchestrator 定义了代码审查辩论编排器所使用的核心类型。
// 该包负责协调多个 AI 审查者之间的多轮辩论流程，包括消息交换、
// 共识检测、Token 用量追踪以及最终结论的生成。
package orchestrator

import (
	"context"
	"time"

	"github.com/longyunfeigu/codedebate/internal/provider"
)

// Reviewer 封装了一个 AI 提供者及其身份标识和系统提示词。
// 每个 Reviewer 代表辩论中的一个独立审查角色，拥有自己的 AI 模型实例。
// 在辩论编排中，多个 Reviewer 会就同一份代码变更进行多轮交叉审查和讨论。
type Reviewer struct {
	// ID 是审查者的唯一标识符，用于在辩论消息、Token 统计和状态追踪中
	// 区分不同的审查者。通常格式为 "reviewer-1"、"reviewer-2" 等。
	ID string

	// Provider 是该审查者使用的 AI 提供者实例，负责实际的模型调用。
	// 不同的审查者可以使用不同的 AI 提供者（如 OpenAI、Anthropic 等），
	// 从而引入多样化的审查视角。
	Provider provider.AIProvider

	// SystemPrompt 是该审查者的系统提示词，用于定义其审查风格和关注重点。
	// 例如，一个审查者可能专注于安全漏洞，另一个可能专注于性能优化。
	SystemPrompt string
}

// DebateMessage 表示辩论对话中的单条消息。
// 记录了发言的审查者 ID、所属轮次、消息内容和时间戳。
// 所有辩论消息按时间顺序组成完整的辩论过程记录。
type DebateMessage struct {
	// ReviewerID 标识该消息的发送者，对应 Reviewer.ID。
	// 用于在 UI 展示和日志中标注消息来源。
	ReviewerID string `json:"reviewerId"`

	// Round 表示该消息所属的辩论轮次（从 1 开始计数）。
	// omitempty 表示在序列化时如果为零值则省略该字段。
	Round int `json:"round,omitempty"`

	// Content 是消息的文本内容，包含审查者对代码变更的分析、意见或回应。
	// 内容通常为 Markdown 格式，可能包含代码片段和结构化的问题列表。
	Content string `json:"content"`

	// Timestamp 是消息的创建时间，用于按时间顺序排列辩论消息，
	// 同时也可用于计算各轮次的耗时。
	Timestamp time.Time `json:"timestamp"`
}

// DebateSummary 保存审查者的最终总结。
// 在辩论结束后，每个审查者会根据整个辩论过程生成一份总结性陈述，
// 概括自己的最终立场和关键发现。
type DebateSummary struct {
	// ReviewerID 标识生成该总结的审查者，对应 Reviewer.ID。
	ReviewerID string `json:"reviewerId"`

	// Summary 是审查者的最终总结文本，概括其在辩论中的关键观点、
	// 发现的问题以及最终的审查结论。
	Summary string `json:"summary"`
}

// TokenUsage 追踪每个审查者的预估 Token 消耗量。
// 包括输入 Token 数、输出 Token 数和预估费用，用于成本监控和预算控制。
// 每个审查者在整个辩论过程中的所有 API 调用累计统计。
type TokenUsage struct {
	// ReviewerID 标识该 Token 用量所属的审查者，对应 Reviewer.ID。
	ReviewerID string `json:"reviewerId"`

	// InputTokens 是该审查者所有请求中累计消耗的输入 Token 数。
	// 输入 Token 包括系统提示词、辩论历史消息和代码 diff 等内容。
	InputTokens int `json:"inputTokens"`

	// OutputTokens 是该审查者所有响应中累计生成的输出 Token 数。
	// 输出 Token 包括审查意见、分析结论和总结等生成内容。
	OutputTokens int `json:"outputTokens"`

	// EstimatedCost 是基于 Token 用量和模型定价预估的费用（单位：美元）。
	// 用于帮助用户监控每次审查的成本开销。
	EstimatedCost float64 `json:"estimatedCost"`
}

// ReviewerStatus 追踪审查者在并行执行过程中的进度状态。
// 用于终端 UI 展示每个审查者的实时工作状态，包括开始时间、结束时间、
// 耗时以及 Token 消耗情况。
type ReviewerStatus struct {
	// ReviewerID 标识该状态所属的审查者，对应 Reviewer.ID。
	ReviewerID string

	// Status 表示审查者当前的工作状态。
	// 可选值："pending"（等待中）、"thinking"（思考中）、"done"（已完成）、
	// "failed"（本阶段失败，已跳过）。
	// UI 层根据此字段展示不同的状态图标和样式。
	Status string

	// StartTime 是审查者开始处理的 Unix 毫秒时间戳。
	// 用于计算耗时和在 UI 中展示实时计时器。
	StartTime int64

	// EndTime 是审查者完成处理的 Unix 毫秒时间戳。
	// 仅在 Status 为 "done" 时有值。
	EndTime int64

	// Duration 是审查者处理耗时，单位为秒。
	// 由 EndTime - StartTime 计算得出，方便直接展示。
	Duration float64

	// InputTokens 是本次执行中消耗的输入 Token 数。
	InputTokens int

	// OutputTokens 是本次执行中生成的输出 Token 数。
	OutputTokens int

	// EstimatedCost 是本次执行的预估费用（单位：美元）。
	EstimatedCost float64
}

// OrchestratorOptions 控制辩论行为的配置选项。
// 通过这些选项可以调整辩论的轮数上限、是否启用共识检测、输出语言
// 以及结构化模式等行为。
type OrchestratorOptions struct {
	// MaxRounds 限制辩论的最大轮数。达到此轮数后辩论将强制结束，
	// 无论是否已达成共识。用于防止无限辩论消耗过多资源。
	MaxRounds int

	// CheckConvergence 决定是否在每轮结束后检查审查者之间是否已达成共识。
	// 若为 true，当检测到共识时辩论会提前终止，节省 Token 消耗。
	CheckConvergence bool

	// Language 指定输出语言代码（如 "zh" 表示中文、"ja" 表示日文）。
	// 空字符串表示使用默认的英文输出。影响审查报告和结论的生成语言。
	Language string

	// StructurizeMode 控制问题结构化的处理模式。
	// "legacy" 模式使用传统的全量解析方式，"ledger" 模式使用增量台账方式。
	// 空值时按 "legacy" 处理以保持向后兼容。
	StructurizeMode string

	// PreviousComments 保存上一轮 CodeDebate review 仍处于 active 状态的评论摘要。
	// 审查者在首轮会优先核查这些问题是否已修复，仅在必要时再提出新的 blocking 问题。
	PreviousComments string
}

// OrchestratorConfig 保存创建 DebateOrchestrator 所需的全部配置。
// 包含多个审查者、一个分析器、一个总结器、可选的上下文收集器以及编排选项。
// 该结构体是构造辩论编排器的唯一入口参数。
type OrchestratorConfig struct {
	// Reviewers 是参与辩论的审查者列表。至少需要两个审查者才能形成有效的辩论。
	// 列表中的每个审查者会在每轮辩论中依次发言，互相评价和回应。
	Reviewers []Reviewer

	// Analyzer 是预分析器角色，负责在辩论正式开始前对代码变更进行初步分析。
	// 分析结果会作为背景信息提供给所有审查者，帮助他们更好地理解变更内容。
	Analyzer Reviewer

	// Summarizer 是总结器角色，负责在辩论过程中判断共识是否达成，
	// 以及在辩论结束后生成最终的综合结论报告。
	Summarizer Reviewer

	// ContextGatherer 是可选的上下文收集器接口实例。
	// 当不为 nil 时，编排器会在辩论前调用它收集代码仓库中与变更相关的上下文信息。
	// 为 nil 时表示禁用上下文收集功能。
	ContextGatherer ContextGathererInterface

	// Options 包含控制辩论行为的各项配置选项。
	Options OrchestratorOptions
}

// ContextGathererInterface 抽象了上下文收集功能，避免与具体实现之间产生循环导入。
// 实现者负责从代码仓库中收集与变更相关的上下文信息（如调用链、受影响模块等），
// 为辩论审查者提供更全面的代码理解。
type ContextGathererInterface interface {
	// Gather 执行上下文收集操作，根据 diff 内容从代码仓库中提取相关上下文。
	// 参数：
	//   - ctx: 上下文对象，用于控制超时和取消操作
	//   - diff: 代码变更的 diff 文本，收集器据此分析涉及的符号和文件
	//   - prNumber: PR 编号字符串，用于查询相关的历史 PR 信息
	//   - baseBranch: 目标分支名称，用于确定代码仓库的基准状态
	// 返回 GatheredContext 指针和可能的错误。当收集失败时应返回 error，
	// 编排器会根据情况决定是否跳过上下文继续辩论。
	Gather(ctx context.Context, diff, prNumber, baseBranch string) (*GatheredContext, error)
}

// GatheredContext 保存收集到的代码上下文数据。
// 包含变更摘要、符号引用关系、受影响的模块和相关的历史 PR。
// 这些信息会被注入到审查者的提示词中，帮助其进行更深入的审查。
type GatheredContext struct {
	// Summary 是对收集到的上下文信息的简要概述，
	// 描述了变更涉及的主要模块、关键符号和潜在影响范围。
	Summary string

	// RawReferences 是变更中涉及的符号（函数、类型、变量等）的原始引用列表。
	// 记录了每个符号在代码仓库中被引用的具体位置，帮助审查者理解变更的影响范围。
	RawReferences []RawReference

	// AffectedModules 是受代码变更影响的模块列表。
	// 按影响级别分类，帮助审查者评估变更的波及范围。
	AffectedModules []AffectedModule

	// RelatedPRs 是与当前变更相关的已合并历史 PR 列表。
	// 提供历史背景参考，帮助审查者理解变更的演进脉络。
	RelatedPRs []RelatedPR
}

// AffectedModule 表示受代码变更影响的模块。
// 包含模块名称、路径、受影响的文件列表和影响级别，
// 帮助审查者快速定位变更的影响范围。
type AffectedModule struct {
	// Name 是模块的名称，通常为目录名或包名。
	Name string

	// Path 是模块在代码仓库中的相对路径。
	Path string

	// AffectedFiles 是该模块中受变更影响的文件路径列表。
	AffectedFiles []string

	// ImpactLevel 表示变更对该模块的影响级别。
	// 可选值："core"（核心影响，直接修改）、"moderate"（中等影响，间接依赖）、
	// "peripheral"（外围影响，弱关联）。
	ImpactLevel string
}

// RelatedPR 表示与当前变更相关的已合并的历史 PR。
// 为审查者提供历史背景参考，帮助理解当前变更的来龙去脉。
type RelatedPR struct {
	// Number 是 PR 编号（如 GitHub/GitLab 上的 PR/MR 编号）。
	Number int

	// Title 是 PR 标题，概括该历史变更的内容。
	Title string
}

// RawReference 描述一个符号在代码仓库中被引用的位置。
// 用于追踪变更中涉及的函数、类型、变量等符号在其他文件中的调用关系，
// 帮助审查者评估变更对下游代码的影响。
type RawReference struct {
	// Symbol 是被引用的符号名称（如函数名、类型名、变量名等）。
	Symbol string

	// FoundInFiles 是该符号在代码仓库中被引用的具体位置列表。
	// 每个位置包含文件路径、行号和上下文内容。
	FoundInFiles []ReferenceLocation
}

// ReferenceLocation 表示单个引用位置的详细信息。
// 包含文件路径、行号和该行的上下文内容，用于精确定位符号的使用位置。
type ReferenceLocation struct {
	// File 是引用所在的文件路径（相对于代码仓库根目录）。
	File string

	// Line 是引用所在的行号（从 1 开始计数）。
	Line int

	// Content 是该行的源代码内容，提供引用的上下文以便快速理解用法。
	Content string
}

// DebateResult 是辩论编排的最终输出结果。
// 汇总了整个辩论过程的所有数据：预分析报告、上下文信息、对话消息、
// 审查者总结、最终结论、Token 消耗、是否达成共识以及结构化的问题列表。
// 该结构体会被序列化为 JSON 用于存档和展示。
type DebateResult struct {
	// PRNumber 是被审查的 PR/MR 编号字符串。
	// 用于标识本次辩论结果对应的具体代码变更。
	PRNumber string `json:"prNumber"`

	// Analysis 是预分析器（Analyzer）在辩论前生成的代码变更分析报告。
	// 包含对 diff 内容的初步解读和关键变更点的提炼。
	Analysis string `json:"analysis"`

	// Context 是上下文收集器收集到的代码上下文数据。
	// 当未启用上下文收集或收集失败时为 nil。omitempty 表示为 nil 时省略序列化。
	Context *GatheredContext `json:"context,omitempty"`

	// Messages 是辩论过程中所有审查者发送的消息列表，按时间顺序排列。
	// 完整记录了辩论的全部对话过程。
	Messages []DebateMessage `json:"messages"`

	// Summaries 是每个审查者在辩论结束后生成的最终总结列表。
	Summaries []DebateSummary `json:"summaries"`

	// FinalConclusion 是总结器（Summarizer）根据所有辩论内容生成的最终综合结论。
	// 融合了所有审查者的观点，给出统一的审查建议。
	FinalConclusion string `json:"finalConclusion"`

	// TokenUsage 是每个审查者的 Token 消耗统计列表。
	// 用于事后分析每次审查的成本分布。
	TokenUsage []TokenUsage `json:"tokenUsage"`

	// ConvergedAtRound 记录辩论在第几轮达成共识。
	// 为 nil 表示辩论未提前收敛（达到最大轮数后结束）。
	// omitempty 表示为 nil 时省略序列化。
	ConvergedAtRound *int `json:"convergedAtRound,omitempty"`

	// ParsedIssues 是从辩论结论中解析出的结构化问题列表。
	// 经过去重合并处理，每个问题包含多个审查者的归属信息。
	// omitempty 表示为空时省略序列化。
	ParsedIssues []MergedIssue `json:"parsedIssues,omitempty"`
}

// ReviewIssue 表示审查者提出的单个结构化问题。
// 包含严重程度、分类、文件位置、标题、描述和建议修复方案。
// 该结构体是从审查者自由文本响应中解析出的标准化问题格式。
type ReviewIssue struct {
	// Severity 表示问题的严重程度。
	// 常见值："critical"（严重）、"major"（重要）、"minor"（轻微）、"suggestion"（建议）。
	Severity string `json:"severity"`

	// Category 表示问题的分类标签。
	// 常见值："bug"（缺陷）、"security"（安全）、"performance"（性能）、
	// "style"（代码风格）、"design"（设计）等。
	Category string `json:"category"`

	// File 是问题所在的文件路径（相对于代码仓库根目录）。
	File string `json:"file"`

	// Line 是问题所在的起始行号。为 nil 表示问题不涉及具体行号（如架构层面的问题）。
	Line *int `json:"line,omitempty"`

	// EndLine 是问题涉及的结束行号。与 Line 配合表示一个代码范围。
	// 为 nil 表示问题仅涉及单行或无具体行号。
	EndLine *int `json:"endLine,omitempty"`

	// Title 是问题的简短标题，一句话概括问题的核心内容。
	Title string `json:"title"`

	// Description 是问题的详细描述，说明问题的原因、影响和上下文。
	Description string `json:"description"`

	// SuggestedFix 是建议的修复方案或代码改进建议。
	// 可能包含代码示例。为空字符串表示未提供修复建议。
	SuggestedFix string `json:"suggestedFix,omitempty"`

	// CodeSnippet 是与问题相关的代码片段，帮助理解问题的具体位置和上下文。
	CodeSnippet string `json:"codeSnippet,omitempty"`

	// ClaimedBy 记录原始模型输出中"声称由哪些 reviewer 提出"该问题。
	// JSON 序列化时使用 "raisedBy" 键名，与前端展示保持一致。
	// 注意：这是模型自我声称的归属，可能不完全准确，实际归属以 MergedIssue.RaisedBy 为准。
	ClaimedBy []string `json:"raisedBy,omitempty"`
}

// IssueMention 表示某个 reviewer 对 issue 的一次本地提及或状态变更。
// 在 ledger 模式下，编排器通过追踪每个 reviewer 对 issue 的提及来构建完整的讨论图谱。
type IssueMention struct {
	// ReviewerID 标识发出该提及的审查者。
	ReviewerID string `json:"reviewerId"`

	// LocalIssueID 是该 reviewer 在其本地台账中为此 issue 分配的局部 ID。
	// 格式通常为 "R1-1"（reviewer 1 的第 1 个问题）。
	// omitempty 表示为空时省略序列化。
	LocalIssueID string `json:"localIssueId,omitempty"`

	// Round 是该提及发生的辩论轮次。
	Round int `json:"round,omitempty"`

	// Status 表示该 reviewer 对此 issue 的态度状态。
	// 可选值："active"（激活/新提出）、"retracted"（自行撤回）、
	// "support"（支持）、"withdraw"（撤回支持）、"contest"（反对质疑）。
	Status string `json:"status,omitempty"`
}

// CanonicalSignal 表示 reviewer 对已有 issueRef 的显式态度变化。
// 当 reviewer 在辩论中对其他 reviewer 提出的问题表达明确立场时，
// 会生成该信号用于更新台账中的共识状态。
type CanonicalSignal struct {
	// ReviewerID 标识发出该信号的审查者。
	ReviewerID string `json:"reviewerId"`

	// IssueRef 是被引用的问题标识符，指向台账中某个已存在的 canonical issue。
	// 格式通常为 "R1-1" 等局部 ID。
	IssueRef string `json:"issueRef"`

	// Round 是该信号发生的辩论轮次。
	Round int `json:"round"`

	// Action 表示 reviewer 对该 issue 采取的态度动作。
	// 可选值："support"（表示支持该问题）、"withdraw"（撤回之前的支持）、
	// "contest"（反对/质疑该问题的有效性）。
	Action string `json:"action"`
}

// MergedIssue 是经过去重合并的问题，包含多个审查者的归属信息。
// 当多个审查者提出相似问题时，会合并为一个 MergedIssue，保留最高严重程度。
// 同时记录问题的完整生命周期：由谁首次提出、谁支持、谁撤回、谁反对。
type MergedIssue struct {
	// ReviewIssue 嵌入了基础的审查问题信息，包含严重程度、分类、文件位置等。
	// 合并后会保留最高的严重程度和最完整的描述信息。
	ReviewIssue

	// CanonicalID 是该合并问题在全局台账中的唯一标识符。
	// 在 ledger 模式下由台账管理器分配。
	CanonicalID string `json:"canonicalId,omitempty"`

	// RaisedBy 记录实际提出该问题的审查者 ID 列表。
	// 与 ReviewIssue.ClaimedBy 不同，这是经过编排器验证的真实归属。
	RaisedBy []string `json:"raisedBy"`

	// IntroducedBy 记录最先引入该问题的审查者 ID 列表。
	// 在 ledger 模式下用于区分 "首次提出者" 和 "后续支持者"。
	IntroducedBy []string `json:"introducedBy,omitempty"`

	// SupportedBy 记录明确表示支持该问题的审查者 ID 列表。
	// 支持者越多，表明该问题获得的共识程度越高。
	SupportedBy []string `json:"supportedBy,omitempty"`

	// WithdrawnBy 记录曾经支持但后来撤回支持的审查者 ID 列表。
	WithdrawnBy []string `json:"withdrawnBy,omitempty"`

	// ContestedBy 记录明确反对或质疑该问题有效性的审查者 ID 列表。
	ContestedBy []string `json:"contestedBy,omitempty"`

	// Descriptions 保存所有审查者对该问题的描述文本列表。
	// 合并时保留各审查者的原始描述，提供多角度的问题阐述。
	Descriptions []string `json:"descriptions"`

	// Mentions 记录该问题在辩论过程中被提及的完整历史。
	// 按时间顺序记录每次提及的审查者、轮次和状态变化。
	Mentions []IssueMention `json:"mentions,omitempty"`
}

// ReviewerOutput 是从审查者响应文本中解析出的结构化输出。
// 编排器通过解析审查者的自由文本响应，提取出标准化的问题列表、
// 审查结论和总结摘要，便于后续的合并处理和展示。
type ReviewerOutput struct {
	// Issues 是该审查者在本轮中提出的结构化问题列表。
	Issues []ReviewIssue `json:"issues"`

	// Verdict 是该审查者的审查结论。
	// 可选值："approve"（批准合并）、"request_changes"（要求修改）、
	// "comment"（仅评论，不做明确判断）。
	Verdict string `json:"verdict"`

	// Summary 是该审查者对本轮审查的简要总结。
	Summary string `json:"summary"`
}

// StructurizeDelta 表示单个 reviewer 在单轮中的 issue 增量变化。
// 在 ledger 模式下，每个 reviewer 的每轮输出会被解析为增量 delta，
// 而非全量的 issue 列表，从而支持跨轮次的问题追踪和状态演进。
type StructurizeDelta struct {
	// Add 是本轮新发现的问题列表。每个新问题会被分配一个局部 ID 并加入台账。
	Add []DeltaAddIssue `json:"add"`

	// Retract 是本轮被 reviewer 自行撤回的问题 ID 列表。
	// 当 reviewer 在后续轮次中认为之前提出的某个问题不再成立时使用。
	Retract []string `json:"retract"`

	// Update 是本轮对已有问题的字段更新列表。
	// reviewer 可以修改之前提出的问题的严重程度、描述等字段。
	Update []DeltaUpdateIssue `json:"update"`

	// Support 是本轮 reviewer 表示支持的其他 reviewer 提出的问题引用列表。
	Support []DeltaIssueRefAction `json:"support"`

	// Withdraw 是本轮 reviewer 撤回之前支持的问题引用列表。
	Withdraw []DeltaIssueRefAction `json:"withdraw"`

	// Contest 是本轮 reviewer 反对/质疑的问题引用列表。
	Contest []DeltaIssueRefAction `json:"contest"`
}

// DeltaAddIssue 表示新发现的问题（由本地 ledger 分配 ID）。
// 包含新问题的完整信息，编排器会在收到后为其分配局部 ID 并注册到台账中。
type DeltaAddIssue struct {
	// Severity 表示新问题的严重程度。
	Severity string `json:"severity"`

	// Category 表示新问题的分类标签。为空时表示未分类。
	Category string `json:"category,omitempty"`

	// File 是新问题所在的文件路径。
	File string `json:"file"`

	// Line 是新问题所在的起始行号。为 nil 表示不涉及具体行号。
	Line *int `json:"line,omitempty"`

	// Title 是新问题的简短标题。
	Title string `json:"title"`

	// Description 是新问题的详细描述。
	Description string `json:"description"`

	// SuggestedFix 是建议的修复方案。为空字符串表示未提供。
	SuggestedFix string `json:"suggestedFix,omitempty"`
}

// DeltaUpdateIssue 表示对已有问题（按 ID）的字段更新。
// 所有字段均为指针类型，nil 表示不更新该字段，非 nil 表示更新为新值。
// 这种设计允许 reviewer 只更新问题的部分字段而保留其他字段不变。
type DeltaUpdateIssue struct {
	// ID 是要更新的问题的局部 ID（如 "R1-1"）。这是必填字段。
	ID string `json:"id"`

	// Severity 是更新后的严重程度。为 nil 表示保持原值不变。
	Severity *string `json:"severity,omitempty"`

	// Category 是更新后的分类标签。为 nil 表示保持原值不变。
	Category *string `json:"category,omitempty"`

	// File 是更新后的文件路径。为 nil 表示保持原值不变。
	File *string `json:"file,omitempty"`

	// Line 是更新后的行号。为 nil 表示保持原值不变。
	Line *int `json:"line,omitempty"`

	// Title 是更新后的标题。为 nil 表示保持原值不变。
	Title *string `json:"title,omitempty"`

	// Description 是更新后的描述。为 nil 表示保持原值不变。
	Description *string `json:"description,omitempty"`

	// SuggestedFix 是更新后的修复建议。为 nil 表示保持原值不变。
	SuggestedFix *string `json:"suggestedFix,omitempty"`
}

// DeltaIssueRefAction 表示对已存在 issueRef 的显式态度动作。
// 用于 StructurizeDelta 中的 Support、Withdraw、Contest 字段，
// 表示 reviewer 对某个已有问题的立场声明。
type DeltaIssueRefAction struct {
	// IssueRef 是被引用的问题标识符（如 "R1-1"、"R2-3" 等），
	// 指向台账中某个已存在的 issue。
	IssueRef string `json:"issueRef"`
}

// DisplayCallbacks 是终端显示集成的回调接口。
// 编排器在执行过程中调用这些回调方法来实时更新终端 UI。
// 包括：等待状态、消息输出、并行执行状态、轮次完成、共识判断和上下文收集完成等事件。
// 实现者应确保回调方法是线程安全的，因为多个审查者可能并行执行。
type DisplayCallbacks interface {
	// OnWaiting 在审查者开始等待 AI 响应时被调用。
	// reviewerID 标识正在等待的审查者。UI 层通常会展示加载动画。
	OnWaiting(reviewerID string)

	// OnMessageChunk 在接收到审查者的流式响应片段时被调用。
	// 用于实现流式输出效果，逐步展示审查者的思考过程。
	// reviewerID 标识响应来源，chunk 是本次接收到的文本片段。
	OnMessageChunk(reviewerID string, chunk string)

	// OnMessage 在审查者的完整响应接收完毕后被调用。
	// reviewerID 标识响应来源，content 是完整的响应文本。
	OnMessage(reviewerID string, content string)

	// OnParallelStatus 在并行执行的审查者状态发生变化时被调用。
	// round 是当前辩论轮次，statuses 包含所有审查者的最新状态。
	// UI 层据此更新并行执行的进度面板。
	OnParallelStatus(round int, statuses []ReviewerStatus)

	// OnSummaryStatus 在总结阶段的状态发生变化时被调用。
	// statuses 包含总结相关任务的状态信息。
	OnSummaryStatus(statuses []ReviewerStatus)

	// OnRoundComplete 在每轮辩论结束时被调用。
	// round 是刚完成的轮次编号，converged 指示该轮是否检测到共识。
	OnRoundComplete(round int, converged bool)

	// OnConvergenceJudgment 在共识检测完成后被调用。
	// verdict 是检测结论（如 "converged" 或 "not_converged"），
	// reasoning 是总结器给出的判断理由。
	OnConvergenceJudgment(verdict string, reasoning string)

	// OnContextGathered 在上下文收集完成后被调用。
	// ctx 包含收集到的完整上下文数据，UI 层可据此展示上下文摘要。
	// 当 ctx 为 nil 时表示上下文收集被跳过或失败。
	OnContextGathered(ctx *GatheredContext)
}
