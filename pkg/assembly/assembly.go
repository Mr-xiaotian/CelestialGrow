// Package assembly 负责运行期资源装配。
//
// Plot 与 Farm 只持有观察者 Hub 与节拍 Hub，不直接持有 funnel spout、
// 持久化观察者实例或时钟；生命周期、日志与节拍时钟等运行期资源由本包统一
// 创建、注册、启动与回收。
package assembly

import (
	"fmt"
	"time"

	"github.com/Mr-xiaotian/CelestialGrow/pkg/metronome"
	"github.com/Mr-xiaotian/CelestialGrow/pkg/persist"
	"github.com/Mr-xiaotian/CelestialGrow/pkg/scarecrow"
)

// StopFunc 描述运行期资源的统一回收函数。
type StopFunc func() error

// StartLocalResources 为本地运行装配观察者、节拍 hub 与节拍时钟。
//
// 启动顺序为 watcherHub.Start() -> tickerHub.Start() -> clock.Start()，回收顺序
// 相反，从而保证日志/持久化通道关闭前不会再有新的周期任务入场。返回的 StopFunc
// 幂等，可安全重复调用。
func StartLocalResources(
	watcherHub *scarecrow.Hub,
	tickerHub *metronome.Hub,
	logLevel string,
	interval time.Duration,
) (StopFunc, error) {
	return startResources(watcherHub, tickerHub, logLevel, interval)
}

// StartReportResources 为带远程上报的运行装配资源。
//
// 当前实现与本地运行一致；后续若增加远程上报、注入等 ticker，可在此入口追加。
func StartReportResources(
	watcherHub *scarecrow.Hub,
	tickerHub *metronome.Hub,
	logLevel string,
	interval time.Duration,
) (StopFunc, error) {
	return startResources(watcherHub, tickerHub, logLevel, interval)
}

// startResources 是两类运行共用的资源装配流程。
func startResources(
	watcherHub *scarecrow.Hub,
	tickerHub *metronome.Hub,
	logLevel string,
	interval time.Duration,
) (StopFunc, error) {
	if watcherHub == nil {
		return nil, fmt.Errorf("assembly: watcher hub is nil")
	}
	if tickerHub == nil {
		return nil, fmt.Errorf("assembly: ticker hub is nil")
	}

	metricsWatcher := scarecrow.NewMetricsWatcher()
	lifecycleWatcher := persist.NewLifecycleWatcher()
	logWatcher := persist.NewLogWatcher(metricsWatcher, logLevel)

	// 指标观察者没有生命周期，先注册以便日志在 Plot 结束时读到计数。
	for _, watcher := range []scarecrow.Watcher{metricsWatcher, lifecycleWatcher, logWatcher} {
		if err := watcherHub.AddWatcher(watcher); err != nil {
			return nil, err
		}
	}

	tickerClock, err := metronome.NewClock(interval, tickerHub)
	if err != nil {
		return nil, err
	}

	if err := watcherHub.Start(); err != nil {
		return nil, err
	}
	if err := tickerHub.Start(); err != nil {
		_ = watcherHub.Stop()
		return nil, err
	}
	if err := tickerClock.Start(); err != nil {
		_ = tickerHub.Stop()
		_ = watcherHub.Stop()
		return nil, err
	}

	stopped := false
	stop := func() error {
		if stopped {
			return nil
		}
		stopped = true

		var stopErr error
		if err := tickerClock.Stop(); err != nil {
			stopErr = err
		}
		if err := tickerHub.Stop(); err != nil {
			if stopErr == nil {
				stopErr = err
			} else {
				stopErr = fmt.Errorf("%w; %w", stopErr, err)
			}
		}
		if err := watcherHub.Stop(); err != nil {
			if stopErr == nil {
				stopErr = err
			} else {
				stopErr = fmt.Errorf("%w; %w", stopErr, err)
			}
		}
		return stopErr
	}

	return stop, nil
}
