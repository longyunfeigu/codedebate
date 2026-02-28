// Package context 提供 PR 上下文收集和分析功能。
// 包括代码引用分析、历史 PR 关联、文档收集和 AI 驱动的架构分析。
package context

import "time"

// GatheredContext 是为 PR 收集的完整上下文信息。
// 包含受影响的模块、调用链、关联 PR、设计模式等结构化分析结果，
// 为代码审查者提供全面的系统影响视角。
type GatheredContext struct {
	AffectedModules []AffectedModule `json:"affectedModules"`          // 受影响的逻辑模块列表
	CallChain       []CallChainItem  `json:"callChain"`                // 调用链分析结果
	RelatedPRs      []RelatedPR      `json:"relatedPRs"`               // 历史关联 PR 列表
	DesignPatterns  []DesignPattern  `json:"designPatterns"`           // 发现的设计模式/代码规范
	Summary         string           `json:"summary"`                  // AI 生成的综合分析摘要
	GatheredAt      time.Time        `json:"gatheredAt"`               // 上下文收集时间戳
	PRNumber        string           `json:"prNumber"`                 // PR 编号
	BaseBranch      string           `json:"baseBranch"`               // 目标基础分支
	RawReferences   []RawReference   `json:"rawReferences,omitempty"` // 原始符号引用数据
}

// AffectedModule 表示受 PR 影响的逻辑模块。
// 每个模块包含名称、路径、描述以及影响程度评估。
type AffectedModule struct {
	Name          string   `json:"name"`          // 模块名称
	Path          string   `json:"path"`          // 模块的基础路径
	Description   string   `json:"description"`   // 模块功能描述
	AffectedFiles []string `json:"affectedFiles"` // 该模块中受影响的文件列表
	TotalFiles    int      `json:"totalFiles"`    // 模块总文件数
	ImpactLevel   string   `json:"impactLevel"`   // 影响级别："core"（核心路径）、"moderate"（重要但非关键）、"peripheral"（工具/辅助）
}

// CallChainItem 表示一个符号及其调用者信息。
// 用于展示被修改的函数/类在系统中的调用关系。
type CallChainItem struct {
	Symbol  string       `json:"symbol"`  // 被调用的符号名称（函数名/类名）
	File    string       `json:"file"`    // 符号所在文件
	Callers []CallerInfo `json:"callers"` // 调用该符号的调用者列表
}

// CallerInfo 描述一个符号的调用者信息。
type CallerInfo struct {
	Symbol  string `json:"symbol"`  // 调用者的符号名称
	File    string `json:"file"`    // 调用者所在文件
	Context string `json:"context"` // 调用上下文描述（如 API 端点、后台任务、测试等）
}

// RelatedPR 表示与当前 PR 历史关联的拉取请求。
// 通过分析 git 提交历史中涉及相同文件的 PR 来建立关联。
type RelatedPR struct {
	Number           int      `json:"number"`           // PR 编号
	Title            string   `json:"title"`            // PR 标题
	Author           string   `json:"author"`           // PR 作者
	MergedAt         string   `json:"mergedAt"`         // 合并时间
	OverlappingFiles []string `json:"overlappingFiles"` // 与当前 PR 重叠的文件列表
	Relevance        string   `json:"relevance"`        // 关联程度："direct"（直接修改相同文件）、"same-module"（同一模块）
}

// DesignPattern 表示在代码库中发现的设计模式或编码规范。
// 帮助审查者了解现有代码遵循的模式，确保新代码保持一致性。
type DesignPattern struct {
	Pattern     string `json:"pattern"`     // 模式名称（如 "Repository Pattern"、"Factory Method"）
	Location    string `json:"location"`    // 模式出现的位置
	Description string `json:"description"` // 模式描述
	Source      string `json:"source"`      // 信息来源："documentation"（来自文档）、"inferred"（从代码推断）
}

// RawReference 表示通过 grep 搜索找到的符号引用结果。
// 包含符号名称及其在代码库中所有出现的位置。
type RawReference struct {
	Symbol       string              `json:"symbol"`       // 被搜索的符号名称
	FoundInFiles []ReferenceLocation `json:"foundInFiles"` // 符号出现的所有位置
}

// ReferenceLocation 表示符号在代码中的单个出现位置。
type ReferenceLocation struct {
	File    string `json:"file"`    // 文件路径
	Line    int    `json:"line"`    // 行号
	Content string `json:"content"` // 该行的代码内容
}

// RawDoc 表示一个文档文件及其内容。
// 用于收集项目文档以提供给 AI 分析。
type RawDoc struct {
	Path    string `json:"path"`    // 文档文件的相对路径
	Content string `json:"content"` // 文档内容
}
