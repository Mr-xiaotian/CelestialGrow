// Package assembly 负责运行期资源装配。
//
// Plot 与 Farm 只持有观察者 Hub，不直接持有 funnel spout 或持久化观察者实例；
// 生命周期、日志等运行期资源由本包统一创建、注册、启动与回收。
package assembly

import (
	"fmt"
	"sync"

	"github.com/Mr-xiaotian/CelestialGrow/pkg/persist"
	"github.com/Mr-xiaotian/CelestialGrow/pkg/scarecrow"
)

// Resources 是一次运行的资源句柄，持有已启动的日志与生命周期观察者。
type Resources struct {
	lifecycle *persist.LifecycleWatcher
	log       *persist.LogWatcher
	stopOnce  sync.Once
}

// Stop 停止并回收本次运行启动的资源，重复调用安全。
func (r *Resources) Stop() {
	r.stopOnce.Do(func() {
		_ = r.log.Stop()
		_ = r.lifecycle.Stop()
	})
}

// StartPlotResources 为单个 Plot 运行装配日志与生命周期观察者。
//
// 进入时依次启动生命周期与日志 spout，再把观察者注册到 hub；返回的 Resources
// 必须在运行结束后 Stop，以保证队列中的记录被刷盘。
func StartPlotResources(hub *scarecrow.Hub, logLevel string) (*Resources, error) {
	return startResources(hub, logLevel)
}

// StartFarmResources 为整个 Farm 运行装配日志与生命周期观察者。
//
// 目前与 Plot 级装配内容一致；Farm 级独有资源（如远程上报）后续在此追加，
// 避免与 Plot 级装配耦合。
func StartFarmResources(hub *scarecrow.Hub, logLevel string) (*Resources, error) {
	return startResources(hub, logLevel)
}

// startResources 是两类运行共用的资源装配流程。
func startResources(hub *scarecrow.Hub, logLevel string) (*Resources, error) {
	if hub == nil {
		return nil, fmt.Errorf("assembly: watcher hub is nil")
	}

	metricsWatcher := scarecrow.NewMetricsWatcher()
	lifecycleWatcher := persist.NewLifecycleWatcher()
	logWatcher := persist.NewLogWatcher(metricsWatcher, logLevel)

	// 指标观察者没有消费循环，先注册；日志在 Plot 结束时读取其计数。
	if err := hub.AddWatcher(metricsWatcher); err != nil {
		return nil, err
	}

	// 先启动再注册：启动失败时不会在 hub 中留下未启动的观察者。
	if err := lifecycleWatcher.Start(); err != nil {
		return nil, fmt.Errorf("assembly: start lifecycle watcher: %w", err)
	}
	if err := logWatcher.Start(); err != nil {
		_ = lifecycleWatcher.Stop()
		return nil, fmt.Errorf("assembly: start log watcher: %w", err)
	}

	if err := hub.AddWatcher(lifecycleWatcher); err != nil {
		_ = logWatcher.Stop()
		_ = lifecycleWatcher.Stop()
		return nil, err
	}
	if err := hub.AddWatcher(logWatcher); err != nil {
		_ = logWatcher.Stop()
		_ = lifecycleWatcher.Stop()
		return nil, err
	}

	return &Resources{lifecycle: lifecycleWatcher, log: logWatcher}, nil
}
