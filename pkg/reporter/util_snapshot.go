package reporter

import "github.com/Mr-xiaotian/CelestialGrow/pkg/scarecrow"

// toStatusSnapshot 将 Plot 指标快照投影为 Web 端状态快照载荷。
//
// 输出字段对齐 celestialflow-web 的 NodeStatus 契约。该投影只包含"稳定状态"：
// 不包含每拍都变化的派生耗时，避免服务端把等价快照误判为更新。
func toStatusSnapshot(
	metrics map[string]scarecrow.PlotMetrics,
) map[string]map[string]any {
	snapshot := make(map[string]map[string]any, len(metrics))
	for name, item := range metrics {
		startTime := 0.0
		if !item.StartTime.IsZero() {
			startTime = float64(item.StartTime.UnixNano()) / 1e9
		}

		endTime := 0.0
		if !item.EndTime.IsZero() {
			endTime = float64(item.EndTime.UnixNano()) / 1e9
		}

		snapshot[name] = map[string]any{
			"status":            int(item.Status),
			"tasks_input":       item.InputTotal,
			"tasks_processed":   item.Processed,
			"tasks_pending":     item.Pending,
			"tasks_succeeded":   item.Succeeded,
			"tasks_failed":      item.Failed,
			"tasks_skipped":     item.Skipped,
			"upstream_counts":   item.UpstreamCounts,
			"downstream_counts": item.DownstreamCounts,
			"start_time":        startTime,
			"end_time":          endTime,
		}
	}
	return snapshot
}
