package reporter

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"
	"time"

	"github.com/Mr-xiaotian/CelestialGrow/pkg/metronome"
)

func TestInjectionTickerPullsTasksAndTerminations(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.URL.Query().Get("session_id"); got != "demo@1" {
			t.Fatalf("session_id = %q, want demo@1", got)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"tasks": map[string][]any{
				"root": {1.0, "x"},
			},
			"terminations": []string{"leaf"},
		})
	}))
	defer server.Close()

	target := &injectionTargetProbe{}
	ticker, err := NewInjectionTicker(server.URL, "demo@1", target, 5, time.Second)
	if err != nil {
		t.Fatalf("NewInjectionTicker() = %v", err)
	}
	if got := ticker.TickPeriod(); got != 5 {
		t.Fatalf("TickPeriod() = %d, want 5", got)
	}
	if err := ticker.Start(); err != nil {
		t.Fatalf("Start() = %v", err)
	}
	defer func() {
		if err := ticker.Stop(); err != nil {
			t.Fatalf("Stop() = %v", err)
		}
	}()

	if err := ticker.OnTick(metronome.TickEvent{Seq: 1}); err != nil {
		t.Fatalf("OnTick() = %v", err)
	}
	if !reflect.DeepEqual(target.tasks, map[string][]any{"root": {1.0, "x"}}) {
		t.Fatalf("tasks = %#v", target.tasks)
	}
	if !reflect.DeepEqual(target.terminations, []string{"leaf"}) {
		t.Fatalf("terminations = %#v", target.terminations)
	}
}

func TestInjectionTickerRejectsUseBeforeStart(t *testing.T) {
	ticker, err := NewInjectionTicker("http://example.com", "demo@1", &injectionTargetProbe{}, 1, time.Second)
	if err != nil {
		t.Fatalf("NewInjectionTicker() = %v", err)
	}
	if err := ticker.OnTick(metronome.TickEvent{Seq: 1}); err == nil {
		t.Fatal("OnTick() before Start must fail")
	}
}

func TestNewInjectionTickerRejectsInvalidArguments(t *testing.T) {
	if _, err := NewInjectionTicker("", "demo@1", &injectionTargetProbe{}, 1, time.Second); err == nil {
		t.Fatal("empty base url must be rejected")
	}
	if _, err := NewInjectionTicker("http://example.com", "", &injectionTargetProbe{}, 1, time.Second); err == nil {
		t.Fatal("empty session id must be rejected")
	}
	if _, err := NewInjectionTicker("http://example.com", "demo@1", nil, 1, time.Second); err == nil {
		t.Fatal("nil target must be rejected")
	}
	if _, err := NewInjectionTicker("127.0.0.1:5005", "demo@1", &injectionTargetProbe{}, 1, time.Second); err == nil {
		t.Fatal("base url without scheme must be rejected")
	}
	if _, err := NewInjectionTicker("ftp://example.com", "demo@1", &injectionTargetProbe{}, 1, time.Second); err == nil {
		t.Fatal("non-http base url must be rejected")
	}
}

type injectionTargetProbe struct {
	tasks        map[string][]any
	terminations []string
}

func (p *injectionTargetProbe) InjectTasks(tasks map[string][]any) error {
	p.tasks = tasks
	return nil
}

func (p *injectionTargetProbe) InjectTerminations(nodes []string) error {
	p.terminations = append([]string(nil), nodes...)
	return nil
}
