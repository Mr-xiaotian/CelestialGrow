// Package assembly 负责运行期资源装配。
//
// Plot 与 Farm 只持有观察者 Hub，不直接持有 funnel spout 或持久化观察者实例；
// 生命周期、日志等运行期资源由本包统一创建与注册，启动与回收交给 Hub 统一管理。
package assembly

import (
	"fmt"

	"github.com/Mr-xiaotian/CelestialGrow/pkg/persist"
	"github.com/Mr-xiaotian/CelestialGrow/pkg/scarecrow"
)

// StartPlotResources 为单个 Plot 运行装配日志与生命周期观察者，并启动 hub 中的资源。
//
// 观察者按指标、生命周期、日志的顺序注册，Hub 以相同顺序启动、相反顺序停止。
// 返回 error 时不会留下已启动的观察者；调用方在运行结束后需调用 hub.Stop 回收。
func StartPlotResources(hub *scarecrow.Hub, logLevel string) error {
	return startResources(hub, logLevel)
}

// StartFarmResources 为整个 Farm 运行装配日志与生命周期观察者，并启动 hub 中的资源。
//
// 目前与 Plot 级装配内容一致；Farm 级独有资源（如远程上报）后续在此追加，
// 避免与 Plot 级装配耦合。
func StartFarmResources(hub *scarecrow.Hub, logLevel string) error {
	return startResources(hub, logLevel)
}

// startResources 是两类运行共用的资源装配流程。
func startResources(hub *scarecrow.Hub, logLevel string) error {
	if hub == nil {
		return fmt.Errorf("assembly: watcher hub is nil")
	}

	metricsWatcher := scarecrow.NewMetricsWatcher()
	lifecycleWatcher := persist.NewLifecycleWatcher()
	logWatcher := persist.NewLogWatcher(metricsWatcher, logLevel)

	// 指标观察者没有生命周期，先注册以便日志在 Plot 结束时读到计数。
	for _, watcher := range []scarecrow.Watcher{metricsWatcher, lifecycleWatcher, logWatcher} {
		if err := hub.AddWatcher(watcher); err != nil {
			return err
		}
	}

	return hub.Start()
}
