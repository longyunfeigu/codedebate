<h1 align="center">CodeDebate</h1>

<p align="center">
  <strong>单个 AI 审不出的 bug，让多个 AI 吵架吵出来</strong>
</p>

<p align="center">
  多模型对抗式代码审查工具 — 让 Claude、GPT、Gemini、Codex 组成审查委员会，通过结构化辩论交叉验证你的每一行代码。
</p>

<p align="center">
  <a href="#快速开始">快速开始</a> ·
  <a href="#它是怎么工作的">工作原理</a> ·
  <a href="#核心特性">核心特性</a> ·
  <a href="docs/configuration.md">配置</a> ·
  <a href="docs/cli-reference.md">CLI 参考</a>
</p>

---

<p align="center">
  <img src="demo/codedebate-demo.gif" alt="CodeDebate Demo" width="800">
</p>

## 为什么需要 CodeDebate？

你可能已经在用 AI 做代码审查了。但一个模型再强，也有盲区：

| | 单模型审查 | CodeDebate 多模型辩论 |
|---|---|---|
| **视角** | 单一视角，容易遗漏 | 多视角交叉验证，互相查漏补缺 |
| **偏见** | 模型固有偏好影响判断 | 不同模型互相挑战，减少偏见 |
| **深度** | 一次性输出，缺乏反思 | 多轮辩论，逐步深入 |
| **可信度** | 无法验证 AI 的判断 | 共识 = 高可信度，分歧 = 值得关注 |

**核心理念：** 就像代码审查需要多人 review 一样，AI 审查也需要"多脑碰撞"。当 Claude 和 GPT 在某个问题上达成一致，你可以更放心地相信它；当它们产生分歧，那个分歧点往往就是最值得你关注的地方。

## 它是怎么工作的？

```
                    你的 PR / MR / 本地代码变更
                              │
                    ┌─────────┴─────────┐
                    │   CodeDebate 编排器     │
                    └─────────┬─────────┘
                              │
          ┌───────────────────┼───────────────────┐
          │                   │                   │
   ┌──────┴──────┐    ┌──────┴──────┐    ┌──────┴──────┐
   │  Claude      │    │  GPT-4o     │    │  Codex      │
   │  "这里有竞态" │    │ "同意，还有注入" │    │ "性能也有问题" │
   └──────┬──────┘    └──────┬──────┘    └──────┬──────┘
          │                   │                   │
          └───────── 交叉审查 + 辩论 ────────────┘
                              │
                    ┌─────────┴─────────┐
                    │  收敛 → 综合结论    │
                    │  发布评论到 PR/MR   │
                    └───────────────────┘
```

1. **独立审查** — 每个 AI 模型独立分析代码变更，互不影响
2. **交叉辩论** — 第 2 轮起，审查者阅读彼此的反馈，挑战弱点、补充遗漏
3. **收敛检测** — 自动判断是否达成共识，达成则提前结束以节省 token
4. **综合输出** — 提取结构化问题，自动发布评论到 GitHub PR / GitLab MR

## 快速开始

### 安装

```bash
# 需要 Go 1.24+
git clone https://github.com/longyunfeigu/codedebate.git
cd codedebate && go install .
```

> 详细的依赖安装请参考 [安装指南](docs/installation.md)

### 初始化配置

```bash
codedebate init   # 生成 ~/.codedebate/config.yaml
```

### 一条命令开始审查

```bash
# 审查 GitHub PR
codedebate review 42

# 审查 GitLab MR（自动检测平台）
codedebate review https://gitlab.com/group/project/-/merge_requests/42

# 审查本地未提交变更
codedebate review --local

# 指定审查者 + 限制辩论轮数
codedebate review 42 --reviewers claude,gpt4o --rounds 2
```

## 核心特性

### 多模型并行审查

支持多种 AI 提供者同时审查，每个审查者使用独立 prompt，从不同角度切入：

| Provider | 类型 | 说明 |
|----------|------|------|
| `claude-code` | CLI | Claude Code CLI（使用订阅，无需 API Key） |
| `codex-cli` | CLI | OpenAI Codex CLI（使用订阅，无需 API Key） |
| `gemini-cli` | CLI | Gemini CLI（Google 账号登录，无需 API Key） |
| `gpt-*` / `o*` | API | OpenAI API — GPT-4o、o3、o4-mini 等（需要 API Key） |
| 自定义 provider | API | 任意 OpenAI 兼容 API — OpenRouter、MiniMax、DeepSeek 等 |
| `mock` | 调试 | 模拟提供者，用于测试（无需 API Key） |

> CLI 类型的提供者通过订阅使用，零 API 成本。API 类型通过 `providers` 配置块设置 Key 和 Base URL。

### 结构化辩论机制

不是简单地合并多个结果，而是让模型之间真正"对话"：
- 第 1 轮：独立分析，各抒己见
- 第 2+ 轮：交叉审查，质疑和补充对方观点
- 自动收敛：达成共识后提前结束，节省 token

### 多平台原生支持

| 平台 | 支持方式 | 评论发布 |
|------|---------|---------|
| GitHub | PR 编号 / URL | 行内评论 + 三级降级 |
| GitLab | MR 编号 / URL | Discussions API + Draft Notes |
| 本地 | `--local` / `--branch` | 终端输出 + 文件导出 |

自动从 `git remote` 检测平台类型，支持自托管 GitLab。

### 智能评论发布

审查结果不是一坨文本丢到 PR 里，而是精准定位到具体代码行：

1. **行内评论** — 问题定位到 diff 中的具体行
2. **文件级评论** — 文件在 diff 中但行号不精确时降级
3. **全局评论** — 问题涉及 diff 之外的文件时兜底

自动去重，不会重复发布相同评论。

### GitLab Webhook 自动化

部署为服务，MR 创建/更新时自动触发审查：

```bash
codedebate serve --webhook-secret your-secret
```

支持并发控制、去重、Draft/WIP 过滤、优雅关闭。

### 上下文感知

可选开启，让审查者获得更丰富的上下文：
- 调用链分析（基于 ripgrep）
- 相关 PR/MR 历史
- 项目文档收集

## 更多文档

| 文档 | 内容 |
|------|------|
| [安装指南](docs/installation.md) | 完整的安装步骤和依赖说明 |
| [配置说明](docs/configuration.md) | 配置文件详解、环境变量、AI 提供者设置 |
| [CLI 参考](docs/cli-reference.md) | 所有命令和参数的完整参考 |
| [设计决策](docs/design.md) | 架构设计、辩论流程、降级策略等技术细节 |

## 许可证

MIT
