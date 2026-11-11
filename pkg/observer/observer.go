package observer

// ==== Interface ====

// Observer 执行生命周期观察者。
//
// 观察者由 Hub 统一分发事件。OnEvent 返回的 error 会被 Hub 捕获并交给
// ErrorHandler 上报，不会中断其余观察者，也不会回流到执行路径。
// 事件可能来自多个并发协程，实现方需自行保证并发安全；
// 实现方应快速返回，避免阻塞事件发送方的执行路径。
type Observer interface {
	OnEvent(event Event) error
}

// ==== Adapter ====

// ObserverFunc 将普通函数适配为 Observer。
type ObserverFunc func(event Event) error

// OnEvent 调用被适配的函数。
func (f ObserverFunc) OnEvent(event Event) error {
	return f(event)
}
