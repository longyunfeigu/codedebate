package cmd

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/fatih/color"
	"github.com/spf13/cobra"
)

// initCmd 定义了 "init" 子命令，用于在用户的 home 目录下生成 CodeDebate 的默认配置文件。
// 这是用户开始使用 CodeDebate 的第一步，生成的配置文件包含了所有可配置项的示例和说明。
var initCmd = &cobra.Command{
	Use:   "init",
	Short: "Create a default configuration file",
	RunE:  runInit,
}

// defaultConfig 是 CodeDebate 的默认配置文件模板。
// 配置内容包括：
//   - providers: AI 提供者配置（claude-code、codex-cli、openai）
//   - defaults: 全局默认参数（最大辩论轮数、输出格式、收敛检测开关）
//   - analyzer: 预分析器的模型和提示词配置，负责在审查前分析代码变更的概况
//   - summarizer: 总结器的模型和提示词配置，负责在辩论后综合各审查者的意见
//   - reviewers: 审查者的定义，每个审查者有独立的模型和审查视角（如安全性、性能等）
//   - contextGatherer: 上下文收集器配置（默认注释掉），可自动收集调用链、历史变更和文档
const defaultConfig = `# CodeDebate Configuration
# Multi-model adversarial code review

providers:
  claude-code:
    enabled: true
  codex-cli:
    enabled: true
  openai:
    api_key: ${OPENAI_API_KEY}
    base_url: https://api.openai.com/v1  # optional, for Azure/Ollama compatibility
  # gemini-cli:
  #   enabled: true
  # openrouter:
  #   api_key: ${OPENROUTER_API_KEY}
  #   base_url: https://openrouter.ai/api/v1
  # minimax:
  #   api_key: ${MINIMAX_API_KEY}
  #   base_url: https://api.minimax.io/v1
  # glm:
  #   api_key: ${GLM_API_KEY}
  #   base_url: https://open.bigmodel.cn/api/paas/v4

defaults:
  max_rounds: 5
  output_format: markdown
  check_convergence: true
  structurize_mode: legacy  # legacy | ledger
  # language: zh  # Output language (e.g. zh, ja, en). Empty = English
  # diff_exclude:  # Additional patterns to exclude from diff (e.g. ["*.min.js", "dist/**"])
  # prompt_size_threshold: 102400  # Bytes. Prompts larger than this are written to a temp file (default 100KB)

analyzer:
  model: claude-code
  prompt: |
    You are a senior code analyst. Analyze the PR diff and provide:
    1. A summary of what the changes do
    2. Key areas of concern
    3. Potential impact on the codebase

    ## Suggested Review Focus
    - List specific areas reviewers should focus on

summarizer:
  # Summarizer must use OpenAI API models (gpt-*/o1-*/o3-*)
  model: gpt-4o
  prompt: |
    You are a senior engineering lead synthesizing code review feedback.
    Provide balanced, actionable conclusions.

reviewers:
  claude:
    model: claude-code
    prompt: |
      You are a thorough code reviewer focused on correctness, security,
      and maintainability. Review every changed file systematically.
  codex:
    model: codex-cli
    prompt: |
      You are a pragmatic code reviewer focused on performance, edge cases,
      and real-world reliability. Challenge assumptions and find issues others miss.
  # openai:
  #   model: gpt-4o
  #   prompt: |
  #     You are an experienced code reviewer focused on design patterns,
  #     API correctness, and code clarity. Provide actionable suggestions.
  # gemini:
  #   model: gemini-cli
  #   prompt: |
  #     You are a meticulous code reviewer focused on correctness and best practices.
  # openrouter-review:
  #   model: anthropic/claude-sonnet-4
  #   provider: openrouter
  #   prompt: |
  #     You are a code reviewer focused on architecture and design quality.

# Platform: auto-detected from git remote (github or gitlab)
# platform:
#   type: auto
#   host: gitlab.example.com  # for self-hosted GitLab

# Optional: context gathering
# contextGatherer:
#   enabled: true
#   callChain:
#     maxDepth: 2
#     maxFilesToAnalyze: 20
#   history:
#     maxDays: 30
#     maxPRs: 10
#   docs:
#     patterns: ["docs", "README.md", "ARCHITECTURE.md"]
#     maxSize: 50000
`

// runInit 执行 init 命令的核心逻辑：在 ~/.codedebate/ 目录下创建默认配置文件。
// 如果配置文件已存在，会提示用户确认是否覆盖，防止意外丢失自定义配置。
// 配置文件路径固定为 ~/.codedebate/config.yaml，这也是 CodeDebate 默认查找配置的位置。
func runInit(cmd *cobra.Command, args []string) error {
	home, err := os.UserHomeDir()
	if err != nil {
		return fmt.Errorf("failed to get home directory: %w", err)
	}

	configDir := filepath.Join(home, ".codedebate")
	configPath := filepath.Join(configDir, "config.yaml")

	// 检查配置文件是否已存在，避免用户意外覆盖已有的自定义配置
	if _, err := os.Stat(configPath); err == nil {
		color.Yellow("Config file already exists: %s", configPath)
		fmt.Print("Overwrite? (y/N): ")
		var answer string
		fmt.Scanln(&answer)
		if answer != "y" && answer != "Y" {
			fmt.Println("Aborted.")
			return nil
		}
	}

	// 确保配置目录存在，如果不存在则递归创建
	if err := os.MkdirAll(configDir, 0755); err != nil {
		return fmt.Errorf("failed to create config directory: %w", err)
	}

	// 将默认配置写入文件
	if err := os.WriteFile(configPath, []byte(defaultConfig), 0644); err != nil {
		return fmt.Errorf("failed to write config file: %w", err)
	}

	color.Green("Config file created: %s", configPath)
	fmt.Println("Edit this file to configure your reviewers and models.")
	return nil
}
