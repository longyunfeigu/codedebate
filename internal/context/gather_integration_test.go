//go:build integration

package context

import (
	gocontext "context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/longyunfeigu/codedebate/internal/provider"
)

// 用于集成测试的真实 diff（取自 orchestrator.go 的一次真实提交）
const testDiff = `diff --git a/internal/orchestrator/orchestrator.go b/internal/orchestrator/orchestrator.go
index f7b0ec7..0117d94 100644
--- a/internal/orchestrator/orchestrator.go
+++ b/internal/orchestrator/orchestrator.go
@@ -11,6 +11,7 @@ import (
 	"unicode"

 	"github.com/longyunfeigu/codedebate/internal/provider"
+	"github.com/longyunfeigu/codedebate/internal/util"
 	"golang.org/x/sync/errgroup"
 )

@@ -631,8 +632,10 @@ func (o *DebateOrchestrator) structurizeIssues(ctx context.Context, display Disp
 	}

 	if len(lastMessages) == 0 {
+		util.Warnf("structurizeIssues: no reviewer messages found in conversation history (total messages: %d)", len(o.conversationHistory))
 		return nil
 	}
+	util.Debugf("structurizeIssues: collected last messages from %d reviewers", len(lastMessages))

 	var reviewParts []string
 	var reviewerIDs []string
`

// requireClaude 检查 claude CLI 是否可用，不可用则跳过测试。
func requireClaude(t *testing.T) {
	t.Helper()
	if _, err := exec.LookPath("claude"); err != nil {
		t.Skip("claude CLI not found, skipping integration test")
	}
}

// requireRipgrep 检查 ripgrep 是否可用，不可用则跳过测试。
func requireRipgrep(t *testing.T) {
	t.Helper()
	if _, err := exec.LookPath("rg"); err != nil {
		t.Skip("ripgrep (rg) not found, skipping integration test")
	}
}

// requireGit 检查 git 是否可用且当前目录是 git 仓库。
func requireGit(t *testing.T) {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not found, skipping integration test")
	}
	cmd := exec.Command("git", "rev-parse", "--git-dir")
	if err := cmd.Run(); err != nil {
		t.Skip("not inside a git repository, skipping integration test")
	}
}

// getProjectRoot 获取项目根目录（go.mod 所在目录）。
func getProjectRoot(t *testing.T) string {
	t.Helper()
	// 从当前文件位置向上找到 go.mod
	dir, err := os.Getwd()
	if err != nil {
		t.Fatalf("failed to get working directory: %v", err)
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatal("could not find project root (go.mod)")
		}
		dir = parent
	}
}

// --- Gather 集成测试 ---

func TestGather_Integration_ClaudeCode(t *testing.T) {
	requireClaude(t)
	requireRipgrep(t)
	requireGit(t)

	projectRoot := getProjectRoot(t)

	// 创建真实的 ClaudeCodeProvider
	p := provider.NewClaudeCodeProvider()
	p.SetCwd(projectRoot)

	gatherer := NewContextGatherer(p, nil, nil) // nil config = 使用默认值, nil historyProvider

	start := time.Now()
	result, err := gatherer.Gather(gocontext.Background(), testDiff, "test-42", "main")
	elapsed := time.Since(start)

	if err != nil {
		t.Fatalf("Gather failed: %v", err)
	}

	t.Logf("Gather completed in %v", elapsed)

	// 验证基本结构
	if result == nil {
		t.Fatal("Gather returned nil result")
	}

	if result.PRNumber != "test-42" {
		t.Errorf("PRNumber = %q, want %q", result.PRNumber, "test-42")
	}
	if result.BaseBranch != "main" {
		t.Errorf("BaseBranch = %q, want %q", result.BaseBranch, "main")
	}
	if result.GatheredAt.IsZero() {
		t.Error("GatheredAt is zero")
	}

	// Summary 不应为空（AI 分析成功时有内容，失败时有 fallback 消息）
	if result.Summary == "" {
		t.Error("Summary is empty")
	}
	t.Logf("Summary (first 200 chars): %.200s", result.Summary)

	// 检查 RawReferences（diff 中有 structurizeIssues 等符号，ripgrep 应能找到引用）
	t.Logf("RawReferences count: %d", len(result.RawReferences))
	for _, ref := range result.RawReferences {
		t.Logf("  Symbol: %s, found in %d files", ref.Symbol, len(ref.FoundInFiles))
	}

	// 检查 AffectedModules（AI 应能识别出 orchestrator 模块）
	t.Logf("AffectedModules count: %d", len(result.AffectedModules))
	for _, mod := range result.AffectedModules {
		t.Logf("  Module: %s (%s), impact: %s, files: %v", mod.Name, mod.Path, mod.ImpactLevel, mod.AffectedFiles)
	}

	// 检查 CallChain
	t.Logf("CallChain count: %d", len(result.CallChain))
	for _, cc := range result.CallChain {
		t.Logf("  Symbol: %s, callers: %d", cc.Symbol, len(cc.Callers))
	}

	// 检查 DesignPatterns
	t.Logf("DesignPatterns count: %d", len(result.DesignPatterns))
	for _, dp := range result.DesignPatterns {
		t.Logf("  Pattern: %s (%s)", dp.Pattern, dp.Source)
	}

	// 打印完整 JSON 便于调试
	jsonBytes, _ := json.MarshalIndent(result, "", "  ")
	t.Logf("Full GatheredContext JSON:\n%s", string(jsonBytes))
}

func TestGather_Integration_AIFailureFallback(t *testing.T) {
	requireRipgrep(t)
	requireGit(t)

	projectRoot := getProjectRoot(t)

	// 使用一个会失败的 provider（不存在的命令）来测试 fallback 路径
	p := &failingProvider{}
	gatherer := NewContextGatherer(p, nil, nil)

	// 需要在项目根目录执行才能找到文件
	origDir, _ := os.Getwd()
	os.Chdir(projectRoot)
	defer os.Chdir(origDir)

	result, err := gatherer.Gather(gocontext.Background(), testDiff, "test-99", "main")

	// Gather 在 AI 失败时不应返回 error，而是返回部分上下文
	if err != nil {
		t.Fatalf("Gather should not fail when AI fails, got: %v", err)
	}
	if result == nil {
		t.Fatal("Gather returned nil result on AI failure")
	}

	// Summary 应包含 fallback 消息
	if !strings.Contains(result.Summary, "AI analysis unavailable") {
		t.Errorf("Summary should contain fallback message, got: %q", result.Summary)
	}

	// RawReferences 仍然应该有数据（ripgrep 不依赖 AI）
	t.Logf("RawReferences on AI failure: %d", len(result.RawReferences))
}

// failingProvider 是一个总是返回错误的 provider，用于测试 fallback 路径。
type failingProvider struct{}

func (p *failingProvider) Name() string { return "failing" }
func (p *failingProvider) Chat(_ gocontext.Context, _ []provider.Message, _ string, _ *provider.ChatOptions) (string, error) {
	return "", fmt.Errorf("intentional failure for testing")
}
func (p *failingProvider) ChatStream(_ gocontext.Context, _ []provider.Message, _ string) (<-chan string, <-chan error) {
	ch := make(chan string)
	errCh := make(chan error, 1)
	errCh <- fmt.Errorf("intentional failure for testing")
	close(ch)
	close(errCh)
	return ch, errCh
}

// --- CollectHistory 集成测试 ---

func TestCollectHistory_Integration(t *testing.T) {
	requireGit(t)

	projectRoot := getProjectRoot(t)

	// 使用项目中实际存在的文件
	changedFiles := []string{
		"internal/orchestrator/orchestrator.go",
		"internal/provider/provider.go",
	}

	result, err := CollectHistory(changedFiles, 90, 5, projectRoot, nil)
	if err != nil {
		t.Fatalf("CollectHistory failed: %v", err)
	}

	// 即使没有 HistoryProvider，extractPRNumbersFromMessages 仍应从 git log 中提取 PR 编号
	// （但因为 historyProvider=nil，getPRDetails 会跳过，所以结果可能为空）
	t.Logf("CollectHistory returned %d related PRs", len(result))
	for _, pr := range result {
		t.Logf("  PR #%d: %s (by %s, relevance: %s)", pr.Number, pr.Title, pr.Author, pr.Relevance)
	}
}

func TestCollectHistory_EmptyFiles(t *testing.T) {
	result, err := CollectHistory(nil, 30, 5, ".", nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result != nil {
		t.Errorf("expected nil for empty files, got %v", result)
	}
}

func TestExtractPRNumbers_Integration(t *testing.T) {
	requireGit(t)

	projectRoot := getProjectRoot(t)

	files := []string{"internal/orchestrator/orchestrator.go"}
	prNumbers := extractPRNumbersFromMessages(files, 90, projectRoot)

	t.Logf("Found %d PR numbers in git log for orchestrator.go", len(prNumbers))
	for _, n := range prNumbers {
		t.Logf("  PR #%d", n)
	}
}

func TestFindPRNumbers(t *testing.T) {
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

// --- CollectReferences 集成测试 ---

func TestCollectReferences_Integration(t *testing.T) {
	requireRipgrep(t)

	projectRoot := getProjectRoot(t)

	// 使用一个肯定存在于代码库中的符号
	diff := `+func RunStreaming(ctx context.Context, label, prompt string, display DisplayCallbacks) (*DebateResult, error) {`

	result := CollectReferences(diff, projectRoot)

	if len(result) == 0 {
		t.Fatal("expected at least one reference for RunStreaming")
	}

	found := false
	for _, ref := range result {
		t.Logf("Symbol: %s, found in %d files", ref.Symbol, len(ref.FoundInFiles))
		if ref.Symbol == "RunStreaming" {
			found = true
			if len(ref.FoundInFiles) == 0 {
				t.Error("RunStreaming should have references in other files")
			}
			for _, loc := range ref.FoundInFiles {
				t.Logf("  %s:%d: %s", loc.File, loc.Line, loc.Content)
			}
		}
	}

	if !found {
		t.Error("RunStreaming symbol not found in references")
	}
}

func TestFindReferences_Integration(t *testing.T) {
	requireRipgrep(t)

	projectRoot := getProjectRoot(t)

	// 搜索一个项目中肯定存在的符号
	symbols := []string{"DebateOrchestrator"}
	refs := FindReferences(symbols, projectRoot)

	if len(refs) == 0 {
		t.Fatal("expected references for DebateOrchestrator")
	}

	ref := refs[0]
	if ref.Symbol != "DebateOrchestrator" {
		t.Errorf("Symbol = %q, want %q", ref.Symbol, "DebateOrchestrator")
	}
	if len(ref.FoundInFiles) == 0 {
		t.Error("expected at least one reference location")
	}

	t.Logf("DebateOrchestrator found in %d locations", len(ref.FoundInFiles))
	for _, loc := range ref.FoundInFiles[:min(5, len(ref.FoundInFiles))] {
		t.Logf("  %s:%d", loc.File, loc.Line)
	}
}

func TestFindReferences_NonexistentSymbol(t *testing.T) {
	requireRipgrep(t)

	projectRoot := getProjectRoot(t)

	// 使用动态拼接避免 ripgrep 在本测试文件中匹配到该字符串
	sym := "Zqx" + "Wvb" + "Nonexistent" + "99887766"
	refs := FindReferences([]string{sym}, projectRoot)
	if len(refs) != 0 {
		t.Errorf("expected no references for nonexistent symbol %q, got %d", sym, len(refs))
	}
}

// --- CollectDocs 集成测试 ---

func TestCollectDocs_Integration(t *testing.T) {
	projectRoot := getProjectRoot(t)

	docs, err := CollectDocs(nil, 50000, projectRoot) // nil patterns = 使用默认值
	if err != nil {
		t.Fatalf("CollectDocs failed: %v", err)
	}

	if len(docs) == 0 {
		t.Fatal("expected at least one document (project has README.md)")
	}

	t.Logf("Found %d documents", len(docs))
	for _, doc := range docs {
		t.Logf("  %s (%d bytes)", doc.Path, len(doc.Content))
	}

	// 验证 README.md 被收集到
	foundReadme := false
	for _, doc := range docs {
		if doc.Path == "README.md" || strings.HasSuffix(doc.Path, "/README.md") {
			foundReadme = true
			if doc.Content == "" {
				t.Error("README.md content is empty")
			}
			break
		}
	}
	if !foundReadme {
		t.Error("README.md not found in collected docs")
	}
}

func TestCollectDocs_CustomPatterns(t *testing.T) {
	projectRoot := getProjectRoot(t)

	// 只收集 internal/orchestrator 下的文档
	docs, err := CollectDocs([]string{"internal/orchestrator"}, 50000, projectRoot)
	if err != nil {
		t.Fatalf("CollectDocs failed: %v", err)
	}

	t.Logf("Found %d docs in internal/orchestrator", len(docs))
	for _, doc := range docs {
		if !strings.HasPrefix(doc.Path, "internal/orchestrator") {
			t.Errorf("unexpected doc outside target dir: %s", doc.Path)
		}
		t.Logf("  %s (%d bytes)", doc.Path, len(doc.Content))
	}
}

func TestCollectDocs_MaxSize(t *testing.T) {
	projectRoot := getProjectRoot(t)

	// 设置很小的 maxSize，应该过滤掉大文件
	docs, err := CollectDocs([]string{"README.md"}, 10, projectRoot)
	if err != nil {
		t.Fatalf("CollectDocs failed: %v", err)
	}

	// README.md 肯定大于 10 字节，所以不应被收集
	for _, doc := range docs {
		if doc.Path == "README.md" {
			t.Error("README.md should have been filtered by maxSize=10")
		}
	}
}

// --- extractChangedFiles 测试 ---

func TestExtractChangedFiles(t *testing.T) {
	tests := []struct {
		name    string
		diff    string
		wantLen int
		wantHas []string
	}{
		{
			name: "standard diff",
			diff: `diff --git a/internal/orchestrator/orchestrator.go b/internal/orchestrator/orchestrator.go
--- a/internal/orchestrator/orchestrator.go
+++ b/internal/orchestrator/orchestrator.go
@@ -1,3 +1,5 @@
+some change`,
			wantLen: 1,
			wantHas: []string{"internal/orchestrator/orchestrator.go"},
		},
		{
			name: "multiple files",
			diff: `diff --git a/file1.go b/file1.go
--- a/file1.go
+++ b/file1.go
@@ -1 +1 @@
-old
+new
diff --git a/file2.go b/file2.go
--- a/file2.go
+++ b/file2.go
@@ -1 +1 @@
-old
+new`,
			wantLen: 2,
			wantHas: []string{"file1.go", "file2.go"},
		},
		{
			name:    "empty diff",
			diff:    "",
			wantLen: 0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := extractChangedFiles(tt.diff)
			if len(got) != tt.wantLen {
				t.Errorf("extractChangedFiles() returned %d files, want %d: %v", len(got), tt.wantLen, got)
			}
			for _, want := range tt.wantHas {
				found := false
				for _, g := range got {
					if g == want {
						found = true
						break
					}
				}
				if !found {
					t.Errorf("expected file %q not found in result: %v", want, got)
				}
			}
		})
	}
}

// --- BuildAnalysisPrompt 测试 ---

func TestBuildAnalysisPrompt(t *testing.T) {
	diff := "+func Hello() {}"
	files := []string{"hello.go"}
	refs := []RawReference{
		{Symbol: "Hello", FoundInFiles: []ReferenceLocation{{File: "main.go", Line: 10, Content: "Hello()"}}},
	}
	prs := []RelatedPR{
		{Number: 1, Title: "init", Author: "alice", Relevance: "direct"},
	}
	docs := []RawDoc{
		{Path: "README.md", Content: "# My Project"},
	}

	prompt := BuildAnalysisPrompt(diff, files, refs, prs, docs)

	// 检查 prompt 包含所有关键部分
	checks := []string{
		"## PR Diff",
		"+func Hello()",
		"## Changed Files",
		"hello.go",
		"## Code References",
		"Hello",
		"main.go:10",
		"## Related Recent PRs",
		"PR #1",
		"alice",
		"## Project Documentation",
		"README.md",
		"# My Project",
		"affectedModules",
		"callChain",
	}

	for _, check := range checks {
		if !strings.Contains(prompt, check) {
			t.Errorf("prompt missing expected content: %q", check)
		}
	}

	t.Logf("Prompt length: %d chars", len(prompt))
}

func TestBuildAnalysisPrompt_DiffTruncation(t *testing.T) {
	// 生成超过 maxDiffLength 的 diff
	longDiff := strings.Repeat("+line of code\n", 2000)
	prompt := BuildAnalysisPrompt(longDiff, nil, nil, nil, nil)

	if !strings.Contains(prompt, "... (truncated)") {
		t.Error("long diff should be truncated")
	}
}

// --- parseAIResponse 测试 ---

func TestParseAIResponse_ValidJSON(t *testing.T) {
	response := "```json\n" + `{
  "affectedModules": [
    {"name": "Auth", "path": "internal/auth", "description": "Authentication module", "affectedFiles": ["auth.go"], "totalFiles": 3, "impactLevel": "core"}
  ],
  "callChain": [
    {"symbol": "Login", "file": "auth.go", "callers": [{"symbol": "main", "file": "main.go", "context": "entry point"}]}
  ],
  "designPatterns": [
    {"pattern": "Factory", "location": "provider/", "description": "Provider creation", "source": "inferred"}
  ],
  "summary": "This PR modifies the auth module."
}` + "\n```"

	result := parseAIResponse(response)

	if len(result.AffectedModules) != 1 {
		t.Errorf("AffectedModules count = %d, want 1", len(result.AffectedModules))
	}
	if result.AffectedModules[0].Name != "Auth" {
		t.Errorf("Module name = %q, want %q", result.AffectedModules[0].Name, "Auth")
	}
	if result.AffectedModules[0].ImpactLevel != "core" {
		t.Errorf("ImpactLevel = %q, want %q", result.AffectedModules[0].ImpactLevel, "core")
	}
	if len(result.CallChain) != 1 {
		t.Errorf("CallChain count = %d, want 1", len(result.CallChain))
	}
	if len(result.DesignPatterns) != 1 {
		t.Errorf("DesignPatterns count = %d, want 1", len(result.DesignPatterns))
	}
	if result.Summary != "This PR modifies the auth module." {
		t.Errorf("Summary = %q", result.Summary)
	}
}

func TestParseAIResponse_RawJSON(t *testing.T) {
	// 没有 ```json 包裹的纯 JSON
	response := `{"affectedModules": [], "callChain": [], "designPatterns": [], "summary": "test"}`

	result := parseAIResponse(response)

	if result.Summary != "test" {
		t.Errorf("Summary = %q, want %q", result.Summary, "test")
	}
}

func TestParseAIResponse_InvalidJSON(t *testing.T) {
	response := "This is not JSON at all, just plain text analysis."

	result := parseAIResponse(response)

	// 应该 fallback 到将原始文本作为 summary
	if result.Summary == "" {
		t.Error("expected non-empty fallback summary")
	}
	if !strings.Contains(result.Summary, "not JSON") {
		t.Errorf("fallback summary should contain original text, got: %q", result.Summary)
	}
}

func TestParseAIResponse_LongFallback(t *testing.T) {
	// 超过 1000 字符的非 JSON 响应应被截断
	response := strings.Repeat("a", 2000)

	result := parseAIResponse(response)

	if len(result.Summary) > 1000 {
		t.Errorf("fallback summary should be truncated to 1000 chars, got %d", len(result.Summary))
	}
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
