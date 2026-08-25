package farm_test

import (
	"sort"
	"sync"
	"testing"

	"github.com/Mr-xiaotian/CelestialGrow/pkg/farm"
	"github.com/Mr-xiaotian/CelestialGrow/pkg/plot"
)

func TestFarmRunLinear(t *testing.T) {
	root := plot.NewPlot("root", func(seed int) (int, error) { return seed * 2, nil }, plot.WithTends(2))

	var (
		mu      sync.Mutex
		results []int
	)
	head := plot.NewPlot("head", func(seed int) (int, error) {
		mu.Lock()
		results = append(results, seed)
		mu.Unlock()
		return seed, nil
	}, plot.WithTends(2))

	farm := farm.NewFarm("start_linear", "INFO")
	if err := farm.AddPlot(root, head); err != nil {
		t.Fatalf("AddPlot() error = %v", err)
	}
	if err := farm.Connect([]plot.PlotNode{root}, []plot.PlotNode{head}); err != nil {
		t.Fatalf("Connect() error = %v", err)
	}

	if err := farm.Run(map[string][]any{
		"root": {1, 2, 3},
	}); err != nil {
		t.Fatalf("Run() error = %v", err)
	}

	sort.Ints(results)
	want := []int{2, 4, 6}
	if len(results) != len(want) {
		t.Fatalf("len(results) = %d, want %d", len(results), len(want))
	}
	for i := range want {
		if results[i] != want[i] {
			t.Fatalf("results[%d] = %d, want %d", i, results[i], want[i])
		}
	}
	if int(root.GetState()) != 2 {
		t.Fatalf("root state = %d, want 2", root.GetState())
	}
	if int(head.GetState()) != 2 {
		t.Fatalf("head state = %d, want 2", head.GetState())
	}
}
