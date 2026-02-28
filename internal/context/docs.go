package context

import (
	"os"
	"path/filepath"
	"strings"
)

// defaultDocPatterns 是默认的文档文件/目录匹配模式。
// 覆盖常见的项目文档位置和文件名。
var defaultDocPatterns = []string{
	"docs",
	"README.md",
	"ARCHITECTURE.md",
	"DESIGN.md",
	"CONTRIBUTING.md",
}

// skipDirs 是在递归搜索文档时需要跳过的目录集合。
// 跳过依赖目录、构建产物目录和版本控制目录以提高搜索效率。
var skipDirs = map[string]bool{
	"node_modules": true, ".git": true, "dist": true,
	"build": true, "vendor": true, "__pycache__": true,
}

// CollectDocs 根据指定的模式查找并读取项目文档文件。
// 支持两种模式：目录模式（递归搜索其中的 .md 文件）和文件模式（直接读取文件）。
// 文档内容会被提供给 AI 进行上下文分析，帮助理解项目的架构和设计规范。
// maxSize 参数限制单个文件的最大字节数，避免读取过大的文件。
func CollectDocs(patterns []string, maxSize int, cwd string) ([]RawDoc, error) {
	if len(patterns) == 0 {
		patterns = defaultDocPatterns
	}
	if maxSize <= 0 {
		maxSize = 50000
	}

	var docs []RawDoc
	seen := make(map[string]bool) // 用于去重，避免同一文件被多个模式匹配到时重复读取

	for _, pattern := range patterns {
		fullPath := filepath.Join(cwd, pattern)

		info, err := os.Stat(fullPath)
		if err != nil {
			continue // 路径不存在则跳过
		}

		if info.IsDir() {
			// 目录模式：递归查找目录中的所有 Markdown 文件
			mdFiles := findMarkdownFiles(fullPath, maxSize)
			for _, file := range mdFiles {
				relPath, _ := filepath.Rel(cwd, file)
				if seen[relPath] {
					continue
				}
				seen[relPath] = true

				content, err := os.ReadFile(file)
				if err != nil {
					continue
				}
				docs = append(docs, RawDoc{Path: relPath, Content: string(content)})
			}
		} else if info.Size() <= int64(maxSize) {
			// 文件模式：直接读取文件（需在大小限制内）
			relPath, _ := filepath.Rel(cwd, fullPath)
			if seen[relPath] {
				continue
			}
			seen[relPath] = true

			content, err := os.ReadFile(fullPath)
			if err != nil {
				continue
			}
			docs = append(docs, RawDoc{Path: relPath, Content: string(content)})
		}
	}

	return docs, nil
}

// findMarkdownFiles 递归搜索指定目录中的所有 Markdown (.md) 文件。
// 跳过 skipDirs 中列出的目录（如 node_modules、.git 等），
// 只返回大小不超过 maxSize 的常规文件。
func findMarkdownFiles(dir string, maxSize int) []string {
	var files []string

	entries, err := os.ReadDir(dir)
	if err != nil {
		return files
	}

	for _, entry := range entries {
		fullPath := filepath.Join(dir, entry.Name())

		if entry.IsDir() {
			// 跳过不需要搜索的目录
			if skipDirs[entry.Name()] {
				continue
			}
			// 递归搜索子目录
			files = append(files, findMarkdownFiles(fullPath, maxSize)...)
		} else if entry.Type().IsRegular() && strings.HasSuffix(entry.Name(), ".md") {
			// 检查文件大小是否在限制内
			info, err := entry.Info()
			if err != nil {
				continue
			}
			if info.Size() <= int64(maxSize) {
				files = append(files, fullPath)
			}
		}
	}

	return files
}
