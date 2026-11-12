package persist

import (
	"errors"
	"math"
	"sync"
	"testing"
	"time"

	"github.com/Mr-xiaotian/CelestialGrow/pkg/observer"
)

// captureLifecycleHandler 记录所有收到的生命周期记录，供观察者翻译测试使用。
type captureLifecycleHandler struct {
	mu      sync.Mutex
	records []LifecycleRecord
}

func (h *captureLifecycleHandler) BeforeStart() error { return nil }

func (h *captureLifecycleHandler) HandleRecord(record LifecycleRecord) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.records = append(h.records, record)
	return nil
}

func (h *captureLifecycleHandler) AfterStop() error { return nil }

func (h *captureLifecycleHandler) snapshot() []LifecycleRecord {
	h.mu.Lock()
	defer h.mu.Unlock()
	return append([]LifecycleRecord{}, h.records...)
}

func TestLifecycleObserverTranslatesEvents(t *testing.T) {
	handler := &captureLifecycleHandler{}
	obs := newLifecycleObserver(handler)
	if err := obs.Start(); err != nil {
		t.Fatalf("Start() = %v", err)
	}

	now := time.UnixMilli(1_700_000_000_123)
	events := []observer.Event{
		observer.SeedInputEvent{Plot: "a", Seed: 1, SeedID: 1, Time: now},
		observer.SeedRipenEvent{Plot: "a", Seed: 1, Fruit: 2, SeedID: 1, FruitID: 2, Time: now},
		observer.SeedInputEvent{Plot: "b", From: "a", Seed: 2, SeedID: 3, Parents: []int{2}, Time: now},
		observer.SeedWitherEvent{Plot: "b", Seed: 2, Err: errors.New("boom"), SeedID: 3, WeedID: 4, Time: now},
		observer.SeedPruneEvent{Plot: "b", Seed: 9, SeedID: 5, PruneID: 6, Time: now},
		observer.PlotStartEvent{Plot: "b", StartTime: now},
		observer.SealMergeEvent{Plot: "b", ParentIDs: []int{7}, SealID: 8, Time: now},
	}
	for _, event := range events {
		if err := obs.OnEvent(event); err != nil {
			t.Fatalf("OnEvent(%T) = %v", event, err)
		}
	}

	if err := obs.Stop(); err != nil {
		t.Fatalf("Stop() = %v", err)
	}

	records := handler.snapshot()
	if len(records) != 5 {
		t.Fatalf("records = %d, want 5: %#v", len(records), records)
	}

	seed := records[0]
	if seed.Kind != lifecycleSeed || seed.InputEventID != 1 || seed.CurrentEventID != 1 {
		t.Fatalf("seed record = %#v", seed)
	}
	if seed.PlotName != "a" || seed.SeedJSON != "1" || seed.ParentIDs != nil {
		t.Fatalf("seed record = %#v", seed)
	}
	if math.Abs(seed.TS-1700000000.123) > 1e-6 {
		t.Fatalf("seed TS = %v, want 1700000000.123", seed.TS)
	}

	ripen := records[1]
	if ripen.Kind != lifecycleRipen || ripen.InputEventID != 1 || ripen.CurrentEventID != 2 {
		t.Fatalf("ripen record = %#v", ripen)
	}
	if ripen.FruitJSON != "2" || len(ripen.ParentIDs) != 1 || ripen.ParentIDs[0] != 1 {
		t.Fatalf("ripen record = %#v", ripen)
	}

	upstream := records[2]
	if upstream.Kind != lifecycleSeed || upstream.PlotName != "b" || upstream.InputEventID != 3 {
		t.Fatalf("upstream record = %#v", upstream)
	}
	if upstream.SeedJSON != "2" || len(upstream.ParentIDs) != 1 || upstream.ParentIDs[0] != 2 {
		t.Fatalf("upstream record = %#v", upstream)
	}

	wither := records[3]
	if wither.Kind != lifecycleWither || wither.CurrentEventID != 4 {
		t.Fatalf("wither record = %#v", wither)
	}
	if wither.WitherMessage != "boom" || wither.WitherType == "" {
		t.Fatalf("wither record = %#v", wither)
	}

	prune := records[4]
	if prune.Kind != lifecyclePrune || prune.CurrentEventID != 6 {
		t.Fatalf("prune record = %#v", prune)
	}
}
