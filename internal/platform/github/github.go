// Package github 实现了 GitHub 平台的 Platform 接口。
// 通过 gh CLI 工具与 GitHub API 交互。
package github

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"os/exec"
	"regexp"
	"strconv"
	"strings"

	"github.com/longyunfeigu/codedebate/internal/platform"
	"github.com/longyunfeigu/codedebate/internal/util"
)

// repoRegex 用于从 GitHub 远程 URL 中提取 owner/repo 格式的仓库标识
var repoRegex = regexp.MustCompile(`github\.com[:/]([^/]+/[^/.]+)`)

// prNumberRegex 用于验证 PR 编号格式（纯数字）
var prNumberRegex = regexp.MustCompile(`^\d+$`)

// urlPRNumberRegex 从 GitHub PR URL 中提取 PR 编号
var urlPRNumberRegex = regexp.MustCompile(`/pull/(\d+)`)

// urlRepoRegex 从 GitHub PR URL 中提取仓库标识
var urlRepoRegex = regexp.MustCompile(`github\.com/([^/]+/[^/]+)/pull/`)

// GitHubPlatform 实现了 platform.Platform 接口的 GitHub 版本。
type GitHubPlatform struct{}

// New 创建一个新的 GitHubPlatform 实例。
func New() *GitHubPlatform {
	return &GitHubPlatform{}
}

func (g *GitHubPlatform) Name() string {
	return "github"
}

// ghPostJSON 通过 gh api 发送 JSON POST 请求。
func ghPostJSON(endpoint string, payload []byte) ([]byte, error) {
	cmd := exec.Command("gh", "api", endpoint,
		"--method", "POST",
		"--input", "-",
		"-H", "Content-Type: application/json",
	)
	cmd.Stdin = strings.NewReader(string(payload))
	return cmd.CombinedOutput()
}

// ghPatchJSON 通过 gh api 发送 JSON PATCH 请求。
func ghPatchJSON(endpoint string, payload []byte) ([]byte, error) {
	cmd := exec.Command("gh", "api", endpoint,
		"--method", "PATCH",
		"--input", "-",
		"-H", "Content-Type: application/json",
	)
	cmd.Stdin = strings.NewReader(string(payload))
	return cmd.CombinedOutput()
}

// DetectRepoFromRemote 从 git remote URL 中检测 GitHub 仓库标识。
func (g *GitHubPlatform) DetectRepoFromRemote() (string, error) {
	out, err := exec.Command("git", "remote", "get-url", "origin").Output()
	if err != nil {
		return "", fmt.Errorf("could not detect GitHub repo from git remote: %w", err)
	}
	remoteURL := strings.TrimSpace(string(out))
	m := repoRegex.FindStringSubmatch(remoteURL)
	if m == nil {
		return "", fmt.Errorf("could not detect GitHub repo from git remote URL: %s", remoteURL)
	}
	return m[1], nil
}

// ParseMRURL 解析 GitHub PR URL，提取仓库和 PR 编号。
func (g *GitHubPlatform) ParseMRURL(url string) (repo, mrID string, err error) {
	mNum := urlPRNumberRegex.FindStringSubmatch(url)
	if len(mNum) < 2 {
		return "", "", fmt.Errorf("could not parse PR number from URL: %s", url)
	}
	mrID = mNum[1]

	mRepo := urlRepoRegex.FindStringSubmatch(url)
	if len(mRepo) > 1 {
		repo = mRepo[1]
	}
	return repo, mrID, nil
}

// BuildMRURL 构建 GitHub PR URL。
func (g *GitHubPlatform) BuildMRURL(repo, mrID string) string {
	return fmt.Sprintf("https://github.com/%s/pull/%s", repo, mrID)
}

// resolveRepo 解析仓库标识，为空时自动检测。
func (g *GitHubPlatform) resolveRepo(repo string) (string, error) {
	if repo != "" {
		return repo, nil
	}
	return g.DetectRepoFromRemote()
}

// validatePRNumber 验证 PR 编号格式。
func validatePRNumber(prNumber string) error {
	if !prNumberRegex.MatchString(prNumber) {
		return fmt.Errorf("invalid PR number: %s", prNumber)
	}
	return nil
}

// GetDiff 获取 PR 的 diff 内容。
func (g *GitHubPlatform) GetDiff(mrID, repo string) (string, error) {
	args := []string{"pr", "diff", mrID}
	if repo != "" {
		args = append(args, "--repo", repo)
	}
	out, err := exec.Command("gh", args...).Output()
	if err != nil {
		return "", fmt.Errorf("failed to get PR diff: %w", err)
	}
	return string(out), nil
}

// GetInfo 获取 PR 的标题和描述。
func (g *GitHubPlatform) GetInfo(mrID, repo string) (*platform.MRInfo, error) {
	args := []string{"pr", "view", mrID, "--json", "title,body"}
	if repo != "" {
		args = append(args, "--repo", repo)
	}
	out, err := exec.Command("gh", args...).Output()
	if err != nil {
		return nil, fmt.Errorf("failed to get PR info: %w", err)
	}
	var info struct {
		Title string `json:"title"`
		Body  string `json:"body"`
	}
	if err := json.Unmarshal(out, &info); err != nil {
		return nil, fmt.Errorf("failed to parse PR info: %w", err)
	}
	return &platform.MRInfo{
		Title:       info.Title,
		Description: info.Body,
	}, nil
}

// GetHeadCommitInfo 获取 PR 的 HEAD 提交信息。
// GitHub 仅需 HeadSHA。
func (g *GitHubPlatform) GetHeadCommitInfo(mrID, repo string) (*platform.CommitInfo, error) {
	if err := validatePRNumber(mrID); err != nil {
		return nil, err
	}
	args := []string{"pr", "view", mrID, "--json", "headRefOid", "--jq", ".headRefOid"}
	if repo != "" {
		args = append(args, "--repo", repo)
	}
	out, err := exec.Command("gh", args...).Output()
	if err != nil {
		return nil, fmt.Errorf("failed to get PR head SHA: %w", err)
	}
	return &platform.CommitInfo{
		HeadSHA: strings.TrimSpace(string(out)),
	}, nil
}

// diffFile 表示 GitHub API 返回的 PR 文件信息。
type diffFile struct {
	Filename string `json:"filename"`
	Patch    string `json:"patch"`
}

type pullComment struct {
	ID                  int    `json:"id"`
	Body                string `json:"body"`
	Path                string `json:"path"`
	Line                *int   `json:"line"`
	SubjectType         string `json:"subject_type"`
	PullRequestReviewID *int   `json:"pull_request_review_id"`
}

// issueComment 表示 GitHub issue comments API 的精简字段。
// PR 的全局评论复用 issue comments API。
type issueComment struct {
	ID   int    `json:"id"`
	Body string `json:"body"`
}

// GetChangedFiles 获取 PR 中的变更文件列表。
func (g *GitHubPlatform) GetChangedFiles(mrID, repo string) ([]platform.DiffFile, error) {
	if err := validatePRNumber(mrID); err != nil {
		return nil, err
	}
	resolvedRepo, err := g.resolveRepo(repo)
	if err != nil {
		return nil, err
	}
	out, err := exec.Command("gh", "api",
		fmt.Sprintf("repos/%s/pulls/%s/files", resolvedRepo, mrID),
		"--paginate",
	).Output()
	if err != nil {
		return nil, fmt.Errorf("failed to get PR files: %w", err)
	}

	var files []diffFile
	if err := json.Unmarshal(out, &files); err != nil {
		return nil, fmt.Errorf("failed to parse PR files: %w", err)
	}

	result := make([]platform.DiffFile, len(files))
	for i, f := range files {
		result[i] = platform.DiffFile{
			Filename: f.Filename,
			Patch:    f.Patch,
		}
	}
	return result, nil
}

// GetExistingComments 获取 PR 上已存在的评审评论。
func (g *GitHubPlatform) GetExistingComments(mrID, repo string) []platform.ExistingComment {
	resolvedRepo, err := g.resolveRepo(repo)
	if err != nil {
		return nil
	}
	var comments []platform.ExistingComment

	if pullComments, err := g.listPullComments(mrID, resolvedRepo); err == nil {
		for _, c := range pullComments {
			source := "inline"
			if c.SubjectType == "file" || c.Line == nil {
				source = "file"
			}
			comments = append(comments, newExistingComment(
				fmt.Sprintf("%d", c.ID),
				"",
				c.Path,
				c.Line,
				nil,
				c.Body,
				source,
			))
		}
	}

	if issueComments, err := g.listIssueComments(mrID, resolvedRepo); err == nil {
		for _, c := range issueComments {
			comments = append(comments, newExistingComment(
				fmt.Sprintf("%d", c.ID),
				"",
				"",
				nil,
				nil,
				c.Body,
				"global",
			))
		}
	}
	return comments
}

// PostComment 在 PR 上发布单条评论，采用三级降级策略。
func (g *GitHubPlatform) PostComment(mrID string, opts platform.PostCommentOpts) platform.CommentResult {
	if err := validatePRNumber(mrID); err != nil {
		return platform.CommentResult{Success: false, Error: err.Error()}
	}
	resolvedRepo, err := g.resolveRepo(opts.Repo)
	if err != nil {
		return platform.CommentResult{Success: false, Error: err.Error()}
	}

	// 第一级：行内评论
	if opts.Line != nil {
		payload, _ := json.Marshal(map[string]interface{}{
			"body":      opts.Body,
			"commit_id": opts.CommitInfo.HeadSHA,
			"path":      opts.Path,
			"line":      *opts.Line,
			"side":      "RIGHT",
		})
		cmd := exec.Command("gh", "api",
			fmt.Sprintf("repos/%s/pulls/%s/comments", resolvedRepo, mrID),
			"--input", "-",
		)
		cmd.Stdin = strings.NewReader(string(payload))
		if err := cmd.Run(); err == nil {
			return platform.CommentResult{Success: true, Inline: true, Mode: "inline"}
		}
	}

	// 第二级：文件级评论
	{
		lineRef := ""
		if opts.Line != nil {
			lineRef = fmt.Sprintf("**Line %d:**\n\n", *opts.Line)
		}
		payload, _ := json.Marshal(map[string]interface{}{
			"body":         lineRef + opts.Body,
			"commit_id":    opts.CommitInfo.HeadSHA,
			"path":         opts.Path,
			"subject_type": "file",
		})
		cmd := exec.Command("gh", "api",
			fmt.Sprintf("repos/%s/pulls/%s/comments", resolvedRepo, mrID),
			"--input", "-",
		)
		cmd.Stdin = strings.NewReader(string(payload))
		if err := cmd.Run(); err == nil {
			return platform.CommentResult{Success: true, Inline: false, Mode: "file"}
		}
	}

	// 第三级：全局 PR 评论
	{
		location := fmt.Sprintf("**%s**\n\n", opts.Path)
		if opts.Line != nil {
			location = fmt.Sprintf("**%s:%d**\n\n", opts.Path, *opts.Line)
		}
		body := location + opts.Body

		args := []string{"pr", "comment", mrID, "--body-file", "-"}
		if opts.Repo != "" {
			args = append(args, "--repo", opts.Repo)
		}
		cmd := exec.Command("gh", args...)
		cmd.Stdin = strings.NewReader(body)
		if err := cmd.Run(); err != nil {
			return platform.CommentResult{Success: false, Error: platform.TruncStr(err.Error(), 200)}
		}
		return platform.CommentResult{Success: true, Inline: false, Mode: "global"}
	}
}

// PostReview 批量发布已分类的评论到 PR。
func (g *GitHubPlatform) PostReview(mrID string, classified []platform.ClassifiedComment, commitInfo platform.CommitInfo, repo string) platform.ReviewResult {
	if err := validatePRNumber(mrID); err != nil {
		return platform.ReviewResult{Failed: len(classified)}
	}
	resolvedRepo, err := g.resolveRepo(repo)
	if err != nil {
		return platform.ReviewResult{Failed: len(classified)}
	}

	existing := g.GetExistingComments(mrID, repo)
	var result platform.ReviewResult

	type reviewComment struct {
		Path        string `json:"path"`
		Line        *int   `json:"line,omitempty"`
		Side        string `json:"side,omitempty"`
		Body        string `json:"body"`
		SubjectType string `json:"subject_type,omitempty"`
	}

	var reviewComments []reviewComment
	var reviewOriginals []platform.ClassifiedComment
	var globalEntries []platform.ClassifiedComment

	for _, cc := range classified {
		if platform.IsDuplicateComment(cc.Input, existing) {
			result.Skipped++
			continue
		}

		switch cc.Mode {
		case "inline":
			reviewComments = append(reviewComments, reviewComment{
				Path: cc.Input.Path,
				Line: cc.Input.Line,
				Side: "RIGHT",
				Body: cc.Input.Body,
			})
			reviewOriginals = append(reviewOriginals, cc)
		case "file":
			lineRef := ""
			if cc.Input.Line != nil {
				lineRef = fmt.Sprintf("**Line %d:**\n\n", *cc.Input.Line)
			}
			reviewComments = append(reviewComments, reviewComment{
				Path:        cc.Input.Path,
				Body:        lineRef + cc.Input.Body,
				SubjectType: "file",
			})
			reviewOriginals = append(reviewOriginals, cc)
		default:
			globalEntries = append(globalEntries, cc)
		}
	}

	// 批量提交行内和文件级评论
	if len(reviewComments) > 0 {
		payload, _ := json.Marshal(map[string]interface{}{
			"commit_id": commitInfo.HeadSHA,
			"event":     "COMMENT",
			"comments":  reviewComments,
		})
		cmd := exec.Command("gh", "api",
			fmt.Sprintf("repos/%s/pulls/%s/reviews", resolvedRepo, mrID),
			"--input", "-",
		)
		cmd.Stdin = strings.NewReader(string(payload))
		if err := cmd.Run(); err == nil {
			for _, orig := range reviewOriginals {
				result.Posted++
				if orig.Mode == "inline" {
					result.Inline++
				} else {
					result.FileLevel++
				}
			}
		} else {
			// 降级为逐条发布
			for _, orig := range reviewOriginals {
				cr := g.PostComment(mrID, platform.PostCommentOpts{
					Path:       orig.Input.Path,
					Line:       orig.Input.Line,
					Body:       orig.Input.Body,
					CommitInfo: commitInfo,
					Repo:       repo,
				})
				if cr.Success {
					result.Posted++
					switch cr.Mode {
					case "inline":
						result.Inline++
					case "file":
						result.FileLevel++
					default:
						result.Global++
					}
				} else {
					result.Failed++
				}
			}
		}
	}

	// 逐条发布全局评论
	for _, cc := range globalEntries {
		location := fmt.Sprintf("**%s**\n\n", cc.Input.Path)
		if cc.Input.Line != nil {
			location = fmt.Sprintf("**%s:%d**\n\n", cc.Input.Path, *cc.Input.Line)
		}
		body := location + cc.Input.Body

		args := []string{"pr", "comment", mrID, "--body-file", "-"}
		if repo != "" {
			args = append(args, "--repo", repo)
		}
		cmd := exec.Command("gh", args...)
		cmd.Stdin = strings.NewReader(body)
		if err := cmd.Run(); err == nil {
			result.Posted++
			result.Global++
		} else {
			result.Failed++
		}
	}

	util.Debugf("PostReview final result: posted=%d inline=%d file=%d global=%d failed=%d skipped=%d",
		result.Posted, result.Inline, result.FileLevel, result.Global, result.Failed, result.Skipped)
	return result
}

// PostIssuesAsComments 将代码审查问题发布为 PR 评论的主入口。
func (g *GitHubPlatform) PostIssuesAsComments(mrID string, issues []platform.IssueForComment, repo string) platform.ReviewResult {
	commitInfo, err := g.GetHeadCommitInfo(mrID, repo)
	if err != nil {
		return platform.ReviewResult{Failed: len(issues)}
	}
	runID := platform.NewLifecycleRunID(commitInfo.HeadSHA)

	comments := make([]platform.ReviewCommentInput, 0, len(issues))
	for _, issue := range issues {
		body := platform.FormatIssueBodyWithMeta(issue, runID, commitInfo.HeadSHA)
		comments = append(comments, platform.ReviewCommentInput{
			Path: issue.File,
			Line: issue.Line,
			Body: body,
		})
	}

	// 获取 diff 信息并分类评论
	changedFiles, err := g.GetChangedFiles(mrID, repo)
	if err != nil {
		return platform.ReviewResult{Failed: len(issues)}
	}

	diffInfo := make(map[string]map[int]bool)
	for _, f := range changedFiles {
		if f.Patch != "" {
			diffInfo[f.Filename] = platform.ParseDiffLines(f.Patch)
		} else {
			diffInfo[f.Filename] = make(map[int]bool)
		}
	}

	classified := platform.ClassifyCommentsByDiff(comments, diffInfo)
	desired := platform.BuildDesiredComments(classified, runID, commitInfo.HeadSHA)
	existing := g.GetExistingComments(mrID, repo)
	plan := platform.PlanLifecycle(existing, desired)
	return g.applyLifecyclePlan(mrID, repo, *commitInfo, existing, plan, runID)
}

// PostNote 在 PR 上发布一条全局 note（评论）。
func (g *GitHubPlatform) PostNote(mrID, repo, body string) error {
	if err := validatePRNumber(mrID); err != nil {
		return err
	}
	resolvedRepo, err := g.resolveRepo(repo)
	if err != nil {
		return err
	}

	payload, _ := json.Marshal(map[string]string{"body": body})
	endpoint := fmt.Sprintf("repos/%s/issues/%s/comments", resolvedRepo, mrID)
	if _, err := ghPostJSON(endpoint, payload); err != nil {
		return fmt.Errorf("failed to post PR note: %w", err)
	}
	return nil
}

// UpsertSummaryNote upsert CodeDebate 的总结 note（通过 marker 匹配已有 note）。
// 找到 marker 则更新该 note，否则创建新 note。
func (g *GitHubPlatform) UpsertSummaryNote(mrID, repo, marker, body string) error {
	if err := validatePRNumber(mrID); err != nil {
		return err
	}
	resolvedRepo, err := g.resolveRepo(repo)
	if err != nil {
		return err
	}

	comments, err := g.listIssueComments(mrID, resolvedRepo)
	if err != nil {
		return fmt.Errorf("failed to list PR notes: %w", err)
	}

	if commentID := findLatestIssueCommentIDByMarker(comments, marker); commentID > 0 {
		payload, _ := json.Marshal(map[string]string{"body": body})
		endpoint := fmt.Sprintf("repos/%s/issues/comments/%d", resolvedRepo, commentID)
		if _, err := ghPatchJSON(endpoint, payload); err != nil {
			return fmt.Errorf("failed to update summary note: %w", err)
		}
		return nil
	}

	return g.PostNote(mrID, resolvedRepo, body)
}

// listIssueComments 获取 PR 的 issue comments（用于 summary upsert）。
func (g *GitHubPlatform) listIssueComments(mrID, resolvedRepo string) ([]issueComment, error) {
	out, err := exec.Command("gh", "api",
		fmt.Sprintf("repos/%s/issues/%s/comments", resolvedRepo, mrID),
		"--paginate",
		"--jq", ".[] | @base64",
	).Output()
	if err != nil {
		return nil, err
	}

	lines := strings.Split(strings.TrimSpace(string(out)), "\n")
	comments := make([]issueComment, 0, len(lines))
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		raw, err := base64.StdEncoding.DecodeString(line)
		if err != nil {
			return nil, fmt.Errorf("failed to decode issue comment row: %w", err)
		}
		var c issueComment
		if err := json.Unmarshal(raw, &c); err != nil {
			return nil, fmt.Errorf("failed to parse issue comment row: %w", err)
		}
		comments = append(comments, c)
	}
	return comments, nil
}

// findLatestIssueCommentIDByMarker 返回包含 marker 的最新 comment ID（按 ID 最大值）。
func findLatestIssueCommentIDByMarker(comments []issueComment, marker string) int {
	if marker == "" {
		return 0
	}
	latestID := 0
	for _, c := range comments {
		if c.ID > latestID && strings.Contains(c.Body, marker) {
			latestID = c.ID
		}
	}
	return latestID
}

func (g *GitHubPlatform) listPullComments(mrID, resolvedRepo string) ([]pullComment, error) {
	out, err := exec.Command("gh", "api",
		fmt.Sprintf("repos/%s/pulls/%s/comments", resolvedRepo, mrID),
		"--paginate",
		"--jq", ".[] | @base64",
	).Output()
	if err != nil {
		return nil, err
	}

	lines := strings.Split(strings.TrimSpace(string(out)), "\n")
	comments := make([]pullComment, 0, len(lines))
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		raw, err := base64.StdEncoding.DecodeString(line)
		if err != nil {
			return nil, fmt.Errorf("failed to decode pull comment row: %w", err)
		}
		var c pullComment
		if err := json.Unmarshal(raw, &c); err != nil {
			return nil, fmt.Errorf("failed to parse pull comment row: %w", err)
		}
		comments = append(comments, c)
	}
	return comments, nil
}

func newExistingComment(id, threadID, path string, line, oldLine *int, body, source string) platform.ExistingComment {
	meta, _ := platform.ParseCodeDebateMeta(body)
	return platform.ExistingComment{
		ID:       id,
		ThreadID: threadID,
		Path:     path,
		Line:     line,
		OldLine:  oldLine,
		Body:     body,
		Source:   source,
		IsCodeDebate:  platform.IsCodeDebateCommentBody(body),
		Meta:     meta,
	}
}

func (g *GitHubPlatform) applyLifecyclePlan(mrID, repo string, commitInfo platform.CommitInfo, existing []platform.ExistingComment, plan platform.LifecyclePlan, runID string) platform.ReviewResult {
	var result platform.ReviewResult
	result.Unchanged = len(plan.Noop)
	result.Skipped += len(plan.Noop)

	for _, item := range plan.Resolve {
		if err := g.updateExistingComment(repo, item.Existing, platform.RenderResolvedBody(item.Existing, runID, commitInfo.HeadSHA)); err != nil {
			result.Failed++
		} else {
			result.Resolved++
		}
	}

	for _, item := range plan.Supersede {
		if err := g.updateExistingComment(repo, item.Existing, platform.RenderSupersededBody(item.Existing, item.Desired, runID, commitInfo.HeadSHA)); err != nil {
			result.Failed++
		} else {
			result.Superseded++
		}
	}

	for _, item := range plan.Update {
		if err := g.updateExistingComment(repo, item.Existing, item.Desired.Body); err != nil {
			result.Failed++
		} else {
			result.Updated++
		}
	}

	for _, item := range plan.Create {
		duplicateCandidates := platform.DuplicateCandidates(existing, item.IssueKey)
		if platform.IsDuplicateComment(platform.ReviewCommentInput{Path: item.Path, Line: item.Line, Body: item.Body}, duplicateCandidates) {
			result.Skipped++
			continue
		}
		cr := g.createDesiredComment(mrID, repo, commitInfo, item)
		if !cr.Success {
			result.Failed++
			continue
		}
		result.Posted++
		switch cr.Mode {
		case "inline":
			result.Inline++
		case "file":
			result.FileLevel++
		default:
			result.Global++
		}

		meta, _ := platform.ParseCodeDebateMeta(item.Body)
		existing = append(existing, platform.ExistingComment{
			Path:    item.Path,
			Line:    item.Line,
			OldLine: item.OldLine,
			Body:    item.Body,
			Source:  item.Source,
			IsCodeDebate: true,
			Meta:    meta,
		})
	}

	return result
}

func (g *GitHubPlatform) createDesiredComment(mrID, repo string, commitInfo platform.CommitInfo, desired platform.DesiredComment) platform.CommentResult {
	if err := validatePRNumber(mrID); err != nil {
		return platform.CommentResult{Success: false, Error: err.Error()}
	}
	resolvedRepo, err := g.resolveRepo(repo)
	if err != nil {
		return platform.CommentResult{Success: false, Error: err.Error()}
	}

	switch desired.Source {
	case "inline":
		if desired.Line == nil {
			return platform.CommentResult{Success: false, Error: "inline comment missing line"}
		}
		payload, _ := json.Marshal(map[string]interface{}{
			"body":      desired.Body,
			"commit_id": commitInfo.HeadSHA,
			"path":      desired.Path,
			"line":      *desired.Line,
			"side":      "RIGHT",
		})
		if _, err := ghPostJSON(fmt.Sprintf("repos/%s/pulls/%s/comments", resolvedRepo, mrID), payload); err != nil {
			return platform.CommentResult{Success: false, Error: platform.TruncStr(err.Error(), 200)}
		}
		return platform.CommentResult{Success: true, Inline: true, Mode: "inline"}
	case "file":
		payload, _ := json.Marshal(map[string]interface{}{
			"body":         desired.Body,
			"commit_id":    commitInfo.HeadSHA,
			"path":         desired.Path,
			"subject_type": "file",
		})
		if _, err := ghPostJSON(fmt.Sprintf("repos/%s/pulls/%s/comments", resolvedRepo, mrID), payload); err != nil {
			return platform.CommentResult{Success: false, Error: platform.TruncStr(err.Error(), 200)}
		}
		return platform.CommentResult{Success: true, Inline: false, Mode: "file"}
	default:
		payload, _ := json.Marshal(map[string]string{"body": desired.Body})
		if _, err := ghPostJSON(fmt.Sprintf("repos/%s/issues/%s/comments", resolvedRepo, mrID), payload); err != nil {
			return platform.CommentResult{Success: false, Error: platform.TruncStr(err.Error(), 200)}
		}
		return platform.CommentResult{Success: true, Inline: false, Mode: "global"}
	}
}

func (g *GitHubPlatform) updateExistingComment(repo string, existing platform.ExistingComment, body string) error {
	resolvedRepo, err := g.resolveRepo(repo)
	if err != nil {
		return err
	}
	if existing.ID == "" {
		return fmt.Errorf("missing comment id")
	}

	payload, _ := json.Marshal(map[string]string{"body": body})
	endpoint := fmt.Sprintf("repos/%s/pulls/comments/%s", resolvedRepo, existing.ID)
	if existing.Source == "global" {
		endpoint = fmt.Sprintf("repos/%s/issues/comments/%s", resolvedRepo, existing.ID)
	}
	if _, err := ghPatchJSON(endpoint, payload); err != nil {
		return fmt.Errorf("failed to update comment: %w", err)
	}
	return nil
}

// GetMRDetails 获取单个 PR 的详细信息，用于历史 PR 关联。
func (g *GitHubPlatform) GetMRDetails(mrNumber int, cwd string) (*platform.MRDetail, error) {
	cmd := exec.Command("gh", "pr", "view", fmt.Sprintf("%d", mrNumber),
		"--json", "number,title,author,mergedAt,files",
	)
	cmd.Dir = cwd
	out, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("failed to get PR details: %w", err)
	}

	var data struct {
		Number   int    `json:"number"`
		Title    string `json:"title"`
		MergedAt string `json:"mergedAt"`
		Author   struct {
			Login string `json:"login"`
		} `json:"author"`
		Files []struct {
			Path string `json:"path"`
		} `json:"files"`
	}
	if err := json.Unmarshal(out, &data); err != nil {
		return nil, fmt.Errorf("failed to parse PR details: %w", err)
	}

	files := make([]string, len(data.Files))
	for i, f := range data.Files {
		files[i] = f.Path
	}

	return &platform.MRDetail{
		Number:   data.Number,
		Title:    data.Title,
		Author:   data.Author.Login,
		MergedAt: data.MergedAt,
		Files:    files,
	}, nil
}

// GetMRsForCommit 通过 GitHub API 查找与指定 commit SHA 关联的 PR 编号列表。
func (g *GitHubPlatform) GetMRsForCommit(commitSHA string, cwd string) ([]int, error) {
	resolvedRepo, err := g.resolveRepo("")
	if err != nil {
		return nil, err
	}

	cmd := exec.Command("gh", "api",
		fmt.Sprintf("repos/%s/commits/%s/pulls", resolvedRepo, commitSHA),
		"--jq", ".[].number",
	)
	cmd.Dir = cwd
	out, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("failed to get PRs for commit %s: %w", commitSHA, err)
	}

	var numbers []int
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		n, err := strconv.Atoi(line)
		if err == nil && n > 0 {
			numbers = append(numbers, n)
		}
	}
	return numbers, nil
}
