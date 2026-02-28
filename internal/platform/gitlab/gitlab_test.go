package gitlab

import (
	"testing"

	"github.com/longyunfeigu/codedebate/internal/platform"
)

func TestParseMRURL(t *testing.T) {
	g := New("")

	tests := []struct {
		url      string
		wantRepo string
		wantID   string
		wantErr  bool
	}{
		{
			url:      "https://gitlab.com/group/project/-/merge_requests/123",
			wantRepo: "group/project",
			wantID:   "123",
		},
		{
			url:      "https://gitlab.com/group/subgroup/project/-/merge_requests/456",
			wantRepo: "group/subgroup/project",
			wantID:   "456",
		},
		{
			url:      "https://gitlab.company.com/org/team/repo/-/merge_requests/789",
			wantRepo: "org/team/repo",
			wantID:   "789",
		},
		{
			url:      "https://gitlab.com/a/b/c/d/project/-/merge_requests/1",
			wantRepo: "a/b/c/d/project",
			wantID:   "1",
		},
		{
			url:     "https://github.com/owner/repo/pull/123",
			wantErr: true,
		},
		{
			url:     "not-a-url",
			wantErr: true,
		},
	}

	for _, tt := range tests {
		repo, id, err := g.ParseMRURL(tt.url)
		if tt.wantErr {
			if err == nil {
				t.Errorf("ParseMRURL(%q) expected error, got repo=%q id=%q", tt.url, repo, id)
			}
			continue
		}
		if err != nil {
			t.Errorf("ParseMRURL(%q) unexpected error: %v", tt.url, err)
			continue
		}
		if repo != tt.wantRepo {
			t.Errorf("ParseMRURL(%q) repo = %q, want %q", tt.url, repo, tt.wantRepo)
		}
		if id != tt.wantID {
			t.Errorf("ParseMRURL(%q) id = %q, want %q", tt.url, id, tt.wantID)
		}
	}
}

func TestBuildMRURL(t *testing.T) {
	tests := []struct {
		host     string
		repo     string
		mrID     string
		wantURL  string
	}{
		{"", "group/project", "123", "https://gitlab.com/group/project/-/merge_requests/123"},
		{"gitlab.company.com", "org/repo", "456", "https://gitlab.company.com/org/repo/-/merge_requests/456"},
		{"", "a/b/c/d/project", "1", "https://gitlab.com/a/b/c/d/project/-/merge_requests/1"},
	}

	for _, tt := range tests {
		g := New(tt.host)
		got := g.BuildMRURL(tt.repo, tt.mrID)
		if got != tt.wantURL {
			t.Errorf("BuildMRURL(%q, %q) = %q, want %q", tt.repo, tt.mrID, got, tt.wantURL)
		}
	}
}

func TestEncodeProject(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"group/project", "group%2Fproject"},
		{"a/b/c/project", "a%2Fb%2Fc%2Fproject"},
		{"simple", "simple"},
	}

	for _, tt := range tests {
		got := encodeProject(tt.input)
		if got != tt.want {
			t.Errorf("encodeProject(%q) = %q, want %q", tt.input, got, tt.want)
		}
	}
}

func TestName(t *testing.T) {
	g := New("")
	if g.Name() != "gitlab" {
		t.Errorf("Name() = %q, want %q", g.Name(), "gitlab")
	}
}

func TestGetHost(t *testing.T) {
	g1 := New("")
	if g1.getHost() != "gitlab.com" {
		t.Errorf("getHost() = %q, want %q", g1.getHost(), "gitlab.com")
	}

	g2 := New("gitlab.company.com")
	if g2.getHost() != "gitlab.company.com" {
		t.Errorf("getHost() = %q, want %q", g2.getHost(), "gitlab.company.com")
	}
}

func TestBuildTextPosition_IncludesOldPath(t *testing.T) {
	commitInfo := platform.CommitInfo{
		HeadSHA:  "head",
		BaseSHA:  "base",
		StartSHA: "start",
	}
	pos := buildTextPosition("backend/a.go", 42, nil, commitInfo)

	if got, _ := pos["position_type"].(string); got != "text" {
		t.Fatalf("position_type = %q, want %q", got, "text")
	}
	if got, _ := pos["new_path"].(string); got != "backend/a.go" {
		t.Fatalf("new_path = %q, want %q", got, "backend/a.go")
	}
	if got, _ := pos["old_path"].(string); got != "backend/a.go" {
		t.Fatalf("old_path = %q, want %q", got, "backend/a.go")
	}
	if got, ok := pos["new_line"].(int); !ok || got != 42 {
		t.Fatalf("new_line = %v (ok=%v), want %d", pos["new_line"], ok, 42)
	}
}

func TestBuildFilePosition_IncludesOldPath(t *testing.T) {
	commitInfo := platform.CommitInfo{
		HeadSHA:  "head",
		BaseSHA:  "base",
		StartSHA: "start",
	}
	pos := buildFilePosition("backend/a.go", commitInfo)

	if got, _ := pos["position_type"].(string); got != "file" {
		t.Fatalf("position_type = %q, want %q", got, "file")
	}
	if got, _ := pos["new_path"].(string); got != "backend/a.go" {
		t.Fatalf("new_path = %q, want %q", got, "backend/a.go")
	}
	if got, _ := pos["old_path"].(string); got != "backend/a.go" {
		t.Fatalf("old_path = %q, want %q", got, "backend/a.go")
	}
}
