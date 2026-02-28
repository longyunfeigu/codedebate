// Package server 实现了 CodeDebate 的 Webhook Server 模式。
// 接收 GitLab MR 事件，自动触发代码审查并将结果推送回 GitLab。
package server

import (
	"crypto/subtle"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
)

// MergeRequestEvent 是 GitLab Merge Request Webhook 的事件结构体。
type MergeRequestEvent struct {
	ObjectKind       string       `json:"object_kind"`
	Project          ProjectInfo  `json:"project"`
	ObjectAttributes MRAttributes `json:"object_attributes"`
}

// ProjectInfo 包含 webhook 事件中的项目信息。
type ProjectInfo struct {
	ID                int    `json:"id"`
	PathWithNamespace string `json:"path_with_namespace"`
	WebURL            string `json:"web_url"`
	HTTPURLToRepo     string `json:"http_url_to_repo"`
}

// MRAttributes 包含 webhook 事件中的 MR 属性。
type MRAttributes struct {
	IID          int    `json:"iid"`
	Title        string `json:"title"`
	Description  string `json:"description"`
	State        string `json:"state"`
	Action       string `json:"action"`
	SourceBranch string `json:"source_branch"`
	TargetBranch string `json:"target_branch"`
	URL          string `json:"url"`
}

// ParseWebhookEvent 从 HTTP 请求中解析 GitLab webhook 事件。
func ParseWebhookEvent(r *http.Request) (*MergeRequestEvent, error) {
	body, err := io.ReadAll(r.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read request body: %w", err)
	}
	defer r.Body.Close()

	var event MergeRequestEvent
	if err := json.Unmarshal(body, &event); err != nil {
		return nil, fmt.Errorf("failed to parse webhook event: %w", err)
	}
	return &event, nil
}

// ValidateWebhookRequest 校验 GitLab webhook 请求的 X-Gitlab-Token header。
func ValidateWebhookRequest(r *http.Request, secret string) bool {
	token := r.Header.Get("X-Gitlab-Token")
	if token == "" || secret == "" {
		return false
	}
	return subtle.ConstantTimeCompare([]byte(token), []byte(secret)) == 1
}

// ShouldTriggerReview 判断一个 MR 事件是否应触发审查。
// 仅 action=open/reopen/update 且 state=opened 时触发；跳过 Draft/WIP MR。
func ShouldTriggerReview(event *MergeRequestEvent) bool {
	if event.ObjectKind != "merge_request" {
		return false
	}

	if event.ObjectAttributes.State != "opened" {
		return false
	}

	action := event.ObjectAttributes.Action
	if action != "open" && action != "reopen" && action != "update" {
		return false
	}

	title := event.ObjectAttributes.Title
	if strings.HasPrefix(title, "Draft:") || strings.HasPrefix(title, "WIP:") {
		return false
	}

	return true
}
