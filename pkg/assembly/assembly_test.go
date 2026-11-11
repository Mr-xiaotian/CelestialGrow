package assembly_test

import (
	"errors"
	"testing"
	"time"

	"github.com/Mr-xiaotian/CelestialGrow/pkg/assembly"
	"github.com/Mr-xiaotian/CelestialGrow/pkg/observer"
	"github.com/Mr-xiaotian/CelestialGrow/pkg/persist"
)

// runFunc 描述一个运行期资源装配入口。
type runFunc func(hub *observer.Hub, logLevel string, fn func(lifecycleDBPath string) error) error

// runners 覆盖节点级与图级两个入口，确保两者行为一致。
var runners = []struct {
	name string
	run  runFunc
}{
	{"RunNodeResources", assembly.RunNodeResources},
	{"RunFarmResources", assembly.RunFarmResources},
}

func TestRunResourcesAssembleLifecycle(t *testing.T) {
	for _, runner := range runners {
		t.Run(runner.name, func(t *testing.T) {
			hub := observer.NewHub(observer.WithErrorHandler(func(err error) {
				t.Errorf("unexpected observer error: %v", err)
			}))

			now := time.Now()
			var dbPath string
			err := runner.run(hub, "DEBUG", func(lifecycleDBPath string) error {
				dbPath = lifecycleDBPath
				if lifecycleDBPath == "" {
					t.Error("lifecycle database path is empty")
				}
				return hub.OnEvent(observer.TaskInputEvent{
					Node:     "node",
					Seed:     42,
					SeedRepr: "42",
					SeedID:   1,
					Time:     now,
				})
			})
			if err != nil {
				t.Fatalf("%s() = %v", runner.name, err)
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

func TestRunResourcesReturnCallbackError(t *testing.T) {
	boom := errors.New("boom")

	for _, runner := range runners {
		t.Run(runner.name, func(t *testing.T) {
			err := runner.run(observer.NewHub(), "INFO", func(string) error {
				return boom
			})
			if !errors.Is(err, boom) {
				t.Fatalf("%s() = %v, want boom", runner.name, err)
			}
		})
	}
}

func TestRunResourcesRejectNilHub(t *testing.T) {
	for _, runner := range runners {
		t.Run(runner.name, func(t *testing.T) {
			if err := runner.run(nil, "INFO", nil); err == nil {
				t.Fatalf("%s(nil) = nil, want error", runner.name)
			}
		})
	}
}

func TestRunResourcesAcceptNilCallback(t *testing.T) {
	for _, runner := range runners {
		t.Run(runner.name, func(t *testing.T) {
			if err := runner.run(observer.NewHub(), "INFO", nil); err != nil {
				t.Fatalf("%s() = %v", runner.name, err)
			}
		})
	}
}
