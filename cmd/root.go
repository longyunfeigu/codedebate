// Package cmd 定义了 CodeDebate 的所有命令行子命令及其执行逻辑。
// 使用 cobra 框架实现命令行解析，支持 review（代码审查）和 init（初始化配置）两个子命令。
package cmd

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"
)

// rootCmd 是 CodeDebate 的根命令，所有子命令都注册在它下面。
// 根命令本身不执行任何操作，仅作为命令树的根节点并显示帮助信息。
var rootCmd = &cobra.Command{
	Use:   "codedebate",
	Short: "Multi-model adversarial code review tool",
	Long: `CodeDebate uses multiple AI models to independently review code changes,
then facilitates a structured debate to produce comprehensive review results.`,
}

// Execute 是 CLI 的顶层入口函数，由 main.go 调用。
// 它启动 cobra 的命令解析流程，如果执行过程中出现错误，
// 会将错误信息输出到标准错误流并以非零状态码退出程序。
func Execute() {
	if err := rootCmd.Execute(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

// init 在包加载时自动执行，将所有子命令注册到根命令下。
// 目前注册了两个子命令：
//   - reviewCmd: 执行代码审查的核心命令
//   - initCmd: 生成默认配置文件的初始化命令
func init() {
	rootCmd.AddCommand(reviewCmd)
	rootCmd.AddCommand(initCmd)
	rootCmd.AddCommand(serveCmd)
}
