package metronome

import (
	"errors"
	"fmt"
	"log"
	"reflect"
	"sync"
)

// HubOption 配置 Hub 的可选参数。
type HubOption func(*Hub) error

// WithErrorHandler 设置 Hub 的默认错误处理器。
//
// 当 ticker 本身未实现 ErrorHandler，或其 HandleTickError 再次 panic 时，
// Hub 会退化为调用该处理器。传入 nil 时回退到默认日志输出。
func WithErrorHandler(handler func(error)) HubOption {
	return func(h *Hub) error {
		h.onError = handler
		return nil
	}
}

// WithTickPeriod 设置 Hub 作为子 ticker 时自身的节拍周期。
func WithTickPeriod(period int) HubOption {
	return func(h *Hub) error {
		if err := ensureTickPeriod(period); err != nil {
			return err
		}
		h.period = period
		return nil
	}
}

// Hub 是节拍分发中心。
//
// Hub 本身也是一个 ticker，因此可以注册到另一个 Hub 形成层级分发。收到的每个
// TickEvent 会先按子 ticker 的 TickPeriod 过滤，再按注册顺序同步转发；
// 单个 ticker 返回的 error 或 panic 都会被隔离并交给其自身的 ErrorHandler
// 或 Hub 的默认错误处理器，不会中断其余 ticker。
//
// Hub 同时管理实现 Lifecycle 的子项：Start 按注册顺序启动它们，Stop 按相反
// 顺序停止。若 Hub 已启动，运行期追加的 ticker 会先启动后注册；移除的 ticker
// 会先从分发树摘除再停止，从而避免收到半初始化或已摘除后的节拍。
type Hub struct {
	mu      sync.RWMutex
	tickers []Ticker

	lifecycleMu sync.Mutex
	started     bool

	period  int
	onError func(error)
}

// NewHub 创建一个节拍分发中心。
func NewHub(opts ...HubOption) (*Hub, error) {
	h := &Hub{period: 1}
	for _, opt := range opts {
		if err := opt(h); err != nil {
			return nil, err
		}
	}
	return h, nil
}

// TickPeriod 返回 Hub 自身作为子 ticker 时的节拍周期。
func (h *Hub) TickPeriod() int {
	h.mu.RLock()
	defer h.mu.RUnlock()

	if h.period < 1 {
		return 1
	}
	return h.period
}

// SetTickPeriod 设置 Hub 自身作为子 ticker 时的节拍周期。
func (h *Hub) SetTickPeriod(period int) error {
	if err := ensureTickPeriod(period); err != nil {
		return err
	}

	h.mu.Lock()
	defer h.mu.Unlock()
	h.period = period
	return nil
}

// AddTicker 注册一个 ticker。
//
// ticker 为 nil、重复注册、tick period 非法或注册会形成 Hub 环时返回错误。
func (h *Hub) AddTicker(ticker Ticker) error {
	if ticker == nil {
		return fmt.Errorf("metronome: cannot add nil ticker")
	}
	if err := ensureTickPeriod(tickPeriod(ticker)); err != nil {
		return err
	}
	if err := h.rejectCycle(ticker); err != nil {
		return err
	}

	h.lifecycleMu.Lock()
	defer h.lifecycleMu.Unlock()

	h.mu.Lock()
	defer h.mu.Unlock()

	for _, existing := range h.tickers {
		if sameTicker(existing, ticker) {
			return fmt.Errorf("metronome: ticker already registered")
		}
	}

	if h.started {
		if lifecycle, ok := ticker.(Lifecycle); ok {
			if err := lifecycle.Start(); err != nil {
				return fmt.Errorf("metronome: start %T: %w", ticker, err)
			}
		}
	}

	h.tickers = append(h.tickers, ticker)
	return nil
}

// RemoveTicker 注销一个 ticker。
func (h *Hub) RemoveTicker(ticker Ticker) error {
	if ticker == nil {
		return fmt.Errorf("metronome: cannot remove nil ticker")
	}

	h.lifecycleMu.Lock()
	defer h.lifecycleMu.Unlock()

	h.mu.Lock()
	index := -1
	for i, existing := range h.tickers {
		if sameTicker(existing, ticker) {
			index = i
			break
		}
	}
	if index < 0 {
		h.mu.Unlock()
		return fmt.Errorf("metronome: ticker is not registered")
	}

	h.tickers = append(h.tickers[:index], h.tickers[index+1:]...)
	started := h.started
	h.mu.Unlock()

	if started {
		if lifecycle, ok := ticker.(Lifecycle); ok {
			if err := lifecycle.Stop(); err != nil {
				return fmt.Errorf("metronome: stop %T: %w", ticker, err)
			}
		}
	}

	return nil
}

// Contains 返回某个 ticker 是否直接注册在当前 hub 上。
func (h *Hub) Contains(ticker Ticker) bool {
	h.mu.RLock()
	defer h.mu.RUnlock()

	for _, existing := range h.tickers {
		if sameTicker(existing, ticker) {
			return true
		}
	}
	return false
}

// Start 按注册顺序启动 hub 中实现 Lifecycle 的 ticker。
func (h *Hub) Start() error {
	h.lifecycleMu.Lock()
	defer h.lifecycleMu.Unlock()

	if h.started {
		return nil
	}

	h.mu.RLock()
	tickers := append([]Ticker(nil), h.tickers...)
	h.mu.RUnlock()

	started := make([]Lifecycle, 0, len(tickers))
	for _, ticker := range tickers {
		lifecycle, ok := ticker.(Lifecycle)
		if !ok {
			continue
		}
		if err := lifecycle.Start(); err != nil {
			startErr := fmt.Errorf("metronome: start %T: %w", ticker, err)
			stopErr := stopReversed(started)
			if stopErr != nil {
				return errors.Join(startErr, stopErr)
			}
			return startErr
		}
		started = append(started, lifecycle)
	}

	h.started = true
	return nil
}

// Stop 按启动的相反顺序停止 hub 中实现 Lifecycle 的 ticker，并汇总所有错误。
func (h *Hub) Stop() error {
	h.lifecycleMu.Lock()
	defer h.lifecycleMu.Unlock()

	if !h.started {
		return nil
	}

	h.mu.RLock()
	tickers := append([]Ticker(nil), h.tickers...)
	h.mu.RUnlock()

	h.started = false

	lifecycles := make([]Lifecycle, 0, len(tickers))
	for _, ticker := range tickers {
		lifecycle, ok := ticker.(Lifecycle)
		if ok {
			lifecycles = append(lifecycles, lifecycle)
		}
	}
	return stopReversed(lifecycles)
}

// OnTick 将节拍按注册顺序同步转发给所有命中周期的子 ticker。
//
// 单个 ticker 的 error 或 panic 会被隔离并交给错误处理器，本方法始终返回 nil。
func (h *Hub) OnTick(event TickEvent) error {
	h.mu.RLock()
	tickers := append([]Ticker(nil), h.tickers...)
	h.mu.RUnlock()

	for _, ticker := range tickers {
		if !shouldTick(ticker, event) {
			continue
		}
		if err := h.deliver(ticker, event); err != nil {
			h.report(err)
		}
	}
	return nil
}

// Find 在 hub 及其嵌套 hub 中查找第一个类型为 T 的 ticker。
func Find[T Ticker](hub *Hub) (T, bool) {
	var zero T

	seen := make(map[*Hub]struct{})
	var search func(*Hub) (T, bool)
	search = func(current *Hub) (T, bool) {
		if current == nil {
			return zero, false
		}
		if _, ok := seen[current]; ok {
			return zero, false
		}
		seen[current] = struct{}{}

		current.mu.RLock()
		tickers := append([]Ticker(nil), current.tickers...)
		current.mu.RUnlock()

		for _, ticker := range tickers {
			if found, ok := ticker.(T); ok {
				return found, true
			}
		}

		for _, ticker := range tickers {
			child, ok := ticker.(*Hub)
			if !ok {
				continue
			}
			if found, ok := search(child); ok {
				return found, true
			}
		}

		return zero, false
	}

	return search(hub)
}

// rejectCycle 拒绝会形成 Hub 环的注册，避免分发时无限递归。
func (h *Hub) rejectCycle(candidate Ticker) error {
	root, ok := candidate.(*Hub)
	if !ok {
		return nil
	}
	if root == h {
		return fmt.Errorf("metronome: cannot add a hub to itself")
	}

	stack := []*Hub{root}
	seen := make(map[*Hub]struct{}, 8)
	for len(stack) > 0 {
		current := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		if _, ok := seen[current]; ok {
			continue
		}
		seen[current] = struct{}{}
		if current == h {
			return fmt.Errorf("metronome: cyclic hub registration rejected: target hub already contains this hub")
		}

		current.mu.RLock()
		children := append([]Ticker(nil), current.tickers...)
		current.mu.RUnlock()
		for _, child := range children {
			if hub, ok := child.(*Hub); ok {
				stack = append(stack, hub)
			}
		}
	}
	return nil
}

// deliver 调用单个 ticker，并把返回的 error 或 panic 包装为带上下文的错误。
func (h *Hub) deliver(ticker Ticker, event TickEvent) (err error) {
	defer func() {
		if recovered := recover(); recovered != nil {
			err = fmt.Errorf("metronome: %T panicked on tick %d: %v", ticker, event.Seq, recovered)
		}
	}()

	if callErr := ticker.OnTick(event); callErr != nil {
		err = fmt.Errorf("metronome: %T failed on tick %d: %w", ticker, event.Seq, callErr)
	}
	if err == nil {
		return nil
	}

	if handler, ok := ticker.(ErrorHandler); ok {
		defer func() {
			if recovered := recover(); recovered != nil {
				err = fmt.Errorf("metronome: %T error handler panicked: %v", ticker, recovered)
			} else {
				err = nil
			}
		}()
		handler.HandleTickError(err)
		return err
	}

	return err
}

// report 将错误交给默认错误处理器；处理器自身 panic 时退化为日志输出。
func (h *Hub) report(err error) {
	handler := h.onError
	if handler == nil {
		handler = defaultErrorHandler
	}

	defer func() {
		if recovered := recover(); recovered != nil {
			defaultErrorHandler(fmt.Errorf("metronome: error handler panicked: %v", recovered))
		}
	}()
	handler(err)
}

// defaultErrorHandler 是默认错误处理器，将错误输出到标准错误。
func defaultErrorHandler(err error) {
	log.Printf("metronome: %v", err)
}

// sameTicker 判断两个 ticker 是否可视为同一实例。
//
// 仅当动态类型可比较时才进行实例判等；函数等不可比较值不会触发判重，
// 但也不会导致运行时 panic。
func sameTicker(left Ticker, right Ticker) bool {
	leftType := reflect.TypeOf(left)
	rightType := reflect.TypeOf(right)
	if leftType == nil || rightType == nil || leftType != rightType {
		return false
	}
	if !leftType.Comparable() {
		return false
	}
	return left == right
}

// stopReversed 逆序停止生命周期项，并用 errors.Join 汇总错误。
func stopReversed(lifecycles []Lifecycle) error {
	errs := make([]error, 0, len(lifecycles))
	for i := len(lifecycles) - 1; i >= 0; i-- {
		if err := lifecycles[i].Stop(); err != nil {
			errs = append(errs, fmt.Errorf("metronome: stop %T: %w", lifecycles[i], err))
		}
	}
	return errors.Join(errs...)
}
