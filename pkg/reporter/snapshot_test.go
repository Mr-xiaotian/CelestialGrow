package reporter

import (
	"encoding/json"
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
	if got.Status != int(scarecrow.PlotStopped) {
		t.Fatalf("status = %d, want %d", got.Status, int(scarecrow.PlotStopped))
	}
	if got.TasksInput != 7 || got.TasksProcessed != 6 || got.TasksPending != 1 {
		t.Fatalf("task counters = %+v", got)
	}
	if got.StartTime != 10.0 || got.EndTime != 20.0 {
		t.Fatalf("times = start %v end %v, want 10/20", got.StartTime, got.EndTime)
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
	snapshot map[string]NodeStatus
	err      error
}

func (s *snapshotSinkProbe) PushSnapshot(snapshot map[string]NodeStatus) error {
	s.snapshot = copySnapshot(snapshot)
	return s.err
}

func TestNodeStatusJSONContract(t *testing.T) {
	snapshot := map[string]NodeStatus{
		"a": {
			Status:           1,
			TasksInput:       2,
			TasksProcessed:   3,
			TasksPending:     4,
			TasksSucceeded:   5,
			TasksFailed:      6,
			TasksSkipped:     7,
			UpstreamCounts:   map[string]int{"b": 8},
			DownstreamCounts: map[string]int{"c": 9},
			StartTime:        10.5,
			EndTime:          11.5,
		},
	}

	data, err := json.Marshal(snapshot)
	if err != nil {
		t.Fatalf("Marshal() = %v", err)
	}

	var decoded map[string]map[string]any
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("Unmarshal() = %v", err)
	}
	for _, key := range []string{
		"status", "tasks_input", "tasks_processed", "tasks_pending",
		"tasks_succeeded", "tasks_failed", "tasks_skipped",
		"upstream_counts", "downstream_counts", "start_time", "end_time",
	} {
		if _, ok := decoded["a"][key]; !ok {
			t.Fatalf("snapshot JSON is missing %q: %s", key, data)
		}
	}
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
