# pkg/scarecrow/metrics.go

> 📅 Last Updated: 2026/11/13

## Purpose

`pkg/scarecrow/metrics.go` **rebuilds the runtime metrics of every Plot** from the event stream: status, start/end times, input sources, ripened/withered/pruned counts, and the number of pending items. `MetricsWatcher` is a pure in-memory accumulator (no child goroutines, no I/O); once registered with a `Hub` it provides the data behind `Plot` queries such as `GetSeedNum` / `GetFruitNum` / `IsFinish`, and it is also the basis `pkg/persist` log observers use to complete their counts.

## Core Objects

### `PlotStatus`

```go
type PlotStatus int

const (
    PlotNotStarted PlotStatus = iota // 尚未收到 PlotStartEvent
    PlotRunning                      // 正在运行
    PlotStopped                      // 已结束
)
```

| Constant | Value | `String()` output |
|------|---|-----------------|
| `PlotNotStarted` | 0 | `not_started` |
| `PlotRunning` | 1 | `running` |
| `PlotStopped` | 2 | `stopped` |
| Other values | — | `not_started` (the `default` branch of `String()`) |

> `PlotStatus` shares the numeric values of `plot.GetState()`'s `0=idle / 1=running / 2=done`, but they are two independent types; do not assign one to the other.

### `PlotMetrics`

```go
type PlotMetrics struct {
    Plot   string
    Status PlotStatus

    StartTime time.Time
    EndTime   time.Time

    ExternalInput int
    UpstreamInput int
    InputTotal    int

    Succeeded int
    Failed    int
    Skipped   int
    Processed int
    Pending   int

    UpstreamCounts   map[string]int
    DownstreamCounts map[string]int
}
```

| Field | Meaning | Computation |
|------|------|----------|
| `Plot` | Plot name | Snapshot key |
| `Status` | Runtime status | Updated by `PlotStartEvent` / `PlotEndEvent`, initially `PlotNotStarted` |
| `StartTime` / `EndTime` | Start/end times | Taken directly from the event's `StartTime` / `EndTime`; stay zero until the event happens |
| `ExternalInput` | Number of externally injected seeds | +1 when `SeedInputEvent.From == ""` |
| `UpstreamInput` | Number of upstream-delivered seeds | Sum of all values in `UpstreamCounts` |
| `InputTotal` | Total inputs | `ExternalInput + UpstreamInput` |
| `Succeeded` | Ripened count | +1 for each `SeedRipenEvent` |
| `Failed` | Withered count | +1 for each `SeedWitherEvent` |
| `Skipped` | Pruned count | +1 for each `SeedPruneEvent` |
| `Processed` | Processed count | `Succeeded + Failed + Skipped` |
| `Pending` | Input with no result yet | `InputTotal - Processed`, **clamped to 0 when negative** |
| `UpstreamCounts` | Source Plot → delivery count | Accumulated on the receiving side for each upstream delivery |
| `DownstreamCounts` | Target Plot → produced count | Accumulated on the **source** side for each upstream delivery |

`PlotMetrics` is a **value snapshot**: `UpstreamCounts` / `DownstreamCounts` are reallocated and copied on every `GetPlot` / `GetPlots`, so caller modifications do not affect `MetricsWatcher`'s internal state and there is no data race.

### `MetricsWatcher`

```go
type MetricsWatcher struct {
    mu    sync.RWMutex
    plots map[string]*plotMetrics
}

func NewMetricsWatcher() *MetricsWatcher
```

| Member | Description |
|------|------|
| `mu` | RWMutex guarding `plots` and every `plotMetrics`; `OnEvent` takes the write lock, read APIs take the read lock |
| `plots` | Plot name → internal mutable cell; created on demand by the first related event (idempotent) |
| `plot(name)` (unexported) | Creates the cell on demand; initializes `plots` first when it is `nil`, so a **zero-value `MetricsWatcher` is usable too** |
| `snapshot(name)` (unexported) | Copies the maps and computes derived fields, assuming the caller holds the lock |

Internal cell `plotMetrics` versus the snapshot: the cell uses private fields (`external` / `success` / `fail` / `skip` / `upstream` / `downstream` / `status` / `start` / `end`) and only exposes the snapshot struct.

### Read APIs

| Method | Signature | Behavior |
|------|------|------|
| `GetPlot` | `func (m *MetricsWatcher) GetPlot(name string) (PlotMetrics, bool)` | Returns the zero value and `false` when unregistered; returns a copied snapshot when registered |
| `GetPlots` | `func (m *MetricsWatcher) GetPlots() map[string]PlotMetrics` | Returns a new map of all registered Plots; returns an empty (non-nil) map when there is no data |

## Key Event Mappings

| Event | Effect on metrics |
|------|--------------|
| `PlotStartEvent` | `status = PlotRunning`, `start = ev.StartTime` |
| `PlotEndEvent` | `status = PlotStopped`, `end = ev.EndTime` |
| `SeedInputEvent` (`From == ""`) | `external++` |
| `SeedInputEvent` (`From != ""`) | Receiver `upstream[From]++`; and `plot(From).downstream[Plot]++` |
| `SeedRipenEvent` | `success++` |
| `SeedWitherEvent` | `fail++` |
| `SeedPruneEvent` | `skip++` |
| `FarmStartEvent` / `FarmEndEvent` / `SeedReplantEvent` / `SealInputEvent` / `SealMergeEvent` | Ignored, do not affect Plot metrics |

```mermaid
flowchart LR
    PS[PlotStartEvent] --> C[plotMetrics cell]
    PE[PlotEndEvent] --> C
    SI[SeedInputEvent] -->|From empty| EXT[external++]
    SI -->|From non-empty| UP[receiver upstream[From]++<br/>source downstream[Plot]++]
    SR[SeedRipenEvent] --> SUC[success++]
    SW[SeedWitherEvent] --> FAIL[fail++]
    SP[SeedPruneEvent] --> SKIP[skip++]
    C --> SNAP[snapshot<br/>InputTotal/Processed/Pending]
    EXT --> SNAP
    UP --> SNAP
    SUC --> SNAP
    FAIL --> SNAP
    SKIP --> SNAP
```

## Important Details

- **Cells appear with events**: any event carrying a `Plot` name creates the corresponding cell; `SeedInputEvent` also creates a source cell for `From`. `GetPlots()` may therefore contain Plots that "never started and appear only in lineage counts", with `Status` `PlotNotStarted` and `InputTotal` 0.
- **Status takes no part in derivation**: even when `Status == PlotStopped`, seed events arriving later keep accumulating; `MetricsWatcher` never decides to "discard after the end".
- **`Pending` has a lower bound of 0**: when the event stream is incomplete (replaying results without inputs, say), `Processed > InputTotal` and the difference is clamped to 0, never negative.
- **Retries do not count**: `SeedReplantEvent` is ignored, so `Succeeded + Failed + Skipped` still corresponds one-to-one with inputs and `Pending` is not polluted by retry counts.
- **Concurrency safe**: `OnEvent` holds the write lock throughout, `GetPlot` / `GetPlots` hold the read lock; concurrent events from several Plots are therefore serialized, with reads and writes sharing one `RWMutex`.
- **Snapshot isolation**: the new maps returned by `GetPlot` / `GetPlots` are one level of deep copy (value copy + map rebuild), so callers may keep them safely and read them from other goroutines.
- **Metrics exclude `Elapsed`**: duration does not enter `PlotMetrics`; compute `EndTime.Sub(StartTime)` yourself when needed, or consume `PlotEndEvent.Elapsed` directly.

## Usage Examples

### Feeding Events Directly

```go
package main

import (
    "fmt"
    "time"

    "github.com/Mr-xiaotian/CelestialGrow/pkg/scarecrow"
)

func main() {
    metrics := scarecrow.NewMetricsWatcher()
    now := time.Now()

    _ = metrics.OnEvent(scarecrow.PlotStartEvent{Plot: "harvester", Tenders: 2, StartTime: now})
    _ = metrics.OnEvent(scarecrow.SeedInputEvent{Plot: "harvester", SeedID: 1, Time: now})
    _ = metrics.OnEvent(scarecrow.SeedRipenEvent{Plot: "harvester", SeedID: 1, FruitID: 2, Time: now})
    _ = metrics.OnEvent(scarecrow.SeedWitherEvent{Plot: "harvester", SeedID: 3, WeedID: 4, Time: now})

    snapshot, ok := metrics.GetPlot("harvester")
    if !ok {
        panic("plot not registered")
    }
    fmt.Println(snapshot.Status)                       // running
    fmt.Println(snapshot.InputTotal, snapshot.Pending) // 2 0
    fmt.Println(snapshot.Succeeded, snapshot.Failed)   // 1 1
}
```

### Registering with a Hub and Searching It

```go
package main

import (
    "fmt"

    "github.com/Mr-xiaotian/CelestialGrow/pkg/scarecrow"
)

func main() {
    hub := scarecrow.NewHub()
    _ = hub.AddWatcher(scarecrow.NewMetricsWatcher())

    _ = hub.OnEvent(scarecrow.SeedInputEvent{Plot: "a", SeedID: 1})
    _ = hub.OnEvent(scarecrow.SeedInputEvent{Plot: "b", From: "a", SeedID: 2, Parents: []int{1}})

    metrics, ok := scarecrow.Find[*scarecrow.MetricsWatcher](hub)
    if !ok {
        panic("metrics watcher not found")
    }
    plots := metrics.GetPlots()
    fmt.Println(plots["a"].DownstreamCounts["b"]) // 1
    fmt.Println(plots["b"].UpstreamCounts["a"])   // 1
    fmt.Println(plots["b"].UpstreamInput)         // 1
}
```

`hub` may be a Plot's Hub or a hierarchy nesting a Farm Hub—`Find` looks at direct entries first and then at nested ones, which is exactly how `basePlot.currentMetrics` uses it.

## Notes

- `MetricsWatcher` only reflects **the events it actually receives**; if it is not registered with a Plot/Farm's Hub, it will not see those events.
- Do not register the same `MetricsWatcher` twice on the same Hub: events are processed twice and counts double.
- `Status` is driven only by Plot lifecycle events; when replaying events by hand without sending `PlotStartEvent`, `Status` stays `PlotNotStarted` forever.
- The read APIs return snapshots, not a live view; new events between two consecutive `GetPlot` calls do not show up in the earlier result.
- If `persist.LogWatcher` is to print accurate ripened/withered/pruned counts on `PlotEndEvent`, `MetricsWatcher` must be registered **earlier** than it (`pkg/assembly` already assembles them in that order).
- For cross-process or on-disk analysis use the lifecycle and log observers of `pkg/persist`, which reuse the counting semantics of this file.
