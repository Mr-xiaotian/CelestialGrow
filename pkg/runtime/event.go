package runtime

import "sync"

// EventClient 定义事件客户端的最小发射能力。
type EventClient interface {
	Emit(type_ string, parents []int) int
}

// LocalEventClient 提供进程内的事件 ID 分配。
type LocalEventClient struct {
	mu     sync.Mutex
	nextID int
}

// NewLocalEventClient 创建一个本地事件客户端。
func NewLocalEventClient() EventClient {
	return &LocalEventClient{}
}

// Emit 发射一个事件并返回递增的本地事件 ID。
func (e *LocalEventClient) Emit(type_ string, parents []int) int {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.nextID++
	return e.nextID
}
