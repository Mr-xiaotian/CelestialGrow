# pkg/reporter/push_test.go

> 📅 Last Updated: 2026/11/26

## Purpose

`pkg/reporter/push_test.go` uses a local `httptest` server to verify the entire push chain: event → record → queue → HTTP request. It focuses on **request order**, **JSON field names**, and **field sources** (especially that `event_id` takes `WeedID` and `error_type` uses `%T`), while also covering the fallback path of `toJSONValue` and constructor argument validation.

## Test distribution

| Test | Coverage |
|------|----------|
| `TestPushWatcherPushesFarmStartSnapshotAndShutdown` | End to end: order of 5 requests, payload fields of graph metadata/error/snapshot/shutdown |
| `TestToJSONValueFallsBackToReadableText` | A task payload that cannot be JSON-serialized degrades to a string |
| `TestNewPushWatcherRejectsInvalidArguments` | Empty SessionID, empty URL, nil MetricsView, missing scheme, non-http(s) scheme |

Test helpers:

```go
type capturedRequest struct {
    path string
    body []byte
}

func requestPaths(requests []capturedRequest) []string
```

`capturedRequest` is only appended in the server handler; because `Spout` sends serially in a single goroutine and `http.Client` returns only after the response, appending is naturally serial, so the tests need no locks. `metricsView` reuses the `stubMetricsView` from `snapshot_test.go` (shared across tests in the same package).

## Key assertions and design intent

### End-to-end ordering: `TestPushWatcherPushesFarmStartSnapshotAndShutdown`

It builds a `stubMetricsView` with a single `root` node (`Status: PlotRunning`, `StartTime: time.Unix(5, 0)`, `InputTotal 3 / Processed 2 / Pending 1 / Succeeded 2`, `UpstreamCounts {}`, `DownstreamCounts {"leaf": 2}`), then triggers in sequence:

| Step | Call | Expected request |
|:----:|------|---------|
| 1 | `OnEvent(FarmStartEvent{Farm:"demo", Plots:["root"], Edges:{"root":{"leaf"},"leaf":{}}, SourcePlots:["root"], NodeMeta:{"root":{ClassName:"SinglePlot",MaxWorkers:2}}, StartTime:Unix(10,0)})` | `/api/push_graph_meta` |
| 2 | `PushSnapshot({"root": {Status: 1}})` | `/api/push_snapshot` |
| 3 | `OnEvent(SeedWitherEvent{Plot:"leaf", Seed:{"id":1}, Err:errors.New("boom"), SeedID:7, WeedID:8, Time:Unix(15,0)})` | `/api/push_error` |
| 4 | `OnEvent(FarmEndEvent{Farm:"demo", EndTime:Unix(20,0)})` | `/api/push_snapshot` |
| 5 | `Stop()` | `/api/shutdown_session` |

Finally it compares the **complete path list** with `reflect.DeepEqual`:

```go
[]string{
    "/api/push_graph_meta",
    "/api/push_snapshot",
    "/api/push_error",
    "/api/push_snapshot",
    "/api/shutdown_session",
}
```

This is the most important assertion in the file: it verifies both "FarmEnd first pushes the final-state snapshot, then sends shutdown" and the deterministic order brought by the FIFO queue + single consumer goroutine. `Stop` is placed before the assertion, ensuring the buffer is drained on shutdown (`Spout.Stop` processes the remaining records).

#### Graph metadata payload (`requests[0]`)

| Assertion | Intent |
|------|------|
| `meta["session_id"] == "farm@1"` | The session identifier comes from the constructor argument, not from the event |
| `meta["graph"] == "demo"` | `graph` takes `FarmStartEvent.Farm` |
| `meta["node_meta"]` is a `map[string]any` containing `"root"` | Build-time metadata is reported together with the graph metadata |
| `edges["leaf"]` is `[]any` with length 0 | **An empty adjacency list is encoded as `[]` rather than `null`** (`copyEdges` uses a non-nil empty slice) |

The test does **not** assert the concrete values of `graph_mode`, `start_time`, `is_dag`, `nodes`, `source_nodes`, nor does it check `class_name`/`max_workers` inside `node_meta.root`.

#### Error payload (`requests[2]`)

| Assertion | Intent |
|------|------|
| `session_id == "farm@1"` | The error record carries the session identifier itself |
| `event_id == float64(8)` | It takes `WeedID` (8), **not** `SeedID` (7) |
| `node == "leaf"` | Takes `SeedWitherEvent.Plot` |
| `error_type == "*errors.errorString"` | Produced by `fmt.Sprintf("%T", Err)` |
| `error_message == "boom"` | Produced by `fmt.Sprintf("%v", Err)` |

`task_json` and `ts` are not asserted. JSON numbers all decode to `float64`, so comparing `event_id` requires writing `float64(8)`.

#### Snapshot and shutdown (`requests[3]` / `requests[4]`)

- Both only assert `session_id == "farm@1"`.
- `requests[3]` is the **final-state snapshot** triggered by FarmEnd (not the periodic snapshot of step 2); its `snapshot` content comes from `stubMetricsView`, but the test does not check nodes, counters, or `timestamp`.
- `requests[4]` verifies the shutdown payload carries the session identifier: the server relies on `session_id` to decide which session to take offline, and a missing field would leave the session unreclaimable.

### Fallback serialization: `TestToJSONValueFallsBackToReadableText`

```go
value := make(chan int)
got, ok := toJSONValue(value).(string)
if !ok || got == "" {
    t.Fatalf("toJSONValue(chan) = %#v, want non-empty string", got)
}
```

- `chan int` cannot be encoded by `encoding/json`, so `toJSONValue` must degrade to a non-empty string produced by `fmt.Sprintf("%+v", value)`.
- Only "the type is string and non-empty" is asserted; the concrete text is not compared (the channel address differs on every run), which is deliberate: the fallback goal is "readable and sendable", not a stable value.
- Design intent: a serialization problem in a single task should not make the whole error report fail.

### Argument validation: `TestNewPushWatcherRejectsInvalidArguments`

| Input | Expected rejection reason |
|------|-------------|
| `("", "http://example.com", metricsView)` | Empty SessionID |
| `("demo@1", "", metricsView)` | Empty base URL |
| `("demo@1", "http://example.com", nil)` | nil MetricsView |
| `("demo@1", "127.0.0.1:5005", metricsView)` | Missing scheme |
| `("demo@1", "ftp://example.com", metricsView)` | Non-`http`/`https` |

All cases only check `err == nil` and do not compare error text. The validation order ensures "empty SessionID + empty URL" reports the SessionID error first, but each test makes only one argument invalid, so the order is not verified.

## How to run

```bash
# 仅本文件的测试
go test ./pkg/reporter -run 'TestPushWatcher|TestToJSONValue|TestNewPushWatcher' -v

# 整个包
go test ./pkg/reporter -v
```

It is recommended to run once with `-race` to confirm the asynchronous chain has no data races:

```bash
go test ./pkg/reporter -race
```

## Notes

- **Failure isolation not covered**: no case makes the server return 500 or disconnect, so "a single failed push only logs and later records keep being sent" is guaranteed only by code review.
- **The 512-byte truncation for non-2xx is not covered**: the `io.LimitReader` and draining logic in `post` have no tests.
- **Enqueue before `Start` / `Stop` timeout not covered**: neither the `shutdown timeout` path nor the `inlet send timeout` path has a case; the tests always `Start` first and `Stop` last.
- **`graph_mode` (fixed to `"grow"`), timestamps, and sub-second precision not covered**: `start_time`/`timestamp`/`ts` are never asserted, and the fractional part of `unixSeconds` has no regression protection.
- **Empty/unknown events not covered**: the `default` branch of `OnEvent` (returns `nil` and sends nothing) has no test.
- **A request body read failure is an immediate `t.Fatalf`**: the server handler terminates the test directly when reading the body fails; that is test code, not a production path.
- **Assertions depend on exact order**: if pushing were changed to multi-goroutine parallelism in the future, the path-list comparison would fail — this is an intentional ordering contract and should not be relaxed casually.
