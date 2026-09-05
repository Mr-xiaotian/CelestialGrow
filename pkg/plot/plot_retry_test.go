package plot_test

import (
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Mr-xiaotian/CelestialGrow/pkg/plot"
)

// 重试后成功。
func TestPlot_RetrySuccess(t *testing.T) {
	var attempts atomic.Int32
	cultivator := func(seed int) (int, error) {
		n := attempts.Add(1)
		if n <= 2 {
			return 0, errors.New("transient error")
		}
		return seed * 10, nil
	}

	plot := plot.NewPlot("test_retry_success", cultivator,
		plot.WithTenders(1),
		plot.WithMaxRetries(3),
	)
	plot.Run([]int{1})
	records := mustHarvest(t, plot)

	if len(records) != 1 {
		t.Fatalf("expected 1 status, got %d", len(records))
	}
	if records[0].Status != "success" || records[0].ResultJSON != "10" {
		t.Fatalf("expected success/result 10, got %#v", records[0])
	}
	if attempts.Load() != 3 {
		t.Errorf("expected 3 attempts, got %d", attempts.Load())
	}
}

// 重试耗尽仍失败。
func TestPlot_RetryExhausted(t *testing.T) {
	var attempts atomic.Int32
	cultivator := func(seed int) (int, error) {
		attempts.Add(1)
		return 0, errors.New("permanent error")
	}

	plot := plot.NewPlot("test_retry_exhausted", cultivator,
		plot.WithTenders(1),
		plot.WithMaxRetries(2),
	)
	plot.Run([]int{1})
	records := mustHarvest(t, plot)

	if len(records) != 1 {
		t.Fatalf("expected 1 status, got %d", len(records))
	}
	if records[0].Status != "failed" || records[0].ErrorMessage != "permanent error" {
		t.Fatalf("expected failed/permanent error, got %#v", records[0])
	}
	if attempts.Load() != 3 {
		t.Errorf("expected 3 attempts (1 + 2 retries), got %d", attempts.Load())
	}
}

// retryIf 过滤不可重试错误。
func TestPlot_RetryIf(t *testing.T) {
	var attempts atomic.Int32
	permanent := errors.New("permanent")
	cultivator := func(seed int) (int, error) {
		attempts.Add(1)
		return 0, permanent
	}

	plot := plot.NewPlot("test_retry_if", cultivator,
		plot.WithTenders(1),
		plot.WithMaxRetries(3),
		plot.WithRetryIf(func(err error) bool {
			return !errors.Is(err, permanent)
		}),
	)
	plot.Run([]int{1})
	records := mustHarvest(t, plot)

	if len(records) != 1 {
		t.Fatalf("expected 1 status, got %d", len(records))
	}
	if records[0].Status != "failed" || records[0].ErrorMessage != "permanent" {
		t.Fatalf("expected failed/permanent, got %#v", records[0])
	}
	if attempts.Load() != 1 {
		t.Errorf("expected 1 attempt (no retry for permanent error), got %d", attempts.Load())
	}
}

// retryDelay 验证间隔被调用。
func TestPlot_RetryDelay(t *testing.T) {
	var attempts atomic.Int32
	cultivator := func(seed int) (int, error) {
		n := attempts.Add(1)
		if n <= 1 {
			return 0, errors.New("transient")
		}
		return seed, nil
	}

	start := time.Now()
	plot := plot.NewPlot("test_retry_delay", cultivator,
		plot.WithTenders(1),
		plot.WithMaxRetries(2),
		plot.WithRetryDelay(func(attempt int) time.Duration {
			return 100 * time.Millisecond
		}),
	)
	plot.Run([]int{1})
	elapsed := time.Since(start)
	records := mustHarvest(t, plot)

	if len(records) != 1 {
		t.Fatalf("expected 1 status, got %d", len(records))
	}
	if records[0].Status != "success" || records[0].ResultJSON != "1" {
		t.Fatalf("expected success/result 1, got %#v", records[0])
	}
	if elapsed < 100*time.Millisecond {
		t.Errorf("expected at least 100ms delay, got %v", elapsed)
	}
}
