package farm_test

import (
	"sync"
	"testing"

	"github.com/Mr-xiaotian/CelestialGrow/pkg/farm"
	"github.com/Mr-xiaotian/CelestialGrow/pkg/plot"
	"github.com/Mr-xiaotian/CelestialGrow/pkg/scarecrow"
)

// TestFarmStartWithInjection 验证 Start 不注入初始任务，任务与终止信号
// 可通过 InjectTasks / InjectTerminations 在运行期注入；同时验证 Farm 级 watcher
// 能收到 Farm 与 Plot 事件。
func TestFarmStartWithInjection(t *testing.T) {
	var (
		mu          sync.Mutex
		seeds       []int
		farmStarted bool
		plotStarts  int
	)

	root := plot.NewSinglePlot("root", func(seed int) (int, error) {
		mu.Lock()
		seeds = append(seeds, seed)
		mu.Unlock()
		return seed, nil
	}, plot.WithTenders(2), plot.WithChanSize(8))

	f := farm.NewFarm("start_injection")
	if err := f.AddPlot(root); err != nil {
		t.Fatalf("AddPlot() error = %v", err)
	}
	if err := f.AddWatcher(scarecrow.WatcherFunc(func(event scarecrow.Event) error {
		mu.Lock()
		defer mu.Unlock()
		switch event.(type) {
		case scarecrow.FarmStartEvent:
			farmStarted = true
		case scarecrow.PlotStartEvent:
			plotStarts++
		}
		return nil
	})); err != nil {
		t.Fatalf("AddWatcher() error = %v", err)
	}

	errCh := make(chan error, 1)
	go func() {
		errCh <- f.Start("INFO")
	}()

	if err := f.InjectTasks(map[string][]any{"root": {1, 2, 3}}); err != nil {
		t.Fatalf("InjectTasks() error = %v", err)
	}
	if err := f.InjectTerminations([]string{"root"}); err != nil {
		t.Fatalf("InjectTerminations() error = %v", err)
	}
	if err := <-errCh; err != nil {
		t.Fatalf("Start() error = %v", err)
	}

	mu.Lock()
	defer mu.Unlock()
	if !farmStarted {
		t.Fatal("FarmStartEvent not observed")
	}
	if plotStarts != 1 {
		t.Fatalf("PlotStartEvent count = %d, want 1", plotStarts)
	}
	if len(seeds) != 3 {
		t.Fatalf("processed seeds = %v, want 3 items", seeds)
	}
}
