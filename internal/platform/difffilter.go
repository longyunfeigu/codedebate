package platform

import (
	"path/filepath"
	"strings"
)

// BuiltinExcludePatterns 是内置的 diff 过滤模式列表。
// 过滤生成文件、vendor 目录和 lock 文件，避免它们浪费 AI 审查的 token。
var BuiltinExcludePatterns = []string{
	"*.pb.go", "*.pb.cc", "*.pb.h",
	"*_generated.*", "*.generated.*", "**/generated/**",
	"*.gen.go", "*.gen.ts",
	"vendor/**", "**/vendor/**",
	"go.sum",
	"package-lock.json", "yarn.lock", "pnpm-lock.yaml",
}

// FilterDiff 根据内置和用户自定义的排除模式过滤 unified diff。
// 返回过滤后的 diff 文本。
func FilterDiff(diff string, userPatterns []string) string {
	allPatterns := make([]string, 0, len(BuiltinExcludePatterns)+len(userPatterns))
	allPatterns = append(allPatterns, BuiltinExcludePatterns...)
	allPatterns = append(allPatterns, userPatterns...)
	sections := SplitDiffByFile(diff)
	var kept []string
	for _, section := range sections {
		path := ExtractFilePath(section)
		if path == "" || !ShouldExclude(path, allPatterns) {
			kept = append(kept, section)
		}
	}
	return strings.Join(kept, "")
}

// SplitDiffByFile 将 unified diff 按文件分割成多个段落。
// 每个段落以 "diff --git" 开头。
func SplitDiffByFile(diff string) []string {
	const marker = "diff --git "
	var sections []string
	remaining := diff

	for {
		idx := strings.Index(remaining, marker)
		if idx < 0 {
			if remaining != "" {
				sections = append(sections, remaining)
			}
			break
		}

		if idx > 0 {
			// diff 开头之前的内容（通常不存在，但保险起见）
			sections = append(sections, remaining[:idx])
		}

		// 找到下一个 "diff --git" 的位置
		nextIdx := strings.Index(remaining[idx+len(marker):], marker)
		if nextIdx < 0 {
			sections = append(sections, remaining[idx:])
			break
		}
		sections = append(sections, remaining[idx:idx+len(marker)+nextIdx])
		remaining = remaining[idx+len(marker)+nextIdx:]
	}

	return sections
}

// ExtractFilePath 从 diff 段落中提取文件路径。
// 支持 "diff --git a/path b/path" 格式。
func ExtractFilePath(section string) string {
	lines := strings.SplitN(section, "\n", 2)
	if len(lines) == 0 {
		return ""
	}
	line := lines[0]
	if !strings.HasPrefix(line, "diff --git ") {
		return ""
	}
	// "diff --git a/foo/bar.go b/foo/bar.go"
	parts := strings.SplitN(line, " b/", 2)
	if len(parts) == 2 {
		return strings.TrimSpace(parts[1])
	}
	return ""
}

// ShouldExclude 检查路径是否匹配任意排除模式。
func ShouldExclude(path string, patterns []string) bool {
	for _, pattern := range patterns {
		if matchPattern(path, pattern) {
			return true
		}
	}
	return false
}

// matchPattern 检查路径是否匹配 glob 模式。
// 支持 ** 通配符（匹配任意层级目录）。
func matchPattern(path, pattern string) bool {
	// 处理 ** — 对路径和文件名都尝试匹配
	if strings.Contains(pattern, "**") {
		// "vendor/**" 或 "**/vendor/**"
		// 移除 ** 并分割为多个 glob 部分
		simplified := strings.ReplaceAll(pattern, "**/", "")
		// 尝试匹配完整路径
		if matched, _ := filepath.Match(simplified, path); matched {
			return true
		}
		// 尝试匹配路径中的每个部分
		if matched, _ := filepath.Match(simplified, filepath.Base(path)); matched {
			return true
		}
		// 检查路径是否包含模式中的目录部分
		dirPart := strings.TrimSuffix(strings.TrimPrefix(pattern, "**/"), "/**")
		if dirPart != pattern && strings.Contains(path, dirPart+"/") {
			return true
		}
		return false
	}

	// 简单 glob：匹配完整路径和文件名
	if matched, _ := filepath.Match(pattern, path); matched {
		return true
	}
	if matched, _ := filepath.Match(pattern, filepath.Base(path)); matched {
		return true
	}
	return false
}
