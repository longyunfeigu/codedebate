package checkout

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

func (m *Manager) ensureMirror(cloneURL, mirrorDir string) (fromCache bool, err error) {
	if _, statErr := os.Stat(mirrorDir); statErr == nil {
		expired := m.isMirrorExpired(mirrorDir)
		active := m.isMirrorActive(mirrorDir)

		if expired && !active {
			_ = os.RemoveAll(mirrorDir)
		} else {
			if fetchErr := runGit("-C", mirrorDir, "fetch", "--all", "--prune"); fetchErr == nil {
				_ = touchPath(mirrorDir)
				return true, nil
			} else if active {
				return false, fetchErr
			}
			_ = os.RemoveAll(mirrorDir)
		}
	}

	if err := os.MkdirAll(filepath.Dir(mirrorDir), 0o755); err != nil {
		return false, err
	}
	if err := runGit("clone", "--mirror", cloneURL, mirrorDir); err != nil {
		return false, err
	}
	_ = touchPath(mirrorDir)
	return false, nil
}

func (m *Manager) createWorktree(mirrorDir string, params Params) (string, error) {
	worktreeRoot := filepath.Join(m.baseDir, "worktrees")
	if err := os.MkdirAll(worktreeRoot, 0o755); err != nil {
		return "", err
	}
	worktreeDir, err := os.MkdirTemp(worktreeRoot, "wt-*")
	if err != nil {
		return "", err
	}

	ref := "HEAD"
	if strings.TrimSpace(params.MRNumber) != "" {
		refSpec := mrRefSpec(params.Platform, params.MRNumber)
		if refSpec == "" {
			_ = os.RemoveAll(worktreeDir)
			return "", fmt.Errorf("unsupported platform for MR ref: %s", params.Platform)
		}
		if err := runGit("-C", mirrorDir, "fetch", "origin", refSpec); err != nil {
			_ = os.RemoveAll(worktreeDir)
			return "", err
		}
		ref = "FETCH_HEAD"
	}

	if err := runGit("-C", mirrorDir, "worktree", "add", "--detach", worktreeDir, ref); err != nil {
		_ = os.RemoveAll(worktreeDir)
		return "", err
	}
	_ = touchPath(mirrorDir)
	return worktreeDir, nil
}

func (m *Manager) removeWorktree(mirrorDir, worktreeDir string) {
	_ = runGit("-C", mirrorDir, "worktree", "remove", "--force", worktreeDir)
	_ = os.RemoveAll(worktreeDir)
}

func buildCloneURL(platform, repo, host string) string {
	repo = strings.Trim(strings.TrimSpace(repo), "/")
	repo = strings.TrimSuffix(repo, ".git")
	if repo == "" {
		return ""
	}

	host = strings.TrimSpace(host)
	if host == "" {
		switch strings.ToLower(strings.TrimSpace(platform)) {
		case "gitlab":
			host = "gitlab.com"
		default:
			host = "github.com"
		}
	}

	host = strings.TrimSuffix(host, "/")
	if strings.Contains(host, "://") {
		return host + "/" + repo + ".git"
	}
	if strings.HasPrefix(host, "/") || strings.HasPrefix(host, ".") {
		return filepath.ToSlash(filepath.Join(host, filepath.FromSlash(repo)+".git"))
	}
	return "https://" + host + "/" + repo + ".git"
}

func mrRefSpec(platform, mrNumber string) string {
	switch strings.ToLower(strings.TrimSpace(platform)) {
	case "gitlab":
		return fmt.Sprintf("merge-requests/%s/head", mrNumber)
	default:
		return fmt.Sprintf("pull/%s/head", mrNumber)
	}
}

func runGit(args ...string) error {
	cmd := exec.Command("git", args...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("git %s failed: %w: %s", strings.Join(args, " "), err, strings.TrimSpace(string(out)))
	}
	return nil
}

func touchPath(path string) error {
	now := time.Now()
	return os.Chtimes(path, now, now)
}
