package observer

import (
	"sync"
	"time"
)

// ==== Types ====

// NodeStatus 节点运行状态。
type NodeStatus int

const (
	NodeNotStarted NodeStatus = iota // 尚未收到节点启动事件
	NodeRunning                      // 正在运行
	NodeStopped                      // 已结束
)

// String 返回状态的可读名称。
func (s NodeStatus) String() string {
	switch s {
	case NodeRunning:
		return "running"
	case NodeStopped:
		return "stopped"
	default:
		return "not_started"
	}
}

// NodeMetrics 单个节点的指标快照。
type NodeMetrics struct {
	Node   string     // 节点名称
	Status NodeStatus // 运行状态

	StartTime time.Time // 节点启动时间
	EndTime   time.Time // 节点结束时间

	ExternalInput int // 外部注入的输入数
	UpstreamInput int // 上游投递的输入数
	InputTotal    int // 输入总数

	Succeeded int // 成功数
	Failed    int // 失败数
	Skipped   int // 跳过数
	Processed int // 已处理数（成功 + 失败 + 跳过）
	Pending   int // 已输入但尚未得出结果的数量

	UpstreamCounts   map[string]int // 来源节点 -> 投递数量
	DownstreamCounts map[string]int // 目标节点 -> 产出数量
}

// ==== Observer ====

// MetricsObserver 从事件流重建节点指标。
//
// 存储格随首个事件按需建立：节点生命周期事件维护状态与起止时间，任务事件累加
// 输入与结果计数；上游投递会同时写入接收方的上游计数与来源方的下游计数。
// 读取返回不可变的 NodeMetrics 快照。事件可能来自多个并发协程，读取与写入
// 均受同一把读写锁保护。
type MetricsObserver struct {
	mu    sync.RWMutex
	nodes map[string]*nodeMetrics
}

// nodeMetrics 是单个节点的内部可变存储格。
type nodeMetrics struct {
	status NodeStatus
	start  time.Time
	end    time.Time

	external int
	success  int
	fail     int
	skip     int

	upstream   map[string]int
	downstream map[string]int
}

// NewMetricsObserver 创建一个指标观察者。
func NewMetricsObserver() *MetricsObserver {
	return &MetricsObserver{nodes: make(map[string]*nodeMetrics)}
}

// node 按需建立节点存储格（幂等）。调用方需自行持有写锁。
func (m *MetricsObserver) node(name string) *nodeMetrics {
	if m.nodes == nil {
		m.nodes = make(map[string]*nodeMetrics)
	}
	metrics, ok := m.nodes[name]
	if !ok {
		metrics = &nodeMetrics{
			upstream:   make(map[string]int),
			downstream: make(map[string]int),
		}
		m.nodes[name] = metrics
	}
	return metrics
}

// snapshot 基于当前存储构造节点快照。调用方需自行持有读锁。
func (n *nodeMetrics) snapshot(name string) NodeMetrics {
	upstream := make(map[string]int, len(n.upstream))
	upstreamInput := 0
	for source, count := range n.upstream {
		upstream[source] = count
		upstreamInput += count
	}

	downstream := make(map[string]int, len(n.downstream))
	for target, count := range n.downstream {
		downstream[target] = count
	}

	inputTotal := n.external + upstreamInput
	processed := n.success + n.fail + n.skip
	pending := inputTotal - processed
	if pending < 0 {
		pending = 0
	}

	return NodeMetrics{
		Node:             name,
		Status:           n.status,
		StartTime:        n.start,
		EndTime:          n.end,
		ExternalInput:    n.external,
		UpstreamInput:    upstreamInput,
		InputTotal:       inputTotal,
		Succeeded:        n.success,
		Failed:           n.fail,
		Skipped:          n.skip,
		Processed:        processed,
		Pending:          pending,
		UpstreamCounts:   upstream,
		DownstreamCounts: downstream,
	}
}

// OnEvent 根据事件更新节点指标。
func (m *MetricsObserver) OnEvent(event Event) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	switch ev := event.(type) {
	case NodeStartEvent:
		metrics := m.node(ev.Node)
		metrics.status = NodeRunning
		metrics.start = ev.StartTime

	case NodeEndEvent:
		metrics := m.node(ev.Node)
		metrics.status = NodeStopped
		metrics.end = ev.EndTime

	case TaskInputEvent:
		metrics := m.node(ev.Node)
		if ev.From == "" {
			metrics.external++
		} else {
			metrics.upstream[ev.From]++
			m.node(ev.From).downstream[ev.Node]++
		}

	case TaskSuccessEvent:
		m.node(ev.Node).success++

	case TaskFailEvent:
		m.node(ev.Node).fail++

	case TaskSkipEvent:
		m.node(ev.Node).skip++

	case GraphStartEvent, GraphEndEvent, TaskRetryEvent,
		TerminationInputEvent, TerminationMergeEvent:
		// 不影响节点指标。
	}

	return nil
}

// ==== Views ====

// GetNode 获取单个节点的指标快照；未登记时 ok 为 false。
func (m *MetricsObserver) GetNode(name string) (NodeMetrics, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	metrics, ok := m.nodes[name]
	if !ok {
		return NodeMetrics{}, false
	}
	return metrics.snapshot(name), true
}

// GetGraph 获取全部已登记节点的指标快照。
func (m *MetricsObserver) GetGraph() map[string]NodeMetrics {
	m.mu.RLock()
	defer m.mu.RUnlock()

	result := make(map[string]NodeMetrics, len(m.nodes))
	for name, metrics := range m.nodes {
		result[name] = metrics.snapshot(name)
	}
	return result
}
