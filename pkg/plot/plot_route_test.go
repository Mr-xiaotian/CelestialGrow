package plot_test

import (
	"fmt"
	"testing"

	"github.com/Mr-xiaotian/CelestialGrow/pkg/plot"
)

// 路由节点会将单个 seed 成功记录为一条 ripen，并持久化完整路由表。
func TestRoutePlot_RunHarvest(t *testing.T) {
	route := plot.NewRoutePlot("route_harvest", func(seed int) (map[string]string, error) {
		return map[string]string{
			"left":  fmt.Sprintf("L%d", seed),
			"right": fmt.Sprintf("R%d", seed),
		}, nil
	}, plot.WithTenders(2))

	route.Run([]int{1, 2})
	records := mustHarvest(t, route)
	if len(records) != 2 {
		t.Fatalf("expected 2 statuses, got %d", len(records))
	}

	index := indexStatusesByTask(records)
	if got := index["1"]; got.Status != "ripen" || got.ResultJSON != `{"left":"L1","right":"R1"}` {
		t.Fatalf("seed 1 status = %#v, want ripen with route result", got)
	}
	if got := index["2"]; got.Status != "ripen" || got.ResultJSON != `{"left":"L2","right":"R2"}` {
		t.Fatalf("seed 2 status = %#v, want ripen with route result", got)
	}
}
