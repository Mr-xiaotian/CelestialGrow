package reporter

import (
	"encoding/json"
	"fmt"
	"time"

	"github.com/Mr-xiaotian/CelestialGrow/pkg/scarecrow"
)

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
			startTime = unixSeconds(item.StartTime)
		}

		endTime := 0.0
		if !item.EndTime.IsZero() {
			endTime = unixSeconds(item.EndTime)
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

// copyEdges 深拷贝边邻接表，并保证空邻接表编码为 [] 而不是 null。
func copyEdges(edges map[string][]string) map[string][]string {
	result := make(map[string][]string, len(edges))
	for from, targets := range edges {
		// 使用非 nil 的空切片，保证无出边的节点编码为 [] 而不是 null。
		result[from] = append([]string{}, targets...)
	}
	return result
}

// copySnapshot 深拷贝状态快照，避免入队后仍被调用方修改。
func copySnapshot(snapshot map[string]map[string]any) map[string]map[string]any {
	result := make(map[string]map[string]any, len(snapshot))
	for name, status := range snapshot {
		copied := make(map[string]any, len(status))
		for key, value := range status {
			copied[key] = value
		}
		result[name] = copied
	}
	return result
}

// copyNodeMeta 复制节点元信息表，避免入队后仍被调用方修改。
func copyNodeMeta(meta map[string]scarecrow.NodeMeta) map[string]scarecrow.NodeMeta {
	result := make(map[string]scarecrow.NodeMeta, len(meta))
	for name, fields := range meta {
		result[name] = fields
	}
	return result
}

// unixSeconds 将时间转换为 Unix 秒。
func unixSeconds(at time.Time) float64 {
	return float64(at.Unix()) + float64(at.Nanosecond())/1e9
}

// toJSONValue 将任意值转换为可安全编码为 JSON 的载荷。
//
// 可被 encoding/json 直接编码的值原样返回；否则退化为可读字符串，保证单条
// 错误上报不会因任务载荷不可序列化而整体失败。
func toJSONValue(value any) any {
	if _, err := json.Marshal(value); err == nil {
		return value
	}
	return fmt.Sprintf("%+v", value)
}
