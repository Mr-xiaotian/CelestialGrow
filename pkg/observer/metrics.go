package observer

import (
	"sync"
	"time"
)

// ==== Types ====

// PlotStatus Plot 运行状态。
type PlotStatus int

const (
	PlotNotStarted PlotStatus = iota // 尚未收到 PlotStartEvent
	PlotRunning                      // 正在运行
	PlotStopped                      // 已结束
)

// String 返回状态的可读名称。
func (s PlotStatus) String() string {
	switch s {
	case PlotRunning:
		return "running"
	case PlotStopped:
		return "stopped"
	default:
		return "not_started"
	}
}

// PlotMetrics 单个 Plot 的指标快照。
type PlotMetrics struct {
	Plot   string     // Plot 名称
	Status PlotStatus // 运行状态

	StartTime time.Time // Plot 启动时间
	EndTime   time.Time // Plot 结束时间

	ExternalInput int // 外部注入的种子数
	UpstreamInput int // 上游投递的种子数
	InputTotal    int // 输入种子总数

	Succeeded int // 成熟数
	Failed    int // 枯萎数
	Skipped   int // 剪枝数
	Processed int // 已处理数（成熟 + 枯萎 + 剪枝）
	Pending   int // 已输入但尚未得出结果的数量

	UpstreamCounts   map[string]int // 来源 Plot -> 投递数量
	DownstreamCounts map[string]int // 目标 Plot -> 产出数量
}

// ==== Observer ====

// MetricsObserver 从事件流重建 Plot 指标。
//
// 存储格随首个事件按需建立：Plot 生命周期事件维护状态与起止时间，种子事件累加
// 输入与结果计数；上游投递会同时写入接收方的上游计数与来源方的下游计数。
// 读取返回不可变的 PlotMetrics 快照。事件可能来自多个并发协程，读取与写入
// 均受同一把读写锁保护。
type MetricsObserver struct {
	mu    sync.RWMutex
	plots map[string]*plotMetrics
}

// plotMetrics 是单个 Plot 的内部可变存储格。
type plotMetrics struct {
	status PlotStatus
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
	return &MetricsObserver{plots: make(map[string]*plotMetrics)}
}

// plot 按需建立 Plot 存储格（幂等）。调用方需自行持有写锁。
func (m *MetricsObserver) plot(name string) *plotMetrics {
	if m.plots == nil {
		m.plots = make(map[string]*plotMetrics)
	}
	metrics, ok := m.plots[name]
	if !ok {
		metrics = &plotMetrics{
			upstream:   make(map[string]int),
			downstream: make(map[string]int),
		}
		m.plots[name] = metrics
	}
	return metrics
}

// snapshot 基于当前存储构造 Plot 快照。调用方需自行持有读锁。
func (p *plotMetrics) snapshot(name string) PlotMetrics {
	upstream := make(map[string]int, len(p.upstream))
	upstreamInput := 0
	for source, count := range p.upstream {
		upstream[source] = count
		upstreamInput += count
	}

	downstream := make(map[string]int, len(p.downstream))
	for target, count := range p.downstream {
		downstream[target] = count
	}

	inputTotal := p.external + upstreamInput
	processed := p.success + p.fail + p.skip
	pending := inputTotal - processed
	if pending < 0 {
		pending = 0
	}

	return PlotMetrics{
		Plot:             name,
		Status:           p.status,
		StartTime:        p.start,
		EndTime:          p.end,
		ExternalInput:    p.external,
		UpstreamInput:    upstreamInput,
		InputTotal:       inputTotal,
		Succeeded:        p.success,
		Failed:           p.fail,
		Skipped:          p.skip,
		Processed:        processed,
		Pending:          pending,
		UpstreamCounts:   upstream,
		DownstreamCounts: downstream,
	}
}

// OnEvent 根据事件更新 Plot 指标。
func (m *MetricsObserver) OnEvent(event Event) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	switch ev := event.(type) {
	case PlotStartEvent:
		metrics := m.plot(ev.Plot)
		metrics.status = PlotRunning
		metrics.start = ev.StartTime

	case PlotEndEvent:
		metrics := m.plot(ev.Plot)
		metrics.status = PlotStopped
		metrics.end = ev.EndTime

	case SeedInputEvent:
		metrics := m.plot(ev.Plot)
		if ev.From == "" {
			metrics.external++
		} else {
			metrics.upstream[ev.From]++
			m.plot(ev.From).downstream[ev.Plot]++
		}

	case SeedRipenEvent:
		m.plot(ev.Plot).success++

	case SeedWitherEvent:
		m.plot(ev.Plot).fail++

	case SeedPruneEvent:
		m.plot(ev.Plot).skip++

	case FarmStartEvent, FarmEndEvent, SeedReplantEvent,
		SealInputEvent, SealMergeEvent:
		// 不影响 Plot 指标。
	}

	return nil
}

// ==== Views ====

// GetPlot 获取单个 Plot 的指标快照；未登记时 ok 为 false。
func (m *MetricsObserver) GetPlot(name string) (PlotMetrics, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	metrics, ok := m.plots[name]
	if !ok {
		return PlotMetrics{}, false
	}
	return metrics.snapshot(name), true
}

// GetPlots 获取全部已登记 Plot 的指标快照。
func (m *MetricsObserver) GetPlots() map[string]PlotMetrics {
	m.mu.RLock()
	defer m.mu.RUnlock()

	result := make(map[string]PlotMetrics, len(m.plots))
	for name, metrics := range m.plots {
		result[name] = metrics.snapshot(name)
	}
	return result
}
