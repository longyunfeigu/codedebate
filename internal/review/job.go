package review

import (
	"errors"
	"fmt"
	"os/exec"
	"regexp"
	"strings"

	"github.com/longyunfeigu/codedebate/internal/platform"
	"github.com/longyunfeigu/codedebate/internal/prompt"
)

// Job 表示一份可执行的 review 任务。
type Job struct {
	Type     string
	Label    string
	Prompt   string
	Repo     string
	MRNumber string
}

// MRRef 描述一个 MR/PR 的定位信息。
type MRRef struct {
	ID   string
	Repo string
	URL  string
}

// MRInputResolver 为 CLI 输入解析 MR/PR 目标。
type MRInputResolver interface {
	platform.Named
	platform.RepoDetector
}

// ErrNoReviewableChanges 表示 diff 在应用排除规则后没有剩余可审查内容。
var ErrNoReviewableChanges = errors.New("no reviewable changes found")

// IsNoReviewableChanges 判断错误是否表示“没有可审查变更”。
func IsNoReviewableChanges(err error) bool {
	return errors.Is(err, ErrNoReviewableChanges)
}

// BuildFilesJob 构造文件列表审查任务。
func BuildFilesJob(files []string) *Job {
	return &Job{
		Type:   "files",
		Label:  fmt.Sprintf("Files: %s", strings.Join(files, ", ")),
		Prompt: fmt.Sprintf("Review the following files: %s.", strings.Join(files, ", ")),
	}
}

// BuildLocalJob 构造本地未提交改动审查任务。
func BuildLocalJob(diffExclude []string) (*Job, error) {
	diff, err := exec.Command("git", "diff", "HEAD").Output()
	if err != nil {
		return nil, fmt.Errorf("not a git repository or git is not available")
	}

	diffStr := string(diff)
	label := "Local Changes"
	isLastCommit := false

	if strings.TrimSpace(diffStr) == "" {
		diff, err = exec.Command("git", "diff", "HEAD~1", "HEAD").Output()
		if err != nil || strings.TrimSpace(string(diff)) == "" {
			return nil, fmt.Errorf("no changes found. Make some changes or commits first")
		}
		diffStr = string(diff)
		isLastCommit = true
		commitMsg, _ := exec.Command("git", "log", "-1", "--pretty=%s").Output()
		label = fmt.Sprintf("Last Commit: %s", strings.TrimSpace(string(commitMsg)))
	}

	diffStr, err = filterReviewDiff(diffStr, diffExclude, "no reviewable local changes found after applying diff exclusions")
	if err != nil {
		return nil, err
	}

	var reviewPrompt string
	if isLastCommit {
		reviewPrompt = fmt.Sprintf("Please review the following code changes from the last commit:\n\n```diff\n%s\n```\n\nAnalyze these changes and provide your feedback.", diffStr)
	} else {
		reviewPrompt = fmt.Sprintf("Please review the following local code changes (uncommitted diff):\n\n```diff\n%s\n```\n\nAnalyze these changes and provide your feedback.", diffStr)
	}

	return &Job{Type: "local", Label: label, Prompt: reviewPrompt}, nil
}

// BuildBranchJob 构造分支差异审查任务。
func BuildBranchJob(baseBranch string, diffExclude []string) (*Job, error) {
	currentBranch, err := exec.Command("git", "branch", "--show-current").Output()
	if err != nil {
		return nil, fmt.Errorf("failed to get current branch: %w", err)
	}
	branch := strings.TrimSpace(string(currentBranch))

	diff, err := exec.Command("git", "diff", baseBranch+"..."+branch).Output()
	if err != nil {
		return nil, fmt.Errorf("failed to get branch diff: %w", err)
	}

	diffStr, err := filterReviewDiff(string(diff), diffExclude,
		fmt.Sprintf("no reviewable differences found between %s and %s after applying diff exclusions", baseBranch, branch))
	if err != nil {
		return nil, err
	}

	annotatedDiff := platform.AnnotateDiffWithLineNumbers(diffStr)
	reviewPrompt := fmt.Sprintf("Please review the changes in branch \"%s\" compared to \"%s\":\n\n```diff\n%s\n```\n\nAnalyze these changes and provide your feedback.\nWhen reporting issues, always reference the line number shown at the beginning of each line.", branch, baseBranch, annotatedDiff)

	return &Job{
		Type:   "branch",
		Label:  fmt.Sprintf("Branch: %s", branch),
		Prompt: reviewPrompt,
	}, nil
}

// BuildMRJobFromInput 从 CLI 输入解析并构造 MR/PR 审查任务。
func BuildMRJobFromInput(input string, resolver MRInputResolver, metadata platform.MRMetadataProvider, diffExclude []string) (*Job, error) {
	if resolver == nil {
		return nil, fmt.Errorf("cannot resolve PR/MR target: platform detection failed (check git remote configuration)")
	}

	ref := MRRef{}
	if strings.HasPrefix(input, "http") {
		ref.URL = input
		if repo, id, err := resolver.ParseMRURL(input); err == nil {
			ref.Repo = repo
			ref.ID = id
		}
		if ref.ID == "" {
			ref.ID = extractMRIDFromText(input)
		}
	} else {
		ref.ID = input
		if repo, err := resolver.DetectRepoFromRemote(); err == nil {
			ref.Repo = repo
			ref.URL = resolver.BuildMRURL(repo, ref.ID)
		}
	}

	if ref.ID == "" {
		return nil, fmt.Errorf("could not determine PR/MR number from input %q", input)
	}
	if ref.URL == "" {
		ref.URL = fmt.Sprintf("MR/PR #%s", ref.ID)
	}
	if ref.Repo == "" {
		if repo, err := resolver.DetectRepoFromRemote(); err == nil {
			ref.Repo = repo
		}
	}

	return BuildMRJobFromRef(ref, resolver.Name(), metadata, diffExclude)
}

// BuildMRJobFromRef 根据已知 MR 信息构造审查任务。
func BuildMRJobFromRef(ref MRRef, platformName string, metadata platform.MRMetadataProvider, diffExclude []string) (*Job, error) {
	var mrDiff, mrTitle, mrBody string
	if metadata == nil {
		return nil, fmt.Errorf("cannot review %s: metadata provider is unavailable", ref.URL)
	}
	diff, err := metadata.GetDiff(ref.ID, ref.Repo)
	if err != nil {
		return nil, fmt.Errorf("failed to get PR/MR diff for %s: %w", ref.URL, err)
	}
	mrDiff, err = filterReviewDiff(diff, diffExclude,
		fmt.Sprintf("%s has no reviewable changes after applying diff exclusions", ref.URL))
	if err != nil {
		return nil, err
	}
	if info, err := metadata.GetInfo(ref.ID, ref.Repo); err == nil {
		mrTitle = info.Title
		mrBody = info.Description
	}

	annotatedDiff := platform.AnnotateDiffWithLineNumbers(mrDiff)
	reviewPrompt := fmt.Sprintf("Please review %s.\n\nTitle: %s\n\nDescription:\n%s\n\nHere is the full diff (each line is prefixed with its new-file line number for reference):\n\n```diff\n%s```\n\nAnalyze these changes and provide your feedback. You already have the complete diff above — do NOT attempt to fetch it again.\nWhen reporting issues, always reference the line number shown at the beginning of each line (e.g. \"line 263\").",
		ref.URL, mrTitle, mrBody, annotatedDiff)

	label := fmt.Sprintf("PR #%s", ref.ID)
	if strings.EqualFold(strings.TrimSpace(platformName), "gitlab") {
		label = fmt.Sprintf("MR !%s", ref.ID)
	}

	return &Job{
		Type:     "pr",
		Label:    label,
		Prompt:   reviewPrompt,
		Repo:     ref.Repo,
		MRNumber: ref.ID,
	}, nil
}

// BuildServerMRJob 使用服务端模板构造 webhook 模式的 MR 审查任务。
func BuildServerMRJob(ref MRRef, metadata platform.MRMetadataProvider, diffExclude []string) (*Job, error) {
	if metadata == nil {
		return nil, fmt.Errorf("cannot review %s: metadata provider is unavailable", ref.URL)
	}
	mrDiff, err := metadata.GetDiff(ref.ID, ref.Repo)
	if err != nil {
		return nil, fmt.Errorf("failed to get MR diff: %w", err)
	}
	mrDiff, err = filterReviewDiff(mrDiff, diffExclude,
		fmt.Sprintf("%s has no reviewable changes after applying diff exclusions", ref.URL))
	if err != nil {
		return nil, err
	}

	mrInfo, err := metadata.GetInfo(ref.ID, ref.Repo)
	if err != nil {
		return nil, fmt.Errorf("failed to get MR info: %w", err)
	}

	reviewPrompt := prompt.MustRender("server_review.tmpl", map[string]any{
		"MRURL":        ref.URL,
		"Title":        mrInfo.Title,
		"Description":  mrInfo.Description,
		"Diff":         platform.AnnotateDiffWithLineNumbers(mrDiff),
		"HasLocalRepo": false,
	})

	return &Job{
		Type:     "pr",
		Label:    fmt.Sprintf("MR !%s", ref.ID),
		Prompt:   reviewPrompt,
		Repo:     ref.Repo,
		MRNumber: ref.ID,
	}, nil
}

func extractMRIDFromText(input string) string {
	for _, re := range []*regexp.Regexp{
		regexp.MustCompile(`/pull/(\d+)`),
		regexp.MustCompile(`/merge_requests/(\d+)`),
	} {
		if m := re.FindStringSubmatch(input); len(m) > 1 {
			return m[1]
		}
	}
	return ""
}

func filterReviewDiff(diff string, diffExclude []string, noReviewableMsg string) (string, error) {
	filtered := platform.FilterDiff(diff, diffExclude)
	if strings.TrimSpace(filtered) == "" {
		return "", fmt.Errorf("%w: %s", ErrNoReviewableChanges, noReviewableMsg)
	}
	return filtered, nil
}
