// Package assembly 负责运行期资源装配。
//
// Plot 与 Farm 只持有观察者 Hub 与节拍 Hub，不直接持有 funnel spout、
// 持久化观察者实例或时钟；生命周期、日志、远程上报与节拍时钟等运行期资源
// 由本包统一创建、注册、启动与回收。
package assembly

import (
	"fmt"
	"time"

	"github.com/Mr-xiaotian/CelestialGrow/pkg/metronome"
	"github.com/Mr-xiaotian/CelestialGrow/pkg/persist"
	"github.com/Mr-xiaotian/CelestialGrow/pkg/reporter"
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
	return startResources(watcherHub, tickerHub, nil, logLevel, interval, nil)
}

// StartReportResources 为带远程上报的运行装配资源。
//
// 在本地资源（指标、生命周期、日志）之外，额外创建推送观察者、周期快照 ticker
// 与远程注入 ticker：推送观察者接收 Farm 事件，快照 ticker 从指标视图读取整图
// 状态，注入 ticker 把远程任务交回 injectionTarget。report 为 nil 时返回错误。
func StartReportResources(
	watcherHub *scarecrow.Hub,
	tickerHub *metronome.Hub,
	injectionTarget reporter.InjectionTarget,
	logLevel string,
	interval time.Duration,
	report *reporter.Config,
) (StopFunc, error) {
	if report == nil {
		return nil, fmt.Errorf("assembly: report config is nil")
	}
	if injectionTarget == nil {
		return nil, fmt.Errorf("assembly: injection target is nil")
	}
	return startResources(watcherHub, tickerHub, injectionTarget, logLevel, interval, report)
}

// startResources 是两类运行共用的资源装配流程。
//
// report 与 injectionTarget 均为 nil 时只装配本地资源；否则额外注册上报推送
// 观察者，并把快照与注入 ticker 挂到节拍 hub 上。
func startResources(
	watcherHub *scarecrow.Hub,
	tickerHub *metronome.Hub,
	injectionTarget reporter.InjectionTarget,
	logLevel string,
	interval time.Duration,
	report *reporter.Config,
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

	watchers := []scarecrow.Watcher{metricsWatcher, lifecycleWatcher, logWatcher}

	var (
		snapshotTicker  *reporter.SnapshotTicker
		injectionTicker *reporter.InjectionTicker
	)
	if report != nil {
		pushWatcher, err := reporter.NewPushWatcher(report.SessionID, report.URL, metricsWatcher)
		if err != nil {
			return nil, err
		}

		snapshotTicker, err = reporter.NewSnapshotTicker(metricsWatcher, pushWatcher, report.SnapshotPeriod)
		if err != nil {
			return nil, err
		}

		injectionTicker, err = reporter.NewInjectionTicker(
			report.URL,
			report.SessionID,
			injectionTarget,
			report.InjectionPeriod,
			0,
		)
		if err != nil {
			return nil, err
		}

		watchers = append(watchers, pushWatcher)
	}

	// 指标观察者没有生命周期，先注册以便日志在 Plot 结束时读到计数。
	for _, watcher := range watchers {
		if err := watcherHub.AddWatcher(watcher); err != nil {
			return nil, err
		}
	}
	if snapshotTicker != nil {
		if err := tickerHub.AddTicker(snapshotTicker); err != nil {
			return nil, err
		}
	}
	if injectionTicker != nil {
		if err := tickerHub.AddTicker(injectionTicker); err != nil {
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
