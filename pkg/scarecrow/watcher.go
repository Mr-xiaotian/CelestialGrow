// Package scarecrow 提供运行期事件的观察者基础设施：
// 事件定义、分发 Hub 与指标模型。
package scarecrow

// ==== Interface ====

// Watcher 是运行期事件的接收者。
//
// Watcher 由 Hub 统一分发事件。OnEvent 返回的 error 会被 Hub 捕获并交给
// ErrorHandler 上报，不会中断其余 Watcher，也不会回流到执行路径。
// 事件可能来自多个并发协程，实现方需自行保证并发安全；
// 实现方应快速返回，避免阻塞事件发送方的执行路径。
type Watcher interface {
	OnEvent(event Event) error
}

// ==== Adapter ====

// WatcherFunc 将普通函数适配为 Watcher。
type WatcherFunc func(event Event) error

// OnEvent 调用被适配的函数。
func (f WatcherFunc) OnEvent(event Event) error {
	return f(event)
}
