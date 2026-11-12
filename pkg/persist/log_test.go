package persist

import (
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Mr-xiaotian/CelestialGrow/pkg/observer"
)

// captureLogHandler 记录所有收到的日志记录，供观察者翻译测试使用。
type captureLogHandler struct {
	mu      sync.Mutex
	records []LogRecord
}

func (h *captureLogHandler) BeforeStart() error { return nil }

func (h *captureLogHandler) HandleRecord(record LogRecord) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.records = append(h.records, record)
	return nil
}

func (h *captureLogHandler) AfterStop() error { return nil }

func (h *captureLogHandler) snapshot() []LogRecord {
	h.mu.Lock()
	defer h.mu.Unlock()
	return append([]LogRecord{}, h.records...)
}

func TestLogObserverTranslatesEvents(t *testing.T) {
	handler := &captureLogHandler{}
	metrics := observer.NewMetricsObserver()
	obs := newLogObserver(handler, metrics, "DEBUG")
	if err := obs.Start(); err != nil {
		t.Fatalf("Start() = %v", err)
	}

	now := time.Now()
	events := []observer.Event{
		observer.GraphStartEvent{Farm: "farm", Structure: []string{"a -> b"}, StartTime: now},
		observer.NodeStartEvent{Node: "a", Tenders: 2, StartTime: now},
		observer.TaskInputEvent{Node: "a", Seed: 1, SeedRepr: "1", SeedID: 1, Time: now},
		observer.TaskRetryEvent{Node: "a", Seed: 1, SeedRepr: "1", Err: errors.New("transient"), Attempt: 1, SeedID: 1, Time: now},
		observer.TaskSuccessEvent{Node: "a", Seed: 1, SeedRepr: "1", Fruit: 10, FruitRepr: "10", SeedID: 1, FruitID: 2, Elapsed: time.Second, Time: now},
		observer.TaskFailEvent{Node: "a", Seed: 2, SeedRepr: "2", Err: errors.New("boom"), SeedID: 3, WeedID: 4, Elapsed: time.Second, Time: now},
		observer.TaskSkipEvent{Node: "a", Seed: 3, SeedRepr: "3", SeedID: 5, PruneID: 6, Time: now},
		observer.NodeEndEvent{Node: "a", EndTime: now, Elapsed: 2 * time.Second},
		observer.GraphEndEvent{Farm: "farm", EndTime: now, Elapsed: 3 * time.Second},
	}
	for _, event := range events {
		if err := metrics.OnEvent(event); err != nil {
			t.Fatalf("metrics.OnEvent(%T) = %v", event, err)
		}
		if err := obs.OnEvent(event); err != nil {
			t.Fatalf("OnEvent(%T) = %v", event, err)
		}
	}

	if err := obs.Stop(); err != nil {
		t.Fatalf("Stop() = %v", err)
	}

	records := handler.snapshot()
	want := []struct {
		level   string
		contain string
	}{
		{"INFO", "Farm 'farm' start. Graph structure:"},
		{"INFO", "a -> b"},
		{"INFO", "Plot 'a' start with 2 tenders."},
		{"DEBUG", "In 'a', Seed 1 input. [1*]"},
		{"WARNING", "attempt 1 withered: transient. Replanting. [1*]"},
		{"SUCCESS", "Seed 1 ripened. Fruit is 10. Use 1.00s. [1->2*]"},
		{"ERROR", "Seed 2 withered: boom. Use 1.00s. [3->4*]"},
		{"INFO", "Seed 3 pruned. Use 0.00s. [5->6*]"},
		{"INFO", "Plot 'a' end. Use 2.00s. 1 ripened, 1 withered, 1 pruned."},
		{"INFO", "Farm 'farm' end. Use 3.00s."},
	}
	if len(records) != len(want) {
		t.Fatalf("records = %d, want %d: %#v", len(records), len(want), records)
	}
	for i, expected := range want {
		if records[i].Level != expected.level {
			t.Fatalf("records[%d].Level = %q, want %q", i, records[i].Level, expected.level)
		}
		if !strings.Contains(records[i].Message, expected.contain) {
			t.Fatalf("records[%d].Message = %q, want to contain %q", i, records[i].Message, expected.contain)
		}
	}
}

func TestLogObserverFiltersLevels(t *testing.T) {
	handler := &captureLogHandler{}
	obs := newLogObserver(handler, observer.NewMetricsObserver(), "ERROR")
	if err := obs.Start(); err != nil {
		t.Fatalf("Start() = %v", err)
	}

	now := time.Now()
	if err := obs.OnEvent(observer.TaskInputEvent{Node: "a", Seed: 1, SeedID: 1, Time: now}); err != nil {
		t.Fatalf("OnEvent(input) = %v", err)
	}
	if err := obs.OnEvent(observer.TaskFailEvent{Node: "a", Seed: 1, SeedID: 1, WeedID: 2, Time: now}); err != nil {
		t.Fatalf("OnEvent(fail) = %v", err)
	}

	if err := obs.Stop(); err != nil {
		t.Fatalf("Stop() = %v", err)
	}

	records := handler.snapshot()
	if len(records) != 1 {
		t.Fatalf("records = %d, want 1: %#v", len(records), records)
	}
	if records[0].Level != "ERROR" {
		t.Fatalf("records[0].Level = %q, want ERROR", records[0].Level)
	}
}
