package reporter

import (
	"fmt"

	"github.com/Mr-xiaotian/CelestialGrow/pkg/metronome"
	"github.com/Mr-xiaotian/CelestialGrow/pkg/scarecrow"
)

const DefaultSnapshotPeriod = 1

// MetricsView 描述 reporter 读取图级指标快照所需的最小接口。
type MetricsView interface {
	GetPlots() map[string]scarecrow.PlotMetrics
}

// SnapshotSink 描述状态快照的写入目标。
type SnapshotSink interface {
	PushSnapshot(snapshot map[string]map[string]any) error
}

// SnapshotTicker 按拍读取图级指标快照并交给写入目标。
//
// 该 ticker 只负责把 MetricsView 投影为 Web 状态载荷并发送到 sink，不直接执行
// 网络 IO。实际网络推送由 PushWatcher 在后台 spout 中异步完成。
type SnapshotTicker struct {
	metricsView MetricsView
	sink        SnapshotSink
	period      int
}

// NewSnapshotTicker 创建一个图级快照 ticker。
func NewSnapshotTicker(
	metricsView MetricsView,
	sink SnapshotSink,
	period int,
) (*SnapshotTicker, error) {
	if metricsView == nil {
		return nil, fmt.Errorf("reporter: metrics view is nil")
	}
	if sink == nil {
		return nil, fmt.Errorf("reporter: snapshot sink is nil")
	}
	if period == 0 {
		period = DefaultSnapshotPeriod
	}
	if period < 1 {
		return nil, fmt.Errorf("reporter: snapshot period must be positive, got %d", period)
	}

	return &SnapshotTicker{
		metricsView: metricsView,
		sink:        sink,
		period:      period,
	}, nil
}

// TickPeriod 返回快照 ticker 的节拍周期。
func (t *SnapshotTicker) TickPeriod() int {
	return t.period
}

// OnTick 读取并发送一次图级状态快照。
func (t *SnapshotTicker) OnTick(event metronome.TickEvent) error {
	snapshot := toStatusSnapshot(t.metricsView.GetPlots())
	return t.sink.PushSnapshot(snapshot)
}
