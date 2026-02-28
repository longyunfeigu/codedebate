package context

import (
	"fmt"
	"os/exec"
	"strconv"
	"strings"

	"github.com/longyunfeigu/codedebate/internal/platform"
)

const (
	maxCommitSHAs = 30 // 最多从 git log 获取的 commit SHA 数量
	maxAPICalls   = 15 // 最多调用 GetMRsForCommit 的次数
)

// CollectHistory 收集与变更文件相关的历史 PR/MR 信息。
// 优先通过 commit SHA + 平台 API 发现关联 PR（适用于所有 merge 策略），
// 如果 API 方式返回 0 个结果，回退到旧的 commit message #N 解析方式。
func CollectHistory(changedFiles []string, maxDays, maxPRs int, cwd string, historyProvider platform.HistoryProvider) ([]RelatedPR, error) {
	if len(changedFiles) == 0 {
		return nil, nil
	}

	if maxDays <= 0 {
		maxDays = 30
	}
	if maxPRs <= 0 {
		maxPRs = 10
	}

	// 优先：通过 commit SHA + 平台 API 发现关联 PR
	var prNumbers []int
	if historyProvider != nil {
		shas := extractCommitSHAs(changedFiles, maxDays, maxCommitSHAs, cwd)
		prNumbers = discoverPRNumbers(shas, maxAPICalls, maxPRs, cwd, historyProvider)
	}

	// 回退：如果 API 方式返回 0 个结果，使用旧的 #N 解析
	if len(prNumbers) == 0 {
		prNumbers = extractPRNumbersFromMessages(changedFiles, maxDays, cwd)
	}

	var relatedPRs []RelatedPR
	for _, prNum := range prNumbers {
		if len(relatedPRs) >= maxPRs {
			break
		}
		pr := getPRDetails(prNum, changedFiles, cwd, historyProvider)
		if pr != nil {
			relatedPRs = append(relatedPRs, *pr)
		}
	}

	return relatedPRs, nil
}

// extractCommitSHAs 从 git log 中获取涉及指定文件的 commit SHA 列表。
func extractCommitSHAs(files []string, maxDays, maxCommits int, cwd string) []string {
	args := make([]string, 0, len(files)+7)
	args = append(args, "log",
		fmt.Sprintf("--since=%d days ago", maxDays),
		"--pretty=format:%H",
		fmt.Sprintf("--max-count=%d", maxCommits),
		"--",
	)
	args = append(args, files...)

	cmd := exec.Command("git", args...)
	cmd.Dir = cwd
	out, err := cmd.Output()
	if err != nil || len(out) == 0 {
		return nil
	}

	var shas []string
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		line = strings.TrimSpace(line)
		if line != "" {
			shas = append(shas, line)
		}
	}
	return shas
}

// discoverPRNumbers 对每个 commit SHA 调用平台 API 查找关联 PR，边查边去重，攒够即停。
func discoverPRNumbers(shas []string, maxCalls, maxPRs int, cwd string, provider platform.HistoryProvider) []int {
	seen := make(map[int]bool)
	var prNumbers []int
	apiCalls := 0

	for _, sha := range shas {
		if apiCalls >= maxCalls || len(prNumbers) >= maxPRs {
			break
		}
		numbers, err := provider.GetMRsForCommit(sha, cwd)
		apiCalls++
		if err != nil {
			continue // 单个 SHA 查询失败，跳过继续
		}
		for _, n := range numbers {
			if !seen[n] {
				seen[n] = true
				prNumbers = append(prNumbers, n)
			}
		}
	}
	return prNumbers
}

// extractPRNumbersFromMessages 从 git log commit message 中提取 #N 格式的 PR 编号。
// 作为 API 方式的兜底回退方案。
func extractPRNumbersFromMessages(files []string, maxDays int, cwd string) []int {
	fileArgs := make([]string, 0, len(files)+6)
	fileArgs = append(fileArgs, "log",
		fmt.Sprintf("--since=%d days ago", maxDays),
		"--pretty=format:%s",
		"--name-only",
		"--",
	)
	fileArgs = append(fileArgs, files...)

	cmd := exec.Command("git", fileArgs...)
	cmd.Dir = cwd
	out, err := cmd.Output()
	if err != nil || len(out) == 0 {
		return nil
	}

	seen := make(map[int]bool)
	var prNumbers []int

	for _, line := range strings.Split(string(out), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		for _, idx := range findPRNumbers(line) {
			if !seen[idx] {
				seen[idx] = true
				prNumbers = append(prNumbers, idx)
			}
		}
	}

	return prNumbers
}

// findPRNumbers 从单条提交消息中查找所有 #N 格式的 PR 编号。
func findPRNumbers(message string) []int {
	var numbers []int
	for i := 0; i < len(message)-2; i++ {
		if message[i] == '#' {
			j := i + 1
			for j < len(message) && message[j] >= '0' && message[j] <= '9' {
				j++
			}
			if j > i+1 {
				n, err := strconv.Atoi(message[i+1 : j])
				if err == nil && n > 0 {
					numbers = append(numbers, n)
				}
			}
		}
	}
	return numbers
}

// getPRDetails 通过 HistoryProvider 获取单个 PR/MR 的详细信息。
func getPRDetails(prNumber int, changedFiles []string, cwd string, historyProvider platform.HistoryProvider) *RelatedPR {
	if historyProvider == nil {
		return nil
	}

	detail, err := historyProvider.GetMRDetails(prNumber, cwd)
	if err != nil {
		return nil
	}

	changedSet := make(map[string]bool, len(changedFiles))
	for _, f := range changedFiles {
		changedSet[f] = true
	}

	var overlapping []string
	for _, f := range detail.Files {
		if changedSet[f] {
			overlapping = append(overlapping, f)
		}
	}

	relevance := "same-module"
	if len(overlapping) > 0 {
		relevance = "direct"
	}

	return &RelatedPR{
		Number:           detail.Number,
		Title:            detail.Title,
		Author:           detail.Author,
		MergedAt:         detail.MergedAt,
		OverlappingFiles: overlapping,
		Relevance:        relevance,
	}
}
