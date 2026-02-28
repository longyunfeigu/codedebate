# display - 终端 UI 和输出格式化

终端彩色输出、spinner 动画、Markdown 渲染，以及服务端模式的静默显示。负责将 orchestrator 的审查过程和结果以可读的形式呈现给用户。

## 文件说明

| 文件 | 说明 |
|------|------|
| `terminal.go` | 彩色终端 UI：spinner 动画、审查进度、结果展示、问题表格、Token 用量、20 条程序员冷笑话 |
| `markdown.go` | Markdown 报告生成（`FormatMarkdown`）、Glamour 终端渲染（`RenderTerminalMarkdown`）、导出数据类型 |
| `noop.go` | 静默显示（服务端模式），所有回调仅写日志，不输出终端 UI |
| `noop_test.go` | NoopDisplay 的单元测试 |

## DisplayCallbacks 接口

orchestrator 通过此接口回调显示层，解耦编排逻辑和展示逻辑：

```go
DisplayCallbacks interface {
    OnWaiting(reviewerID string)                                  // 审查者等待中（显示 spinner）
    OnMessage(reviewerID string, content string)                  // 审查者输出内容（渲染 Markdown）
    OnParallelStatus(round int, statuses []ReviewerStatus)        // 并行审查状态更新
    OnRoundComplete(round int, converged bool)                    // 轮次完成（CONVERGED / NOT CONVERGED）
    OnConvergenceJudgment(verdict string, reasoning string)       // 收敛判断结果和推理过程
    OnContextGathered(context *GatheredContext)                   // 上下文收集完成
}
```

## 两种实现对比

| 特性 | `Display` (CLI 模式) | `NoopDisplay` (服务端模式) |
|------|---------------------|--------------------------|
| 使用场景 | `codedebate review` 交互式 CLI | `codedebate serve` Webhook 服务器 |
| OnWaiting | spinner 动画 + 随机冷笑话 | `logger.Printf("[waiting] %s")` |
| OnMessage | Markdown → Glamour 终端渲染 | 截断到 200 字符后写日志 |
| OnParallelStatus | 实时更新 spinner 文本（颜色状态） | `logger.Printf("[parallel] round %d")` |
| OnRoundComplete | 彩色 CONVERGED/NOT CONVERGED 标记 | `logger.Printf("[round] %d complete")` |
| OnConvergenceJudgment | 灰色逐行显示推理过程 | `logger.Printf("[convergence] verdict=%s")` |
| OnContextGathered | 按影响级别着色的模块列表 + 关联 PR | `logger.Printf("[context] %d modules")` |
| 依赖 | spinner、color、glamour | 仅 log.Logger |

## 终端输出示例

执行 `codedebate review 42` 时用户实际看到的终端输出：

```
  ==================================================
  CodeDebate Code Review
  ==================================================

  Target:      PR #42 - feat: add user authentication
  Reviewers:   security-reviewer, perf-reviewer
  Max Rounds:  2
  Convergence: enabled
  Context:     enabled

──────────────────────────────────────────────────
  System Context
──────────────────────────────────────────────────

Affected Modules:
  * Authentication Module (2 files)
  * API Router (1 files)

Related Changes:
  * #38: refactor: extract auth middleware

[AI context summary rendered as markdown]

──────────────────────────────────────────────────
  Analysis
──────────────────────────────────────────────────

[analyzer output rendered as markdown]

|- security-reviewer [Round 1/2]
|
[review content rendered as markdown]

|- perf-reviewer [Round 1/2]
|
[review content rendered as markdown]

-- Round 1/2 complete --

|- Convergence Judge ──────────────────────────────
| The reviewers agree on the SQL injection issue...
| Both identified the missing error handling...
|- Verdict: CONVERGED

  Round 1/2 - CONSENSUS REACHED
   Stopping early to save tokens.

══════════════════════════════════════════════════
  Final Conclusion
══════════════════════════════════════════════════

[final conclusion rendered as markdown]

──────────────────────────────────────────────────
  Issues Found (4 unique, 6 total across reviewers)
──────────────────────────────────────────────────

   1. [CRITICAL] SQL injection vulnerability
      auth.go:42  [security-reviewer, perf-reviewer]
      Fix: Use parameterized queries instead of string concat...

   2. [HIGH    ] Missing error handling
      handler.go:15  [security-reviewer]

   3. [MEDIUM  ] N+1 query pattern
      service.go:88  [perf-reviewer]

   4. [LOW     ] Unused import
      config.go:3  [perf-reviewer]

──────────────────────────────────────────────────
  Token Usage (Estimated)
──────────────────────────────────────────────────
  security-reviewer    5,200 in     1,800 out
  perf-reviewer        5,200 in     1,500 out
──────────────────────────────────────────────────
  Total               10,400 in     3,300 out  ~$0.1370

  Converged at round 1
```

严重等级着色规则：

```
critical → 红色粗体
high     → 红色
medium   → 黄色
low      → 蓝色
nitpick  → 灰色
```

## Markdown 报告输出示例

`FormatMarkdown(result)` 默认生成“交付视图”报告，优先展示结论与问题，不包含完整辩论转录：

```markdown
# Code Review: PR #42

## Final Conclusion

[synthesized conclusion]

## System Context

### Affected Modules

#### Authentication Module

- Path: `internal/auth`
- Impact: `core`
- File Count: `2`
- Affected Files:
  - `internal/auth/handler.go`
  - `internal/auth/service.go`

### Affected Files

- `internal/auth/handler.go`
- `internal/auth/service.go`

### Related Changes

- #38: refactor: extract auth middleware

### Context Summary

[context gatherer summary]

## Issues (4)

1. **[CRITICAL]** SQL injection vulnerability
   - Location: `auth.go:42`
   - Found by: security-reviewer, perf-reviewer
   - Why: The query is built with string concatenation.
   - Fix: Use parameterized queries

2. **[HIGH]** Missing error handling
   - Location: `handler.go:15`
   - Found by: security-reviewer

## Analysis

[analyzer output]

## Reviewer Summaries

### security-reviewer

[reviewer summary]

### perf-reviewer

[reviewer summary]

## Token Usage

| Reviewer | Input | Output |
|----------|------:|-------:|
| security-reviewer | 5,200 | 1,800 |
| perf-reviewer | 5,200 | 1,500 |
| **Total** | **10,400** | **3,300** |

Converged at round 1.
```

若需要附带完整辩论过程，可用 `FormatMarkdownWithOptions(result, MarkdownOptions{IncludeDebateTranscript: true})`。
导出的附录会按 `Round 1 / Round 2` 分组，并过滤 `[tool] ...` 这类过程噪音。

对于本地变更（非 PR），标题格式不同：

```markdown
# Local Changes Review       (codedebate review --diff)
# Last Commit abc1234 Review  (codedebate review --last-commit)
```

## 等待冷笑话

`OnWaiting` 和 `OnParallelStatus` 在 spinner 旁随机显示 20 条内置程序员冷笑话，缓解等待焦虑：

```
  security-reviewer is thinking... | Why do programmers confuse Halloween and Christmas? Because Oct 31 = Dec 25
  Round 1: [* security-reviewer | * perf-reviewer] | There's no place like 127.0.0.1
```

部分冷笑话摘录：
- `Why do programmers confuse Halloween and Christmas? Because Oct 31 = Dec 25`
- `A SQL query walks into a bar, walks up to two tables and asks: "Can I join you?"`
- `99 little bugs in the code, take one down, patch it around... 127 little bugs in the code.`
- `There are two hard things in computer science: cache invalidation, naming things, and off-by-one errors.`
- `Git commit -m "fixed it for real this time"`

## Display 关键功能

| 方法 | 功能 |
|------|------|
| `ReviewHeader` | 彩色横幅：审查目标、审查者列表、轮数、收敛检测、上下文开关 |
| `OnWaiting` | spinner + 标签（根据 reviewerID 区分：context-gatherer/analyzer/summarizer/convergence-check/structurizer/round-N/普通审查者）+ 随机冷笑话 |
| `OnMessage` | 审查者切换时打印标题头，`RenderTerminalMarkdown`（glamour，120 字符换行）渲染内容 |
| `OnParallelStatus` | `formatParallelStatus`：绿色 done(耗时) / 黄色 thinking / 灰色 waiting |
| `OnRoundComplete` | 绿色 CONVERGED（提示提前结束节省 token）/ 红色 NOT CONVERGED |
| `OnConvergenceJudgment` | 灰色逐行显示收敛判断推理过程 |
| `OnContextGathered` | 模块列表（core=红点, moderate=黄点, peripheral=绿点）+ 关联 PR（最多 5 条）+ Markdown 摘要 |
| `FinalConclusion` | 绿色双线框 + Markdown 渲染 |
| `IssuesTable` | 按严重度着色的问题列表 + 位置 + 提出者 + 修复建议（截断到 100 字符） |
| `TokenUsage` | 每个审查者的 input/output token 数 + 总计 + 预估费用 + 收敛轮次 |
| `formatNumber` | 千分位格式化：1234 → "1,234"，1234567 → "1,234,567" |
