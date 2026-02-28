package platform

import (
	"strings"
	"testing"
)

func TestParseDiffLines_SingleHunk(t *testing.T) {
	patch := `@@ -1,5 +1,7 @@ func example() {
 existing line
+new line 1
+new line 2
 another existing
 final line`

	got := ParseDiffLines(patch)

	want := map[int]bool{1: true, 2: true, 3: true, 4: true, 5: true}

	if len(got) != len(want) {
		t.Fatalf("got %d lines, want %d lines\ngot: %v", len(got), len(want), got)
	}
	for line := range want {
		if !got[line] {
			t.Errorf("expected line %d in result, got: %v", line, got)
		}
	}
}

func TestParseDiffLines_MultipleHunks(t *testing.T) {
	patch := `@@ -1,3 +1,4 @@ package main
 line one
+added in first hunk
 line two
@@ -20,3 +21,4 @@ func other() {
 existing
+added in second hunk
 end`

	got := ParseDiffLines(patch)

	wantLines := []int{1, 2, 3, 21, 22, 23}

	for _, line := range wantLines {
		if !got[line] {
			t.Errorf("expected line %d in result, got: %v", line, got)
		}
	}

	if len(got) != len(wantLines) {
		t.Errorf("got %d lines, want %d lines\ngot: %v", len(got), len(wantLines), got)
	}
}

func TestParseDiffLines_AdditionsOnly(t *testing.T) {
	patch := `@@ -0,0 +1,3 @@
+line one
+line two
+line three`

	got := ParseDiffLines(patch)

	want := map[int]bool{1: true, 2: true, 3: true}

	if len(got) != len(want) {
		t.Fatalf("got %d lines, want %d\ngot: %v", len(got), len(want), got)
	}
	for line := range want {
		if !got[line] {
			t.Errorf("expected line %d in result", line)
		}
	}
}

func TestParseDiffLines_DeletionsOnly(t *testing.T) {
	patch := `@@ -1,4 +1,2 @@ func foo() {
 keep this
-removed line one
-removed line two
 keep that`

	got := ParseDiffLines(patch)

	want := map[int]bool{1: true, 2: true}

	if len(got) != len(want) {
		t.Fatalf("got %d lines, want %d\ngot: %v", len(got), len(want), got)
	}
	for line := range want {
		if !got[line] {
			t.Errorf("expected line %d in result", line)
		}
	}
}

func TestParseDiffLines_EmptyPatch(t *testing.T) {
	got := ParseDiffLines("")

	if len(got) != 0 {
		t.Errorf("expected empty map for empty patch, got: %v", got)
	}
}

func TestParseDiffLines_ContextLines(t *testing.T) {
	patch := `@@ -5,4 +5,4 @@ func bar() {
 context line A
 context line B
 context line C
 context line D`

	got := ParseDiffLines(patch)

	want := map[int]bool{5: true, 6: true, 7: true, 8: true}

	if len(got) != len(want) {
		t.Fatalf("got %d lines, want %d\ngot: %v", len(got), len(want), got)
	}
	for line := range want {
		if !got[line] {
			t.Errorf("expected line %d in result", line)
		}
	}
}

func TestParseDiffLines_RealWorldDiff(t *testing.T) {
	patch := `@@ -10,6 +10,8 @@ func example() {
     existing line
+    new line 1
+    new line 2
     another existing
-    removed line
     final line`

	got := ParseDiffLines(patch)

	wantLines := []int{10, 11, 12, 13, 14}

	for _, line := range wantLines {
		if !got[line] {
			t.Errorf("expected line %d in result, got: %v", line, got)
		}
	}

	if len(got) != len(wantLines) {
		t.Errorf("got %d lines, want %d\ngot: %v", len(got), len(wantLines), got)
	}
}

func TestClassifyCommentsByDiff(t *testing.T) {
	line10 := 10
	line99 := 99

	diffInfo := map[string]map[int]bool{
		"main.go": {10: true, 11: true, 12: true},
		"util.go": {5: true, 6: true},
	}

	comments := []ReviewCommentInput{
		{Path: "main.go", Line: &line10, Body: "inline comment"},
		{Path: "main.go", Line: &line99, Body: "file-level comment"},
		{Path: "main.go", Line: nil, Body: "file-level no line"},
		{Path: "unknown.go", Line: &line10, Body: "global comment"},
	}

	classified := ClassifyCommentsByDiff(comments, diffInfo)

	if len(classified) != 4 {
		t.Fatalf("expected 4 classified comments, got %d", len(classified))
	}
	if classified[0].Mode != "inline" {
		t.Errorf("expected inline, got %s", classified[0].Mode)
	}
	// 行号不在 diff 范围内或无行号 → 自动分配第一个 diff 行，变为 inline
	if classified[1].Mode != "inline" {
		t.Errorf("expected inline (fallback to first diff line), got %s", classified[1].Mode)
	}
	if classified[2].Mode != "inline" {
		t.Errorf("expected inline (fallback to first diff line), got %s", classified[2].Mode)
	}
	if classified[3].Mode != "global" {
		t.Errorf("expected global, got %s", classified[3].Mode)
	}
}

// --- FindNearestLine 测试 ---

func TestFindNearestLine_ExactMatch(t *testing.T) {
	diffLines := map[int]bool{10: true, 20: true, 30: true}
	line, found := FindNearestLine(diffLines, 20, 20)
	if !found || line != 20 {
		t.Errorf("expected (20, true), got (%d, %v)", line, found)
	}
}

func TestFindNearestLine_NearbyMatch(t *testing.T) {
	diffLines := map[int]bool{10: true, 20: true, 30: true}
	line, found := FindNearestLine(diffLines, 25, 20)
	if !found || line != 20 {
		t.Errorf("expected nearest line 20, got (%d, %v)", line, found)
	}
}

func TestFindNearestLine_BeyondThreshold(t *testing.T) {
	diffLines := map[int]bool{10: true, 100: true}
	_, found := FindNearestLine(diffLines, 50, 20)
	if found {
		t.Error("expected no match beyond threshold")
	}
}

func TestFindNearestLine_EmptyDiffLines(t *testing.T) {
	_, found := FindNearestLine(map[int]bool{}, 10, 20)
	if found {
		t.Error("expected no match for empty diff lines")
	}
}

func TestFindNearestLine_PicksClosest(t *testing.T) {
	diffLines := map[int]bool{5: true, 15: true, 25: true}
	line, found := FindNearestLine(diffLines, 14, 20)
	if !found || line != 15 {
		t.Errorf("expected nearest line 15, got (%d, %v)", line, found)
	}
}

// --- ClassifyCommentsByDiff near-line matching 测试 ---

func TestClassifyCommentsByDiff_NearLineMatching(t *testing.T) {
	line15 := 15

	diffInfo := map[string]map[int]bool{
		"main.go": {10: true, 11: true, 12: true},
	}

	comments := []ReviewCommentInput{
		{Path: "main.go", Line: &line15, Body: "issue near diff"},
	}

	classified := ClassifyCommentsByDiff(comments, diffInfo)

	if len(classified) != 1 {
		t.Fatalf("expected 1 classified comment, got %d", len(classified))
	}
	if classified[0].Mode != "inline" {
		t.Errorf("expected inline via near-line matching, got %s", classified[0].Mode)
	}
	if *classified[0].Input.Line != 12 {
		t.Errorf("expected line to be remapped to 12, got %d", *classified[0].Input.Line)
	}
	if !strings.Contains(classified[0].Input.Body, "**Line 15:**") {
		t.Errorf("expected body to contain original line reference, got %s", classified[0].Input.Body)
	}
}

func TestClassifyCommentsByDiff_NearLineBeyondThreshold(t *testing.T) {
	line50 := 50

	diffInfo := map[string]map[int]bool{
		"main.go": {10: true, 11: true},
	}

	comments := []ReviewCommentInput{
		{Path: "main.go", Line: &line50, Body: "too far from diff"},
	}

	classified := ClassifyCommentsByDiff(comments, diffInfo)

	if len(classified) != 1 {
		t.Fatalf("expected 1, got %d", len(classified))
	}
	// Beyond ±20 threshold, should fall back to firstDiffLine
	if classified[0].Mode != "inline" {
		t.Errorf("expected inline (fallback to first diff line), got %s", classified[0].Mode)
	}
	if *classified[0].Input.Line != 10 {
		t.Errorf("expected fallback to first diff line 10, got %d", *classified[0].Input.Line)
	}
}

// --- resolvePathByDiff 测试 ---

func TestResolvePathByDiff_ExactMatch(t *testing.T) {
	diffInfo := map[string]map[int]bool{
		"backend/infrastructure/parsers/base_parser.py": {10: true},
	}
	got := resolvePathByDiff("backend/infrastructure/parsers/base_parser.py", diffInfo)
	if got != "backend/infrastructure/parsers/base_parser.py" {
		t.Errorf("exact match failed, got %q", got)
	}
}

func TestResolvePathByDiff_SuffixMatch(t *testing.T) {
	diffInfo := map[string]map[int]bool{
		"backend/infrastructure/parsers/base_parser.py": {10: true},
		"frontend/components/App.tsx":                   {1: true},
	}
	got := resolvePathByDiff("base_parser.py", diffInfo)
	if got != "backend/infrastructure/parsers/base_parser.py" {
		t.Errorf("suffix match failed, got %q", got)
	}
}

func TestResolvePathByDiff_AmbiguousSuffix(t *testing.T) {
	// 两个文件都以 base_parser.py 结尾，无法唯一匹配，应返回原路径
	diffInfo := map[string]map[int]bool{
		"backend/parsers/base_parser.py":  {10: true},
		"frontend/parsers/base_parser.py": {5: true},
	}
	got := resolvePathByDiff("base_parser.py", diffInfo)
	if got != "base_parser.py" {
		t.Errorf("ambiguous match should return original, got %q", got)
	}
}

func TestResolvePathByDiff_NoMatch(t *testing.T) {
	diffInfo := map[string]map[int]bool{
		"backend/main.go": {1: true},
	}
	got := resolvePathByDiff("unknown_file.py", diffInfo)
	if got != "unknown_file.py" {
		t.Errorf("no match should return original, got %q", got)
	}
}

func TestResolvePathByDiff_PartialNameNoMatch(t *testing.T) {
	// "parser.py" 不应匹配 "base_parser.py"（后缀匹配需要 /parser.py）
	diffInfo := map[string]map[int]bool{
		"backend/base_parser.py": {10: true},
	}
	got := resolvePathByDiff("parser.py", diffInfo)
	if got != "parser.py" {
		t.Errorf("partial name should not match, got %q", got)
	}
}

// --- ClassifyCommentsByDiff 路径解析集成测试 ---

func TestClassifyCommentsByDiff_ShortPathResolution(t *testing.T) {
	line10 := 10

	diffInfo := map[string]map[int]bool{
		"backend/infrastructure/parsers/base_parser.py": {10: true, 11: true},
		"frontend/components/App.tsx":                   {5: true},
	}

	comments := []ReviewCommentInput{
		// 短路径 + 行号在 diff 范围内 → 应该解析为 inline
		{Path: "base_parser.py", Line: &line10, Body: "issue in parser"},
		// 短路径 + 无行号 → 应该解析为 file
		{Path: "App.tsx", Line: nil, Body: "component issue"},
		// 无法匹配的路径 → global
		{Path: "unknown.py", Line: &line10, Body: "unknown file"},
	}

	classified := ClassifyCommentsByDiff(comments, diffInfo)

	if len(classified) != 3 {
		t.Fatalf("expected 3, got %d", len(classified))
	}
	if classified[0].Mode != "inline" {
		t.Errorf("short path with matching line should be inline, got %s", classified[0].Mode)
	}
	if classified[0].Input.Path != "backend/infrastructure/parsers/base_parser.py" {
		t.Errorf("path should be resolved to full path, got %s", classified[0].Input.Path)
	}
	if classified[1].Mode != "inline" {
		t.Errorf("short path without line should be inline (fallback), got %s", classified[1].Mode)
	}
	if classified[2].Mode != "global" {
		t.Errorf("unknown path should be global, got %s", classified[2].Mode)
	}
}

// --- AnnotateDiffWithLineNumbers 测试 ---

func TestAnnotateDiffWithLineNumbers(t *testing.T) {
	diff := `diff --git a/main.go b/main.go
index abc..def 100644
--- a/main.go
+++ b/main.go
@@ -10,6 +10,8 @@ func example() {
 existing line
+new line 1
+new line 2
 another existing
-removed line
 final line`

	got := AnnotateDiffWithLineNumbers(diff)

	// 文件头应该原样保留
	if !strings.Contains(got, "diff --git a/main.go b/main.go") {
		t.Error("file header should be preserved")
	}
	// hunk header 原样保留
	if !strings.Contains(got, "@@ -10,6 +10,8 @@ func example()") {
		t.Error("hunk header should be preserved")
	}
	// context 行带行号
	if !strings.Contains(got, "  10: existing line") {
		t.Errorf("context line should have line number 10, got:\n%s", got)
	}
	// 新增行带行号
	if !strings.Contains(got, "  11:+new line 1") {
		t.Errorf("added line should have line number 11, got:\n%s", got)
	}
	if !strings.Contains(got, "  12:+new line 2") {
		t.Errorf("added line should have line number 12, got:\n%s", got)
	}
	// 删除行没有行号
	if !strings.Contains(got, "    :-removed line") {
		t.Errorf("deleted line should have no line number, got:\n%s", got)
	}
	// 删除行后面的 context 行号不受影响
	if !strings.Contains(got, "  14: final line") {
		t.Errorf("line after delete should be 14, got:\n%s", got)
	}
}

func TestAnnotateDiffWithLineNumbers_MultipleFiles(t *testing.T) {
	diff := `diff --git a/a.go b/a.go
--- a/a.go
+++ b/a.go
@@ -1,3 +1,4 @@
 line one
+added
 line two
diff --git a/b.go b/b.go
--- a/b.go
+++ b/b.go
@@ -100,3 +100,3 @@
 existing
-old
+new`

	got := AnnotateDiffWithLineNumbers(diff)

	// 第一个文件: 行号从 1 开始
	if !strings.Contains(got, "   1: line one") {
		t.Errorf("first file should start at line 1, got:\n%s", got)
	}
	// 第二个文件: 行号从 100 开始
	if !strings.Contains(got, " 100: existing") {
		t.Errorf("second file should start at line 100, got:\n%s", got)
	}
	// -old 不递增行号，+new 就是 101
	if !strings.Contains(got, " 101:+new") {
		t.Errorf("replacement line should be 101, got:\n%s", got)
	}
}

func TestIsDuplicateComment(t *testing.T) {
	line5 := 5

	existing := []ExistingComment{
		{Path: "main.go", Line: &line5, Body: "This is a duplicate comment body"},
	}

	// Should be duplicate
	dup := ReviewCommentInput{Path: "main.go", Line: &line5, Body: "This is a duplicate comment body"}
	if !IsDuplicateComment(dup, existing) {
		t.Error("expected duplicate, got not duplicate")
	}

	// Different path
	diffPath := ReviewCommentInput{Path: "other.go", Line: &line5, Body: "This is a duplicate comment body"}
	if IsDuplicateComment(diffPath, existing) {
		t.Error("expected not duplicate for different path")
	}

	// Different line
	line10 := 10
	diffLine := ReviewCommentInput{Path: "main.go", Line: &line10, Body: "This is a duplicate comment body"}
	if IsDuplicateComment(diffLine, existing) {
		t.Error("expected not duplicate for different line")
	}

	// Different body
	diffBody := ReviewCommentInput{Path: "main.go", Line: &line5, Body: "Completely different body"}
	if IsDuplicateComment(diffBody, existing) {
		t.Error("expected not duplicate for different body")
	}
}

func TestTruncStr(t *testing.T) {
	if TruncStr("hello", 10) != "hello" {
		t.Error("short string should not be truncated")
	}
	if TruncStr("hello world", 5) != "hello" {
		t.Errorf("expected 'hello', got '%s'", TruncStr("hello world", 5))
	}
	if TruncStr("", 5) != "" {
		t.Error("empty string should stay empty")
	}
}

func TestFormatIssueBody(t *testing.T) {
	issue := IssueForComment{
		Title:        "Missing error check",
		Description:  "The error is not handled",
		Severity:     "high",
		SuggestedFix: "Add error handling",
		RaisedBy:     "claude, codex",
	}
	body := FormatIssueBody(issue)

	if body == "" {
		t.Fatal("expected non-empty body")
	}
	if !contains(body, "🟠") {
		t.Error("expected orange badge for high severity")
	}
	if !contains(body, "Missing error check") {
		t.Error("expected title in body")
	}
	if !contains(body, "Add error handling") {
		t.Error("expected suggested fix in body")
	}
	if !contains(body, "claude, codex") {
		t.Error("expected raised by in body")
	}
}

func TestSeverityToBadge(t *testing.T) {
	tests := map[string]string{
		"critical": "🔴",
		"high":     "🟠",
		"medium":   "🟡",
		"low":      "🟢",
		"unknown":  "⚪",
	}
	for sev, want := range tests {
		got := SeverityToBadge(sev)
		if got != want {
			t.Errorf("SeverityToBadge(%q) = %q, want %q", sev, got, want)
		}
	}
}

func contains(s, substr string) bool {
	return len(s) >= len(substr) && (s == substr || len(s) > 0 && containsImpl(s, substr))
}

func containsImpl(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}

// --- Issue Marker 幂等去重测试 ---

func TestBuildIssueMarker_Deterministic(t *testing.T) {
	line := 42
	m1 := BuildIssueMarker("main.go", &line, "high", "SQL injection")
	m2 := BuildIssueMarker("main.go", &line, "high", "SQL injection")
	if m1 != m2 {
		t.Fatalf("same input should produce same marker: %q vs %q", m1, m2)
	}
	if !strings.HasPrefix(m1, IssueMarkerPrefix) {
		t.Fatalf("marker should start with prefix, got: %q", m1)
	}
	if !strings.HasSuffix(m1, "-->") {
		t.Fatalf("marker should end with -->, got: %q", m1)
	}
}

func TestBuildIssueMarker_DifferentInputs(t *testing.T) {
	line := 42
	m1 := BuildIssueMarker("main.go", &line, "high", "SQL injection")
	m2 := BuildIssueMarker("main.go", &line, "high", "XSS vulnerability")
	if m1 == m2 {
		t.Fatal("different titles should produce different markers")
	}
}

func TestBuildIssueMarker_NilLine(t *testing.T) {
	m := BuildIssueMarker("main.go", nil, "medium", "issue")
	if m == "" {
		t.Fatal("marker should not be empty for nil line")
	}
}

func TestFormatIssueBody_ContainsMarker(t *testing.T) {
	line := 10
	issue := IssueForComment{
		File:     "main.go",
		Line:     &line,
		Title:    "Missing error check",
		Severity: "high",
	}
	body := FormatIssueBody(issue)
	if _, ok := ParseCodeDebateMeta(body); !ok {
		t.Fatal("formatted body should contain structured issue marker")
	}
	if !strings.Contains(body, "Missing error check") {
		t.Fatal("formatted body should still contain title")
	}
}

func TestIsDuplicateComment_MarkerMatch(t *testing.T) {
	line10 := 10
	line12 := 12 // 不同行号

	issue := IssueForComment{
		File:     "main.go",
		Line:     &line10,
		Title:    "SQL injection",
		Severity: "high",
	}
	body := FormatIssueBody(issue)

	comment := ReviewCommentInput{Path: "main.go", Line: &line12, Body: body}
	existing := []ExistingComment{
		// 旧评论在 line 10，但 marker 相同
		{Path: "main.go", Line: &line10, Body: body},
	}

	// marker 匹配应该成功，即使行号不同
	if !IsDuplicateComment(comment, existing) {
		t.Fatal("expected duplicate via marker match despite different line numbers")
	}
}

func TestIsDuplicateComment_MarkerMismatch(t *testing.T) {
	line := 10
	issue1 := IssueForComment{File: "main.go", Line: &line, Title: "SQL injection", Severity: "high"}
	issue2 := IssueForComment{File: "main.go", Line: &line, Title: "XSS vulnerability", Severity: "high"}

	body1 := FormatIssueBody(issue1)
	body2 := FormatIssueBody(issue2)

	comment := ReviewCommentInput{Path: "main.go", Line: &line, Body: body1}
	existing := []ExistingComment{{Path: "main.go", Line: &line, Body: body2}}

	// 不同 marker，不应被判为重复
	if IsDuplicateComment(comment, existing) {
		t.Fatal("different markers should not be duplicate")
	}
}

func TestIsDuplicateComment_LegacyBodyPrefixStillWorks(t *testing.T) {
	line := 10
	body := "some old comment without marker"

	comment := ReviewCommentInput{Path: "main.go", Line: &line, Body: body}
	existing := []ExistingComment{{Path: "main.go", Line: &line, Body: body}}

	if !IsDuplicateComment(comment, existing) {
		t.Fatal("legacy body prefix matching should still detect duplicates")
	}
}
