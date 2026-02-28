package platform

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
)

// hunkHeaderRegex 匹配 unified diff 的 hunk 头部行（仅捕获 new 起始行号）。
var hunkHeaderRegex = regexp.MustCompile(`^@@ -\d+(?:,\d+)? \+(\d+)(?:,\d+)? @@`)

// hunkHeaderRegexFull 匹配 unified diff 的 hunk 头部行（同时捕获 old 和 new 起始行号）。
var hunkHeaderRegexFull = regexp.MustCompile(`^@@ -(\d+)(?:,\d+)? \+(\d+)(?:,\d+)? @@`)

// DiffLineInfo 描述 diff 中一行的位置信息。
// 对于 added 行（+），只有 NewLine；对于 context 行（空格前缀），同时有 OldLine 和 NewLine。
type DiffLineInfo struct {
	NewLine int // 新文件中的行号
	OldLine int // 旧文件中的行号（仅 context 行有效，added 行为 0）
}

// ParseDiffLinesEx 从 unified diff 补丁文本中解析出有效行的详细信息。
// 返回 map[newLineNumber]DiffLineInfo，包含每行的 old/new 行号信息。
func ParseDiffLinesEx(patch string) map[int]DiffLineInfo {
	lines := make(map[int]DiffLineInfo)
	patchLines := strings.Split(patch, "\n")
	rightLine := 0
	leftLine := 0

	for _, line := range patchLines {
		if m := hunkHeaderRegexFull.FindStringSubmatch(line); m != nil {
			leftLine, _ = strconv.Atoi(m[1])
			rightLine, _ = strconv.Atoi(m[2])
			continue
		}

		if rightLine == 0 {
			continue
		}

		if strings.HasPrefix(line, "+") {
			lines[rightLine] = DiffLineInfo{NewLine: rightLine, OldLine: 0}
			rightLine++
		} else if strings.HasPrefix(line, "-") {
			leftLine++
		} else if strings.HasPrefix(line, " ") {
			lines[rightLine] = DiffLineInfo{NewLine: rightLine, OldLine: leftLine}
			leftLine++
			rightLine++
		}
	}

	return lines
}

// ParseDiffLines 从 unified diff 补丁文本中解析出右侧（新文件）的有效行号集合。
// 兼容旧调用方，内部调用 ParseDiffLinesEx。
func ParseDiffLines(patch string) map[int]bool {
	detailed := ParseDiffLinesEx(patch)
	simple := make(map[int]bool, len(detailed))
	for line := range detailed {
		simple[line] = true
	}
	return simple
}

// ClassifyCommentsByDiff 根据 diff 信息对评论进行分类。
// diffInfo 是 map[filename]map[lineNumber]bool 格式的 diff 信息。
func ClassifyCommentsByDiff(comments []ReviewCommentInput, diffInfo map[string]map[int]bool) []ClassifiedComment {
	// 将 simple diffInfo 转换为 detailed 格式（OldLine 全部为 0）
	detailed := make(map[string]map[int]DiffLineInfo, len(diffInfo))
	for path, lines := range diffInfo {
		m := make(map[int]DiffLineInfo, len(lines))
		for line := range lines {
			m[line] = DiffLineInfo{NewLine: line, OldLine: 0}
		}
		detailed[path] = m
	}
	return ClassifyCommentsByDiffEx(comments, detailed)
}

// ClassifyCommentsByDiffEx 根据详细 diff 信息对评论进行分类。
// diffInfo 是 map[filename]map[newLineNumber]DiffLineInfo 格式，包含 old/new 行号。
func ClassifyCommentsByDiffEx(comments []ReviewCommentInput, diffInfo map[string]map[int]DiffLineInfo) []ClassifiedComment {
	// 构建 simple map 用于路径解析和就近匹配
	simpleInfo := make(map[string]map[int]bool, len(diffInfo))
	for path, lines := range diffInfo {
		m := make(map[int]bool, len(lines))
		for line := range lines {
			m[line] = true
		}
		simpleInfo[path] = m
	}

	classified := make([]ClassifiedComment, 0, len(comments))
	for _, c := range comments {
		// 先尝试精确匹配，再尝试后缀匹配（reviewer 可能输出短路径如 "base_parser.py"）
		resolvedPath := resolvePathByDiff(c.Path, simpleInfo)
		resolved := c
		resolved.Path = resolvedPath

		fileLines, fileInDiff := diffInfo[resolvedPath]
		simpleLines := simpleInfo[resolvedPath]
		if fileInDiff && c.Line != nil {
			if info, ok := fileLines[*c.Line]; ok {
				// 行号在 diff 范围内：inline
				cc := ClassifiedComment{Input: resolved, Mode: "inline"}
				if info.OldLine > 0 {
					oldLine := info.OldLine
					cc.OldLine = &oldLine
				}
				classified = append(classified, cc)
				continue
			}
		}
		if fileInDiff {
			// 文件在 diff 中但行号不在范围内：先尝试 ±20 行就近匹配
			if c.Line != nil {
				if nearLine, found := FindNearestLine(simpleLines, *c.Line, 20); found {
					nearLineCopy := nearLine
					resolved.Line = &nearLineCopy
					resolved.Body = fmt.Sprintf("**Line %d:**\n\n%s", *c.Line, resolved.Body)
					cc := ClassifiedComment{Input: resolved, Mode: "inline"}
					if info, ok := fileLines[nearLine]; ok && info.OldLine > 0 {
						oldLine := info.OldLine
						cc.OldLine = &oldLine
					}
					classified = append(classified, cc)
					continue
				}
			}
			// 回退：分配第一个变更行
			if firstLine := firstDiffLine(simpleLines); firstLine > 0 {
				resolved.Line = &firstLine
				cc := ClassifiedComment{Input: resolved, Mode: "inline"}
				if info, ok := fileLines[firstLine]; ok && info.OldLine > 0 {
					oldLine := info.OldLine
					cc.OldLine = &oldLine
				}
				classified = append(classified, cc)
			} else {
				classified = append(classified, ClassifiedComment{Input: resolved, Mode: "file"})
			}
		} else {
			classified = append(classified, ClassifiedComment{Input: resolved, Mode: "global"})
		}
	}
	return classified
}

// FindNearestLine 在 diff 行号集合中找到距离 targetLine 最近的行，阈值 maxDistance 行。
// 如果找到则返回最近行号和 true，否则返回 0 和 false。
func FindNearestLine(diffLines map[int]bool, targetLine, maxDistance int) (int, bool) {
	bestLine := 0
	bestDist := maxDistance + 1
	for line := range diffLines {
		dist := line - targetLine
		if dist < 0 {
			dist = -dist
		}
		if dist <= maxDistance && (dist < bestDist || (dist == bestDist && (bestLine == 0 || line < bestLine))) {
			bestDist = dist
			bestLine = line
		}
	}
	if bestDist <= maxDistance {
		return bestLine, true
	}
	return 0, false
}

// firstDiffLine 返回 diff 行号集合中最小的行号。
func firstDiffLine(lines map[int]bool) int {
	min := 0
	for line := range lines {
		if min == 0 || line < min {
			min = line
		}
	}
	return min
}

// resolvePathByDiff 将评论中的文件路径解析为 diff 中的完整路径。
// 策略：精确匹配 → 后缀匹配（唯一时采用）。
// 例如 "base_parser.py" 匹配 "backend/infrastructure/parsers/base_parser.py"。
func resolvePathByDiff(path string, diffInfo map[string]map[int]bool) string {
	// 精确匹配
	if _, ok := diffInfo[path]; ok {
		return path
	}

	// 后缀匹配：找出所有以 path 结尾的 diff 文件
	suffix := "/" + path
	var matches []string
	for diffPath := range diffInfo {
		if strings.HasSuffix(diffPath, suffix) || diffPath == path {
			matches = append(matches, diffPath)
		}
	}

	// 唯一匹配时采用
	if len(matches) == 1 {
		return matches[0]
	}

	return path
}

// AnnotateDiffWithLineNumbers 给 unified diff 的每一行加上显式的新文件行号前缀。
// 这样 AI reviewer 可以直接看到每行的行号，无需从 hunk header 手动推算。
// 例如: "255:  existing line", "256: +new line", "   : -removed line"
func AnnotateDiffWithLineNumbers(diff string) string {
	lines := strings.Split(diff, "\n")
	var result []string
	rightLine := 0
	maxWidth := 4 // 行号占位宽度

	for _, line := range lines {
		// hunk header: 提取起始行号，原样保留
		if m := hunkHeaderRegex.FindStringSubmatch(line); m != nil {
			rightLine, _ = strconv.Atoi(m[1])
			result = append(result, line)
			continue
		}

		// 文件级 header (diff --git, index, ---, +++, etc.)：原样保留并重置行号
		if strings.HasPrefix(line, "diff ") || strings.HasPrefix(line, "index ") ||
			strings.HasPrefix(line, "--- ") || strings.HasPrefix(line, "+++ ") ||
			strings.HasPrefix(line, "new file") || strings.HasPrefix(line, "deleted file") ||
			strings.HasPrefix(line, "old mode") || strings.HasPrefix(line, "new mode") ||
			strings.HasPrefix(line, "similarity") || strings.HasPrefix(line, "rename") {
			rightLine = 0
			result = append(result, line)
			continue
		}

		// 还没进入 hunk 区域
		if rightLine == 0 {
			result = append(result, line)
			continue
		}

		if strings.HasPrefix(line, "+") {
			result = append(result, fmt.Sprintf("%*d:%s", maxWidth, rightLine, line))
			rightLine++
		} else if strings.HasPrefix(line, "-") {
			// 删除行没有新文件行号
			result = append(result, fmt.Sprintf("%*s:%s", maxWidth, "", line))
		} else if strings.HasPrefix(line, " ") {
			result = append(result, fmt.Sprintf("%*d:%s", maxWidth, rightLine, line))
			rightLine++
		} else {
			// 空行或其他（如 "\ No newline at end of file"）
			result = append(result, line)
		}
	}

	return strings.Join(result, "\n")
}

// IsDuplicateComment 检查待发布的评论是否与已存在的评论重复。
// 优先通过 <!-- codedebate:issue:hash --> marker 匹配（幂等去重），
// 回退到 (path, line, body前缀) 匹配（兼容无 marker 的旧评论）。
func IsDuplicateComment(comment ReviewCommentInput, existing []ExistingComment) bool {
	// 提取当前评论的 marker（如果有）
	commentMarker := extractIssueMarker(comment.Body)

	prefix := TruncStr(StripCodeDebateMeta(comment.Body), 100)
	for _, e := range existing {
		// 方式一：marker 匹配（不要求 path/line 完全一致，因为 near-line 可能改变行号）
		if commentMarker != "" {
			if existingMarker := extractIssueMarker(e.Body); existingMarker == commentMarker {
				return true
			}
		}

		// 方式二：传统匹配（path + line + body 前缀）
		if e.Path != comment.Path {
			continue
		}
		if (comment.Line == nil) != (e.Line == nil) {
			continue
		}
		if comment.Line != nil && e.Line != nil && *comment.Line != *e.Line {
			continue
		}
		if TruncStr(StripCodeDebateMeta(e.Body), 100) == prefix {
			return true
		}
	}
	return false
}
