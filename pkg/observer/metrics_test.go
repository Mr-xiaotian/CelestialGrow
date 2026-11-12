package observer

import (
	"testing"
	"time"
)

func TestMetricsObserverCountsInputs(t *testing.T) {
	metrics := NewMetricsObserver()
	now := time.Now()

	if err := metrics.OnEvent(TaskInputEvent{Node: "a", Seed: 1, SeedID: 1, Time: now}); err != nil {
		t.Fatalf("OnEvent() = %v", err)
	}
	if err := metrics.OnEvent(TaskInputEvent{Node: "b", From: "a", Seed: 2, SeedID: 2, Time: now}); err != nil {
		t.Fatalf("OnEvent() = %v", err)
	}

	a, ok := metrics.GetNode("a")
	if !ok {
		t.Fatal("node a not registered")
	}
	if a.ExternalInput != 1 || a.UpstreamInput != 0 || a.InputTotal != 1 {
		t.Fatalf("a inputs = external %d upstream %d total %d, want 1/0/1", a.ExternalInput, a.UpstreamInput, a.InputTotal)
	}
	if a.DownstreamCounts["b"] != 1 {
		t.Fatalf("a.DownstreamCounts[b] = %d, want 1", a.DownstreamCounts["b"])
	}

	b, ok := metrics.GetNode("b")
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

func TestMetricsObserverCountsResults(t *testing.T) {
	metrics := NewMetricsObserver()
	now := time.Now()

	for id := 1; id <= 4; id++ {
		if err := metrics.OnEvent(TaskInputEvent{Node: "a", Seed: id, SeedID: id, Time: now}); err != nil {
			t.Fatalf("OnEvent() = %v", err)
		}
	}
	_ = metrics.OnEvent(TaskSuccessEvent{Node: "a", SeedID: 1, FruitID: 11, Time: now})
	_ = metrics.OnEvent(TaskFailEvent{Node: "a", SeedID: 2, WeedID: 12, Time: now})
	_ = metrics.OnEvent(TaskSkipEvent{Node: "a", SeedID: 3, PruneID: 13, Time: now})

	node, ok := metrics.GetNode("a")
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

func TestMetricsObserverTracksStatus(t *testing.T) {
	metrics := NewMetricsObserver()
	start := time.Now()
	end := start.Add(time.Second)

	if err := metrics.OnEvent(NodeStartEvent{Node: "a", Tenders: 2, StartTime: start}); err != nil {
		t.Fatalf("OnEvent() = %v", err)
	}
	running, ok := metrics.GetNode("a")
	if !ok {
		t.Fatal("node a not registered")
	}
	if running.Status != NodeRunning {
		t.Fatalf("status = %v, want running", running.Status)
	}
	if !running.StartTime.Equal(start) || !running.EndTime.IsZero() {
		t.Fatalf("times = (%v, %v), want (%v, zero)", running.StartTime, running.EndTime, start)
	}

	if err := metrics.OnEvent(NodeEndEvent{Node: "a", EndTime: end, Elapsed: time.Second}); err != nil {
		t.Fatalf("OnEvent() = %v", err)
	}
	stopped, ok := metrics.GetNode("a")
	if !ok {
		t.Fatal("node a not registered")
	}
	if stopped.Status != NodeStopped || !stopped.EndTime.Equal(end) {
		t.Fatalf("status/time = (%v, %v), want (stopped, %v)", stopped.Status, stopped.EndTime, end)
	}
}

func TestMetricsObserverGraphView(t *testing.T) {
	metrics := NewMetricsObserver()
	now := time.Now()
	_ = metrics.OnEvent(NodeStartEvent{Node: "a", StartTime: now})
	_ = metrics.OnEvent(NodeStartEvent{Node: "b", StartTime: now})

	graph := metrics.GetGraph()
	if len(graph) != 2 {
		t.Fatalf("graph size = %d, want 2", len(graph))
	}
	if graph["a"].Status != NodeRunning || graph["b"].Status != NodeRunning {
		t.Fatalf("graph statuses = (%v, %v), want running", graph["a"].Status, graph["b"].Status)
	}
	if _, ok := metrics.GetNode("ghost"); ok {
		t.Fatal("GetNode(ghost) = true, want false")
	}
}

func TestMetricsObserverZeroValue(t *testing.T) {
	var metrics MetricsObserver
	if err := metrics.OnEvent(TaskInputEvent{Node: "a", Seed: 1, SeedID: 1}); err != nil {
		t.Fatalf("OnEvent() = %v", err)
	}

	node, ok := metrics.GetNode("a")
	if !ok || node.ExternalInput != 1 {
		t.Fatalf("GetNode(a) = (%#v, %v), want external 1", node, ok)
	}
}
