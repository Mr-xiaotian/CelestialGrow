package reporter

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/Mr-xiaotian/CelestialGrow/pkg/scarecrow"
)

// toStatusSnapshot 将 Plot 指标快照投影为 Web 端状态快照载荷。
//
// 输出字段对齐 celestialflow-web 的 NodeStatus 契约。该投影只包含"稳定状态"：
// 不包含每拍都变化的派生耗时，避免服务端把等价快照误判为更新。
func toStatusSnapshot(
	metrics map[string]scarecrow.PlotMetrics,
) map[string]NodeStatus {
	snapshot := make(map[string]NodeStatus, len(metrics))
	for name, item := range metrics {
		startTime := 0.0
		if !item.StartTime.IsZero() {
			startTime = unixSeconds(item.StartTime)
		}

		endTime := 0.0
		if !item.EndTime.IsZero() {
			endTime = unixSeconds(item.EndTime)
		}

		snapshot[name] = NodeStatus{
			Status:           int(item.Status),
			TasksInput:       item.InputTotal,
			TasksProcessed:   item.Processed,
			TasksPending:     item.Pending,
			TasksSucceeded:   item.Succeeded,
			TasksFailed:      item.Failed,
			TasksSkipped:     item.Skipped,
			UpstreamCounts:   item.UpstreamCounts,
			DownstreamCounts: item.DownstreamCounts,
			StartTime:        startTime,
			EndTime:          endTime,
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
func copySnapshot(snapshot map[string]NodeStatus) map[string]NodeStatus {
	result := make(map[string]NodeStatus, len(snapshot))
	for name, status := range snapshot {
		status.UpstreamCounts = copyIntMap(status.UpstreamCounts)
		status.DownstreamCounts = copyIntMap(status.DownstreamCounts)
		result[name] = status
	}
	return result
}

// copyIntMap 深拷贝 string -> int 映射，并保证空映射编码为 {} 而不是 null。
func copyIntMap(source map[string]int) map[string]int {
	result := make(map[string]int, len(source))
	for key, value := range source {
		result[key] = value
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

// newHTTPClient 创建持有独立 Transport 的 HTTP 客户端。
//
// 使用克隆的 Transport 可以避免关闭空闲连接时影响进程内其他客户端。
func newHTTPClient(timeout time.Duration) *http.Client {
	transport := http.DefaultTransport
	if base, ok := transport.(*http.Transport); ok {
		transport = base.Clone()
	}
	return &http.Client{
		Timeout:   timeout,
		Transport: transport,
	}
}

// normalizeBaseURL 校验并规范化上报服务的基础地址。
func normalizeBaseURL(raw string) (string, error) {
	trimmed := strings.TrimRight(strings.TrimSpace(raw), "/")
	if trimmed == "" {
		return "", fmt.Errorf("reporter: base url is empty")
	}

	parsed, err := url.Parse(trimmed)
	if err != nil || parsed.Scheme == "" || parsed.Host == "" {
		return "", fmt.Errorf("reporter: invalid base url %q", raw)
	}
	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		return "", fmt.Errorf("reporter: base url must use http or https, got %q", raw)
	}
	return trimmed, nil
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
