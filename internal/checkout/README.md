# checkout

管理 Git 仓库的本地 mirror 缓存与按次创建的独立 worktree，为 code review 提供本地源码访问能力。

## 核心概念

```
~/.codedebate/repos/
├── github/owner/repo.git    ← mirror (bare repo，长期缓存)
├── gitlab/group/project.git ← mirror
└── worktrees/
    ├── wt-abc123/           ← 单次 review 的 worktree（用完即删）
    └── wt-def456/
```

- **Mirror** — `git clone --mirror` 创建的 bare 仓库，按 `platform/owner/repo.git` 路径存储。已有 mirror 时只做 `fetch --all --prune`，无需重新 clone。
- **Worktree** — 每次 `Checkout()` 从 mirror 创建一个 detached worktree，review 结束后通过 `Result.Release()` 自动清理。
- **TTL** — mirror 有过期时间（默认 24h），过期且无活跃 worktree 时被后台清理任务删除。

## API

### Manager

```go
mgr := checkout.NewManager(cfg)  // 根据 config.CheckoutConfig 创建；nil/disabled 返回 nil
mgr.StartCleanup(ctx)            // 启动后台定期清理过期 mirror
mgr.Wait()                       // 等待所有活跃 worktree 释放
```

### Checkout / Release

```go
result := mgr.Checkout(checkout.Params{
    Platform: "github",          // "github" | "gitlab"
    Repo:     "owner/repo",      // 仓库路径
    MRNumber: "42",              // PR/MR 编号（可选，空则 checkout HEAD）
    Host:     "",                // 自定义域名（可选，默认 github.com / gitlab.com）
})

if result.Available {
    // result.RepoDir 即可浏览的本地源码目录
    // result.FromCache 表示是否命中 mirror 缓存
    defer result.Release()       // 用完释放 worktree
}
```

### Params

| 字段 | 类型 | 说明 |
|------|------|------|
| `Platform` | string | `"github"` 或 `"gitlab"` |
| `Repo` | string | 仓库路径，如 `"owner/repo"` |
| `MRNumber` | string | PR/MR 编号；为空时 checkout HEAD |
| `Host` | string | 平台域名；为空使用默认值 |

### Result

| 字段 | 类型 | 说明 |
|------|------|------|
| `RepoDir` | string | worktree 目录路径 |
| `Available` | bool | 是否成功获取 |
| `FromCache` | bool | 是否命中 mirror 缓存 |
| `Release()` | func | 释放 worktree（幂等，可安全多次调用） |

## 内部流程

```
Checkout(params)
  │
  ├─ buildCloneURL()          构造 clone URL（支持 https / 自定义 host / 本地路径）
  │
  ├─ ensureMirror()           确保 mirror 存在且最新
  │    ├─ 已有且未过期 → fetch --all --prune
  │    ├─ 已有但过期且无活跃 worktree → 删除后重新 clone --mirror
  │    └─ 不存在 → clone --mirror
  │
  ├─ createWorktree()         从 mirror 创建 detached worktree
  │    ├─ MRNumber 非空 → fetch origin pull/N/head 或 merge-requests/N/head
  │    └─ worktree add --detach
  │
  └─ 返回 Result（含 cleanup 回调）
```

## 并发安全

- 每个 mirror 目录有独立的 `sync.Mutex`，同一仓库的并发 checkout 串行执行 mirror 操作。
- 活跃 worktree 计数（`activeByMirror`）防止清理任务删除仍在使用的 mirror。
- `Release()` 通过 `sync.Once` 保证幂等。
