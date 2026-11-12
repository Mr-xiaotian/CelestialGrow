package farm_test

import (
	"testing"

	"github.com/Mr-xiaotian/CelestialGrow/pkg/farm"
	"github.com/Mr-xiaotian/CelestialGrow/pkg/plot"
)

// TestFarmRunHarvest 验证图表结束后，plot 能通过嵌套 hub 找到 farm 级
// 生命周期观察者并读取自身状态快照。
func TestFarmRunHarvest(t *testing.T) {
	root := plot.NewSinglePlot("root", func(seed int) (int, error) {
		return seed * 2, nil
	}, plot.WithTenders(2))

	farm := farm.NewFarm("farm_harvest")
	if err := farm.AddPlot(root); err != nil {
		t.Fatalf("AddPlot() error = %v", err)
	}
	if err := farm.Run(map[string][]any{
		"root": {1, 2, 3},
	}, "INFO"); err != nil {
		t.Fatalf("Run() error = %v", err)
	}

	records, err := root.Harvest()
	if err != nil {
		t.Fatalf("Harvest() error = %v", err)
	}
	if len(records) != 3 {
		t.Fatalf("records = %d, want 3", len(records))
	}
	for _, record := range records {
		if record.Status != "ripen" {
			t.Fatalf("record status = %q, want ripen: %#v", record.Status, record)
		}
	}
}
