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
	case GraphStartEvent:
		m.graphStarted = true
	case GraphEndEvent:
		m.graphEnded = true
	case NodeStartEvent:
		probe := m.node(ev.Node)
		probe.started = true
		probe.start = ev.StartTime
	case NodeEndEvent:
		probe := m.node(ev.Node)
		probe.ended = true
		probe.end = ev.EndTime
	case TaskInputEvent:
		probe := m.node(ev.Node)
		if ev.From == "" {
			probe.external++
			break
		}
		probe.upstream[ev.From]++
		m.node(ev.From).downstream[ev.Node]++
	case TaskSuccessEvent:
		m.node(ev.Node).succeeded++
	case TaskFailEvent:
		m.node(ev.Node).failed++
	case TaskSkipEvent:
		m.node(ev.Node).skipped++
	case TaskRetryEvent:
		// 重试不改变完成数，指标侧只统计最终结果。
	case TerminationInputEvent, TerminationMergeEvent:
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
		GraphStartEvent{
			Farm:      "farm",
			IsDAG:     true,
			Nodes:     []string{"a", "b"},
			Edges:     map[string][]string{"a": {"b"}},
			Sources:   []string{"a"},
			Structure: []string{"a -> b"},
			StartTime: now,
		},
		NodeStartEvent{Node: "a", Tenders: 2, StartTime: now},
		NodeStartEvent{Node: "b", Tenders: 1, StartTime: now},
		TaskInputEvent{Node: "a", Seed: 1, SeedRepr: "1", SeedID: 1, Time: now},
		TaskInputEvent{Node: "a", Seed: 2, SeedRepr: "2", SeedID: 2, Time: now},
		TaskRetryEvent{Node: "a", Seed: 1, SeedRepr: "1", Err: errors.New("retry"), Attempt: 1, SeedID: 1, Time: now},
		TaskSuccessEvent{Node: "a", Seed: 1, SeedRepr: "1", Fruit: 10, FruitRepr: "10", SeedID: 1, FruitID: 3, Time: now},
		TaskSkipEvent{Node: "a", Seed: 2, SeedRepr: "2", SeedID: 2, PruneID: 4, Time: now},
		TaskInputEvent{Node: "b", From: "a", Seed: 10, SeedRepr: "10", SeedID: 5, Parents: []int{3}, Time: now},
		TaskSuccessEvent{Node: "b", Seed: 10, SeedRepr: "10", Fruit: 20, FruitRepr: "20", SeedID: 5, FruitID: 6, Time: now},
		TaskInputEvent{Node: "b", Seed: 11, SeedRepr: "11", SeedID: 7, Time: now},
		TaskFailEvent{Node: "b", Seed: 11, SeedRepr: "11", Err: errors.New("boom"), SeedID: 7, WeedID: 8, Time: now},
		TerminationInputEvent{Node: "a", SealID: 9, Time: now},
		TerminationMergeEvent{Node: "b", ParentIDs: []int{9}, SealID: 10, Time: now},
		NodeEndEvent{Node: "a", EndTime: now, Elapsed: time.Second},
		NodeEndEvent{Node: "b", EndTime: now, Elapsed: time.Second},
		GraphEndEvent{Farm: "farm", EndTime: now, Elapsed: time.Second},
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
		TaskSuccessEvent{
			Node:     "a",
			Seed:     seed,
			SeedRepr: "seed",
			Fruit:    "fruit",
			SeedID:   1,
			FruitID:  2,
			Elapsed:  time.Second,
			Time:     time.Unix(100, 0),
		},
		TaskFailEvent{
			Node:     "a",
			Seed:     seed,
			SeedRepr: "seed",
			Err:      boom,
			SeedID:   1,
			WeedID:   3,
			Elapsed:  time.Second,
			Time:     time.Unix(101, 0),
		},
		TaskRetryEvent{
			Node:     "a",
			Seed:     seed,
			SeedRepr: "seed",
			Err:      boom,
			Attempt:  2,
			SeedID:   1,
			Time:     time.Unix(102, 0),
		},
		TaskInputEvent{
			Node:     "b",
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

	success, ok := got[0].(TaskSuccessEvent)
	if !ok {
		t.Fatalf("got[0] = %T, want TaskSuccessEvent", got[0])
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

	fail, ok := got[1].(TaskFailEvent)
	if !ok {
		t.Fatalf("got[1] = %T, want TaskFailEvent", got[1])
	}
	if !errors.Is(fail.Err, boom) {
		t.Fatalf("fail error = %v, want boom", fail.Err)
	}
	if fail.WeedID == fail.SeedID {
		t.Fatalf("fail IDs must differ: seed=%d weed=%d", fail.SeedID, fail.WeedID)
	}

	retry, ok := got[2].(TaskRetryEvent)
	if !ok {
		t.Fatalf("got[2] = %T, want TaskRetryEvent", got[2])
	}
	if retry.Attempt != 2 {
		t.Fatalf("retry attempt = %d, want 2", retry.Attempt)
	}
	if !errors.Is(retry.Err, boom) {
		t.Fatalf("retry error = %v, want boom", retry.Err)
	}

	input, ok := got[3].(TaskInputEvent)
	if !ok {
		t.Fatalf("got[3] = %T, want TaskInputEvent", got[3])
	}
	if input.Node != "b" || input.From != "a" {
		t.Fatalf("input endpoints = (%q, %q), want (b, a)", input.Node, input.From)
	}
	if len(input.Parents) != 1 || input.Parents[0] != 2 {
		t.Fatalf("input parents = %v, want [2]", input.Parents)
	}
}
