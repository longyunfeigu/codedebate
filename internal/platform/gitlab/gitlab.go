// Package gitlab 实现了 GitLab 平台的 Platform 接口。
// 通过 glab CLI 工具与 GitLab API 交互。
package gitlab

import (
	"encoding/json"
	"fmt"
	"net/url"
	"os/exec"
	"regexp"
	"strings"

	"github.com/longyunfeigu/codedebate/internal/platform"
	"github.com/longyunfeigu/codedebate/internal/util"
)

// mrURLRegex 从 GitLab MR URL 中提取项目路径和 MR IID
var mrURLRegex = regexp.MustCompile(`(?:https?://[^/]+/)?(.+?)/-/merge_requests/(\d+)`)

// remoteRegex 从 git remote URL 中提取 GitLab 项目路径
var remoteRegex = regexp.MustCompile(`[:/](.+?)(?:\.git)?$`)

// GitLabPlatform 实现了 platform.Platform 接口的 GitLab 版本。
type GitLabPlatform struct {
	host string // 自托管 GitLab 域名，为空表示 gitlab.com
}

// New 创建一个新的 GitLabPlatform 实例。
func New(host string) *GitLabPlatform {
	return &GitLabPlatform{host: host}
}

func (g *GitLabPlatform) Name() string {
	return "gitlab"
}

// getHost 返回 GitLab 主机地址。
func (g *GitLabPlatform) getHost() string {
	if g.host != "" {
		return g.host
	}
	return "gitlab.com"
}

// glabHostname 返回用于 glab --hostname 参数的纯主机名（去掉 scheme）。
// 例如 "http://39.99.155.169" → "39.99.155.169"，"gitlab.com" → "gitlab.com"
func (g *GitLabPlatform) glabHostname() string {
	h := g.getHost()
	if u, err := url.Parse(h); err == nil && u.Host != "" {
		return u.Host
	}
	return h
}

// glabCmd 创建一个自动附带 --hostname 的 glab 命令（仅自托管 GitLab 时附加）。
func (g *GitLabPlatform) glabCmd(args ...string) *exec.Cmd {
	hostname := g.glabHostname()
	if hostname != "" && hostname != "gitlab.com" {
		args = append(args, "--hostname", hostname)
	}
	return exec.Command("glab", args...)
}

// DetectRepoFromRemote 从 git remote URL 中检测 GitLab 项目路径。
// 支持嵌套组路径（如 group/subgroup/project）。
func (g *GitLabPlatform) DetectRepoFromRemote() (string, error) {
	out, err := exec.Command("git", "remote", "get-url", "origin").Output()
	if err != nil {
		return "", fmt.Errorf("could not detect GitLab repo from git remote: %w", err)
	}
	remoteURL := strings.TrimSpace(string(out))

	// 匹配 SSH (git@host:path) 或 HTTPS (host/path) 格式
	m := remoteRegex.FindStringSubmatch(remoteURL)
	if m == nil {
		return "", fmt.Errorf("could not detect GitLab repo from git remote URL: %s", remoteURL)
	}
	return m[1], nil
}

// ParseMRURL 解析 GitLab MR URL，提取项目路径和 MR IID。
func (g *GitLabPlatform) ParseMRURL(rawURL string) (repo, mrID string, err error) {
	m := mrURLRegex.FindStringSubmatch(rawURL)
	if len(m) < 3 {
		return "", "", fmt.Errorf("could not parse MR URL: %s", rawURL)
	}
	return m[1], m[2], nil
}

// BuildMRURL 构建 GitLab MR URL。
func (g *GitLabPlatform) BuildMRURL(repo, mrID string) string {
	return fmt.Sprintf("https://%s/%s/-/merge_requests/%s", g.getHost(), repo, mrID)
}

// encodeProject 对 GitLab 项目路径进行 URL 编码（用于 API 调用）。
func encodeProject(repo string) string {
	return url.PathEscape(repo)
}

// glabPostJSON 通过 glab api 发送 JSON POST 请求。
// 必须设置 Content-Type: application/json，否则 glab 默认不设 Content-Type，
// 导致 GitLab 返回 415 或将 position 中的整数字段当作字符串处理（inline comment 失效）。
func (g *GitLabPlatform) glabPostJSON(endpoint string, payload []byte) ([]byte, error) {
	cmd := g.glabCmd("api", endpoint,
		"--method", "POST",
		"--input", "-",
		"-H", "Content-Type: application/json",
	)
	cmd.Stdin = strings.NewReader(string(payload))
	return cmd.CombinedOutput()
}

// glabPutJSON 通过 glab api 发送 JSON PUT 请求。
func (g *GitLabPlatform) glabPutJSON(endpoint string, payload []byte) ([]byte, error) {
	cmd := g.glabCmd("api", endpoint,
		"--method", "PUT",
		"--input", "-",
		"-H", "Content-Type: application/json",
	)
	cmd.Stdin = strings.NewReader(string(payload))
	return cmd.CombinedOutput()
}

// GetDiff 获取 MR 的 diff 内容。
// 有 repo 参数时用 glab api（server 模式），否则用 glab mr（CLI 模式，向后兼容）。
func (g *GitLabPlatform) GetDiff(mrID, repo string) (string, error) {
	if repo != "" {
		return g.getDiffViaAPI(mrID, repo)
	}
	out, err := g.glabCmd("mr", "diff", mrID).Output()
	if err != nil {
		return "", fmt.Errorf("failed to get MR diff: %w", err)
	}
	return string(out), nil
}

// getDiffViaAPI 通过 glab api 获取 MR diff（不需要 git 上下文）。
func (g *GitLabPlatform) getDiffViaAPI(mrID, repo string) (string, error) {
	encoded := encodeProject(repo)
	out, err := g.glabCmd("api",
		fmt.Sprintf("projects/%s/merge_requests/%s/diffs", encoded, mrID),
	).Output()
	if err != nil {
		return "", fmt.Errorf("failed to get MR diff via API: %w", err)
	}

	var diffs []struct {
		OldPath string `json:"old_path"`
		NewPath string `json:"new_path"`
		Diff    string `json:"diff"`
	}
	if err := json.Unmarshal(out, &diffs); err != nil {
		return "", fmt.Errorf("failed to parse MR diff: %w", err)
	}

	var sb strings.Builder
	for _, d := range diffs {
		sb.WriteString(fmt.Sprintf("--- a/%s\n+++ b/%s\n%s\n", d.OldPath, d.NewPath, d.Diff))
	}
	return sb.String(), nil
}

// GetInfo 获取 MR 的标题和描述。
// 有 repo 参数时用 glab api（server 模式），否则用 glab mr（CLI 模式，向后兼容）。
func (g *GitLabPlatform) GetInfo(mrID, repo string) (*platform.MRInfo, error) {
	if repo != "" {
		return g.getInfoViaAPI(mrID, repo)
	}
	out, err := g.glabCmd("mr", "view", mrID, "--output", "json").Output()
	if err != nil {
		return nil, fmt.Errorf("failed to get MR info: %w", err)
	}
	var info struct {
		Title       string `json:"title"`
		Description string `json:"description"`
	}
	if err := json.Unmarshal(out, &info); err != nil {
		return nil, fmt.Errorf("failed to parse MR info: %w", err)
	}
	return &platform.MRInfo{
		Title:       info.Title,
		Description: info.Description,
	}, nil
}

// getInfoViaAPI 通过 glab api 获取 MR 信息（不需要 git 上下文）。
func (g *GitLabPlatform) getInfoViaAPI(mrID, repo string) (*platform.MRInfo, error) {
	encoded := encodeProject(repo)
	out, err := g.glabCmd("api",
		fmt.Sprintf("projects/%s/merge_requests/%s", encoded, mrID),
	).Output()
	if err != nil {
		return nil, fmt.Errorf("failed to get MR info via API: %w", err)
	}
	var info struct {
		Title       string `json:"title"`
		Description string `json:"description"`
	}
	if err := json.Unmarshal(out, &info); err != nil {
		return nil, fmt.Errorf("failed to parse MR info: %w", err)
	}
	return &platform.MRInfo{
		Title:       info.Title,
		Description: info.Description,
	}, nil
}

// GetHeadCommitInfo 获取 MR 的 HEAD 提交信息。
// GitLab 需要 3 个 SHA（head_sha, base_sha, start_sha）用于 Discussions API。
// 有 repo 参数时用 glab api（server 模式），否则用 glab mr（CLI 模式，向后兼容）。
func (g *GitLabPlatform) GetHeadCommitInfo(mrID, repo string) (*platform.CommitInfo, error) {
	if repo != "" {
		return g.getHeadCommitInfoViaAPI(mrID, repo)
	}
	out, err := g.glabCmd("mr", "view", mrID, "--output", "json").Output()
	if err != nil {
		return nil, fmt.Errorf("failed to get MR commit info: %w", err)
	}
	return parseCommitInfo(out)
}

// getHeadCommitInfoViaAPI 通过 glab api 获取 MR 提交信息（不需要 git 上下文）。
func (g *GitLabPlatform) getHeadCommitInfoViaAPI(mrID, repo string) (*platform.CommitInfo, error) {
	encoded := encodeProject(repo)
	out, err := g.glabCmd("api",
		fmt.Sprintf("projects/%s/merge_requests/%s", encoded, mrID),
	).Output()
	if err != nil {
		return nil, fmt.Errorf("failed to get MR commit info via API: %w", err)
	}
	return parseCommitInfo(out)
}

// parseCommitInfo 从 MR JSON 响应中解析 diff_refs 提交信息。
func parseCommitInfo(data []byte) (*platform.CommitInfo, error) {
	var result struct {
		DiffRefs struct {
			HeadSha  string `json:"head_sha"`
			BaseSha  string `json:"base_sha"`
			StartSha string `json:"start_sha"`
		} `json:"diff_refs"`
		SHA string `json:"sha"`
	}
	if err := json.Unmarshal(data, &result); err != nil {
		return nil, fmt.Errorf("failed to parse MR commit info: %w", err)
	}

	headSHA := result.DiffRefs.HeadSha
	if headSHA == "" {
		headSHA = result.SHA
	}

	return &platform.CommitInfo{
		HeadSHA:  headSHA,
		BaseSHA:  result.DiffRefs.BaseSha,
		StartSHA: result.DiffRefs.StartSha,
	}, nil
}

// GetChangedFiles 获取 MR 中的变更文件列表。
func (g *GitLabPlatform) GetChangedFiles(mrID, repo string) ([]platform.DiffFile, error) {
	resolvedRepo, err := g.resolveRepo(repo)
	if err != nil {
		return nil, err
	}
	encodedProject := encodeProject(resolvedRepo)

	out, err := g.glabCmd("api",
		fmt.Sprintf("projects/%s/merge_requests/%s/diffs", encodedProject, mrID),
	).Output()
	if err != nil {
		return nil, fmt.Errorf("failed to get MR diffs: %w", err)
	}

	var diffs []struct {
		NewPath string `json:"new_path"`
		Diff    string `json:"diff"`
	}
	if err := json.Unmarshal(out, &diffs); err != nil {
		return nil, fmt.Errorf("failed to parse MR diffs: %w", err)
	}

	result := make([]platform.DiffFile, len(diffs))
	for i, d := range diffs {
		result[i] = platform.DiffFile{
			Filename: d.NewPath,
			Patch:    d.Diff,
		}
	}
	return result, nil
}

// resolveRepo 解析项目路径，为空时自动检测。
func (g *GitLabPlatform) resolveRepo(repo string) (string, error) {
	if repo != "" {
		return repo, nil
	}
	return g.DetectRepoFromRemote()
}

// GetExistingComments 获取 MR 上已存在的评论（包括 discussions）。
func (g *GitLabPlatform) GetExistingComments(mrID, repo string) []platform.ExistingComment {
	resolvedRepo, err := g.resolveRepo(repo)
	if err != nil {
		return nil
	}
	encodedProject := encodeProject(resolvedRepo)

	out, err := g.glabCmd("api",
		fmt.Sprintf("projects/%s/merge_requests/%s/discussions", encodedProject, mrID),
	).Output()
	var comments []platform.ExistingComment
	if err == nil {
		var discussions []struct {
			ID    string `json:"id"`
			Notes []struct {
				ID       int    `json:"id"`
				Body     string `json:"body"`
				Position *struct {
					PositionType string `json:"position_type"`
					NewPath      string `json:"new_path"`
					NewLine      *int   `json:"new_line"`
					OldLine      *int   `json:"old_line"`
				} `json:"position"`
			} `json:"notes"`
		}
		if json.Unmarshal(out, &discussions) == nil {
			for _, d := range discussions {
				for _, note := range d.Notes {
					source := "global"
					path := ""
					var line *int
					var oldLine *int
					if note.Position != nil {
						path = note.Position.NewPath
						line = note.Position.NewLine
						oldLine = note.Position.OldLine
						source = "file"
						if note.Position.PositionType == "text" || note.Position.NewLine != nil {
							source = "inline"
						}
					}
					comments = append(comments, newGitLabExistingComment(
						fmt.Sprintf("%d", note.ID),
						d.ID,
						path,
						line,
						oldLine,
						note.Body,
						source,
					))
				}
			}
		}
	}

	notesOut, err := g.glabCmd("api",
		fmt.Sprintf("projects/%s/merge_requests/%s/notes", encodedProject, mrID),
	).Output()
	if err == nil {
		var notes []struct {
			ID   int    `json:"id"`
			Body string `json:"body"`
		}
		if json.Unmarshal(notesOut, &notes) == nil {
			for _, note := range notes {
				comments = append(comments, newGitLabExistingComment(
					fmt.Sprintf("%d", note.ID),
					"",
					"",
					nil,
					nil,
					note.Body,
					"global",
				))
			}
		}
	}

	return comments
}

// PostComment 在 MR 上发布单条评论，采用三级降级策略：
// 1. Discussions API 行内评论
// 2. Discussions API 文件级评论
// 3. glab mr note 全局评论
func (g *GitLabPlatform) PostComment(mrID string, opts platform.PostCommentOpts) platform.CommentResult {
	resolvedRepo, err := g.resolveRepo(opts.Repo)
	if err != nil {
		return platform.CommentResult{Success: false, Error: err.Error()}
	}
	encodedProject := encodeProject(resolvedRepo)

	// 第一级：行内评论（通过 Discussions API，使用 JSON body 确保 new_line 为整数类型）
	if opts.Line != nil && opts.CommitInfo.HeadSHA != "" {
		endpoint := fmt.Sprintf("projects/%s/merge_requests/%s/discussions", encodedProject, mrID)
		payload, _ := json.Marshal(map[string]interface{}{
			"body":     opts.Body,
			"position": buildTextPosition(opts.Path, *opts.Line, opts.OldLine, opts.CommitInfo),
		})
		out, err := g.glabPostJSON(endpoint, payload)
		if err == nil {
			util.Debugf("PostComment success mode=inline path=%s line=%d", opts.Path, *opts.Line)
			return platform.CommentResult{Success: true, Inline: true, Mode: "inline"}
		}
		util.Debugf("PostComment inline failed for %s:%d: %v | response: %s", opts.Path, *opts.Line, err, string(out))
	}

	// 第二级：文件级评论（通过 Discussions API，position_type: file，使用 JSON body）
	if opts.CommitInfo.HeadSHA != "" {
		lineRef := ""
		if opts.Line != nil {
			lineRef = fmt.Sprintf("**Line %d:**\n\n", *opts.Line)
		}
		endpoint := fmt.Sprintf("projects/%s/merge_requests/%s/discussions", encodedProject, mrID)
		payload, _ := json.Marshal(map[string]interface{}{
			"body":     lineRef + opts.Body,
			"position": buildFilePosition(opts.Path, opts.CommitInfo),
		})
		out, err := g.glabPostJSON(endpoint, payload)
		if err == nil {
			util.Debugf("PostComment success mode=file path=%s", opts.Path)
			return platform.CommentResult{Success: true, Inline: false, Mode: "file"}
		}
		util.Debugf("PostComment file-level failed for %s: %v | response: %s", opts.Path, err, string(out))
	}

	// 第三级：全局评论（通过 glab mr note）
	{
		location := fmt.Sprintf("**%s**\n\n", opts.Path)
		if opts.Line != nil {
			location = fmt.Sprintf("**%s:%d**\n\n", opts.Path, *opts.Line)
		}
		body := location + opts.Body

		cmd := g.glabCmd("mr", "note", mrID, "--unique", "--message", body)
		if err := cmd.Run(); err != nil {
			return platform.CommentResult{Success: false, Error: platform.TruncStr(err.Error(), 200)}
		}
		util.Debugf("PostComment success mode=global path=%s", opts.Path)
		return platform.CommentResult{Success: true, Inline: false, Mode: "global"}
	}
}

func buildTextPosition(path string, line int, oldLine *int, commitInfo platform.CommitInfo) map[string]interface{} {
	pos := map[string]interface{}{
		"position_type": "text",
		"new_path":      path,
		"old_path":      path,
		"new_line":      line,
		"head_sha":      commitInfo.HeadSHA,
		"base_sha":      commitInfo.BaseSHA,
		"start_sha":     commitInfo.StartSHA,
	}
	// context 行需要同时设置 old_line，否则 GitLab 无法计算 line_code
	if oldLine != nil && *oldLine > 0 {
		pos["old_line"] = *oldLine
	}
	return pos
}

func buildFilePosition(path string, commitInfo platform.CommitInfo) map[string]interface{} {
	return map[string]interface{}{
		"position_type": "file",
		"new_path":      path,
		"old_path":      path,
		"head_sha":      commitInfo.HeadSHA,
		"base_sha":      commitInfo.BaseSHA,
		"start_sha":     commitInfo.StartSHA,
	}
}

// PostReview 批量发布已分类的评论到 MR。
// 先尝试 Draft Notes API + bulk_publish，失败则降级为逐条 Discussions API。
func (g *GitLabPlatform) PostReview(mrID string, classified []platform.ClassifiedComment, commitInfo platform.CommitInfo, repo string) platform.ReviewResult {
	resolvedRepo, err := g.resolveRepo(repo)
	if err != nil {
		return platform.ReviewResult{Failed: len(classified)}
	}
	encodedProject := encodeProject(resolvedRepo)

	existing := g.GetExistingComments(mrID, repo)
	var result platform.ReviewResult

	var inlineEntries []platform.ClassifiedComment
	var fileEntries []platform.ClassifiedComment
	var globalEntries []platform.ClassifiedComment

	for _, cc := range classified {
		if platform.IsDuplicateComment(cc.Input, existing) {
			result.Skipped++
			continue
		}
		switch cc.Mode {
		case "inline":
			inlineEntries = append(inlineEntries, cc)
		case "file":
			fileEntries = append(fileEntries, cc)
		default:
			globalEntries = append(globalEntries, cc)
		}
	}

	// 尝试使用 Draft Notes API 批量提交（需要 GitLab Premium）
	draftNotesSucceeded := false
	if len(inlineEntries)+len(fileEntries) > 0 {
		draftNotesSucceeded = g.tryDraftNotes(encodedProject, mrID, commitInfo, inlineEntries, fileEntries, &result)
	}

	// 如果 Draft Notes API 不可用，降级为逐条 Discussions API
	if !draftNotesSucceeded {
		for _, cc := range inlineEntries {
			cr := g.PostComment(mrID, platform.PostCommentOpts{
				Path:       cc.Input.Path,
				Line:       cc.Input.Line,
				OldLine:    cc.OldLine,
				Body:       cc.Input.Body,
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
		for _, cc := range fileEntries {
			lineRef := ""
			if cc.Input.Line != nil {
				lineRef = fmt.Sprintf("**Line %d:**\n\n", *cc.Input.Line)
			}
			cr := g.PostComment(mrID, platform.PostCommentOpts{
				Path:       cc.Input.Path,
				Body:       lineRef + cc.Input.Body,
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

	// 逐条发布全局评论
	for _, cc := range globalEntries {
		location := fmt.Sprintf("**%s**\n\n", cc.Input.Path)
		if cc.Input.Line != nil {
			location = fmt.Sprintf("**%s:%d**\n\n", cc.Input.Path, *cc.Input.Line)
		}
		body := location + cc.Input.Body

		cmd := g.glabCmd("mr", "note", mrID, "--unique", "--message", body)
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

// tryDraftNotes 尝试使用 Draft Notes API 批量提交评论。
// 成功返回 true，失败（如 GitLab 版本不支持）返回 false。
func (g *GitLabPlatform) tryDraftNotes(encodedProject, mrID string, commitInfo platform.CommitInfo, inlineEntries, fileEntries []platform.ClassifiedComment, result *platform.ReviewResult) bool {
	var draftIDs []string

	// 创建 draft notes
	for _, cc := range inlineEntries {
		if cc.Input.Line == nil {
			return false
		}
		payload, _ := json.Marshal(map[string]interface{}{
			"note":     cc.Input.Body,
			"position": buildTextPosition(cc.Input.Path, *cc.Input.Line, cc.OldLine, commitInfo),
		})
		endpoint := fmt.Sprintf("projects/%s/merge_requests/%s/draft_notes", encodedProject, mrID)
		out, err := g.glabPostJSON(endpoint, payload)
		if err != nil {
			return false // Draft Notes API 不可用
		}
		var resp struct {
			ID int `json:"id"`
		}
		if json.Unmarshal(out, &resp) == nil {
			draftIDs = append(draftIDs, fmt.Sprintf("%d", resp.ID))
		}
	}

	for _, cc := range fileEntries {
		lineRef := ""
		if cc.Input.Line != nil {
			lineRef = fmt.Sprintf("**Line %d:**\n\n", *cc.Input.Line)
		}
		payload, _ := json.Marshal(map[string]interface{}{
			"note":     lineRef + cc.Input.Body,
			"position": buildFilePosition(cc.Input.Path, commitInfo),
		})
		endpoint := fmt.Sprintf("projects/%s/merge_requests/%s/draft_notes", encodedProject, mrID)
		out, err := g.glabPostJSON(endpoint, payload)
		if err != nil {
			return false
		}
		var resp struct {
			ID int `json:"id"`
		}
		if json.Unmarshal(out, &resp) == nil {
			draftIDs = append(draftIDs, fmt.Sprintf("%d", resp.ID))
		}
	}

	// 批量发布所有 draft notes
	if len(draftIDs) == 0 {
		util.Warnf("tryDraftNotes: no draft note IDs returned, fallback to Discussions API")
		return false
	}

	cmd := g.glabCmd("api",
		fmt.Sprintf("projects/%s/merge_requests/%s/draft_notes/bulk_publish", encodedProject, mrID),
		"--method", "POST",
	)
	if err := cmd.Run(); err != nil {
		util.Warnf("tryDraftNotes: bulk_publish failed, fallback to Discussions API: %v", err)
		return false
	}

	for range inlineEntries {
		result.Posted++
		result.Inline++
	}
	for range fileEntries {
		result.Posted++
		result.FileLevel++
	}

	return true
}

// PostIssuesAsComments 将代码审查问题发布为 MR 评论的主入口。
func (g *GitLabPlatform) PostIssuesAsComments(mrID string, issues []platform.IssueForComment, repo string) platform.ReviewResult {
	commitInfo, err := g.GetHeadCommitInfo(mrID, repo)
	if err != nil {
		return platform.ReviewResult{Failed: len(issues)}
	}
	runID := platform.NewLifecycleRunID(commitInfo.HeadSHA)
	util.Debugf("PostIssuesAsComments: commitInfo head=%s base=%s start=%s", commitInfo.HeadSHA, commitInfo.BaseSHA, commitInfo.StartSHA)

	comments := make([]platform.ReviewCommentInput, 0, len(issues))
	for _, issue := range issues {
		body := platform.FormatIssueBodyWithMeta(issue, runID, commitInfo.HeadSHA)
		comments = append(comments, platform.ReviewCommentInput{
			Path: issue.File,
			Line: issue.Line,
			Body: body,
		})
	}

	changedFiles, err := g.GetChangedFiles(mrID, repo)
	if err != nil {
		return platform.ReviewResult{Failed: len(issues)}
	}

	diffInfoEx := make(map[string]map[int]platform.DiffLineInfo)
	for _, f := range changedFiles {
		if f.Patch != "" {
			diffInfoEx[f.Filename] = platform.ParseDiffLinesEx(f.Patch)
		} else {
			diffInfoEx[f.Filename] = make(map[int]platform.DiffLineInfo)
		}
	}

	// 调试：打印 diff 中的文件路径
	util.Debugf("PostIssuesAsComments: diff contains %d files:", len(diffInfoEx))
	for path, lines := range diffInfoEx {
		util.Debugf("  diff file: %s (lines in diff: %d)", path, len(lines))
	}
	for _, c := range comments {
		lineStr := "nil"
		if c.Line != nil {
			lineStr = fmt.Sprintf("%d", *c.Line)
		}
		util.Debugf("  comment path: %q line: %s", c.Path, lineStr)
	}

	classified := platform.ClassifyCommentsByDiffEx(comments, diffInfoEx)

	// 调试：打印分类结果
	for _, cc := range classified {
		lineStr := "nil"
		if cc.Input.Line != nil {
			lineStr = fmt.Sprintf("%d", *cc.Input.Line)
		}
		util.Debugf("  classified: path=%q line=%s mode=%s", cc.Input.Path, lineStr, cc.Mode)
	}
	desired := platform.BuildDesiredComments(classified, runID, commitInfo.HeadSHA)
	existing := g.GetExistingComments(mrID, repo)
	plan := platform.PlanLifecycle(existing, desired)
	return g.applyLifecyclePlan(mrID, repo, *commitInfo, existing, plan, runID)
}

// PostNote 在 MR 上发布一条全局 note（评论）。
func (g *GitLabPlatform) PostNote(mrID, repo, body string) error {
	resolvedRepo, err := g.resolveRepo(repo)
	if err != nil {
		return err
	}
	encoded := encodeProject(resolvedRepo)

	payload, _ := json.Marshal(map[string]string{"body": body})
	endpoint := fmt.Sprintf("projects/%s/merge_requests/%s/notes", encoded, mrID)
	if _, err := g.glabPostJSON(endpoint, payload); err != nil {
		return fmt.Errorf("failed to post MR note: %w", err)
	}
	return nil
}

// UpsertSummaryNote upsert CodeDebate 的总结 note（通过 marker 匹配已有 note）。
// 找到 marker 则更新该 note，否则创建新 note。
func (g *GitLabPlatform) UpsertSummaryNote(mrID, repo, marker, body string) error {
	resolvedRepo, err := g.resolveRepo(repo)
	if err != nil {
		return err
	}
	encoded := encodeProject(resolvedRepo)

	listEndpoint := fmt.Sprintf("projects/%s/merge_requests/%s/notes", encoded, mrID)
	out, err := g.glabCmd("api", listEndpoint).Output()
	if err != nil {
		return fmt.Errorf("failed to list MR notes: %w", err)
	}

	var notes []struct {
		ID   int    `json:"id"`
		Body string `json:"body"`
	}
	if err := json.Unmarshal(out, &notes); err != nil {
		return fmt.Errorf("failed to parse MR notes: %w", err)
	}

	for _, note := range notes {
		if marker != "" && strings.Contains(note.Body, marker) {
			payload, _ := json.Marshal(map[string]string{"body": body})
			updateEndpoint := fmt.Sprintf("projects/%s/merge_requests/%s/notes/%d", encoded, mrID, note.ID)
			if _, err := g.glabPutJSON(updateEndpoint, payload); err != nil {
				return fmt.Errorf("failed to update summary note: %w", err)
			}
			return nil
		}
	}

	return g.PostNote(mrID, repo, body)
}

func newGitLabExistingComment(id, threadID, path string, line, oldLine *int, body, source string) platform.ExistingComment {
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

func (g *GitLabPlatform) applyLifecyclePlan(mrID, repo string, commitInfo platform.CommitInfo, existing []platform.ExistingComment, plan platform.LifecyclePlan, runID string) platform.ReviewResult {
	var result platform.ReviewResult
	result.Unchanged = len(plan.Noop)
	result.Skipped += len(plan.Noop)

	for _, item := range plan.Resolve {
		if err := g.updateExistingComment(mrID, repo, item.Existing, platform.RenderResolvedBody(item.Existing, runID, commitInfo.HeadSHA)); err != nil {
			result.Failed++
		} else {
			result.Resolved++
		}
	}

	for _, item := range plan.Supersede {
		if err := g.updateExistingComment(mrID, repo, item.Existing, platform.RenderSupersededBody(item.Existing, item.Desired, runID, commitInfo.HeadSHA)); err != nil {
			result.Failed++
		} else {
			result.Superseded++
		}
	}

	for _, item := range plan.Update {
		if err := g.updateExistingComment(mrID, repo, item.Existing, item.Desired.Body); err != nil {
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

func (g *GitLabPlatform) createDesiredComment(mrID, repo string, commitInfo platform.CommitInfo, desired platform.DesiredComment) platform.CommentResult {
	resolvedRepo, err := g.resolveRepo(repo)
	if err != nil {
		return platform.CommentResult{Success: false, Error: err.Error()}
	}
	encodedProject := encodeProject(resolvedRepo)

	switch desired.Source {
	case "inline":
		if desired.Line == nil {
			return platform.CommentResult{Success: false, Error: "inline comment missing line"}
		}
		endpoint := fmt.Sprintf("projects/%s/merge_requests/%s/discussions", encodedProject, mrID)
		payload, _ := json.Marshal(map[string]interface{}{
			"body":     desired.Body,
			"position": buildTextPosition(desired.Path, *desired.Line, desired.OldLine, commitInfo),
		})
		if _, err := g.glabPostJSON(endpoint, payload); err != nil {
			return platform.CommentResult{Success: false, Error: platform.TruncStr(err.Error(), 200)}
		}
		return platform.CommentResult{Success: true, Inline: true, Mode: "inline"}
	case "file":
		endpoint := fmt.Sprintf("projects/%s/merge_requests/%s/discussions", encodedProject, mrID)
		payload, _ := json.Marshal(map[string]interface{}{
			"body":     desired.Body,
			"position": buildFilePosition(desired.Path, commitInfo),
		})
		if _, err := g.glabPostJSON(endpoint, payload); err != nil {
			return platform.CommentResult{Success: false, Error: platform.TruncStr(err.Error(), 200)}
		}
		return platform.CommentResult{Success: true, Inline: false, Mode: "file"}
	default:
		payload, _ := json.Marshal(map[string]string{"body": desired.Body})
		endpoint := fmt.Sprintf("projects/%s/merge_requests/%s/notes", encodedProject, mrID)
		if _, err := g.glabPostJSON(endpoint, payload); err != nil {
			return platform.CommentResult{Success: false, Error: platform.TruncStr(err.Error(), 200)}
		}
		return platform.CommentResult{Success: true, Inline: false, Mode: "global"}
	}
}

func (g *GitLabPlatform) updateExistingComment(mrID, repo string, existing platform.ExistingComment, body string) error {
	resolvedRepo, err := g.resolveRepo(repo)
	if err != nil {
		return err
	}
	encoded := encodeProject(resolvedRepo)
	payload, _ := json.Marshal(map[string]string{"body": body})

	switch existing.Source {
	case "inline", "file":
		if existing.ThreadID == "" || existing.ID == "" {
			return fmt.Errorf("missing discussion or note id")
		}
		endpoint := fmt.Sprintf("projects/%s/merge_requests/%s/discussions/%s/notes/%s", encoded, mrID, existing.ThreadID, existing.ID)
		if _, err := g.glabPutJSON(endpoint, payload); err != nil {
			return fmt.Errorf("failed to update discussion note: %w", err)
		}
	default:
		if existing.ID == "" {
			return fmt.Errorf("missing note id")
		}
		endpoint := fmt.Sprintf("projects/%s/merge_requests/%s/notes/%s", encoded, mrID, existing.ID)
		if _, err := g.glabPutJSON(endpoint, payload); err != nil {
			return fmt.Errorf("failed to update merge request note: %w", err)
		}
	}

	return nil
}

// GetMRDetails 获取单个 MR 的详细信息，用于历史关联。
func (g *GitLabPlatform) GetMRDetails(mrNumber int, cwd string) (*platform.MRDetail, error) {
	resolvedRepo, err := g.resolveRepo("")
	if err != nil {
		return nil, err
	}
	encodedProject := encodeProject(resolvedRepo)

	cmd := g.glabCmd("api",
		fmt.Sprintf("projects/%s/merge_requests/%d", encodedProject, mrNumber),
	)
	cmd.Dir = cwd
	out, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("failed to get MR details: %w", err)
	}

	var data struct {
		IID    int    `json:"iid"`
		Title  string `json:"title"`
		Author struct {
			Username string `json:"username"`
		} `json:"author"`
		MergedAt string `json:"merged_at"`
	}
	if err := json.Unmarshal(out, &data); err != nil {
		return nil, fmt.Errorf("failed to parse MR details: %w", err)
	}

	// 获取 MR 变更的文件列表
	changesOut, err := g.glabCmd("api",
		fmt.Sprintf("projects/%s/merge_requests/%d/diffs", encodedProject, mrNumber),
	).Output()

	var files []string
	if err == nil {
		var diffs []struct {
			NewPath string `json:"new_path"`
		}
		if json.Unmarshal(changesOut, &diffs) == nil {
			for _, d := range diffs {
				files = append(files, d.NewPath)
			}
		}
	}

	return &platform.MRDetail{
		Number:   data.IID,
		Title:    data.Title,
		Author:   data.Author.Username,
		MergedAt: data.MergedAt,
		Files:    files,
	}, nil
}

// GetMRsForCommit 通过 GitLab API 查找与指定 commit SHA 关联的 MR 编号列表。
func (g *GitLabPlatform) GetMRsForCommit(commitSHA string, cwd string) ([]int, error) {
	resolvedRepo, err := g.resolveRepo("")
	if err != nil {
		return nil, err
	}
	encodedProject := encodeProject(resolvedRepo)

	cmd := g.glabCmd("api",
		fmt.Sprintf("projects/%s/repository/commits/%s/merge_requests", encodedProject, commitSHA),
	)
	cmd.Dir = cwd
	out, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("failed to get MRs for commit %s: %w", commitSHA, err)
	}

	var mrs []struct {
		IID int `json:"iid"`
	}
	if err := json.Unmarshal(out, &mrs); err != nil {
		return nil, fmt.Errorf("failed to parse MRs for commit %s: %w", commitSHA, err)
	}

	numbers := make([]int, 0, len(mrs))
	for _, mr := range mrs {
		if mr.IID > 0 {
			numbers = append(numbers, mr.IID)
		}
	}
	return numbers, nil
}
