package farm_test

import (
	"fmt"
	"sort"
	"sync"
	"testing"

	"github.com/Mr-xiaotian/CelestialGrow/pkg/farm"
	"github.com/Mr-xiaotian/CelestialGrow/pkg/plot"
)

// RoutePlot 可以根据路由表将不同路由表项定向转发到指定下游。
func TestFarmRunRoutePlot(t *testing.T) {
	var mu sync.Mutex
	received := map[string][]string{}

	recorder := func(name string) func(string) (string, error) {
		return func(seed string) (string, error) {
			mu.Lock()
			received[name] = append(received[name], seed)
			mu.Unlock()
			return seed, nil
		}
	}

	left := plot.NewPlot("left", recorder("left"), plot.WithTenders(2))
	right := plot.NewPlot("right", recorder("right"), plot.WithTenders(2))
	route := plot.NewRoutePlot("route", func(seed int) (map[string]string, error) {
		if seed%2 == 0 {
			return map[string]string{"left": fmt.Sprintf("even-%d", seed)}, nil
		}
		return map[string]string{"right": fmt.Sprintf("odd-%d", seed)}, nil
	}, plot.WithTenders(2))

	f := farm.NewFarm("route_plot", "INFO")
	if err := f.AddPlot(route, left, right); err != nil {
		t.Fatalf("AddPlot() error = %v", err)
	}
	if err := f.Connect([]plot.PlotNode{route}, []plot.PlotNode{left, right}); err != nil {
		t.Fatalf("Connect() error = %v", err)
	}
	if err := f.Run(map[string][]any{
		"route": {1, 2, 3, 4},
	}); err != nil {
		t.Fatalf("Run() error = %v", err)
	}

	if route.GetFruitNum() != 4 {
		t.Fatalf("route.GetFruitNum() = %d, want 4", route.GetFruitNum())
	}
	if left.GetSeedNum() != 2 {
		t.Fatalf("left.GetSeedNum() = %d, want 2", left.GetSeedNum())
	}
	if right.GetSeedNum() != 2 {
		t.Fatalf("right.GetSeedNum() = %d, want 2", right.GetSeedNum())
	}

	mu.Lock()
	defer mu.Unlock()
	assertSeeds(t, received["left"], []string{"even-2", "even-4"})
	assertSeeds(t, received["right"], []string{"odd-1", "odd-3"})
}

// RoutePlot 会跳过路由表中未连接的下游，其余路由项正常转发。
func TestFarmRunRoutePlotSkipsUnconnectedTarget(t *testing.T) {
	var mu sync.Mutex
	var received []string

	left := plot.NewPlot("left", func(seed string) (string, error) {
		mu.Lock()
		received = append(received, seed)
		mu.Unlock()
		return seed, nil
	}, plot.WithTenders(1))
	route := plot.NewRoutePlot("route", func(seed int) (map[string]string, error) {
		return map[string]string{
			"left":  fmt.Sprintf("kept-%d", seed),
			"ghost": "dropped",
		}, nil
	}, plot.WithTenders(1))

	f := farm.NewFarm("route_skip", "INFO")
	if err := f.AddPlot(route, left); err != nil {
		t.Fatalf("AddPlot() error = %v", err)
	}
	if err := f.Connect([]plot.PlotNode{route}, []plot.PlotNode{left}); err != nil {
		t.Fatalf("Connect() error = %v", err)
	}
	if err := f.Run(map[string][]any{
		"route": {1},
	}); err != nil {
		t.Fatalf("Run() error = %v", err)
	}

	if left.GetSeedNum() != 1 {
		t.Fatalf("left.GetSeedNum() = %d, want 1", left.GetSeedNum())
	}

	mu.Lock()
	defer mu.Unlock()
	assertSeeds(t, received, []string{"kept-1"})
}

// assertSeeds 忽略顺序地断言实收 seed 集合与期望一致。
func assertSeeds(t *testing.T, got []string, want []string) {
	t.Helper()

	sortedGot := append([]string(nil), got...)
	sort.Strings(sortedGot)
	if len(sortedGot) != len(want) {
		t.Fatalf("seeds = %v, want %v", sortedGot, want)
	}
	for i := range want {
		if sortedGot[i] != want[i] {
			t.Fatalf("seeds = %v, want %v", sortedGot, want)
		}
	}
}
