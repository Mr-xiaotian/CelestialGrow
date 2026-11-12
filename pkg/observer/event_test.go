package observer

import (
	"errors"
	"testing"
	"time"
)

// ==== Probe ====

// nodeProbe 按 MetricsObserver 所需口径记录单个节点的事件状态。
type nodeProbe struct {
	started bool
	ended   bool
	start   time.Time
	end     time.Time

	external   int
	upstream   map[string]int
	downstream map[string]int

	succeeded int
	failed    int
	skipped   int
}

// metricsProbe 从事件流重建指标，用于验证事件字段是否足以支撑 metric。
type metricsProbe struct {
	nodes        map[string]*nodeProbe
	graphStarted bool
	graphEnded   bool
}

func newMetricsProbe() *metricsProbe {
	return &metricsProbe{nodes: make(map[string]*nodeProbe)}
}

func (m *metricsProbe) node(name string) *nodeProbe {
	probe, ok := m.nodes[name]
	if !ok {
		probe = &nodeProbe{
			upstream:   make(map[string]int),
			downstream: make(map[string]int),
		}
		m.nodes[name] = probe
	}
	return probe
}

func (m *metricsProbe) OnEvent(event Event) error {
	switch ev := event.(type) {
	case FarmStartEvent:
		m.graphStarted = true
	case FarmEndEvent:
		m.graphEnded = true
	case PlotStartEvent:
		probe := m.node(ev.Plot)
		probe.started = true
		probe.start = ev.StartTime
	case PlotEndEvent:
		probe := m.node(ev.Plot)
		probe.ended = true
		probe.end = ev.EndTime
	case SeedInputEvent:
		probe := m.node(ev.Plot)
		if ev.From == "" {
			probe.external++
			break
		}
		probe.upstream[ev.From]++
		m.node(ev.From).downstream[ev.Plot]++
	case SeedRipenEvent:
		m.node(ev.Plot).succeeded++
	case SeedWitherEvent:
		m.node(ev.Plot).failed++
	case SeedPruneEvent:
		m.node(ev.Plot).skipped++
	case SeedReplantEvent:
		// 重试不改变完成数，指标侧只统计最终结果。
	case SealInputEvent, SealMergeEvent:
		// 终止信号不影响节点指标。
	}
	return nil
}

// pending 返回已输入但尚未得出结果的任务数。
func (p *nodeProbe) pending() int {
	input := p.external
	for _, count := range p.upstream {
		input += count
	}
	return input - (p.succeeded + p.failed + p.skipped)
}

// ==== Tests ====

func TestSyntheticLifecycleFeedsMetrics(t *testing.T) {
	now := time.Now()
	probe := newMetricsProbe()
	hub := NewHub(WithErrorHandler(func(err error) {
		t.Errorf("unexpected observer error: %v", err)
	}))
	if err := hub.AddObserver(probe); err != nil {
		t.Fatalf("AddObserver() = %v", err)
	}

	events := []Event{
		FarmStartEvent{
			Farm:        "farm",
			IsDAG:       true,
			Plots:       []string{"a", "b"},
			Edges:       map[string][]string{"a": {"b"}},
			SourcePlots: []string{"a"},
			Structure:   []string{"a -> b"},
			StartTime:   now,
		},
		PlotStartEvent{Plot: "a", Tenders: 2, StartTime: now},
		PlotStartEvent{Plot: "b", Tenders: 1, StartTime: now},
		SeedInputEvent{Plot: "a", Seed: 1, SeedRepr: "1", SeedID: 1, Time: now},
		SeedInputEvent{Plot: "a", Seed: 2, SeedRepr: "2", SeedID: 2, Time: now},
		SeedReplantEvent{Plot: "a", Seed: 1, SeedRepr: "1", Err: errors.New("retry"), Attempt: 1, SeedID: 1, Time: now},
		SeedRipenEvent{Plot: "a", Seed: 1, SeedRepr: "1", Fruit: 10, FruitRepr: "10", SeedID: 1, FruitID: 3, Time: now},
		SeedPruneEvent{Plot: "a", Seed: 2, SeedRepr: "2", SeedID: 2, PruneID: 4, Time: now},
		SeedInputEvent{Plot: "b", From: "a", Seed: 10, SeedRepr: "10", SeedID: 5, Parents: []int{3}, Time: now},
		SeedRipenEvent{Plot: "b", Seed: 10, SeedRepr: "10", Fruit: 20, FruitRepr: "20", SeedID: 5, FruitID: 6, Time: now},
		SeedInputEvent{Plot: "b", Seed: 11, SeedRepr: "11", SeedID: 7, Time: now},
		SeedWitherEvent{Plot: "b", Seed: 11, SeedRepr: "11", Err: errors.New("boom"), SeedID: 7, WeedID: 8, Time: now},
		SealInputEvent{Plot: "a", SealID: 9, Time: now},
		SealMergeEvent{Plot: "b", ParentIDs: []int{9}, SealID: 10, Time: now},
		PlotEndEvent{Plot: "a", EndTime: now, Elapsed: time.Second},
		PlotEndEvent{Plot: "b", EndTime: now, Elapsed: time.Second},
		FarmEndEvent{Farm: "farm", EndTime: now, Elapsed: time.Second},
	}
	for _, event := range events {
		if err := hub.OnEvent(event); err != nil {
			t.Fatalf("OnEvent(%T) = %v", event, err)
		}
	}

	if !probe.graphStarted || !probe.graphEnded {
		t.Fatalf("graph lifecycle not observed: start=%v end=%v", probe.graphStarted, probe.graphEnded)
	}

	a := probe.nodes["a"]
	if a == nil {
		t.Fatal("node a not observed")
	}
	if !a.started || !a.ended {
		t.Fatalf("node a lifecycle incomplete: started=%v ended=%v", a.started, a.ended)
	}
	if a.external != 2 {
		t.Fatalf("a.external = %d, want 2", a.external)
	}
	if a.succeeded != 1 || a.skipped != 1 || a.failed != 0 {
		t.Fatalf("a result counts = (%d, %d, %d), want (1, 0, 1)", a.succeeded, a.failed, a.skipped)
	}
	if a.downstream["b"] != 1 {
		t.Fatalf("a.downstream[b] = %d, want 1", a.downstream["b"])
	}
	if pending := a.pending(); pending != 0 {
		t.Fatalf("a.pending = %d, want 0", pending)
	}

	b := probe.nodes["b"]
	if b == nil {
		t.Fatal("node b not observed")
	}
	if b.external != 1 || b.upstream["a"] != 1 {
		t.Fatalf("b inputs = external %d upstream %v, want 1 and map[a:1]", b.external, b.upstream)
	}
	if b.succeeded != 1 || b.failed != 1 || b.skipped != 0 {
		t.Fatalf("b result counts = (%d, %d, %d), want (1, 1, 0)", b.succeeded, b.failed, b.skipped)
	}
	if pending := b.pending(); pending != 0 {
		t.Fatalf("b.pending = %d, want 0", pending)
	}
}

func TestEventsCarryPayloadAndIDs(t *testing.T) {
	seed := struct{ Name string }{Name: "seed"}
	boom := errors.New("boom")

	var got []Event
	hub := NewHub()
	if err := hub.AddObserver(ObserverFunc(func(event Event) error {
		got = append(got, event)
		return nil
	})); err != nil {
		t.Fatalf("AddObserver() = %v", err)
	}

	events := []Event{
		SeedRipenEvent{
			Plot:     "a",
			Seed:     seed,
			SeedRepr: "seed",
			Fruit:    "fruit",
			SeedID:   1,
			FruitID:  2,
			Elapsed:  time.Second,
			Time:     time.Unix(100, 0),
		},
		SeedWitherEvent{
			Plot:     "a",
			Seed:     seed,
			SeedRepr: "seed",
			Err:      boom,
			SeedID:   1,
			WeedID:   3,
			Elapsed:  time.Second,
			Time:     time.Unix(101, 0),
		},
		SeedReplantEvent{
			Plot:     "a",
			Seed:     seed,
			SeedRepr: "seed",
			Err:      boom,
			Attempt:  2,
			SeedID:   1,
			Time:     time.Unix(102, 0),
		},
		SeedInputEvent{
			Plot:     "b",
			From:     "a",
			Seed:     "fruit",
			SeedRepr: "fruit",
			SeedID:   4,
			Parents:  []int{2},
			Time:     time.Unix(103, 0),
		},
	}
	for _, event := range events {
		if err := hub.OnEvent(event); err != nil {
			t.Fatalf("OnEvent(%T) = %v", event, err)
		}
	}

	success, ok := got[0].(SeedRipenEvent)
	if !ok {
		t.Fatalf("got[0] = %T, want SeedRipenEvent", got[0])
	}
	if success.Seed != seed || success.Fruit != "fruit" {
		t.Fatalf("success payload = (%v, %v)", success.Seed, success.Fruit)
	}
	if success.SeedID == success.FruitID {
		t.Fatalf("success IDs must differ: seed=%d fruit=%d", success.SeedID, success.FruitID)
	}
	if !success.Time.Equal(time.Unix(100, 0)) {
		t.Fatalf("success time = %v", success.Time)
	}

	fail, ok := got[1].(SeedWitherEvent)
	if !ok {
		t.Fatalf("got[1] = %T, want SeedWitherEvent", got[1])
	}
	if !errors.Is(fail.Err, boom) {
		t.Fatalf("fail error = %v, want boom", fail.Err)
	}
	if fail.WeedID == fail.SeedID {
		t.Fatalf("fail IDs must differ: seed=%d weed=%d", fail.SeedID, fail.WeedID)
	}

	retry, ok := got[2].(SeedReplantEvent)
	if !ok {
		t.Fatalf("got[2] = %T, want SeedReplantEvent", got[2])
	}
	if retry.Attempt != 2 {
		t.Fatalf("retry attempt = %d, want 2", retry.Attempt)
	}
	if !errors.Is(retry.Err, boom) {
		t.Fatalf("retry error = %v, want boom", retry.Err)
	}

	input, ok := got[3].(SeedInputEvent)
	if !ok {
		t.Fatalf("got[3] = %T, want SeedInputEvent", got[3])
	}
	if input.Plot != "b" || input.From != "a" {
		t.Fatalf("input endpoints = (%q, %q), want (b, a)", input.Plot, input.From)
	}
	if len(input.Parents) != 1 || input.Parents[0] != 2 {
		t.Fatalf("input parents = %v, want [2]", input.Parents)
	}
}
