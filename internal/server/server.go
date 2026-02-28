package server

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"runtime/debug"
	"strings"
	"sync"
	"time"

	"github.com/longyunfeigu/codedebate/internal/config"
	"github.com/longyunfeigu/codedebate/internal/platform/gitlab"
	"github.com/longyunfeigu/codedebate/internal/review"
)

// ServerConfig 是 Webhook Server 的配置。
type ServerConfig struct {
	CodeDebateConfig   *config.CodeDebateConfig
	Addr          string // 监听地址，如 ":8080"
	WebhookSecret string // GitLab webhook 验证密钥
	MaxConcurrent int    // 最大并发 review 数
	GitLabHost    string // GitLab 域名
}

// inFlightEntry 表示一个正在进行的 review，支持取消和等待完成。
type inFlightEntry struct {
	cancel context.CancelFunc
	done   chan struct{}
}

// Server 是 CodeDebate 的 Webhook HTTP Server。
type Server struct {
	cfg           ServerConfig
	sem           chan struct{} // 并发信号量
	mu            sync.Mutex
	inFlight      map[string]*inFlightEntry // "projectPath/mrIID" → entry，取消重跑
	logger        *slog.Logger
	server        *http.Server
	runner        *review.Runner
	cancelCleanup context.CancelFunc
}

// newSlogLogger 根据环境变量创建 slog.Logger。
// CODEDEBATE_LOG_FORMAT: json（默认）/ text
// CODEDEBATE_LOG_LEVEL: debug/info/warn/error（默认 info）
func newSlogLogger() *slog.Logger {
	var level slog.Level
	switch strings.ToLower(os.Getenv("CODEDEBATE_LOG_LEVEL")) {
	case "debug":
		level = slog.LevelDebug
	case "warn":
		level = slog.LevelWarn
	case "error":
		level = slog.LevelError
	default:
		level = slog.LevelInfo
	}

	opts := &slog.HandlerOptions{Level: level}
	var handler slog.Handler
	if strings.ToLower(os.Getenv("CODEDEBATE_LOG_FORMAT")) == "text" {
		handler = slog.NewTextHandler(os.Stdout, opts)
	} else {
		handler = slog.NewJSONHandler(os.Stdout, opts)
	}
	return slog.New(handler)
}

// New 创建一个新的 Server 实例。
func New(cfg ServerConfig) *Server {
	if cfg.MaxConcurrent <= 0 {
		cfg.MaxConcurrent = 3
	}
	logger := newSlogLogger()
	runner := review.NewRunner(cfg.CodeDebateConfig, nil)
	return &Server{
		cfg:      cfg,
		sem:      make(chan struct{}, cfg.MaxConcurrent),
		inFlight: make(map[string]*inFlightEntry),
		logger:   logger,
		runner:   runner,
	}
}

// Start 启动 HTTP Server。
func (s *Server) Start() error {
	cleanupCtx, cancel := context.WithCancel(context.Background())
	s.cancelCleanup = cancel
	if s.runner != nil {
		s.runner.StartCleanup(cleanupCtx)
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/webhook/gitlab", s.handleWebhook)
	mux.HandleFunc("/health", s.handleHealth)

	s.server = &http.Server{
		Addr:    s.cfg.Addr,
		Handler: mux,
	}

	s.logger.Info("starting server", "addr", s.cfg.Addr)
	return s.server.ListenAndServe()
}

// Shutdown 优雅关闭 Server，取消所有进行中的 review 并等待完成。
func (s *Server) Shutdown(ctx context.Context) error {
	s.logger.Info("shutting down server")
	s.mu.Lock()
	entries := make([]*inFlightEntry, 0, len(s.inFlight))
	for _, entry := range s.inFlight {
		entry.cancel()
		entries = append(entries, entry)
	}
	s.mu.Unlock()
	for _, entry := range entries {
		<-entry.done
	}
	if s.cancelCleanup != nil {
		s.cancelCleanup()
	}
	if s.runner != nil {
		s.runner.Wait()
	}
	if s.server == nil {
		return nil
	}
	return s.server.Shutdown(ctx)
}

// handleHealth 处理健康检查请求，返回运行时状态。
func (s *Server) handleHealth(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	activeReviews := len(s.inFlight)
	s.mu.Unlock()

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(map[string]any{
		"status":         "ok",
		"active_reviews": activeReviews,
		"max_concurrent": cap(s.sem),
	})
}

// handleWebhook 处理 GitLab webhook 请求。
func (s *Server) handleWebhook(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	// TODO: 暂时跳过 webhook secret 验证
	// if !ValidateWebhookRequest(r, s.cfg.WebhookSecret) {
	// 	http.Error(w, "Unauthorized", http.StatusUnauthorized)
	// 	return
	// }

	// 解析事件
	event, err := ParseWebhookEvent(r)
	if err != nil {
		s.logger.Error("failed to parse webhook", "error", err)
		http.Error(w, "Bad request", http.StatusBadRequest)
		return
	}

	// 检查是否应触发审查
	if !ShouldTriggerReview(event) {
		s.logger.Info("event not eligible, skipping",
			"kind", event.ObjectKind, "action", event.ObjectAttributes.Action,
			"state", event.ObjectAttributes.State, "title", event.ObjectAttributes.Title)
		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(map[string]string{"status": "skipped"})
		return
	}

	// 立即返回 202 Accepted，异步执行 review
	w.WriteHeader(http.StatusAccepted)
	json.NewEncoder(w).Encode(map[string]string{"status": "accepted"})

	go s.triggerReview(event)
}

// triggerReview 异步执行审查流程，含取消重跑和并发控制。
// 同一 MR 收到新 webhook 时，取消正在进行的 review，等待其清理完成后重新执行。
func (s *Server) triggerReview(event *MergeRequestEvent) {
	key := fmt.Sprintf("%s/%d", event.Project.PathWithNamespace, event.ObjectAttributes.IID)

	// Panic recovery: 防止单次 review panic 导致整个进程 crash
	defer func() {
		if r := recover(); r != nil {
			s.logger.Error("panic in review", "key", key, "panic", r, "stack", string(debug.Stack()))
		}
	}()

	// 1. 注册自己，取消已有的 review
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	done := make(chan struct{})
	entry := &inFlightEntry{cancel: cancel, done: done}

	s.mu.Lock()
	if existing, ok := s.inFlight[key]; ok {
		s.logger.Info("cancelling previous review", "key", key)
		existing.cancel()
		waitCh := existing.done
		s.inFlight[key] = entry // 立即替换，保证后续新 webhook 取消的是自己
		s.mu.Unlock()
		<-waitCh // 等旧 review 清理完（worktree 释放、sem 归还）
	} else {
		s.inFlight[key] = entry
		s.mu.Unlock()
	}

	// 2. 清理（无论正常结束还是被取消）
	defer func() {
		cancel()
		close(done)
		s.mu.Lock()
		if cur, ok := s.inFlight[key]; ok && cur == entry {
			delete(s.inFlight, key)
		}
		s.mu.Unlock()
	}()

	// 3. 等待期间可能被更新的 webhook 取消了
	if ctx.Err() != nil {
		s.logger.Info("review superseded", "key", key)
		return
	}

	// 4. 信号量：限制并发数。超出并发上限时阻塞等待（排队），不丢弃任务。
	select {
	case s.sem <- struct{}{}:
		defer func() { <-s.sem }()
	case <-ctx.Done():
		s.logger.Warn("review cancelled while waiting for semaphore", "key", key)
		return
	}

	// 5. 执行 review
	s.logger.Info("review slot acquired", "key", key)
	plat := gitlab.New(s.cfg.GitLabHost)
	var diffExclude []string
	if s.cfg.CodeDebateConfig != nil {
		diffExclude = s.cfg.CodeDebateConfig.Defaults.DiffExclude
	}
	if err := RunServerReview(ctx, event, plat, s.runner, s.cfg.GitLabHost, diffExclude, s.logger); err != nil {
		if ctx.Err() != nil {
			s.logger.Warn("review cancelled", "key", key)
		} else {
			s.logger.Error("review failed", "key", key, "error", err)
		}
	} else {
		s.logger.Info("review goroutine finished", "key", key)
	}
}
