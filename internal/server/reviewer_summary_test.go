package server

import (
	"testing"

	"github.com/longyunfeigu/codedebate/internal/platform"
	gh "github.com/longyunfeigu/codedebate/internal/platform/github"
	gl "github.com/longyunfeigu/codedebate/internal/platform/gitlab"
)

type fakeServerUpsertPlatform struct {
	platform.Platform
	called bool
	marker string
}

func (f *fakeServerUpsertPlatform) UpsertSummaryNote(mrID, repo, marker, body string) error {
	f.called = true
	f.marker = marker
	return nil
}

type fakeServerPostNotePlatform struct {
	platform.Platform
	called bool
}

func (f *fakeServerPostNotePlatform) PostNote(mrID, repo, body string) error {
	f.called = true
	return nil
}

type fakeUnsupportedServerSummaryPlatform struct {
	platform.Platform
}

func TestSupportsServerSummaryPosting(t *testing.T) {
	if supportsServerSummaryPosting(nil) {
		t.Fatal("nil platform should not support summary posting")
	}
	if !supportsServerSummaryPosting(gl.New("39.99.155.169")) {
		t.Fatal("gitlab platform should support summary posting")
	}
	if !supportsServerSummaryPosting(gh.New()) {
		t.Fatal("github platform should support summary posting")
	}
	if supportsServerSummaryPosting(&fakeUnsupportedServerSummaryPlatform{Platform: gl.New("39.99.155.169")}) {
		t.Fatal("fake unsupported platform should not support summary posting")
	}
}

func TestShouldPostServerSummary(t *testing.T) {
	if !shouldPostServerSummary("final", gl.New("39.99.155.169")) {
		t.Fatal("expected summary to be posted for gitlab + non-empty conclusion")
	}
	if shouldPostServerSummary("   ", gl.New("39.99.155.169")) {
		t.Fatal("empty conclusion should not be posted")
	}
	if !shouldPostServerSummary("final", gh.New()) {
		t.Fatal("github platform should post summary")
	}
}

func TestBuildServerSummaryNoteBody(t *testing.T) {
	body := buildServerSummaryNoteBody("  final summary  ")
	if !contains(body, codedebateSummaryMarker) {
		t.Fatalf("expected marker in body, got %q", body)
	}
	if !contains(body, "## CodeDebate Code Review Summary") {
		t.Fatalf("expected heading in body, got %q", body)
	}
	if !contains(body, "final summary") {
		t.Fatalf("expected trimmed summary in body, got %q", body)
	}
}

func TestUpsertServerSummaryNote_UsesUpserter(t *testing.T) {
	fp := &fakeServerUpsertPlatform{Platform: gl.New("39.99.155.169")}
	if err := upsertServerSummaryNote(fp, "1", "group/repo", "body"); err != nil {
		t.Fatalf("upsertServerSummaryNote returned error: %v", err)
	}
	if !fp.called {
		t.Fatal("expected upserter to be called")
	}
	if fp.marker != codedebateSummaryMarker {
		t.Fatalf("marker = %q, want %q", fp.marker, codedebateSummaryMarker)
	}
}

func TestUpsertServerSummaryNote_FallbackPostNote(t *testing.T) {
	fp := &fakeServerPostNotePlatform{Platform: gh.New()}
	if err := upsertServerSummaryNote(fp, "1", "group/repo", "body"); err != nil {
		t.Fatalf("upsertServerSummaryNote returned error: %v", err)
	}
	if !fp.called {
		t.Fatal("expected PostNote fallback to be called")
	}
}

func TestUpsertServerSummaryNote_Unsupported(t *testing.T) {
	unsupported := &fakeUnsupportedServerSummaryPlatform{Platform: gl.New("39.99.155.169")}
	err := upsertServerSummaryNote(unsupported, "1", "owner/repo", "body")
	if err == nil {
		t.Fatal("expected unsupported platform error")
	}
}

func contains(s, substr string) bool {
	return len(s) >= len(substr) && searchString(s, substr)
}

func searchString(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}
