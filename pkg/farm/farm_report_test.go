package farm_test

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/Mr-xiaotian/CelestialGrow/pkg/farm"
	"github.com/Mr-xiaotian/CelestialGrow/pkg/plot"
	"github.com/Mr-xiaotian/CelestialGrow/pkg/reporter"
)

func TestFarmRunWithReportPushesGraphErrorSnapshotAndShutdown(t *testing.T) {
	var (
		mu       sync.Mutex
		requests []capturedReport
	)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Errorf("ReadAll() = %v", err)
		}
		_ = r.Body.Close()

		mu.Lock()
		requests = append(requests, capturedReport{
			path: r.URL.Path,
			body: append([]byte(nil), body...),
		})
		mu.Unlock()

		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	root := plot.NewSinglePlot("root", func(seed int) (int, error) {
		if seed == 2 {
			return 0, errors.New("boom")
		}
		return seed * 2, nil
	}, plot.WithTenders(2))

	f := farm.NewFarm("report_farm")
	if err := f.AddPlot(root); err != nil {
		t.Fatalf("AddPlot() error = %v", err)
	}

	if err := f.Run(map[string][]any{
		"root": {1, 2, 3},
	}, "INFO", &reporter.Config{
		URL:       server.URL,
		SessionID: "farm@1",
	}); err != nil {
		t.Fatalf("Run() error = %v", err)
	}

	mu.Lock()
	got := append([]capturedReport(nil), requests...)
	mu.Unlock()

	paths := make([]string, 0, len(got))
	for _, item := range got {
		paths = append(paths, item.path)
	}
	for _, want := range []string{
		"/api/push_graph_meta",
		"/api/push_error",
		"/api/push_snapshot",
		"/api/shutdown_session",
	} {
		if !slices.Contains(paths, want) {
			t.Fatalf("paths = %v, want %s", paths, want)
		}
	}

	var meta map[string]any
	if err := json.Unmarshal(findReport(t, got, "/api/push_graph_meta").body, &meta); err != nil {
		t.Fatalf("Unmarshal(meta) = %v", err)
	}
	if meta["graph"] != "report_farm" || meta["session_id"] != "farm@1" {
		t.Fatalf("graph meta payload = %#v", meta)
	}
	nodeMeta, ok := meta["node_meta"].(map[string]any)
	if !ok {
		t.Fatalf("node_meta = %#v", meta["node_meta"])
	}
	rootMeta, ok := nodeMeta["root"].(map[string]any)
	if !ok || rootMeta["class_name"] != "SinglePlot" || rootMeta["max_workers"] != float64(2) {
		t.Fatalf("root node meta = %#v", nodeMeta["root"])
	}

	var failure map[string]any
	if err := json.Unmarshal(findReport(t, got, "/api/push_error").body, &failure); err != nil {
		t.Fatalf("Unmarshal(failure) = %v", err)
	}
	if failure["node"] != "root" || failure["error_message"] != "boom" {
		t.Fatalf("error payload = %#v", failure)
	}

	var shutdown map[string]any
	if err := json.Unmarshal(findReport(t, got, "/api/shutdown_session").body, &shutdown); err != nil {
		t.Fatalf("Unmarshal(shutdown) = %v", err)
	}
	if shutdown["session_id"] != "farm@1" {
		t.Fatalf("shutdown payload = %#v", shutdown)
	}
}

func findReport(t *testing.T, requests []capturedReport, path string) capturedReport {
	t.Helper()

	for _, item := range requests {
		if item.path == path {
			return item
		}
	}
	t.Fatalf("request %s is missing", path)
	return capturedReport{}
}

func TestFarmRunGeneratesSessionID(t *testing.T) {
	var (
		mu       sync.Mutex
		requests []capturedReport
	)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Errorf("ReadAll() = %v", err)
		}
		_ = r.Body.Close()

		mu.Lock()
		requests = append(requests, capturedReport{
			path: r.URL.Path,
			body: append([]byte(nil), body...),
		})
		mu.Unlock()
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	f := farm.NewFarm("auto_session")
	if err := f.AddPlot(plot.NewSinglePlot("root", func(seed int) (int, error) {
		return seed, nil
	})); err != nil {
		t.Fatalf("AddPlot() error = %v", err)
	}

	if err := f.Run(map[string][]any{"root": {1}}, "INFO", &reporter.Config{URL: server.URL}); err != nil {
		t.Fatalf("Run() error = %v", err)
	}

	mu.Lock()
	got := append([]capturedReport(nil), requests...)
	mu.Unlock()

	for _, item := range got {
		if item.path != "/api/push_graph_meta" {
			continue
		}

		var meta map[string]any
		if err := json.Unmarshal(item.body, &meta); err != nil {
			t.Fatalf("Unmarshal(meta) = %v", err)
		}
		sessionID, _ := meta["session_id"].(string)
		if !strings.HasPrefix(sessionID, "auto_session@") {
			t.Fatalf("session_id = %q, want auto_session@ prefix", sessionID)
		}
		return
	}

	t.Fatalf("requests = %v, want graph meta", got)
}

type capturedReport struct {
	path string
	body []byte
}
