package scarecrow

import (
	"errors"
	"fmt"
	"log"
	"sync"
)

// ==== Options ====

// HubOption 配置 Hub 的可选参数。
type HubOption func(*Hub)

// WithErrorHandler 设置观察者回调出错时的处理器。
// 传入 nil 时回退到默认处理器（输出到标准错误）。
func WithErrorHandler(handler func(error)) HubOption {
	return func(h *Hub) {
		h.onError = handler
	}
}

// ==== Hub ====

// Hub 事件分发中心。
//
// 本身实现 Watcher，因此可以注册到另一个 Hub 形成层级分发。收到的每个
// 事件按注册顺序同步转发给所有 Watcher；单个 Watcher 返回的 error 或 panic
// 都会被捕获并交给 ErrorHandler，不会中断其余 Watcher。
//
// Watcher 列表采用写时复制语义：AddWatcher 在写锁下追加切片，OnEvent 仅在
// 读锁下取一次切片快照，随后在锁外遍历。append 只会替换底层数组而不会修改
// 已发布的元素，因此快照在并发追加下依然完整，遍历也无需持锁。
//
// Hub 同时管理实现 Lifecycle 的子项：Start 按注册顺序启动它们，Stop 按相反
// 顺序停止。嵌套的 Hub 本身也实现 Lifecycle，因此生命周期会沿层级递归。
type Hub struct {
	mu        sync.RWMutex
	listeners []Watcher
	onError   func(error)

	lifecycleMu sync.Mutex
	state       hubState
	lifecycle   []Lifecycle
}

// hubState 表示 Hub 的生命周期状态。
type hubState int

const (
	hubIdle    hubState = iota // 尚未启动
	hubRunning                 // 已启动，子项正在运行
	hubStopped                 // 已停止，不可再次启动
)

// NewHub 创建一个分发中心。
func NewHub(opts ...HubOption) *Hub {
	h := &Hub{}
	for _, opt := range opts {
		opt(h)
	}
	if h.onError == nil {
		h.onError = defaultErrorHandler
	}
	return h
}

// defaultErrorHandler 是默认错误处理器，将错误输出到标准错误。
func defaultErrorHandler(err error) {
	log.Printf("scarecrow: %v", err)
}

// ==== Registration ====

// AddWatcher 注册一个 Watcher。
// listener 为 nil 或注册会形成 Hub 环时返回错误。
//
// 环检测在加锁前完成，避免与其他 Hub 的锁形成交叉等待；因此两个 Hub 同时
// 互相注册的极端并发场景仍可能绕过检测，不建议在运行时并发构建 Hub 拓扑。
func (h *Hub) AddWatcher(listener Watcher) error {
	if listener == nil {
		return fmt.Errorf("scarecrow: cannot add nil listener")
	}
	if err := h.rejectCycle(listener); err != nil {
		return err
	}

	h.mu.Lock()
	defer h.mu.Unlock()
	h.listeners = append(h.listeners, listener)
	return nil
}

// rejectCycle 拒绝会形成 Hub 环的注册，避免分发时无限递归。
func (h *Hub) rejectCycle(candidate Watcher) error {
	root, ok := candidate.(*Hub)
	if !ok {
		return nil
	}
	if root == h {
		return fmt.Errorf("scarecrow: cannot add a hub to itself")
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
			return fmt.Errorf("scarecrow: cyclic hub registration rejected: target hub already contains this hub")
		}

		current.mu.RLock()
		children := current.listeners
		current.mu.RUnlock()
		for _, child := range children {
			if hub, ok := child.(*Hub); ok {
				stack = append(stack, hub)
			}
		}
	}
	return nil
}

// ==== Lifecycle ====

// Start 按注册顺序启动 hub 中实现 Lifecycle 的观察者。
//
// 未实现 Lifecycle 的观察者只参与事件分发，会被跳过。某个观察者启动失败时，
// 本次已启动的观察者会按相反顺序停止，Hub 转为已停止状态（不可再次启动），
// 返回的错误包含失败者类型。已启动时重复调用无副作用。
func (h *Hub) Start() error {
	h.lifecycleMu.Lock()
	defer h.lifecycleMu.Unlock()

	switch h.state {
	case hubRunning:
		return nil
	case hubStopped:
		return fmt.Errorf("scarecrow: hub already stopped")
	}

	h.mu.RLock()
	listeners := h.listeners
	h.mu.RUnlock()

	started := make([]Lifecycle, 0, len(listeners))
	for _, listener := range listeners {
		lifecycle, ok := listener.(Lifecycle)
		if !ok {
			continue
		}
		if err := lifecycle.Start(); err != nil {
			startErr := fmt.Errorf("scarecrow: start %T: %w", listener, err)
			stopErr := stopReversed(started)
			// 已停止的子项无法重新启动（如 spout 的通道已关闭），因此失败即终态。
			h.state = hubStopped
			if stopErr != nil {
				return errors.Join(startErr, stopErr)
			}
			return startErr
		}
		started = append(started, lifecycle)
	}

	h.lifecycle = started
	h.state = hubRunning
	return nil
}

// Stop 按启动的相反顺序停止 hub 管理的观察者，并汇总所有错误。
//
// 未启动或已停止时调用无副作用，可安全重复调用。停止后观察者仍留在 hub 中，
// 事件分发与 Find 查找不受影响。
func (h *Hub) Stop() error {
	h.lifecycleMu.Lock()
	defer h.lifecycleMu.Unlock()

	if h.state != hubRunning {
		return nil
	}

	started := h.lifecycle
	h.lifecycle = nil
	h.state = hubStopped

	return stopReversed(started)
}

// stopReversed 逆序停止观察者，并用 errors.Join 汇总错误。
func stopReversed(lifecycles []Lifecycle) error {
	errs := make([]error, 0, len(lifecycles))
	for i := len(lifecycles) - 1; i >= 0; i-- {
		if err := lifecycles[i].Stop(); err != nil {
			errs = append(errs, fmt.Errorf("scarecrow: stop %T: %w", lifecycles[i], err))
		}
	}
	return errors.Join(errs...)
}

// ==== Dispatch ====

// OnEvent 将事件按注册顺序同步分发给所有观察者。
//
// 单个观察者的 error 或 panic 会被包装后交给 ErrorHandler，其余观察者继续
// 执行。本方法始终返回 nil：观察者故障已由 ErrorHandler 消费，不回流到
// 执行路径。nil 事件会被忽略。
func (h *Hub) OnEvent(event Event) error {
	if event == nil {
		return nil
	}

	h.mu.RLock()
	listeners := h.listeners
	h.mu.RUnlock()

	for _, listener := range listeners {
		if err := h.deliver(listener, event); err != nil {
			h.report(err)
		}
	}
	return nil
}

// deliver 调用单个观察者，并把它返回的 error 或 panic 包装为带上下文的错误。
func (h *Hub) deliver(listener Watcher, event Event) (err error) {
	defer func() {
		if recovered := recover(); recovered != nil {
			err = fmt.Errorf("scarecrow: %T panicked on %T: %v", listener, event, recovered)
		}
	}()

	if callErr := listener.OnEvent(event); callErr != nil {
		return fmt.Errorf("scarecrow: %T failed on %T: %w", listener, event, callErr)
	}
	return nil
}

// ==== Lookup ====

// Find 在 hub 及其嵌套 hub 中查找第一个类型为 T 的观察者。
//
// 直属观察者优先于嵌套 hub，同级按注册顺序。hub 为 nil 或未找到时返回零值与
// false。嵌套由 Hub 支持（Hub 本身也是 Watcher），环在注册时已被拒绝，
// 这里仍用 seen 集合兜底，避免异常拓扑导致递归不终止。
func Find[T Watcher](hub *Hub) (T, bool) {
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
		listeners := current.listeners
		current.mu.RUnlock()

		for _, candidate := range listeners {
			if typed, ok := candidate.(T); ok {
				return typed, true
			}
		}
		for _, candidate := range listeners {
			if child, ok := candidate.(*Hub); ok {
				if found, ok := search(child); ok {
					return found, true
				}
			}
		}
		return zero, false
	}

	return search(hub)
}

// report 将错误交给 ErrorHandler；处理器自身 panic 时退化为默认输出。
// 零值 Hub 未设置处理器时同样回退到默认输出。
func (h *Hub) report(err error) {
	handler := h.onError
	if handler == nil {
		handler = defaultErrorHandler
	}

	defer func() {
		if recovered := recover(); recovered != nil {
			defaultErrorHandler(fmt.Errorf("scarecrow: error handler panicked: %v", recovered))
		}
	}()
	handler(err)
}
