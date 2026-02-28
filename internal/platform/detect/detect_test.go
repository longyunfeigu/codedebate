package detect

import (
	"testing"
)

func TestGitHubRemoteRegex(t *testing.T) {
	tests := []struct {
		url  string
		want bool
	}{
		{"git@github.com:owner/repo.git", true},
		{"https://github.com/owner/repo.git", true},
		{"https://github.com/owner/repo", true},
		{"git@gitlab.com:group/project.git", false},
		{"https://gitlab.com/group/project", false},
		{"git@example.com:org/repo.git", false},
	}

	for _, tt := range tests {
		got := githubRemoteRegex.MatchString(tt.url)
		if got != tt.want {
			t.Errorf("githubRemoteRegex.MatchString(%q) = %v, want %v", tt.url, got, tt.want)
		}
	}
}

func TestGitLabRemoteRegex(t *testing.T) {
	tests := []struct {
		url  string
		want bool
	}{
		{"git@gitlab.com:group/project.git", true},
		{"https://gitlab.com/group/project.git", true},
		{"https://gitlab.com/group/subgroup/project", true},
		{"git@github.com:owner/repo.git", false},
		{"https://github.com/owner/repo", false},
		{"git@example.com:org/repo.git", false},
	}

	for _, tt := range tests {
		got := gitlabRemoteRegex.MatchString(tt.url)
		if got != tt.want {
			t.Errorf("gitlabRemoteRegex.MatchString(%q) = %v, want %v", tt.url, got, tt.want)
		}
	}
}

func TestFromRemote_ExplicitType(t *testing.T) {
	// Explicit github type
	p, err := FromRemote("github", "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if p.Name() != "github" {
		t.Errorf("expected github, got %s", p.Name())
	}

	// Explicit gitlab type
	p, err = FromRemote("gitlab", "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if p.Name() != "gitlab" {
		t.Errorf("expected gitlab, got %s", p.Name())
	}

	// Explicit gitlab with custom host
	p, err = FromRemote("gitlab", "gitlab.company.com")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if p.Name() != "gitlab" {
		t.Errorf("expected gitlab, got %s", p.Name())
	}
}
