package server

import (
	"net/http"
	"strings"
	"testing"
)

func TestValidateWebhookRequest(t *testing.T) {
	tests := []struct {
		name   string
		token  string
		secret string
		want   bool
	}{
		{"valid secret", "mysecret", "mysecret", true},
		{"invalid secret", "wrong", "mysecret", false},
		{"empty token", "", "mysecret", false},
		{"empty secret", "mysecret", "", false},
		{"both empty", "", "", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r, _ := http.NewRequest("POST", "/webhook/gitlab", nil)
			if tt.token != "" {
				r.Header.Set("X-Gitlab-Token", tt.token)
			}
			got := ValidateWebhookRequest(r, tt.secret)
			if got != tt.want {
				t.Errorf("ValidateWebhookRequest() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestShouldTriggerReview(t *testing.T) {
	tests := []struct {
		name  string
		event MergeRequestEvent
		want  bool
	}{
		{
			name: "open MR triggers review",
			event: MergeRequestEvent{
				ObjectKind:       "merge_request",
				ObjectAttributes: MRAttributes{Action: "open", State: "opened", Title: "Add feature"},
			},
			want: true,
		},
		{
			name: "reopen MR triggers review",
			event: MergeRequestEvent{
				ObjectKind:       "merge_request",
				ObjectAttributes: MRAttributes{Action: "reopen", State: "opened", Title: "Add feature"},
			},
			want: true,
		},
		{
			name: "update MR triggers review",
			event: MergeRequestEvent{
				ObjectKind:       "merge_request",
				ObjectAttributes: MRAttributes{Action: "update", State: "opened", Title: "Add feature"},
			},
			want: true,
		},
		{
			name: "close MR does not trigger",
			event: MergeRequestEvent{
				ObjectKind:       "merge_request",
				ObjectAttributes: MRAttributes{Action: "close", State: "closed", Title: "Add feature"},
			},
			want: false,
		},
		{
			name: "merge MR does not trigger",
			event: MergeRequestEvent{
				ObjectKind:       "merge_request",
				ObjectAttributes: MRAttributes{Action: "merge", State: "merged", Title: "Add feature"},
			},
			want: false,
		},
		{
			name: "Draft MR skipped",
			event: MergeRequestEvent{
				ObjectKind:       "merge_request",
				ObjectAttributes: MRAttributes{Action: "open", State: "opened", Title: "Draft: WIP feature"},
			},
			want: false,
		},
		{
			name: "WIP MR skipped",
			event: MergeRequestEvent{
				ObjectKind:       "merge_request",
				ObjectAttributes: MRAttributes{Action: "open", State: "opened", Title: "WIP: unfinished work"},
			},
			want: false,
		},
		{
			name: "non-MR event skipped",
			event: MergeRequestEvent{
				ObjectKind:       "push",
				ObjectAttributes: MRAttributes{Action: "open", State: "opened", Title: "Test"},
			},
			want: false,
		},
		{
			name: "approve action does not trigger",
			event: MergeRequestEvent{
				ObjectKind:       "merge_request",
				ObjectAttributes: MRAttributes{Action: "approved", State: "opened", Title: "Add feature"},
			},
			want: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := ShouldTriggerReview(&tt.event)
			if got != tt.want {
				t.Errorf("ShouldTriggerReview() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestParseWebhookEvent(t *testing.T) {
	body := `{
		"object_kind": "merge_request",
		"project": {
			"id": 123,
			"path_with_namespace": "group/project",
			"web_url": "https://gitlab.com/group/project"
		},
		"object_attributes": {
			"iid": 42,
			"title": "Test MR",
			"description": "A test merge request",
			"state": "opened",
			"action": "open",
			"source_branch": "feature",
			"target_branch": "main",
			"url": "https://gitlab.com/group/project/-/merge_requests/42"
		}
	}`

	r, _ := http.NewRequest("POST", "/webhook/gitlab", strings.NewReader(body))
	event, err := ParseWebhookEvent(r)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if event.ObjectKind != "merge_request" {
		t.Errorf("ObjectKind = %q, want %q", event.ObjectKind, "merge_request")
	}
	if event.Project.PathWithNamespace != "group/project" {
		t.Errorf("PathWithNamespace = %q, want %q", event.Project.PathWithNamespace, "group/project")
	}
	if event.Project.ID != 123 {
		t.Errorf("Project.ID = %d, want %d", event.Project.ID, 123)
	}
	if event.ObjectAttributes.IID != 42 {
		t.Errorf("IID = %d, want %d", event.ObjectAttributes.IID, 42)
	}
	if event.ObjectAttributes.Title != "Test MR" {
		t.Errorf("Title = %q, want %q", event.ObjectAttributes.Title, "Test MR")
	}
	if event.ObjectAttributes.Action != "open" {
		t.Errorf("Action = %q, want %q", event.ObjectAttributes.Action, "open")
	}
	if event.ObjectAttributes.SourceBranch != "feature" {
		t.Errorf("SourceBranch = %q, want %q", event.ObjectAttributes.SourceBranch, "feature")
	}
}

func TestParseWebhookEventInvalidJSON(t *testing.T) {
	r, _ := http.NewRequest("POST", "/webhook/gitlab", strings.NewReader("not json"))
	_, err := ParseWebhookEvent(r)
	if err == nil {
		t.Fatal("expected error for invalid JSON")
	}
}
