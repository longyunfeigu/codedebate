package context

import (
	gocontext "context"
	"encoding/json"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/longyunfeigu/codedebate/internal/config"
	"github.com/longyunfeigu/codedebate/internal/platform"
	"github.com/longyunfeigu/codedebate/internal/provider"
)

// GathererOptions 控制上下文收集的行为参数。
// 包含历史记录范围和文档收集规则等配置。
type GathererOptions struct {
	History struct {
		MaxDays int // 查询历史 PR 的最大天数范围
		MaxPRs  int // 最多返回的关联 PR 数量
	}
	Docs struct {
		Patterns []string // 文档文件/目录的匹配模式
		MaxSize  int      // 单个文档文件的最大字节数
	}
}

// ContextGatherer 使用 AI 收集和分析 PR 上下文。
// 它协调多个数据源（代码引用、git 历史、项目文档）的收集，
// 并通过 AI 提供者进行结构化的架构影响分析。
type ContextGatherer struct {
	provider        provider.AIProvider      // AI 提供者，用于执行上下文分析
	options         GathererOptions          // 收集行为配置选项
	historyProvider platform.HistoryProvider // 平台历史提供者（可为 nil）
	cwd             string                   // 上下文收集时使用的工作目录
}

// NewContextGatherer 使用指定的 AI 提供者和配置创建新的 ContextGatherer。
// 先设置合理的默认值，然后用配置文件中的值覆盖（如果提供了配置）。
func NewContextGatherer(p provider.AIProvider, cfg *config.ContextGathererConfig, historyProvider platform.HistoryProvider) *ContextGatherer {
	opts := GathererOptions{}

	// 设置默认值
	opts.History.MaxDays = 30
	opts.History.MaxPRs = 10
	opts.Docs.Patterns = []string{"docs", "README.md", "ARCHITECTURE.md", "DESIGN.md"}
	opts.Docs.MaxSize = 50000

	// 用配置文件中的值覆盖默认值（仅覆盖非零值）
	if cfg != nil {
		if cfg.History != nil {
			if cfg.History.MaxDays > 0 {
				opts.History.MaxDays = cfg.History.MaxDays
			}
			if cfg.History.MaxPRs > 0 {
				opts.History.MaxPRs = cfg.History.MaxPRs
			}
		}
		if cfg.Docs != nil {
			if len(cfg.Docs.Patterns) > 0 {
				opts.Docs.Patterns = cfg.Docs.Patterns
			}
			if cfg.Docs.MaxSize > 0 {
				opts.Docs.MaxSize = cfg.Docs.MaxSize
			}
		}
	}

	return &ContextGatherer{
		provider:        p,
		options:         opts,
		historyProvider: historyProvider,
		cwd:             ".",
	}
}

// SetCwd 设置上下文收集时使用的工作目录。
func (g *ContextGatherer) SetCwd(cwd string) {
	if strings.TrimSpace(cwd) == "" {
		g.cwd = "."
		return
	}
	g.cwd = cwd
}

// diffFilePattern 匹配 unified diff 中文件路径的正则表达式。
// 支持三种格式：diff --git a/file b/file、--- a/file、+++ b/file
var diffFilePattern = regexp.MustCompile(`(?m)^(?:diff --git a/(.+?) b/|--- a/(.+?)|\+\+\+ b/(.+?))$`)

// extractChangedFiles 从 unified diff 文本中提取所有被修改文件的路径。
// 去重并排除 /dev/null（表示新增或删除的文件的占位路径）。
func extractChangedFiles(diff string) []string {
	matches := diffFilePattern.FindAllStringSubmatch(diff, -1)
	seen := make(map[string]bool)
	var files []string

	for _, m := range matches {
		// 尝试从三个捕获组中获取文件路径
		file := m[1]
		if file == "" {
			file = m[2]
		}
		if file == "" {
			file = m[3]
		}
		// 跳过空路径、已见过的路径和 /dev/null
		if file == "" || seen[file] || strings.HasPrefix(file, "/dev/null") {
			continue
		}
		seen[file] = true
		files = append(files, file)
	}

	return files
}

// Gather 是上下文收集的主方法，收集 PR 的完整上下文信息并返回结构化分析结果。
// 执行流程：
//  1. 从 diff 中提取变更文件列表
//  2. 并行收集三类数据：代码符号引用、git 历史关联 PR、项目文档
//  3. 构建分析提示词并调用 AI 进行架构影响分析
//  4. 解析 AI 响应并组合所有数据返回完整的上下文
//
// 如果 AI 分析失败，仍会返回部分上下文（不含 AI 分析结果）而不是报错。
func (g *ContextGatherer) Gather(ctx gocontext.Context, diff, prNumber, baseBranch string) (*GatheredContext, error) {
	cwd := g.cwd
	changedFiles := extractChangedFiles(diff)

	// 并行收集三类数据：代码引用、历史 PR、项目文档
	var references []RawReference
	var relatedPRs []RelatedPR
	var docs []RawDoc
	var wg sync.WaitGroup
	wg.Add(3)
	go func() { defer wg.Done(); references = CollectReferences(diff, cwd) }()
	go func() {
		defer wg.Done()
		relatedPRs, _ = CollectHistory(changedFiles, g.options.History.MaxDays, g.options.History.MaxPRs, cwd, g.historyProvider)
	}()
	go func() { defer wg.Done(); docs, _ = CollectDocs(g.options.Docs.Patterns, g.options.Docs.MaxSize, cwd) }()
	wg.Wait()

	// 构建分析提示词并调用 AI 进行分析
	prompt := BuildAnalysisPrompt(diff, changedFiles, references, relatedPRs, docs)
	if ctx == nil {
		ctx = gocontext.Background()
	}

	response, err := g.provider.Chat(
		ctx,
		[]provider.Message{{Role: "user", Content: prompt}},
		"You are a senior software architect. Analyze the PR context and respond in JSON format only.",
		&provider.ChatOptions{MaxTokens: 16384},
	)
	if err != nil {
		// AI 分析失败时返回部分上下文（包含原始引用和历史 PR 数据），不中断流程
		return &GatheredContext{
			RelatedPRs:    relatedPRs,
			Summary:       "AI analysis unavailable: " + err.Error(),
			GatheredAt:    time.Now(),
			PRNumber:      prNumber,
			BaseBranch:    baseBranch,
			RawReferences: references,
		}, nil
	}

	// 解析 AI 响应中的 JSON 结构化分析结果
	parsed := parseAIResponse(response)

	return &GatheredContext{
		AffectedModules: parsed.AffectedModules,
		CallChain:       parsed.CallChain,
		RelatedPRs:      relatedPRs,
		DesignPatterns:  parsed.DesignPatterns,
		Summary:         parsed.Summary,
		GatheredAt:      time.Now(),
		PRNumber:        prNumber,
		BaseBranch:      baseBranch,
		RawReferences:   references,
	}, nil
}

// aiAnalysisResult 是 AI 分析响应的解析结果结构体。
type aiAnalysisResult struct {
	AffectedModules []AffectedModule `json:"affectedModules"` // 受影响的模块
	CallChain       []CallChainItem  `json:"callChain"`       // 调用链
	DesignPatterns  []DesignPattern  `json:"designPatterns"`  // 设计模式
	Summary         string           `json:"summary"`         // 分析摘要
}

// jsonBlockRegex 匹配 Markdown JSON 代码块（```json ... ```）
var jsonBlockRegex = regexp.MustCompile("(?s)```json\\s*(.*?)\\s*```")

// anyCodeBlockRegex 匹配任意 Markdown 代码块（``` ... ```），不限语言标记
var anyCodeBlockRegex = regexp.MustCompile("(?s)```\\w*\\s*(.*?)\\s*```")

// jsonObjectRegex 匹配任意 JSON 对象（{...}）
var jsonObjectRegex = regexp.MustCompile(`(?s)\{.*\}`)

// parseAIResponse 解析 AI 的文本响应，从中提取结构化的分析结果。
// 尝试顺序：
//  1. 从 Markdown 的 ```json 代码块中提取 JSON
//  2. 从响应文本中直接匹配 JSON 对象
//  3. 如果都失败，将原始响应（截断到1000字符）作为摘要返回
func parseAIResponse(response string) aiAnalysisResult {
	candidates := collectJSONCandidates(response)

	for _, jsonStr := range candidates {
		var result aiAnalysisResult
		if err := json.Unmarshal([]byte(jsonStr), &result); err != nil {
			continue
		}
		// 校验解析结果：至少要有 AffectedModules 或 Summary 才算有效
		if len(result.AffectedModules) > 0 || result.Summary != "" {
			return result
		}
	}

	// 所有候选都失败，将原始响应作为摘要兜底
	summary := response
	if len(summary) > 1000 {
		summary = summary[:1000]
	}
	return aiAnalysisResult{Summary: summary}
}

// collectJSONCandidates 从 AI 响应中提取所有可能的 JSON 字符串候选。
// 按优先级排列：```json 代码块 > 任意代码块 > 直接匹配的 JSON 对象。
func collectJSONCandidates(response string) []string {
	var candidates []string

	// 1. 所有 ```json 代码块
	for _, m := range jsonBlockRegex.FindAllStringSubmatch(response, -1) {
		candidates = append(candidates, m[1])
	}

	// 2. 所有 ``` 代码块（非 json 标记的）
	for _, m := range anyCodeBlockRegex.FindAllStringSubmatch(response, -1) {
		block := strings.TrimSpace(m[1])
		if len(block) > 0 && block[0] == '{' {
			candidates = append(candidates, block)
		}
	}

	// 3. 直接匹配最外层 JSON 对象
	if m := jsonObjectRegex.FindString(response); m != "" {
		candidates = append(candidates, m)
	}

	return candidates
}
