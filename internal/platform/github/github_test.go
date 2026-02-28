package github

import (
	"testing"
)

var (
	_ interface {
		PostNote(mrID, repo, body string) error
	} = (*GitHubPlatform)(nil)
	_ interface {
		UpsertSummaryNote(mrID, repo, marker, body string) error
	} = (*GitHubPlatform)(nil)
)

func TestParseMRURL(t *testing.T) {
	g := New()

	tests := []struct {
		url      string
		wantRepo string
		wantID   string
		wantErr  bool
	}{
		{
			url:      "https://github.com/owner/repo/pull/123",
			wantRepo: "owner/repo",
			wantID:   "123",
		},
		{
			url:      "https://github.com/org/project/pull/456",
			wantRepo: "org/project",
			wantID:   "456",
		},
		{
			url:     "https://gitlab.com/group/project/-/merge_requests/789",
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
	g := New()

	tests := []struct {
		repo    string
		mrID    string
		wantURL string
	}{
		{"owner/repo", "123", "https://github.com/owner/repo/pull/123"},
		{"org/project", "456", "https://github.com/org/project/pull/456"},
	}

	for _, tt := range tests {
		got := g.BuildMRURL(tt.repo, tt.mrID)
		if got != tt.wantURL {
			t.Errorf("BuildMRURL(%q, %q) = %q, want %q", tt.repo, tt.mrID, got, tt.wantURL)
		}
	}
}

func TestName(t *testing.T) {
	g := New()
	if g.Name() != "github" {
		t.Errorf("Name() = %q, want %q", g.Name(), "github")
	}
}

func TestValidatePRNumber(t *testing.T) {
	tests := []struct {
		input   string
		wantErr bool
	}{
		{"123", false},
		{"1", false},
		{"abc", true},
		{"12a", true},
		{"", true},
	}

	for _, tt := range tests {
		err := validatePRNumber(tt.input)
		if tt.wantErr && err == nil {
			t.Errorf("validatePRNumber(%q) expected error", tt.input)
		}
		if !tt.wantErr && err != nil {
			t.Errorf("validatePRNumber(%q) unexpected error: %v", tt.input, err)
		}
	}
}

func TestFindLatestIssueCommentIDByMarker(t *testing.T) {
	comments := []issueComment{
		{ID: 11, Body: "normal comment"},
		{ID: 12, Body: "<!-- codedebate:summary -->\nold summary"},
		{ID: 18, Body: "another comment"},
		{ID: 20, Body: "<!-- codedebate:summary -->\nnew summary"},
	}

	if got := findLatestIssueCommentIDByMarker(comments, ""); got != 0 {
		t.Fatalf("empty marker should return 0, got %d", got)
	}

	if got := findLatestIssueCommentIDByMarker(comments, "<!-- codedebate:summary -->"); got != 20 {
		t.Fatalf("expected latest marker comment ID 20, got %d", got)
	}

	if got := findLatestIssueCommentIDByMarker(comments, "<!-- not-exist -->"); got != 0 {
		t.Fatalf("marker not found should return 0, got %d", got)
	}
}
