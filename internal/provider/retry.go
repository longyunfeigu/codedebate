// Package provider 的 retry.go 文件实现了通用的重试机制，
// 用于处理 CLI 调用过程中可能出现的暂时性错误（如网络超时、限流等）。
// 采用指数退避策略，避免在错误恢复期间频繁重试导致问题恶化。
package provider

import (
	"strings"
	"time"
)

// RetryOptions 配置重试行为的参数。
// 可自定义最大重试次数、退避间隔和错误判断函数。
type RetryOptions struct {
	MaxAttempts int              // 最大尝试次数（包含首次尝试）
	BackoffMs   []int            // 退避间隔序列（毫秒），每次重试依次使用
	ShouldRetry func(error) bool // 自定义的错误判断函数，返回 true 表示应重试
}

// defaultBackoff 是默认的指数退避间隔序列（毫秒）：1秒、2秒、4秒。
// 如果重试次数超过序列长度，则使用最后一个值。
var defaultBackoff = []int{1000, 2000, 4000}

// isTransientError 判断错误是否为暂时性错误，即有可能通过重试恢复的错误。
// 检测以下类型的暂时性错误：
//   - 超时错误（timeout）
//   - 连接重置（connection reset / econnreset）
//   - 速率限制（rate limit / HTTP 429）
//   - 服务不可用（HTTP 502 / 503）
func isTransientError(err error) bool {
	if err == nil {
		return false
	}
	msg := strings.ToLower(err.Error())
	return strings.Contains(msg, "timeout") ||
		strings.Contains(msg, "connection reset") ||
		strings.Contains(msg, "econnreset") ||
		strings.Contains(msg, "rate limit") ||
		strings.Contains(msg, "429") ||
		strings.Contains(msg, "503") ||
		strings.Contains(msg, "502")
}

// WithRetry 是一个泛型重试函数，对暂时性错误自动重试并采用指数退避策略。
// 类型参数 T 允许该函数适用于任何返回类型的操作。
//
// 默认行为（opts 为 nil 时）：
//   - 最多尝试 3 次
//   - 退避间隔：1秒、2秒、4秒
//   - 使用 isTransientError 判断是否应重试
//
// 如果所有重试都失败，返回最后一次的错误。
// 如果错误不是暂时性的（如参数错误），则立即返回不再重试。
func WithRetry[T any](fn func() (T, error), opts *RetryOptions) (T, error) {
	maxAttempts := 3
	backoff := defaultBackoff
	shouldRetry := isTransientError

	// 如果提供了自定义选项，则覆盖默认值
	if opts != nil {
		if opts.MaxAttempts > 0 {
			maxAttempts = opts.MaxAttempts
		}
		if len(opts.BackoffMs) > 0 {
			backoff = opts.BackoffMs
		}
		if opts.ShouldRetry != nil {
			shouldRetry = opts.ShouldRetry
		}
	}

	var lastErr error
	var zero T // T 类型的零值，用于错误时返回
	for attempt := 1; attempt <= maxAttempts; attempt++ {
		result, err := fn()
		if err == nil {
			return result, nil // 成功，直接返回结果
		}
		lastErr = err
		// 如果已达到最大尝试次数或错误不可重试，则立即返回错误
		if attempt >= maxAttempts || !shouldRetry(err) {
			return zero, err
		}
		// 计算退避索引，超出序列长度时使用最后一个值
		idx := attempt - 1
		if idx >= len(backoff) {
			idx = len(backoff) - 1
		}
		// 按照退避间隔等待后再重试
		time.Sleep(time.Duration(backoff[idx]) * time.Millisecond)
	}
	return zero, lastErr
}
