package plot_test

import (
	"fmt"
	"testing"

	"github.com/Mr-xiaotian/CelestialGrow/pkg/plot"
)

// 拆分节点会将单个 seed 成功记录为一条 ripen，并持久化完整拆分结果。
func TestSplitPlot_RunHarvest(t *testing.T) {
	split := plot.NewSplitPlot("split_harvest", func(seed int) ([]string, error) {
		fruits := make([]string, 0, seed)
		for i := 0; i < seed; i++ {
			fruits = append(fruits, fmt.Sprintf("%d-%d", seed, i))
		}
		return fruits, nil
	}, plot.WithTenders(2))

	split.Run([]int{0, 2})
	records := mustHarvest(t, split)
	if len(records) != 2 {
		t.Fatalf("expected 2 statuses, got %d", len(records))
	}

	index := indexStatusesBySeed(records)
	if got := index["0"]; got.Status != "ripen" || got.FruitJSON != "[]" {
		t.Fatalf("seed 0 status = %#v, want ripen with empty result", got)
	}
	if got := index["2"]; got.Status != "ripen" || got.FruitJSON != "[\"2-0\",\"2-1\"]" {
		t.Fatalf("seed 2 status = %#v, want ripen with split result", got)
	}
}
