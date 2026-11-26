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

// pushRecord 是推送记录的封闭类型集合，仅由本文件中的记录类型实现。
type pushRecord interface {
	isPushRecord()
}

// graphMetaRecord 表示图元信息推送记录。
type graphMetaRecord struct {
	SessionID   string                        `json:"session_id"`
	Graph       string                        `json:"graph"`
	GraphMode   string                        `json:"graph_mode"`
	StartTime   float64                       `json:"start_time"`
	IsDAG       bool                          `json:"is_dag"`
	Nodes       []string                      `json:"nodes"`
	Edges       map[string][]string           `json:"edges"`
	SourceNodes []string                      `json:"source_nodes"`
	NodeMeta    map[string]scarecrow.NodeMeta `json:"node_meta"`
}

// isPushRecord 标记 graphMetaRecord 实现 pushRecord。
func (graphMetaRecord) isPushRecord() {}

// snapshotRecord 表示图级状态快照推送记录。
type snapshotRecord struct {
	SessionID string                `json:"session_id"`
	Timestamp float64               `json:"timestamp"`
	Snapshot  map[string]NodeStatus `json:"snapshot"`
}

// isPushRecord 标记 snapshotRecord 实现 pushRecord。
func (snapshotRecord) isPushRecord() {}

// taskFailRecord 表示单条任务失败推送记录。
type taskFailRecord struct {
	SessionID    string  `json:"session_id"`
	EventID      int     `json:"event_id"`
	Node         string  `json:"node"`
	Task         any     `json:"task_json"`
	ErrorType    string  `json:"error_type"`
	ErrorMessage string  `json:"error_message"`
	Timestamp    float64 `json:"ts"`
}

// isPushRecord 标记 taskFailRecord 实现 pushRecord。
func (taskFailRecord) isPushRecord() {}

// shutdownRecord 表示会话下线推送记录。
type shutdownRecord struct {
	SessionID string `json:"session_id"`
}

// isPushRecord 标记 shutdownRecord 实现 pushRecord。
func (shutdownRecord) isPushRecord() {}

// pushHandler 实现 funnel.RecordHandler，把推送记录逐条发送到远程服务。
type pushHandler struct {
	baseURL string
	timeout time.Duration
	client  *http.Client
}

// BeforeStart 在消费循环启动前创建可复用的 HTTP 客户端。
func (h *pushHandler) BeforeStart() error {
	h.client = newHTTPClient(h.timeout)
	return nil
}

// HandleRecord 推送单条记录；失败只记录日志，不中断消费循环。
func (h *pushHandler) HandleRecord(record pushRecord) error {
	if err := h.handle(record); err != nil {
		log.Print(err)
	}
	return nil
}

// AfterStop 关闭 HTTP 客户端持有的空闲连接。
func (h *pushHandler) AfterStop() error {
	if h.client != nil {
		h.client.CloseIdleConnections()
		h.client = nil
	}
	return nil
}

// handle 按记录类型分发到对应的远程端点。
func (h *pushHandler) handle(record pushRecord) error {
	switch item := record.(type) {
	case graphMetaRecord:
		return h.post("/api/push_graph_meta", item)
	case snapshotRecord:
		return h.post("/api/push_snapshot", item)
	case taskFailRecord:
		return h.post("/api/push_error", item)
	case shutdownRecord:
		return h.post("/api/shutdown_session", item)
	default:
		return fmt.Errorf("reporter: unsupported push record %T", item)
	}
}

// post 将 payload 编码为 JSON 并 POST 到指定端点；非 2xx 时返回带响应体的错误。
func (h *pushHandler) post(path string, payload any) error {
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
		_, _ = io.Copy(io.Discard, res.Body)
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
	if metricsView == nil {
		return nil, fmt.Errorf("reporter: metrics view is nil")
	}

	baseURL, err := normalizeBaseURL(baseURL)
	if err != nil {
		return nil, err
	}

	handler := &pushHandler{
		baseURL: baseURL,
		timeout: DefaultPushTimeout,
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
			SessionID:   w.sessionID,
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
			SessionID:    w.sessionID,
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
		return w.inlet.Send(shutdownRecord{SessionID: w.sessionID})
	default:
		return nil
	}
}

// PushSnapshot 将图级状态快照送入后台推送队列。
func (w *PushWatcher) PushSnapshot(snapshot map[string]NodeStatus) error {
	return w.pushSnapshotAt(snapshot, time.Now())
}

// pushSnapshotAt 将状态快照以指定时间戳送入后台推送队列。
func (w *PushWatcher) pushSnapshotAt(
	snapshot map[string]NodeStatus,
	ts time.Time,
) error {
	return w.inlet.Send(snapshotRecord{
		SessionID: w.sessionID,
		Timestamp: unixSeconds(ts),
		Snapshot:  copySnapshot(snapshot),
	})
}
