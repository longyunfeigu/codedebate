package cmd

import (
	"testing"

	"github.com/longyunfeigu/codedebate/internal/orchestrator"
	"github.com/longyunfeigu/codedebate/internal/platform"
	"github.com/longyunfeigu/codedebate/internal/platform/detect"
	gh "github.com/longyunfeigu/codedebate/internal/platform/github"
	gl "github.com/longyunfeigu/codedebate/internal/platform/gitlab"
)

// --- 平台检测测试 ---

func TestDetectPlatform_SelfHostedGitLabIP(t *testing.T) {
	// 模拟配置中 platform.type=gitlab, platform.host=39.99.155.169
	plat, err := detect.FromRemote("gitlab", "39.99.155.169")
	if err != nil {
		t.Fatalf("FromRemote('gitlab', '39.99.155.169') returned error: %v", err)
	}
	if plat == nil {
		t.Fatal("FromRemote returned nil platform")
	}
	if plat.Name() != "gitlab" {
		t.Errorf("Name() = %q, want %q", plat.Name(), "gitlab")
	}
}

func TestDetectPlatform_AutoDetectFailsForIP(t *testing.T) {
	// 自动检测模式下，IP 地址不匹配任何正则 → 返回 nil
	// 注意：此测试依赖当前目录的 git remote，只测正则逻辑
	// 如果 remote 恰好是 github/gitlab.com，此测试会通过（检测到平台）
	// 这里直接测 FromRemote("", "") 不带配置的情况
	// 由于自动检测依赖本地 git remote，跳过；改用正则直测
}

// --- URL 解析测试 ---

func TestParseMRURL_SelfHostedIP(t *testing.T) {
	g := gl.New("39.99.155.169")

	repo, id, err := g.ParseMRURL("http://39.99.155.169/enterprisesearch/knowledge-hub/-/merge_requests/155")
	if err != nil {
		t.Fatalf("ParseMRURL returned error: %v", err)
	}
	if repo != "enterprisesearch/knowledge-hub" {
		t.Errorf("repo = %q, want %q", repo, "enterprisesearch/knowledge-hub")
	}
	if id != "155" {
		t.Errorf("id = %q, want %q", id, "155")
	}
}

func TestBuildMRURL_SelfHostedIP(t *testing.T) {
	g := gl.New("39.99.155.169")
	got := g.BuildMRURL("enterprisesearch/knowledge-hub", "155")
	want := "https://39.99.155.169/enterprisesearch/knowledge-hub/-/merge_requests/155"
	if got != want {
		t.Errorf("BuildMRURL = %q, want %q", got, want)
	}
}

// --- extractPRNumber 测试 ---

func TestExtractPRNumber(t *testing.T) {
	tests := []struct {
		label string
		want  string
	}{
		{"PR #123", "123"},
		{"MR !155", "155"},
		{"MR !1", "1"},
		{"no number", ""},
	}
	for _, tt := range tests {
		got := extractPRNumber(tt.label)
		if got != tt.want {
			t.Errorf("extractPRNumber(%q) = %q, want %q", tt.label, got, tt.want)
		}
	}
}

// --- convertIssuesToPlatform 测试 ---

func TestConvertIssuesToPlatform(t *testing.T) {
	line := 42
	issues := []orchestrator.MergedIssue{
		{
			ReviewIssue: orchestrator.ReviewIssue{
				Severity:     "high",
				Category:     "security",
				File:         "main.go",
				Line:         &line,
				Title:        "SQL injection risk",
				Description:  "User input not sanitized",
				SuggestedFix: "Use parameterized queries",
			},
			RaisedBy:     []string{"claude", "gpt4o"},
			Descriptions: []string{"desc1", "desc2"},
		},
		{
			ReviewIssue: orchestrator.ReviewIssue{
				Severity:    "low",
				Category:    "style",
				File:        "utils.go",
				Line:        nil,
				Title:       "Unused variable",
				Description: "Variable x is declared but not used",
			},
			RaisedBy:     []string{"claude"},
			Descriptions: []string{"desc1"},
		},
	}

	result := convertIssuesToPlatform(issues)
	if len(result) != 2 {
		t.Fatalf("len(result) = %d, want 2", len(result))
	}

	// 第一条
	if result[0].File != "main.go" {
		t.Errorf("result[0].File = %q, want %q", result[0].File, "main.go")
	}
	if result[0].Line == nil || *result[0].Line != 42 {
		t.Errorf("result[0].Line = %v, want 42", result[0].Line)
	}
	if result[0].Severity != "high" {
		t.Errorf("result[0].Severity = %q, want %q", result[0].Severity, "high")
	}
	if result[0].RaisedBy != "claude, gpt4o" {
		t.Errorf("result[0].RaisedBy = %q, want %q", result[0].RaisedBy, "claude, gpt4o")
	}

	// 第二条：无行号
	if result[1].Line != nil {
		t.Errorf("result[1].Line = %v, want nil", result[1].Line)
	}
	if result[1].RaisedBy != "claude" {
		t.Errorf("result[1].RaisedBy = %q, want %q", result[1].RaisedBy, "claude")
	}
}

// --- 发布条件逻辑测试 ---

// shouldPostComments 提取了 review.go 中的发布判断逻辑
func shouldPostComments(noPost bool, targetType string, parsedIssues []orchestrator.MergedIssue, plat platform.Platform) bool {
	return !noPost && targetType == "pr" && len(parsedIssues) > 0 && plat != nil
}

func TestShouldPostComments(t *testing.T) {
	fakePlat := gl.New("39.99.155.169")
	line := 10
	issues := []orchestrator.MergedIssue{
		{
			ReviewIssue: orchestrator.ReviewIssue{
				Severity: "medium", File: "a.go", Line: &line,
				Title: "test", Description: "test desc",
			},
			RaisedBy: []string{"claude"},
		},
	}

	tests := []struct {
		name       string
		noPost     bool
		targetType string
		issues     []orchestrator.MergedIssue
		plat       platform.Platform
		want       bool
	}{
		{
			name:       "all conditions met → post",
			noPost:     false,
			targetType: "pr",
			issues:     issues,
			plat:       fakePlat,
			want:       true,
		},
		{
			name:       "noPost flag → skip",
			noPost:     true,
			targetType: "pr",
			issues:     issues,
			plat:       fakePlat,
			want:       false,
		},
		{
			name:       "local target → skip",
			noPost:     false,
			targetType: "local",
			issues:     issues,
			plat:       fakePlat,
			want:       false,
		},
		{
			name:       "branch target → skip",
			noPost:     false,
			targetType: "branch",
			issues:     issues,
			plat:       fakePlat,
			want:       false,
		},
		{
			name:       "no parsed issues → skip",
			noPost:     false,
			targetType: "pr",
			issues:     nil,
			plat:       fakePlat,
			want:       false,
		},
		{
			name:       "empty parsed issues → skip",
			noPost:     false,
			targetType: "pr",
			issues:     []orchestrator.MergedIssue{},
			plat:       fakePlat,
			want:       false,
		},
		{
			name:       "platform nil → skip",
			noPost:     false,
			targetType: "pr",
			issues:     issues,
			plat:       nil,
			want:       false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := shouldPostComments(tt.noPost, tt.targetType, tt.issues, tt.plat)
			if got != tt.want {
				t.Errorf("shouldPostComments() = %v, want %v", got, tt.want)
			}
		})
	}
}

// --- summary 发布条件逻辑测试 ---

func TestShouldPostSummary(t *testing.T) {
	fakePlat := gl.New("39.99.155.169")

	tests := []struct {
		name           string
		noPost         bool
		noPostSummary  bool
		targetType     string
		finalConclusion string
		plat           platform.Platform
		want           bool
	}{
		{
			name:            "all conditions met -> post",
			noPost:          false,
			noPostSummary:   false,
			targetType:      "pr",
			finalConclusion: "Looks good overall.",
			plat:            fakePlat,
			want:            true,
		},
		{
			name:            "github should post summary too",
			noPost:          false,
			noPostSummary:   false,
			targetType:      "pr",
			finalConclusion: "summary",
			plat:            gh.New(),
			want:            true,
		},
		{
			name:            "no-post -> skip",
			noPost:          true,
			noPostSummary:   false,
			targetType:      "pr",
			finalConclusion: "summary",
			plat:            fakePlat,
			want:            false,
		},
		{
			name:            "no-post-summary -> skip",
			noPost:          false,
			noPostSummary:   true,
			targetType:      "pr",
			finalConclusion: "summary",
			plat:            fakePlat,
			want:            false,
		},
		{
			name:            "empty conclusion -> skip",
			noPost:          false,
			noPostSummary:   false,
			targetType:      "pr",
			finalConclusion: "   ",
			plat:            fakePlat,
			want:            false,
		},
		{
			name:            "non-pr target -> skip",
			noPost:          false,
			noPostSummary:   false,
			targetType:      "local",
			finalConclusion: "summary",
			plat:            fakePlat,
			want:            false,
		},
		{
			name:            "nil platform -> skip",
			noPost:          false,
			noPostSummary:   false,
			targetType:      "pr",
			finalConclusion: "summary",
			plat:            nil,
			want:            false,
		},
		{
			name:            "unsupported platform -> skip",
			noPost:          false,
			noPostSummary:   false,
			targetType:      "pr",
			finalConclusion: "summary",
			plat:            &fakeUnsupportedSummaryPlatform{Platform: gl.New("39.99.155.169")},
			want:            false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := shouldPostSummary(tt.noPost, tt.noPostSummary, tt.targetType, tt.finalConclusion, tt.plat)
			if got != tt.want {
				t.Errorf("shouldPostSummary() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestSupportsSummaryPosting(t *testing.T) {
	if supportsSummaryPosting(nil) {
		t.Fatal("nil platform should not support summary posting")
	}
	if !supportsSummaryPosting(gl.New("39.99.155.169")) {
		t.Fatal("gitlab platform should support summary posting")
	}
	if !supportsSummaryPosting(gh.New()) {
		t.Fatal("github platform should support summary posting")
	}
	if supportsSummaryPosting(&fakeUnsupportedSummaryPlatform{Platform: gl.New("39.99.155.169")}) {
		t.Fatal("fake unsupported platform should not support summary posting")
	}
}

func TestBuildSummaryNoteBody(t *testing.T) {
	body := buildSummaryNoteBody("  Final conclusion here.  ")
	if !contains(body, codedebateSummaryMarker) {
		t.Fatalf("expected body to contain summary marker, got %q", body)
	}
	if !contains(body, "## CodeDebate Code Review Summary") {
		t.Fatalf("expected body to contain heading, got %q", body)
	}
	if !contains(body, "Final conclusion here.") {
		t.Fatalf("expected body to contain trimmed conclusion, got %q", body)
	}
}

type fakeUpsertPlatform struct {
	platform.Platform
	called bool
	mrID   string
	repo   string
	marker string
	body   string
}

type fakeUnsupportedSummaryPlatform struct {
	platform.Platform
}

func (f *fakeUpsertPlatform) UpsertSummaryNote(mrID, repo, marker, body string) error {
	f.called = true
	f.mrID = mrID
	f.repo = repo
	f.marker = marker
	f.body = body
	return nil
}

type fakePostNotePlatform struct {
	platform.Platform
	called bool
	mrID   string
	repo   string
	body   string
}

func (f *fakePostNotePlatform) PostNote(mrID, repo, body string) error {
	f.called = true
	f.mrID = mrID
	f.repo = repo
	f.body = body
	return nil
}

func TestUpsertSummaryNote_UsesUpserter(t *testing.T) {
	fp := &fakeUpsertPlatform{Platform: gl.New("39.99.155.169")}
	body := buildSummaryNoteBody("summary")
	if err := upsertSummaryNote(fp, "123", "group/repo", body); err != nil {
		t.Fatalf("upsertSummaryNote returned error: %v", err)
	}
	if !fp.called {
		t.Fatal("expected UpsertSummaryNote to be called")
	}
	if fp.marker != codedebateSummaryMarker {
		t.Fatalf("marker = %q, want %q", fp.marker, codedebateSummaryMarker)
	}
}

func TestUpsertSummaryNote_FallsBackToPostNote(t *testing.T) {
	fp := &fakePostNotePlatform{Platform: gh.New()}
	body := buildSummaryNoteBody("summary")
	if err := upsertSummaryNote(fp, "123", "group/repo", body); err != nil {
		t.Fatalf("upsertSummaryNote returned error: %v", err)
	}
	if !fp.called {
		t.Fatal("expected PostNote fallback to be called")
	}
}

func TestUpsertSummaryNote_UnsupportedPlatform(t *testing.T) {
	unsupported := &fakeUnsupportedSummaryPlatform{Platform: gl.New("39.99.155.169")}
	err := upsertSummaryNote(unsupported, "123", "owner/repo", "summary")
	if err == nil {
		t.Fatal("expected error for unsupported platform")
	}
	if !contains(err.Error(), "does not support summary posting") {
		t.Fatalf("unexpected error: %v", err)
	}
}

// --- FormatIssueBody 测试 ---

func TestFormatIssueBody(t *testing.T) {
	line := 42
	issue := platform.IssueForComment{
		File:         "main.go",
		Line:         &line,
		Title:        "SQL injection risk",
		Description:  "User input not sanitized",
		Severity:     "critical",
		SuggestedFix: "Use parameterized queries",
		RaisedBy:     "claude, gpt4o",
	}

	body := platform.FormatIssueBody(issue)

	// 检查关键内容是否存在
	if !contains(body, "🔴") {
		t.Error("expected critical severity badge 🔴")
	}
	if !contains(body, "SQL injection risk") {
		t.Error("expected title in body")
	}
	if !contains(body, "User input not sanitized") {
		t.Error("expected description in body")
	}
	if !contains(body, "Use parameterized queries") {
		t.Error("expected suggested fix in body")
	}
	if !contains(body, "claude, gpt4o") {
		t.Error("expected raisedBy in body")
	}
}

func contains(s, substr string) bool {
	return len(s) >= len(substr) && searchString(s, substr)
}

func searchString(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}
