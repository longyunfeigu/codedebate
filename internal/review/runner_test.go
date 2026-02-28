package review

import (
	"reflect"
	"strings"
	"testing"

	"github.com/longyunfeigu/codedebate/internal/config"
	"github.com/longyunfeigu/codedebate/internal/platform"
)

type stubMRCommenter struct {
	comments []platform.ExistingComment
	calls    int
	mrID     string
	repo     string
}

func (s *stubMRCommenter) PostComment(string, platform.PostCommentOpts) platform.CommentResult {
	return platform.CommentResult{}
}

func (s *stubMRCommenter) PostReview(string, []platform.ClassifiedComment, platform.CommitInfo, string) platform.ReviewResult {
	return platform.ReviewResult{}
}

func (s *stubMRCommenter) GetExistingComments(mrID, repo string) []platform.ExistingComment {
	s.calls++
	s.mrID = mrID
	s.repo = repo
	return append([]platform.ExistingComment(nil), s.comments...)
}

func TestFormatPreviousComments_IncludesOnlyActiveCodeDebateFindings(t *testing.T) {
	line := 45
	comments := []platform.ExistingComment{
		{
			Path:    "src/auth.go",
			Line:    &line,
			Body:    "<!-- codedebate:issue {\"key\":\"issue-1\",\"status\":\"active\",\"run\":\"run-1\",\"head\":\"abc\",\"body\":\"hash\",\"anchor\":\"anchor\"} -->\n🟠 **Missing token validation**\n\nUser-controlled token is not validated.",
			IsCodeDebate: true,
			Meta: &platform.CodeDebateCommentMeta{
				IssueKey: "issue-1",
				Status:   "active",
			},
		},
		{
			Path:    "src/auth.go",
			Line:    &line,
			Body:    "<!-- codedebate:issue {\"key\":\"issue-2\",\"status\":\"resolved\",\"run\":\"run-1\",\"head\":\"abc\",\"body\":\"hash\",\"anchor\":\"anchor\"} -->\n🟡 **Resolved issue**",
			IsCodeDebate: true,
			Meta: &platform.CodeDebateCommentMeta{
				IssueKey: "issue-2",
				Status:   "resolved",
			},
		},
		{
			Path:    "src/handler.go",
			Line:    &line,
			Body:    "human comment",
			IsCodeDebate: false,
		},
	}

	got := formatPreviousComments(comments)
	if !strings.Contains(got, "1. [high] `src/auth.go:45` - Missing token validation") {
		t.Fatalf("formatted comments missing active CodeDebate issue: %q", got)
	}
	if strings.Contains(got, "Resolved issue") {
		t.Fatalf("formatted comments should skip resolved CodeDebate issue: %q", got)
	}
	if strings.Contains(got, "human comment") {
		t.Fatalf("formatted comments should skip human comments: %q", got)
	}
}

func TestRunnerPrepare_LoadsPreviousCommentsIntoOrchestratorOptions(t *testing.T) {
	line := 42
	commenter := &stubMRCommenter{comments: []platform.ExistingComment{{
		Path:    "pkg/auth.go",
		Line:    &line,
		Body:    "<!-- codedebate:issue {\"key\":\"issue-1\",\"status\":\"active\",\"run\":\"run-1\",\"head\":\"abc\",\"body\":\"hash\",\"anchor\":\"anchor\"} -->\n🟡 **Auth check missing**\n\nMissing auth check before update.",
		IsCodeDebate: true,
		Meta: &platform.CodeDebateCommentMeta{
			IssueKey: "issue-1",
			Status:   "active",
		},
	}}}

	runner := NewRunner(&config.CodeDebateConfig{
		Mock: true,
		Defaults: config.DefaultsConfig{
			MaxRounds:        5,
			CheckConvergence: true,
			StructurizeMode:  "ledger",
		},
		Reviewers: map[string]config.ReviewerConfig{
			"reviewer-a": {Model: "mock", Prompt: "reviewer prompt"},
		},
		Analyzer:   config.ReviewerConfig{Model: "mock", Prompt: "analyzer prompt"},
		Summarizer: config.ReviewerConfig{Model: "mock", Prompt: "summarizer prompt"},
	}, nil)

	prepared, err := runner.Prepare(Job{Type: "pr", Repo: "owner/repo", MRNumber: "123", Prompt: "Review this PR"}, RunOptions{Commenter: commenter})
	if err != nil {
		t.Fatalf("Prepare returned error: %v", err)
	}

	if commenter.calls != 1 || commenter.mrID != "123" || commenter.repo != "owner/repo" {
		t.Fatalf("commenter calls = %d, mrID = %q, repo = %q", commenter.calls, commenter.mrID, commenter.repo)
	}

	options := reflect.ValueOf(prepared.orchestrator).Elem().FieldByName("options")
	previousComments := options.FieldByName("PreviousComments").String()
	if !strings.Contains(previousComments, "[medium] `pkg/auth.go:42` - Auth check missing") {
		t.Fatalf("PreviousComments = %q", previousComments)
	}
}
