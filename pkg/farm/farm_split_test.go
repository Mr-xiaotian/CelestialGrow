package farm_test

import (
	"strconv"
	"strings"
	"testing"

	"github.com/Mr-xiaotian/CelestialGrow/pkg/farm"
	"github.com/Mr-xiaotian/CelestialGrow/pkg/plot"
)

// SplitPlot 可以在 Farm 中将单个输入拆成多个下游 seed。
func TestFarmRunSplitPlot(t *testing.T) {
	f := farm.NewFarm("split_plot", "INFO")
	split := plot.NewSplitPlot("split", func(seed string) ([]int, error) {
		if seed == "" {
			return nil, nil
		}

		parts := strings.Split(seed, ",")
		values := make([]int, 0, len(parts))
		for _, part := range parts {
			value, err := strconv.Atoi(strings.TrimSpace(part))
			if err != nil {
				return nil, err
			}
			values = append(values, value)
		}
		return values, nil
	})
	head := plot.NewPlot("head", func(seed int) (int, error) {
		return seed * 10, nil
	}, plot.WithTenders(2))

	if err := f.AddPlot(split, head); err != nil {
		t.Fatalf("AddPlot() error = %v", err)
	}
	if err := f.Connect([]plot.PlotNode{split}, []plot.PlotNode{head}); err != nil {
		t.Fatalf("Connect() error = %v", err)
	}
	if err := f.Run(map[string][]any{
		"split": {"1,2,3", "4,5"},
	}); err != nil {
		t.Fatalf("Run() error = %v", err)
	}

	if split.GetFruitNum() != 2 {
		t.Fatalf("split.GetFruitNum() = %d, want 2", split.GetFruitNum())
	}
	if head.GetSeedNum() != 5 {
		t.Fatalf("head.GetSeedNum() = %d, want 5", head.GetSeedNum())
	}
	if head.GetFruitNum() != 5 {
		t.Fatalf("head.GetFruitNum() = %d, want 5", head.GetFruitNum())
	}

	yield := split.GetDownstreamYieldCounter("head")
	if yield == nil {
		t.Fatal("split downstream yield counter for head should exist")
	}
	if got := yield.Load(); got != 5 {
		t.Fatalf("split downstream yield = %d, want 5", got)
	}
}
