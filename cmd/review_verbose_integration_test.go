//go:build integration

package cmd

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestReviewVerbose_Integration_RealClaude(t *testing.T) {
	requireRealClaudeIntegration(t)

	projectRoot := findProjectRoot(t)
	bin := buildCodeDebateBinary(t, projectRoot)
	workDir := setupTempGitRepo(t)
	cfgPath := writeIntegrationConfig(t, workDir)

	t.Run("default mode hides trace", func(t *testing.T) {
		out, err := runReviewCommand(t, bin, workDir, cfgPath, "mocker", false)
		if err != nil {
			t.Fatalf("default mode run should succeed, got error: %v\n%s", err, out)
		}
		if !strings.Contains(out, "Detailed trace is hidden") {
			t.Fatalf("expected hidden trace hint in default mode, output:\n%s", out)
		}
	})

	t.Run("verbose mode shows real claude output", func(t *testing.T) {
		out, err := runReviewCommand(t, bin, workDir, cfgPath, "claude", true)
		// 真实 Claude 在后续 summary 阶段可能失败，但这不影响 -v 行为验证：
		// 只要 reviewer 输出已展示，就能证明 verbose 开关生效。
		if err != nil {
			t.Logf("review command returned error (acceptable for this integration check): %v", err)
		}
		if strings.Contains(out, "Detailed trace is hidden") {
			t.Fatalf("did not expect hidden trace hint with -v, output:\n%s", out)
		}
		if !strings.Contains(out, "Trace:       enabled") {
			t.Fatalf("expected verbose trace mode to be enabled, output:\n%s", out)
		}
		hasReviewerStream := strings.Contains(out, "┌─ claude") || strings.Contains(out, "claude [Round")
		hasClaudeError := strings.Contains(out, "claude stream returned error result") ||
			strings.Contains(out, "reviewer claude failed") ||
			strings.Contains(out, "No available Claude accounts support")
		if !hasReviewerStream && !hasClaudeError {
			t.Fatalf("expected real claude execution evidence in output, got:\n%s", out)
		}
		t.Logf("codedebate review -v output (first 1200 chars):\n%s", limitText(out, 1200))
	})
}

func requireRealClaudeIntegration(t *testing.T) {
	t.Helper()
	if os.Getenv("CODEDEBATE_RUN_REAL_CLAUDE") != "1" {
		t.Skip("set CODEDEBATE_RUN_REAL_CLAUDE=1 to run real Claude integration test")
	}
	if _, err := exec.LookPath("claude"); err != nil {
		t.Skip("claude CLI not found in PATH, skipping integration test")
	}
}

func findProjectRoot(t *testing.T) string {
	t.Helper()
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

func buildCodeDebateBinary(t *testing.T, projectRoot string) string {
	t.Helper()
	bin := filepath.Join(t.TempDir(), "codedebate-test-bin")
	goBin := os.Getenv("GO_BIN")
	if goBin == "" {
		if p, err := exec.LookPath("go"); err == nil {
			goBin = p
		} else if _, err := os.Stat("/usr/local/go/bin/go"); err == nil {
			goBin = "/usr/local/go/bin/go"
		} else {
			t.Skip("go binary not found in PATH and /usr/local/go/bin/go missing")
		}
	}
	cmd := exec.Command(goBin, "build", "-o", bin, ".")
	cmd.Dir = projectRoot
	cmd.Env = os.Environ()
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("failed to build codedebate binary: %v\n%s", err, string(out))
	}
	return bin
}

func setupTempGitRepo(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	runGit(t, dir, "init")
	runGit(t, dir, "config", "user.email", "codedebate-test@example.com")
	runGit(t, dir, "config", "user.name", "CodeDebate Test")

	mainFile := filepath.Join(dir, "main.go")
	initial := "package main\n\nfunc add(a, b int) int { return a + b }\n"
	if err := os.WriteFile(mainFile, []byte(initial), 0o644); err != nil {
		t.Fatalf("write initial file: %v", err)
	}
	runGit(t, dir, "add", ".")
	runGit(t, dir, "commit", "-m", "init")

	updated := "package main\n\nfunc add(a, b int) int {\n\tif a == 0 {\n\t\treturn b\n\t}\n\treturn a + b\n}\n"
	if err := os.WriteFile(mainFile, []byte(updated), 0o644); err != nil {
		t.Fatalf("write updated file: %v", err)
	}

	return dir
}

func runGit(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	cmd.Env = os.Environ()
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %s failed: %v\n%s", strings.Join(args, " "), err, string(out))
	}
}

func writeIntegrationConfig(t *testing.T, dir string) string {
	t.Helper()
	cfg := `providers:
  claude-code:
    enabled: true
defaults:
  max_rounds: 1
  output_format: markdown
  check_convergence: false
  skip_permissions: true
reviewers:
  claude:
    model: claude-code
    prompt: |
      You are a concise code reviewer.
      Keep the review brief and concrete.
  mocker:
    model: mock
    prompt: |
      You are a mock reviewer.
analyzer:
  model: mock
  prompt: |
    You are a mock analyzer.
summarizer:
  model: mock
  prompt: |
    You are a mock summarizer.
`
	path := filepath.Join(dir, "codedebate.integration.yaml")
	if err := os.WriteFile(path, []byte(cfg), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}
	return path
}

func runReviewCommand(t *testing.T, bin, workDir, cfgPath, reviewer string, verbose bool) (string, error) {
	t.Helper()
	args := []string{
		"review",
		"--local",
		"--skip-context",
		"--reviewers", reviewer,
		"--config", cfgPath,
	}
	if verbose {
		args = append(args, "-v")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Minute)
	defer cancel()

	cmd := exec.CommandContext(ctx, bin, args...)
	cmd.Dir = workDir
	cmd.Env = os.Environ()

	out, err := cmd.CombinedOutput()
	if ctx.Err() == context.DeadlineExceeded {
		t.Fatalf("codedebate review timed out (%s):\n%s", strings.Join(args, " "), string(out))
	}
	return string(out), err
}

func limitText(s string, max int) string {
	if len(s) <= max {
		return s
	}
	return s[:max] + "...(truncated)"
}
