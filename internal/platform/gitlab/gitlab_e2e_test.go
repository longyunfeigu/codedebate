package gitlab

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/longyunfeigu/codedebate/internal/platform"
)

// TestE2E_PostInlineCommentVisibleInChanges posts one real inline comment to a target MR
// and verifies the created note is a diff note with non-nil position.
//
// Enable this test explicitly:
//
//	CODEDEBATE_E2E_GITLAB=1
//
// Required env:
//
//	CODEDEBATE_E2E_GITLAB_REPO   e.g. group/project
//	CODEDEBATE_E2E_GITLAB_MR_ID  e.g. 123
//
// Optional env:
//
//	CODEDEBATE_E2E_GITLAB_HOST   e.g. gitlab.company.com
func TestE2E_PostInlineCommentVisibleInChanges(t *testing.T) {
	if os.Getenv("CODEDEBATE_E2E_GITLAB") != "1" {
		t.Skip("set CODEDEBATE_E2E_GITLAB=1 to run e2e test")
	}

	repo := os.Getenv("CODEDEBATE_E2E_GITLAB_REPO")
	mrID := os.Getenv("CODEDEBATE_E2E_GITLAB_MR_ID")
	if repo == "" || mrID == "" {
		t.Fatal("CODEDEBATE_E2E_GITLAB_REPO and CODEDEBATE_E2E_GITLAB_MR_ID are required")
	}

	g := New(os.Getenv("CODEDEBATE_E2E_GITLAB_HOST"))

	commitInfo, err := g.GetHeadCommitInfo(mrID, repo)
	if err != nil {
		t.Fatalf("GetHeadCommitInfo failed: %v", err)
	}

	path, line, err := pickInlineTarget(g, mrID, repo)
	if err != nil {
		t.Fatalf("pickInlineTarget failed: %v", err)
	}

	marker := fmt.Sprintf("codedebate-inline-e2e-%d", time.Now().UnixNano())
	body := "E2E inline visibility check\n\nmarker: `" + marker + "`"

	cr := g.PostComment(mrID, platform.PostCommentOpts{
		Path:       path,
		Line:       &line,
		Body:       body,
		CommitInfo: *commitInfo,
		Repo:       repo,
	})
	if !cr.Success {
		t.Fatalf("PostComment failed: %s", cr.Error)
	}
	if cr.Mode != "inline" {
		t.Fatalf("expected inline mode, got %q", cr.Mode)
	}

	note, err := findDiscussionNoteByMarker(g, repo, mrID, marker)
	if err != nil {
		t.Fatalf("failed to query discussions: %v", err)
	}
	if note == nil {
		t.Fatalf("posted note not found by marker: %s", marker)
	}
	if note.Position == nil {
		t.Fatalf("note found but position is nil; this is not inline in Changes")
	}
	if note.Position.NewPath != path {
		t.Fatalf("position.new_path = %q, want %q", note.Position.NewPath, path)
	}
	if note.Position.NewLine == nil || *note.Position.NewLine != line {
		t.Fatalf("position.new_line = %v, want %d", note.Position.NewLine, line)
	}

	t.Logf("inline note created and positioned: %s/%s/-/merge_requests/%s/diffs", "https://"+g.getHost(), repo, mrID)
	t.Logf("marker: %s", marker)
}

// TestE2E_PostReviewInlineCommentVisibleInChanges exercises the batch posting path
// (PostReview -> draft_notes or Discussions fallback) and verifies the created note
// is still a diff note with non-nil position.
func TestE2E_PostReviewInlineCommentVisibleInChanges(t *testing.T) {
	if os.Getenv("CODEDEBATE_E2E_GITLAB") != "1" {
		t.Skip("set CODEDEBATE_E2E_GITLAB=1 to run e2e test")
	}

	repo := os.Getenv("CODEDEBATE_E2E_GITLAB_REPO")
	mrID := os.Getenv("CODEDEBATE_E2E_GITLAB_MR_ID")
	if repo == "" || mrID == "" {
		t.Fatal("CODEDEBATE_E2E_GITLAB_REPO and CODEDEBATE_E2E_GITLAB_MR_ID are required")
	}

	g := New(os.Getenv("CODEDEBATE_E2E_GITLAB_HOST"))

	commitInfo, err := g.GetHeadCommitInfo(mrID, repo)
	if err != nil {
		t.Fatalf("GetHeadCommitInfo failed: %v", err)
	}

	path, line, err := pickInlineTarget(g, mrID, repo)
	if err != nil {
		t.Fatalf("pickInlineTarget failed: %v", err)
	}

	marker := fmt.Sprintf("codedebate-postreview-inline-e2e-%d", time.Now().UnixNano())
	body := "E2E PostReview inline visibility check\n\nmarker: `" + marker + "`"

	classified := []platform.ClassifiedComment{
		{
			Input: platform.ReviewCommentInput{
				Path: path,
				Line: &line,
				Body: body,
			},
			Mode: "inline",
		},
	}

	rr := g.PostReview(mrID, classified, *commitInfo, repo)
	if rr.Posted < 1 {
		t.Fatalf("PostReview posted=%d, inline=%d, file=%d, global=%d, failed=%d, skipped=%d",
			rr.Posted, rr.Inline, rr.FileLevel, rr.Global, rr.Failed, rr.Skipped)
	}

	note, err := findDiscussionNoteByMarker(g, repo, mrID, marker)
	if err != nil {
		t.Fatalf("failed to query discussions: %v", err)
	}
	if note == nil {
		t.Fatalf("posted note not found by marker: %s", marker)
	}
	if note.Position == nil {
		t.Fatalf("note found but position is nil; this is not inline in Changes")
	}
	if note.Position.NewPath != path {
		t.Fatalf("position.new_path = %q, want %q", note.Position.NewPath, path)
	}
	if note.Position.NewLine == nil || *note.Position.NewLine != line {
		t.Fatalf("position.new_line = %v, want %d", note.Position.NewLine, line)
	}

	t.Logf("PostReview note created and positioned: %s/%s/-/merge_requests/%s/diffs", "https://"+g.getHost(), repo, mrID)
	t.Logf("marker: %s", marker)
}

func pickInlineTarget(g *GitLabPlatform, mrID, repo string) (string, int, error) {
	changedFiles, err := g.GetChangedFiles(mrID, repo)
	if err != nil {
		return "", 0, err
	}
	for _, f := range changedFiles {
		if f.Patch == "" {
			continue
		}
		lines := platform.ParseDiffLines(f.Patch)
		if len(lines) == 0 {
			continue
		}
		var nums []int
		for n := range lines {
			nums = append(nums, n)
		}
		sort.Ints(nums)
		return f.Filename, nums[0], nil
	}
	return "", 0, fmt.Errorf("no diff file with available line numbers")
}

type e2eDiscussionNote struct {
	Body     string `json:"body"`
	Position *struct {
		NewPath string `json:"new_path"`
		NewLine *int   `json:"new_line"`
	} `json:"position"`
}

func findDiscussionNoteByMarker(g *GitLabPlatform, repo, mrID, marker string) (*e2eDiscussionNote, error) {
	encodedProject := encodeProject(repo)
	out, err := exec.Command("glab", "api",
		fmt.Sprintf("projects/%s/merge_requests/%s/discussions", encodedProject, mrID),
	).Output()
	if err != nil {
		return nil, fmt.Errorf("glab api discussions failed: %w", err)
	}

	var discussions []struct {
		Notes []e2eDiscussionNote `json:"notes"`
	}
	if err := json.Unmarshal(out, &discussions); err != nil {
		return nil, fmt.Errorf("failed to parse discussions: %w", err)
	}

	for i := range discussions {
		for j := range discussions[i].Notes {
			n := &discussions[i].Notes[j]
			if n.Body != "" && strings.Contains(n.Body, marker) {
				return n, nil
			}
		}
	}
	return nil, nil
}
