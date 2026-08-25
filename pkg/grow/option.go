package grow

import (
	"runtime"
	"time"
)

// ==== Types ====

// Option 配置 Plot 的可选参数函数。
type Option func(*plotOptions)

// plotOptions 存储所有可选参数的内部结构体。
type plotOptions struct {
	numTends   int
	chanSize   int
	maxRetries int
	retryDelay func(attempt int) time.Duration
	retryIf    func(error) bool
	logLevel   string
}

// ==== Defaults ====

// defaultOptions 返回默认配置。
func defaultOptions() plotOptions {
	return plotOptions{
		numTends:   runtime.NumCPU(),
		chanSize:   runtime.NumCPU(),
		maxRetries: 1,
		retryDelay: func(attempt int) time.Duration { return 0 },
		retryIf:    func(error) bool { return true },
		logLevel:   "INFO",
	}
}

// ==== Option Functions ====

// WithTends 设置并发 tend 协程数。默认为 runtime.NumCPU()。
func WithTends(n int) Option {
	return func(o *plotOptions) {
		o.numTends = n
	}
}

// WithChanSize 设置 seedChan/fruitChan 的缓冲区大小。默认为 runtime.NumCPU()。
func WithChanSize(n int) Option {
	return func(o *plotOptions) {
		o.chanSize = n
	}
}

// WithMaxRetries 设置最大重试次数（不含首次执行）。
// 例如 WithMaxRetries(2) 表示最多执行 3 次（1 次原始 + 2 次重试）。
// 默认为 1。
func WithMaxRetries(n int) Option {
	return func(o *plotOptions) {
		o.maxRetries = n
	}
}

// WithRetryDelay 设置重试间隔策略。attempt 从 1 开始递增。
func WithRetryDelay(fn func(attempt int) time.Duration) Option {
	return func(o *plotOptions) {
		o.retryDelay = fn
	}
}

// WithRetryIf 设置错误过滤器，返回 true 的错误才会触发重试。默认全部重试。
func WithRetryIf(fn func(error) bool) Option {
	return func(o *plotOptions) {
		o.retryIf = fn
	}
}

// WithLogLevel 设置日志最低级别。默认为 "INFO"。
func WithLogLevel(level string) Option {
	return func(o *plotOptions) {
		o.logLevel = level
	}
}
