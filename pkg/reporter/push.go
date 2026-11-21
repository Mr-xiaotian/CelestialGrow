package reporter

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"strings"
	"time"

	"github.com/Mr-xiaotian/CelestialGrow/pkg/funnel"
	"github.com/Mr-xiaotian/CelestialGrow/pkg/scarecrow"
)

const (
	DefaultPushBufferSize = 64
	DefaultPushTimeout    = 3 * time.Second
	DefaultSendTimeout    = 500 * time.Millisecond

	defaultGraphMode = "grow"
)

type pushRecord interface {
	isPushRecord()
}

type graphMetaRecord struct {
	Graph       string                        `json:"graph"`
	GraphMode   string                        `json:"graph_mode"`
	StartTime   float64                       `json:"start_time"`
	IsDAG       bool                          `json:"is_dag"`
	Nodes       []string                      `json:"nodes"`
	Edges       map[string][]string           `json:"edges"`
	SourceNodes []string                      `json:"source_nodes"`
	NodeMeta    map[string]scarecrow.NodeMeta `json:"node_meta"`
}

func (graphMetaRecord) isPushRecord() {}

type snapshotRecord struct {
	Timestamp float64                   `json:"timestamp"`
	Snapshot  map[string]map[string]any `json:"snapshot"`
}

func (snapshotRecord) isPushRecord() {}

type taskFailRecord struct {
	EventID      int
	Node         string
	Task         any
	ErrorType    string
	ErrorMessage string
	Timestamp    float64
}

func (taskFailRecord) isPushRecord() {}

type shutdownRecord struct{}

func (shutdownRecord) isPushRecord() {}

type pushHandler struct {
	sessionID string
	baseURL   string
	timeout   time.Duration
	client    *http.Client
}

func (h *pushHandler) BeforeStart() error {
	h.client = &http.Client{Timeout: h.timeout}
	return nil
}

func (h *pushHandler) HandleRecord(record pushRecord) error {
	if err := h.handle(record); err != nil {
		log.Print(err)
	}
	return nil
}

func (h *pushHandler) AfterStop() error {
	if h.client != nil {
		if transport, ok := h.client.Transport.(interface{ CloseIdleConnections() }); ok {
			transport.CloseIdleConnections()
		}
		h.client = nil
	}
	return nil
}

func (h *pushHandler) handle(record pushRecord) error {
	switch item := record.(type) {
	case graphMetaRecord:
		return h.post("/api/push_graph_meta", map[string]any{
			"session_id":   h.sessionID,
			"graph":        item.Graph,
			"graph_mode":   item.GraphMode,
			"start_time":   item.StartTime,
			"is_dag":       item.IsDAG,
			"nodes":        item.Nodes,
			"edges":        item.Edges,
			"source_nodes": item.SourceNodes,
			"node_meta":    item.NodeMeta,
		})
	case snapshotRecord:
		return h.post("/api/push_snapshot", map[string]any{
			"session_id": h.sessionID,
			"timestamp":  item.Timestamp,
			"snapshot":   item.Snapshot,
		})
	case taskFailRecord:
		return h.post("/api/push_error", map[string]any{
			"session_id":    h.sessionID,
			"event_id":      item.EventID,
			"node":          item.Node,
			"task_json":     item.Task,
			"error_type":    item.ErrorType,
			"error_message": item.ErrorMessage,
			"ts":            item.Timestamp,
		})
	case shutdownRecord:
		return h.post("/api/shutdown_session", map[string]any{
			"session_id": h.sessionID,
		})
	default:
		return fmt.Errorf("reporter: unsupported push record %T", item)
	}
}

func (h *pushHandler) post(path string, payload map[string]any) error {
	client := h.client
	if client == nil {
		return fmt.Errorf("reporter: push client is not initialized")
	}

	body, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("reporter: marshal %s payload: %w", path, err)
	}
	res, err := client.Post(
		h.baseURL+path,
		"application/json",
		bytes.NewReader(body),
	)
	if err != nil {
		return fmt.Errorf("reporter: push %s: %w", path, err)
	}
	defer res.Body.Close()

	if res.StatusCode/100 != 2 {
		detail, _ := io.ReadAll(io.LimitReader(res.Body, 512))
		if message := strings.TrimSpace(string(detail)); message != "" {
			return fmt.Errorf("reporter: push %s failed: %s: %s", path, res.Status, message)
		}
		return fmt.Errorf("reporter: push %s failed: %s", path, res.Status)
	}
	return nil
}

// PushWatcher 将 Farm 事件与周期快照翻译为远程上报请求。
//
// 事件侧处理 FarmStartEvent / FarmEndEvent / SeedWitherEvent：启动事件上报图元
// 信息，最终枯萎事件上报单条错误，结束事件先补推一次终态快照再通知会话下线。
// 周期状态快照由 SnapshotTicker 调用 PushSnapshot 送入后台 spout，实际网络请求
// 在独立 goroutine 中执行。
type PushWatcher struct {
	sessionID   string
	metricsView MetricsView
	spout       *funnel.Spout[pushRecord]
	inlet       *funnel.Inlet[pushRecord]
}

// NewPushWatcher 创建一个上报推送观察者。
func NewPushWatcher(
	sessionID string,
	baseURL string,
	metricsView MetricsView,
) (*PushWatcher, error) {
	if strings.TrimSpace(sessionID) == "" {
		return nil, fmt.Errorf("reporter: session id is empty")
	}
	if strings.TrimSpace(baseURL) == "" {
		return nil, fmt.Errorf("reporter: base url is empty")
	}
	if metricsView == nil {
		return nil, fmt.Errorf("reporter: metrics view is nil")
	}

	handler := &pushHandler{
		sessionID: sessionID,
		baseURL:   strings.TrimRight(baseURL, "/"),
		timeout:   DefaultPushTimeout,
	}
	spout := funnel.NewSpout(handler, DefaultPushBufferSize, DefaultPushTimeout)
	return &PushWatcher{
		sessionID:   sessionID,
		metricsView: metricsView,
		spout:       spout,
		inlet:       funnel.NewInlet(spout.GetQueue(), DefaultSendTimeout),
	}, nil
}

// Start 启动后台推送循环。
func (w *PushWatcher) Start() error {
	return w.spout.Start()
}

// Stop 停止后台推送循环。
func (w *PushWatcher) Stop() error {
	w.inlet.Close()
	return w.spout.Stop()
}

// OnEvent 接收并处理 Farm 级事件。
func (w *PushWatcher) OnEvent(event scarecrow.Event) error {
	switch item := event.(type) {
	case scarecrow.FarmStartEvent:
		return w.inlet.Send(graphMetaRecord{
			Graph:       item.Farm,
			GraphMode:   defaultGraphMode,
			StartTime:   unixSeconds(item.StartTime),
			IsDAG:       item.IsDAG,
			Nodes:       append([]string{}, item.Plots...),
			Edges:       copyEdges(item.Edges),
			SourceNodes: append([]string{}, item.SourcePlots...),
			NodeMeta:    copyNodeMeta(item.NodeMeta),
		})
	case scarecrow.SeedWitherEvent:
		return w.inlet.Send(taskFailRecord{
			EventID:      item.WeedID,
			Node:         item.Plot,
			Task:         toJSONValue(item.Seed),
			ErrorType:    fmt.Sprintf("%T", item.Err),
			ErrorMessage: fmt.Sprintf("%v", item.Err),
			Timestamp:    unixSeconds(item.Time),
		})
	case scarecrow.FarmEndEvent:
		snapshot := toStatusSnapshot(w.metricsView.GetPlots())
		if err := w.pushSnapshotAt(snapshot, item.EndTime); err != nil {
			return err
		}
		return w.inlet.Send(shutdownRecord{})
	default:
		return nil
	}
}

// PushSnapshot 将图级状态快照送入后台推送队列。
func (w *PushWatcher) PushSnapshot(snapshot map[string]map[string]any) error {
	return w.pushSnapshotAt(snapshot, time.Now())
}

func (w *PushWatcher) pushSnapshotAt(
	snapshot map[string]map[string]any,
	ts time.Time,
) error {
	return w.inlet.Send(snapshotRecord{
		Timestamp: unixSeconds(ts),
		Snapshot:  copySnapshot(snapshot),
	})
}

func copyEdges(edges map[string][]string) map[string][]string {
	result := make(map[string][]string, len(edges))
	for from, targets := range edges {
		// 使用非 nil 的空切片，保证无出边的节点编码为 [] 而不是 null。
		result[from] = append([]string{}, targets...)
	}
	return result
}

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
