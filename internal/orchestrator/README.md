# orchestrator - 辩论式代码审查编排器

核心调度引擎，协调多个 AI 审查者进行多轮对抗式代码审查。完整流程：并行预处理 -> 多轮辩论 -> 收敛检测 -> 总结与问题提取。

## 文件说明

| 文件 | 职责 |
|------|------|
| `orchestrator.go` | `DebateOrchestrator` + `debateRun` 主流程：三阶段编排算法、消息构建、会话管理、Ledger 增量提取、Token 追踪 |
| `types.go` | 所有数据类型和接口定义（`Reviewer`, `DebateMessage`, `DebateResult`, `ReviewIssue`, `MergedIssue`, `StructurizeDelta`, `IssueMention`, `CanonicalSignal` 等） |
| `issueparser.go` | JSON 问题解析（`ParseReviewerOutput` / `ParseStructurizeDelta`）+ Jaccard 相似度去重（`DeduplicateIssues` / `DeduplicateMergedIssues`）+ 调用链格式化 |
| `ledger.go` | `IssueLedger` — 单个审查者的跨轮次问题跟踪器，提供 delta 应用、摘要生成、格式转换 |
| `canonical.go` | 跨审查者问题规范化 — `CanonicalizeMergedIssues` 相似度匹配去重、`ApplyCanonicalSignals` 信号处理、`BuildCanonicalIssueSummary` |
| `issueparser_test.go` | 问题解析、去重、Jaccard 相似度、调用链格式化的单元测试 |
| `delta_parser_test.go` | `ParseStructurizeDelta` 的单元测试 |
| `incremental_structurize_test.go` | Ledger 增量提取 + 规范化管线的集成测试 |
| `canonical_test.go` | `CanonicalizeMergedIssues` / `ApplyCanonicalSignals` 的单元测试 |
| `structurize_test.go` | `structurizeIssuesLegacy` 的集成测试（mock provider + 重试逻辑验证） |

## 完整类型层级

```
DebateOrchestrator (核心编排器 — 可复用配置)
├── reviewers: []Reviewer                       // 参与辩论的审查者列表
│   └── Reviewer
│       ├── ID: string                          // e.g. "security-reviewer", "perf-reviewer"
│       ├── Provider: AIProvider                // claude-code / gpt-4o / deepseek / ...
│       └── SystemPrompt: string               // 角色系统提示词，定义审查者的专业视角
├── analyzer: Reviewer                          // 预分析器 -- 在辩论前对 diff 做初步分析
├── summarizer: Reviewer                        // 总结器 -- 负责收敛判断 + 最终结论 + 结构化提取
├── contextGatherer: ContextGathererInterface   // 上下文收集器（nil = 禁用）
│   └── Gather(diff, prNumber, baseBranch) -> *GatheredContext
│       └── GatheredContext
│           ├── Summary: string                 // 变更摘要文本
│           ├── RawReferences: []RawReference   // 符号引用关系
│           │   └── RawReference
│           │       ├── Symbol: string          // e.g. "HandleLogin"
│           │       └── FoundInFiles: []ReferenceLocation
│           │           └── ReferenceLocation
│           │               ├── File: string    // e.g. "main.go"
│           │               ├── Line: int       // e.g. 15
│           │               └── Content: string // 引用处上下文
│           ├── AffectedModules: []AffectedModule
│           │   └── AffectedModule
│           │       ├── Name, Path: string
│           │       ├── AffectedFiles: []string
│           │       └── ImpactLevel: "core" | "moderate" | "peripheral"
│           └── RelatedPRs: []RelatedPR
│               └── Number: int, Title: string
└── options: OrchestratorOptions
    ├── MaxRounds: int                          // 最大辩论轮数
    ├── CheckConvergence: bool                  // 是否启用共识检测
    ├── Language: string                        // 输出语言 ("zh", "ja", "" = english)
    └── StructurizeMode: string                 // "legacy" | "ledger"（空值 = legacy）

debateRun (单次执行的可变状态 — 与 DebateOrchestrator 分离)
├── *DebateOrchestrator                         // 嵌入不可变配置
├── conversationHistory: []DebateMessage        // 完整的辩论对话历史
├── tokenUsage: map[string]*tokenCount          // 每个审查者的 Token 使用量
├── analysis: string                            // 预分析结果
├── gatheredContext: *GatheredContext            // 收集到的代码上下文
├── taskPrompt: string                          // 原始任务提示词（包含 diff）
├── lastSeenIndex: map[string]int               // 每个审查者最后看到的消息索引
├── issueLedgers: map[string]*IssueLedger       // 每个审查者的问题账本（ledger 模式）
├── canonicalSignals: []CanonicalSignal         // 跨审查者态度信号
└── mu: sync.Mutex                              // 保护 tokenUsage 和 canonicalSignals

DebateResult (辩论最终输出)
├── PRNumber: string
├── Analysis: string                            // 预分析报告全文
├── Context: *GatheredContext
├── Messages: []DebateMessage                   // 完整对话记录
├── Summaries: []DebateSummary                  // 每个审查者的最终总结
├── FinalConclusion: string                     // 总结器生成的最终结论
├── TokenUsage: []TokenUsage                    // Token消耗汇总
├── ConvergedAtRound: *int                      // 共识达成轮次（nil=未达成）
└── ParsedIssues: []MergedIssue                 // 结构化问题列表

ReviewIssue (单个结构化问题)
├── Severity: "critical" | "high" | "medium" | "low" | "nitpick"
├── Category: string                            // e.g. "security", "performance", "general"
├── File: string                                // 文件路径
├── Line: *int                                  // 起始行号（可选）
├── EndLine: *int                               // 结束行号（可选）
├── Title: string
├── Description: string
├── SuggestedFix: string                        // 建议修复方案（可选）
├── CodeSnippet: string                         // 代码片段（可选）
└── ClaimedBy: []string                         // 模型输出中"声称由哪些 reviewer 提出"

MergedIssue (去重合并后的问题 — 含跨审查者归属信息)
├── ReviewIssue                                 // 嵌入基础问题字段
├── CanonicalID: string                         // SHA256 确定性 ID（canonical 模式）
├── RaisedBy: []string                          // 投影自 SupportedBy
├── IntroducedBy: []string                      // 最早提出该问题的审查者
├── SupportedBy: []string                       // 当前支持该问题的审查者
├── WithdrawnBy: []string                       // 撤回支持的审查者
├── ContestedBy: []string                       // 反对该问题的审查者
├── Descriptions: []string                      // 多个审查者的描述合集
└── Mentions: []IssueMention                    // 完整的提及记录

IssueMention (审查者对问题的一次状态提及)
├── ReviewerID: string
├── LocalIssueID: string                        // ledger 内的本地 ID (e.g. "I1")
├── Round: int
└── Status: "active" | "retracted" | "support" | "withdraw" | "contest"

CanonicalSignal (跨审查者对已有问题的态度变化)
├── ReviewerID: string
├── IssueRef: string                            // 格式 "reviewerID:localIssueID"
├── Round: int
└── Action: "support" | "withdraw" | "contest"

StructurizeDelta (单个审查者单轮的增量变化)
├── Add: []DeltaAddIssue                        // 新发现的问题
├── Retract: []string                           // 撤回的本地 issue ID
├── Update: []DeltaUpdateIssue                  // 更新已有问题的字段
├── Support: []DeltaIssueRefAction              // 支持其他审查者的问题
├── Withdraw: []DeltaIssueRefAction             // 撤回对其他审查者问题的支持
└── Contest: []DeltaIssueRefAction              // 反对其他审查者的问题
```

## debateRun 模式 — 单次执行状态分离

这是一个经典的**配置与状态分离**设计模式，核心思想是将不可变的配置和每次执行的可变状态拆开，使编排器可以安全地并发复用。

### 设计动机

类比：把 `DebateOrchestrator` 想象成一台**咖啡机**（配置固定：水温、豆子种类、研磨度），`debateRun` 就是每次按下按钮后的**一次冲泡过程**（水量在变、压力在变、时间在推进）。咖啡机可以反复用，但每杯咖啡的冲泡状态是独立的——你不会把上一杯的残留水量带到下一杯。

如果不做分离，状态直接放在 `DebateOrchestrator` 上：
- 并发调用两次 `RunStreaming` → 两次执行共享同一份对话历史 → 数据竞争、结果混乱
- 第二次调用时上一次的残留状态还在 → 需要手动清理，容易遗漏

分离之后：
- 每次调用天然隔离，互不影响
- `DebateOrchestrator` 是线程安全的（不可变），可以安全地并发复用
- 不需要 reset 逻辑，`newRun()` 天然就是干净的

这是 Go 里常见的模式，类似 `http.Server`（配置）vs 每个请求的 handler context（状态）。

### 两者的职责划分

| | `DebateOrchestrator` | `debateRun` |
|---|---|---|
| 性质 | **不可变配置**（造好就不变） | **可变状态**（每次执行都不同） |
| 内容 | reviewers、analyzer、summarizer、options | 对话历史、token 统计、ledger 等 |
| 生命周期 | 创建一次，**复用多次** | 每次 `RunStreaming` **新建一个** |

### 代码结构

`DebateOrchestrator` 本身只保存**可复用的配置**（reviewers、analyzer、summarizer、options 等）。每次调用 `RunStreaming` 时，通过 `newRun(prompt)` 创建一个独立的 `debateRun`，将**可变状态**（对话历史、token 统计、ledger 等）隔离到 run 实例中：

```go
// RunStreaming 入口：
run := o.newRun(prompt)        // 创建隔离的执行状态
run.startSessions(label)       // 启动会话
defer run.endAllSessions()     // 确保清理
```

同时保留了 `legacyRun()` / `syncLegacyRun()` 桥接方法，使得同包测试和直接调用内部 helper 的旧代码可以继续通过 `DebateOrchestrator` 上的包装方法工作，内部委托给 `debateRun`。

## 核心编排流程（RunStreaming）

```
RunStreaming(ctx, label, prompt, display)
│
├── newRun(prompt)                          // 创建隔离的执行状态
├── startSessions(label)                    // 启动所有 AI 会话
│
├── 阶段1: runAnalysisPhase (并行)
│   ├── contextGatherer.Gather()            // 收集代码上下文（非致命失败）
│   └── analyzer.ChatStream()               // 预分析代码变更
│
├── 阶段2: runDebatePhase
│   ├── [if ledger mode] initIssueLedgers() // 初始化每个审查者的 ledger
│   │
│   └── for round 1..MaxRounds:
│       ├── runDebateRound()                // 快照消息 → 并行执行 → 收集结果
│       │   ├── buildMessages(reviewerID)   // 为每个审查者构建输入
│       │   ├── 并行: reviewer.ChatStream() // 所有审查者并行执行
│       │   └── append to history           // 统一添加到对话历史
│       │
│       ├── [if ledger mode]
│       │   extractRoundIssueDeltas()       // 并行为每个审查者提取 delta
│       │   ├── extractIssueDelta()         // 调用 summarizer 提取 delta JSON
│       │   ├── ledger.ApplyDelta()         // 更新本地 ledger 状态
│       │   └── applyCanonicalActions()     // 收集跨审查者信号
│       │
│       └── [if convergence enabled]
│           checkConvergence()              // 总结器判断 CONVERGED / NOT_CONVERGED
│
├── 阶段3: runSummaryPhase
│   ├── collectSummaries()                  // 并行收集审查者总结（会话感知）
│   ├── summarizer.EndSession()             // 结束总结器会话
│   │
│   ├── getFinalConclusion()                // 生成最终结论（含辩论摘要）
│   │   └── buildDebateTranscript(16000)    // 截断的辩论记录
│   │
│   └── [分支: 问题提取]
│       ├── [ledger mode] structurizeIssuesFromLedgers()
│       │   ├── collectCanonicalInputsFromLedgers()   // 收集所有 ledger 输入
│       │   ├── CanonicalizeMergedIssues()             // 跨审查者相似度匹配合并
│       │   ├── ApplyCanonicalSignals()                // 应用 support/withdraw/contest
│       │   └── [fallback] structurizeIssuesLegacy()   // ledger 全失败时回退
│       │
│       └── [legacy mode] structurizeIssuesLegacy()
│           ├── 收集所有审查者消息（按轮次）
│           ├── ParseReviewerOutput() + DeduplicateMergedIssues()
│           └── 最多重试 3 次（ChatOptions{MaxTokens: 32768}）
│
└── endAllSessions()                        // 清理所有会话
```

## extractRoundIssueDeltas 流程详解（含示例）

### 场景设定

假设有两个审查者 `claude` 和 `gpt4o`，刚结束 **Round 2** 的辩论。Round 1 时各自已经发现了一些问题：

```
claude 的 Ledger（Round 1 积累）:
  I1: [active] high/security  auth.go:15  "SQL注入风险"
  I2: [active] medium/perf    db.go:80    "N+1查询"

gpt4o 的 Ledger（Round 1 积累）:
  I1: [active] high/security  auth.go:17  "SQL注入漏洞"     ← 和 claude:I1 本质相同
  I2: [active] low/style      main.go:5   "变量命名不规范"
```

Round 2 辩论中，两人的发言（`roundOutputs`）大致是：
- **claude**: "我同意 gpt4o 的 SQL 注入问题，撤回我之前提的 N+1 查询（经确认不是问题），另外发现一个新的 XSS 问题"
- **gpt4o**: "认同 claude 说的 N+1 不是问题，我也撤回变量命名那个太 nitpick 了"

### Step 1: 构建全局问题视图（currentCanonicalSummary）

```go
currentCanonicalSummary := BuildCanonicalIssueSummary(o.currentCanonicalIssues())
```

在提取新一轮 delta 之前，先给 AI 一张"全局问题总览表"。这一步做了三件事：

1. **`collectCanonicalInputsFromLedgers()`** — 把所有审查者的 ledger 问题收集到一起：`[claude:I1, claude:I2, gpt4o:I1, gpt4o:I2]`
2. **`CanonicalizeMergedIssues()`** — 跨审查者去重合并：claude:I1 和 gpt4o:I1 因为同文件、标题相似，合并为一个 canonical issue
3. **`ApplyCanonicalSignals()`** — 应用之前轮次积累的 support/withdraw/contest 信号

最终生成的 markdown 表格大致如下：

```markdown
| Canonical ID | Severity | File:Line  | Title        | Supporters    | Issue Refs           |
|-------------|----------|------------|--------------|---------------|----------------------|
| a1b2c3d4    | high     | auth.go:15 | SQL注入风险   | claude, gpt4o | claude:I1, gpt4o:I1  |
| e5f6g7h8    | medium   | db.go:80   | N+1查询       | claude        | claude:I2            |
| i9j0k1l2    | low      | main.go:5  | 变量命名不规范 | gpt4o         | gpt4o:I2             |
```

**为什么需要这张表？** 如果没有全局视图，AI 在提取 claude 的 delta 时只知道 claude 自己的 ledger，不知道 `gpt4o:I1` 的存在，就无法正确生成 `support: [{issue_ref: "gpt4o:I1"}]`。这张表让 AI 能正确理解审查者对其他人问题的态度变化。

### Step 2: 并行提取每个审查者的 delta

```go
g, gctx := errgroup.WithContext(ctx)
for _, reviewer := range o.reviewers {
    g.Go(func() error {
        delta := o.extractIssueDelta(reviewerID, round, roundContent, ledger.BuildSummary(), canonicalSummary)
        ledger.ApplyDelta(delta, round)
        o.applyCanonicalActions(reviewerID, round, delta)
    })
}
```

两个审查者**并行**处理。以 claude 为例，`extractIssueDelta()` 把以下信息拼成 prompt 发给 summarizer：

| 输入 | 内容 |
|------|------|
| `roundContent` | claude 在 Round 2 说的原文 |
| `ledgerSummary` | claude 当前的问题清单（I1 SQL注入、I2 N+1查询） |
| `canonicalSummary` | 上面的全局问题表格 |
| `schema` | 要求输出的 JSON 格式 |

AI 返回结构化的 delta JSON（最多重试 3 次）：

```json
// claude 的 Round 2 delta
{
  "add": [{"severity": "high", "category": "security", "file": "template.go", "line": 42,
           "title": "XSS漏洞", "description": "未转义用户输入"}],
  "retract": ["I2"],
  "update": [],
  "support": [{"issue_ref": "gpt4o:I1"}],
  "withdraw": [],
  "contest": []
}
```

```json
// gpt4o 的 Round 2 delta
{
  "add": [],
  "retract": ["I2"],
  "update": [],
  "support": [],
  "withdraw": [],
  "contest": []
}
```

### Step 3: ledger.ApplyDelta() — 更新本地账本

**claude 的 Ledger 变化：**

```
I1: [active]    high/security  auth.go:15     "SQL注入风险"   ← 不变
I2: [retracted] medium/perf    db.go:80       "N+1查询"       ← retract
I3: [active]    high/security  template.go:42 "XSS漏洞"       ← 新增（add）
```

**gpt4o 的 Ledger 变化：**

```
I1: [active]    high/security  auth.go:17  "SQL注入漏洞"      ← 不变
I2: [retracted] low/style      main.go:5   "变量命名不规范"    ← retract
```

### Step 4: applyCanonicalActions() — 收集跨审查者信号

从 claude 的 delta 中提取 `support: [gpt4o:I1]`，生成：

```go
CanonicalSignal{ReviewerID: "claude", IssueRef: "gpt4o:I1", Round: 2, Action: "support"}
```

加锁写入 `o.canonicalSignals`。

### 最终状态（Round 2 结束后）

```
claude Ledger:  I1(active:SQL注入)  I2(retracted:N+1)  I3(active:XSS)
gpt4o Ledger:   I1(active:SQL注入)  I2(retracted:命名)

canonicalSignals: [claude supports gpt4o:I1]
```

辩论结束后，`structurizeIssuesFromLedgers()` 会把两个 ledger 合并：
- claude:I1 和 gpt4o:I1 因为同文件、标题相似 → 合并为一个 canonical issue，`SupportedBy: [claude, gpt4o]`
- claude:I3 (XSS) → 独立的 canonical issue，`SupportedBy: [claude]`
- 两个 retracted 的问题被标记 `WithdrawnBy`，最终因为 `SupportedBy` 为空而被过滤掉

### 全景图

```
extractRoundIssueDeltas(round=2, roundOutputs)
│
│ ┌─────────────────────────────────────────────────────┐
│ │ Step 0: 构建全局问题视图                              │
│ │                                                     │
│ │ currentCanonicalIssues()                            │
│ │ ├── collectCanonicalInputsFromLedgers()             │
│ │ │   把所有审查者 ledger 的问题倒成一个扁平列表         │
│ │ │   [claude:I1, claude:I2, gpt4o:I1, gpt4o:I2]     │
│ │ │                                                   │
│ │ ├── CanonicalizeMergedIssues()                      │
│ │ │   跨审查者去重合并（文件+行号+文本相似度）            │
│ │ │   claude:I1 + gpt4o:I1 → 合并为 1 个               │
│ │ │                                                   │
│ │ └── ApplyCanonicalSignals()                         │
│ │     应用历史轮次的 support/withdraw/contest           │
│ │     过滤掉无人支持的问题                              │
│ │                                                     │
│ │ BuildCanonicalIssueSummary() → Markdown 表格         │
│ │ ┌──────────────────────────────────────────────┐     │
│ │ │ ID       │ Severity │ File     │ Supporters  │     │
│ │ │ a1b2c3d4 │ high     │ auth.go  │ claude,gpt4o│     │
│ │ │ e5f6g7h8 │ medium   │ db.go    │ claude      │     │
│ │ │ i9j0k1l2 │ low      │ main.go  │ gpt4o       │     │
│ │ └──────────────────────────────────────────────┘     │
│ └─────────────────────────────────────────────────────┘
│
│ ┌─────────────────────────────────────────────────────┐
│ │ Step 1: 并行提取每个审查者的 delta                     │
│ │                                                     │
│ │  errgroup.Go ──┬── claude 分支 ──┬── gpt4o 分支      │
│ │                │                 │                   │
│ │  ┌─────────────▼──────┐  ┌──────▼──────────────┐    │
│ │  │ extractIssueDelta  │  │ extractIssueDelta    │    │
│ │  │                    │  │                      │    │
│ │  │ 输入:              │  │ 输入:                │    │
│ │  │  · claude 的发言    │  │  · gpt4o 的发言      │    │
│ │  │  · claude 的 ledger │  │  · gpt4o 的 ledger   │    │
│ │  │  · 全局问题表格     │  │  · 全局问题表格       │    │
│ │  │  · JSON schema     │  │  · JSON schema       │    │
│ │  │                    │  │                      │    │
│ │  │ 拼成 prompt        │  │ 拼成 prompt          │    │
│ │  │ 发给 summarizer AI │  │ 发给 summarizer AI   │    │
│ │  │ (最多重试 3 次)     │  │ (最多重试 3 次)       │    │
│ │  │                    │  │                      │    │
│ │  │ 输出:              │  │ 输出:                │    │
│ │  │ {                  │  │ {                    │    │
│ │  │  add: [XSS漏洞],   │  │  add: [],            │    │
│ │  │  retract: ["I2"],  │  │  retract: ["I2"],    │    │
│ │  │  support: [gpt4o:  │  │  support: [],        │    │
│ │  │    I1],            │  │  withdraw: [],       │    │
│ │  │  ...               │  │  ...                 │    │
│ │  │ }                  │  │ }                    │    │
│ │  └────────┬───────────┘  └──────┬───────────────┘    │
│ │           │                     │                    │
│ └───────────┼─────────────────────┼────────────────────┘
│             │                     │
│ ┌───────────▼─────────────────────▼────────────────────┐
│ │ Step 2: 更新本地 Ledger (ledger.ApplyDelta)           │
│ │                                                     │
│ │ claude Ledger:                gpt4o Ledger:          │
│ │  I1 [active]  SQL注入         I1 [active]  SQL注入    │
│ │  I2 [retracted] N+1查询       I2 [retracted] 命名     │
│ │  I3 [active]  XSS ← 新增                             │
│ └─────────────────────────────────────────────────────┘
│
│ ┌─────────────────────────────────────────────────────┐
│ │ Step 3: 收集跨审查者信号 (applyCanonicalActions)      │
│ │                                                     │
│ │ 从 delta 的 support/withdraw/contest 字段提取信号      │
│ │                                                     │
│ │ claude 的 delta 有 support:[gpt4o:I1]                │
│ │ → CanonicalSignal{claude, "gpt4o:I1", round=2,      │
│ │                   action="support"}                  │
│ │                                                     │
│ │ 加锁写入 o.canonicalSignals（因为并行所以要锁）         │
│ └─────────────────────────────────────────────────────┘
│
│ g.Wait()  ← 等待所有审查者完成
│
└── return（本轮处理结束，进入下一轮或总结阶段）
```

### 数据流总结

整个函数是一个**感知-提取-更新**的循环：

```
全局视图（给 AI 看的地图）
       ↓
AI 从发言中提取增量变化（add/retract/update/support/withdraw/contest）
       ↓
更新两个地方：
  ├── 本地 Ledger（每个审查者自己的问题账本）
  └── 全局 Signals（跨审查者的态度信号池）
       ↓
下一轮开始时，重新构建全局视图（包含了本轮的更新）→ 循环
```

复杂的根源在于它同时维护了两个层次的状态：
- **局部状态**（Ledger）：每个审查者独立维护自己发现的问题，可以新增、撤回、更新
- **全局状态**（Canonical + Signals）：跨审查者的问题合并视图和态度信号

每轮结束时，先从局部状态推导出全局视图给 AI 看，AI 再产出新的局部变更，如此循环直到辩论结束。

### applyCanonicalActions vs ApplyCanonicalSignals

这两个函数名字相似但职责完全不同，一个是**收集信号**，一个是**兑现信号**：

| | `applyCanonicalActions` | `ApplyCanonicalSignals` |
|---|---|---|
| **阶段** | 每轮结束时（辩论进行中） | 辩论结束时 / 构建全局视图时 |
| **做什么** | 从 delta 里**收集**信号，扔进信号池 | 把信号池里的信号**应用**到 canonical issues 上 |
| **类比** | 往信箱里**投信** | 把信箱里的信**拆开处理** |
| **输入** | 一个审查者的一轮 delta | 所有 canonical issues + 全部历史信号 |
| **输出** | 追加到 `o.canonicalSignals` | 更新后的 issue 列表（SupportedBy/WithdrawnBy/ContestedBy 已变更） |
| **可见性** | unexported（内部方法） | Exported（公开函数） |

时间线视角：

```
Round 1 结束
  applyCanonicalActions(claude, delta) → 收集: []            ← 第一轮没有跨审查者信号
  applyCanonicalActions(gpt4o, delta)  → 收集: []

Round 2 结束
  applyCanonicalActions(claude, delta) → 收集: [claude supports gpt4o:I1]
  applyCanonicalActions(gpt4o, delta)  → 收集: []

  信号池: [claude supports gpt4o:I1]

                    ↓ 辩论结束，最终合并 ↓

structurizeIssuesFromLedgers()
  CanonicalizeMergedIssues(...)         → 先做文本相似度去重合并
  ApplyCanonicalSignals(issues, 信号池)  → 再把信号池里的信号应用上去
    → "claude supports gpt4o:I1"
    → 找到包含 gpt4o:I1 的 canonical issue
    → 把 claude 加入 SupportedBy
    → 过滤掉 SupportedBy 为空的
```

## 双模问题提取：Legacy vs Ledger

通过 `OrchestratorOptions.StructurizeMode` 控制：

### Legacy 模式（默认）

一次性提取：辩论结束后，将所有审查者的全部轮次消息打包发给 summarizer，要求输出完整的结构化问题 JSON。

```
所有轮次消息 → structurizeIssuesLegacy()
                ├── 构建完整 reviewText（所有审查者×所有轮次）
                ├── summarizer.Chat(structurize_issues.tmpl)
                ├── ParseReviewerOutput() → ReviewerOutput
                ├── issuesToMerged() → []MergedIssue
                ├── DeduplicateMergedIssues() → 去重排序
                └── 失败时最多重试 3 次（含 schema 验证反馈）
```

### Ledger 模式（增量式）

每轮结束后实时提取增量 delta，维护每个审查者的本地 ledger，最终通过 canonical 匹配合并。

```
每轮结束后:
  extractRoundIssueDeltas()
  ├── 并行: 每个 reviewer
  │   ├── extractIssueDelta() → StructurizeDelta
  │   │   ├── 构建 prompt（含 ledger 摘要 + canonical 摘要）
  │   │   ├── summarizer.Chat(structurize_delta.tmpl)
  │   │   └── ParseStructurizeDelta() → 解析 + schema 验证
  │   ├── ledger.ApplyDelta(delta, round)
  │   │   ├── Add → 分配本地 ID (I1, I2, ...), 设为 active
  │   │   ├── Retract → 标记为 retracted
  │   │   └── Update → 更新指定字段
  │   └── applyCanonicalActions(delta)
  │       └── support/withdraw/contest → CanonicalSignal

辩论结束后:
  structurizeIssuesFromLedgers()
  ├── collectCanonicalInputsFromLedgers()
  │   └── 每个 ledger.ToCanonicalInputs() → 含 retracted 的完整记录
  ├── CanonicalizeMergedIssues(allIssues)
  │   ├── 按 (firstMentionRound, severity, file, title) 排序
  │   ├── canonicalMatchScore() 相似度匹配
  │   │   ├── 必须 same file
  │   │   ├── 行号兼容性检查（±8行）
  │   │   ├── title Jaccard * 0.6 + desc Jaccard * 0.35 + category bonus
  │   │   └── 多组阈值: (exact title + desc≥0.12), (title≥0.58 + desc≥0.18), ...
  │   ├── mergeCanonicalIssue() → 合并归属信息
  │   └── finalizeCanonicalIssue() → 去重、计算 CanonicalID (SHA256)
  ├── ApplyCanonicalSignals(issues, canonicalSignals)
  │   ├── 按 (round, reviewerID, issueRef, action) 排序
  │   ├── 匹配 issueRef → applySignalToIssue()
  │   │   ├── support → 加入 SupportedBy, 移出 WithdrawnBy/ContestedBy
  │   │   ├── withdraw → 移出 SupportedBy, 加入 WithdrawnBy
  │   │   └── contest → 移出 SupportedBy, 加入 ContestedBy
  │   └── 过滤：SupportedBy 为空的问题被移除
  └── [fallback] 无问题时回退到 structurizeIssuesLegacy()
```

## IssueLedger 详解（ledger.go）

每个审查者拥有一个独立的 `IssueLedger`，在辩论过程中跟踪该审查者发现的问题：

```go
type IssueLedger struct {
    ReviewerID string
    Issues     map[string]*LedgerIssue  // key = 本地 ID ("I1", "I2", ...)
    nextID     int                       // 自增 ID 计数器
}

type LedgerIssue struct {
    ID, Status       string  // Status: "active" | "retracted"
    Severity, Category, File string
    Line             *int
    Title, Description, SuggestedFix string
    Round, LastRound int     // 首次出现轮次 / 最后变更轮次
    Mentions         []IssueMention
}
```

**核心方法：**

| 方法 | 职责 |
|------|------|
| `ApplyDelta(delta, round)` | 应用增量：Add 分配新 ID 并标记 active，Retract 标记 retracted，Update 覆盖指定字段 |
| `BuildSummary()` | 生成活跃问题的 markdown 表格（按 severity 排序，最多 100 条），用于注入下一轮 delta 提取 prompt |
| `ToMergedIssues()` | 将**活跃**问题转换为 `[]MergedIssue`（RaisedBy/SupportedBy/IntroducedBy 均设为当前 reviewer） |
| `ToCanonicalInputs()` | 将**所有**问题（含 retracted）转换为 `[]MergedIssue`，retracted 的放入 WithdrawnBy |

## Canonical 问题合并详解（canonical.go）

跨审查者的问题规范化管线：

### CanonicalizeMergedIssues

将多个审查者的 `[]MergedIssue` 合并为去重的规范问题列表：

1. **排序**：按 (firstMentionRound, severity, file, title) 排序输入
2. **逐个匹配**：对每个 issue，与已有 canonical 列表计算 `canonicalMatchScore()`
3. **匹配条件**（`canonicalMatchScore`）：
   - 必须同一文件
   - 行号兼容性（`canonicalLineCompatible`）：均有行号时 ±8 行内；一边缺失时要求 title≥0.78 + desc≥0.18
   - 加权分数 = title_jaccard * 0.6 + desc_jaccard * 0.35 + category_bonus(0.05)
   - 多组通过阈值（exact title match、title≥0.58+desc≥0.18、title≥0.45+desc≥0.34、title≥0.75+desc≥0.10）
4. **合并**（`mergeCanonicalIssue`）：保留更高 severity 的 ReviewIssue，合并所有归属列表
5. **最终化**（`finalizeCanonicalIssue`）：去重归属、计算 `CanonicalID`（SHA256 of file|category|title|desc）、过滤无支持者的问题

### ApplyCanonicalSignals

在 canonical 合并后，应用辩论过程中收集的跨审查者态度信号：

- 按 (round, reviewerID, issueRef, action) 排序信号
- 通过 `issueRef`（格式 `"reviewerID:localIssueID"`）匹配 canonical issue
- `applySignalToIssue`：support 加入 SupportedBy、withdraw 移出 SupportedBy、contest 加入 ContestedBy
- 最终过滤 `SupportedBy` 为空的问题

### BuildCanonicalIssueSummary

生成 markdown 表格，包含 Canonical ID、Severity、File:Line、Title、Supporters、Withdrawn、Contested、Issue Refs，用于注入 delta 提取 prompt 提供全局视图。

## 消息构建策略

```
buildMessages(reviewerID)
├── [第1轮] buildFirstRoundMessages()
│   ├── taskPrompt (包含diff)
│   ├── contextSection (gatheredContext.Summary)
│   ├── focusSection (ParseFocusAreas → analyzer 建议的关注点)
│   └── callChainSection (FormatCallChainForReviewer)
│
└── [第2轮+] collectPreviousRoundsMessages()
    ├── [会话模式] buildSessionDebateMessages()
    │   └── 仅发送最新一轮其他审查者的消息（增量更新）
    └── [非会话模式] buildFullContextDebateMessages()
        ├── 完整任务+分析上下文
        ├── 所有历史消息（其他审查者 → user角色）
        └── 自己的历史消息（→ assistant角色）
```

## 问题解析与去重（issueparser.go）

### ParseReviewerOutput

从审查者响应文本中提取结构化的 `ReviewerOutput`：

```
响应文本 → extractJSON(rawJSONRe)
         ├── 优先: ```json 代码块
         └── 回退: 裸 JSON 对象（含 "issues" 数组）
         → schema.ValidateIssuesJSON() → schema 校验
         → json.Unmarshal → 逐个字段验证
         │  ├── severity: 必须有效 (critical/high/medium/low/nitpick)
         │  ├── file: 必须非空，支持 "file.py:37-48" 嵌入行号
         │  ├── title, description: 必须非空
         │  ├── category: 默认 "general"
         │  ├── line/endLine: 可选，JSON 字段优先于路径提取
         │  └── suggestedFix, codeSnippet, raisedBy(→ClaimedBy): 可选
         → ParseResult{Output, RawJSON, SchemaErrors, ParseError}
```

### ParseStructurizeDelta

从模型响应中解析单轮增量 `StructurizeDelta`：

```
响应文本 → extractJSON(rawDeltaJSONRe)
         ├── 优先: ```json 代码块
         └── 回退: 裸 JSON（含 add/retract/update/support/withdraw/contest）
         → schema.ValidateJSON("issues_delta")
         → json.Unmarshal → 要求所有 6 个键存在
         ├── Add: 逐个验证 severity/file/title/description 必填
         ├── Retract: 字符串 ID 列表
         ├── Update: 必须有 id，其余字段可选覆盖
         └── Support/Withdraw/Contest: parseDeltaIssueRefActions() → issueRef 去重
         → DeltaParseResult{Output, RawJSON, SchemaErrors, ParseError}
```

### DeduplicateIssues / DeduplicateMergedIssues

跨审查者合并相似问题（legacy 模式使用）：

```
相似度判定 (isSimilarIssue):
├── 必须同文件
├── 行号范围重叠或相邻（±5行）
└── 加权 Jaccard > 0.35 (title*0.7 + desc*0.3)

合并规则:
├── 保留最高 severity
├── 合并 RaisedBy / SupportedBy / Descriptions / Mentions
├── 补充 SuggestedFix
└── finalizeCanonicalIssue() → 计算 CanonicalID + 去重
```

## 共识检测与收敛

```
checkConvergence()
├── 所有已完成轮次的消息（含轮次标注）
├── convergence_check.tmpl → 严格共识判定标准
├── summarizer.Chat() → 响应文本
└── 解析最后一行首词: "CONVERGED" / "NOT_CONVERGED"
    ├── CONVERGED → 提前终止辩论
    └── NOT_CONVERGED → 继续下一轮
```

共识从 Round 1 开始检测，独立审查达成一致也是有效共识。

## 总结与结论

```
collectSummaries()
├── 并行: 每个 reviewer
│   ├── [会话模式] 合并最新上下文 + 总结要求到单条消息
│   └── [非会话模式] 追加总结要求到完整消息列表
└── reviewer.ChatStream() → DebateSummary

runSummaryPhase()
├── collectSummaries()
├── summarizer.EndSession()           // 结束会话，后续不依赖上下文
├── getFinalConclusion()
│   ├── 匿名审查者总结 + 辩论摘要（截断 16000 字符）
│   └── final_conclusion.tmpl → 流式输出
└── structurizeIssues[FromLedgers|Legacy]()
```

## Token 估算

```go
estimateTokens(text) = CJK字符数 * 0.7 + 非CJK字符数 / 4.0
```

- CJK：`unicode.Is(unicode.Han, r)` + U+3000-U+303F + U+FF00-U+FFEF
- 费用预估：`(inputTokens + outputTokens) * 0.00001`

## 关键实现细节

### debateRun 状态隔离

```go
// RunStreaming → newRun() 创建独立状态
run := o.newRun(prompt)   // tokenUsage、lastSeenIndex、issueLedgers 全新

// 测试兼容 → legacyRun() 桥接旧字段
run := o.legacyRun()      // 共享 DebateOrchestrator 上的字段
defer o.syncLegacyRun(run) // 回写变更
```

### 消息快照保证一致性

```go
// runDebateRound: 先为所有审查者构建消息快照
tasks := make([]reviewerTask, len(o.reviewers))
for i, r := range o.reviewers {
    tasks[i].messages = o.buildMessages(r.ID) // 快照，确保所有审查者看到相同信息
}
// 然后并行执行，统一添加到历史
```

### Ledger 增量提取并行化

```go
// extractRoundIssueDeltas: 并行为每个审查者提取 delta
g, gctx := errgroup.WithContext(ctx)
for _, reviewer := range o.reviewers {
    g.Go(func() error {
        delta, err := o.extractIssueDelta(...)  // 调用 summarizer
        ledger.ApplyDelta(delta, round)         // 更新本地状态
        o.applyCanonicalActions(...)            // 收集跨审查者信号
        return nil
    })
}
```

### extractIssueDelta 重试

```go
// 最多 3 次尝试，每次失败将验证错误注入下一次 prompt
chatOpts := &provider.ChatOptions{DisableTools: true, MaxTokens: 8192}
for attempt := 1; attempt <= 3; attempt++ {
    response := summarizer.Chat(prompt, systemPrompt, chatOpts)
    parsed := ParseStructurizeDelta(response)
    if parsed.Output != nil { return parsed.Output }
    // 注入错误信息重试
}
```

### structurizeIssuesLegacy 重试与 best-effort

```go
chatOpts := &provider.ChatOptions{DisableTools: true, MaxTokens: 32768}
for attempt := 1; attempt <= 3; attempt++ {
    response := summarizer.Chat(prompt, systemPrompt, chatOpts)
    parsed := ParseReviewerOutput(response)
    if len(parsedIssues) > len(bestEffort) { bestEffort = parsedIssues }
    if success { return parsedIssues }
}
return bestEffort  // 返回最优部分结果
```

### Canonical ID 生成

```go
// buildCanonicalID: SHA256(file|category|normalizedTitle|truncatedDesc)
key := file + "|" + category + "|" + normalizedTitle + "|" + truncatedDesc[:160]
sum := sha256.Sum256([]byte(key))
return fmt.Sprintf("%x", sum[:8])  // 16 字符十六进制
```

### 总结器会话提前结束

```go
// runSummaryPhase: 结束总结器会话后，conclusion 和 structurize 构建全新消息
if sp, ok := o.summarizer.Provider.(provider.SessionProvider); ok {
    sp.EndSession()
}
// 后续 getFinalConclusion 和 structurizeIssues 不依赖会话上下文
```
