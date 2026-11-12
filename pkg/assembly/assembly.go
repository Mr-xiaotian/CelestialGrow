// Package assembly 负责运行期资源装配。
//
// 节点与任务图只持有观察者 Hub，不直接持有 funnel spout 或持久化观察者实例；
// 生命周期、日志等运行期资源由本包统一创建、注册、启动与回收。
package assembly

import (
	"fmt"
	"sync"

	"github.com/Mr-xiaotian/CelestialGrow/pkg/observer"
	"github.com/Mr-xiaotian/CelestialGrow/pkg/persist"
)

// Resources 是一次运行的资源句柄，持有已启动的日志与生命周期观察者。
type Resources struct {
	lifecycle *persist.LifecycleObserver
	log       *persist.LogObserver
	stopOnce  sync.Once
}

// Stop 停止并回收本次运行启动的资源，重复调用安全。
func (r *Resources) Stop() {
	r.stopOnce.Do(func() {
		_ = r.log.Stop()
		_ = r.lifecycle.Stop()
	})
}

// StartNodeResources 为单节点运行装配日志与生命周期观察者。
//
// 进入时依次启动生命周期与日志 spout，再把观察者注册到 hub；返回的 Resources
// 必须在运行结束后 Stop，以保证队列中的记录被刷盘。
func StartNodeResources(hub *observer.Hub, logLevel string) (*Resources, error) {
	return startResources(hub, logLevel)
}

// StartFarmResources 为整图运行装配日志与生命周期观察者。
//
// 目前与单节点装配内容一致；图级独有资源（如远程上报）后续在此追加，
// 避免与节点级装配耦合。
func StartFarmResources(hub *observer.Hub, logLevel string) (*Resources, error) {
	return startResources(hub, logLevel)
}

// startResources 是两类运行共用的资源装配流程。
func startResources(hub *observer.Hub, logLevel string) (*Resources, error) {
	if hub == nil {
		return nil, fmt.Errorf("assembly: observer hub is nil")
	}

	metricsObserver := observer.NewMetricsObserver()
	lifecycleObserver := persist.NewLifecycleObserver()
	logObserver := persist.NewLogObserver(metricsObserver, logLevel)

	// 指标观察者没有消费循环，先注册；日志在节点结束时读取其计数。
	if err := hub.AddObserver(metricsObserver); err != nil {
		return nil, err
	}

	// 先启动再注册：启动失败时不会在 hub 中留下未启动的观察者。
	if err := lifecycleObserver.Start(); err != nil {
		return nil, fmt.Errorf("assembly: start lifecycle observer: %w", err)
	}
	if err := logObserver.Start(); err != nil {
		_ = lifecycleObserver.Stop()
		return nil, fmt.Errorf("assembly: start log observer: %w", err)
	}

	if err := hub.AddObserver(lifecycleObserver); err != nil {
		_ = logObserver.Stop()
		_ = lifecycleObserver.Stop()
		return nil, err
	}
	if err := hub.AddObserver(logObserver); err != nil {
		_ = logObserver.Stop()
		_ = lifecycleObserver.Stop()
		return nil, err
	}

	return &Resources{lifecycle: lifecycleObserver, log: logObserver}, nil
}
