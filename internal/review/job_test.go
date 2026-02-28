package review

import (
	"errors"
	"strings"
	"testing"

	"github.com/longyunfeigu/codedebate/internal/platform"
)

type stubMetadataProvider struct {
	diff    string
	diffErr error
	info    *platform.MRInfo
	infoErr error
}

func (s stubMetadataProvider) GetDiff(_, _ string) (string, error) {
	if s.diffErr != nil {
		return "", s.diffErr
	}
	return s.diff, nil
}

func (s stubMetadataProvider) GetInfo(_, _ string) (*platform.MRInfo, error) {
	if s.infoErr != nil {
		return nil, s.infoErr
	}
	if s.info != nil {
		return s.info, nil
	}
	return &platform.MRInfo{}, nil
}

func TestFilterReviewDiff_NoReviewableChangesAfterExclude(t *testing.T) {
	diff := "" +
		"diff --git a/go.sum b/go.sum\n" +
		"--- a/go.sum\n" +
		"+++ b/go.sum\n" +
		"@@ -1 +1 @@\n" +
		"-old\n" +
		"+new\n"

	_, err := filterReviewDiff(diff, nil, "all changes excluded")
	if err == nil {
		t.Fatal("expected no-reviewable-changes error")
	}
	if !IsNoReviewableChanges(err) {
		t.Fatalf("expected ErrNoReviewableChanges, got %v", err)
	}
}

func TestBuildMRJobFromRef_DiffFetchErrorDoesNotFallbackToSelfFetch(t *testing.T) {
	ref := MRRef{ID: "123", Repo: "group/project", URL: "https://gitlab.example.com/group/project/-/merge_requests/123"}

	_, err := BuildMRJobFromRef(ref, "gitlab", stubMetadataProvider{
		diffErr: errors.New("boom"),
	}, nil)
	if err == nil {
		t.Fatal("expected diff fetch error")
	}
	if !strings.Contains(err.Error(), "failed to get PR/MR diff") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestBuildMRJobFromRef_NoReviewableChangesAfterExclude(t *testing.T) {
	ref := MRRef{ID: "123", Repo: "group/project", URL: "https://gitlab.example.com/group/project/-/merge_requests/123"}

	_, err := BuildMRJobFromRef(ref, "gitlab", stubMetadataProvider{
		diff: "" +
			"diff --git a/go.sum b/go.sum\n" +
			"--- a/go.sum\n" +
			"+++ b/go.sum\n" +
			"@@ -1 +1 @@\n" +
			"-old\n" +
			"+new\n",
	}, nil)
	if err == nil {
		t.Fatal("expected no-reviewable-changes error")
	}
	if !IsNoReviewableChanges(err) {
		t.Fatalf("expected ErrNoReviewableChanges, got %v", err)
	}
}

func TestBuildMRJobFromRef_UsesProvidedDiffPrompt(t *testing.T) {
	ref := MRRef{ID: "123", Repo: "group/project", URL: "https://gitlab.example.com/group/project/-/merge_requests/123"}

	job, err := BuildMRJobFromRef(ref, "gitlab", stubMetadataProvider{
		diff: "" +
			"diff --git a/main.go b/main.go\n" +
			"--- a/main.go\n" +
			"+++ b/main.go\n" +
			"@@ -1 +1 @@\n" +
			"-old\n" +
			"+new\n",
		info: &platform.MRInfo{Title: "Test MR", Description: "Desc"},
	}, nil)
	if err != nil {
		t.Fatalf("BuildMRJobFromRef returned error: %v", err)
	}
	if strings.Contains(job.Prompt, "Get the details and diff using any method available") {
		t.Fatalf("prompt should not instruct model to fetch diff again: %s", job.Prompt)
	}
	if !strings.Contains(job.Prompt, "Here is the full diff") {
		t.Fatalf("prompt should embed diff directly: %s", job.Prompt)
	}
}

func TestBuildServerMRJob_NoReviewableChangesAfterExclude(t *testing.T) {
	ref := MRRef{ID: "123", Repo: "group/project", URL: "https://gitlab.example.com/group/project/-/merge_requests/123"}

	_, err := BuildServerMRJob(ref, stubMetadataProvider{
		diff: "" +
			"diff --git a/go.sum b/go.sum\n" +
			"--- a/go.sum\n" +
			"+++ b/go.sum\n" +
			"@@ -1 +1 @@\n" +
			"-old\n" +
			"+new\n",
	}, nil)
	if err == nil {
		t.Fatal("expected no-reviewable-changes error")
	}
	if !IsNoReviewableChanges(err) {
		t.Fatalf("expected ErrNoReviewableChanges, got %v", err)
	}
}
