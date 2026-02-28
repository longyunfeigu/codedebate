package platform

import (
	"strings"
	"testing"
)

func TestFilterDiff_ExcludesBuiltinPatterns(t *testing.T) {
	diff := `diff --git a/main.go b/main.go
--- a/main.go
+++ b/main.go
@@ -1,3 +1,4 @@
 package main
+import "fmt"
 func main() {}
diff --git a/api.pb.go b/api.pb.go
--- a/api.pb.go
+++ b/api.pb.go
@@ -1,3 +1,4 @@
 // generated code
+// new line
 package api
diff --git a/go.sum b/go.sum
--- a/go.sum
+++ b/go.sum
@@ -1,2 +1,3 @@
 module1 v1.0.0 h1:abc
+module2 v2.0.0 h1:def
`

	filtered := FilterDiff(diff, nil)

	if !strings.Contains(filtered, "main.go") {
		t.Error("expected main.go to be kept")
	}
	if strings.Contains(filtered, "api.pb.go") {
		t.Error("expected api.pb.go to be filtered out")
	}
	if strings.Contains(filtered, "go.sum") {
		t.Error("expected go.sum to be filtered out")
	}
}

func TestFilterDiff_UserPatterns(t *testing.T) {
	diff := `diff --git a/src/main.go b/src/main.go
--- a/src/main.go
+++ b/src/main.go
@@ -1,3 +1,4 @@
 package main
+import "fmt"
 func main() {}
diff --git a/docs/api.md b/docs/api.md
--- a/docs/api.md
+++ b/docs/api.md
@@ -1,2 +1,3 @@
 # API
+new section
`

	filtered := FilterDiff(diff, []string{"docs/**"})

	if !strings.Contains(filtered, "main.go") {
		t.Error("expected main.go to be kept")
	}
	if strings.Contains(filtered, "docs/api.md") {
		t.Error("expected docs/api.md to be filtered by user pattern")
	}
}

func TestFilterDiff_EmptyDiff(t *testing.T) {
	result := FilterDiff("", nil)
	if result != "" {
		t.Errorf("expected empty result, got %q", result)
	}
}

func TestSplitDiffByFile(t *testing.T) {
	diff := `diff --git a/a.go b/a.go
content a
diff --git a/b.go b/b.go
content b
`
	sections := SplitDiffByFile(diff)
	if len(sections) != 2 {
		t.Fatalf("expected 2 sections, got %d", len(sections))
	}
	if !strings.Contains(sections[0], "a.go") {
		t.Error("first section should contain a.go")
	}
	if !strings.Contains(sections[1], "b.go") {
		t.Error("second section should contain b.go")
	}
}

func TestExtractFilePath(t *testing.T) {
	tests := []struct {
		section string
		want    string
	}{
		{"diff --git a/src/main.go b/src/main.go\n@@ -1,3 +1,4 @@", "src/main.go"},
		{"diff --git a/file.pb.go b/file.pb.go\n", "file.pb.go"},
		{"not a diff line\n", ""},
	}
	for _, tt := range tests {
		got := ExtractFilePath(tt.section)
		if got != tt.want {
			t.Errorf("ExtractFilePath(%q) = %q, want %q", tt.section[:30], got, tt.want)
		}
	}
}

func TestShouldExclude(t *testing.T) {
	tests := []struct {
		path    string
		want    bool
	}{
		{"api.pb.go", true},
		{"internal/api.pb.go", true},
		{"main.go", false},
		{"vendor/lib/pkg.go", true},
		{"go.sum", true},
		{"package-lock.json", true},
		{"yarn.lock", true},
		{"pnpm-lock.yaml", true},
		{"src/util.go", false},
		{"types_generated.go", true},       // matches *_generated.*
		{"api.generated.ts", true},          // matches *.generated.*
		{"src/generated/models.go", true},   // matches **/generated/**
		{"code_generator.go", false},        // should NOT match — not a generated file
		{"generate_test.go", false},         // should NOT match — not a generated file
	}
	for _, tt := range tests {
		got := ShouldExclude(tt.path, BuiltinExcludePatterns)
		if got != tt.want {
			t.Errorf("ShouldExclude(%q) = %v, want %v", tt.path, got, tt.want)
		}
	}
}
