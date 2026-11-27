# pkg/assembly/assembly_test.go

> 📅 最后更新日期: 2026/11/26

本文件是 `pkg/assembly` 的端到端装配测试，采用外部测试包 `assembly_test`（只通过公开 API 访问，不触碰未导出字段）。测试覆盖本地装配、远程上报装配以及参数校验三类路径，共 5 个测试函数加 2 个辅助定义。

## 测试覆盖点

| 测试 | 覆盖能力 | 关键不变量 |
|------|----------|-----------|
| `TestStartLocalResourcesAssembleLifecycle` | 本地装配 + 事件落盘 + 重复 Stop + 运行后查询 | 返回非 nil `StopFunc`；`SeedInputEvent` 经 Hub 写入 SQLite；连续两次 `stop()` 都返回 `nil`；`scarecrow.Find` 能取回 `*persist.LifecycleWatcher`；`DBPath()` 非空；快照状态为 `seed`、`SeedJSON` 为 `"42"` |
| `TestStartReportResourcesAssembleReporter` | 远程装配 + 推送链路 | `*reporter.PushWatcher` 注册在 watcher Hub；`*reporter.SnapshotTicker` 与 `*reporter.InjectionTicker` 注册在 ticker Hub；`FarmStartEvent` 最终触发 `POST /api/push_graph_meta` |
| `TestStartLocalResourcesRejectNilHub` | 本地入口的 watcher Hub 边界 | `watcherHub == nil` 时返回非 nil error |
| `TestStartLocalResourcesRejectNilTickerHub` | 本地入口的 ticker Hub 边界 | `tickerHub == nil` 时返回非 nil error |
| `TestStartReportResourcesRejectInvalidArguments` | 远程入口的参数边界 | `report == nil`、`injectionTarget == nil` 分别返回非 nil error |

测试的核心链路是：**创建两个 Hub → 装配资源 → 向 Hub 发事件 → Stop → 从 Hub 取回观察者 → 按数据库路径回读状态快照 / 等待 HTTP 请求到达**。

## 关键测试细节

### 辅助定义

```go
func waitForPath(t *testing.T, paths <-chan string, want string) {
    t.Helper()
    timeout := time.After(3 * time.Second)
    for {
        select {
        case got := <-paths:
            if got == want {
                return
            }
        case <-timeout:
            t.Fatalf("did not receive %s", want)
        }
    }
}

type injectionTargetStub struct{}

func (injectionTargetStub) InjectTasks(map[string][]any) error { return nil }
func (injectionTargetStub) InjectTerminations([]string) error  { return nil }
```

- `waitForPath` 给异步推送留出最多 3 秒，并忽略不匹配的路径。
- `injectionTargetStub` 实现 `reporter.InjectionTarget` 的两个方法，避免远程测试依赖真实 `*farm.Farm`。

### `TestStartLocalResourcesAssembleLifecycle`

```go
hub := scarecrow.NewHub(scarecrow.WithErrorHandler(func(err error) {
    t.Errorf("unexpected watcher error: %v", err)
}))
tickerHub, err := metronome.NewHub(metronome.WithErrorHandler(func(err error) {
    t.Errorf("unexpected ticker error: %v", err)
}))
if err != nil {
    t.Fatalf("NewHub() = %v", err)
}

stop, err := assembly.StartLocalResources(hub, tickerHub, "DEBUG", metronome.DefaultInterval)
if err != nil {
    t.Fatalf("StartLocalResources() = %v", err)
}

now := time.Now()
if err := hub.OnEvent(scarecrow.SeedInputEvent{
    Plot: "node", Seed: 42, SeedRepr: "42", SeedID: 1, Time: now,
}); err != nil {
    t.Fatalf("OnEvent() = %v", err)
}

if err := stop(); err != nil {
    t.Fatalf("stop() = %v", err)
}
if err := stop(); err != nil {
    t.Fatalf("second stop() = %v", err)
}
```

要点：

1. **两个 Hub 的错误处理器都直接记为测试失败**：`Hub.OnEvent` 对观察者故障只走 `ErrorHandler` 且始终返回 `nil`，必须借助处理器捕获「观察者处理事件时报错」。
2. **日志级别传 `DEBUG`**：`SeedInputEvent` 在 `LogWatcher` 中被映射为 `DEBUG` 级日志；传 `DEBUG` 可保证该事件不会被最低级别过滤（本测试并不校验日志文件内容）。
3. **`StopFunc` 连续调用两次且都要求 `nil`**：验证 `stopped` 幂等保护。底层的 `funnel.Spout.Stop` 会 `close` 队列通道，直接重复调用会 `close of closed channel` panic，因此这两行在守护幂等实现。
4. **先 Stop 再查询**：`Stop` 会等消费协程处理完队列并关闭 SQLite；随后 `LoadLifecycleStatusesFromPath` 重新按路径打开数据库文件读取，避免读到未刷盘的数据。

查询与断言：

```go
lifecycle, ok := scarecrow.Find[*persist.LifecycleWatcher](hub)
if !ok {
    t.Fatal("lifecycle watcher is not registered")
}
dbPath := lifecycle.DBPath()
if dbPath == "" {
    t.Fatal("lifecycle database path is empty")
}

records, err := persist.LoadLifecycleStatusesFromPath(dbPath, "node")
if err != nil {
    t.Fatalf("LoadLifecycleStatusesFromPath() = %v", err)
}
if len(records) != 1 {
    t.Fatalf("records = %d, want 1", len(records))
}
if records[0].Status != "seed" || records[0].SeedJSON != "42" {
    t.Fatalf("record = %#v, want seed/42", records[0])
}
```

- `scarecrow.Find` 在 Hub 及其嵌套 Hub 中查找第一个 `*persist.LifecycleWatcher`，验证观察者确实被注册到**传入的 hub**。
- `DBPath()` 非空说明 `LifecycleWatcher.Start` 已经执行到 `BeforeStart` 并创建了 SQLite 文件；配合 `Find` 成功，可确认该观察者既已启动又已注册。
- `len(records) == 1` 验证该 plot 只有一条输入事件对应的状态快照。
- `Status == "seed"` 是因为只发送了 `SeedInputEvent`，没有后续 ripen / wither / prune 事件；`SeedJSON == "42"` 来自 `persist` 对 `Seed` 字段的 JSON 序列化（`int` 42 → `42`）。

### `TestStartReportResourcesAssembleReporter`

测试先用 `httptest.NewServer` 启动一个记录请求路径的本地服务：

```go
paths := make(chan string, 16)
server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
    select {
    case paths <- r.URL.Path:
    default:
    }
    w.WriteHeader(http.StatusOK)
}))
defer server.Close()
```

装配时把 `server.URL` 与会话标识传给 `reporter.Config`：

```go
stop, err := assembly.StartReportResources(
    hub,
    tickerHub,
    injectionTargetStub{},
    "INFO",
    metronome.DefaultInterval,
    &reporter.Config{URL: server.URL, SessionID: "farm@1"},
)
if err != nil {
    t.Fatalf("StartReportResources() = %v", err)
}
defer func() {
    if err := stop(); err != nil {
        t.Fatalf("stop() = %v", err)
    }
}()
```

装配后先检查三类远程资源都已注册：

```go
if _, ok := scarecrow.Find[*reporter.PushWatcher](hub); !ok {
    t.Fatal("push watcher is not registered")
}
if _, ok := metronome.Find[*reporter.SnapshotTicker](tickerHub); !ok {
    t.Fatal("snapshot ticker is not registered")
}
if _, ok := metronome.Find[*reporter.InjectionTicker](tickerHub); !ok {
    t.Fatal("injection ticker is not registered")
}
```

再发送 Farm 启动事件，并等待推送真正到达服务端：

```go
if err := hub.OnEvent(scarecrow.FarmStartEvent{
    Farm:        "farm",
    Plots:       []string{"root"},
    Edges:       map[string][]string{"root": {}},
    SourcePlots: []string{"root"},
    StartTime:   time.Now(),
}); err != nil {
    t.Fatalf("OnEvent(FarmStartEvent) = %v", err)
}

waitForPath(t, paths, "/api/push_graph_meta")
```

- `PushWatcher.OnEvent` 只把记录写入后台队列，HTTP 请求由 `Spout` 的消费 goroutine 发出，因此必须用 `waitForPath` 等待，不能立即断言。
- 端点路径来自 `pushHandler.handle` 的固定映射：`FarmStartEvent → /api/push_graph_meta`。测试没有断言请求体内容。
- `SnapshotPeriod` / `InjectionPeriod` 未设置（零值），分别回落到默认周期 1 / 5。测试只等待图元推送，不对周期请求做断言；等待期间快照 ticker 可能同时发出 `/api/push_snapshot`，`waitForPath` 会忽略不匹配的路径。
- `defer stop()` 在装配成功后立即登记，确保测试结束前停止消费循环与时钟；事件发送与断言都在 `defer` 执行之前完成。

### `TestStartLocalResourcesRejectNilHub`

```go
tickerHub, err := metronome.NewHub()
if err != nil {
    t.Fatalf("NewHub() = %v", err)
}
if _, err := assembly.StartLocalResources(nil, tickerHub, "INFO", metronome.DefaultInterval); err == nil {
    t.Fatal("StartLocalResources(nil, tickerHub) = nil, want error")
}
```

- 只断言「有错误」，不匹配具体错误文案，因此错误信息调整不会破坏测试。
- 该路径在创建观察者之前就返回，不会产生目录或协程副作用。

### `TestStartLocalResourcesRejectNilTickerHub`

```go
watcherHub := scarecrow.NewHub()
if _, err := assembly.StartLocalResources(watcherHub, nil, "INFO", metronome.DefaultInterval); err == nil {
    t.Fatal("StartLocalResources(watcherHub, nil) = nil, want error")
}
```

- 覆盖 ticker Hub 为 nil 的第二个前置校验，同样只断言错误存在。

### `TestStartReportResourcesRejectInvalidArguments`

```go
if _, err := assembly.StartReportResources(
    watcherHub,
    tickerHub,
    injectionTargetStub{},
    "INFO",
    metronome.DefaultInterval,
    nil,
); err == nil {
    t.Fatal("nil report config must be rejected")
}

if _, err := assembly.StartReportResources(
    watcherHub,
    tickerHub,
    nil,
    "INFO",
    metronome.DefaultInterval,
    &reporter.Config{URL: "http://example.com", SessionID: "farm@1"},
); err == nil {
    t.Fatal("nil injection target must be rejected")
}
```

- 依次覆盖 `report == nil` 与 `injectionTarget == nil` 两个校验分支。
- 两个调用都发生在注册观察者之前，因此不会污染后续用例（每个测试也各自新建 Hub）。

## 运行方式

```bash
# 运行本包全部测试
go test ./pkg/assembly/...

# 仅运行本地装配相关用例
go test ./pkg/assembly -run 'TestStartLocalResources'

# 仅运行远程装配相关用例
go test ./pkg/assembly -run 'TestStartReportResources'

# 查看详细结果
go test ./pkg/assembly -v
```

## 注意事项

- **会留下运行时产物**：`TestStartLocalResourcesAssembleLifecycle` 不设置工作目录，`LifecycleRecordHandler` / `LogRecordHandler` 按相对路径创建 `lifecycles/<日期>/grow_lifecycle(<时间>).sqlite3` 与 `logs/grow_log(<日期>).log`，位置是执行 `go test` 时的工作目录（通常是 `pkg/assembly/`）。测试没有清理逻辑，这些目录已由 `.gitignore` 忽略。
- **未覆盖的路径**：
  - 日志观察者的内容（是否真的写入文件、级别过滤是否正确）没有断言；
  - 指标观察者的聚合结果（`GetPlot` / `GetPlots`）没有断言；
  - 推送请求体、请求方法、失败重试没有断言，只验证路径到达；
  - 快照 ticker 与注入 ticker 的周期触发、远程注入接口没有被驱动；
  - 启动失败的各类回滚分支（`AddWatcher` / `AddTicker` / `Hub.Start` 失败）没有覆盖，需要注入失败 handler 或不可写目录才能触发。
- **`StopFunc` 的错误被接收**：本地用例要求两次 `stop()` 都返回 `nil`；远程用例在 `defer` 中检查一次。底层刷盘超时会让测试失败。
- **错误处理器会让观察者故障立即失败**：`WithErrorHandler` 中调用 `t.Errorf` 只影响当前测试；如果观察者 panic，Hub 会把它包装成错误交给该处理器，同样会被捕获。
- **测试依赖真实文件系统与 SQLite 驱动**：`modernc.org/sqlite` 需可用；工作目录不可写或磁盘满时，`Start` 会返回错误并使本地用例失败。
- **远程用例依赖本地网络栈**：`httptest.NewServer` 监听回环地址，若运行环境禁止本地端口监听，远程用例会失败。
