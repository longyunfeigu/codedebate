package platform

import (
	"crypto/sha256"
	"fmt"
	"strings"
)

// FormatIssueBody 将单个问题格式化为 Markdown 格式的评论正文，并附带 CodeDebate meta marker。
func FormatIssueBody(issue IssueForComment) string {
	return FormatIssueBodyWithMeta(issue, "", "")
}

// FormatIssueBodyWithMeta 使用给定 run/head 信息生成带结构化 marker 的 issue 正文。
func FormatIssueBodyWithMeta(issue IssueForComment, runID, headSHA string) string {
	displayBody := FormatIssueDisplayBody(issue)
	meta := BuildCodeDebateCommentMeta(
		BuildIssueKey(issue),
		displayBody,
		issue.File,
		issue.Line,
		nil,
		"inline",
		runID,
		headSHA,
		"active",
	)
	return EncodeCodeDebateMeta(meta) + "\n" + displayBody
}

// FormatIssueDisplayBody 仅渲染给人看的 issue 正文，不包含隐藏 marker。
func FormatIssueDisplayBody(issue IssueForComment) string {
	severityBadge := SeverityToBadge(issue.Severity)
	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("%s **%s**\n\n", severityBadge, issue.Title))
	sb.WriteString(issue.Description)
	if issue.SuggestedFix != "" {
		sb.WriteString(fmt.Sprintf("\n\n**Suggested fix:** %s", issue.SuggestedFix))
	}
	if issue.RaisedBy != "" {
		sb.WriteString(fmt.Sprintf("\n\n_Raised by: %s_", issue.RaisedBy))
	}
	return sb.String()
}

// BuildIssueMarker 生成旧版幂等 marker，保留给兼容逻辑与测试使用。
func BuildIssueMarker(file string, line *int, severity, title string) string {
	lineStr := "0"
	if line != nil {
		lineStr = fmt.Sprintf("%d", *line)
	}
	key := fmt.Sprintf("%s:%s:%s:%s",
		strings.ToLower(strings.TrimSpace(file)),
		lineStr,
		strings.ToLower(strings.TrimSpace(severity)),
		strings.ToLower(strings.TrimSpace(title)),
	)
	h := sha256.Sum256([]byte(key))
	return fmt.Sprintf("%s%x -->", IssueMarkerPrefix, h[:4])
}

// SeverityToBadge 将严重等级字符串转换为对应的 emoji 徽章。
func SeverityToBadge(severity string) string {
	switch strings.ToLower(severity) {
	case "critical":
		return "🔴"
	case "high":
		return "🟠"
	case "medium":
		return "🟡"
	case "low":
		return "🟢"
	default:
		return "⚪"
	}
}

// TruncStr 将字符串截断到指定的最大长度。
func TruncStr(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen]
}
