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

// Lifecycle 是可被统一启动与停止的资源能力接口。
//
// Hub 会在注册的 Watcher 中识别实现该接口的观察者：Start 按注册顺序启动它们，
// Stop 按相反顺序停止。未实现该接口的 Watcher 只参与事件分发。
//
// 该接口不要求实现方是 Watcher，因此也可用于纯粹的启停型资源。
type Lifecycle interface {
	Start() error
	Stop() error
}

// ==== Adapter ====

// WatcherFunc 将普通函数适配为 Watcher。
type WatcherFunc func(event Event) error

// OnEvent 调用被适配的函数。
func (f WatcherFunc) OnEvent(event Event) error {
	return f(event)
}
