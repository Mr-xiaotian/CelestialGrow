package assembly_test

import (
	"testing"
	"time"

	"github.com/Mr-xiaotian/CelestialGrow/pkg/assembly"
	"github.com/Mr-xiaotian/CelestialGrow/pkg/metronome"
	"github.com/Mr-xiaotian/CelestialGrow/pkg/persist"
	"github.com/Mr-xiaotian/CelestialGrow/pkg/scarecrow"
)

// startFunc 描述一个运行期资源装配入口。
type startFunc func(
	watcherHub *scarecrow.Hub,
	tickerHub *metronome.Hub,
	logLevel string,
	interval time.Duration,
) (assembly.StopFunc, error)

// starters 覆盖 Plot 级与 Farm 级两个入口，确保两者行为一致。
var starters = []struct {
	name  string
	start startFunc
}{
	{"StartLocalResources", assembly.StartLocalResources},
	{"StartReportResources", assembly.StartReportResources},
}

func TestStartResourcesAssembleLifecycle(t *testing.T) {
	for _, starter := range starters {
		t.Run(starter.name, func(t *testing.T) {
			hub := scarecrow.NewHub(scarecrow.WithErrorHandler(func(err error) {
				t.Errorf("unexpected watcher error: %v", err)
			}))
			tickerHub, err := metronome.NewHub(metronome.WithErrorHandler(func(err error) {
				t.Errorf("unexpected ticker error: %v", err)
			}))
			if err != nil {
				t.Fatalf("NewHub() = %v", err)
			}

			stop, err := starter.start(hub, tickerHub, "DEBUG", metronome.DefaultInterval)
			if err != nil {
				t.Fatalf("%s() = %v", starter.name, err)
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
		})
	}
}

func TestStartResourcesRejectNilHub(t *testing.T) {
	for _, starter := range starters {
		t.Run(starter.name, func(t *testing.T) {
			tickerHub, err := metronome.NewHub()
			if err != nil {
				t.Fatalf("NewHub() = %v", err)
			}
			if _, err := starter.start(nil, tickerHub, "INFO", metronome.DefaultInterval); err == nil {
				t.Fatalf("%s(nil, tickerHub) = nil, want error", starter.name)
			}
		})
	}
}

func TestStartResourcesRejectNilTickerHub(t *testing.T) {
	for _, starter := range starters {
		t.Run(starter.name, func(t *testing.T) {
			watcherHub := scarecrow.NewHub()
			if _, err := starter.start(watcherHub, nil, "INFO", metronome.DefaultInterval); err == nil {
				t.Fatalf("%s(watcherHub, nil) = nil, want error", starter.name)
			}
		})
	}
}
