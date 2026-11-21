package metronome

import (
	"fmt"
	"sync"
	"time"
)

const (
	// DefaultInterval 是默认基准周期。
	DefaultInterval = time.Second

	joinTimeout = 5 * time.Second
)

// Clock 是节拍树唯一的时间源头。
//
// Clock 在独立 goroutine 中以固定速率触发节拍：以 time.Now 的单调时钟分量为
// 基准锚定计划触发时刻，每拍构造 TickEvent 并交给根 ticker。若某拍处理耗时
// 超过周期，则跳过错过的节拍，不做追赶，避免请求风暴。
//
// 根 ticker 抛出的 error 或 panic 会优先交给其自身的 ErrorHandler；若处理器
// 自身再 panic，则退化为默认日志输出。时钟因此不会因节拍处理失败而静默停摆。
type Clock struct {
	root Ticker

	mu       sync.RWMutex
	interval time.Duration

	stateMu sync.Mutex
	running bool
	stopCh  chan struct{}
	doneCh  chan struct{}
	wakeCh  chan struct{}
}

// NewClock 创建一个节拍时钟。
func NewClock(interval time.Duration, root Ticker) (*Clock, error) {
	if root == nil {
		return nil, fmt.Errorf("metronome: root ticker is nil")
	}
	if interval <= 0 {
		return nil, fmt.Errorf("metronome: interval must be positive, got %s", interval)
	}

	return &Clock{
		root:     root,
		interval: interval,
	}, nil
}

// Start 启动后台节拍 goroutine。
func (c *Clock) Start() error {
	c.stateMu.Lock()
	defer c.stateMu.Unlock()

	if c.running {
		return nil
	}

	c.stopCh = make(chan struct{})
	c.doneCh = make(chan struct{})
	c.wakeCh = make(chan struct{}, 1)
	c.running = true

	go c.run(c.stopCh, c.doneCh, c.wakeCh)
	return nil
}

// Stop 停止后台节拍 goroutine。
func (c *Clock) Stop() error {
	c.stateMu.Lock()
	if !c.running {
		c.stateMu.Unlock()
		return nil
	}

	stopCh := c.stopCh
	doneCh := c.doneCh
	c.running = false
	c.stateMu.Unlock()

	close(stopCh)

	select {
	case <-doneCh:
		return nil
	case <-time.After(joinTimeout):
		return fmt.Errorf("metronome: clock goroutine did not terminate within %s", joinTimeout)
	}
}

// IsRunning 返回时钟是否正在运行。
func (c *Clock) IsRunning() bool {
	c.stateMu.Lock()
	defer c.stateMu.Unlock()
	return c.running
}

// GetInterval 返回当前基准周期。
func (c *Clock) GetInterval() time.Duration {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.interval
}

// SetInterval 动态调整基准周期，下一拍起生效并重新锚定计划时刻。
func (c *Clock) SetInterval(interval time.Duration) error {
	if interval <= 0 {
		return fmt.Errorf("metronome: interval must be positive, got %s", interval)
	}

	c.mu.Lock()
	c.interval = interval
	c.mu.Unlock()

	c.stateMu.Lock()
	wakeCh := c.wakeCh
	running := c.running
	c.stateMu.Unlock()

	if running {
		select {
		case wakeCh <- struct{}{}:
		default:
		}
	}

	return nil
}

// run 是后台节拍主循环。
func (c *Clock) run(stopCh <-chan struct{}, doneCh chan<- struct{}, wakeCh <-chan struct{}) {
	defer close(doneCh)

	seq := 0
	prevFire := time.Now()
	timer := time.NewTimer(c.GetInterval())
	defer timer.Stop()

	for {
		interval := c.GetInterval()
		nextDeadline := prevFire.Add(interval)
		wait := time.Until(nextDeadline)
		if wait < 0 {
			wait = 0
		}

		if !timer.Stop() {
			select {
			case <-timer.C:
			default:
			}
		}
		timer.Reset(wait)

		select {
		case <-stopCh:
			return
		case <-wakeCh:
			if time.Now().Before(prevFire.Add(c.GetInterval())) {
				continue
			}
		case <-timer.C:
		}

		select {
		case <-stopCh:
			return
		default:
		}

		interval = c.GetInterval()
		fired := time.Now()
		if fired.Before(prevFire.Add(interval)) {
			continue
		}

		scheduled := prevFire.Add(interval)
		lateness := fired.Sub(scheduled)
		skipped := 0
		if lateness > 0 {
			skipped = int(lateness / interval)
		}
		prevFire = scheduled.Add(time.Duration(skipped) * interval)
		seq++

		event := TickEvent{
			Seq:     seq,
			FiredAt: fired,
		}
		if !shouldTick(c.root, event) {
			continue
		}
		c.deliver(event)
	}
}

// deliver 调用根 ticker，并隔离其 error / panic。
func (c *Clock) deliver(event TickEvent) {
	var err error
	func() {
		defer func() {
			if recovered := recover(); recovered != nil {
				err = fmt.Errorf("metronome: %T panicked on tick %d: %v", c.root, event.Seq, recovered)
			}
		}()

		if callErr := c.root.OnTick(event); callErr != nil {
			err = fmt.Errorf("metronome: %T failed on tick %d: %w", c.root, event.Seq, callErr)
		}
	}()

	if err == nil {
		return
	}

	if handler, ok := c.root.(ErrorHandler); ok {
		defer func() {
			if recovered := recover(); recovered != nil {
				defaultErrorHandler(fmt.Errorf("metronome: %T error handler panicked: %v", c.root, recovered))
			}
		}()
		handler.HandleTickError(err)
		return
	}

	defaultErrorHandler(err)
}
