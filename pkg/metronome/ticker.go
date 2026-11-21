package metronome

import "fmt"

// Ticker 是节拍接收者。
//
// OnTick 在命中自身节拍周期时被调用。返回的 error 会被上层 Hub 或 Clock
// 捕获并交给错误处理器，不会中断其余 ticker。
type Ticker interface {
	OnTick(event TickEvent) error
}

// Lifecycle 描述可被统一启动与停止的资源。
//
// Hub 会在注册的 ticker 中识别实现该接口的项：Start 按注册顺序启动，
// Stop 按相反顺序停止。未实现该接口的 ticker 仅参与节拍分发。
type Lifecycle interface {
	Start() error
	Stop() error
}

// ErrorHandler 描述 ticker 自身的错误处理钩子。
//
// 当 OnTick 返回 error 或 panic 时，Hub 与 Clock 会优先调用该钩子；若钩子
// 自身再返回 error 或 panic，则退化为所属 Hub 或 Clock 的默认错误处理器。
type ErrorHandler interface {
	HandleTickError(err error)
}

// Periodic 描述 ticker 的节拍周期。
//
// TickPeriod 返回值必须为正整数；1 表示每拍都触发，2 表示每两拍触发一次。
// Hub 与 Clock 每次分发前都会重新读取该值，因此实现方可在运行期动态调整。
type Periodic interface {
	TickPeriod() int
}

// TickerFunc 将普通函数适配为 Ticker。
type TickerFunc func(event TickEvent) error

// OnTick 调用被适配的函数。
func (f TickerFunc) OnTick(event TickEvent) error {
	return f(event)
}

// ensureTickPeriod 校验节拍周期为正整数。
func ensureTickPeriod(period int) error {
	if period < 1 {
		return fmt.Errorf("metronome: tick period must be positive, got %d", period)
	}
	return nil
}

// tickPeriod 返回 ticker 当前的节拍周期；未实现 Periodic 时默认为 1。
func tickPeriod(ticker Ticker) int {
	if periodic, ok := ticker.(Periodic); ok {
		return periodic.TickPeriod()
	}
	return 1
}

// shouldTick 判断 ticker 是否应在当前节拍触发。
func shouldTick(ticker Ticker, event TickEvent) bool {
	period := tickPeriod(ticker)
	if period < 1 {
		return false
	}
	return (event.Seq-1)%period == 0
}
