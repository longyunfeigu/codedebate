package provider

import (
	"fmt"
	"os"

	"github.com/longyunfeigu/codedebate/internal/util"
)

// DefaultPromptSizeThreshold 是默认的大 prompt 写临时文件阈值（100KB）。
const DefaultPromptSizeThreshold = 100 * 1024

// PreparedPrompt 包含处理后的 prompt 和清理函数。
type PreparedPrompt struct {
	Prompt  string // 实际发送给 CLI 的 prompt（可能包含文件读取指令）
	Cleanup func() // 清理函数，用于删除临时文件
}

// PreparePromptForCli 检查 prompt 大小，如果超过阈值则写入临时文件。
// threshold <= 0 时使用 DefaultPromptSizeThreshold。
// 返回的 PreparedPrompt.Prompt 可能是原始内容，也可能是指引 CLI 读取临时文件的指令。
// 调用者必须 defer PreparedPrompt.Cleanup() 以确保临时文件被删除。
func PreparePromptForCli(prompt string, threshold int) PreparedPrompt {
	if threshold <= 0 {
		threshold = DefaultPromptSizeThreshold
	}
	if len(prompt) <= threshold {
		return PreparedPrompt{Prompt: prompt, Cleanup: func() {}}
	}

	tmpFile, err := os.CreateTemp("", "codedebate-prompt-*.txt")
	if err != nil {
		util.Warnf("Failed to create temp file for large prompt, falling back to stdin: %v", err)
		return PreparedPrompt{Prompt: prompt, Cleanup: func() {}}
	}

	if _, err := tmpFile.WriteString(prompt); err != nil {
		tmpFile.Close()
		os.Remove(tmpFile.Name())
		util.Warnf("Failed to write prompt to temp file, falling back to stdin: %v", err)
		return PreparedPrompt{Prompt: prompt, Cleanup: func() {}}
	}
	tmpFile.Close()

	// 返回指令让 CLI 先读取临时文件内容
	redirectedPrompt := fmt.Sprintf(
		"Read the file %s for the full review context and instructions. "+
			"The file contains the complete diff and review prompt. "+
			"Process its contents as if they were provided directly.",
		tmpFile.Name(),
	)

	return PreparedPrompt{
		Prompt: redirectedPrompt,
		Cleanup: func() {
			os.Remove(tmpFile.Name())
		},
	}
}
