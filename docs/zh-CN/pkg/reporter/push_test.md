# pkg/reporter/push_test.go

> 📅 最后更新日期: 2026/11/26

## 作用

`pkg/reporter/push_test.go` 用本地 `httptest` 服务端验证整条推送链路：事件 → 记录 → 队列 → HTTP 请求。它重点锁定**请求顺序**、**JSON 字段名**与**字段来源**（尤其是 `event_id` 取 `WeedID`、`error_type` 用 `%T`），同时覆盖 `toJSONValue` 的降级路径与构造函数参数校验。

## 测试分布

| 测试 | 覆盖内容 |
|------|----------|
| `TestPushWatcherPushesFarmStartSnapshotAndShutdown` | 端到端：5 次请求的顺序、图元/错误/快照/下线载荷字段 |
| `TestToJSONValueFallsBackToReadableText` | 不可 JSON 序列化的任务载荷降级为字符串 |
| `TestNewPushWatcherRejectsInvalidArguments` | 空 SessionID、空 URL、nil MetricsView、缺 scheme、非 http(s) scheme |

测试辅助：

```go
type capturedRequest struct {
    path string
    body []byte
}

func requestPaths(requests []capturedRequest) []string
```

`capturedRequest` 只在服务端处理函数中追加；由于 `Spout` 单 goroutine 串行发送、且 `http.Client` 等到响应才返回，追加天然串行，测试不需要加锁。`metricsView` 直接复用 `snapshot_test.go` 中的 `stubMetricsView`（同包测试共享）。

## 关键断言与设计意图

### 端到端顺序：`TestPushWatcherPushesFarmStartSnapshotAndShutdown`

构造一个只有一个 `root` 节点的 `stubMetricsView`（`Status: PlotRunning`、`StartTime: time.Unix(5, 0)`、`InputTotal 3 / Processed 2 / Pending 1 / Succeeded 2`、`UpstreamCounts {}`、`DownstreamCounts {"leaf": 2}`），随后依次触发：

| 步骤 | 调用 | 预期请求 |
|:----:|------|---------|
| 1 | `OnEvent(FarmStartEvent{Farm:"demo", Plots:["root"], Edges:{"root":{"leaf"},"leaf":{}}, SourcePlots:["root"], NodeMeta:{"root":{ClassName:"SinglePlot",MaxWorkers:2}}, StartTime:Unix(10,0)})` | `/api/push_graph_meta` |
| 2 | `PushSnapshot({"root": {Status: 1}})` | `/api/push_snapshot` |
| 3 | `OnEvent(SeedWitherEvent{Plot:"leaf", Seed:{"id":1}, Err:errors.New("boom"), SeedID:7, WeedID:8, Time:Unix(15,0)})` | `/api/push_error` |
| 4 | `OnEvent(FarmEndEvent{Farm:"demo", EndTime:Unix(20,0)})` | `/api/push_snapshot` |
| 5 | `Stop()` | `/api/shutdown_session` |

最终用 `reflect.DeepEqual` 比对**完整路径列表**：

```go
[]string{
    "/api/push_graph_meta",
    "/api/push_snapshot",
    "/api/push_error",
    "/api/push_snapshot",
    "/api/shutdown_session",
}
```

这是本文件最重要的断言：它同时验证了「FarmEnd 先补推终态快照、再发 shutdown」，以及队列 FIFO + 单消费 goroutine 带来的确定性顺序。`Stop` 放在断言之前，确保关闭时缓冲区被排空（`Spout.Stop` 会处理完剩余记录）。

#### 图元信息载荷（`requests[0]`）

| 断言 | 意图 |
|------|------|
| `meta["session_id"] == "farm@1"` | 会话标识来自构造参数，而不是事件 |
| `meta["graph"] == "demo"` | `graph` 取 `FarmStartEvent.Farm` |
| `meta["node_meta"]` 是 `map[string]any` 且含 `"root"` | 构建期元信息随图元一次性上报 |
| `edges["leaf"]` 是 `[]any` 且长度为 0 | **空邻接表编码为 `[]` 而非 `null`**（`copyEdges` 使用非 nil 空切片） |

测试**未**断言 `graph_mode`、`start_time`、`is_dag`、`nodes`、`source_nodes` 的具体值，也未检查 `node_meta.root` 内部的 `class_name`/`max_workers`。

#### 错误载荷（`requests[2]`）

| 断言 | 意图 |
|------|------|
| `session_id == "farm@1"` | 错误记录自带会话标识 |
| `event_id == float64(8)` | 取的是 `WeedID`（8），**不是** `SeedID`（7） |
| `node == "leaf"` | 取 `SeedWitherEvent.Plot` |
| `error_type == "*errors.errorString"` | 由 `fmt.Sprintf("%T", Err)` 产生 |
| `error_message == "boom"` | 由 `fmt.Sprintf("%v", Err)` 产生 |

`task_json` 与 `ts` 未被断言。JSON 数字统一解码为 `float64`，因此比较 `event_id` 时必须写 `float64(8)`。

#### 快照与下线（`requests[3]` / `requests[4]`）

- 两者都只断言 `session_id == "farm@1"`。
- `requests[3]` 是 FarmEnd 触发的**终态快照**（不是第 2 步的周期快照），其 `snapshot` 内容来自 `stubMetricsView`，但测试未检查节点、计数或 `timestamp`。
- `requests[4]` 验证 shutdown 载荷带会话标识：服务端依赖 `session_id` 判断下线哪个会话，缺少该字段会导致会话无法回收。

### 降级序列化：`TestToJSONValueFallsBackToReadableText`

```go
value := make(chan int)
got, ok := toJSONValue(value).(string)
if !ok || got == "" {
    t.Fatalf("toJSONValue(chan) = %#v, want non-empty string", got)
}
```

- `chan int` 无法被 `encoding/json` 编码，`toJSONValue` 必须退化为 `fmt.Sprintf("%+v", value)` 得到的非空字符串。
- 只断言「类型是 string 且非空」，不比对具体文本（通道地址每次运行都不同），这是刻意的：降级目标是"可读且可发送"，而非稳定值。
- 设计意图：单个任务的序列化问题不应让整条错误上报失败。

### 参数校验：`TestNewPushWatcherRejectsInvalidArguments`

| 输入 | 预期拒绝原因 |
|------|-------------|
| `("", "http://example.com", metricsView)` | 空 SessionID |
| `("demo@1", "", metricsView)` | 空 base URL |
| `("demo@1", "http://example.com", nil)` | nil MetricsView |
| `("demo@1", "127.0.0.1:5005", metricsView)` | 缺 scheme |
| `("demo@1", "ftp://example.com", metricsView)` | 非 `http`/`https` |

所有用例只判断 `err == nil`，不比对错误文本。校验顺序保证「空 SessionID + 空 URL」先报 SessionID 错误，但测试分别只让一个参数非法，未验证顺序。

## 运行方式

```bash
# 仅本文件的测试
go test ./pkg/reporter -run 'TestPushWatcher|TestToJSONValue|TestNewPushWatcher' -v

# 整个包
go test ./pkg/reporter -v
```

建议带 `-race` 跑一次，确认异步链路没有数据竞争：

```bash
go test ./pkg/reporter -race
```

## 注意事项

- **未覆盖失败隔离**：没有用例让服务端返回 500 或直接断开，因此「单条推送失败只记日志、后续记录继续发送」只能靠代码审查保证。
- **未覆盖非 2xx 的 512 字节截断**：`post` 的 `io.LimitReader` 与排空逻辑无测试。
- **未覆盖 `Start` 前入队 / `Stop` 超时**：`shutdown timeout` 路径与 `inlet send timeout` 路径都没有用例；测试总是先 `Start`、最后 `Stop`。
- **未覆盖 `graph_mode`（固定 `"grow"`）、时间戳与子秒精度**：`start_time`/`timestamp`/`ts` 均未断言，`unixSeconds` 的小数部分没有回归保护。
- **未覆盖空/未知事件**：`OnEvent` 的 `default` 分支（返回 `nil` 且不发送）没有测试。
- **请求体读取失败即 `t.Fatalf`**：服务端处理函数在读取 body 失败时直接终止测试；它属于测试代码而非生产路径。
- **断言依赖精确顺序**：若未来把推送改为多协程并行，路径列表比较会失败——这是有意的顺序契约，不应随意放宽。
