package assembly_test

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/Mr-xiaotian/CelestialGrow/pkg/assembly"
	"github.com/Mr-xiaotian/CelestialGrow/pkg/metronome"
	"github.com/Mr-xiaotian/CelestialGrow/pkg/persist"
	"github.com/Mr-xiaotian/CelestialGrow/pkg/reporter"
	"github.com/Mr-xiaotian/CelestialGrow/pkg/scarecrow"
)

func TestStartLocalResourcesAssembleLifecycle(t *testing.T) {
	hub := scarecrow.NewHub(scarecrow.WithErrorHandler(func(err error) {
		t.Errorf("unexpected watcher error: %v", err)
	}))
	tickerHub, err := metronome.NewHub(metronome.WithErrorHandler(func(err error) {
		t.Errorf("unexpected ticker error: %v", err)
	}))
	if err != nil {
		t.Fatalf("NewHub() = %v", err)
	}

	stop, err := assembly.StartLocalResources(hub, tickerHub, "DEBUG", metronome.DefaultInterval)
	if err != nil {
		t.Fatalf("StartLocalResources() = %v", err)
	}

	now := time.Now()
	if err := hub.OnEvent(scarecrow.SeedInputEvent{
		Plot:     "node",
		Seed:     42,
		SeedRepr: "42",
		SeedID:   1,
		Time:     now,
	}); err != nil {
		t.Fatalf("OnEvent() = %v", err)
	}

	if err := stop(); err != nil {
		t.Fatalf("stop() = %v", err)
	}
	if err := stop(); err != nil {
		t.Fatalf("second stop() = %v", err)
	}

	lifecycle, ok := scarecrow.Find[*persist.LifecycleWatcher](hub)
	if !ok {
		t.Fatal("lifecycle watcher is not registered")
	}
	dbPath := lifecycle.DBPath()
	if dbPath == "" {
		t.Fatal("lifecycle database path is empty")
	}

	records, err := persist.LoadLifecycleStatusesFromPath(dbPath, "node")
	if err != nil {
		t.Fatalf("LoadLifecycleStatusesFromPath() = %v", err)
	}
	if len(records) != 1 {
		t.Fatalf("records = %d, want 1", len(records))
	}
	if records[0].Status != "seed" || records[0].SeedJSON != "42" {
		t.Fatalf("record = %#v, want seed/42", records[0])
	}
}

func TestStartReportResourcesAssembleReporter(t *testing.T) {
	paths := make(chan string, 16)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case paths <- r.URL.Path:
		default:
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	hub := scarecrow.NewHub(scarecrow.WithErrorHandler(func(err error) {
		t.Errorf("unexpected watcher error: %v", err)
	}))
	tickerHub, err := metronome.NewHub(metronome.WithErrorHandler(func(err error) {
		t.Errorf("unexpected ticker error: %v", err)
	}))
	if err != nil {
		t.Fatalf("NewHub() = %v", err)
	}

	stop, err := assembly.StartReportResources(
		hub,
		tickerHub,
		injectionTargetStub{},
		"INFO",
		metronome.DefaultInterval,
		&reporter.Config{URL: server.URL, SessionID: "farm@1"},
	)
	if err != nil {
		t.Fatalf("StartReportResources() = %v", err)
	}
	defer func() {
		if err := stop(); err != nil {
			t.Fatalf("stop() = %v", err)
		}
	}()

	if _, ok := scarecrow.Find[*reporter.PushWatcher](hub); !ok {
		t.Fatal("push watcher is not registered")
	}
	if _, ok := metronome.Find[*reporter.SnapshotTicker](tickerHub); !ok {
		t.Fatal("snapshot ticker is not registered")
	}
	if _, ok := metronome.Find[*reporter.InjectionTicker](tickerHub); !ok {
		t.Fatal("injection ticker is not registered")
	}

	if err := hub.OnEvent(scarecrow.FarmStartEvent{
		Farm:        "farm",
		Plots:       []string{"root"},
		Edges:       map[string][]string{"root": {}},
		SourcePlots: []string{"root"},
		StartTime:   time.Now(),
	}); err != nil {
		t.Fatalf("OnEvent(FarmStartEvent) = %v", err)
	}

	waitForPath(t, paths, "/api/push_graph_meta")
}

func TestStartLocalResourcesRejectNilHub(t *testing.T) {
	tickerHub, err := metronome.NewHub()
	if err != nil {
		t.Fatalf("NewHub() = %v", err)
	}
	if _, err := assembly.StartLocalResources(nil, tickerHub, "INFO", metronome.DefaultInterval); err == nil {
		t.Fatal("StartLocalResources(nil, tickerHub) = nil, want error")
	}
}

func TestStartLocalResourcesRejectNilTickerHub(t *testing.T) {
	watcherHub := scarecrow.NewHub()
	if _, err := assembly.StartLocalResources(watcherHub, nil, "INFO", metronome.DefaultInterval); err == nil {
		t.Fatal("StartLocalResources(watcherHub, nil) = nil, want error")
	}
}

func TestStartReportResourcesRejectInvalidArguments(t *testing.T) {
	watcherHub := scarecrow.NewHub()
	tickerHub, err := metronome.NewHub()
	if err != nil {
		t.Fatalf("NewHub() = %v", err)
	}

	if _, err := assembly.StartReportResources(
		watcherHub,
		tickerHub,
		injectionTargetStub{},
		"INFO",
		metronome.DefaultInterval,
		nil,
	); err == nil {
		t.Fatal("nil report config must be rejected")
	}

	if _, err := assembly.StartReportResources(
		watcherHub,
		tickerHub,
		nil,
		"INFO",
		metronome.DefaultInterval,
		&reporter.Config{URL: "http://example.com", SessionID: "farm@1"},
	); err == nil {
		t.Fatal("nil injection target must be rejected")
	}
}

func waitForPath(t *testing.T, paths <-chan string, want string) {
	t.Helper()

	timeout := time.After(3 * time.Second)
	for {
		select {
		case got := <-paths:
			if got == want {
				return
			}
		case <-timeout:
			t.Fatalf("did not receive %s", want)
		}
	}
}

type injectionTargetStub struct{}

func (injectionTargetStub) InjectTasks(map[string][]any) error { return nil }

func (injectionTargetStub) InjectTerminations([]string) error { return nil }
