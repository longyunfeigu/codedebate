# util - 工具库

提供项目级基础工具，当前包含分级日志系统。

## 文件说明

| 文件 | 说明 |
|------|------|
| `logger.go` | 分级日志器：全局单例、4 个日志级别、环境变量控制、输出到 stderr |

## 日志级别

| 级别 | 常量值 | 说明 |
|------|--------|------|
| `debug` | 0 (`LevelDebug`) | 详细调试信息，如配置加载路径、中间变量 |
| `info` | 1 (`LevelInfo`) | 一般运行信息（默认级别） |
| `warn` | 2 (`LevelWarn`) | 警告，非致命但需关注的问题 |
| `error` | 3 (`LevelError`) | 运行错误 |

过滤规则：只有级别 >= 当前设置级别的消息才会输出。默认级别为 `info`。

## 使用方式

通过环境变量控制日志级别：

```bash
# 显示所有日志（包括 debug）
CODEDEBATE_LOG_LEVEL=debug codedebate review 42

# 仅显示警告和错误
CODEDEBATE_LOG_LEVEL=warn codedebate review 42

# 默认行为（info 及以上）
codedebate review 42
```

代码中使用包级便捷函数（委托给全局 logger 实例）：

```go
util.Debugf("loading config from %s", path)
util.Info("review started")
util.Warnf("context gathering failed: %v", err)
util.Errorf("provider error: %v", err)
```

也可以创建独立的 Logger 实例：

```go
logger := util.NewLogger()
logger.SetLevel(util.LevelDebug)
logger.Debugf("custom logger: %s", msg)
```

每个级别都提供两种方法：

```go
util.Debug(args...)          // fmt.Fprintln 风格
util.Debugf(format, args...) // fmt.Fprintf 风格
util.Info(args...)
util.Infof(format, args...)
util.Warn(args...)
util.Warnf(format, args...)
util.Error(args...)
util.Errorf(format, args...)
util.SetLevel(level)         // 动态调整日志级别
```

## 日志输出格式

```
[DEBUG] loading config from /home/user/.codedebate/config.yaml
[INFO] review started
[WARN] context gathering failed: ripgrep not found
[ERROR] provider error: connection timeout
```

格式说明：`[LEVEL] message`，不包含时间戳（由 `fmt.Fprintf` 直接输出）。

注意：`Debug` / `Info` 等无格式的方法使用 `fmt.Fprintln`，会在 `[LEVEL]` 和消息之间有空格分隔：

```go
util.Info("review started")
// 输出: [INFO] review started
//       ^--- Fprintln 自动在各参数间加空格

util.Infof("review %s started", "PR #42")
// 输出: [INFO] review PR #42 started
```

## Logger 架构

```
全局单例 logger（init() 中初始化）
├── 输出: os.Stderr（不污染 stdout）
├── 格式: [LEVEL] message
├── 控制: CODEDEBATE_LOG_LEVEL 环境变量（debug/info/warn/error）
├── 默认: LevelInfo
└── 级别映射:
    levelNames = map[string]LogLevel{
        "debug": 0,  // LevelDebug
        "info":  1,  // LevelInfo
        "warn":  2,  // LevelWarn
        "error": 3,  // LevelError
    }

包级便捷函数（12 个）:
├── Debug / Debugf   → logger.Debug / logger.Debugf
├── Info  / Infof    → logger.Info  / logger.Infof
├── Warn  / Warnf    → logger.Warn  / logger.Warnf
├── Error / Errorf   → logger.Error / logger.Errorf
└── SetLevel         → logger.SetLevel
```

设计决策：输出到 `stderr` 而非 `stdout`，这样 `codedebate review` 的程序输出（markdown/json）可以通过管道传递，而调试日志不会混入其中。

```bash
# 程序输出（markdown 报告）走 stdout，日志走 stderr
codedebate review 42 --format markdown > report.md  # report.md 中没有日志
codedebate review 42 --format markdown 2>/dev/null  # 静默日志，仅看报告
```
