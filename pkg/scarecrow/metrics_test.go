package scarecrow

import (
	"testing"
	"time"
)

func TestMetricsWatcherCountsInputs(t *testing.T) {
	metrics := NewMetricsWatcher()
	now := time.Now()

	if err := metrics.OnEvent(SeedInputEvent{Plot: "a", Seed: 1, SeedID: 1, Time: now}); err != nil {
		t.Fatalf("OnEvent() = %v", err)
	}
	if err := metrics.OnEvent(SeedInputEvent{Plot: "b", From: "a", Seed: 2, SeedID: 2, Time: now}); err != nil {
		t.Fatalf("OnEvent() = %v", err)
	}

	a, ok := metrics.GetPlot("a")
	if !ok {
		t.Fatal("node a not registered")
	}
	if a.ExternalInput != 1 || a.UpstreamInput != 0 || a.InputTotal != 1 {
		t.Fatalf("a inputs = external %d upstream %d total %d, want 1/0/1", a.ExternalInput, a.UpstreamInput, a.InputTotal)
	}
	if a.DownstreamCounts["b"] != 1 {
		t.Fatalf("a.DownstreamCounts[b] = %d, want 1", a.DownstreamCounts["b"])
	}

	b, ok := metrics.GetPlot("b")
	if !ok {
		t.Fatal("node b not registered")
	}
	if b.ExternalInput != 0 || b.UpstreamInput != 1 || b.InputTotal != 1 {
		t.Fatalf("b inputs = external %d upstream %d total %d, want 0/1/1", b.ExternalInput, b.UpstreamInput, b.InputTotal)
	}
	if b.UpstreamCounts["a"] != 1 {
		t.Fatalf("b.UpstreamCounts[a] = %d, want 1", b.UpstreamCounts["a"])
	}
}

func TestMetricsWatcherCountsResults(t *testing.T) {
	metrics := NewMetricsWatcher()
	now := time.Now()

	for id := 1; id <= 4; id++ {
		if err := metrics.OnEvent(SeedInputEvent{Plot: "a", Seed: id, SeedID: id, Time: now}); err != nil {
			t.Fatalf("OnEvent() = %v", err)
		}
	}
	_ = metrics.OnEvent(SeedRipenEvent{Plot: "a", SeedID: 1, FruitID: 11, Time: now})
	_ = metrics.OnEvent(SeedWitherEvent{Plot: "a", SeedID: 2, WeedID: 12, Time: now})
	_ = metrics.OnEvent(SeedPruneEvent{Plot: "a", SeedID: 3, PruneID: 13, Time: now})

	node, ok := metrics.GetPlot("a")
	if !ok {
		t.Fatal("node a not registered")
	}
	if node.Succeeded != 1 || node.Failed != 1 || node.Skipped != 1 {
		t.Fatalf("results = (%d, %d, %d), want (1, 1, 1)", node.Succeeded, node.Failed, node.Skipped)
	}
	if node.Processed != 3 {
		t.Fatalf("processed = %d, want 3", node.Processed)
	}
	if node.InputTotal != 4 || node.Pending != 1 {
		t.Fatalf("pending = %d with total %d, want 1 with total 4", node.Pending, node.InputTotal)
	}
}

func TestMetricsWatcherTracksStatus(t *testing.T) {
	metrics := NewMetricsWatcher()
	start := time.Now()
	end := start.Add(time.Second)

	if err := metrics.OnEvent(PlotStartEvent{Plot: "a", Tenders: 2, StartTime: start}); err != nil {
		t.Fatalf("OnEvent() = %v", err)
	}
	running, ok := metrics.GetPlot("a")
	if !ok {
		t.Fatal("node a not registered")
	}
	if running.Status != PlotRunning {
		t.Fatalf("status = %v, want running", running.Status)
	}
	if !running.StartTime.Equal(start) || !running.EndTime.IsZero() {
		t.Fatalf("times = (%v, %v), want (%v, zero)", running.StartTime, running.EndTime, start)
	}

	if err := metrics.OnEvent(PlotEndEvent{Plot: "a", EndTime: end, Elapsed: time.Second}); err != nil {
		t.Fatalf("OnEvent() = %v", err)
	}
	stopped, ok := metrics.GetPlot("a")
	if !ok {
		t.Fatal("node a not registered")
	}
	if stopped.Status != PlotStopped || !stopped.EndTime.Equal(end) {
		t.Fatalf("status/time = (%v, %v), want (stopped, %v)", stopped.Status, stopped.EndTime, end)
	}
}

func TestMetricsWatcherFarmView(t *testing.T) {
	metrics := NewMetricsWatcher()
	now := time.Now()
	_ = metrics.OnEvent(PlotStartEvent{Plot: "a", StartTime: now})
	_ = metrics.OnEvent(PlotStartEvent{Plot: "b", StartTime: now})

	graph := metrics.GetPlots()
	if len(graph) != 2 {
		t.Fatalf("graph size = %d, want 2", len(graph))
	}
	if graph["a"].Status != PlotRunning || graph["b"].Status != PlotRunning {
		t.Fatalf("graph statuses = (%v, %v), want running", graph["a"].Status, graph["b"].Status)
	}
	if _, ok := metrics.GetPlot("ghost"); ok {
		t.Fatal("GetPlot(ghost) = true, want false")
	}
}

func TestMetricsWatcherZeroValue(t *testing.T) {
	var metrics MetricsWatcher
	if err := metrics.OnEvent(SeedInputEvent{Plot: "a", Seed: 1, SeedID: 1}); err != nil {
		t.Fatalf("OnEvent() = %v", err)
	}

	node, ok := metrics.GetPlot("a")
	if !ok || node.ExternalInput != 1 {
		t.Fatalf("GetPlot(a) = (%#v, %v), want external 1", node, ok)
	}
}
