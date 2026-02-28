package reviewpost

import (
	"fmt"
	"strings"

	"github.com/longyunfeigu/codedebate/internal/orchestrator"
	"github.com/longyunfeigu/codedebate/internal/platform"
)

// CodeDebateSummaryMarker 用于定位和更新 CodeDebate 自动发布的总结评论。
const CodeDebateSummaryMarker = "<!-- codedebate:summary -->"

// ConvertIssuesToPlatform 将编排器产出的 MergedIssue 列表转换为平台通用的评论格式。
func ConvertIssuesToPlatform(issues []orchestrator.MergedIssue) []platform.IssueForComment {
	result := make([]platform.IssueForComment, 0, len(issues))
	for _, issue := range issues {
		raisedBy := ""
		supporters := issue.SupportedBy
		if len(supporters) == 0 {
			supporters = issue.RaisedBy
		}
		if len(supporters) > 0 {
			raisedBy = strings.Join(supporters, ", ")
		}
		result = append(result, platform.IssueForComment{
			File:         issue.File,
			Line:         issue.Line,
			Title:        issue.Title,
			Description:  issue.Description,
			Severity:     issue.Severity,
			SuggestedFix: issue.SuggestedFix,
			RaisedBy:     raisedBy,
		})
	}
	return result
}

// BuildSummaryNoteBody 构造可幂等更新的总结评论正文。
func BuildSummaryNoteBody(finalConclusion string) string {
	return CodeDebateSummaryMarker + "\n## CodeDebate Code Review Summary\n\n" + strings.TrimSpace(finalConclusion)
}

// UpsertSummaryNote 尝试调用平台的 UpsertSummaryNote；若不支持则回退到 PostNote。
func UpsertSummaryNote(plat platform.Named, mrID, repo, body string) error {
	type summaryUpserter interface {
		UpsertSummaryNote(mrID, repo, marker, body string) error
	}
	type summaryPoster interface {
		PostNote(mrID, repo, body string) error
	}

	if upserter, ok := plat.(summaryUpserter); ok {
		return upserter.UpsertSummaryNote(mrID, repo, CodeDebateSummaryMarker, body)
	}
	if poster, ok := plat.(summaryPoster); ok {
		return poster.PostNote(mrID, repo, body)
	}
	return fmt.Errorf("platform %q does not support summary posting", plat.Name())
}

// SupportsSummaryPosting 判断平台是否支持发布总结评论。
func SupportsSummaryPosting(plat platform.Named) bool {
	if plat == nil {
		return false
	}
	type summaryUpserter interface {
		UpsertSummaryNote(mrID, repo, marker, body string) error
	}
	type summaryPoster interface {
		PostNote(mrID, repo, body string) error
	}
	if _, ok := plat.(summaryUpserter); ok {
		return true
	}
	if _, ok := plat.(summaryPoster); ok {
		return true
	}
	return false
}
