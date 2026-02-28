# 安装指南

## 1. 安装 CodeDebate

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

## 2. 安装依赖工具

根据你的使用场景，安装对应的工具。

### 平台 CLI（用于获取 PR/MR 信息和发布评论）

**GitHub CLI** (`gh`) - 审查 GitHub PR 时必需：

```bash
# macOS
brew install gh

# Debian/Ubuntu
sudo apt install gh

# 安装后认证
gh auth login
```

**GitLab CLI** (`glab`) - 审查 GitLab MR 时必需：

```bash
# macOS
brew install glab

# Debian/Ubuntu（通过官方仓库）
# 参考 https://gitlab.com/gitlab-org/cli#installation

# 通过 Go 安装
go install gitlab.com/gitlab-org/cli/cmd/glab@latest

# 安装后认证（二选一）
glab auth login                          # 交互式登录
export GITLAB_TOKEN=your-access-token    # 或通过环境变量

# 自托管 GitLab 需要指定 host
glab auth login --hostname gitlab.company.com
```

### AI 提供者（至少安装一个）

**Claude Code CLI** (`claude`) - claude-code 提供者：

```bash
npm install -g @anthropic-ai/claude-code
claude --version
```

**Codex CLI** (`codex`) - codex-cli 提供者：

```bash
npm install -g @openai/codex
codex --version
```

**OpenAI API** - openai 提供者（无需安装 CLI，需配置 API Key）：

```bash
export OPENAI_API_KEY=sk-your-key
```

### 可选工具

**ripgrep** (`rg`) - 上下文收集中的调用链分析：

```bash
# macOS
brew install ripgrep

# Debian/Ubuntu
sudo apt install ripgrep
```

## 3. 依赖总结

| 工具 | 命令 | 何时需要 | 安装方式 |
|------|------|----------|----------|
| Go 1.24+ | `go` | 编译 CodeDebate | [golang.org](https://golang.org/dl/) |
| GitHub CLI | `gh` | 审查 GitHub PR | `brew install gh` / `apt install gh` |
| GitLab CLI | `glab` | 审查 GitLab MR | `brew install glab` / `go install` |
| Claude Code | `claude` | 使用 claude-code 提供者 | `npm install -g @anthropic-ai/claude-code` |
| Codex CLI | `codex` | 使用 codex-cli 提供者 | `npm install -g @openai/codex` |
| OpenAI API Key | - | 使用 openai 提供者 | 配置 `OPENAI_API_KEY` 环境变量 |
| ripgrep | `rg` | 上下文收集（可选） | `brew install ripgrep` / `apt install ripgrep` |
