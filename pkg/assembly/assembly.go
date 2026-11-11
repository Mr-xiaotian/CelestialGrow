// Package assembly 负责运行期资源装配。
//
// 节点与任务图只持有观察者 Hub，不直接持有 funnel spout 或持久化观察者实例；
// 生命周期、日志等运行期资源由本包统一创建、注册、启动与回收。
package assembly

import (
	"fmt"

	"github.com/Mr-xiaotian/CelestialGrow/pkg/observer"
	"github.com/Mr-xiaotian/CelestialGrow/pkg/persist"
)

// RunNodeResources 为单节点运行装配日志与生命周期观察者，执行 fn 后统一回收。
//
// 进入时把观察者注册到 hub，并依次启动生命周期与日志 spout；fn 返回后按相反
// 顺序停止，保证运行期即使返回错误也能完成回收。fn 会收到生命周期数据库路径，
// 调用方可据此在运行结束后读取状态快照。
func RunNodeResources(hub *observer.Hub, logLevel string, fn func(lifecycleDBPath string) error) error {
	return runResources(hub, logLevel, fn)
}

// RunFarmResources 为整图运行装配日志与生命周期观察者，执行 fn 后统一回收。
//
// 目前与单节点装配内容一致；图级独有资源（如远程上报）后续在此追加，
// 避免与节点级装配耦合。
func RunFarmResources(hub *observer.Hub, logLevel string, fn func(lifecycleDBPath string) error) error {
	return runResources(hub, logLevel, fn)
}

// runResources 是两类运行共用的资源装配流程。
func runResources(hub *observer.Hub, logLevel string, fn func(lifecycleDBPath string) error) error {
	if hub == nil {
		return fmt.Errorf("assembly: observer hub is nil")
	}

	lifecycleObserver := persist.NewLifecycleObserver()
	logObserver := persist.NewLogObserver(logLevel)
	if err := hub.AddObserver(lifecycleObserver); err != nil {
		return err
	}
	if err := hub.AddObserver(logObserver); err != nil {
		return err
	}

	if err := lifecycleObserver.Start(); err != nil {
		return fmt.Errorf("assembly: start lifecycle observer: %w", err)
	}
	if err := logObserver.Start(); err != nil {
		_ = lifecycleObserver.Stop()
		return fmt.Errorf("assembly: start log observer: %w", err)
	}
	defer func() {
		_ = logObserver.Stop()
		_ = lifecycleObserver.Stop()
	}()

	if fn == nil {
		return nil
	}
	return fn(lifecycleObserver.DBPath())
}
