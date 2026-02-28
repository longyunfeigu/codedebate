package platform

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
	"time"
)

const (
	// IssueMarkerPrefix 保留旧版 marker 前缀，兼容历史评论与旧测试。
	IssueMarkerPrefix = "<!-- codedebate:issue:"
	// CodeDebateIssueMetaPrefix 是新版结构化 marker 前缀。
	CodeDebateIssueMetaPrefix = "<!-- codedebate:issue "
)

var markdownSyntaxRe = regexp.MustCompile("[`*_#>\\[\\]\\(\\)]")

func BuildIssueKey(issue IssueForComment) string {
	lineKey := "0"
	if issue.Line != nil {
		lineKey = fmt.Sprintf("%d", *issue.Line)
	}
	key := strings.Join([]string{
		normalizeIssueKeyText(issue.File),
		lineKey,
		normalizeIssueKeyText(issue.Title),
		truncateForKey(normalizeIssueKeyText(issue.Description), 160),
	}, "|")
	return shortHash(key)
}

func BuildCodeDebateCommentMeta(issueKey, body, path string, line, oldLine *int, source, runID, headSHA, status string) CodeDebateCommentMeta {
	if status == "" {
		status = "active"
	}
	return CodeDebateCommentMeta{
		IssueKey:   issueKey,
		Status:     status,
		RunID:      runID,
		HeadSHA:    headSHA,
		BodyHash:   BodyHash(body),
		AnchorHash: AnchorHash(path, line, oldLine, source),
	}
}

func EncodeCodeDebateMeta(meta CodeDebateCommentMeta) string {
	payload, _ := json.Marshal(meta)
	return CodeDebateIssueMetaPrefix + string(payload) + " -->"
}

func ParseCodeDebateMeta(body string) (*CodeDebateCommentMeta, bool) {
	marker := extractStructuredIssueMarker(body)
	if marker == "" {
		return nil, false
	}
	raw := strings.TrimSuffix(strings.TrimPrefix(marker, CodeDebateIssueMetaPrefix), " -->")
	var meta CodeDebateCommentMeta
	if err := json.Unmarshal([]byte(raw), &meta); err != nil {
		return nil, false
	}
	return &meta, true
}

func IsCodeDebateCommentBody(body string) bool {
	if _, ok := ParseCodeDebateMeta(body); ok {
		return true
	}
	return extractLegacyIssueMarker(body) != ""
}

func StripCodeDebateMeta(body string) string {
	if marker := extractStructuredIssueMarker(body); marker != "" {
		body = strings.Replace(body, marker, "", 1)
	}
	if marker := extractLegacyIssueMarker(body); marker != "" {
		body = strings.Replace(body, marker, "", 1)
	}
	return strings.TrimSpace(body)
}

func BodyHash(body string) string {
	return shortHash(strings.TrimSpace(StripCodeDebateMeta(body)))
}

func AnchorHash(path string, line, oldLine *int, source string) string {
	lineStr := "0"
	if line != nil {
		lineStr = fmt.Sprintf("%d", *line)
	}
	oldLineStr := "0"
	if oldLine != nil {
		oldLineStr = fmt.Sprintf("%d", *oldLine)
	}
	key := fmt.Sprintf("%s|%s|%s|%s", strings.TrimSpace(path), lineStr, oldLineStr, strings.TrimSpace(source))
	return shortHash(key)
}

func NewLifecycleRunID(headSHA string) string {
	head := truncateForKey(strings.TrimSpace(headSHA), 12)
	if head == "" {
		head = "nohead"
	}
	return fmt.Sprintf("%s-%s", head, time.Now().UTC().Format("20060102T150405Z"))
}

func extractIssueMarker(body string) string {
	if marker := extractStructuredIssueMarker(body); marker != "" {
		return marker
	}
	return extractLegacyIssueMarker(body)
}

func extractStructuredIssueMarker(body string) string {
	return extractMarker(body, CodeDebateIssueMetaPrefix)
}

func extractLegacyIssueMarker(body string) string {
	return extractMarker(body, IssueMarkerPrefix)
}

func extractMarker(body, prefix string) string {
	idx := strings.Index(body, prefix)
	if idx < 0 {
		return ""
	}
	end := strings.Index(body[idx:], "-->")
	if end < 0 {
		return ""
	}
	return body[idx : idx+end+3]
}

func shortHash(s string) string {
	sum := sha256.Sum256([]byte(s))
	return fmt.Sprintf("%x", sum[:8])
}

func normalizeIssueKeyText(s string) string {
	s = markdownSyntaxRe.ReplaceAllString(strings.ToLower(strings.TrimSpace(s)), "")
	return strings.Join(strings.Fields(s), " ")
}

func truncateForKey(s string, limit int) string {
	if limit <= 0 {
		return ""
	}
	runes := []rune(s)
	if len(runes) <= limit {
		return s
	}
	return string(runes[:limit])
}
