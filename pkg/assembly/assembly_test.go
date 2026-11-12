package assembly_test

import (
	"testing"
	"time"

	"github.com/Mr-xiaotian/CelestialGrow/pkg/assembly"
	"github.com/Mr-xiaotian/CelestialGrow/pkg/persist"
	"github.com/Mr-xiaotian/CelestialGrow/pkg/scarecrow"
)

// startFunc 描述一个运行期资源装配入口。
type startFunc func(hub *scarecrow.Hub, logLevel string) (*assembly.Resources, error)

// starters 覆盖 Plot 级与 Farm 级两个入口，确保两者行为一致。
var starters = []struct {
	name  string
	start startFunc
}{
	{"StartPlotResources", assembly.StartPlotResources},
	{"StartFarmResources", assembly.StartFarmResources},
}

func TestStartResourcesAssembleLifecycle(t *testing.T) {
	for _, starter := range starters {
		t.Run(starter.name, func(t *testing.T) {
			hub := scarecrow.NewHub(scarecrow.WithErrorHandler(func(err error) {
				t.Errorf("unexpected watcher error: %v", err)
			}))

			resources, err := starter.start(hub, "DEBUG")
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

			resources.Stop()
			resources.Stop() // 幂等

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
			resources, err := starter.start(nil, "INFO")
			if err == nil {
				t.Fatalf("%s(nil) = nil, want error", starter.name)
			}
			if resources != nil {
				t.Fatalf("%s(nil) resources = %#v, want nil", starter.name, resources)
			}
		})
	}
}
