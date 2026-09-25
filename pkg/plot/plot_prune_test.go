package plot_test

import (
	"errors"
	"strconv"
	"sync/atomic"
	"testing"

	"github.com/Mr-xiaotian/CelestialGrow/pkg/plot"
)

// 全量命中修剪：不调用 cultivator，全部终结为 prune。
func TestPlot_PruneSkipsCultivator(t *testing.T) {
	var cultivations atomic.Int32
	cultivator := func(seed int) (int, error) {
		cultivations.Add(1)
		return 0, errors.New("cultivator should never be called")
	}

	plot := plot.NewPlot("test_prune_all", cultivator,
		plot.WithTenders(1),
		plot.WithPruneIf(func(seed int) bool { return true }),
	)
	plot.Run([]int{1, 2, 3})
	records := mustHarvest(t, plot)

	if cultivations.Load() != 0 {
		t.Errorf("expected 0 cultivator calls, got %d", cultivations.Load())
	}
	if len(records) != 3 {
		t.Fatalf("expected 3 statuses, got %d", len(records))
	}
	for _, record := range records {
		if record.Status != "prune" {
			t.Fatalf("expected prune status, got %#v", record)
		}
	}
	if plot.GetPruneNum() != 3 {
		t.Errorf("expected 3 pruned, got %d", plot.GetPruneNum())
	}
	if plot.GetCompleted() != 3 {
		t.Errorf("expected 3 completed, got %d", plot.GetCompleted())
	}
}

// 全部未命中：行为与不设置 WithPruneIf 完全一致。
func TestPlot_PruneMiss(t *testing.T) {
	cultivator := func(seed int) (int, error) {
		return seed * 10, nil
	}

	plot := plot.NewPlot("test_prune_miss", cultivator,
		plot.WithTenders(2),
		plot.WithPruneIf(func(seed int) bool { return false }),
	)
	seeds := []int{1, 2, 3}
	plot.Run(seeds)
	records := mustHarvest(t, plot)

	for _, seed := range seeds {
		record, ok := indexStatusesBySeed(records)[strconv.Itoa(seed)]
		if !ok {
			t.Fatalf("missing lifecycle status for seed %d", seed)
		}
		if record.Status != "ripen" || record.FruitJSON != strconv.Itoa(seed*10) {
			t.Fatalf("seed %d expected ripen/result %d, got %#v", seed, seed*10, record)
		}
	}
	if plot.GetPruneNum() != 0 {
		t.Errorf("expected 0 pruned, got %d", plot.GetPruneNum())
	}
	if plot.GetFruitNum() != len(seeds) {
		t.Errorf("expected %d fruit, got %d", len(seeds), plot.GetFruitNum())
	}
}

// 混合场景：prune / ripen / wither 共存，计数与 GetCompleted 保持平衡。
func TestPlot_PruneMixed(t *testing.T) {
	cultivator := func(seed int) (int, error) {
		if seed%2 == 0 {
			return 0, errors.New("even number error")
		}
		return seed * 10, nil
	}

	plot := plot.NewPlot("test_prune_mixed", cultivator,
		plot.WithTenders(2),
		plot.WithPruneIf(func(seed int) bool { return seed > 3 }),
	)
	seeds := []int{1, 2, 3, 4, 5}
	plot.Run(seeds)
	records := mustHarvest(t, plot)
	index := indexStatusesBySeed(records)

	if len(records) != len(seeds) {
		t.Fatalf("expected %d statuses, got %d", len(seeds), len(records))
	}

	pruneCount, successCount, failedCount := 0, 0, 0
	for _, seed := range seeds {
		record, ok := index[strconv.Itoa(seed)]
		if !ok {
			t.Fatalf("missing lifecycle status for seed %d", seed)
		}

		switch {
		case seed > 3:
			pruneCount++
			if record.Status != "prune" {
				t.Fatalf("seed %d expected prune status, got %#v", seed, record)
			}
		case seed%2 == 0:
			failedCount++
			if record.Status != "wither" {
				t.Fatalf("seed %d expected wither status, got %#v", seed, record)
			}
		default:
			successCount++
			if record.Status != "ripen" {
				t.Fatalf("seed %d expected ripen status, got %#v", seed, record)
			}
		}
	}

	if successCount != 2 || failedCount != 1 || pruneCount != 2 {
		t.Errorf("expected 2 success/1 failed/2 pruned, got %d/%d/%d", successCount, failedCount, pruneCount)
	}
	if plot.GetFruitNum() != 2 || plot.GetWeedNum() != 1 || plot.GetPruneNum() != 2 {
		t.Errorf("expected fruit=2 weed=1 prune=2, got fruit=%d weed=%d prune=%d", plot.GetFruitNum(), plot.GetWeedNum(), plot.GetPruneNum())
	}
	if plot.GetCompleted() != len(seeds) {
		t.Errorf("expected %d completed, got %d", len(seeds), plot.GetCompleted())
	}
}