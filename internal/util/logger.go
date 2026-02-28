// Package util 提供 CodeDebate 项目的通用工具函数，包括日志记录等。
package util

import (
	"fmt"
	"os"
	"strings"
)

// LogLevel 表示日志级别。
// 数值越大级别越高，只有不低于当前设置级别的日志消息才会被输出。
type LogLevel int

// 日志级别常量，使用 iota 自动递增。
// 级别从低到高：Debug < Info < Warn < Error
const (
	LevelDebug LogLevel = iota // 调试级别：详细的调试信息
	LevelInfo                  // 信息级别：一般运行信息
	LevelWarn                  // 警告级别：需要注意的潜在问题
	LevelError                 // 错误级别：运行错误
)

// levelNames 将日志级别名称字符串映射到 LogLevel 常量。
// 用于从环境变量 CODEDEBATE_LOG_LEVEL 解析日志级别配置。
var levelNames = map[string]LogLevel{
	"debug": LevelDebug,
	"info":  LevelInfo,
	"warn":  LevelWarn,
	"error": LevelError,
}

// logger 是包级别的全局日志实例，在 init() 中初始化。
var logger *Logger

// init 在包加载时自动初始化全局日志实例。
func init() {
	logger = NewLogger()
}

// Logger 是一个简单的分级日志器。
// 所有日志输出到 stderr，以避免与程序的正常输出（stdout）混淆。
type Logger struct {
	level LogLevel // 当前日志级别，低于此级别的消息将被过滤
}

// NewLogger 创建一个新的 Logger 实例。
// 默认日志级别为 Info，可通过 CODEDEBATE_LOG_LEVEL 环境变量覆盖
// （支持 "debug"、"info"、"warn"、"error"）。
func NewLogger() *Logger {
	l := &Logger{level: LevelInfo}
	if envLevel := os.Getenv("CODEDEBATE_LOG_LEVEL"); envLevel != "" {
		if level, ok := levelNames[strings.ToLower(envLevel)]; ok {
			l.level = level
		}
	}
	return l
}

// shouldLog 判断指定级别的日志是否应该被输出。
// 只有级别 >= 当前设置级别的消息才会被记录。
func (l *Logger) shouldLog(level LogLevel) bool {
	return level >= l.level
}

// Debug 输出调试级别日志。
func (l *Logger) Debug(args ...interface{}) {
	if l.shouldLog(LevelDebug) {
		fmt.Fprintln(os.Stderr, append([]interface{}{"[DEBUG]"}, args...)...)
	}
}

// Debugf 输出格式化的调试级别日志。
func (l *Logger) Debugf(format string, args ...interface{}) {
	if l.shouldLog(LevelDebug) {
		fmt.Fprintf(os.Stderr, "[DEBUG] "+format+"\n", args...)
	}
}

// Info 输出信息级别日志。
func (l *Logger) Info(args ...interface{}) {
	if l.shouldLog(LevelInfo) {
		fmt.Fprintln(os.Stderr, append([]interface{}{"[INFO]"}, args...)...)
	}
}

// Infof 输出格式化的信息级别日志。
func (l *Logger) Infof(format string, args ...interface{}) {
	if l.shouldLog(LevelInfo) {
		fmt.Fprintf(os.Stderr, "[INFO] "+format+"\n", args...)
	}
}

// Warn 输出警告级别日志。
func (l *Logger) Warn(args ...interface{}) {
	if l.shouldLog(LevelWarn) {
		fmt.Fprintln(os.Stderr, append([]interface{}{"[WARN]"}, args...)...)
	}
}

// Warnf 输出格式化的警告级别日志。
func (l *Logger) Warnf(format string, args ...interface{}) {
	if l.shouldLog(LevelWarn) {
		fmt.Fprintf(os.Stderr, "[WARN] "+format+"\n", args...)
	}
}

// Error 输出错误级别日志。
func (l *Logger) Error(args ...interface{}) {
	if l.shouldLog(LevelError) {
		fmt.Fprintln(os.Stderr, append([]interface{}{"[ERROR]"}, args...)...)
	}
}

// Errorf 输出格式化的错误级别日志。
func (l *Logger) Errorf(format string, args ...interface{}) {
	if l.shouldLog(LevelError) {
		fmt.Fprintf(os.Stderr, "[ERROR] "+format+"\n", args...)
	}
}

// SetLevel 设置日志级别。低于此级别的消息将不再被输出。
func (l *Logger) SetLevel(level LogLevel) {
	l.level = level
}

// 包级别的便捷函数，委托给全局 logger 实例。
// 允许直接调用 util.Debug()、util.Info() 等，无需获取 Logger 实例。
func Debug(args ...interface{})                 { logger.Debug(args...) }
func Debugf(format string, args ...interface{}) { logger.Debugf(format, args...) }
func Info(args ...interface{})                  { logger.Info(args...) }
func Infof(format string, args ...interface{})  { logger.Infof(format, args...) }
func Warn(args ...interface{})                  { logger.Warn(args...) }
func Warnf(format string, args ...interface{})  { logger.Warnf(format, args...) }
func Error(args ...interface{})                 { logger.Error(args...) }
func Errorf(format string, args ...interface{}) { logger.Errorf(format, args...) }
func SetLevel(level LogLevel)                   { logger.SetLevel(level) }
