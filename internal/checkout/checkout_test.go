package checkout

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/longyunfeigu/codedebate/internal/config"
)

func TestNewManager_Disabled(t *testing.T) {
	if m := NewManager(nil); m != nil {
		t.Fatal("expected nil manager for nil config")
	}
	if m := NewManager(&config.CheckoutConfig{Enabled: false}); m != nil {
		t.Fatal("expected nil manager when checkout is disabled")
	}
}

func TestNewManager_Defaults(t *testing.T) {
	m := NewManager(&config.CheckoutConfig{Enabled: true})
	if m == nil {
		t.Fatal("expected non-nil manager")
	}

	home, _ := os.UserHomeDir()
	wantBaseDir := filepath.Join(home, ".codedebate", "repos")
	if m.baseDir != wantBaseDir {
		t.Fatalf("baseDir = %q, want %q", m.baseDir, wantBaseDir)
	}
	if m.ttl != 24*time.Hour {
		t.Fatalf("ttl = %v, want 24h", m.ttl)
	}
}

func TestBuildCloneURL(t *testing.T) {
	tests := []struct {
		name     string
		platform string
		repo     string
		host     string
		want     string
	}{
		{
			name:     "github default host",
			platform: "github",
			repo:     "owner/repo",
			want:     "https://github.com/owner/repo.git",
		},
		{
			name:     "gitlab default host",
			platform: "gitlab",
			repo:     "group/project",
			want:     "https://gitlab.com/group/project.git",
		},
		{
			name:     "custom https host",
			platform: "gitlab",
			repo:     "group/project",
			host:     "gitlab.company.com",
			want:     "https://gitlab.company.com/group/project.git",
		},
		{
			name:     "file scheme host",
			platform: "github",
			repo:     "owner/repo",
			host:     "file:///tmp/repos",
			want:     "file:///tmp/repos/owner/repo.git",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := buildCloneURL(tt.platform, tt.repo, tt.host)
			if got != tt.want {
				t.Fatalf("buildCloneURL() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestMirrorDir(t *testing.T) {
	m := &Manager{baseDir: "/tmp/codedebate-test-repos"}
	got := m.mirrorDir(Params{
		Platform: "github",
		Repo:     "owner/repo",
	})
	want := filepath.Join("/tmp/codedebate-test-repos", "github", "owner", "repo.git")
	if got != want {
		t.Fatalf("mirrorDir() = %q, want %q", got, want)
	}
}

func TestConcurrentCheckout_Isolation(t *testing.T) {
	fixture := setupRemoteFixture(t)
	m := NewManager(&config.CheckoutConfig{
		Enabled: true,
		BaseDir: t.TempDir(),
		TTL:     "24h",
	})

	params1 := Params{
		Platform: "github",
		Repo:     fixture.repo,
		MRNumber: "101",
		Host:     "file://" + fixture.hostDir,
	}
	params2 := Params{
		Platform: "github",
		Repo:     fixture.repo,
		MRNumber: "102",
		Host:     "file://" + fixture.hostDir,
	}

	var r1, r2 Result
	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		r1 = m.Checkout(params1)
	}()
	go func() {
		defer wg.Done()
		r2 = m.Checkout(params2)
	}()
	wg.Wait()
	defer r1.Release()
	defer r2.Release()

	if !r1.Available || !r2.Available {
		t.Fatalf("expected both checkouts available, got r1=%v r2=%v", r1.Available, r2.Available)
	}
	if r1.RepoDir == r2.RepoDir {
		t.Fatalf("worktree dirs should differ, both are %q", r1.RepoDir)
	}
}

func TestRelease_CleansWorktree(t *testing.T) {
	fixture := setupRemoteFixture(t)
	m := NewManager(&config.CheckoutConfig{
		Enabled: true,
		BaseDir: t.TempDir(),
		TTL:     "24h",
	})
	params := Params{
		Platform: "github",
		Repo:     fixture.repo,
		MRNumber: "101",
		Host:     "file://" + fixture.hostDir,
	}

	result := m.Checkout(params)
	if !result.Available {
		t.Fatal("expected checkout available")
	}

	if _, err := os.Stat(result.RepoDir); err != nil {
		t.Fatalf("expected worktree dir to exist: %v", err)
	}

	result.Release()
	result.Release() // 幂等性

	if _, err := os.Stat(result.RepoDir); !os.IsNotExist(err) {
		t.Fatalf("expected worktree dir removed, stat err: %v", err)
	}
}

func TestCleanup_SkipsActiveCheckouts(t *testing.T) {
	fixture := setupRemoteFixture(t)
	m := NewManager(&config.CheckoutConfig{
		Enabled: true,
		BaseDir: t.TempDir(),
		TTL:     "1s",
	})
	params := Params{
		Platform: "github",
		Repo:     fixture.repo,
		MRNumber: "101",
		Host:     "file://" + fixture.hostDir,
	}

	result := m.Checkout(params)
	if !result.Available {
		t.Fatal("expected checkout available")
	}
	mirrorDir := m.mirrorDir(params)

	old := time.Now().Add(-2 * time.Hour)
	if err := os.Chtimes(mirrorDir, old, old); err != nil {
		t.Fatalf("failed to age mirror dir: %v", err)
	}

	m.cleanupExpiredMirrors()
	if _, err := os.Stat(mirrorDir); err != nil {
		t.Fatalf("expected active mirror kept, got: %v", err)
	}

	result.Release()
	if err := os.Chtimes(mirrorDir, old, old); err != nil {
		t.Fatalf("failed to re-age mirror dir: %v", err)
	}
	m.cleanupExpiredMirrors()
	if _, err := os.Stat(mirrorDir); !os.IsNotExist(err) {
		t.Fatalf("expected mirror removed after release, stat err: %v", err)
	}
}

func TestFetchFailure_FallbackToReclone(t *testing.T) {
	fixture := setupRemoteFixture(t)
	m := NewManager(&config.CheckoutConfig{
		Enabled: true,
		BaseDir: t.TempDir(),
		TTL:     "24h",
	})
	params := Params{
		Platform: "github",
		Repo:     fixture.repo,
		MRNumber: "101",
		Host:     "file://" + fixture.hostDir,
	}

	first := m.Checkout(params)
	if !first.Available {
		t.Fatal("expected first checkout available")
	}
	first.Release()

	mirrorDir := m.mirrorDir(params)
	mustGit(t, "-C", mirrorDir, "remote", "set-url", "origin", "file:///path/does/not/exist/repo.git")

	second := m.Checkout(params)
	if !second.Available {
		t.Fatal("expected second checkout available")
	}
	defer second.Release()
	if second.FromCache {
		t.Fatal("expected fallback re-clone to report FromCache=false")
	}

	gotOrigin := gitOutput(t, "-C", mirrorDir, "remote", "get-url", "origin")
	wantOrigin := buildCloneURL(params.Platform, params.Repo, params.Host)
	if gotOrigin != wantOrigin {
		t.Fatalf("origin url = %q, want %q", gotOrigin, wantOrigin)
	}
}

type remoteFixture struct {
	hostDir string
	repo    string
}

func setupRemoteFixture(t *testing.T) remoteFixture {
	t.Helper()

	root := t.TempDir()
	seed := filepath.Join(root, "seed")
	hostDir := filepath.Join(root, "host")
	repo := "owner/repo"
	remote := filepath.Join(hostDir, "owner", "repo.git")

	mustGit(t, "init", seed)
	mustGit(t, "-C", seed, "config", "user.email", "test@example.com")
	mustGit(t, "-C", seed, "config", "user.name", "CodeDebate Test")

	if err := os.WriteFile(filepath.Join(seed, "README.md"), []byte("hello\n"), 0o644); err != nil {
		t.Fatalf("failed writing README: %v", err)
	}
	mustGit(t, "-C", seed, "add", "README.md")
	mustGit(t, "-C", seed, "commit", "-m", "initial")
	mustGit(t, "-C", seed, "branch", "-M", "main")

	if err := os.MkdirAll(filepath.Dir(remote), 0o755); err != nil {
		t.Fatalf("failed creating remote parent: %v", err)
	}
	mustGit(t, "clone", "--bare", seed, remote)
	mustGit(t, "-C", seed, "remote", "add", "origin", remote)
	mustGit(t, "-C", seed, "push", "-u", "origin", "main")

	// feature-1 -> PR/MR 101
	mustGit(t, "-C", seed, "checkout", "-b", "feature-1", "main")
	if err := os.WriteFile(filepath.Join(seed, "feature1.txt"), []byte("feature1\n"), 0o644); err != nil {
		t.Fatalf("failed writing feature1: %v", err)
	}
	mustGit(t, "-C", seed, "add", "feature1.txt")
	mustGit(t, "-C", seed, "commit", "-m", "feature 1")
	mustGit(t, "-C", seed, "push", "origin", "feature-1")
	sha1 := gitOutput(t, "-C", seed, "rev-parse", "HEAD")

	// feature-2 -> PR/MR 102
	mustGit(t, "-C", seed, "checkout", "main")
	mustGit(t, "-C", seed, "checkout", "-b", "feature-2", "main")
	if err := os.WriteFile(filepath.Join(seed, "feature2.txt"), []byte("feature2\n"), 0o644); err != nil {
		t.Fatalf("failed writing feature2: %v", err)
	}
	mustGit(t, "-C", seed, "add", "feature2.txt")
	mustGit(t, "-C", seed, "commit", "-m", "feature 2")
	mustGit(t, "-C", seed, "push", "origin", "feature-2")
	sha2 := gitOutput(t, "-C", seed, "rev-parse", "HEAD")

	// 为 GitHub/GitLab 都创建测试用 MR/PR ref。
	mustGit(t, "--git-dir", remote, "update-ref", "refs/pull/101/head", sha1)
	mustGit(t, "--git-dir", remote, "update-ref", "refs/merge-requests/101/head", sha1)
	mustGit(t, "--git-dir", remote, "update-ref", "refs/pull/102/head", sha2)
	mustGit(t, "--git-dir", remote, "update-ref", "refs/merge-requests/102/head", sha2)

	return remoteFixture{
		hostDir: hostDir,
		repo:    repo,
	}
}

func mustGit(t *testing.T, args ...string) {
	t.Helper()
	if err := runGit(args...); err != nil {
		t.Fatalf("git command failed: %v", err)
	}
}

func gitOutput(t *testing.T, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %s failed: %v: %s", strings.Join(args, " "), err, strings.TrimSpace(string(out)))
	}
	return strings.TrimSpace(string(out))
}
