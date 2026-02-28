// Package main 是 CodeDebate 多模型对抗式代码审查工具的入口包。
// CodeDebate 利用多个 AI 模型独立审查代码变更，通过结构化辩论机制产生全面的审查结果。
package main

import "github.com/longyunfeigu/codedebate/cmd"

// main 是程序的入口函数，委托给 cmd 包执行命令行解析和调度。
func main() {
	cmd.Execute()
}
