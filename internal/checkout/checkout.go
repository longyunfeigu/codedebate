package checkout

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/longyunfeigu/codedebate/internal/config"
)

const (
	defaultTTL = 24 * time.Hour
)

// Params 描述一次 checkout 所需的输入参数。
type Params struct {
	Platform string // "github" | "gitlab"
	Repo     string // "owner/repo" 或 "group/project"
	MRNumber string
	Host     string // 平台域名；为空时使用默认
}

// Result 描述一次 checkout 的输出结果。
type Result struct {
	RepoDir   string // 本次 review 独立 worktree 目录
	Available bool
	FromCache bool
	cleanup   func()
}

// Release 释放本次 checkout 生成的 worktree。
func (r *Result) Release() {
	if r == nil || r.cleanup == nil {
		return
	}
	r.cleanup()
}

// Manager 管理 mirror 缓存与每次 review 的独立 worktree。
type Manager struct {
	baseDir string
	ttl     time.Duration

	mu             sync.Mutex
	repoLocks      map[string]*sync.Mutex // mirrorDir -> lock
	activeByMirror map[string]int         // mirrorDir -> active worktree 数
	active         sync.WaitGroup
	cancelFn       context.CancelFunc
}

// NewManager 根据配置创建 checkout manager。nil 或 disabled 返回 nil。
func NewManager(cfg *config.CheckoutConfig) *Manager {
	if cfg == nil || !cfg.Enabled {
		return nil
	}

	baseDir := strings.TrimSpace(cfg.BaseDir)
	if baseDir == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			baseDir = filepath.Join(".codedebate", "repos")
		} else {
			baseDir = filepath.Join(home, ".codedebate", "repos")
		}
	}

	ttl := defaultTTL
	if strings.TrimSpace(cfg.TTL) != "" {
		if parsed, err := time.ParseDuration(cfg.TTL); err == nil && parsed > 0 {
			ttl = parsed
		}
	}

	return &Manager{
		baseDir:        baseDir,
		ttl:            ttl,
		repoLocks:      make(map[string]*sync.Mutex),
		activeByMirror: make(map[string]int),
	}
}

// Checkout 获取仓库 mirror 并创建本次 review 独立 worktree。
func (m *Manager) Checkout(params Params) Result {
	if m == nil {
		return Result{}
	}
	if strings.TrimSpace(params.Repo) == "" {
		return Result{}
	}

	mirrorDir := m.mirrorDir(params)
	cloneURL := buildCloneURL(params.Platform, params.Repo, params.Host)
	if cloneURL == "" {
		return Result{}
	}

	lock := m.repoLock(mirrorDir)
	lock.Lock()
	fromCache, err := m.ensureMirror(cloneURL, mirrorDir)
	if err != nil {
		lock.Unlock()
		return Result{}
	}

	worktreeDir, err := m.createWorktree(mirrorDir, params)
	lock.Unlock()
	if err != nil {
		return Result{}
	}

	m.markActiveStart(mirrorDir)
	once := sync.Once{}
	return Result{
		RepoDir:   worktreeDir,
		Available: true,
		FromCache: fromCache,
		cleanup: func() {
			once.Do(func() {
				m.removeWorktree(mirrorDir, worktreeDir)
				m.markActiveDone(mirrorDir)
			})
		},
	}
}

// StartCleanup 启动定期清理过期 mirror 的后台任务。
func (m *Manager) StartCleanup(ctx context.Context) {
	if m == nil {
		return
	}

	runCtx, cancel := context.WithCancel(ctx)
	m.mu.Lock()
	if m.cancelFn != nil {
		m.mu.Unlock()
		cancel()
		return
	}
	m.cancelFn = cancel
	m.mu.Unlock()

	go func() {
		m.cleanupExpiredMirrors()
		interval := m.ttl / 2
		if interval < time.Minute {
			interval = time.Minute
		}
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			select {
			case <-runCtx.Done():
				return
			case <-ticker.C:
				m.cleanupExpiredMirrors()
			}
		}
	}()
}

// Wait 等待所有活跃 checkout 释放。
func (m *Manager) Wait() {
	if m == nil {
		return
	}
	m.active.Wait()
}

func (m *Manager) repoLock(mirrorDir string) *sync.Mutex {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.repoLocks == nil {
		m.repoLocks = make(map[string]*sync.Mutex)
	}
	if lock, ok := m.repoLocks[mirrorDir]; ok {
		return lock
	}
	lock := &sync.Mutex{}
	m.repoLocks[mirrorDir] = lock
	return lock
}

func (m *Manager) mirrorDir(params Params) string {
	platform := strings.ToLower(strings.TrimSpace(params.Platform))
	if platform == "" {
		platform = "github"
	}
	repo := strings.Trim(strings.TrimSpace(params.Repo), "/")
	repo = strings.TrimSuffix(repo, ".git")
	return filepath.Join(m.baseDir, platform, filepath.FromSlash(repo)+".git")
}

func (m *Manager) markActiveStart(mirrorDir string) {
	m.active.Add(1)
	m.mu.Lock()
	m.activeByMirror[mirrorDir]++
	m.mu.Unlock()
}

func (m *Manager) markActiveDone(mirrorDir string) {
	m.mu.Lock()
	if m.activeByMirror[mirrorDir] <= 1 {
		delete(m.activeByMirror, mirrorDir)
	} else {
		m.activeByMirror[mirrorDir]--
	}
	m.mu.Unlock()
	m.active.Done()
}

func (m *Manager) isMirrorActive(mirrorDir string) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.activeByMirror[mirrorDir] > 0
}

func (m *Manager) isMirrorExpired(mirrorDir string) bool {
	info, err := os.Stat(mirrorDir)
	if err != nil {
		return true
	}
	return time.Since(info.ModTime()) > m.ttl
}

func (m *Manager) cleanupExpiredMirrors() {
	entries, err := os.ReadDir(m.baseDir)
	if err != nil {
		return
	}

	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		if entry.Name() == "worktrees" {
			continue
		}
		root := filepath.Join(m.baseDir, entry.Name())
		_ = filepath.WalkDir(root, func(path string, d os.DirEntry, walkErr error) error {
			if walkErr != nil {
				return nil
			}
			if !d.IsDir() {
				return nil
			}
			if !strings.HasSuffix(d.Name(), ".git") {
				return nil
			}
			if m.isMirrorActive(path) {
				return filepath.SkipDir
			}
			if m.isMirrorExpired(path) {
				_ = os.RemoveAll(path)
			}
			return filepath.SkipDir
		})
	}
}
