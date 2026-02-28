package context

import (
	"fmt"
	"os/exec"
	"regexp"
	"strings"
	"sync"
)

// symbolPatterns 是用于从 diff 新增行（"+"开头）中提取符号名称的正则表达式集合。
// 支持多种语言的函数、类、变量定义模式。
var symbolPatterns = []*regexp.Regexp{
	// Go 语言：函数定义（包括方法接收器），如 func Name( 或 func (r *Receiver) Name(
	regexp.MustCompile(`(?m)^\+.*func\s+(?:\([^)]+\)\s+)?([a-zA-Z_][a-zA-Z0-9_]*)\s*\(`),
	// Go 语言：类型定义，如 type Name struct 或 type Name interface
	regexp.MustCompile(`(?m)^\+.*type\s+([a-zA-Z_][a-zA-Z0-9_]*)\s+(?:struct|interface)`),
	// JS/TS：普通函数和异步函数定义，如 function name( 或 async function name(
	regexp.MustCompile(`(?m)^\+.*(?:function|async function)\s+([a-zA-Z_][a-zA-Z0-9_]*)\s*\(`),
	// JS/TS：变量赋值为函数调用，如 const name = ( 或 const name = async(
	regexp.MustCompile(`(?m)^\+.*(?:const|let|var)\s+([a-zA-Z_][a-zA-Z0-9_]*)\s*=\s*(?:async\s*)?\(`),
	// JS/TS：箭头函数，如 const name = (params) =>
	regexp.MustCompile(`(?m)^\+.*(?:const|let|var)\s+([a-zA-Z_][a-zA-Z0-9_]*)\s*=\s*(?:async\s*)?\([^)]*\)\s*=>`),
	// 类定义（Go/JS/TS/Python），如 class Name
	regexp.MustCompile(`(?m)^\+.*class\s+([a-zA-Z_][a-zA-Z0-9_]*)`),
	// JS/TS：导出声明，如 export const/function/class Name
	regexp.MustCompile(`(?m)^\+.*export\s+(?:const|let|var|function|class|async function)\s+([a-zA-Z_][a-zA-Z0-9_]*)`),
	// Python：函数和异步函数定义，如 def name( 或 async def name(
	regexp.MustCompile(`(?m)^\+.*(?:async\s+)?def\s+([a-zA-Z_][a-zA-Z0-9_]*)\s*\(`),
}

// skipSymbols 是需要跳过的常见关键字和短标识符集合。
// 这些词太常见，搜索它们的引用没有实际意义，会产生大量噪音。
var skipSymbols = map[string]bool{
	"get": true, "set": true, "new": true, "for": true,
	"if": true, "do": true, "var": true, "nil": true,
	"err": true, "ok": true,
	// Python 常见关键字和魔术方法
	"self": true, "cls": true, "def": true,
	"None": true, "True": true, "False": true,
}

// ExtractSymbolsFromDiff 从 diff 的新增行中提取函数、类、方法等符号名称。
// 使用多个正则表达式模式匹配不同语言的定义语法，自动去重，
// 并过滤掉长度 <= 2 的短标识符和常见关键字。
func ExtractSymbolsFromDiff(diff string) []string {
	seen := make(map[string]bool)
	var symbols []string

	for _, pattern := range symbolPatterns {
		matches := pattern.FindAllStringSubmatch(diff, -1)
		for _, m := range matches {
			name := m[1]
			// 跳过过短的名称、常见关键字和已出现的符号
			if len(name) <= 2 || skipSymbols[name] || seen[name] {
				continue
			}
			seen[name] = true
			symbols = append(symbols, name)
		}
	}

	return symbols
}

// FindReferences 并行使用 ripgrep (rg) 在代码库中搜索指定符号的所有出现位置。
// 对每个符号执行全文搜索，解析 ripgrep 输出的 "文件:行号:内容" 格式，
// 返回每个符号的引用位置列表。这些数据用于调用链分析。
func FindReferences(symbols []string, cwd string) []RawReference {
	// 预分配结果切片，每个 goroutine 写入自己的索引位置
	results := make([]RawReference, len(symbols))
	found := make([]bool, len(symbols))

	var wg sync.WaitGroup
	for i, symbol := range symbols {
		i, symbol := i, symbol
		wg.Add(1)
		go func() {
			defer wg.Done()
			// 使用 ripgrep 搜索：-n 显示行号，-H 显示文件名，--no-heading 不分组
			cmd := exec.Command("rg", "-n", "-H", "--no-heading", symbol)
			cmd.Dir = cwd
			out, err := cmd.Output()
			if err != nil || len(out) == 0 {
				return
			}

			// 解析 ripgrep 输出，格式为 "文件:行号:内容"
			var locations []ReferenceLocation
			for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
				if line == "" {
					continue
				}
				parts := strings.SplitN(line, ":", 3)
				if len(parts) < 3 {
					continue
				}
				lineNum := 0
				fmt.Sscanf(parts[1], "%d", &lineNum)
				locations = append(locations, ReferenceLocation{
					File:    parts[0],
					Line:    lineNum,
					Content: strings.TrimSpace(parts[2]),
				})
			}

			if len(locations) > 0 {
				results[i] = RawReference{
					Symbol:       symbol,
					FoundInFiles: locations,
				}
				found[i] = true
			}
		}()
	}
	wg.Wait()

	// 按原始顺序收集有结果的引用
	var references []RawReference
	for i := range results {
		if found[i] {
			references = append(references, results[i])
		}
	}
	return references
}

// FormatCallChainForReviewer 将原始引用数据格式化为结构化的 Markdown 文本。
// 每个符号最多展示 10 个调用位置，内容截断到 150 字符，
// 以便审查者快速了解被修改符号在系统中的调用关系。
func FormatCallChainForReviewer(references []RawReference) string {
	if len(references) == 0 {
		return ""
	}

	var sb strings.Builder
	sb.WriteString("## Call Chain Context\n\n")

	for i, ref := range references {
		if i > 0 {
			sb.WriteString("\n\n---\n\n")
		}
		// 限制展示数量，避免输出过长
		callers := ref.FoundInFiles
		if len(callers) > 10 {
			callers = callers[:10]
		}

		sb.WriteString(fmt.Sprintf("### Callers of `%s`\n", ref.Symbol))
		sb.WriteString(fmt.Sprintf("Found in %d locations:\n\n", len(ref.FoundInFiles)))

		for j, f := range callers {
			content := f.Content
			if len(content) > 150 {
				content = content[:150]
			}
			sb.WriteString(fmt.Sprintf("%d. %s:%d\n   > %s\n\n", j+1, f.File, f.Line, content))
		}
	}

	return sb.String()
}

// CollectReferences 是符号引用收集的便捷入口函数。
// 先从 diff 中提取符号名称，然后在代码库中搜索这些符号的所有引用位置。
func CollectReferences(diff, cwd string) []RawReference {
	symbols := ExtractSymbolsFromDiff(diff)
	return FindReferences(symbols, cwd)
}
