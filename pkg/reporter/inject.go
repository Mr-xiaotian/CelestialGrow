package reporter

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/Mr-xiaotian/CelestialGrow/pkg/metronome"
)

const (
	DefaultInjectionPeriod = 5
	DefaultPullTimeout     = 3 * time.Second
)

// InjectionTarget 描述远程注入的最小目标接口。
type InjectionTarget interface {
	InjectTasks(tasks map[string][]any) error
	InjectTerminations(nodes []string) error
}

type injectionPayload struct {
	Tasks        map[string][]any `json:"tasks"`
	Terminations []string         `json:"terminations"`
}

// InjectionTicker 按拍从远程服务拉取注入信息并交给目标处理。
//
// 处理器自持 HTTP 客户端：Start 建立可复用 client，Stop 关闭空闲连接。每次
// OnTick 调用都会请求 `/api/pull_injection?session_id=...`，把返回的 tasks 与
// terminations 依次交给目标对象。
type InjectionTicker struct {
	baseURL   string
	sessionID string
	target    InjectionTarget
	period    int
	timeout   time.Duration
	client    *http.Client
}

// NewInjectionTicker 创建一个远程注入 ticker。
func NewInjectionTicker(
	baseURL string,
	sessionID string,
	target InjectionTarget,
	period int,
	timeout time.Duration,
) (*InjectionTicker, error) {
	if target == nil {
		return nil, fmt.Errorf("reporter: injection target is nil")
	}
	if strings.TrimSpace(sessionID) == "" {
		return nil, fmt.Errorf("reporter: session id is empty")
	}
	if period == 0 {
		period = DefaultInjectionPeriod
	}
	if period < 1 {
		return nil, fmt.Errorf("reporter: injection period must be positive, got %d", period)
	}
	if timeout == 0 {
		timeout = DefaultPullTimeout
	}
	if timeout < 0 {
		return nil, fmt.Errorf("reporter: pull timeout must be non-negative, got %s", timeout)
	}

	baseURL, err := normalizeBaseURL(baseURL)
	if err != nil {
		return nil, err
	}

	return &InjectionTicker{
		baseURL:   baseURL,
		sessionID: sessionID,
		target:    target,
		period:    period,
		timeout:   timeout,
	}, nil
}

// TickPeriod 返回注入 ticker 的节拍周期。
func (t *InjectionTicker) TickPeriod() int {
	return t.period
}

// Start 建立用于拉取注入的 HTTP 客户端。
func (t *InjectionTicker) Start() error {
	if t.client == nil {
		t.client = newHTTPClient(t.timeout)
	}
	return nil
}

// Stop 关闭 HTTP 客户端持有的空闲连接。
func (t *InjectionTicker) Stop() error {
	if t.client != nil {
		t.client.CloseIdleConnections()
		t.client = nil
	}
	return nil
}

// OnTick 拉取一次注入信息并交给目标处理。
func (t *InjectionTicker) OnTick(event metronome.TickEvent) error {
	client := t.client
	if client == nil {
		return fmt.Errorf("reporter: injection client is not initialized")
	}

	endpoint := t.baseURL + "/api/pull_injection?session_id=" + url.QueryEscape(t.sessionID)
	res, err := client.Get(endpoint)
	if err != nil {
		return fmt.Errorf("reporter: pull injection: %w", err)
	}
	defer res.Body.Close()

	if res.StatusCode/100 != 2 {
		return fmt.Errorf("reporter: pull injection failed: %s", res.Status)
	}

	payload := injectionPayload{
		Tasks:        map[string][]any{},
		Terminations: []string{},
	}
	if err := json.NewDecoder(res.Body).Decode(&payload); err != nil {
		return fmt.Errorf("reporter: decode injection payload: %w", err)
	}
	if payload.Tasks == nil {
		payload.Tasks = map[string][]any{}
	}
	if payload.Terminations == nil {
		payload.Terminations = []string{}
	}

	if err := t.target.InjectTasks(payload.Tasks); err != nil {
		return err
	}
	return t.target.InjectTerminations(payload.Terminations)
}
