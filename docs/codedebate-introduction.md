# CodeDebate - 多模型对抗式代码审查工具

## 1. 背景信息

### 1.1 AI 辅助代码审查的兴起

随着大语言模型（LLM）能力的快速提升，越来越多的团队开始尝试用 AI 来辅助代码审查。GitHub Copilot、CodeRabbit 等产品已经开始提供基于单一 AI 模型的自动化 Code Review 服务。然而，单一模型的审查存在固有缺陷——正如人类代码审查中"多人审查优于单人审查"一样，AI 审查同样需要多视角、多角度的交叉验证。

### 1.2 CodeDebate 是什么

CodeDebate 是一个用 Go 编写的 CLI 工具，核心理念是：**利用多个不同的 AI 模型独立审查代码变更，然后通过结构化的辩论机制产出高质量的综合审查结果。**

它的名字来源于希腊神话中的九头蛇（CodeDebate）——多个头脑从不同角度审视同一个问题，比单个头脑更能发现隐藏的缺陷。

CodeDebate 支持的 AI 提供者包括：

| 提供者 | 类型 | 说明 |
|--------|------|------|
| **Claude Code** | CLI | 通过 `claude` CLI 调用，支持会话复用 |
| **Codex CLI** | CLI | 通过 `codex` CLI 调用，支持会话复用 |
| **OpenAI API** | HTTP API | 直接调用 OpenAI API（gpt-4o、o1、o3 等），支持 SSE 流式，兼容 Azure OpenAI、Ollama 等 |

CodeDebate 同时支持 **GitHub PR** 和 **GitLab MR** 两大主流代码托管平台，并能自动从 `git remote` URL 检测平台类型。

---

## 2. 当前 Code Review 的痛点

### 2.1 人工审查的痛点

| 痛点 | 说明 |
|------|------|
| **审查者精力有限** | 大型 PR 经常得到敷衍的"LGTM"，真正的深度审查难以持续 |
| **审查瓶颈** | 关键审查者成为团队的瓶颈，PR 等待审查时间长 |
| **知识盲区** | 单个审查者的知识面有限，安全、性能、架构等多维度难以兼顾 |
| **一致性差** | 不同审查者标准不一，审查质量波动大 |

### 2.2 现有 AI 审查工具的痛点

| 痛点 | 说明 |
|------|------|
| **单模型偏见** | 单一 AI 模型有固有的知识偏差和盲区，容易遗漏特定类型的问题 |
| **幻觉问题** | AI 可能"一本正经地胡说八道"，误报率高，导致开发者对审查结果失去信任 |
| **缺乏交叉验证** | 单一模型的结论无法被验证，开发者无法判断哪些建议是可靠的 |
| **上下文不足** | 大多数工具只看 diff 本身，不了解代码的调用链、历史演进和架构上下文 |
| **集成门槛高** | 很多 AI 审查工具需要复杂的 SaaS 集成，对代码隐私有顾虑的团队难以采用 |
| **输出非结构化** | AI 的审查意见通常是纯文本，难以自动化处理、追踪和统计 |

### 2.3 核心矛盾

> **单一 AI 模型的审查≈一个人的审查**——它可能很快，但缺乏多角度的交叉验证，既无法有效过滤误报，也无法发现单一视角的盲区。

---

## 3. CodeDebate 如何解决这些痛点

### 3.1 核心思路：对抗式辩论

CodeDebate 的核心创新在于**多模型对抗式辩论（Adversarial Debate）**：

- 不同的 AI 模型从**不同角度**独立审查代码
- 然后进入**交叉审查**阶段，互相质疑、挑战、补充对方的观点
- 通过多轮辩论，**被多个模型共同确认的问题**更加可信，**仅被单一模型提出的问题**会被标注
- 最终由**总结器（Summarizer）**提炼出高质量的结论

这个机制类似于学术界的同行评审（Peer Review）——多个独立的评审者交叉验证，共识越强的结论可信度越高。

### 3.2 核心架构

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
     └────────┬────────┘ └───────┬───────┘ └────────┬────────┘
              │                   │                   │
              └───────────────────┼───────────────────┘
                                  │
                    ┌─────────────┴─────────────┐
                    │    Provider 抽象层         │
                    │  Claude Code / Codex / OpenAI │
                    └─────────────┬─────────────┘
                                  │
                    ┌─────────────┴─────────────┐
                    │    Platform 平台抽象层      │
                    │  GitHub (gh) / GitLab (glab) │
                    └───────────────────────────┘
```

CodeDebate 定义了**三个 AI 角色**，各司其职：

| 角色 | 职责 | 执行时机 |
|------|------|----------|
| **Analyzer（分析器）** | 预分析 diff，提取变更摘要和建议关注点 | 在辩论开始前，与上下文收集并行 |
| **Reviewers（审查者）** | 执行实际的代码审查和辩论 | 多轮辩论，第 2 轮起能看到其他审查者的反馈 |
| **Summarizer（总结者）** | 收敛判断、最终结论生成、结构化问题提取 | 每轮后（收敛检测）+ 辩论结束后 |

### 3.3 完整审查流程（核心流程详解）

CodeDebate 的完整审查流程分为 **5 个阶段**：

#### 阶段 0：输入与预处理

```
用户输入 (PR 编号/URL/本地变更/分支)
    │
    ├── 平台自动检测 (GitHub/GitLab)
    ├── 获取 PR/MR 的 diff、标题、描述
    ├── Diff 行号标注 (为行内评论做准备)
    └── 加载配置，创建 Provider 实例
```

#### 阶段 1：并行预处理（上下文收集 + 预分析）

```
┌──────────────────────────────┐
│  errgroup 并行执行            │
│                              │
│  ┌────────────────────────┐  │
│  │ Context Gatherer       │  │  ← 收集调用链 (ripgrep)
│  │ (上下文收集器)          │  │    收集相关 PR/MR 历史
│  │                        │  │    收集相关文档
│  └────────────────────────┘  │
│                              │
│  ┌────────────────────────┐  │
│  │ Analyzer (预分析器)     │  │  ← 分析 diff，提取关注重点
│  │ 输出: 变更摘要 + 关注点 │  │    供 Reviewer 参考但不锚定
│  └────────────────────────┘  │
│                              │
└──────────────────────────────┘
```

上下文收集器会从代码仓库中提取：
- **调用链引用**：从 diff 中提取修改的符号（函数名、类名等），用 ripgrep 查找它们在仓库中被引用的位置
- **历史 PR/MR**：查找与变更文件相关的历史 PR/MR，提供演进背景
- **相关文档**：查找与变更相关的 README、设计文档等

#### 阶段 2：多轮辩论

这是 CodeDebate 的核心阶段：

```
第 1 轮: 独立审查 (所有审查者并行)
  ├── Reviewer A (如 Claude): 独立分析代码变更
  ├── Reviewer B (如 Codex): 独立分析代码变更
  └── Reviewer C (如 GPT-4o): 独立分析代码变更
         ↓
  收敛检查: Summarizer 判断是否达成共识
    ├── CONVERGED → 直接进入总结阶段 (节省 token)
    └── NOT_CONVERGED → 继续
         ↓
第 2 轮: 交叉审查 (所有审查者并行)
  ├── Reviewer A: 阅读 B 和 C 的反馈，补充遗漏，挑战弱点
  ├── Reviewer B: 阅读 A 和 C 的反馈，补充遗漏，挑战弱点
  └── Reviewer C: 阅读 A 和 B 的反馈，补充遗漏，挑战弱点
         ↓
  收敛检查 → 继续或结束
         ↓
第 N 轮: 继续交叉审查... (直到收敛或达到 MaxRounds)
```

**关键设计要点：**

1. **消息快照机制**：每轮开始时，为所有审查者构建消息的快照。这确保同一轮中所有审查者看到的信息完全一致，避免先执行的审查者影响后执行的审查者。

2. **会话复用**：对于 CLI 提供者（Claude Code、Codex），使用会话 ID 维持上下文，第 2 轮起只需发送增量消息（其他审查者的新反馈），大幅节省 token。

3. **严格的收敛检测**：Summarizer 需要判断"所有审查者是否就最终结论达成一致"，关键问题必须被所有人确认，不能有被忽略的分歧。收敛后提前结束辩论，避免无意义的 token 消耗。

#### 阶段 3：总结与问题提取

```
辩论结束后:
  │
  ├── 各审查者提交匿名要点总结 (并行)
  │
  ├── Summarizer 生成最终结论 ──┐
  │   (综合所有审查者观点)      │ 并行执行
  │                            │
  └── Structurizer 提取        ┘
      结构化 JSON 问题列表
      ├── 每个问题包含: severity, category, file, line, description, suggestedFix
      ├── Jaccard 相似度去重 (避免重复问题)
      └── JSON Schema 校验 (最多重试 3 次)
```

#### 阶段 4：输出与评论发布

```
审查结果:
  │
  ├── 终端彩色展示 (Markdown 渲染 + 问题表格)
  │
  ├── PR/MR 评论发布 (三级降级策略):
  │   ├── 行内评论 (Inline): 行号在 diff 有效范围内 → 直接发到对应行
  │   ├── 文件级评论 (File-level): 文件在 diff 中但行号无效 → 发到文件级
  │   └── 全局评论 (Global): 文件不在 diff 中 → 作为全局评论
  │
  ├── 总结评论 (Summary Note): 发布/更新最终结论到 PR/MR
  │
  └── 文件导出: Markdown 或 JSON 格式
```

### 3.4 痛点对应的解决方案

| 痛点 | CodeDebate 的解决方案 |
|------|-----------------|
| 单模型偏见 | 多个不同 AI 模型并行审查，交叉验证 |
| 幻觉/误报 | 辩论机制：一个模型的"幻觉"会被其他模型质疑和纠正 |
| 缺乏交叉验证 | 第 2 轮起审查者互相挑战、补充，被多人确认的问题更可信 |
| 上下文不足 | Context Gatherer 自动收集调用链、PR 历史、文档，checkout 完整代码仓库 |
| 集成门槛高 | 纯 CLI 工具，无需 SaaS，通过 gh/glab CLI 与平台交互 |
| 输出非结构化 | 提取 JSON Schema 校验的结构化问题，自动发布行内评论 |
| 审查者精力有限 | AI 不疲倦，可以对每个 PR 进行深度审查 |
| 审查瓶颈 | Webhook 服务模式，MR 创建/更新时自动触发，无需人工介入 |
| 一致性差 | 可配置的 System Prompt 定义审查标准，每次审查行为一致 |

---

## 4. 如何使用

### 4.1 安装

#### 4.1.1 编译 CodeDebate

```bash
# 需要 Go 1.24+
go version

# 克隆仓库
git clone https://github.com/longyunfeigu/codedebate.git
cd codedebate

# 编译
go build -o codedebate .

# 或直接安装到 $GOPATH/bin
go install .
```

#### 4.1.2 安装平台 CLI

根据你使用的代码托管平台，安装对应的 CLI 工具：

**GitHub CLI** (`gh`) — 审查 GitHub PR 时必需：

```bash
# macOS
brew install gh

# Debian/Ubuntu
sudo apt install gh

# 认证
gh auth login
```

**GitLab CLI** (`glab`) — 审查 GitLab MR 时必需：

```bash
# macOS
brew install glab

# 通过 Go 安装
go install gitlab.com/gitlab-org/cli/cmd/glab@latest

# 认证（二选一）
glab auth login                          # 交互式
export GITLAB_TOKEN=your-access-token    # 环境变量

# 自托管 GitLab
glab auth login --hostname gitlab.company.com
```

#### 4.1.3 安装 AI 提供者（至少安装一个）

**Claude Code CLI** (`claude`):

```bash
npm install -g @anthropic-ai/claude-code
claude --version
```

**Codex CLI** (`codex`):

```bash
npm install -g @openai/codex
codex --version
```

**OpenAI API**（无需安装 CLI）:

```bash
export OPENAI_API_KEY=sk-your-key
```

#### 4.1.4 可选工具

**ripgrep** (`rg`) — 上下文收集中的调用链分析：

```bash
# macOS
brew install ripgrep

# Debian/Ubuntu
sudo apt install ripgrep
```

#### 4.1.5 依赖总结

| 工具 | 命令 | 何时需要 |
|------|------|----------|
| Go 1.24+ | `go` | 编译 CodeDebate |
| GitHub CLI | `gh` | 审查 GitHub PR |
| GitLab CLI | `glab` | 审查 GitLab MR |
| Claude Code | `claude` | 使用 claude-code 提供者 |
| Codex CLI | `codex` | 使用 codex-cli 提供者 |
| OpenAI API Key | — | 使用 openai 提供者 |
| ripgrep | `rg` | 上下文收集（可选） |

### 4.2 初始化配置

```bash
codedebate init
```

这会在 `~/.codedebate/config.yaml` 生成默认配置文件。

### 4.3 配置文件详解

配置文件 `~/.codedebate/config.yaml` 的完整结构如下：

```yaml
# ====== 平台配置（可选）======
platform:
  type: auto           # auto | github | gitlab（默认 auto，自动检测）
  host: ""             # 自托管 GitLab 地址，如 gitlab.company.com

# ====== AI 提供者配置 ======
providers:
  claude-code:
    enabled: true
  codex-cli:
    enabled: true
  openai:
    api_key: ${OPENAI_API_KEY}    # 支持 ${ENV_VAR} 语法引用环境变量
    base_url: ""                   # 可选，自定义端点（Azure OpenAI、Ollama 等）

# ====== 默认设置 ======
defaults:
  max_rounds: 3              # 最大辩论轮数
  output_format: markdown    # 输出格式
  check_convergence: true    # 启用收敛检测
  language: ""               # 输出语言（"zh"、"ja"、"en"），空为英文

# ====== 分析器配置 ======
# 预分析变更，提取关注重点供审查者参考
analyzer:
  model: claude-code
  prompt: |
    You are a senior code analyst. Analyze the code changes and identify:
    1. Key areas that need careful review
    2. Potential risks or concerns
    3. Architecture implications

# ====== 总结者配置 ======
# 重要：summarizer 必须使用 API 模型（如 gpt-4o），不能用 CLI 模型！
# 原因：structurizeIssues 阶段需要严格的 JSON 输出，CLI 模型会附加额外文本导致解析失败
summarizer:
  model: gpt-4o
  prompt: |
    You are a senior engineering lead. Synthesize multiple reviewers'
    feedback into a coherent conclusion.

# ====== 审查者配置 ======
# 可以配置多个审查者，每个使用不同的 AI 模型和 prompt
reviewers:
  claude:
    model: claude-code
    prompt: |
      You are a thorough code reviewer focused on correctness,
      security vulnerabilities, and edge cases.
  codex:
    model: codex-cli
    prompt: |
      You are a pragmatic code reviewer focused on maintainability,
      performance, and best practices.
  gpt4o:
    model: gpt-4o
    model_name: gpt-4o              # 底层模型名称
    reasoning_effort: medium         # 推理深度（仅推理模型有效）
    prompt: |
      You are an experienced code reviewer focused on architecture,
      design patterns, and code organization.

# ====== 上下文收集器配置（可选）======
contextGatherer:
  enabled: true
  model: claude-code               # 用于上下文分析的 AI 模型
  history:
    maxDays: 30                    # 查找历史 PR/MR 的天数范围
    maxPRs: 10                     # 最大相关 PR/MR 数
  docs:
    patterns:                      # 文档搜索模式
      - "docs"
      - "README.md"
    maxSize: 50000                 # 单文件最大大小（字节）

# ====== 本地 Checkout 配置（可选）======
checkout:
  enabled: true                    # 启用后审查者可以浏览完整代码仓库
  baseDir: ~/.codedebate/repos          # 镜像缓存目录
  ttl: 24h                         # 缓存过期时间
```

**配置要点提醒：**

1. **Summarizer 必须用 API 模型**（如 gpt-4o），不能用 CLI 模型（claude-code、codex-cli），否则结构化问题提取会失败
2. 配置中的 `${ENV_VAR}` 会在加载时自动替换为环境变量的值
3. `diff_exclude` 可以过滤不需要审查的文件（如生成代码、vendor 等）

### 4.4 使用方式

#### 4.4.1 审查 GitHub PR

```bash
# 在 GitHub 项目目录中，通过 PR 编号审查
cd /path/to/your-github-repo
codedebate review 42

# 通过 PR URL 审查（可在任意目录执行）
codedebate review https://github.com/owner/repo/pull/42

# 指定审查者和轮数
codedebate review 42 --reviewers claude,codex --rounds 2

# 使用所有配置的审查者
codedebate review 42 --all

# 审查但不发布评论到 PR
codedebate review 42 --no-post

# 保存审查结果到文件
codedebate review 42 -o review-result.md
codedebate review 42 -f json -o result.json
```

#### 4.4.2 审查 GitLab MR

CodeDebate 会从 `git remote` URL 自动检测平台类型。

```bash
# 在 GitLab 项目目录中，通过 MR 编号审查
cd /path/to/your-gitlab-repo
codedebate review 42

# 通过 MR URL 审查
codedebate review https://gitlab.com/group/project/-/merge_requests/42
```

**自托管 GitLab** 需要在配置中指定平台信息：

```yaml
platform:
  type: gitlab
  host: gitlab.company.com
```

#### 4.4.3 审查本地变更

无需平台 CLI，直接审查本地 git 变更：

```bash
# 审查未提交的本地变更（无未提交变更时自动审查最近一次 commit）
codedebate review --local

# 审查当前分支 vs main 的变更
codedebate review --branch main

# 审查指定文件
codedebate review --files main.go,cmd/review.go
```

#### 4.4.4 启动 GitLab Webhook 服务

部署为长期运行的服务，MR 创建/更新时自动触发审查：

```bash
# 启动 webhook 服务器
codedebate serve --webhook-secret your-secret

# 自定义监听地址和并发数
codedebate serve --addr :9090 --max-concurrent 5 --webhook-secret your-secret

# 自托管 GitLab
codedebate serve --gitlab-host gitlab.company.com --webhook-secret your-secret

# 通过环境变量配置
export CODEDEBATE_WEBHOOK_SECRET=your-secret
export CODEDEBATE_ADDR=:8080
export GITLAB_HOST=gitlab.company.com
codedebate serve
```

在 GitLab 项目 **Settings > Webhooks** 中配置：
- **URL**: `http://your-server:8080/webhook/gitlab`
- **Secret Token**: 与 `--webhook-secret` 一致
- **Trigger**: 勾选 **Merge request events**

服务特性：
- 收到 MR 事件后立即返回 202 Accepted，异步执行审查
- 自动过滤：仅处理 open/reopen/update 动作，跳过 Draft/WIP MR
- 并发控制：通过信号量限制同时进行的审查数
- 去重：同一 MR 不会重复触发审查
- 单次审查超时 10 分钟，服务优雅关闭超时 30 秒

#### 4.4.5 Docker 部署（Webhook 服务）

```bash
# 构建镜像
docker build -t codedebate .

# 运行
docker run -d \
  -p 8080:8080 \
  -v ~/.codedebate:/root/.codedebate \
  -e CODEDEBATE_WEBHOOK_SECRET=your-secret \
  -e GITLAB_TOKEN=your-token \
  -e OPENAI_API_KEY=sk-your-key \
  codedebate
```

### 4.5 CLI 完整参数参考

#### codedebate review

```
codedebate review [pr-number-or-url] [flags]

Flags:
  -c, --config string      配置文件路径（默认 ~/.codedebate/config.yaml）
  -r, --rounds int         最大辩论轮数（覆盖配置）
  -o, --output string      输出保存到文件
  -f, --format string      输出格式：markdown | json（默认 "markdown"）
      --no-converge        禁用收敛检测
      --show-tool-trace    显示 analyzer/reviewer 完整过程输出（默认摘要模式）
  -v, --verbose            --show-tool-trace 的别名
  -l, --local              审查本地未提交变更
  -b, --branch string      审查当前分支 vs 指定基准分支
      --files strings      审查指定文件
      --reviewers string   逗号分隔的审查者 ID
  -a, --all                使用所有审查者
      --skip-context       跳过上下文收集
      --no-post            跳过平台评论发布
      --no-post-summary    跳过发布审查总结
```

#### codedebate serve

```
codedebate serve [flags]

Flags:
  -c, --config string          配置文件路径（默认 ~/.codedebate/config.yaml）
      --addr string            监听地址（默认 ":8080"）
      --webhook-secret string  GitLab Webhook Secret（必填）
      --max-concurrent int     最大并发审查数（默认 3）
      --gitlab-host string     GitLab 主机地址（默认 "gitlab.com"）

端点:
  POST /webhook/gitlab    接收 GitLab MR Webhook 事件
  GET  /health            健康检查
```

### 4.6 调试与排错

```bash
# 启用调试日志
CODEDEBATE_LOG_LEVEL=debug codedebate review 42

# 查看完整的 AI 交互过程
codedebate review 42 --show-tool-trace

# 跳过上下文收集（加快速度）
codedebate review 42 --skip-context

# 仅生成审查结果，不发布到 PR/MR
codedebate review 42 --no-post
```

支持的日志级别：`debug`、`info`（默认）、`warn`、`error`

### 4.7 典型使用场景

#### 场景 1：日常 PR 审查

```bash
# 快速审查，使用默认审查者
codedebate review 42
```

#### 场景 2：重要变更的深度审查

```bash
# 使用所有审查者，增加辩论轮数
codedebate review 42 --all --rounds 4
```

#### 场景 3：提交前自查

```bash
# 审查本地未提交的变更
codedebate review --local

# 审查整个功能分支
codedebate review --branch main
```

#### 场景 4：CI/CD 集成

```bash
# 在 CI pipeline 中自动审查
codedebate review $MR_IID --no-post-summary -o review.md
```

#### 场景 5：GitLab 自动化审查服务

```bash
# 部署 Webhook 服务，MR 创建/更新时自动审查
codedebate serve --webhook-secret $WEBHOOK_SECRET
```
