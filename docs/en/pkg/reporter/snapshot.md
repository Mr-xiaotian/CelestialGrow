# pkg/reporter/snapshot.go

> 📅 Last Updated: 2026/11/26

`pkg/reporter/snapshot.go` defines the public contract and the periodic driver for **graph-level status snapshots**: `MetricsView` (data source), `NodeStatus` (web-side payload), `SnapshotSink` (write target), and `SnapshotTicker` (per-tick projection). It only performs pure "metrics → payload" projection and forwarding and **executes no network IO**.

## Purpose

- Project `scarecrow.PlotMetrics` into `NodeStatus` aligned with celestialflow-web.
- Hand the snapshot to the downstream via `SnapshotSink`; in production assembly the downstream is `PushWatcher`, which then asynchronously POSTs it to `/api/push_snapshot`.
- Attach to the tick hub as a `metronome.Ticker` (only `OnTick` + `TickPeriod`), with no need for `Start` / `Stop`.

## Core objects

### Constant

```go
const DefaultSnapshotPeriod = 1 // 每拍都推
```

### `type MetricsView` (interface)

```go
type MetricsView interface {
    GetPlots() map[string]scarecrow.PlotMetrics
}
```

| Convention | Description |
|------|------|
| Keys | Plot names |
| Values | `scarecrow.PlotMetrics` snapshots (status, counters, start/end times, upstream/downstream counters) |
| Ownership | The caller reads only and must not mutate the returned map or the counter dictionaries inside it |
| Thread safety | `GetPlots` may be called by the tick goroutine, so implementations must do their own locking |

The standard implementation is `*scarecrow.MetricsWatcher`: it copies the metrics and counter dictionaries under a read lock and returns an immutable snapshot, so it can be handed to `SnapshotTicker` directly.

### `type NodeStatus`

```go
type NodeStatus struct {
    Status           int            `json:"status"`
    TasksInput       int            `json:"tasks_input"`
    TasksProcessed   int            `json:"tasks_processed"`
    TasksPending     int            `json:"tasks_pending"`
    TasksSucceeded   int            `json:"tasks_succeeded"`
    TasksFailed      int            `json:"tasks_failed"`
    TasksSkipped     int            `json:"tasks_skipped"`
    UpstreamCounts   map[string]int `json:"upstream_counts"`
    DownstreamCounts map[string]int `json:"downstream_counts"`
    StartTime        float64        `json:"start_time"`
    EndTime          float64        `json:"end_time"`
}
```

Field names and semantics must stay consistent with `NodeStatus` in celestialflow-web:

| Go field | JSON key | Type | Semantics | Source |
|---------|---------|------|------|------|
| `Status` | `status` | `int` | `0` not started / `1` running / `2` stopped | `int(PlotMetrics.Status)` |
| `TasksInput` | `tasks_input` | `int` | Total input tasks (external injection + upstream delivery) | `InputTotal` |
| `TasksProcessed` | `tasks_processed` | `int` | Total processed (succeeded + failed + skipped) | `Processed` |
| `TasksPending` | `tasks_pending` | `int` | Input but not yet resolved | `Pending` |
| `TasksSucceeded` | `tasks_succeeded` | `int` | Succeeded count | `Succeeded` |
| `TasksFailed` | `tasks_failed` | `int` | Failed count | `Failed` |
| `TasksSkipped` | `tasks_skipped` | `int` | Skipped count | `Skipped` |
| `UpstreamCounts` | `upstream_counts` | `map[string]int` | Deliveries from each upstream → this node | `UpstreamCounts` |
| `DownstreamCounts` | `downstream_counts` | `map[string]int` | Outputs from this node → each downstream | `DownstreamCounts` |
| `StartTime` | `start_time` | `float64` | Start Unix float seconds; `0` if not started | `StartTime` (zero value → `0`) |
| `EndTime` | `end_time` | `float64` | End Unix float seconds; `0` if not ended | `EndTime` (zero value → `0`) |

> **`elapsed_time` is deliberately excluded**: that value changes on every tick, which would make the server mistake content-equivalent snapshots for updates; the front end instead derives it from `start_time` / `end_time` and the browser clock.

### `type SnapshotSink` (interface)

```go
type SnapshotSink interface {
    PushSnapshot(snapshot map[string]NodeStatus) error
}
```

- The production implementation is `*PushWatcher` (enqueue, then push asynchronously).
- The interface has write semantics only, with no `Close`; the lifecycle is managed by the sink itself.
- The returned error is propagated **as-is** by `SnapshotTicker.OnTick`, without wrapping.

### `type SnapshotTicker`

```go
type SnapshotTicker struct {
    metricsView MetricsView
    sink        SnapshotSink
    period      int
}
```

#### Construction: `NewSnapshotTicker`

```go
func NewSnapshotTicker(metricsView MetricsView, sink SnapshotSink, period int) (*SnapshotTicker, error)
```

Validation order and errors:

| Order | Condition | Error |
|:----:|------|------|
| 1 | `metricsView == nil` | `reporter: metrics view is nil` |
| 2 | `sink == nil` | `reporter: snapshot sink is nil` |
| 3 | `period == 0` → replaced with `1`; then `period < 1` | `reporter: snapshot period must be positive, got %d` |

- The interface nil check only looks at "whether the interface value is nil": passing a **typed nil pointer** such as `(*SomeView)(nil)` is not rejected, and calling `GetPlots` on it may panic.
- A negative period is not degraded to the default but reported as an error directly.

#### Methods

| Method | Signature | Behavior |
|------|------|------|
| `TickPeriod` | `func (t *SnapshotTicker) TickPeriod() int` | Returns the effective period (defaults already filled) |
| `OnTick` | `func (t *SnapshotTicker) OnTick(event metronome.TickEvent) error` | Reads metrics once, projects, and hands to the sink |

`OnTick` details:

1. `event` is completely unused.
2. `toStatusSnapshot(t.metricsView.GetPlots())` projects the whole graph every time: with N nodes and E counter entries, the cost is `O(N + E)`.
3. It directly `return t.sink.PushSnapshot(snapshot)`, with no wrapping and no retry.
4. No caching, no deduplication: even if the metrics are unchanged, an equivalent snapshot is generated and forwarded on every tick. Deduplication is the server's responsibility.

## Key flow

```mermaid
flowchart LR
    MW[MetricsWatcher<br/>scarecrow] -->|GetPlots| MV[MetricsView]
    MV -->|map[string]PlotMetrics| T[SnapshotTicker.OnTick]
    T -->|toStatusSnapshot| NS[map[string]NodeStatus]
    NS -->|SnapshotSink.PushSnapshot| PW[PushWatcher]
    PW -->|Inlet.Send| Q[(async queue)]
    Q -->|POST /api/push_snapshot| API[Reporting service]
```

Division of responsibilities between `SnapshotTicker` and `PushWatcher`:

- the ticker decides **when** to read the data (the tick period);
- the watcher decides **how** to send it (session identifier, timestamp, JSON, retry policy = none).

## Usage examples

### Consuming snapshots directly (custom sink)

```go
package main

import (
    "fmt"

    "github.com/Mr-xiaotian/CelestialGrow/pkg/metronome"
    "github.com/Mr-xiaotian/CelestialGrow/pkg/reporter"
    "github.com/Mr-xiaotian/CelestialGrow/pkg/scarecrow"
)

// printSink 把快照打印到标准输出，演示 SnapshotSink 的最小实现。
type printSink struct{}

func (printSink) PushSnapshot(snapshot map[string]reporter.NodeStatus) error {
    for name, status := range snapshot {
        fmt.Printf("%s: status=%d input=%d pending=%d\n",
            name, status.Status, status.TasksInput, status.TasksPending)
    }
    return nil
}

func main() {
    metrics := scarecrow.NewMetricsWatcher() // 实现 MetricsView

    ticker, err := reporter.NewSnapshotTicker(metrics, printSink{}, 0) // 0 → 每拍
    if err != nil {
        panic(err)
    }
    fmt.Println("period:", ticker.TickPeriod())

    // 通常由 metronome 驱动；这里手动触发一次。
    if err := ticker.OnTick(metronome.TickEvent{Seq: 1}); err != nil {
        fmt.Println("snapshot failed:", err)
    }
}
```

### Registering with the tick Hub (equivalent to the production assembly)

```go
package main

import (
    "fmt"
    "time"

    "github.com/Mr-xiaotian/CelestialGrow/pkg/metronome"
    "github.com/Mr-xiaotian/CelestialGrow/pkg/reporter"
    "github.com/Mr-xiaotian/CelestialGrow/pkg/scarecrow"
)

func main() {
    // 指标源同时作为 MetricsView 传给快照 ticker 与推送观察者。
    metrics := scarecrow.NewMetricsWatcher()

    pushWatcher, err := reporter.NewPushWatcher("demo@1", "http://127.0.0.1:5005", metrics)
    if err != nil {
        panic(err)
    }
    ticker, err := reporter.NewSnapshotTicker(metrics, pushWatcher, 2) // 每 2 拍
    if err != nil {
        panic(err)
    }

    watcherHub := scarecrow.NewHub()
    if err := watcherHub.AddWatcher(pushWatcher); err != nil {
        panic(err)
    }
    tickerHub, err := metronome.NewHub()
    if err != nil {
        panic(err)
    }
    if err := tickerHub.AddTicker(ticker); err != nil { // SnapshotTicker 不是 Lifecycle
        panic(err)
    }
    clock, err := metronome.NewClock(time.Second, tickerHub)
    if err != nil {
        panic(err)
    }

    // 先启动 watcher（PushWatcher.Start），再启动节拍。
    if err := watcherHub.Start(); err != nil {
        panic(err)
    }
    if err := tickerHub.Start(); err != nil {
        panic(err)
    }
    if err := clock.Start(); err != nil {
        panic(err)
    }

    time.Sleep(2500 * time.Millisecond) // 供观察两拍

    _ = clock.Stop()
    if err := tickerHub.Stop(); err != nil {
        fmt.Println(err)
    }
    if err := watcherHub.Stop(); err != nil { // 排空推送队列并断开会话
        fmt.Println(err)
    }
}
```

`SnapshotTicker` has no `Start` / `Stop`; `metronome.Hub` only treats it as a pure ticker participating in tick dispatch. Whether its sink has been started is guaranteed by the assembler — `pkg/assembly` calls `watcherHub.Start()` first (which starts `PushWatcher` internally), then `tickerHub.Start()` and `clock.Start()`.

## Notes

- **Projection does not deep-copy**: `toStatusSnapshot` references `PlotMetrics.UpstreamCounts` / `DownstreamCounts` directly. This is safe for `scarecrow.MetricsWatcher` (it returns a new map each time), but if a custom `MetricsView` reuses dictionaries, the sink must copy them itself before retaining the data — `PushWatcher.pushSnapshotAt` already does this via `copySnapshot`.
- **Zero-value time semantics**: the `time.Time` zero values of `StartTime` / `EndTime` are projected as `0`, not as a negative Unix timestamp; the front end uses this to tell "not started/not ended".
- **Status is an integer snapshot**: `String()` of `PlotStatus` (`not_started`/`running`/`stopped`) never appears in the payload; the web side relies on the `0/1/2` numbers.
- **Errors are not wrapped**: `OnTick` returns exactly the sink's error, so `errors.Is` matches directly; if an upper layer needs extra context, it must wrap it itself.
- **No local deduplication**: equivalent snapshots enter the queue on every tick, and the server deduplicates by content (which is why `elapsed_time` is deliberately excluded). A high-frequency period + a large graph amplifies queue pressure; `DefaultPushBufferSize = 64` is the only buffer limit.
- **Node set is not validated**: an empty graph pushes an empty `snapshot: {}`; a Plot that has never started may also appear in `MetricsWatcher` because it received seeds/upstream events (with status `0`).
- **`GetPlots` cost runs in the tick goroutine**: the time spent holding the metrics read lock is proportional to the number of nodes; if metrics are written frequently (many seed events), it competes for the same `RWMutex` with snapshot reads.
