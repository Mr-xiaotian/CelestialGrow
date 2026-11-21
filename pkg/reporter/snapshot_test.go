package reporter

import (
	"errors"
	"slices"
	"testing"
	"time"

	"github.com/Mr-xiaotian/CelestialGrow/pkg/metronome"
	"github.com/Mr-xiaotian/CelestialGrow/pkg/scarecrow"
)

func TestSnapshotTickerPushesProjectedSnapshot(t *testing.T) {
	start := time.Unix(10, 0)
	end := time.Unix(20, 0)
	metricsView := stubMetricsView{
		plots: map[string]scarecrow.PlotMetrics{
			"stage_a": {
				Plot:             "stage_a",
				Status:           scarecrow.PlotStopped,
				StartTime:        start,
				EndTime:          end,
				InputTotal:       7,
				Processed:        6,
				Pending:          1,
				Succeeded:        4,
				Failed:           1,
				Skipped:          1,
				UpstreamCounts:   map[string]int{"root": 3},
				DownstreamCounts: map[string]int{"leaf": 2},
			},
		},
	}
	sink := &snapshotSinkProbe{}

	ticker, err := NewSnapshotTicker(metricsView, sink, 2)
	if err != nil {
		t.Fatalf("NewSnapshotTicker() = %v", err)
	}
	if got := ticker.TickPeriod(); got != 2 {
		t.Fatalf("TickPeriod() = %d, want 2", got)
	}
	if err := ticker.OnTick(metronome.TickEvent{Seq: 1}); err != nil {
		t.Fatalf("OnTick() = %v", err)
	}

	got, ok := sink.snapshot["stage_a"]
	if !ok {
		t.Fatal("stage_a snapshot is missing")
	}
	if got["status"] != int(scarecrow.PlotStopped) {
		t.Fatalf("status = %v, want %d", got["status"], int(scarecrow.PlotStopped))
	}
	if got["tasks_input"] != 7 || got["tasks_processed"] != 6 || got["tasks_pending"] != 1 {
		t.Fatalf("task counters = %v", got)
	}
	if got["start_time"] != 10.0 || got["end_time"] != 20.0 {
		t.Fatalf("times = start %v end %v, want 10/20", got["start_time"], got["end_time"])
	}
}

func TestSnapshotTickerPropagatesSinkError(t *testing.T) {
	want := errors.New("boom")
	ticker, err := NewSnapshotTicker(
		stubMetricsView{plots: map[string]scarecrow.PlotMetrics{}},
		&snapshotSinkProbe{err: want},
		1,
	)
	if err != nil {
		t.Fatalf("NewSnapshotTicker() = %v", err)
	}

	if err := ticker.OnTick(metronome.TickEvent{Seq: 1}); !errors.Is(err, want) {
		t.Fatalf("OnTick() error = %v, want wrap %v", err, want)
	}
}

func TestNewSnapshotTickerRejectsInvalidArguments(t *testing.T) {
	if _, err := NewSnapshotTicker(nil, &snapshotSinkProbe{}, 1); err == nil {
		t.Fatal("nil metrics view must be rejected")
	}
	if _, err := NewSnapshotTicker(stubMetricsView{}, nil, 1); err == nil {
		t.Fatal("nil sink must be rejected")
	}
	if _, err := NewSnapshotTicker(stubMetricsView{}, &snapshotSinkProbe{}, -1); err == nil {
		t.Fatal("negative period must be rejected")
	}
}

type stubMetricsView struct {
	plots map[string]scarecrow.PlotMetrics
}

func (m stubMetricsView) GetPlots() map[string]scarecrow.PlotMetrics {
	result := make(map[string]scarecrow.PlotMetrics, len(m.plots))
	for name, item := range m.plots {
		item.UpstreamCounts = copyIntMap(item.UpstreamCounts)
		item.DownstreamCounts = copyIntMap(item.DownstreamCounts)
		result[name] = item
	}
	return result
}

type snapshotSinkProbe struct {
	snapshot map[string]map[string]any
	err      error
}

func (s *snapshotSinkProbe) PushSnapshot(snapshot map[string]map[string]any) error {
	s.snapshot = copySnapshot(snapshot)
	return s.err
}

func copyIntMap(source map[string]int) map[string]int {
	result := make(map[string]int, len(source))
	for key, value := range source {
		result[key] = value
	}
	return result
}

func TestToStatusSnapshotPreservesNodeSet(t *testing.T) {
	snapshot := toStatusSnapshot(map[string]scarecrow.PlotMetrics{
		"a": {},
		"b": {},
	})
	if got, want := sortedKeys(snapshot), []string{"a", "b"}; !slices.Equal(got, want) {
		t.Fatalf("keys = %v, want %v", got, want)
	}
}

func sortedKeys[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for key := range m {
		keys = append(keys, key)
	}
	slices.Sort(keys)
	return keys
}
