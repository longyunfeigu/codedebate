package context

import (
	"fmt"
	"testing"

	"github.com/longyunfeigu/codedebate/internal/platform"
)

// mockHistoryProvider 是 HistoryProvider 的测试 mock。
type mockHistoryProvider struct {
	// commitToPRs 映射 commit SHA → PR 编号列表
	commitToPRs map[string][]int
	// mrDetails 映射 PR 编号 → MRDetail
	mrDetails map[int]*platform.MRDetail
	// commitCallCount 记录 GetMRsForCommit 被调用的次数
	commitCallCount int
	// commitCallLog 记录每次调用的 SHA
	commitCallLog []string
	// forceError 为 true 时，GetMRsForCommit 总是返回错误
	forceError bool
}

func (m *mockHistoryProvider) GetMRsForCommit(commitSHA string, cwd string) ([]int, error) {
	m.commitCallCount++
	m.commitCallLog = append(m.commitCallLog, commitSHA)
	if m.forceError {
		return nil, fmt.Errorf("mock API error")
	}
	prs, ok := m.commitToPRs[commitSHA]
	if !ok {
		return nil, fmt.Errorf("commit %s not found", commitSHA)
	}
	return prs, nil
}

func (m *mockHistoryProvider) GetMRDetails(mrNumber int, cwd string) (*platform.MRDetail, error) {
	detail, ok := m.mrDetails[mrNumber]
	if !ok {
		return nil, fmt.Errorf("MR #%d not found", mrNumber)
	}
	return detail, nil
}

// --- discoverPRNumbers 测试 ---

func TestDiscoverPRNumbers_Basic(t *testing.T) {
	mock := &mockHistoryProvider{
		commitToPRs: map[string][]int{
			"aaa": {10},
			"bbb": {20},
			"ccc": {30},
		},
	}

	shas := []string{"aaa", "bbb", "ccc"}
	result := discoverPRNumbers(shas, 15, 10, ".", mock)

	if len(result) != 3 {
		t.Fatalf("expected 3 PRs, got %d: %v", len(result), result)
	}
	expected := []int{10, 20, 30}
	for i, n := range expected {
		if result[i] != n {
			t.Errorf("result[%d] = %d, want %d", i, result[i], n)
		}
	}
}

func TestDiscoverPRNumbers_Dedup(t *testing.T) {
	// 多个 commit 关联同一个 PR，应该去重
	mock := &mockHistoryProvider{
		commitToPRs: map[string][]int{
			"aaa": {10},
			"bbb": {10}, // 重复
			"ccc": {10, 20},
			"ddd": {20}, // 重复
		},
	}

	shas := []string{"aaa", "bbb", "ccc", "ddd"}
	result := discoverPRNumbers(shas, 15, 10, ".", mock)

	if len(result) != 2 {
		t.Fatalf("expected 2 unique PRs, got %d: %v", len(result), result)
	}
	if result[0] != 10 || result[1] != 20 {
		t.Errorf("expected [10, 20], got %v", result)
	}
}

func TestDiscoverPRNumbers_StopsAtMaxPRs(t *testing.T) {
	mock := &mockHistoryProvider{
		commitToPRs: map[string][]int{
			"aaa": {10},
			"bbb": {20},
			"ccc": {30},
			"ddd": {40},
		},
	}

	shas := []string{"aaa", "bbb", "ccc", "ddd"}
	result := discoverPRNumbers(shas, 15, 2, ".", mock)

	if len(result) != 2 {
		t.Fatalf("expected 2 PRs (maxPRs=2), got %d: %v", len(result), result)
	}
	// 攒够 2 个 PR 后应该停止，不再调用第 3 个 SHA
	if mock.commitCallCount > 2 {
		t.Errorf("expected at most 2 API calls, got %d", mock.commitCallCount)
	}
}

func TestDiscoverPRNumbers_StopsAtMaxCalls(t *testing.T) {
	mock := &mockHistoryProvider{
		commitToPRs: map[string][]int{
			"aaa": {10},
			"bbb": {20},
			"ccc": {30},
			"ddd": {40},
			"eee": {50},
		},
	}

	shas := []string{"aaa", "bbb", "ccc", "ddd", "eee"}
	result := discoverPRNumbers(shas, 3, 10, ".", mock)

	if mock.commitCallCount != 3 {
		t.Errorf("expected exactly 3 API calls (maxCalls=3), got %d", mock.commitCallCount)
	}
	if len(result) != 3 {
		t.Errorf("expected 3 PRs, got %d: %v", len(result), result)
	}
}

func TestDiscoverPRNumbers_SkipsErrors(t *testing.T) {
	mock := &mockHistoryProvider{
		commitToPRs: map[string][]int{
			"aaa": {10},
			// "bbb" 不存在 → 返回错误
			"ccc": {30},
		},
	}

	shas := []string{"aaa", "bbb", "ccc"}
	result := discoverPRNumbers(shas, 15, 10, ".", mock)

	if len(result) != 2 {
		t.Fatalf("expected 2 PRs (skip errored bbb), got %d: %v", len(result), result)
	}
	if result[0] != 10 || result[1] != 30 {
		t.Errorf("expected [10, 30], got %v", result)
	}
	if mock.commitCallCount != 3 {
		t.Errorf("expected 3 API calls (including the failed one), got %d", mock.commitCallCount)
	}
}

func TestDiscoverPRNumbers_AllErrors(t *testing.T) {
	mock := &mockHistoryProvider{
		forceError: true,
	}

	shas := []string{"aaa", "bbb", "ccc"}
	result := discoverPRNumbers(shas, 15, 10, ".", mock)

	if len(result) != 0 {
		t.Fatalf("expected 0 PRs when all calls fail, got %d: %v", len(result), result)
	}
}

func TestDiscoverPRNumbers_EmptySHAs(t *testing.T) {
	mock := &mockHistoryProvider{}

	result := discoverPRNumbers(nil, 15, 10, ".", mock)
	if len(result) != 0 {
		t.Fatalf("expected 0 PRs for nil shas, got %d", len(result))
	}
	if mock.commitCallCount != 0 {
		t.Errorf("expected 0 API calls for nil shas, got %d", mock.commitCallCount)
	}
}

func TestDiscoverPRNumbers_MultiPRsPerCommit(t *testing.T) {
	// 单个 commit 关联多个 PR（cherry-pick 场景）
	mock := &mockHistoryProvider{
		commitToPRs: map[string][]int{
			"aaa": {10, 20, 30},
		},
	}

	shas := []string{"aaa"}
	result := discoverPRNumbers(shas, 15, 10, ".", mock)

	if len(result) != 3 {
		t.Fatalf("expected 3 PRs from single commit, got %d: %v", len(result), result)
	}
}

// --- CollectHistory with mock 测试 ---

func TestCollectHistory_APIPath(t *testing.T) {
	// 测试：当 historyProvider 非 nil 但 extractCommitSHAs 返回空（因为 cwd 无效），
	// 应该回退到 extractPRNumbersFromMessages
	mock := &mockHistoryProvider{
		commitToPRs: map[string][]int{},
		mrDetails:   map[int]*platform.MRDetail{},
	}

	result, err := CollectHistory([]string{"nonexistent.go"}, 30, 5, "/tmp", mock)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// 两条路径都找不到 PR，结果应该为空
	if len(result) != 0 {
		t.Errorf("expected 0 related PRs, got %d", len(result))
	}
}

func TestCollectHistory_NilProvider(t *testing.T) {
	// historyProvider 为 nil 时应直接走回退路径，不 panic
	result, err := CollectHistory([]string{"some_file.go"}, 30, 5, "/tmp", nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(result) != 0 {
		t.Errorf("expected 0 results with nil provider, got %d", len(result))
	}
}

func TestCollectHistory_DefaultValues(t *testing.T) {
	// maxDays=0 和 maxPRs=0 应使用默认值，不 panic
	result, err := CollectHistory([]string{"some_file.go"}, 0, 0, "/tmp", nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(result) != 0 {
		t.Errorf("expected 0 results, got %d", len(result))
	}
}

func TestCollectHistory_EmptyFilesUnit(t *testing.T) {
	result, err := CollectHistory(nil, 30, 5, ".", nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result != nil {
		t.Errorf("expected nil for empty files, got %v", result)
	}
}

// --- findPRNumbers 单元测试（保留原有测试） ---

func TestFindPRNumbers_Unit(t *testing.T) {
	tests := []struct {
		message string
		want    []int
	}{
		{"fix: resolve issue #42", []int{42}},
		{"feat(auth): add login (#123)", []int{123}},
		{"Merge pull request #7 from branch", []int{7}},
		{"refs #10, #20 and #30", []int{10, 20, 30}},
		{"no pr reference here", nil},
		{"#0 should be ignored", nil},
		{"trailing # sign", nil},
		{"", nil},
		{"##5 double hash", []int{5}},
	}

	for _, tt := range tests {
		got := findPRNumbers(tt.message)
		if len(got) != len(tt.want) {
			t.Errorf("findPRNumbers(%q) = %v, want %v", tt.message, got, tt.want)
			continue
		}
		for i := range got {
			if got[i] != tt.want[i] {
				t.Errorf("findPRNumbers(%q)[%d] = %d, want %d", tt.message, i, got[i], tt.want[i])
			}
		}
	}
}
