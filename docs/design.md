# 设计决策

## 架构概览

```
                        ┌────────────────────┐
                        │    codedebate review     │  CLI 入口
                        │    codedebate serve      │  Webhook 服务
                        └─────────┬──────────┘
                                  │
                    ┌─────────────┴─────────────┐
                    │     Orchestrator 编排器     │  核心调度
                    └─────────────┬─────────────┘
                                  │
              ┌───────────────────┼───────────────────┐
              │                   │                   │
     ┌────────┴────────┐ ┌───────┴───────┐ ┌────────┴────────┐
     │   Analyzer 分析  │ │ Reviewers 审查 │ │ Summarizer 总结 │
     │   预分析变更内容  │ │ 多模型并行审查  │ │ 收敛判断+最终结论 │
     └────────┬────────┘ └───────┬───────┘ └────────┬────────┘
              │                   │                   │
              └───────────────────┼───────────────────┘
                                  │
                    ┌─────────────┴─────────────┐
                    │    Provider 抽象层         │
                    │  AIProvider / Session 接口  │
                    └─────────────┬─────────────┘
                                  │
              ┌───────────────────┼───────────────────┐
              │                   │                   │
        ┌─────┴─────┐     ┌─────┴─────┐      ┌──────┴──────┐
        │Claude Code│     │ Codex CLI │      │OpenAI / Gem │
        │  Provider │     │ Provider  │      │ ini API     │
        └───────────┘     └───────────┘      └─────────────┘
                                  │
                    ┌─────────────┴─────────────┐
                    │    Platform 平台抽象层      │
                    │  GitHub / GitLab 统一接口   │
                    └─────────────┬─────────────┘
                                  │
                         ┌────────┼────────┐
                         │                 │
                   ┌─────┴─────┐   ┌──────┴──────┐
                   │  GitHub   │   │   GitLab    │
                   │  (gh CLI) │   │ (glab CLI)  │
                   └───────────┘   └─────────────┘
```

## 辩论流程

```
第 1 轮: 独立审查
  ├─ Reviewer A: 独立分析代码变更
  └─ Reviewer B: 独立分析代码变更
         ↓
第 2 轮: 交叉审查
  ├─ Reviewer A: 阅读 B 的反馈，补充遗漏，挑战弱点
  └─ Reviewer B: 阅读 A 的反馈，补充遗漏，挑战弱点
         ↓
  收敛检查: Summarizer 判断是否达成共识
    ├─ CONVERGED → 提前结束，节省 token
    └─ NOT_CONVERGED → 继续下一轮
         ↓
第 N 轮: 继续交叉审查...
         ↓
总结阶段:
  ├─ 各审查者提交要点总结
  ├─ Summarizer 生成最终结论
  └─ Summarizer 提取结构化 JSON 问题（最多重试 3 次）
```

## 平台评论降级策略

当发布审查评论到 PR/MR 时，采用三级降级策略：

1. **行内评论** (Inline) - 如果行号在 diff 有效范围内，直接发到对应行
2. **文件级评论** (File-level) - 如果文件在 diff 中但行号无效，发到文件级
3. **全局评论** (Global) - 如果文件不在 diff 中，作为 PR/MR 全局评论发布

评论发布前会检查已有评论，避免重复发布。

**GitLab 特殊支持：**
- 使用 Discussions API 发布行内/文件级评论
- 尝试 Draft Notes API + bulk_publish（Premium 功能，不可用时自动降级）
- 服务端模式下通过 `glab api` 直接调用 GitLab API

## 关键设计决策

1. **无 AI SDK 依赖** - CLI 提供者通过 `os/exec` 调用命令行工具，OpenAI 提供者通过原生 `net/http` 调用 API，均不依赖第三方 SDK
2. **errgroup 并行执行** - 审查者并行运行，上下文收集与分析并行
3. **会话复用** - CLI 提供者通过会话 ID 复用连接，避免重复发送完整历史
4. **流式输出** - 实时显示审查者响应（CLI 和 OpenAI SSE 均支持）
5. **优雅降级** - 平台集成、上下文收集、收敛检测均为非致命性故障
6. **平台抽象** - 统一的 Platform 接口，GitHub 和 GitLab 实现可互换
7. **自动平台检测** - 从 git remote URL 自动识别 GitHub/GitLab，支持手动覆盖
8. **Summarizer 必须用 API 模型** - structurizeIssues 要求严格 JSON 输出，CLI 模型无法可靠生成纯 JSON

## 项目结构

```
codedebate/
├── main.go                          # 程序入口
├── go.mod                           # Go 模块定义 (Go 1.24)
├── cmd/                             # CLI 命令层
│   ├── root.go                      # Cobra 根命令注册
│   ├── review.go                    # review 子命令
│   ├── init.go                      # init 子命令
│   └── serve.go                     # serve 子命令
├── internal/                        # 内部包
│   ├── config/                      # 配置管理
│   ├── provider/                    # AI 提供者抽象层
│   ├── orchestrator/                # 辩论编排器（核心逻辑）
│   ├── platform/                    # 多平台抽象层
│   ├── server/                      # GitLab Webhook 服务器
│   ├── context/                     # 上下文收集器
│   ├── display/                     # 终端 UI 和输出格式化
│   └── util/                        # 日志工具
├── docs/                            # 文档
└── deploy/                          # Docker 部署
```

## 依赖库

| 库 | 用途 |
|---|---|
| [cobra](https://github.com/spf13/cobra) | CLI 框架 |
| [yaml.v3](https://github.com/go-yaml/yaml) | YAML 配置解析 |
| [errgroup](https://pkg.go.dev/golang.org/x/sync/errgroup) | 并发 goroutine 管理 |
| [color](https://github.com/fatih/color) | 终端彩色输出 |
| [spinner](https://github.com/briandowns/spinner) | 加载动画 |
| [glamour](https://github.com/charmbracelet/glamour) | 终端 Markdown 渲染 |
