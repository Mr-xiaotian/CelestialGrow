package plot_test

import (
	"errors"
	"strconv"
	"testing"

	"github.com/Mr-xiaotian/CelestialGrow/pkg/persist"
	"github.com/Mr-xiaotian/CelestialGrow/pkg/plot"
)

// mustHarvest 运行后读取指定 plot 的生命周期状态快照。
func mustHarvest[S any, F any](t *testing.T, plot *plot.Plot[S, F]) []persist.LifecycleStatusRecord {
	t.Helper()

	records, err := plot.Harvest()
	if err != nil {
		t.Fatalf("Harvest() error = %v", err)
	}
	return records
}

// indexStatusesByTask 使用 task_json 作为键建立索引，便于逐任务断言。
func indexStatusesByTask(records []persist.LifecycleStatusRecord) map[string]persist.LifecycleStatusRecord {
	index := make(map[string]persist.LifecycleStatusRecord, len(records))
	for _, record := range records {
		index[record.TaskJSON] = record
	}
	return index
}

// 全部失败。
func TestPlot_AllError(t *testing.T) {
	cultivator := func(seed int) (string, error) {
		return "", errors.New("always fail")
	}

	plot := plot.NewPlot("test_all_error", cultivator, plot.WithTenders(2))
	seeds := []int{1, 2, 3, 4, 5}

	plot.Run(seeds)
	records := mustHarvest(t, plot)

	if len(records) != len(seeds) {
		t.Fatalf("expected %d statuses, got %d", len(seeds), len(records))
	}
	for _, record := range records {
		if record.Status != "failed" {
			t.Fatalf("expected failed status, got %#v", record)
		}
		if record.ErrorMessage != "always fail" {
			t.Fatalf("expected error message %q, got %#v", "always fail", record)
		}
	}

	if plot.GetCompleted() != len(seeds) {
		t.Errorf("expected %d completed, got %d", len(seeds), plot.GetCompleted())
	}
	if int(plot.GetState()) != 2 {
		t.Errorf("expected state 2 (done), got %d", plot.GetState())
	}
}

// 部分失败。
func TestPlot_PartialError(t *testing.T) {
	cultivator := func(seed int) (int, error) {
		if seed%2 == 0 {
			return 0, errors.New("even number error")
		}
		return seed * 10, nil
	}

	plot := plot.NewPlot("test_partial_error", cultivator, plot.WithTenders(2))
	seeds := []int{1, 2, 3, 4, 5}

	plot.Run(seeds)
	records := mustHarvest(t, plot)
	index := indexStatusesByTask(records)

	if len(records) != len(seeds) {
		t.Fatalf("expected %d statuses, got %d", len(seeds), len(records))
	}

	successCount := 0
	failedCount := 0
	for _, seed := range seeds {
		record, ok := index[strconv.Itoa(seed)]
		if !ok {
			t.Fatalf("missing lifecycle status for seed %d", seed)
		}

		if seed%2 == 0 {
			failedCount++
			if record.Status != "failed" || record.ErrorMessage != "even number error" {
				t.Fatalf("seed %d expected failed/even number error, got %#v", seed, record)
			}
			continue
		}

		successCount++
		if record.Status != "success" || record.ResultJSON != strconv.Itoa(seed*10) {
			t.Fatalf("seed %d expected success/result %d, got %#v", seed, seed*10, record)
		}
	}

	if successCount != 3 {
		t.Errorf("expected 3 successes, got %d", successCount)
	}
	if failedCount != 2 {
		t.Errorf("expected 2 failures, got %d", failedCount)
	}
	if plot.GetCompleted() != len(seeds) {
		t.Errorf("expected %d completed, got %d", len(seeds), plot.GetCompleted())
	}
}

// 全部成功。
func TestPlot_AllSuccess(t *testing.T) {
	cultivator := func(seed int) (int, error) {
		return seed * 2, nil
	}

	plot := plot.NewPlot("test_all_success", cultivator, plot.WithTenders(3))
	seeds := []int{1, 2, 3, 4, 5}

	plot.Run(seeds)
	records := mustHarvest(t, plot)
	index := indexStatusesByTask(records)

	if len(records) != len(seeds) {
		t.Fatalf("expected %d statuses, got %d", len(seeds), len(records))
	}
	for _, seed := range seeds {
		record, ok := index[strconv.Itoa(seed)]
		if !ok {
			t.Fatalf("missing lifecycle status for seed %d", seed)
		}
		if record.Status != "success" || record.ResultJSON != strconv.Itoa(seed*2) {
			t.Fatalf("seed %d expected success/result %d, got %#v", seed, seed*2, record)
		}
	}
	if int(plot.GetState()) != 2 {
		t.Errorf("expected state 2 (done), got %d", plot.GetState())
	}
}
