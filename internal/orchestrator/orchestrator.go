// Package orchestrator 实现了 CodeDebate 的核心辩论编排逻辑。
//
// 该包负责协调多个 AI 审查者（Reviewer）进行多轮对抗性辩论式代码审查。
// 核心思想是：通过让多个 AI 审查者互相挑战和验证对方的观点，可以发现单个审查者
// 容易遗漏的代码问题，提高代码审查的覆盖度和准确性。
//
// 整体架构分为三个阶段：
//   - 阶段1（分析阶段）：并行执行上下文收集和代码预分析
//   - 阶段2（辩论阶段）：多轮辩论，每轮所有审查者并行执行，可选共识检测提前终止
//   - 阶段3（总结阶段）：收集审查者总结，生成最终结论，提取结构化问题列表
package orchestrator

import (
	"context"
	"errors"
	"fmt"
	"math"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"
	"unicode"

	"github.com/longyunfeigu/codedebate/internal/prompt"
	"github.com/longyunfeigu/codedebate/internal/provider"
	"github.com/longyunfeigu/codedebate/internal/schema"
	"github.com/longyunfeigu/codedebate/internal/util"
	"golang.org/x/sync/errgroup"
)

// tokenCount 追踪单个审查者的输入/输出 Token 计数。
//
// 用于估算每个审查者的 API 调用成本。输入 Token 包括系统提示词和用户消息，
// 输出 Token 是模型生成的响应内容。Token 数量通过 estimateTokens 函数粗略估算，
// 而非使用精确的 tokenizer，因此仅用于成本参考而非精确计费。
type tokenCount struct {
	input  int // 累计输入 Token 数（包括系统提示词、用户消息等）
	output int // 累计输出 Token 数（模型生成的响应内容）
}

// DebateOrchestrator 运行多审查者辩论式代码审查的核心编排器。
// 它协调多个AI审查者进行多轮辩论，通过对抗性讨论发现代码中的问题。
//
// 核心流程：
//  1. 并行执行上下文收集和代码预分析
//  2. 多轮辩论：每轮所有审查者并行执行，互相挑战对方的观点
//  3. 收集总结并生成最终结论和结构化问题列表
type DebateOrchestrator struct {
	reviewers       []Reviewer               // 参与辩论的审查者列表
	analyzer        Reviewer                 // 预分析器
	summarizer      Reviewer                 // 总结器/共识判断器
	contextGatherer ContextGathererInterface // 上下文收集器

	options OrchestratorOptions // 辩论行为配置

	// RunStreaming 不再复用这些字段；单次执行状态保存在 debateRun 中。
	// 保留这些字段是为了兼容同包测试和直接调用内部 helper 的场景。
	conversationHistory []DebateMessage
	tokenUsage          map[string]*tokenCount
	analysis            string
	gatheredContext     *GatheredContext
	taskPrompt          string
	lastSeenIndex       map[string]int
	issueLedgers        map[string]*IssueLedger
	canonicalSignals    []CanonicalSignal
}

// debateRun 保存单次 review 执行期的可变状态。
//
// 设计动机：将单次执行的可变状态从 DebateOrchestrator 中分离出来，
// 使得同一个 DebateOrchestrator 实例可以安全地执行多次 review，而不会互相干扰。
// debateRun 通过嵌入 *DebateOrchestrator 继承其配置（reviewers、options 等），
// 但拥有独立的对话历史、Token 计数等运行时状态。
type debateRun struct {
	*DebateOrchestrator                         // 嵌入编排器，继承配置信息（reviewers、analyzer、summarizer 等）
	conversationHistory []DebateMessage         // 完整的辩论对话历史，按时间顺序记录所有审查者的发言
	tokenUsage          map[string]*tokenCount  // 每个审查者（含 analyzer/summarizer）的 Token 使用量映射
	analysis            string                  // 预分析器（analyzer）生成的代码分析结果，用于指导审查者关注重点
	gatheredContext     *GatheredContext        // 从代码仓库中收集的上下文信息（调用链、模块关系等）
	taskPrompt          string                  // 原始任务提示词，包含代码 diff 和审查要求
	currentRound        int                     // 当前正在执行的辩论轮次，用于后期轮次切换到收敛导向 prompt
	lastSeenIndex       map[string]int          // 每个审查者最后看到的消息在 conversationHistory 中的索引，用于会话模式的增量消息计算
	failedReviewers     map[string]error        // 已在辩论阶段失败的审查者，后续轮次和总结阶段会跳过
	issueLedgers        map[string]*IssueLedger // 每个审查者的问题账本，用于 ledger 模式下的增量问题追踪
	canonicalSignals    []CanonicalSignal       // 跨审查者的规范化信号（support/withdraw/contest），用于问题去重和置信度计算

	mu sync.Mutex // 保护 tokenUsage 和 canonicalSignals 的并发访问锁
}

// New 根据给定的配置创建一个新的 DebateOrchestrator 实例。
//
// 参数：
//   - cfg: 编排器配置，包含审查者列表、分析器、总结器、上下文收集器和行为选项
//
// 返回：
//   - *DebateOrchestrator: 初始化完成的编排器实例，可直接调用 RunStreaming 执行审查
//
// 注意：tokenUsage 和 lastSeenIndex 在此处初始化为空 map，是为了兼容旧的 legacyRun 路径。
// 新的 RunStreaming 路径会通过 newRun 创建独立的 debateRun 实例，不依赖这些字段。
func New(cfg OrchestratorConfig) *DebateOrchestrator {
	return &DebateOrchestrator{
		reviewers:       cfg.Reviewers,
		analyzer:        cfg.Analyzer,
		summarizer:      cfg.Summarizer,
		contextGatherer: cfg.ContextGatherer,
		options:         cfg.Options,
		tokenUsage:      make(map[string]*tokenCount),
		lastSeenIndex:   make(map[string]int),
	}
}

// newRun 创建一个新的 debateRun 实例，用于一次完整的 review 执行。
//
// 参数：
//   - prompt: 包含代码 diff 的完整审查提示词
//
// 返回：
//   - *debateRun: 拥有独立可变状态的执行实例，嵌入当前编排器的配置
//
// 设计决策：每次 RunStreaming 调用都创建新的 debateRun，确保并发安全和状态隔离。
func (o *DebateOrchestrator) newRun(prompt string) *debateRun {
	return &debateRun{
		DebateOrchestrator: o,
		tokenUsage:         make(map[string]*tokenCount),
		lastSeenIndex:      make(map[string]int),
		failedReviewers:    make(map[string]error),
		taskPrompt:         prompt,
	}
}

// legacyRun 将 DebateOrchestrator 的旧式字段封装为 debateRun 实例。
//
// 返回：
//   - *debateRun: 引用编排器旧式字段的执行实例
//
// 兼容性说明：这是一个过渡方法，用于让旧的同包测试和直接调用内部 helper 的代码
// 能够继续工作。新代码应使用 newRun 而非 legacyRun。legacyRun 会直接引用
// DebateOrchestrator 上的可变字段（而非创建副本），因此调用方需要在完成后
// 通过 syncLegacyRun 将修改同步回编排器。
func (o *DebateOrchestrator) legacyRun() *debateRun {
	tokenUsage := o.tokenUsage
	if tokenUsage == nil {
		tokenUsage = make(map[string]*tokenCount)
	}
	lastSeen := o.lastSeenIndex
	if lastSeen == nil {
		lastSeen = make(map[string]int)
	}
	return &debateRun{
		DebateOrchestrator:  o,
		conversationHistory: o.conversationHistory,
		tokenUsage:          tokenUsage,
		analysis:            o.analysis,
		gatheredContext:     o.gatheredContext,
		taskPrompt:          o.taskPrompt,
		lastSeenIndex:       lastSeen,
		failedReviewers:     make(map[string]error),
		issueLedgers:        o.issueLedgers,
		canonicalSignals:    o.canonicalSignals,
	}
}

// syncLegacyRun 将 debateRun 中的可变状态同步回 DebateOrchestrator。
//
// 参数：
//   - run: 通过 legacyRun 创建的执行实例
//
// 这是 legacyRun 的配套方法。由于 legacyRun 创建的 debateRun 持有的是独立的
// map/slice 引用，执行过程中可能被替换为新的引用（如 append 导致的重新分配），
// 因此需要在执行完成后将最新的引用同步回编排器。
func (o *DebateOrchestrator) syncLegacyRun(run *debateRun) {
	o.conversationHistory = run.conversationHistory
	o.tokenUsage = run.tokenUsage
	o.analysis = run.analysis
	o.gatheredContext = run.gatheredContext
	o.taskPrompt = run.taskPrompt
	o.lastSeenIndex = run.lastSeenIndex
	o.issueLedgers = run.issueLedgers
	o.canonicalSignals = run.canonicalSignals
}

// RunStreaming 执行完整的辩论循环，支持并行审查者执行和流式输出。
// 这是CodeDebate的核心算法，包含三个阶段：
//
//	阶段1：并行执行上下文收集和代码预分析
//	阶段2：多轮辩论，每轮所有审查者并行执行，可选共识检测提前终止
//	阶段3：收集审查者总结，生成最终结论，提取结构化问题
//
// 参数：
//   - label: 任务标识（如PR编号），用于会话标记
//   - prompt: 包含代码diff的完整审查提示词
//   - display: 终端UI回调接口，用于实时更新显示
func (o *DebateOrchestrator) RunStreaming(ctx context.Context, label, prompt string, display DisplayCallbacks) (*DebateResult, error) {
	run := o.newRun(prompt)
	run.startSessions(label)
	defer run.endAllSessions()

	// 阶段1: 并行执行上下文收集和预分析
	if err := run.runAnalysisPhase(ctx, label, prompt, display); err != nil {
		return nil, err
	}

	// 阶段2: 多轮辩论
	convergedAtRound, err := run.runDebatePhase(ctx, display)
	if err != nil {
		return nil, err
	}

	// 阶段3: 总结 + 结论 + 问题提取
	return run.runSummaryPhase(ctx, label, display, convergedAtRound)
}

// checkConvergence 是 DebateOrchestrator 级别的共识检测代理方法。
//
// 通过 legacyRun/syncLegacyRun 模式将调用委托给 debateRun 的同名方法。
// 此方法主要为旧测试代码保留，新代码应直接通过 debateRun 调用。
//
// 参数：
//   - ctx: 上下文，用于超时和取消控制
//   - display: UI 回调接口，用于通知共识判定结果
//
// 返回：
//   - bool: 是否达成共识
//   - error: 执行错误（非共识判定本身的失败）
func (o *DebateOrchestrator) checkConvergence(ctx context.Context, display DisplayCallbacks) (bool, error) {
	run := o.legacyRun()
	converged, err := run.checkConvergence(ctx, display)
	o.syncLegacyRun(run)
	return converged, err
}

// collectSummaries 是 DebateOrchestrator 级别的总结收集代理方法。
//
// 通过 legacyRun/syncLegacyRun 模式委托给 debateRun 的同名方法。
// 此方法主要为旧测试代码保留。
//
// 参数：
//   - ctx: 上下文，用于超时和取消控制
//   - display: UI 回调接口
//
// 返回：
//   - []DebateSummary: 每个审查者的最终总结
//   - error: 执行错误
func (o *DebateOrchestrator) collectSummaries(ctx context.Context, display DisplayCallbacks) ([]DebateSummary, error) {
	run := o.legacyRun()
	summaries, err := run.collectSummaries(ctx, display)
	o.syncLegacyRun(run)
	return summaries, err
}

// structurizeIssues 是 DebateOrchestrator 级别的问题结构化代理方法。
//
// 通过 legacyRun/syncLegacyRun 模式委托给 debateRun 的同名方法。
// 此方法主要为旧测试代码保留。
//
// 参数：
//   - ctx: 上下文
//   - display: UI 回调接口
//
// 返回：
//   - []MergedIssue: 去重合并后的结构化问题列表
func (o *DebateOrchestrator) structurizeIssues(ctx context.Context, display DisplayCallbacks) []MergedIssue {
	run := o.legacyRun()
	issues := run.structurizeIssues(ctx, display)
	o.syncLegacyRun(run)
	return issues
}

// extractRoundIssueDeltas 是 DebateOrchestrator 级别的增量问题提取代理方法。
//
// 通过 legacyRun/syncLegacyRun 模式委托给 debateRun 的同名方法。
// 此方法主要为旧测试代码保留。
//
// 参数：
//   - ctx: 上下文
//   - round: 当前辩论轮次
//   - roundOutputs: 本轮各审查者的输出内容映射（reviewerID -> content）
//   - display: UI 回调接口
func (o *DebateOrchestrator) extractRoundIssueDeltas(ctx context.Context, round int, roundOutputs map[string]string, display DisplayCallbacks) {
	run := o.legacyRun()
	run.extractRoundIssueDeltas(ctx, round, roundOutputs, display)
	o.syncLegacyRun(run)
}

// structurizeIssuesFromLedgers 是 DebateOrchestrator 级别的 ledger 模式问题结构化代理方法。
//
// 通过 legacyRun/syncLegacyRun 模式委托给 debateRun 的同名方法。
// 此方法主要为旧测试代码保留。
//
// 参数：
//   - ctx: 上下文
//   - display: UI 回调接口
//
// 返回：
//   - []MergedIssue: 从 ledger 中提取并规范化后的问题列表
func (o *DebateOrchestrator) structurizeIssuesFromLedgers(ctx context.Context, display DisplayCallbacks) []MergedIssue {
	run := o.legacyRun()
	issues := run.structurizeIssuesFromLedgers(ctx, display)
	o.syncLegacyRun(run)
	return issues
}

// initIssueLedgers 是 DebateOrchestrator 级别的 ledger 初始化代理方法。
//
// 通过 legacyRun/syncLegacyRun 模式委托给 debateRun 的同名方法。
// 此方法主要为旧测试代码保留。
func (o *DebateOrchestrator) initIssueLedgers() {
	run := o.legacyRun()
	run.initIssueLedgers()
	o.syncLegacyRun(run)
}

// mergeAllLedgers 是 DebateOrchestrator 级别的 ledger 合并代理方法。
//
// 通过 legacyRun/syncLegacyRun 模式委托给 debateRun 的同名方法。
// 此方法主要为旧测试代码保留。
//
// 返回：
//   - []MergedIssue: 所有审查者 ledger 合并后的问题列表
func (o *DebateOrchestrator) mergeAllLedgers() []MergedIssue {
	run := o.legacyRun()
	issues := run.mergeAllLedgers()
	o.syncLegacyRun(run)
	return issues
}

// startSessions 为所有支持会话模式的 AI 提供者启动会话。
//
// 参数：
//   - label: 任务标识（如 PR 编号），用于给会话命名，便于在提供者端追踪和区分
//
// 设计说明：并非所有提供者都支持会话模式。通过类型断言 provider.SessionProvider
// 来判断是否支持。会话模式的优势是提供者可以在服务端维护对话上下文，
// 后续轮次只需发送增量消息（而非完整历史），可以显著减少 Token 消耗和延迟。
// 会话命名格式为 "CodeDebate | {label} | {角色}"，方便在提供者端按任务和角色检索。
func (o *debateRun) startSessions(label string) {
	for _, r := range o.reviewers {
		if sp, ok := r.Provider.(provider.SessionProvider); ok {
			sp.StartSession(fmt.Sprintf("CodeDebate | %s | reviewer:%s", label, r.ID))
		}
	}
	if sp, ok := o.analyzer.Provider.(provider.SessionProvider); ok {
		sp.StartSession(fmt.Sprintf("CodeDebate | %s | analyzer", label))
	}
	if sp, ok := o.summarizer.Provider.(provider.SessionProvider); ok {
		sp.StartSession(fmt.Sprintf("CodeDebate | %s | summarizer", label))
	}
}

// runAnalysisPhase 执行阶段1：并行执行上下文收集和代码预分析。
//
// 参数：
//   - ctx: 上下文，用于超时和取消控制
//   - label: 任务标识（如 PR 编号），传递给上下文收集器
//   - prompt: 包含代码 diff 的完整审查提示词
//   - display: UI 回调接口，用于实时更新显示状态
//
// 返回：
//   - error: 仅在外部取消/超时时返回错误；上下文收集和预分析失败都按非致命处理
//
// 设计说明：上下文收集和预分析是两个完全独立的操作，互不依赖，因此使用 errgroup
// 并行执行以减少总耗时。上下文收集从代码仓库中提取调用链、模块关系等信息；
// 预分析使用 analyzer 对代码变更进行初步分析，为后续审查者提供关注重点。
func (o *debateRun) runAnalysisPhase(ctx context.Context, label, prompt string, display DisplayCallbacks) error {
	g, gctx := errgroup.WithContext(ctx)

	// 上下文收集：从代码仓库中提取与变更相关的调用链、模块关系等信息
	if o.contextGatherer != nil {
		g.Go(func() error {
			display.OnWaiting("context-gatherer")
			diff := extractDiffFromPrompt(prompt)
			gathered, err := o.contextGatherer.Gather(gctx, diff, label, "main")
			if err != nil {
				// 上下文收集失败是非致命的，不影响核心审查流程
				return nil
			}
			o.gatheredContext = gathered
			return nil
		})
	}

	// 预分析：使用分析器对代码变更进行初步分析，为审查者提供关注重点
	g.Go(func() error {
		display.OnWaiting("analyzer")
		msgs := []provider.Message{{Role: "user", Content: prompt}}

		ch, errCh := o.analyzer.Provider.ChatStream(gctx, msgs, o.analyzer.SystemPrompt)
		var sb strings.Builder
		for chunk := range ch {
			sb.WriteString(chunk)
			display.OnMessageChunk("analyzer", chunk)
		}
		if err := <-errCh; err != nil {
			if isContextCancellation(err, gctx) {
				return err
			}
			util.Warnf("runAnalysisPhase: analyzer failed, continuing without analyzer guidance: %v", err)
			return nil
		}
		o.analysis = sb.String()
		display.OnMessage("analyzer", o.analysis)
		o.trackTokens("analyzer", prompt+o.analyzer.SystemPrompt, o.analysis)
		return nil
	})

	if err := g.Wait(); err != nil {
		return err
	}

	// 通知UI层上下文收集已完成，可以展示相关信息
	if o.gatheredContext != nil {
		display.OnContextGathered(o.gatheredContext)
	}

	return nil
}

// runDebatePhase 执行阶段2：多轮辩论，返回达成共识的轮次号。
//
// 参数：
//   - ctx: 上下文，用于超时和取消控制
//   - display: UI 回调接口
//
// 返回：
//   - *int: 达成共识的轮次号；如果辩论打满所有轮次仍未达成共识则返回 nil
//   - error: 仅在所有审查者都未产出任何结果，或外部取消/超时时返回错误
//
// 核心逻辑：
//  1. 如果使用 ledger 模式，先初始化每个审查者的问题账本
//  2. 循环执行最多 MaxRounds 轮辩论
//  3. 每轮结束后，如果启用了共识检测且不是最后一轮，检查是否达成共识
//  4. 达成共识则提前结束辩论循环，节省 API 调用成本
func (o *debateRun) runDebatePhase(ctx context.Context, display DisplayCallbacks) (*int, error) {
	var convergedAtRound *int
	if o.useLedgerStructurize() {
		o.initIssueLedgers()
	}

	for round := 1; round <= o.options.MaxRounds; round++ {
		if len(o.activeReviewers()) == 0 {
			if o.hasReviewerMessages() {
				util.Warnf("runDebatePhase: no reviewers remain after earlier failures, finishing with partial results")
				break
			}
			return nil, fmt.Errorf("all reviewers failed before any review output was produced")
		}

		roundOutputs, err := o.runDebateRound(ctx, round, display)
		if err != nil {
			return nil, err
		}
		if len(roundOutputs) == 0 {
			if o.hasReviewerMessages() {
				util.Warnf("runDebatePhase: round %d produced no successful reviewer output, finishing with partial results", round)
				display.OnRoundComplete(round, false)
				break
			}
			return nil, fmt.Errorf("all reviewers failed before any review output was produced")
		}
		if o.useLedgerStructurize() {
			o.extractRoundIssueDeltas(ctx, round, roundOutputs, display)
		}

		// 共识检测：从第1轮开始（且不是最后一轮）检查审查者是否已达成共识
		// Round 1 独立审查后也可能达成一致，无需强制进入第2轮
		converged := false
		if o.options.CheckConvergence && round >= 1 && round < o.options.MaxRounds {
			display.OnWaiting("convergence-check")
			var err error
			converged, err = o.checkConvergence(ctx, display)
			if err != nil {
				// 共识检测失败是非致命的，继续下一轮辩论
				converged = false
			}
			if converged {
				r := round
				convergedAtRound = &r
			}
		}

		display.OnRoundComplete(round, converged)

		if converged {
			break
		}
		if len(o.activeReviewers()) <= 1 {
			util.Warnf("runDebatePhase: %d reviewer remains after round %d, skipping remaining debate rounds", len(o.activeReviewers()), round)
			break
		}
	}

	return convergedAtRound, nil
}

// runDebateRound 执行单轮辩论：构建消息、并行执行所有审查者、收集结果到对话历史。
//
// 参数：
//   - ctx: 上下文，用于超时和取消控制
//   - round: 当前辩论轮次（从1开始）
//   - display: UI 回调接口，用于实时更新审查者状态和流式输出
//
// 返回：
//   - map[string]string: 每个审查者本轮的完整响应（reviewerID -> content），用于后续增量问题提取
//   - error: 仅在外部取消/超时时返回错误；单个审查者失败会被记录并跳过
//
// 关键设计：
//  1. 先为所有审查者构建消息（快照），再并行执行。这确保所有审查者看到完全相同的
//     辩论历史，避免先执行完的审查者的输出影响后执行审查者的输入（保证公平性）。
//  2. 并行执行各审查者，单个审查者失败不会取消其他审查者。
//  3. 所有成功的审查者完成后，按固定顺序将结果添加到对话历史，保证消息顺序的确定性。
func (o *debateRun) runDebateRound(ctx context.Context, round int, display DisplayCallbacks) (map[string]string, error) {
	o.currentRound = round

	// 在执行前为所有审查者构建消息（快照，确保所有审查者看到相同的信息）
	// 这样避免了先执行的审查者的输出影响后执行审查者的输入
	type reviewerTask struct {
		reviewer    Reviewer
		messages    []provider.Message
		inputText   string
		inputTokens int
	}
	activeReviewers := o.activeReviewers()
	tasks := make([]reviewerTask, len(activeReviewers))
	for i, r := range activeReviewers {
		messages := o.buildMessages(r.ID)
		var inputParts []string
		for _, m := range messages {
			inputParts = append(inputParts, m.Content)
		}
		inputText := strings.Join(inputParts, "\n") + r.SystemPrompt
		tasks[i] = reviewerTask{
			reviewer:    r,
			messages:    messages,
			inputText:   inputText,
			inputTokens: estimateTokens(inputText),
		}
	}
	if len(tasks) == 0 {
		return nil, nil
	}

	// 初始化每个审查者的状态追踪，用于UI实时显示
	statuses := make([]ReviewerStatus, len(tasks))
	var statusesMu sync.Mutex
	for i, r := range activeReviewers {
		statuses[i] = ReviewerStatus{
			ReviewerID: r.ID,
			Status:     "pending",
		}
	}

	display.OnWaiting(fmt.Sprintf("round-%d", round))
	display.OnParallelStatus(round, copyStatuses(statuses))

	// 并行执行所有审查者，每个审查者在独立的goroutine中运行
	type roundResult struct {
		reviewer     Reviewer
		fullResponse string
		inputText    string
	}
	results := make([]roundResult, len(tasks))
	var wg sync.WaitGroup
	var roundErr error
	var roundErrMu sync.Mutex
	for i, task := range tasks {
		i, task := i, task
		wg.Add(1)
		go func() {
			defer wg.Done()

			// 标记审查者状态为"思考中"，记录开始时间
			startTime := time.Now().UnixMilli()
			statusesMu.Lock()
			statuses[i] = ReviewerStatus{
				ReviewerID:  task.reviewer.ID,
				Status:      "thinking",
				StartTime:   startTime,
				InputTokens: task.inputTokens,
			}
			statusSnapshot := copyStatuses(statuses)
			statusesMu.Unlock()
			display.OnParallelStatus(round, statusSnapshot)

			// 流式接收审查者的响应，逐块读取
			ch, errCh := task.reviewer.Provider.ChatStream(ctx, task.messages, task.reviewer.SystemPrompt)
			var sb strings.Builder
			for chunk := range ch {
				sb.WriteString(chunk)
				display.OnMessageChunk(task.reviewer.ID, chunk)
			}
			if err := <-errCh; err != nil {
				if isContextCancellation(err, ctx) {
					roundErrMu.Lock()
					if roundErr == nil {
						roundErr = err
					}
					roundErrMu.Unlock()
					return
				}

				endTime := time.Now().UnixMilli()
				statusesMu.Lock()
				statuses[i] = ReviewerStatus{
					ReviewerID:  task.reviewer.ID,
					Status:      "failed",
					StartTime:   startTime,
					EndTime:     endTime,
					Duration:    float64(endTime-startTime) / 1000.0,
					InputTokens: task.inputTokens,
				}
				statusSnapshot = copyStatuses(statuses)
				statusesMu.Unlock()
				display.OnParallelStatus(round, statusSnapshot)

				o.markReviewerFailed(task.reviewer.ID, err)
				util.Warnf("runDebateRound: reviewer %s failed in round %d, continuing with remaining reviewers: %v", task.reviewer.ID, round, err)
				return
			}

			fullResponse := sb.String()
			outputTokens := estimateTokens(fullResponse)

			// 标记审查者状态为"已完成"，记录结束时间和耗时
			endTime := time.Now().UnixMilli()
			statusesMu.Lock()
			statuses[i] = ReviewerStatus{
				ReviewerID:    task.reviewer.ID,
				Status:        "done",
				StartTime:     startTime,
				EndTime:       endTime,
				Duration:      float64(endTime-startTime) / 1000.0,
				InputTokens:   task.inputTokens,
				OutputTokens:  outputTokens,
				EstimatedCost: float64(task.inputTokens+outputTokens) * 0.00001,
			}
			statusSnapshot = copyStatuses(statuses)
			statusesMu.Unlock()
			display.OnParallelStatus(round, statusSnapshot)

			results[i] = roundResult{
				reviewer:     task.reviewer,
				fullResponse: fullResponse,
				inputText:    task.inputText,
			}
		}()
	}

	wg.Wait()
	if roundErr != nil {
		return nil, roundErr
	}
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}

	// 所有审查者完成后，将结果添加到对话历史并通知UI
	// 注意：必须在所有审查者完成后统一添加，保证消息顺序一致
	for _, r := range results {
		if r.reviewer.ID == "" {
			continue
		}
		o.trackTokens(r.reviewer.ID, r.inputText, r.fullResponse)
		o.conversationHistory = append(o.conversationHistory, DebateMessage{
			ReviewerID: r.reviewer.ID,
			Round:      round,
			Content:    r.fullResponse,
			Timestamp:  time.Now(),
		})
		o.markAsSeen(r.reviewer.ID)
		display.OnMessage(r.reviewer.ID, r.fullResponse)
	}

	roundOutputs := make(map[string]string, len(results))
	for _, r := range results {
		roundOutputs[r.reviewer.ID] = r.fullResponse
	}
	return roundOutputs, nil
}

// runSummaryPhase 执行阶段3：收集审查者总结，生成最终结论和结构化问题列表。
//
// 参数：
//   - ctx: 上下文，用于超时和取消控制
//   - label: 任务标识（如 PR 编号），记录到最终结果中
//   - display: UI 回调接口
//   - convergedAtRound: 达成共识的轮次号，nil 表示未达成共识
//
// 返回：
//   - *DebateResult: 完整的辩论结果，包含分析、上下文、对话历史、总结、结论和结构化问题
//   - error: 仅在外部取消/超时时返回错误；总结/结论失败会回退到 partial result
//
// 流程：
//  1. 并行收集每个审查者的最终总结
//  2. 结束总结器的会话（后续的结论生成和问题提取会构建全新消息，不依赖会话上下文）
//  3. 根据配置选择 ledger 模式或 legacy 模式生成最终结论和提取结构化问题
func (o *debateRun) runSummaryPhase(ctx context.Context, label string, display DisplayCallbacks, convergedAtRound *int) (*DebateResult, error) {
	// 辩论结束后，收集每个审查者的最终总结，然后由总结器生成统一结论
	display.OnWaiting("reviewer-summaries")
	summaries, err := o.collectSummaries(ctx, display)
	if err != nil {
		return nil, fmt.Errorf("collecting summaries: %w", err)
	}

	// 结束总结器会话，后续的 getFinalConclusion 和 structurizeIssues 都构建全新消息，
	// 不依赖会话上下文，可以安全地并行执行
	if sp, ok := o.summarizer.Provider.(provider.SessionProvider); ok {
		sp.EndSession()
	}

	var (
		finalConclusion string
		parsedIssues    []MergedIssue
	)

	display.OnWaiting("final-conclusion")
	if len(summaries) == 0 {
		finalConclusion = o.buildFallbackConclusion(nil)
		display.OnMessage("summarizer", finalConclusion)
	} else {
		finalConclusion, err = o.getFinalConclusion(ctx, summaries, display)
		if err != nil {
			if isContextCancellation(err, ctx) {
				return nil, fmt.Errorf("final conclusion: %w", err)
			}
			util.Warnf("runSummaryPhase: final conclusion generation failed, using fallback summary: %v", err)
			finalConclusion = o.buildFallbackConclusion(summaries)
			display.OnMessage("summarizer", finalConclusion)
		}
	}
	if o.useLedgerStructurize() {
		parsedIssues = o.structurizeIssuesFromLedgers(ctx, display)
	} else {
		parsedIssues = o.structurizeIssuesLegacy(ctx, display)
	}

	return &DebateResult{
		PRNumber:         label,
		Analysis:         o.analysis,
		Context:          o.gatheredContext,
		Messages:         o.conversationHistory,
		Summaries:        summaries,
		FinalConclusion:  finalConclusion,
		TokenUsage:       o.getTokenUsage(),
		ConvergedAtRound: convergedAtRound,
		ParsedIssues:     parsedIssues,
	}, nil
}

// useLedgerStructurize 判断是否启用 ledger 模式的增量问题结构化。
//
// 返回：
//   - bool: 当 StructurizeMode 配置为 "ledger"（不区分大小写）时返回 true
//
// 设计说明：CodeDebate 支持两种问题结构化模式：
//   - legacy 模式：辩论结束后一次性从全部审查文本中提取所有问题
//   - ledger 模式：每轮辩论后增量提取新问题和状态变更，最终合并
//
// ledger 模式的优势是可以追踪问题在辩论过程中的演变（被支持/撤回/质疑），
// 提供更精确的问题置信度评估。
func (o *DebateOrchestrator) useLedgerStructurize() bool {
	return strings.EqualFold(strings.TrimSpace(o.options.StructurizeMode), "ledger")
}

// initIssueLedgers 为每个审查者初始化一个空的问题账本（IssueLedger）。
//
// 在 ledger 模式下，每个审查者拥有独立的问题账本，用于追踪该审查者在
// 辩论过程中发现、修改和撤回的问题。账本在辩论开始前初始化，
// 在每轮辩论后通过 extractRoundIssueDeltas 更新。
func (o *debateRun) initIssueLedgers() {
	o.issueLedgers = make(map[string]*IssueLedger, len(o.reviewers))
	for _, reviewer := range o.reviewers {
		o.issueLedgers[reviewer.ID] = NewIssueLedger(reviewer.ID)
	}
}

// extractRoundIssueDeltas 从本轮各审查者的输出中并行提取增量问题变更。
//
// 参数：
//   - ctx: 上下文，用于超时和取消控制
//   - round: 当前辩论轮次
//   - roundOutputs: 本轮各审查者的输出内容映射（reviewerID -> content）
//   - display: UI 回调接口
//
// 设计说明：每轮辩论后，通过 AI 从审查者的自然语言输出中提取结构化的增量变更
// （新增的问题、更新的问题、撤回的问题等），并应用到对应审查者的 ledger 中。
// 同时提取跨审查者的规范化信号（support/withdraw/contest），用于后续的问题
// 去重和置信度计算。提取过程是并行的，因为各审查者的 ledger 是独立的。
func (o *debateRun) extractRoundIssueDeltas(ctx context.Context, round int, roundOutputs map[string]string, display DisplayCallbacks) {
	if len(o.issueLedgers) == 0 || len(roundOutputs) == 0 {
		return
	}

	display.OnWaiting("structurizer")
	currentCanonicalSummary := BuildCanonicalIssueSummary(o.currentCanonicalIssues())

	g, gctx := errgroup.WithContext(ctx)
	for _, reviewer := range o.reviewers {
		reviewerID := reviewer.ID
		roundContent := strings.TrimSpace(roundOutputs[reviewerID])
		ledger := o.issueLedgers[reviewerID]
		if roundContent == "" || ledger == nil {
			continue
		}

		g.Go(func() error {
			delta, err := o.extractIssueDelta(gctx, reviewerID, round, roundContent, ledger.BuildSummary(), currentCanonicalSummary)
			if err != nil {
				util.Warnf("extractRoundIssueDeltas: reviewer %s round %d skipped: %v", reviewerID, round, err)
				return nil
			}
			ledger.ApplyDelta(delta, round)
			o.applyCanonicalActions(reviewerID, round, delta)
			return nil
		})
	}

	if err := g.Wait(); err != nil {
		util.Warnf("extractRoundIssueDeltas: unexpected error: %v", err)
	}
}

// extractIssueDelta 使用 AI 从单个审查者的单轮输出中提取结构化的增量问题变更。
//
// 参数：
//   - ctx: 上下文
//   - reviewerID: 审查者标识
//   - round: 当前辩论轮次
//   - roundContent: 该审查者本轮的完整输出文本
//   - ledgerSummary: 该审查者当前 ledger 的摘要（已有问题列表），帮助 AI 识别增量
//   - canonicalSummary: 全局规范化问题摘要，帮助 AI 进行跨审查者的问题引用
//
// 返回：
//   - *StructurizeDelta: 结构化的增量变更，包含新增/更新/撤回的问题和跨审查者信号
//   - error: 所有重试都失败时的错误
//
// 容错机制：最多重试 3 次。如果 AI 输出的 JSON 格式不正确或不符合 schema，
// 会将验证错误反馈给 AI 并要求重新生成。这种 "反馈-重试" 模式可以有效提高
// 结构化输出的成功率。
func (o *debateRun) extractIssueDelta(ctx context.Context, reviewerID string, round int, roundContent, ledgerSummary, canonicalSummary string) (*StructurizeDelta, error) {
	deltaSchema := schema.GetSchemaString("issues_delta")
	basePrompt := prompt.MustRender("structurize_delta.tmpl", map[string]any{
		"ReviewerID":       reviewerID,
		"Round":            round,
		"RoundContent":     roundContent,
		"LedgerSummary":    ledgerSummary,
		"CanonicalSummary": canonicalSummary,
		"Schema":           deltaSchema,
		"Language":         o.options.Language,
	})
	systemPrompt := prompt.MustRender("structurize_delta_system.tmpl", nil)

	// 禁用工具调用，因为 structurizer 只需要输出纯 JSON；限制 MaxTokens 防止生成过长响应
	chatOpts := &provider.ChatOptions{DisableTools: true, MaxTokens: 8192}
	const maxAttempts = 3
	var lastValidationErrors string

	for attempt := 1; attempt <= maxAttempts; attempt++ {
		attemptPrompt := basePrompt
		if attempt > 1 {
			// 重试时将上一次的验证错误拼接在提示词前面，引导 AI 修正输出格式
			attemptPrompt = fmt.Sprintf("Previous output had validation errors:\n%s\n\n%s", lastValidationErrors, basePrompt)
		}

		msgs := []provider.Message{{Role: "user", Content: attemptPrompt}}
		response, err := o.summarizer.Provider.Chat(ctx, msgs, systemPrompt, chatOpts)
		if err != nil {
			lastValidationErrors = fmt.Sprintf("Chat error: %v", err)
			continue
		}
		o.trackTokens("summarizer", attemptPrompt+systemPrompt, response)

		// 尝试将 AI 响应解析为结构化的 delta 对象
		parsed := ParseStructurizeDelta(response)
		switch {
		case parsed.ParseError != nil:
			lastValidationErrors = fmt.Sprintf("JSON parse error: %v", parsed.ParseError)
		case len(parsed.SchemaErrors) > 0:
			// Schema 验证失败：JSON 格式正确但不符合预期的结构
			vr := &schema.ValidationResult{Errors: parsed.SchemaErrors}
			lastValidationErrors = schema.FormatErrorsForRetry(vr)
		case parsed.Output == nil:
			lastValidationErrors = "JSON was valid but output is empty."
		default:
			// 解析成功且通过验证，返回结果
			return parsed.Output, nil
		}
	}

	return nil, fmt.Errorf("failed after %d attempts: %s", maxAttempts, lastValidationErrors)
}

// structurizeIssuesFromLedgers 从所有审查者的 ledger 中收集问题，进行规范化和去重。
//
// 参数：
//   - ctx: 上下文
//   - display: UI 回调接口
//
// 返回：
//   - []MergedIssue: 规范化、去重后的最终问题列表
//
// 容错设计：如果所有 ledger 都为空（增量提取全部失败），则回退到 legacy 模式
// 进行一次性全量提取，确保不会因为中间步骤失败而丢失所有审查结果。
func (o *debateRun) structurizeIssuesFromLedgers(ctx context.Context, display DisplayCallbacks) []MergedIssue {
	allIssues := o.collectCanonicalInputsFromLedgers()
	if len(allIssues) > 0 {
		return ApplyCanonicalSignals(CanonicalizeMergedIssues(allIssues), o.canonicalSignals)
	}

	// 兜底：当增量提取全失败时，回退到 legacy 一次性提取。
	if o.hasReviewerMessages() {
		util.Warnf("structurizeIssuesFromLedgers: no issues from ledgers, falling back to legacy structurizer")
		return o.structurizeIssuesLegacy(ctx, display)
	}
	return nil
}

// mergeAllLedgers 将所有审查者的 ledger 合并为统一的 MergedIssue 列表。
//
// 返回：
//   - []MergedIssue: 合并后的问题列表，按审查者 ID 字母序排列（确保结果确定性）
//
// 注意：此方法不做去重或规范化，仅做简单的列表拼接。去重和规范化由调用方
// 通过 CanonicalizeMergedIssues 和 ApplyCanonicalSignals 完成。
func (o *debateRun) mergeAllLedgers() []MergedIssue {
	if len(o.issueLedgers) == 0 {
		return nil
	}
	reviewerIDs := make([]string, 0, len(o.issueLedgers))
	for reviewerID := range o.issueLedgers {
		reviewerIDs = append(reviewerIDs, reviewerID)
	}
	sort.Strings(reviewerIDs)

	var merged []MergedIssue
	for _, reviewerID := range reviewerIDs {
		merged = append(merged, o.issueLedgers[reviewerID].ToMergedIssues()...)
	}
	return merged
}

// collectCanonicalInputsFromLedgers 从所有审查者的 ledger 中收集规范化输入。
//
// 返回：
//   - []MergedIssue: 所有审查者 ledger 中的问题（转换为规范化输入格式），按审查者 ID 排序
//
// 与 mergeAllLedgers 的区别：mergeAllLedgers 使用 ToMergedIssues 转换，保留原始信息；
// 此方法使用 ToCanonicalInputs 转换，输出适合后续规范化处理的格式。
func (o *debateRun) collectCanonicalInputsFromLedgers() []MergedIssue {
	if len(o.issueLedgers) == 0 {
		return nil
	}
	reviewerIDs := make([]string, 0, len(o.issueLedgers))
	for reviewerID := range o.issueLedgers {
		reviewerIDs = append(reviewerIDs, reviewerID)
	}
	sort.Strings(reviewerIDs)

	var merged []MergedIssue
	for _, reviewerID := range reviewerIDs {
		merged = append(merged, o.issueLedgers[reviewerID].ToCanonicalInputs()...)
	}
	return merged
}

// currentCanonicalIssues 获取当前时刻的全局规范化问题列表。
//
// 返回：
//   - []MergedIssue: 经过规范化和信号应用后的当前问题列表
//
// 此方法在每轮增量提取前调用，将当前的全局问题状态提供给 AI，
// 帮助 AI 在提取增量时正确引用已有的问题（避免重复创建）。
func (o *debateRun) currentCanonicalIssues() []MergedIssue {
	return ApplyCanonicalSignals(CanonicalizeMergedIssues(o.collectCanonicalInputsFromLedgers()), o.canonicalSignals)
}

// applyCanonicalActions 从增量变更中提取跨审查者的规范化信号并追加到全局信号列表。
//
// 参数：
//   - reviewerID: 发出信号的审查者 ID
//   - round: 信号产生的辩论轮次
//   - delta: 从审查者输出中提取的增量变更
//
// 规范化信号类型：
//   - support: 审查者支持/认同另一个审查者提出的问题
//   - withdraw: 审查者撤回自己之前提出的问题
//   - contest: 审查者质疑/反对另一个审查者提出的问题
//
// 去重策略：使用 "action|issueRef" 作为去重键，同一个 delta 中同一审查者
// 对同一问题只保留一个相同类型的信号（但允许不同类型的信号共存）。
// 使用互斥锁保护 canonicalSignals 的并发写入，因为多个审查者的增量提取是并行的。
func (o *debateRun) applyCanonicalActions(reviewerID string, round int, delta *StructurizeDelta) {
	if delta == nil {
		return
	}
	seen := make(map[string]struct{}, len(delta.Support)+len(delta.Withdraw)+len(delta.Contest))
	actions := make([]CanonicalSignal, 0, len(delta.Support)+len(delta.Withdraw)+len(delta.Contest))
	for _, item := range delta.Support {
		ref := strings.TrimSpace(item.IssueRef)
		if ref == "" {
			continue
		}
		key := "support|" + ref
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		actions = append(actions, CanonicalSignal{
			ReviewerID: reviewerID,
			IssueRef:   ref,
			Round:      round,
			Action:     "support",
		})
	}
	for _, item := range delta.Withdraw {
		ref := strings.TrimSpace(item.IssueRef)
		if ref == "" {
			continue
		}
		key := "withdraw|" + ref
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		actions = append(actions, CanonicalSignal{
			ReviewerID: reviewerID,
			IssueRef:   ref,
			Round:      round,
			Action:     "withdraw",
		})
	}
	for _, item := range delta.Contest {
		ref := strings.TrimSpace(item.IssueRef)
		if ref == "" {
			continue
		}
		key := "contest|" + ref
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		actions = append(actions, CanonicalSignal{
			ReviewerID: reviewerID,
			IssueRef:   ref,
			Round:      round,
			Action:     "contest",
		})
	}
	if len(actions) == 0 {
		return
	}

	o.mu.Lock()
	defer o.mu.Unlock()
	o.canonicalSignals = append(o.canonicalSignals, actions...)
}

// hasReviewerMessages 检查对话历史中是否存在审查者的消息（排除 "user" 角色）。
//
// 返回：
//   - bool: 如果存在任何非 "user" 角色的消息则返回 true
//
// 用于 structurizeIssuesFromLedgers 的兜底判断：如果有审查者消息但 ledger 为空，
// 说明增量提取全部失败，可以回退到 legacy 一次性提取。
func (o *debateRun) hasReviewerMessages() bool {
	for _, msg := range o.conversationHistory {
		if msg.ReviewerID != "user" {
			return true
		}
	}
	return false
}

// ========== 消息构建 ==========

// buildMessages 根据当前轮次和审查者状态构建发送给 AI 提供者的消息列表。
//
// 参数：
//   - currentReviewerID: 当前审查者的 ID
//
// 返回：
//   - []provider.Message: 构建好的消息列表，可直接传递给 AI 提供者的 ChatStream 方法
//
// 消息构建策略：
//   - 第1轮（首次调用）：构建独立审查提示，包含任务描述、代码上下文、分析结果和关注重点。
//     此时审查者独立工作，不受其他审查者影响。
//   - 第2轮及之后（辩论轮次）：根据提供者是否支持会话模式选择不同策略：
//   - 会话模式：仅发送上一轮其他审查者的新消息（增量更新），节省 Token
//   - 非会话模式：发送完整上下文，包含任务描述和所有历史消息
//
// 判断是否为首次调用的依据是 lastSeenIndex 中是否有该审查者的记录，
// 而非直接检查轮次号，因为 buildMessages 也在 collectSummaries 中被复用。
func (o *debateRun) buildMessages(currentReviewerID string) []provider.Message {
	reviewer := o.findReviewer(currentReviewerID)
	hasSession := false
	if reviewer != nil {
		_, hasSession = reviewer.Provider.(provider.SessionProvider)
	}
	lastSeen := -1
	if v, ok := o.lastSeenIndex[currentReviewerID]; ok {
		lastSeen = v
	}
	isFirstCall := lastSeen < 0

	// 第1轮：独立审查
	if isFirstCall {
		return o.buildFirstRoundMessages(currentReviewerID)
	}

	// 第2轮及之后：收集辩论上下文
	var otherIDs []string
	for _, r := range o.reviewers {
		if r.ID != currentReviewerID {
			otherIDs = append(otherIDs, r.ID)
		}
	}

	myMessageCount, previousRoundsMessages := o.collectPreviousRoundsMessages(currentReviewerID)

	if hasSession {
		return o.buildSessionDebateMessages(currentReviewerID, myMessageCount, previousRoundsMessages)
	}
	return o.buildFullContextDebateMessages(currentReviewerID, otherIDs, previousRoundsMessages)
}

// buildFirstRoundMessages 构建第一轮独立审查的消息。
//
// 参数：
//   - currentReviewerID: 当前审查者的 ID，嵌入提示词中让审查者知道自己的身份
//
// 返回：
//   - []provider.Message: 包含单条 user 消息的列表
//
// 消息内容由以下部分组成：
//   - 任务提示词（包含代码 diff）
//   - 系统上下文（如果有，来自上下文收集器）
//   - 关注重点（从 analyzer 分析结果中提取的焦点领域）
//   - 调用链信息（如果有，展示代码变更涉及的函数调用关系）
//
// 设计决策：只传递 analyzer 提取的 focus bullets 而不传递全文，
// 是为了避免锚定审查者的思维。如果审查者看到完整的分析报告，
// 可能会过度依赖分析结果而忽略自己独立发现的问题。
func (o *debateRun) buildFirstRoundMessages(currentReviewerID string) []provider.Message {
	var contextSection string
	if o.gatheredContext != nil && o.gatheredContext.Summary != "" {
		contextSection = fmt.Sprintf("\n## System Context\n%s\n\n", o.gatheredContext.Summary)
	}

	var focusSection string
	focusAreas := ParseFocusAreas(o.analysis)
	if len(focusAreas) > 0 {
		focusSection = fmt.Sprintf("\nThe analyzer suggests focusing on: %s.\nThese are suggestions -- also flag anything else you notice beyond these areas.\n",
			strings.Join(focusAreas, "; "))
	}

	var callChainSection string
	if o.gatheredContext != nil && len(o.gatheredContext.RawReferences) > 0 {
		callChainSection = "\n" + FormatCallChainForReviewer(o.gatheredContext.RawReferences) + "\n"
	}

	var previousCommentsSection string
	if strings.TrimSpace(o.options.PreviousComments) != "" {
		previousCommentsSection = fmt.Sprintf("\n## Previous Review Findings (from last CodeDebate run)\nThe following issues were flagged in the previous review and are still active.\nYour PRIMARY task: check if each issue has been fixed in the current diff.\n- Mark resolved issues as FIXED\n- Mark unresolved issues as STILL OPEN\n- Only raise NEW issues if they are critical/blocking\n\n%s\n", o.options.PreviousComments)
	}

	// 只传 focus bullets，不传 analyzer 全文，避免锚定 reviewer 思维
	p := prompt.MustRender("reviewer_first_round.tmpl", map[string]any{
		"TaskPrompt":              o.taskPrompt,
		"ContextSection":          contextSection,
		"FocusSection":            focusSection,
		"CallChainSection":        callChainSection,
		"PreviousCommentsSection": previousCommentsSection,
		"ReviewerID":              currentReviewerID,
		"Language":                o.options.Language,
	})

	return []provider.Message{{Role: "user", Content: p}}
}

// collectPreviousRoundsMessages 收集之前轮次中其他审查者的消息，用于构建辩论上下文。
//
// 参数：
//   - currentReviewerID: 当前审查者的 ID
//
// 返回：
//   - int: 当前审查者自己在对话历史中的消息条数（即已完成的轮次数）
//   - []DebateMessage: 其他审查者的消息列表（排除当前审查者自己的消息）
//
// 过滤逻辑：
//  1. 排除当前审查者自己的消息（因为自己的消息会在后续作为 assistant 角色单独添加）
//  2. 保留 "user" 角色的消息（通常是人工介入的反馈）
//  3. 对于其他审查者，只保留其消息条数不超过当前审查者已完成轮次数的部分。
//     这是为了确保对称性：如果当前审查者完成了 N 轮，那么其他审查者的消息
//     也只展示前 N 条，避免信息不对称导致的偏差。
func (o *debateRun) collectPreviousRoundsMessages(currentReviewerID string) (int, []DebateMessage) {
	myMessageCount := 0
	for _, m := range o.conversationHistory {
		if m.ReviewerID == currentReviewerID {
			myMessageCount++
		}
	}

	// 只获取之前轮次的消息（排除当前审查者自己的消息，因为会作为assistant消息单独添加）
	messageCountByReviewer := make(map[string]int)
	var previousRoundsMessages []DebateMessage
	for _, msg := range o.conversationHistory {
		if msg.ReviewerID == currentReviewerID {
			continue // 排除自己的消息
		}
		if msg.ReviewerID == "user" {
			previousRoundsMessages = append(previousRoundsMessages, msg)
			continue
		}
		count := messageCountByReviewer[msg.ReviewerID]
		if count < myMessageCount {
			messageCountByReviewer[msg.ReviewerID] = count + 1
			previousRoundsMessages = append(previousRoundsMessages, msg)
		}
	}

	return myMessageCount, previousRoundsMessages
}

// buildSessionDebateMessages 为会话模式构建增量辩论消息。
//
// 参数：
//   - currentReviewerID: 当前审查者的 ID
//   - myMessageCount: 当前审查者已完成的轮次数
//   - previousRoundsMessages: 其他审查者的所有历史消息
//
// 返回：
//   - []provider.Message: 仅包含最新一轮新增消息的列表
//
// 设计说明：会话模式下，AI 提供者在服务端维护了完整的对话上下文。
// 因此只需发送自上次请求以来的新消息（增量更新），而非完整历史。
// 这显著减少了 Token 消耗——对于 N 轮辩论 M 个审查者的场景，
// 每轮每个审查者的输入从 O(N*M) 降低到 O(M)。
//
// 增量计算方法：只保留其他审查者消息中轮次 >= 当前审查者上一轮次的消息。
// 如果没有新消息（通常不会发生），发送一个兜底提示继续审查。
func (o *debateRun) buildSessionDebateMessages(currentReviewerID string, myMessageCount int, previousRoundsMessages []DebateMessage) []provider.Message {
	prevRoundCount := myMessageCount - 1
	messageCountByReviewer2 := make(map[string]int)
	var newMessages []DebateMessage
	for _, msg := range previousRoundsMessages {
		if msg.ReviewerID == "user" {
			newMessages = append(newMessages, msg)
			continue
		}
		count := messageCountByReviewer2[msg.ReviewerID]
		messageCountByReviewer2[msg.ReviewerID] = count + 1
		if count >= prevRoundCount {
			newMessages = append(newMessages, msg)
		}
	}

	if len(newMessages) == 0 {
		newMessages = append(newMessages, DebateMessage{ReviewerID: "system", Content: "No additional reviewer messages were added in the previous round. Focus on convergence based on the existing debate state."})
	}

	var parts []string
	for _, m := range newMessages {
		parts = append(parts, fmt.Sprintf("[%s]: %s", m.ReviewerID, m.Content))
	}
	newContent := strings.Join(parts, "\n\n---\n\n")

	p := prompt.MustRender("reviewer_debate_session.tmpl", map[string]any{
		"ReviewerID": currentReviewerID,
		"NewContent": newContent,
		"Round":      o.currentRound,
		"MaxRounds":  o.options.MaxRounds,
		"Language":   o.options.Language,
	})

	return []provider.Message{{Role: "user", Content: p}}
}

// buildFullContextDebateMessages 为非会话模式构建包含完整上下文的辩论消息。
//
// 参数：
//   - currentReviewerID: 当前审查者的 ID
//   - otherIDs: 其他审查者的 ID 列表
//   - previousRoundsMessages: 其他审查者的所有历史消息
//
// 返回：
//   - []provider.Message: 包含完整上下文的消息列表
//
// 消息结构：
//  1. 第一条 user 消息：包含任务描述、分析结果、辩论角色说明
//  2. 中间的 user 消息：其他审查者在历次轮次中的发言（以 "[reviewerID]: " 为前缀）
//  3. 最后的 assistant 消息：当前审查者自己之前的发言，维持对话连贯性
//
// 设计说明：非会话模式下，每次请求都需要发送完整上下文。这虽然消耗更多 Token，
// 但兼容性更好（不依赖提供者的会话功能）。将其他审查者的消息设为 user 角色、
// 自己的历史消息设为 assistant 角色，是为了利用 LLM 的角色感知能力，
// 让模型清楚区分"别人说了什么"和"自己之前说了什么"。
func (o *debateRun) buildFullContextDebateMessages(currentReviewerID string, otherIDs []string, previousRoundsMessages []DebateMessage) []provider.Message {
	otherLabel := fmt.Sprintf("[%s]", strings.Join(otherIDs, "], ["))
	isPlural := len(otherIDs) > 1
	otherWord := "is"
	if isPlural {
		otherWord = "are"
	}

	p := prompt.MustRender("reviewer_debate_full.tmpl", map[string]any{
		"TaskPrompt": o.taskPrompt,
		"Analysis":   o.analysis,
		"ReviewerID": currentReviewerID,
		"OtherLabel": otherLabel,
		"PluralS":    pluralS(isPlural),
		"OtherWord":  otherWord,
		"Round":      o.currentRound,
		"MaxRounds":  o.options.MaxRounds,
		"Language":   o.options.Language,
	})

	messages := []provider.Message{{Role: "user", Content: p}}

	// 添加之前轮次的其他审查者消息作为user角色
	for _, msg := range previousRoundsMessages {
		prefix := fmt.Sprintf("[%s]: ", msg.ReviewerID)
		if msg.ReviewerID == "user" {
			prefix = "[Human]: "
		}
		messages = append(messages, provider.Message{
			Role:    "user",
			Content: prefix + msg.Content,
		})
	}

	// 添加审查者自己之前的消息作为assistant角色，维持对话的连贯性
	for _, m := range o.conversationHistory {
		if m.ReviewerID == currentReviewerID {
			messages = append(messages, provider.Message{
				Role:    "assistant",
				Content: m.Content,
			})
		}
	}

	return messages
}

// ========== 共识检测与总结 ==========

// checkConvergence 请求总结器（summarizer）判断审查者是否已达成共识。
//
// 参数：
//   - ctx: 上下文，用于超时和取消控制
//   - display: UI 回调接口，用于通知共识判定结果和推理过程
//
// 返回：
//   - bool: 是否达成共识
//   - error: AI 调用失败时的错误（不包括判定为未达成共识的情况）
//
// 共识判定标准（由 AI 判定，标准非常严格）：
//   - 所有审查者必须就关键问题达成一致
//   - 不能有被忽略的重要分歧
//   - 第1轮独立审查达成一致也是有效共识（无需强制进入辩论阶段）
//
// 输出格式：AI 在响应的最后一行输出 "CONVERGED" 或 "NOT_CONVERGED"，
// 前面的内容是推理过程。这种 "先推理后判定" 的格式可以提高判定准确性
// （类似 Chain-of-Thought 思维链）。
//
// 容错：空响应、无法识别的判定词、AI 调用失败都视为未达成共识（保守策略），
// 确保不会因为判定错误而提前终止辩论。
func (o *debateRun) checkConvergence(ctx context.Context, display DisplayCallbacks) (bool, error) {
	activeReviewerCount := len(o.activeReviewers())
	if activeReviewerCount < 2 {
		return false, nil
	}
	if len(o.conversationHistory) < activeReviewerCount {
		return false, nil
	}

	roundsCompleted := o.completedRounds()

	// 使用所有已完成轮次的消息（而非仅最后一轮），避免以下情况导致误判：
	// 审查者在最后一轮表面上达成一致，但实际上早期轮次中有重要的分歧被遗忘了
	roundByReviewer := make(map[string]int)
	var parts []string
	for _, m := range o.conversationHistory {
		if m.ReviewerID == "user" {
			continue
		}
		roundByReviewer[m.ReviewerID]++
		parts = append(parts, fmt.Sprintf("[%s] Round %d: %s", m.ReviewerID, roundByReviewer[m.ReviewerID], m.Content))
	}
	messagesText := strings.Join(parts, "\n\n---\n\n")

	convergencePrompt := prompt.MustRender("convergence_check.tmpl", map[string]any{
		"ReviewerCount":   activeReviewerCount,
		"RoundsCompleted": roundsCompleted,
		"MaxRounds":       o.options.MaxRounds,
		"IsFirstRound":    roundsCompleted == 1,
		"MessagesText":    messagesText,
	})

	systemPrompt := prompt.MustRender("convergence_system.tmpl", nil)

	msgs := []provider.Message{{Role: "user", Content: convergencePrompt}}
	response, err := o.summarizer.Provider.Chat(ctx, msgs, systemPrompt, nil)
	if err != nil {
		return false, err
	}

	// 解析响应：从后往前扫描，提取最后一条非空行的首词作为判定结果。
	// 采用 "最后一行" 策略是因为 AI 通常先输出推理过程再输出结论，
	// 而推理过程中可能出现 "CONVERGED" / "NOT_CONVERGED" 等词汇，取最后一行可以避免误匹配。
	trimmed := strings.TrimSpace(response)
	if trimmed == "" {
		reasoning := "Convergence judge returned empty response."
		display.OnConvergenceJudgment("NOT_CONVERGED", reasoning)
		util.Warnf("checkConvergence: empty response, treating as NOT_CONVERGED")
		return false, nil
	}

	lines := strings.Split(trimmed, "\n")
	verdictLineIdx := -1
	verdict := ""
	for i := len(lines) - 1; i >= 0; i-- {
		line := strings.TrimSpace(lines[i])
		if line == "" {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) == 0 {
			continue
		}
		verdict = strings.ToUpper(fields[0])
		verdictLineIdx = i
		break
	}

	if verdict == "" {
		reasoning := "Convergence judge returned no verdict token."
		display.OnConvergenceJudgment("NOT_CONVERGED", reasoning)
		util.Warnf("checkConvergence: no verdict token found, treating as NOT_CONVERGED")
		return false, nil
	}

	isConverged := verdict == "CONVERGED"
	if verdict != "CONVERGED" && verdict != "NOT_CONVERGED" {
		util.Warnf("checkConvergence: invalid verdict token %q, treating as NOT_CONVERGED", verdict)
		verdict = "NOT_CONVERGED"
		isConverged = false
	}

	reasoning := trimmed
	if verdictLineIdx > 0 {
		reasoning = strings.TrimSpace(strings.Join(lines[:verdictLineIdx], "\n"))
	}
	if reasoning == "" {
		reasoning = trimmed
	}
	display.OnConvergenceJudgment(verdict, reasoning)

	return isConverged, nil
}

// collectSummaries 并行请求每个审查者提供最终总结。
//
// 参数：
//   - ctx: 上下文，用于超时和取消控制
//   - display: UI 回调接口，用于实时更新各审查者的总结状态
//
// 返回：
//   - []DebateSummary: 每个审查者的最终总结，顺序与 o.reviewers 一致
//   - error: 仅在外部取消/超时时返回错误；单个审查者总结失败会被跳过
//
// 在辩论结束后，每个审查者独立总结自己的核心观点和结论。总结内容将传递给
// 总结器（summarizer）用于生成最终结论。审查者在总结中不透露身份，
// 以确保总结器的判断不受审查者身份偏见的影响。
//
// 会话模式特殊处理：由于会话模式下续传只发送最后一条 user 消息，
// 需要将最新的辩论上下文和总结要求合并到同一条消息中，
// 否则审查者可能看不到最后一轮的辩论内容。
func (o *debateRun) collectSummaries(ctx context.Context, display DisplayCallbacks) ([]DebateSummary, error) {
	activeReviewers := o.activeReviewers()
	if len(activeReviewers) == 0 {
		return nil, nil
	}

	// 预分配结果切片，每个 goroutine 写入自己的索引位置，无需加锁
	summaries := make([]DebateSummary, len(activeReviewers))
	statuses := make([]ReviewerStatus, len(activeReviewers))
	var statusesMu sync.Mutex
	for i, reviewer := range activeReviewers {
		statuses[i] = ReviewerStatus{
			ReviewerID: reviewer.ID,
			Status:     "pending",
		}
	}

	displayStatuses := func() {
		statusesMu.Lock()
		snapshot := copyStatuses(statuses)
		statusesMu.Unlock()
		display.OnSummaryStatus(snapshot)
	}
	displayStatuses()
	var wg sync.WaitGroup
	var summaryErr error
	var summaryErrMu sync.Mutex
	for i, reviewer := range activeReviewers {
		i, reviewer := i, reviewer
		wg.Add(1)
		go func() {
			defer wg.Done()

			messages := o.buildMessages(reviewer.ID)
			summaryPrompt := prompt.MustRender("reviewer_summary.tmpl", map[string]any{
				"Language": o.options.Language,
			})

			// 会话模式下续传只会发送最后一条 user 消息，将最新上下文与总结要求合并到同一条消息中
			if _, ok := reviewer.Provider.(provider.SessionProvider); ok {
				var contextParts []string
				for _, m := range messages {
					content := strings.TrimSpace(m.Content)
					if content == "" {
						continue
					}
					contextParts = append(contextParts, content)
				}
				if len(contextParts) > 0 {
					summaryPrompt = fmt.Sprintf("Latest debate context:\n\n%s\n\n%s",
						strings.Join(contextParts, "\n\n---\n\n"), summaryPrompt)
				}
				messages = []provider.Message{{Role: "user", Content: summaryPrompt}}
			} else {
				messages = append(messages, provider.Message{Role: "user", Content: summaryPrompt})
			}

			var inputParts []string
			for _, m := range messages {
				inputParts = append(inputParts, m.Content)
			}
			inputText := strings.Join(inputParts, "\n") + reviewer.SystemPrompt
			inputTokens := estimateTokens(inputText)

			startTime := time.Now().UnixMilli()
			statusesMu.Lock()
			statuses[i] = ReviewerStatus{
				ReviewerID:  reviewer.ID,
				Status:      "thinking",
				StartTime:   startTime,
				InputTokens: inputTokens,
			}
			statusesMu.Unlock()
			displayStatuses()

			ch, errCh := reviewer.Provider.ChatStream(ctx, messages, reviewer.SystemPrompt)
			var sb strings.Builder
			for chunk := range ch {
				sb.WriteString(chunk)
				display.OnMessageChunk("summary:"+reviewer.ID, chunk)
			}
			if err := <-errCh; err != nil {
				if isContextCancellation(err, ctx) {
					summaryErrMu.Lock()
					if summaryErr == nil {
						summaryErr = err
					}
					summaryErrMu.Unlock()
					return
				}

				endTime := time.Now().UnixMilli()
				statusesMu.Lock()
				statuses[i] = ReviewerStatus{
					ReviewerID:  reviewer.ID,
					Status:      "failed",
					StartTime:   startTime,
					EndTime:     endTime,
					Duration:    float64(endTime-startTime) / 1000.0,
					InputTokens: inputTokens,
				}
				statusesMu.Unlock()
				displayStatuses()

				util.Warnf("collectSummaries: reviewer %s summary failed, skipping: %v", reviewer.ID, err)
				return
			}

			summary := sb.String()
			display.OnMessage("summary:"+reviewer.ID, summary)

			outputTokens := estimateTokens(summary)
			endTime := time.Now().UnixMilli()
			statusesMu.Lock()
			statuses[i] = ReviewerStatus{
				ReviewerID:    reviewer.ID,
				Status:        "done",
				StartTime:     startTime,
				EndTime:       endTime,
				Duration:      float64(endTime-startTime) / 1000.0,
				InputTokens:   inputTokens,
				OutputTokens:  outputTokens,
				EstimatedCost: float64(inputTokens+outputTokens) * 0.00001,
			}
			statusesMu.Unlock()
			displayStatuses()

			o.trackTokens(reviewer.ID, inputText, summary)

			summaries[i] = DebateSummary{
				ReviewerID: reviewer.ID,
				Summary:    summary,
			}
		}()
	}

	wg.Wait()
	if summaryErr != nil {
		return nil, summaryErr
	}

	filtered := make([]DebateSummary, 0, len(summaries))
	for _, summary := range summaries {
		if strings.TrimSpace(summary.ReviewerID) == "" {
			continue
		}
		filtered = append(filtered, summary)
	}
	return filtered, nil
}

// getFinalConclusion 请求总结器根据所有审查者的匿名总结生成最终结论。
//
// 参数：
//   - ctx: 上下文，用于超时和取消控制
//   - summaries: 所有审查者的最终总结列表
//   - display: UI 回调接口，用于流式输出最终结论
//
// 返回：
//   - string: 最终结论文本，包含共识点、分歧点分析和推荐行动项
//   - error: AI 调用失败时的错误
//
// 设计说明：
//  1. 审查者的总结使用匿名编号（"Reviewer 1"、"Reviewer 2"）而非实际 ID，
//     避免总结器对特定审查者产生偏见。
//  2. 同时提供辩论记录（截取最后 16000 字符），让总结器能够验证审查者总结
//     的准确性，避免审查者在总结中遗漏或歪曲之前辩论中的重要观点。
//  3. 使用流式输出（ChatStream），让用户可以实时看到结论的生成过程。
func (o *debateRun) getFinalConclusion(ctx context.Context, summaries []DebateSummary, display DisplayCallbacks) (string, error) {
	var parts []string
	for i, s := range summaries {
		parts = append(parts, fmt.Sprintf("Reviewer %d:\n%s", i+1, s.Summary))
	}
	summaryText := strings.Join(parts, "\n\n---\n\n")
	debateText := o.buildDebateTranscript(16000)

	conclusionPrompt := prompt.MustRender("final_conclusion.tmpl", map[string]any{
		"ReviewerCount": len(summaries),
		"SummaryText":   summaryText,
		"DebateText":    debateText,
		"Language":      o.options.Language,
	})

	msgs := []provider.Message{{Role: "user", Content: conclusionPrompt}}
	ch, errCh := o.summarizer.Provider.ChatStream(ctx, msgs, o.summarizer.SystemPrompt)
	var sb strings.Builder
	for chunk := range ch {
		sb.WriteString(chunk)
		display.OnMessageChunk("summarizer", chunk)
	}
	if err := <-errCh; err != nil {
		return "", err
	}

	response := sb.String()
	display.OnMessage("summarizer", response)

	o.trackTokens("summarizer", conclusionPrompt+o.summarizer.SystemPrompt, response)
	return response, nil
}

// ========== 问题提取 ==========

// structurizeIssuesLegacy 使用 AI 从完整审查文本中一次性提取结构化的问题列表（legacy 模式）。
//
// 参数：
//   - ctx: 上下文，用于超时和取消控制
//   - display: UI 回调接口
//
// 返回：
//   - []MergedIssue: 去重后的结构化问题列表；所有重试都失败时返回 best-effort 结果或 nil
//
// 与 ledger 模式的区别：legacy 模式在辩论完全结束后才提取问题，将所有轮次的
// 审查内容作为输入一次性交给 AI 处理。优点是实现简单；缺点是无法追踪问题在
// 辩论过程中的演变。
//
// 容错机制：
//   - 最多重试 3 次，每次重试将上一次的验证错误反馈给 AI
//   - 即使 JSON 格式不完全正确，也会尽量保留部分解析成功的问题（best-effort）
//   - 所有重试都失败后，返回解析到的最多问题数的 best-effort 结果
//   - 显式设置 MaxTokens=32768，因为问题列表的 JSON 输出通常很大，
//     OpenAI 默认的 max_tokens（约 4096）会导致 JSON 输出被截断
func (o *debateRun) structurizeIssuesLegacy(ctx context.Context, display DisplayCallbacks) []MergedIssue {
	// 收集每个审查者在所有轮次中的消息（按时间顺序），而非只取最后一条。
	// 这样做是因为审查者可能在早期轮次中发现了重要问题，但在后续辩论中
	// 被其他话题覆盖而没有重复提及——如果只看最后一轮，这些问题会被遗漏。
	allMessages := make(map[string][]string) // reviewerID -> []content (按轮次顺序)
	for _, msg := range o.conversationHistory {
		if msg.ReviewerID == "user" {
			continue
		}
		allMessages[msg.ReviewerID] = append(allMessages[msg.ReviewerID], msg.Content)
	}

	if len(allMessages) == 0 {
		util.Warnf("structurizeIssues: no reviewer messages found in conversation history (total messages: %d)", len(o.conversationHistory))
		return nil
	}
	util.Debugf("structurizeIssues: collected messages from %d reviewers", len(allMessages))

	var reviewerIDs []string
	for id := range allMessages {
		reviewerIDs = append(reviewerIDs, id)
	}
	sort.Strings(reviewerIDs)

	var reviewParts []string
	for _, id := range reviewerIDs {
		rounds := allMessages[id]
		if len(rounds) == 1 {
			reviewParts = append(reviewParts, fmt.Sprintf("[%s]:\n%s", id, rounds[0]))
		} else {
			var roundParts []string
			for i, content := range rounds {
				roundParts = append(roundParts, fmt.Sprintf("[%s] Round %d:\n%s", id, i+1, content))
			}
			reviewParts = append(reviewParts, strings.Join(roundParts, "\n\n"))
		}
	}
	reviewText := strings.Join(reviewParts, "\n\n---\n\n")
	reviewerIDsStr := strings.Join(reviewerIDs, ", ")

	issuesSchema := schema.GetSchemaString("issues")
	basePrompt := prompt.MustRender("structurize_issues.tmpl", map[string]any{
		"ReviewText":  reviewText,
		"ReviewerIDs": reviewerIDsStr,
		"Schema":      issuesSchema,
		"Language":    o.options.Language,
	})

	systemPrompt := prompt.MustRender("structurize_system.tmpl", nil)
	// 显式设置 MaxTokens=32768：structurizer 需要输出大量结构化 JSON（每个 issue 包含
	// 完整描述+代码片段+修复建议），不设置时 OpenAI 默认 max_tokens 仅约 4096，
	// 会导致 JSON 输出被截断，触发 "unexpected end of JSON input" 解析错误。
	// DisableTools=true：structurizer 只需要输出纯 JSON，不需要调用任何工具。
	chatOpts := &provider.ChatOptions{DisableTools: true, MaxTokens: 32768}
	const maxAttempts = 3

	var lastValidationErrors string
	var bestEffort []MergedIssue // 保存历次尝试中解析到最多问题数的结果，作为兜底返回

	for attempt := 1; attempt <= maxAttempts; attempt++ {
		display.OnWaiting("structurizer")

		var attemptPrompt string
		if attempt == 1 {
			attemptPrompt = basePrompt
		} else {
			attemptPrompt = prompt.MustRender("structurize_retry.tmpl", map[string]any{
				"ValidationErrors": lastValidationErrors,
				"ReviewText":       reviewText,
				"ReviewerIDs":      reviewerIDsStr,
				"Schema":           issuesSchema,
			})
		}

		// 打印 structurizer 的完整请求（system + user），便于复现与单元测试。
		// 通过 -v/--show-tool-trace 可在终端看到完整内容。
		structurizerTrace := fmt.Sprintf(
			"## Structurizer Request (Attempt %d/%d)\n\n### System Prompt\n```text\n%s\n```\n\n### User Prompt\n```text\n%s\n```",
			attempt, maxAttempts, systemPrompt, attemptPrompt,
		)
		display.OnMessage("structurizer-request", structurizerTrace)
		util.Debugf("structurizeIssues: attempt %d/%d full request:\n%s", attempt, maxAttempts, structurizerTrace)

		msgs := []provider.Message{{Role: "user", Content: attemptPrompt}}
		response, err := o.summarizer.Provider.Chat(ctx, msgs, systemPrompt, chatOpts)
		if err != nil {
			util.Warnf("structurizeIssues: attempt %d/%d Chat error: %v", attempt, maxAttempts, err)
			lastValidationErrors = fmt.Sprintf("Chat error: %v", err)
			continue
		}
		o.trackTokens("summarizer", attemptPrompt, response)

		// 截取前 500 字符用于调试
		preview := response
		if len(preview) > 500 {
			preview = preview[:500] + "..."
		}
		util.Debugf("structurizeIssues: attempt %d/%d response preview: %s", attempt, maxAttempts, preview)

		parsed := ParseReviewerOutput(response)
		var parsedIssues []MergedIssue
		if parsed.Output != nil && len(parsed.Output.Issues) > 0 {
			parsedIssues = DeduplicateMergedIssues(issuesToMerged(parsed.Output.Issues))
			if len(parsedIssues) > len(bestEffort) {
				bestEffort = parsedIssues
			}
		}

		// 根据解析结果决定是否重试
		switch {
		case parsed.ParseError != nil:
			util.Warnf("structurizeIssues: attempt %d/%d parse error: %v", attempt, maxAttempts, parsed.ParseError)
			lastValidationErrors = fmt.Sprintf("JSON parse error: %v", parsed.ParseError)
		case len(parsed.SchemaErrors) > 0:
			vr := &schema.ValidationResult{Errors: parsed.SchemaErrors}
			lastValidationErrors = schema.FormatErrorsForRetry(vr)
			util.Warnf("structurizeIssues: attempt %d/%d schema errors: %s", attempt, maxAttempts, lastValidationErrors)
		case parsed.Output == nil || len(parsed.Output.Issues) == 0:
			util.Warnf("structurizeIssues: attempt %d/%d parsed OK but 0 valid issues", attempt, maxAttempts)
			lastValidationErrors = "JSON was valid but contained 0 issues. Please include all issues found in the review."
		default:
			// 成功
			return parsedIssues
		}
	}

	if len(bestEffort) > 0 {
		util.Warnf("structurizeIssues: returning best-effort issues after retries (%d issues)", len(bestEffort))
		return bestEffort
	}

	return nil
}

// structurizeIssues 是 debateRun 级别的问题结构化入口，兼容旧测试和旧调用。
//
// 参数：
//   - ctx: 上下文
//   - display: UI 回调接口
//
// 返回：
//   - []MergedIssue: 结构化问题列表
//
// 内部直接委托给 structurizeIssuesLegacy，保持向后兼容。
func (o *debateRun) structurizeIssues(ctx context.Context, display DisplayCallbacks) []MergedIssue {
	return o.structurizeIssuesLegacy(ctx, display)
}

// issuesToMerged 将 AI 输出的 ReviewIssue 列表转换为内部使用的 MergedIssue 列表。
//
// 参数：
//   - issues: 从 AI 结构化输出中解析得到的原始问题列表
//
// 返回：
//   - []MergedIssue: 转换后的合并问题列表，每个问题包含归属、支持者和提及信息
//
// 设计决策：
//   - ClaimedBy 是 AI 在 JSON 输出中标注的问题归属（"这个问题是哪个审查者提出的"），
//     但 AI 可能标注不准确。这里暂时信任 AI 的标注，将其用作 RaisedBy 和 SupportedBy。
//     后续的 DeduplicateMergedIssues 会基于审查者的实际输出进行交叉验证和合并。
//   - 如果 ClaimedBy 为空，默认标注为 "summarizer"，表示这是总结器归纳出的问题。
//   - 使用 append([]string(nil), ...) 创建独立的 slice 副本，避免多个字段共享底层数组
//     导致后续修改时的意外互相影响。
func issuesToMerged(issues []ReviewIssue) []MergedIssue {
	result := make([]MergedIssue, 0, len(issues))
	for _, issue := range issues {
		supportedBy := uniqueSorted(issue.ClaimedBy)
		if len(supportedBy) == 0 {
			supportedBy = []string{"summarizer"}
		}
		result = append(result, MergedIssue{
			ReviewIssue:  issue,
			RaisedBy:     supportedBy,
			IntroducedBy: append([]string(nil), supportedBy...),
			SupportedBy:  append([]string(nil), supportedBy...),
			Descriptions: []string{issue.Description},
			Mentions: func() []IssueMention {
				mentions := make([]IssueMention, 0, len(supportedBy))
				for _, reviewerID := range supportedBy {
					mentions = append(mentions, IssueMention{
						ReviewerID: reviewerID,
						Status:     "active",
					})
				}
				return mentions
			}(),
		})
	}
	return result
}

// ========== 辅助方法 ==========

// findReviewer 根据 ID 在审查者列表中查找对应的审查者。
//
// 参数：
//   - id: 审查者的唯一标识
//
// 返回：
//   - *Reviewer: 找到的审查者指针（指向 slice 中的元素）；未找到返回 nil
//
// 注意：返回的指针指向 o.reviewers slice 中的元素，修改返回值会影响原始数据。
// 采用线性查找而非 map，因为审查者数量通常很少（2-5 个）。
func (o *DebateOrchestrator) findReviewer(id string) *Reviewer {
	for i := range o.reviewers {
		if o.reviewers[i].ID == id {
			return &o.reviewers[i]
		}
	}
	return nil
}

// markAsSeen 将当前对话历史的最后一条消息标记为该审查者已读。
//
// 参数：
//   - reviewerID: 审查者的 ID
//
// 用途：在每轮辩论结束后，将审查者的 lastSeenIndex 更新为对话历史的最新位置。
// 这样在下一轮构建消息时（buildMessages），可以知道该审查者已经看到了哪些消息，
// 从而只发送增量部分。这是会话模式实现增量消息的关键。
func (o *debateRun) markAsSeen(reviewerID string) {
	o.lastSeenIndex[reviewerID] = len(o.conversationHistory) - 1
}

// trackTokens 追踪指定审查者的 Token 使用量（线程安全）。
//
// 参数：
//   - reviewerID: 审查者/角色 ID（如 "reviewer-1"、"analyzer"、"summarizer"）
//   - input: 发送给 AI 的输入文本（用于估算输入 Token）
//   - output: AI 返回的输出文本（用于估算输出 Token）
//
// 使用 estimateTokens 进行粗略估算而非精确计算，因为精确的 tokenizer
// 依赖具体的模型实现且计算成本较高，对于成本预估场景粗略估算已经足够。
// 使用互斥锁保护，因为多个审查者的 goroutine 可能并发调用此方法。
func (o *debateRun) trackTokens(reviewerID, input, output string) {
	o.mu.Lock()
	defer o.mu.Unlock()
	tc, ok := o.tokenUsage[reviewerID]
	if !ok {
		tc = &tokenCount{}
		o.tokenUsage[reviewerID] = tc
	}
	tc.input += estimateTokens(input)
	tc.output += estimateTokens(output)
}

// getTokenUsage 返回所有审查者/角色的 Token 使用量汇总（线程安全）。
//
// 返回：
//   - []TokenUsage: 每个审查者的 Token 使用量和预估费用列表
//
// 预估费用按照每 Token $0.00001 计算。这是一个非常粗略的估算值，
// 实际费用取决于具体的模型和提供者定价。仅用于给用户一个大致的成本概念。
// 返回的列表顺序不确定（来自 map 遍历），调用方如需排序应自行处理。
func (o *debateRun) getTokenUsage() []TokenUsage {
	o.mu.Lock()
	defer o.mu.Unlock()
	var usage []TokenUsage
	for id, tc := range o.tokenUsage {
		usage = append(usage, TokenUsage{
			ReviewerID:    id,
			InputTokens:   tc.input,
			OutputTokens:  tc.output,
			EstimatedCost: float64(tc.input+tc.output) * 0.00001,
		})
	}
	return usage
}

func (o *debateRun) activeReviewers() []Reviewer {
	if len(o.failedReviewers) == 0 {
		return o.reviewers
	}

	active := make([]Reviewer, 0, len(o.reviewers))
	for _, reviewer := range o.reviewers {
		if _, failed := o.failedReviewers[reviewer.ID]; failed {
			continue
		}
		active = append(active, reviewer)
	}
	return active
}

func (o *debateRun) markReviewerFailed(reviewerID string, err error) {
	if reviewerID == "" || err == nil {
		return
	}
	if o.failedReviewers == nil {
		o.failedReviewers = make(map[string]error)
	}
	o.failedReviewers[reviewerID] = err
}

func (o *debateRun) completedRounds() int {
	maxRound := 0
	if o.currentRound > maxRound {
		maxRound = o.currentRound
	}

	counts := make(map[string]int)
	for _, msg := range o.conversationHistory {
		if msg.ReviewerID == "user" {
			continue
		}
		if msg.Round > maxRound {
			maxRound = msg.Round
		}
		counts[msg.ReviewerID]++
	}
	if maxRound > 0 {
		return maxRound
	}

	for _, count := range counts {
		if count > maxRound {
			maxRound = count
		}
	}
	return maxRound
}

func (o *debateRun) buildFallbackConclusion(summaries []DebateSummary) string {
	var b strings.Builder
	b.WriteString("## Partial Review Result\n\n")
	b.WriteString("CodeDebate completed with partial results because final synthesis could not be generated reliably.\n\n")

	if len(summaries) > 0 {
		b.WriteString("### Available reviewer summaries\n\n")
		for _, summary := range summaries {
			if strings.TrimSpace(summary.ReviewerID) == "" || strings.TrimSpace(summary.Summary) == "" {
				continue
			}
			fmt.Fprintf(&b, "#### %s\n\n%s\n\n", summary.ReviewerID, summary.Summary)
		}
	} else {
		transcript := strings.TrimSpace(o.buildDebateTranscript(4000))
		if transcript != "" {
			b.WriteString("### Debate Transcript Excerpt\n\n")
			b.WriteString("```text\n")
			b.WriteString(transcript)
			b.WriteString("\n```\n")
		}
	}

	return strings.TrimSpace(b.String())
}

// endAllSessions 结束所有 AI 提供者的会话，释放服务端的会话资源。
//
// 在辩论完成或出错时通过 defer 调用，确保会话资源不会泄漏。
// 仅对支持会话模式的提供者（实现了 provider.SessionProvider 接口）有效。
// 对于不支持会话的提供者，此方法是无操作（no-op）。
func (o *debateRun) endAllSessions() {
	for _, r := range o.reviewers {
		if sp, ok := r.Provider.(provider.SessionProvider); ok {
			sp.EndSession()
		}
	}
	if sp, ok := o.analyzer.Provider.(provider.SessionProvider); ok {
		sp.EndSession()
	}
	if sp, ok := o.summarizer.Provider.(provider.SessionProvider); ok {
		sp.EndSession()
	}
}

// buildDebateTranscript 将对话历史构建为格式化的辩论记录文本。
//
// 参数：
//   - maxLen: 最大字符数限制；如果超出则截取最后 maxLen 个字符（保留最近的内容）。
//     设为 0 或负数表示不限制长度。
//
// 返回：
//   - string: 格式化的辩论记录，每条消息格式为 "[审查者ID] Round N:\n内容"，
//     消息之间用 "---" 分隔。如果对话历史为空则返回空字符串。
//
// 设计说明：截取最后部分（而非开头）是因为辩论中后期的讨论通常更有价值——
// 早期的初步观点在后续轮次中会被修正和深化，保留最近的内容可以让总结器
// 看到最终的共识状态。
func (o *debateRun) buildDebateTranscript(maxLen int) string {
	if len(o.conversationHistory) == 0 {
		return ""
	}

	roundByReviewer := make(map[string]int)
	var parts []string
	for _, msg := range o.conversationHistory {
		if msg.ReviewerID == "user" {
			continue
		}
		roundByReviewer[msg.ReviewerID]++
		parts = append(parts, fmt.Sprintf("[%s] Round %d:\n%s", msg.ReviewerID, roundByReviewer[msg.ReviewerID], msg.Content))
	}
	transcript := strings.Join(parts, "\n\n---\n\n")
	if maxLen > 0 && len(transcript) > maxLen {
		return fmt.Sprintf("[Truncated: showing last %d chars of transcript]\n\n%s", maxLen, transcript[len(transcript)-maxLen:])
	}
	return transcript
}

// GetReviewers 返回编排器中配置的审查者列表。
//
// 返回：
//   - []Reviewer: 审查者列表的直接引用（非副本）
//
// 用途：在辩论完成后，外部代码可能需要访问审查者列表来进行后续讨论
// （如让用户针对特定审查者的观点进行追问）。
// 注意：返回的是原始 slice 的引用，调用方不应修改其内容。
func (o *DebateOrchestrator) GetReviewers() []Reviewer {
	return o.reviewers
}

// estimateTokens 估算文本的 Token 数量。
//
// 参数：
//   - text: 需要估算 Token 数量的文本
//
// 返回：
//   - int: 估算的 Token 数量（向上取整）
//
// 估算规则：
//   - CJK 字符（中日韩）：按约 0.7 Token/字符计算。CJK 字符在 BPE 编码中通常
//     每个字符对应 1-2 个 Token，取 0.7 作为经验平均值。
//   - 非 CJK 字符（英文等）：按约 0.25 Token/字符（即 4 字符 ≈ 1 Token）计算，
//     这是英文文本在 GPT/Claude tokenizer 中的典型比率。
//
// 这是一个粗略的启发式估算，不同模型的实际 Token 数会有差异，
// 但对于成本预估和 Token 限制的粗略判断已经足够。
func estimateTokens(text string) int {
	cjkCount := 0
	for _, r := range text {
		if isCJK(r) {
			cjkCount++
		}
	}
	return int(math.Ceil(float64(cjkCount)*0.7 + float64(len(text)-cjkCount)/4.0))
}

// isCJK 检查一个 Unicode 字符是否为 CJK（中日韩）字符。
//
// 参数：
//   - r: 需要检查的 Unicode 字符
//
// 返回：
//   - bool: 如果字符属于 CJK 范围则返回 true
//
// 判断范围包括：
//   - unicode.Han: 汉字（CJK 统一表意文字）
//   - U+3000-U+303F: CJK 符号和标点（如。、「」等）
//   - U+FF00-U+FFEF: 半角和全角形式（如全角英文字母、全角数字等）
//
// 这些范围覆盖了中文、日文汉字和韩文汉字的常见字符。
// 日文假名和韩文谚文不在此范围内，但它们的 Token 比率接近 CJK 字符，
// 在实际使用中影响不大。
func isCJK(r rune) bool {
	return unicode.Is(unicode.Han, r) ||
		(r >= 0x3000 && r <= 0x303F) || // CJK符号和标点
		(r >= 0xFF00 && r <= 0xFFEF) // 半角和全角字符
}

// diffFenceRe 匹配 Markdown 格式的 diff 代码块（```diff\n...\n```）。
// 使用 (?s) 标志使 . 匹配换行符，(.*?) 使用非贪婪模式只匹配第一个 diff 块。
var diffFenceRe = regexp.MustCompile("(?s)```diff\\n(.*?)```")

// extractDiffFromPrompt 从包含 ```diff 代码块的提示词中提取纯 diff 内容。
//
// 参数：
//   - prompt: 可能包含 Markdown 格式 diff 代码块的完整提示词
//
// 返回：
//   - string: 提取出的纯 diff 文本；如果未找到 diff 代码块则返回原始提示词
//
// 用途：上下文收集器需要纯 diff 内容（不含 Markdown 格式包装）来分析代码变更。
// 传入的提示词通常包含 diff 之外的其他说明文字，此函数将 diff 部分单独提取出来。
func extractDiffFromPrompt(prompt string) string {
	if m := diffFenceRe.FindStringSubmatch(prompt); len(m) > 1 {
		return m[1]
	}
	return prompt
}

// copyStatuses 创建 ReviewerStatus 切片的浅拷贝。
//
// 参数：
//   - statuses: 原始状态切片
//
// 返回：
//   - []ReviewerStatus: 独立的副本切片
//
// 并发安全说明：多个审查者的 goroutine 会并发修改 statuses slice 中的元素，
// 而 UI 回调（display.OnParallelStatus）可能在另一个 goroutine 中读取这些数据。
// 通过创建浅拷贝，确保传递给 UI 回调的数据在回调执行期间不会被修改。
// ReviewerStatus 是值类型（struct），浅拷贝即可实现完全隔离。
func copyStatuses(statuses []ReviewerStatus) []ReviewerStatus {
	cp := make([]ReviewerStatus, len(statuses))
	copy(cp, statuses)
	return cp
}

func isContextCancellation(err error, ctx context.Context) bool {
	return errors.Is(err, context.Canceled) ||
		errors.Is(err, context.DeadlineExceeded) ||
		errors.Is(ctx.Err(), context.Canceled) ||
		errors.Is(ctx.Err(), context.DeadlineExceeded)
}

// pluralS 根据是否为复数返回英文复数后缀 "s" 或空字符串。
//
// 参数：
//   - plural: 是否为复数
//
// 返回：
//   - string: 复数时返回 "s"，单数时返回空字符串 ""
//
// 用于构建语法正确的英文提示词，例如 "reviewer" vs "reviewers"。
// 虽然简单，但提取为独立函数可以避免在多处重复相同的条件判断。
func pluralS(plural bool) string {
	if plural {
		return "s"
	}
	return ""
}
