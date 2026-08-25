package funnel

import (
	"context"
	"errors"
	"sync"
	"time"
)

// RecordHandler 定义记录处理的生命周期接口。
// BeforeStart 在消费循环启动前调用，用于初始化资源（如打开文件）。
// HandleRecord 处理单条记录。
// AfterStop 在消费循环结束后调用，用于清理资源（如关闭文件）。
type RecordHandler[T any] interface {
	BeforeStart() error
	HandleRecord(record T) error
	AfterStop() error
}

// Spout 消费端，从通道读取记录并交给 RecordHandler 处理。
// 支持优雅关闭和超时强制退出。
type Spout[T any] struct {
	// 状态字段
	ch      chan T
	wg      sync.WaitGroup
	timeout time.Duration
	ctx     context.Context
	cancel  context.CancelFunc

	// 依赖：具体的处理逻辑（接口注入）
	handler RecordHandler[T]
}

// NewSpout 创建一个 Spout，使用指定的 handler 处理记录。
// bufferSize 控制内部通道缓冲大小，timeout 为关闭时的最大等待时间。
func NewSpout[T any](handler RecordHandler[T], bufferSize int, timeout time.Duration) *Spout[T] {
	ctx, cancel := context.WithCancel(context.Background())
	return &Spout[T]{
		ch:      make(chan T, bufferSize),
		ctx:     ctx,
		cancel:  cancel,
		handler: handler,
		timeout: timeout,
	}
}

// GetQueue 返回写入通道，供 Inlet 绑定使用。
func (b *Spout[T]) GetQueue() chan<- T {
	return b.ch
}

// Start 启动消费循环。调用 handler.BeforeStart 初始化后，在后台 goroutine 中持续消费记录。
func (b *Spout[T]) Start() error {
	if err := b.handler.BeforeStart(); err != nil {
		return err
	}

	b.wg.Add(1)
	go b.spout()
	return nil
}

// spout 持续消费通道中的记录
func (b *Spout[T]) spout() {
	defer b.wg.Done()

	for {
		select {
		case record, ok := <-b.ch:
			if !ok {
				// 通道关闭，优雅退出
				return
			}
			b.handler.HandleRecord(record)

		case <-b.ctx.Done():
			// 收到取消信号
			return
		}
	}
}

// Stop 停止消费循环。先关闭通道触发优雅退出，超时后强制取消。
// 无论是否超时，都会调用 handler.AfterStop 清理资源。
func (b *Spout[T]) Stop() error {
	close(b.ch) // 先尝试优雅关闭

	// 等待 Handler 处理完，但最多等 timeout 秒
	done := make(chan struct{})
	go func() { b.wg.Wait(); close(done) }()

	var err error
	select {
	case <-done:
	case <-time.After(b.timeout):
		b.cancel()
		err = errors.New("shutdown timeout")
	}

	// 无论如何都执行清理
	b.handler.AfterStop()
	return err
}

// Handler 返回当前 Spout 持有的 record handler。
func (b *Spout[T]) Handler() RecordHandler[T] {
	return b.handler
}
