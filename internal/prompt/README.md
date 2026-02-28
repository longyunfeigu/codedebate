# prompt

基于 Go `text/template` 的 LLM Prompt 渲染引擎。所有 `.tmpl` 模板通过 `go:embed` 编译期嵌入，启动时一次性解析，运行时按名称渲染。

## API

```go
prompt.Render("reviewer_first_round.tmpl", data)   // 返回 (string, error)
prompt.MustRender("reviewer_first_round.tmpl", data) // panic on error
```

## 模板一览

### 1. 首轮审查

**`reviewer_first_round.tmpl`** — 每个 Reviewer 独立审查 diff 的入口 prompt。

| 变量 | 说明 |
|------|------|
| `TaskPrompt` | 审查任务描述（PR 标题 + 描述） |
| `ContextSection` | 上下文分析结果（模块影响、调用链等） |
| `FocusSection` | 重点关注方向（bullet 形式，不注入全文以保持独立性） |
| `CallChainSection` | 调用链分析摘要 |
| `ReviewerID` | 当前 Reviewer 标识（如 `R1`） |
| `Language` | 响应语言（可选，如 `Chinese`） |

要求 Reviewer 逐文件逐函数审查，检查正确性/安全/性能/错误处理/边界/可维护性，必须引用文件路径和行号。

### 2. 辩论环节

**`reviewer_debate_session.tmpl`** — 多轮辩论中的单轮 prompt（增量模式，只注入上一轮新内容）。

| 变量 | 说明 |
|------|------|
| `ReviewerID` | 当前 Reviewer 标识 |
| `NewContent` | 上一轮其他 Reviewer 的输出 |
| `Language` | 响应语言（可选） |

要求：继续覆盖未审查的文件、指出他人遗漏、回应他人观点。

**`reviewer_debate_full.tmpl`** — 全量辩论 prompt（注入完整历史，用于首次进入辩论或上下文重建）。

| 变量 | 说明 |
|------|------|
| `TaskPrompt` | 审查任务描述 |
| `Analysis` | 上下文分析全文 |
| `ReviewerID` | 当前 Reviewer 标识 |
| `OtherLabel` | 其他 Reviewer 标识列表（如 `[R1], [R3]`） |
| `PluralS` | 复数后缀（`s` 或空） |
| `OtherWord` | `is` 或 `are` |
| `Language` | 响应语言（可选） |

### 3. 摘要

**`reviewer_summary.tmpl`** — 要求 Reviewer 匿名总结关键发现。

| 变量 | 说明 |
|------|------|
| `Language` | 响应语言（可选） |

无其他变量，仅指示 Reviewer 压缩输出、隐藏身份。

### 4. 收敛判定

**`convergence_system.tmpl`** — system prompt，设定严格共识裁判角色。无变量。

**`convergence_check.tmpl`** — 判断 Reviewer 们是否已达成共识。

| 变量 | 说明 |
|------|------|
| `ReviewerCount` | Reviewer 总数 |
| `IsFirstRound` | 是否为首轮（首轮使用独立共识标准） |
| `RoundsCompleted` | 已完成的轮次数 |
| `MessagesText` | 所有轮次的审查文本 |

输出最后一行必须为 `CONVERGED` 或 `NOT_CONVERGED`。

### 5. 最终结论

**`final_conclusion.tmpl`** — 综合所有摘要和辩论，输出最终审查结论。

| 变量 | 说明 |
|------|------|
| `ReviewerCount` | Reviewer 总数 |
| `SummaryText` | 各 Reviewer 的匿名摘要 |
| `DebateText` | 完整辩论记录 |
| `Language` | 响应语言（可选） |

输出：共识点、分歧点、行动项、不可验证项。

### 6. 结构化提取（全量）

**`structurize_system.tmpl`** — system prompt，设定 JSON 提取角色。无变量。

**`structurize_issues.tmpl`** — 将自然语言审查意见转为结构化 JSON issues 列表。

| 变量 | 说明 |
|------|------|
| `ReviewText` | 完整审查文本 |
| `Schema` | JSON Schema 定义 |
| `ReviewerIDs` | Reviewer ID 列表（如 `R1, R2, R3`） |
| `Language` | 响应语言（可选，影响 title/description/suggestedFix 语言） |

每个 issue 包含：file、line、severity、title、description（含问题/影响/原始代码/修复建议）、raisedBy。

### 7. 结构化提取（增量）

**`structurize_delta_system.tmpl`** — system prompt，设定增量 delta 提取角色。无变量。

**`structurize_delta.tmpl`** — 针对单个 Reviewer 的单轮输出，增量提取 issue delta。

| 变量 | 说明 |
|------|------|
| `ReviewerID` | 当前 Reviewer 标识 |
| `Round` | 辩论轮次 |
| `RoundContent` | 该 Reviewer 本轮输出 |
| `LedgerSummary` | 该 Reviewer 已有的 issue 列表摘要 |
| `CanonicalSummary` | 跨 Reviewer 的 canonical issue 汇总表 |
| `Schema` | delta JSON Schema |
| `Language` | 响应语言（可选） |

输出 delta 操作：`add`（新增）、`retract`（撤回）、`update`（更新）、`support`（支持 canonical issue）、`withdraw`（撤回支持）、`contest`（反对）。

### 8. 结构化重试

**`structurize_retry.tmpl`** — 当 JSON 解析失败时，将错误信息反馈给 LLM 让其修正。

| 变量 | 说明 |
|------|------|
| `ValidationErrors` | 上次输出的校验错误信息 |
| `ReviewText` | 原始审查文本 |
| `Schema` | JSON Schema |
| `ReviewerIDs` | Reviewer ID 列表 |

### 9. 上下文分析

**`context_analysis.tmpl`** — 在审查前分析 PR 的系统影响。

| 变量 | 说明 |
|------|------|
| `Diff` | PR 完整 diff |
| `ChangedFiles` | 变更文件列表 |
| `References` | grep 得到的函数/类引用结果 |
| `RelatedPRs` | 相关的近期 PR |
| `Docs` | 项目文档 |

输出 JSON：`affectedModules`（受影响模块）、`callChain`（调用链）、`designPatterns`（设计模式）、`summary`（给 Reviewer 的摘要）。

### 10. 服务端审查

**`server_review.tmpl`** — 用于服务端（如 GitLab MR）场景，直接将 diff 注入 prompt。

| 变量 | 说明 |
|------|------|
| `MRURL` | MR/PR 的 URL |
| `Title` | MR/PR 标题 |
| `Description` | MR/PR 描述 |
| `Diff` | 完整 diff（行号前缀） |
| `HasLocalRepo` | 是否有本地仓库可供浏览 |

## 流水线概览

```
context_analysis → reviewer_first_round (×N 并行)
                       ↓
               convergence_check
              ┌────────┴────────┐
          CONVERGED        NOT_CONVERGED
              ↓                 ↓
     reviewer_summary    reviewer_debate_session (×N)
              ↓                 ↓
     final_conclusion    convergence_check (循环)
              ↓
     structurize_issues  ←── structurize_retry (失败时)
```

增量模式下 `structurize_delta` 在每轮辩论后即时提取 delta，替代最终一次性全量提取。
