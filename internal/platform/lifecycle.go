package platform

import (
	"fmt"
	"strconv"
	"strings"
)

func BuildDesiredComments(classified []ClassifiedComment, runID, headSHA string) []DesiredComment {
	desired := make([]DesiredComment, 0, len(classified))
	for _, cc := range classified {
		displayBody := StripCodeDebateMeta(cc.Input.Body)
		renderedBody := RenderCommentBody(cc.Mode, cc.Input.Path, cc.Input.Line, displayBody)

		issueKey := issueKeyFromBody(cc.Input.Path, cc.Input.Line, cc.Input.Body)
		meta := BuildCodeDebateCommentMeta(issueKey, renderedBody, cc.Input.Path, cc.Input.Line, cc.OldLine, cc.Mode, runID, headSHA, "active")

		desired = append(desired, DesiredComment{
			IssueKey:   issueKey,
			Path:       cc.Input.Path,
			Line:       cc.Input.Line,
			OldLine:    cc.OldLine,
			Body:       EncodeCodeDebateMeta(meta) + "\n" + renderedBody,
			Source:     cc.Mode,
			BodyHash:   meta.BodyHash,
			AnchorHash: meta.AnchorHash,
		})
	}
	return desired
}

func RenderCommentBody(mode, path string, line *int, body string) string {
	body = strings.TrimSpace(StripCodeDebateMeta(body))
	switch mode {
	case "file":
		if line != nil {
			return fmt.Sprintf("**Line %d:**\n\n%s", *line, body)
		}
		return body
	case "global":
		location := fmt.Sprintf("**%s**\n\n", path)
		if line != nil {
			location = fmt.Sprintf("**%s:%d**\n\n", path, *line)
		}
		return location + body
	default:
		return body
	}
}

func FilterCodeDebateComments(existing []ExistingComment) []ExistingComment {
	filtered := make([]ExistingComment, 0, len(existing))
	for _, comment := range existing {
		if !comment.IsCodeDebate || comment.Meta == nil {
			continue
		}
		if comment.Meta.Status != "" && comment.Meta.Status != "active" {
			continue
		}
		filtered = append(filtered, comment)
	}
	return filtered
}

func PlanLifecycle(existing []ExistingComment, desired []DesiredComment) LifecyclePlan {
	filtered := FilterCodeDebateComments(existing)
	existingByKey := make(map[string]ExistingComment, len(filtered))
	for _, comment := range filtered {
		if comment.Meta == nil || comment.Meta.IssueKey == "" {
			continue
		}
		key := comment.Meta.IssueKey
		if current, ok := existingByKey[key]; !ok || isNewerComment(current, comment) {
			existingByKey[key] = comment
		}
	}

	desiredKeys := make(map[string]struct{}, len(desired))
	matchedExisting := make(map[string]struct{}, len(existingByKey))
	var plan LifecyclePlan

	for _, item := range desired {
		desiredKeys[item.IssueKey] = struct{}{}
		existingComment, ok := existingByKey[item.IssueKey]
		if !ok {
			plan.Create = append(plan.Create, item)
			continue
		}

		matchedExisting[existingComment.ID] = struct{}{}
		switch {
		case existingComment.Meta.BodyHash == item.BodyHash && existingComment.Meta.AnchorHash == item.AnchorHash:
			plan.Noop = append(plan.Noop, item)
		case existingComment.Meta.AnchorHash == item.AnchorHash:
			plan.Update = append(plan.Update, CommentUpdate{Existing: existingComment, Desired: item})
		default:
			plan.Supersede = append(plan.Supersede, CommentSupersede{Existing: existingComment, Desired: item})
			plan.Create = append(plan.Create, item)
		}
	}

	for _, comment := range existingByKey {
		if _, ok := matchedExisting[comment.ID]; ok {
			continue
		}
		if _, ok := desiredKeys[comment.Meta.IssueKey]; ok {
			continue
		}
		plan.Resolve = append(plan.Resolve, CommentResolve{Existing: comment})
	}

	return plan
}

func RenderResolvedBody(existing ExistingComment, runID, headSHA string) string {
	meta := existingLifecycleMeta(existing, runID, headSHA, "resolved")
	body := "This CodeDebate finding was not reproduced in the latest review run and is now marked as resolved.\n\nPrevious finding:\n\n" + StripCodeDebateMeta(existing.Body)
	meta.BodyHash = BodyHash(body)
	return EncodeCodeDebateMeta(meta) + "\n" + body
}

func RenderSupersededBody(existing ExistingComment, replacement DesiredComment, runID, headSHA string) string {
	meta := existingLifecycleMeta(existing, runID, headSHA, "superseded")
	body := "This CodeDebate finding has been superseded by a newer comment for the same issue in the latest review run.\n\nPrevious finding:\n\n" + StripCodeDebateMeta(existing.Body)
	meta.BodyHash = BodyHash(body)
	return EncodeCodeDebateMeta(meta) + "\n" + body
}

func DuplicateCandidates(existing []ExistingComment, issueKey string) []ExistingComment {
	if issueKey == "" {
		return existing
	}
	filtered := make([]ExistingComment, 0, len(existing))
	for _, comment := range existing {
		if comment.Meta != nil && comment.Meta.IssueKey == issueKey {
			continue
		}
		filtered = append(filtered, comment)
	}
	return filtered
}

func issueKeyFromBody(path string, line *int, body string) string {
	if meta, ok := ParseCodeDebateMeta(body); ok && meta.IssueKey != "" {
		return meta.IssueKey
	}
	lineKey := "0"
	if line != nil {
		lineKey = fmt.Sprintf("%d", *line)
	}
	return shortHash(strings.Join([]string{
		normalizeIssueKeyText(path),
		lineKey,
		truncateForKey(normalizeIssueKeyText(StripCodeDebateMeta(body)), 160),
	}, "|"))
}

func existingLifecycleMeta(existing ExistingComment, runID, headSHA, status string) CodeDebateCommentMeta {
	meta := CodeDebateCommentMeta{
		IssueKey:   "",
		Status:     status,
		RunID:      runID,
		HeadSHA:    headSHA,
		AnchorHash: AnchorHash(existing.Path, existing.Line, existing.OldLine, existing.Source),
	}
	if existing.Meta != nil {
		meta = *existing.Meta
		meta.Status = status
		if runID != "" {
			meta.RunID = runID
		}
		if headSHA != "" {
			meta.HeadSHA = headSHA
		}
		if meta.AnchorHash == "" {
			meta.AnchorHash = AnchorHash(existing.Path, existing.Line, existing.OldLine, existing.Source)
		}
	}
	return meta
}

func isNewerComment(current, candidate ExistingComment) bool {
	if current.ID == "" {
		return true
	}
	curID, curErr := strconv.Atoi(current.ID)
	candID, candErr := strconv.Atoi(candidate.ID)
	if curErr == nil && candErr == nil {
		return candID > curID
	}
	return candidate.ID > current.ID
}
