package funnel

import (
	"context"
	"fmt"
	"time"
)

// Inlet 生产端，向通道写入记录。支持上下文取消和超时控制。
type Inlet[T any] struct {
	ch      chan<- T
	timeout time.Duration
	ctx     context.Context
	cancel  context.CancelFunc
}

// NewInlet 创建一个 Inlet，绑定到指定的写通道，超时后 Send 将返回错误。
func NewInlet[T any](ch chan<- T, timeout time.Duration) *Inlet[T] {
	ctx, cancel := context.WithCancel(context.Background())
	return &Inlet[T]{ch: ch, timeout: timeout, ctx: ctx, cancel: cancel}
}

// Send 发送记录，支持上下文取消和超时控制
func (s *Inlet[T]) Send(record T) error {
	select {
	case s.ch <- record:
		return nil
	case <-s.ctx.Done():
		return s.ctx.Err()
	case <-time.After(s.timeout):
		return fmt.Errorf("inlet send timeout after %v", s.timeout)
	}
}

// Close 关闭 Inlet，后续 Send 调用将返回错误
func (s *Inlet[T]) Close() {
	s.cancel()
}
