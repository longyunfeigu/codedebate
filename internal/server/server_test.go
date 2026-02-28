package server

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/longyunfeigu/codedebate/internal/config"
)

func newTestServer() *Server {
	cfg := &config.CodeDebateConfig{
		Defaults: config.DefaultsConfig{MaxRounds: 3},
		Reviewers: map[string]config.ReviewerConfig{
			"test": {Model: "mock", Prompt: "Test reviewer"},
		},
		Analyzer:   config.ReviewerConfig{Model: "mock", Prompt: "Test analyzer"},
		Summarizer: config.ReviewerConfig{Model: "mock", Prompt: "Test summarizer"},
	}

	return New(ServerConfig{
		CodeDebateConfig:   cfg,
		Addr:          ":0",
		WebhookSecret: "test-secret",
		MaxConcurrent: 2,
		GitLabHost:    "gitlab.com",
	})
}

func TestHealthEndpoint(t *testing.T) {
	srv := newTestServer()
	req := httptest.NewRequest("GET", "/health", nil)
	w := httptest.NewRecorder()

	srv.handleHealth(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("health check status = %d, want %d", w.Code, http.StatusOK)
	}

	var resp map[string]any
	json.NewDecoder(w.Body).Decode(&resp)
	if resp["status"] != "ok" {
		t.Errorf("health status = %q, want %q", resp["status"], "ok")
	}
	if resp["active_reviews"] != float64(0) {
		t.Errorf("active_reviews = %v, want 0", resp["active_reviews"])
	}
	if resp["max_concurrent"] != float64(2) {
		t.Errorf("max_concurrent = %v, want 2", resp["max_concurrent"])
	}
}

func TestWebhookUnauthorized(t *testing.T) {
	t.Skip("webhook secret validation is currently disabled in handler")
}

func TestWebhookBadRequest(t *testing.T) {
	srv := newTestServer()

	req := httptest.NewRequest("POST", "/webhook/gitlab", strings.NewReader("not json"))
	req.Header.Set("X-Gitlab-Token", "test-secret")
	w := httptest.NewRecorder()

	srv.handleWebhook(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want %d", w.Code, http.StatusBadRequest)
	}
}

func TestWebhookMethodNotAllowed(t *testing.T) {
	srv := newTestServer()

	req := httptest.NewRequest("GET", "/webhook/gitlab", nil)
	w := httptest.NewRecorder()

	srv.handleWebhook(w, req)

	if w.Code != http.StatusMethodNotAllowed {
		t.Errorf("status = %d, want %d", w.Code, http.StatusMethodNotAllowed)
	}
}

func TestWebhookAccepted(t *testing.T) {
	srv := newTestServer()

	body := `{
		"object_kind": "merge_request",
		"project": {"id": 1, "path_with_namespace": "group/project", "web_url": "https://gitlab.com/group/project"},
		"object_attributes": {"iid": 1, "action": "open", "state": "opened", "title": "Test MR", "url": "https://gitlab.com/group/project/-/merge_requests/1"}
	}`
	req := httptest.NewRequest("POST", "/webhook/gitlab", strings.NewReader(body))
	req.Header.Set("X-Gitlab-Token", "test-secret")
	w := httptest.NewRecorder()

	srv.handleWebhook(w, req)

	if w.Code != http.StatusAccepted {
		t.Errorf("status = %d, want %d", w.Code, http.StatusAccepted)
	}

	var resp map[string]string
	json.NewDecoder(w.Body).Decode(&resp)
	if resp["status"] != "accepted" {
		t.Errorf("response status = %q, want %q", resp["status"], "accepted")
	}
}

func TestWebhookSkippedEvent(t *testing.T) {
	srv := newTestServer()

	body := `{
		"object_kind": "merge_request",
		"project": {"id": 1, "path_with_namespace": "group/project"},
		"object_attributes": {"iid": 1, "action": "close", "state": "closed", "title": "Test MR"}
	}`
	req := httptest.NewRequest("POST", "/webhook/gitlab", strings.NewReader(body))
	req.Header.Set("X-Gitlab-Token", "test-secret")
	w := httptest.NewRecorder()

	srv.handleWebhook(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("status = %d, want %d", w.Code, http.StatusOK)
	}

	var resp map[string]string
	json.NewDecoder(w.Body).Decode(&resp)
	if resp["status"] != "skipped" {
		t.Errorf("response status = %q, want %q", resp["status"], "skipped")
	}
}

func TestPanicRecovery(t *testing.T) {
	// 关闭 sem channel，使 triggerReview 在 select send 时触发真实 panic（send on closed channel）。
	// 验证 panic 被 recover 捕获，goroutine 正常退出且 inFlight 被清理。
	srv := newTestServer()
	close(srv.sem) // send on closed channel → panic

	event := &MergeRequestEvent{
		Project:          ProjectInfo{PathWithNamespace: "test/panic"},
		ObjectAttributes: MRAttributes{IID: 99},
	}

	done := make(chan struct{})
	go func() {
		srv.triggerReview(event)
		close(done)
	}()

	select {
	case <-done:
		// goroutine 正常退出，panic 被捕获
	case <-time.After(5 * time.Second):
		t.Fatal("triggerReview did not return after panic — recovery failed")
	}

	// 验证 inFlight 已被清理
	srv.mu.Lock()
	_, exists := srv.inFlight["test/panic/99"]
	srv.mu.Unlock()
	if exists {
		t.Error("expected inFlight entry to be cleaned up after panic")
	}
}

func TestDeduplication(t *testing.T) {
	srv := newTestServer()

	// 模拟一个正在进行的 review entry
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	existing := &inFlightEntry{cancel: cancel, done: done}

	srv.mu.Lock()
	srv.inFlight["group/project/1"] = existing
	srv.mu.Unlock()

	// 填满信号量，让 triggerReview 拿不到 sem 直接返回，不会走到 RunServerReview
	for range cap(srv.sem) {
		srv.sem <- struct{}{}
	}
	defer func() {
		for range cap(srv.sem) {
			<-srv.sem
		}
	}()

	// 模拟旧 review 在被取消后完成清理
	go func() {
		<-ctx.Done() // 等待 context 被取消
		close(done)  // 通知新 review 旧的已清理完
	}()

	event := &MergeRequestEvent{
		Project:          ProjectInfo{PathWithNamespace: "group/project"},
		ObjectAttributes: MRAttributes{IID: 1},
	}

	// 在 goroutine 中触发新 review（它会取消旧的并等待）
	reviewDone := make(chan struct{})
	go func() {
		srv.triggerReview(event)
		close(reviewDone)
	}()

	// 新 entry 注册后，主动取消它，验证“排队时可取消”的行为
	var queued *inFlightEntry
	deadline := time.Now().Add(2 * time.Second)
	for {
		srv.mu.Lock()
		cur := srv.inFlight["group/project/1"]
		srv.mu.Unlock()
		if cur != nil && cur != existing {
			queued = cur
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("new in-flight review entry was not registered in time")
		}
		time.Sleep(10 * time.Millisecond)
	}
	queued.cancel()

	// 等待 triggerReview 完成（应在排队阶段收到取消并结束）
	select {
	case <-reviewDone:
		// ok
	case <-time.After(5 * time.Second):
		t.Fatal("triggerReview did not complete in time")
	}

	// 验证旧 entry 的 context 被取消了
	if ctx.Err() == nil {
		t.Error("expected old review context to be cancelled")
	}
}
