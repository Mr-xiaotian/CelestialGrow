package reporter

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"
	"time"

	"github.com/Mr-xiaotian/CelestialGrow/pkg/scarecrow"
)

func TestPushWatcherPushesFarmStartSnapshotAndShutdown(t *testing.T) {
	var requests []capturedRequest
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Fatalf("ReadAll() = %v", err)
		}
		_ = r.Body.Close()
		requests = append(requests, capturedRequest{
			path: r.URL.Path,
			body: append([]byte(nil), body...),
		})
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	metricsView := stubMetricsView{
		plots: map[string]scarecrow.PlotMetrics{
			"root": {
				Plot:             "root",
				Status:           scarecrow.PlotRunning,
				StartTime:        time.Unix(5, 0),
				InputTotal:       3,
				Processed:        2,
				Pending:          1,
				Succeeded:        2,
				UpstreamCounts:   map[string]int{},
				DownstreamCounts: map[string]int{"leaf": 2},
			},
		},
	}

	watcher, err := NewPushWatcher("farm@1", server.URL, metricsView)
	if err != nil {
		t.Fatalf("NewPushWatcher() = %v", err)
	}
	if err := watcher.Start(); err != nil {
		t.Fatalf("Start() = %v", err)
	}

	start := time.Unix(10, 0)
	end := time.Unix(20, 0)
	if err := watcher.OnEvent(scarecrow.FarmStartEvent{
		Farm:        "demo",
		IsDAG:       true,
		Plots:       []string{"root"},
		Edges:       map[string][]string{"root": {"leaf"}},
		SourcePlots: []string{"root"},
		NodeMeta: map[string]scarecrow.NodeMeta{
			"root": {ClassName: "SinglePlot", MaxWorkers: 2},
		},
		StartTime: start,
	}); err != nil {
		t.Fatalf("OnEvent(FarmStartEvent) = %v", err)
	}
	if err := watcher.PushSnapshot(map[string]map[string]any{
		"root": {"status": 1},
	}); err != nil {
		t.Fatalf("PushSnapshot() = %v", err)
	}
	if err := watcher.OnEvent(scarecrow.SeedWitherEvent{
		Plot:   "leaf",
		Seed:   map[string]any{"id": 1},
		Err:    errors.New("boom"),
		SeedID: 7,
		WeedID: 8,
		Time:   time.Unix(15, 0),
	}); err != nil {
		t.Fatalf("OnEvent(SeedWitherEvent) = %v", err)
	}
	if err := watcher.OnEvent(scarecrow.FarmEndEvent{
		Farm:    "demo",
		EndTime: end,
	}); err != nil {
		t.Fatalf("OnEvent(FarmEndEvent) = %v", err)
	}
	if err := watcher.Stop(); err != nil {
		t.Fatalf("Stop() = %v", err)
	}

	if got, want := requestPaths(requests), []string{
		"/api/push_graph_meta",
		"/api/push_snapshot",
		"/api/push_error",
		"/api/push_snapshot",
		"/api/shutdown_session",
	}; !reflect.DeepEqual(got, want) {
		t.Fatalf("paths = %v, want %v", got, want)
	}

	var meta map[string]any
	if err := json.Unmarshal(requests[0].body, &meta); err != nil {
		t.Fatalf("Unmarshal(meta) = %v", err)
	}
	if meta["session_id"] != "farm@1" || meta["graph"] != "demo" {
		t.Fatalf("graph meta payload = %#v", meta)
	}
	nodeMeta, ok := meta["node_meta"].(map[string]any)
	if !ok || nodeMeta["root"] == nil {
		t.Fatalf("graph meta node_meta = %#v", meta["node_meta"])
	}

	var failure map[string]any
	if err := json.Unmarshal(requests[2].body, &failure); err != nil {
		t.Fatalf("Unmarshal(failure) = %v", err)
	}
	if failure["session_id"] != "farm@1" ||
		failure["event_id"] != float64(8) ||
		failure["node"] != "leaf" ||
		failure["error_type"] != "*errors.errorString" ||
		failure["error_message"] != "boom" {
		t.Fatalf("error payload = %#v", failure)
	}

	var snapshot map[string]any
	if err := json.Unmarshal(requests[3].body, &snapshot); err != nil {
		t.Fatalf("Unmarshal(snapshot) = %v", err)
	}
	if snapshot["session_id"] != "farm@1" {
		t.Fatalf("snapshot payload = %#v", snapshot)
	}

	var shutdown map[string]any
	if err := json.Unmarshal(requests[4].body, &shutdown); err != nil {
		t.Fatalf("Unmarshal(shutdown) = %v", err)
	}
	if shutdown["session_id"] != "farm@1" {
		t.Fatalf("shutdown payload = %#v", shutdown)
	}
}

func TestToJSONValueFallsBackToReadableText(t *testing.T) {
	value := make(chan int)
	got, ok := toJSONValue(value).(string)
	if !ok || got == "" {
		t.Fatalf("toJSONValue(chan) = %#v, want non-empty string", got)
	}
}

func TestNewPushWatcherRejectsInvalidArguments(t *testing.T) {
	metricsView := stubMetricsView{}
	if _, err := NewPushWatcher("", "http://example.com", metricsView); err == nil {
		t.Fatal("empty session id must be rejected")
	}
	if _, err := NewPushWatcher("demo@1", "", metricsView); err == nil {
		t.Fatal("empty base url must be rejected")
	}
	if _, err := NewPushWatcher("demo@1", "http://example.com", nil); err == nil {
		t.Fatal("nil metrics view must be rejected")
	}
}

type capturedRequest struct {
	path string
	body []byte
}

func requestPaths(requests []capturedRequest) []string {
	paths := make([]string, 0, len(requests))
	for _, item := range requests {
		paths = append(paths, item.path)
	}
	return paths
}
