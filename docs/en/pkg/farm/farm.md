# pkg/farm/farm.go

> 📅 Last Updated: 2026/11/26

## Purpose

The `farm` package is CelestialGrow's "graph-level" scheduler. `farm.go` provides the `Farm` type, which organizes multiple `plot.PlotNode` nodes into a static directed graph and handles:

- node registration and name uniqueness validation
- group-to-group (hyper-edge) style fully connected edge creation
- Farm-level watcher registration, receiving both Farm events and all Plot events through a **hierarchical Hub**
- Farm-level ticker registration: collecting each Plot's ticker Hub into the Farm's ticker Hub, and allowing users to append tickers
- runtime resource assembly (`assembly.StartLocalResources` / `StartReportResources` uniformly create / start / reclaim watchers, the ticker Hub, and the clock)
- rendering the graph structure before running (`getStructureList` → `RenderStructureList`) into `FarmStartEvent`
- source node detection (`SourceNodes`), initial seed injection, termination signal injection, and waiting for exit
- remote reporting mode: filling in the session identifier and handing the Farm to the injection ticker as a `reporter.InjectionTarget`

`Farm` only manages the orchestration layer and does not care about a Plot's generic seed / fruit types — this is erased by the `plot.PlotNode` interface. The graph algorithms themselves live in `graph.go` and structure rendering in `render.go` in the same directory; see [`graph.md`](./graph.md) and [`render.md`](./render.md) for details.

## Core Objects

### The `Farm` struct

```go
type Farm struct {
    name        string
    plots       map[string]plot.PlotNode
    sourceNodes []string
    *OrderGraph

    eventClient runtime.EventClient
    watcherHub  *scarecrow.Hub
    tickerHub   *metronome.Hub
}
```

| Field | Purpose |
| --- | --- |
| `name` | The Farm name, written into `FarmStartEvent` / `FarmEndEvent` and logs; also used to generate the default `SessionID` when the reporting session identifier is empty |
| `plots` | The node table indexed by plot name, used for registration validation and lookup |
| `sourceNodes` | The "source node representatives" computed once when `Run` / `Start` begins, one per Source SCC |
| `*OrderGraph` | The embedded directed graph recording the edges between plots, used for topology and SCC analysis |
| `eventClient` | The shared runtime event client, injected into each plot through `AddPlot` |
| `watcherHub` | The Farm-level watcher Hub; it is also the nested Hub registered into each Plot, receiving both Farm and Plot events |
| `tickerHub` | The Farm-level ticker Hub; before running, each Plot's ticker Hub is collected into it, forming the whole ticker tree |

> `Farm` does not hold spout / inlet / persistence watchers or a clock. Watchers and the clock are assembled by `pkg/assembly`, and Farm exposes only the watcher Hub and the ticker Hub. `NewFarm` panics directly if creating `metronome.Hub` fails.

### The `PlotNode` interface contract

`Farm` is decoupled from concrete nodes through the `plot.PlotNode` interface. That interface is defined in `pkg/plot/plot.go`, and `Farm` actually calls the following methods:

| Method | Purpose |
| --- | --- |
| `GetName() string` | Unique identifier and edge matching |
| `GetState() int32` | Reads the node state (`0=idle` / `1=running` / `2=done`) |
| `GetMeta() scarecrow.NodeMeta` | Returns build-time meta information (type name, tender count, etc.), aggregated by `getNodeMeta` into `FarmStartEvent.NodeMeta` |
| `GetSeedChanAny() any` | Exposes `seedChan` as `any`, for upstream `ConnectTo` to type-assert |
| `ConnectTo(next PlotNode) error` | Validates type compatibility, registers the downstream yield channel and the upstream name; returns an error on type mismatch |
| `AddUpstream(name string)` | Called by upstream `ConnectTo`, registers the upstream name for seal aggregation |
| `AddWatcher(listener scarecrow.Watcher) error` | Registers a watcher; Farm registers its `watcherHub` here at startup |
| `AddTicker(ticker metronome.Ticker) error` | Registers a ticker; `Farm.AddTicker` forwards directly to it |
| `GetTickerHub() *metronome.Hub` | Returns the Plot's own ticker Hub, which `injectTickerHubs` collects into the Farm's ticker Hub |
| `SetEventClient(runtime.EventClient)` | Injects the same event client uniformly during `AddPlot` |
| `StartAsync()` / `WaitAsync()` | Asynchronous lifecycle control |
| `SeedAny(seed any) error` | Injects an initial seed (called once per seed by `InjectTasks`) |
| `Seal()` | Injects an external termination signal (called by `InjectTerminations`) |

## Public Symbols

| Symbol | Signature | Purpose |
| --- | --- | --- |
| `NewFarm` | `func NewFarm(name string) *Farm` | Constructs a Farm: initializes the plot table, an empty graph, a local event client, the watcher Hub, and the ticker Hub |
| `PlotCount` | `func (f *Farm) PlotCount() int` | Returns the number of registered plots |
| `HasPlot` | `func (f *Farm) HasPlot(name string) bool` | Reports whether the named plot is registered |
| `GetPlot` | `func (f *Farm) GetPlot(name string) (plot.PlotNode, bool)` | Gets a plot by name; `ok` indicates whether it exists |
| `AddPlot` | `func (f *Farm) AddPlot(plots ...plot.PlotNode) error` | Registers one or more plots, requiring non-nil, non-empty and unique names |
| `Connect` | `func (f *Farm) Connect(fromPlots, toPlots []plot.PlotNode) error` | Creates Cartesian-product style connections between the source group and the target group |
| `AddWatcher` | `func (f *Farm) AddWatcher(watcher scarecrow.Watcher) error` | Registers a Farm-level watcher (receives both Farm and Plot events) |
| `AddTicker` | `func (f *Farm) AddTicker(ticker metronome.Ticker) error` | Registers a Farm-level ticker, receiving ticks for the whole Farm run |
| `Run` | `func (f *Farm) Run(inputs map[string][]any, logLevel string, report *reporter.Config) error` | Runs synchronously: start → inject seeds → seal sources → wait for exit; `report` is the remote reporting config |
| `Start` | `func (f *Farm) Start(logLevel string, report *reporter.Config) error` | Runs synchronously without any injection, driven externally; `report` is the remote reporting config |
| `InjectTasks` | `func (f *Farm) InjectTasks(tasks map[string][]any) error` | Injects seeds into the given Plots; may be called at runtime; also satisfies `reporter.InjectionTarget` |
| `InjectTerminations` | `func (f *Farm) InjectTerminations(nodes []string) error` | Injects termination signals into the given Plots; may be called at runtime; also satisfies `reporter.InjectionTarget` |

> A `nil` `report` means a local run, assembling local resources only; a non-`nil` `report` additionally assembles the push watcher, the snapshot ticker, and the remote injection ticker. `URL` of `reporter.Config` is the reporting service address; when `SessionID` is left empty, `normalizeReport` generates it as `<Farm name>@<UnixNano>`, and `SnapshotPeriod` / `InjectionPeriod` are measured in ticks (0 means the default period).

### Semantic differences between `Run` / `Start` / `Inject*`

| Method | Starts Plots | Injects seeds | Injects termination signals | Blocks | Typical scenario |
| --- | :---: | :---: | :---: | :---: | --- |
| `Run(inputs, logLevel, report)` | ✅ | ✅ `inputs` | ✅ `sourceNodes` | Yes | One-shot batch processing (remote reporting optional) |
| `Start(logLevel, report)` | ✅ | ❌ | ❌ | Yes | External driving (e.g. reporter, interactive injection) |
| `InjectTasks(tasks)` | ❌ | ✅ `tasks` | ❌ | No | Appending seeds at runtime |
| `InjectTerminations(nodes)` | ❌ | ❌ | ✅ `nodes` | No | Terminating the given Plots at runtime |

- `Run` = `validateRunInputs` + `start(logLevel, report, prepare)`, where `prepare` executes `InjectTasks(inputs)` and then `InjectTerminations(f.sourceNodes)`.
- `Start` performs no input validation and calls `start(logLevel, report, nil)` directly; **`Start` will not return if no external termination signal is ever injected**. In reporting mode, `InjectionTicker` can hand the tasks and termination signals submitted by the web side back to the Farm driven by `Start`.
- `InjectTasks` / `InjectTerminations` only operate on registered Plots; they do not trigger resource assembly and do not change `sourceNodes`.

> The log level is no longer decided by `NewFarm`: `logLevel` is a parameter of every run (`Run` / `Start`), and an empty string is treated as `INFO`.

## Key Flows

### The full `AddPlot → Connect → Run` chain

```mermaid
sequenceDiagram
    autonumber
    participant U as Caller
    participant F as Farm
    participant A as assembly
    participant H as Farm watcherHub
    participant T as Farm tickerHub
    participant P as PlotNode

    U->>F: NewFarm(name)
    Note over F: initialize plots table / OrderGraph / watcherHub / tickerHub

    U->>F: AddPlot(p1, p2, ...)
    loop each plot
        F->>F: validate non-nil / non-empty / unique name
        F->>F: AddNode(name)
        F->>P: SetEventClient(eventClient)
    end

    U->>F: Connect(froms, tos)
    F->>F: uniquePlots dedup + nil filtering
    F->>F: requireRegistered(froms / tos)
    loop from × to Cartesian product
        F->>P: from.ConnectTo(to)
        Note over P: register yieldChans and upstreams
        F->>F: AddEdge(from, to)
    end

    U->>F: Run(inputs, logLevel, report)
    F->>F: validateRunInputs(inputs)
    F->>F: sourceNodes = SourceNodes(OrderGraph)
    F->>F: normalizeReport(report)
    F->>F: injectTickerHubs()
    alt report == nil
        F->>A: StartLocalResources(watcherHub, tickerHub, logLevel, 1s)
    else report != nil
        F->>A: StartReportResources(watcherHub, tickerHub, F, logLevel, 1s, report)
    end
    A->>H: AddWatcher(Metrics / Lifecycle / Log, plus Push in reporting mode)
    A->>T: register SnapshotTicker / InjectionTicker (reporting mode only)
    F->>H: emit(FarmStartEvent)
    loop each plot
        F->>P: AddWatcher(watcherHub)
    end
    loop each plot
        F->>P: StartAsync()
    end
    F->>F: InjectTasks(inputs)
    F->>F: InjectTerminations(sourceNodes)
    Note over F: injection failed → fallback InjectTerminations(sourceNodes)
    loop each plot
        F->>P: WaitAsync()
    end
    F->>H: emit(FarmEndEvent)
    F->>A: stop()
```

### Internal `start` flow (shared by `Run` / `Start`)

1. Records `startTime`.
2. `normalizeReport(report)`: returns `report` as-is when it is `nil` or already has a non-empty `SessionID`; otherwise copies it and fills in `<Farm name>@<UnixNano>`.
3. `injectTickerHubs()`: registers each Plot's `GetTickerHub()` into the Farm's `tickerHub`, skipping those already registered. Returns immediately on failure, before any Plot has started.
4. Assembles runtime resources according to `report`:
   - `nil` → `assembly.StartLocalResources(watcherHub, tickerHub, logLevel, metronome.DefaultInterval)`;
   - non-`nil` → `assembly.StartReportResources(watcherHub, tickerHub, f, logLevel, metronome.DefaultInterval, report)`. The Farm itself implements `InjectTasks` / `InjectTerminations`, so it can serve as a `reporter.InjectionTarget`.

   The resources include `MetricsWatcher`, `LifecycleWatcher`, `LogWatcher` (plus `PushWatcher`, `SnapshotTicker`, `InjectionTicker` in reporting mode) and the tick clock. Returns immediately on failure, before any Plot has started.
5. `defer stop()`: when the run ends, stops and flushes in reverse order "clock → tickerHub → watcherHub"; `StopFunc` is idempotent.
6. Emits `scarecrow.FarmStartEvent`, containing `Farm`, `IsDAG`, `Plots`, `Edges`, `SourcePlots`, `NodeMeta`, `Structure`, `StartTime`.
7. `defer` emits `scarecrow.FarmEndEvent` (`Elapsed` is `time.Since(startTime)`).
8. For each plot, calls `plot.AddWatcher(f.watcherHub)` — **the Farm watcher Hub is registered into every Plot as a nested Hub**.
9. For each plot, calls `StartAsync()`.
10. Calls `prepare()` (the injection logic for `Run`, `nil` for `Start`).
11. If `prepare` returns an error, still performs one `InjectTerminations(f.sourceNodes)` as a fallback (ignoring its error), so the graph does not wait forever for a termination signal that will never arrive.
12. For each plot, calls `WaitAsync()`, and returns `prepareErr` after all of them have exited.

> `start` returns only assembly errors or injection errors; cultivation failures inside a Plot enter the watcher system as `SeedWitherEvent` and will not make `Run` / `Start` return an error.

### Hierarchical Hub dispatch

```mermaid
flowchart LR
    subgraph PlotHub[Plot internal Hub]
        PE[Plot.watcherHub.OnEvent] --> PH[watcherHub]
    end
    PH -->|registered via AddWatcher| FH[Farm watcherHub]
    FH --> M[MetricsWatcher]
    FH --> L[LifecycleWatcher]
    FH --> G[LogWatcher]
    FH --> PU[PushWatcher<br/>reporting mode only]
    FH --> W[user AddWatcher]
    FE[Farm.emit] --> FH

    style FH fill:#eef
```

- Plot events (`PlotStart`, `SeedRipen`, etc.) are dispatched by the Plot's own Hub to registered watchers; the Farm watcher Hub is one of them, so they continue to propagate upward.
- Farm events (`FarmStartEvent` / `FarmEndEvent`) go directly into the Farm watcher Hub and are **not** dispatched down to Plots.
- `scarecrow.Find` can look up watchers along nested Hubs; this is exactly how `plot.Harvest()` finds `LifecycleWatcher` on the Plot Hub → Farm Hub chain.
- The copy-on-write semantics of `Hub` allow watchers to be appended at runtime, but only for subsequent events; registering the same Hub twice causes duplicate dispatch.

The ticker side is likewise a tree:

```mermaid
flowchart LR
    C[metronome.Clock<br/>DefaultInterval=1s] --> FT[Farm tickerHub]
    FT --> PT1[Plot A tickerHub]
    FT --> PT2[Plot B tickerHub]
    FT --> ST[SnapshotTicker<br/>reporting mode only]
    FT --> IT[InjectionTicker<br/>reporting mode only]
    FT --> UT[user AddTicker]
```

- `injectTickerHubs` only attaches the Plots' ticker Hubs under the Farm `tickerHub`; the Farm itself does not create Plot tickers.
- `assembly` registers the snapshot / injection tickers before starting `tickerHub`, and creates the single clock that drives the whole tree.
- Plot tickers and Farm tickers both filter ticks by their own `TickPeriod`; the period semantics are defined by `pkg/metronome`.

### Computing `sourceNodes`

`Run` and `Start` both execute `f.sourceNodes = SourceNodes(f.OrderGraph)` before starting:

1. `TarjanSCC` computes the strongly connected components;
2. computes the Source SCCs with in-degree 0 in the condensation graph (`SourceSCCs`);
3. takes `scc[0]` of each Source SCC as its representative node (`SourceNodes`).

This way, even if a source lies inside a strongly connected cycle, only one representative node receives the termination signal, avoiding duplicate seals. Isolated nodes (added by `AddPlot` but never connected) also become Source SCCs, so they receive termination signals too.

## Error Scenarios

### `AddPlot`

- Any plot is `nil` → `plot is nil`
- The name is empty → `plot name cannot be empty`
- The name is duplicated → `plot %q already exists`

The first error is returned immediately; plots already added successfully are **not rolled back**.

### `Connect`

- `fromPlots` is empty after deduplication → `from plots cannot be empty`
- `toPlots` is empty after deduplication → `to plots cannot be empty`
- Any plot is `nil` or unregistered → `plot is nil` / `plot %q is not registered in farm`
- The type assertion in `from.ConnectTo(to)` fails → passes through `plot %q yield type is incompatible with plot %q seed type`

> ⚠️ **Partial failure is not undone**: once the inner loop fails, `Connect` returns immediately and connections already established (`yieldChans`, `upstreams`, graph edges) are kept. On a later `Connect`, duplicate edges are ignored at the graph level, but the mappings inside Plots keep the old state.

### `AddWatcher` / `AddTicker`

- `watcher` is `nil` → `scarecrow: cannot add nil listener`
- Would create a Hub cycle → `scarecrow: cyclic hub registration rejected: ...` / `scarecrow: cannot add a hub to itself`
- `ticker` is `nil` → `metronome: cannot add nil ticker`
- The ticker's tick period is invalid → `metronome: tick period must be positive ...`
- The ticker is registered twice or would create a Hub cycle → `metronome: ticker already registered` / `metronome: cyclic hub registration rejected: ...`

### `Run` / `Start`

| Stage | Error source | Description |
| --- | --- | --- |
| Validation (`Run` only) | `validateRunInputs` | An unregistered plot appears in `inputs` → `plot %q is not registered in farm`; at this point **no Plot has been started** |
| Ticker assembly | `injectTickerHubs` | Registering a Plot ticker Hub fails (nil / duplicate / cycle); Plots have not been started yet |
| Resource assembly | `assembly.StartLocalResources` / `StartReportResources` | Hub or ticker Hub is nil, watcher/ticker startup fails, invalid `report` config; Plots have not been started yet |
| Registration | `plot.AddWatcher(f.watcherHub)` | Hub cycle or nil watcher; returns before `StartAsync` |
| Injection | `InjectTasks` | Unregistered plot, `SeedAny` type assertion failure (`plot %q seed type mismatch: got %T`) |
| Injection | `InjectTerminations` | Unregistered plot |
| Wait | `WaitAsync` | Does not return an error |

### `InjectTasks`

- First runs `validateRunInputs` on all keys: if any target is unregistered → returns an error and **injects no seed at all**.
- Then calls `SeedAny` for each seed in map iteration order; if one seed has a mismatched type, it returns immediately and seeds already injected **cannot be withdrawn**.

### `InjectTerminations`

- First validates that all names are registered; if any is unregistered → returns an error and **injects no termination signal at all**.
- After validation passes, calls `Seal()` one by one to inject external termination signals (`Source == "__input__"`), with strong termination semantics: it no longer waits for upstream seals that have not yet arrived.
- After a termination signal is injected into a Plot, any seed later injected into it will not be processed.

## Important Details

### Group-to-group Cartesian product

`Connect(froms, tos)` first applies `uniquePlots` (deduplication + `nil` filtering) to both sides, then creates `len(froms) × len(tos)` edges:

```go
farm.Connect([]plot.PlotNode{root}, []plot.PlotNode{midA, midB})
// 等价于 root → midA 与 root → midB 两条独立边
```

Edges are created only between the source group and the target group; nodes inside the same group are not connected to each other.

### Termination fallback on injection failure

In `start`, after `prepare()` fails, `InjectTerminations(f.sourceNodes)` is still called:

- avoiding `WaitAsync` blocking forever on a graph that has no termination signal;
- the fallback error is explicitly ignored (`_ =`), and the returned value is still the original `prepareErr`;
- the fallback only terminates `sourceNodes`; downstream nodes exit through the normal propagation path after receiving the seal signal.

### Events and logs

- `Farm` emits only two events: `FarmStartEvent` and `FarmEndEvent`, defined in `pkg/scarecrow/event.go`.
- The `Structure` field comes from `getStructureList()`, i.e. `RenderStructureList(f.Nodes(), f.OutEdges(), f.sourceNodes)`; `NodeMeta` comes from `getNodeMeta()`, aggregating `GetMeta()` by Plot name.
- `persist.LogWatcher` outputs the structure line by line when consuming `FarmStartEvent`, and outputs the elapsed time when consuming `FarmEndEvent`.
- In reporting mode, `reporter.PushWatcher` pushes the graph metadata when consuming `FarmStartEvent`, pushes errors when consuming `SeedWitherEvent`, and on `FarmEndEvent` first pushes a supplementary final-state snapshot before notifying the session to shut down; periodic snapshots are driven by `SnapshotTicker`.
- `Farm` itself does not write to the database; lifecycle, logging, and remote reporting are all handled by the watchers registered by `assembly`.

### Collaboration with `pkg/runtime`

`eventClient` is injected during `AddPlot`, and all plots share the same event ID namespace, guaranteeing that the cross-plot seed → fruit → seal event chain is traceable.

### Session identifier and reporting

- `normalizeReport` only fills in an empty `SessionID` and does not modify the config passed by the caller (the struct is copied first).
- The generated form is `fmt.Sprintf("%s@%d", f.name, time.Now().UnixNano())`, ensuring that multiple runs of the same Farm do not overwrite each other on the server side.
- `StartReportResources` takes the Farm as the injection target, periodically pulls `/api/pull_injection`, and hands `tasks` / `terminations` back to `InjectTasks` / `InjectTerminations`.

### Concurrency control

- `Run` / `Start` are **synchronous** calls: they call `StartAsync` in turn and then wait for all plots to exit through `WaitAsync`.
- `StartAsync` uses `sync.WaitGroup.Go` (the Go 1.25 style) internally to start each plot's `sprout` scheduler.
- `InjectTasks` / `InjectTerminations` are designed to be called by other goroutines while `Start` is blocking; the `plots` table of `Farm` itself is not locked, so **the registration / connection phase must not be concurrent with the running phase**.
- `watcherHub` / `tickerHub` are themselves thread-safe, but `injectTickerHubs` is only called once before startup.

## Usage Examples

### Batch processing: `Run`

```go
package main

import (
    "fmt"

    "github.com/Mr-xiaotian/CelestialGrow/pkg/farm"
    "github.com/Mr-xiaotian/CelestialGrow/pkg/plot"
)

func main() {
    double := plot.NewSinglePlot("double", func(seed int) (int, error) {
        return seed * 2, nil
    }, plot.WithTenders(2))

    format := plot.NewSinglePlot("format", func(seed int) (string, error) {
        return fmt.Sprintf("result=%d", seed), nil
    })

    f := farm.NewFarm("demo_farm")
    if err := f.AddPlot(double, format); err != nil {
        panic(err)
    }
    if err := f.Connect([]plot.PlotNode{double}, []plot.PlotNode{format}); err != nil {
        panic(err)
    }

    if err := f.Run(map[string][]any{
        "double": {1, 2, 3, 4},
    }, "INFO", nil); err != nil {
        panic(err)
    }
}
```

To push the run state to `celestialflow-web`, pass a reporting config (importing `github.com/Mr-xiaotian/CelestialGrow/pkg/reporter` additionally); leave `SessionID` empty to generate it automatically:

```go
if err := f.Run(map[string][]any{
    "double": {1, 2, 3, 4},
}, "INFO", &reporter.Config{
    URL: "http://127.0.0.1:5005",
}); err != nil {
    panic(err)
}
```

### Externally driven: `Start` + `InjectTasks` / `InjectTerminations`

```go
package main

import (
    "fmt"

    "github.com/Mr-xiaotian/CelestialGrow/pkg/farm"
    "github.com/Mr-xiaotian/CelestialGrow/pkg/plot"
    "github.com/Mr-xiaotian/CelestialGrow/pkg/scarecrow"
)

func main() {
    root := plot.NewSinglePlot("root", func(seed int) (int, error) {
        return seed * 2, nil
    }, plot.WithTenders(2), plot.WithChanSize(8))

    f := farm.NewFarm("external_driven")
    if err := f.AddPlot(root); err != nil {
        panic(err)
    }

    if err := f.AddWatcher(scarecrow.WatcherFunc(func(event scarecrow.Event) error {
        switch ev := event.(type) {
        case scarecrow.PlotStartEvent:
            fmt.Println("plot started:", ev.Plot)
        case scarecrow.FarmEndEvent:
            fmt.Println("farm done in", ev.Elapsed)
        }
        return nil
    })); err != nil {
        panic(err)
    }

    go func() {
        for i := 0; i < 3; i++ {
            if err := f.InjectTasks(map[string][]any{"root": {i}}); err != nil {
                panic(err)
            }
        }
        if err := f.InjectTerminations([]string{"root"}); err != nil {
            panic(err)
        }
    }()

    if err := f.Start("INFO", nil); err != nil {
        panic(err)
    }
}
```

> `Start` blocks until all Plots exit; you must ensure that some external goroutine (or signal handling logic) eventually calls `InjectTerminations`, otherwise the call will never return. When a non-nil `report` is passed, the remote injection ticker also drives `InjectTasks` / `InjectTerminations`.

### Reading results after the run ends

```go
records, err := root.Harvest()
if err != nil {
    panic(err)
}
for _, record := range records {
    fmt.Println(record.Status) // 例如 ripen
}
```

`Harvest` relies on the Farm watcher Hub already being registered into the Plot Hub, so that it can find `LifecycleWatcher` along the nested Hubs.

## Notes

- **Not re-entrant**: the same `Farm` instance does not support concurrent or repeated `Run` / `Start`. Each `start` registers the `watcherHub` into every Plot again, causing duplicate event dispatch; the Plot `state` also stays at `done`.
- **Injection order**: `InjectTasks` relies on `plots[name]` being registered. Do not modify the registry before runtime injection.
- **Termination is irreversible**: after `Seal()` the Plot's `sprout` no longer consumes seeds; `InjectTasks` does not report an error, but seeds will only linger in `seedChan`, and once the buffer is full the call blocks.
- **Graph vs. node set**: nodes in `OrderGraph` may be auto-completed by `AddEdge`, but on the normal path `AddPlot` has registered all nodes; `sourceNodes` uses the graph view rather than the `plots` view.
- **Log level**: for `logLevel` values see the level table in `pkg/persist`; unknown values are treated as `INFO`.
- **Reporting and injection**: when `report == nil`, `PushWatcher` / `SnapshotTicker` / `InjectionTicker` are not assembled and no web service is accessed; when `report != nil`, `URL` must be a valid `http(s)` address, otherwise the resource assembly stage returns an error.
- **Test coverage**: `farm_connect_test.go`, `farm_start_test.go`, `farm_structure_test.go`, `farm_route_test.go`, `farm_split_test.go`, `farm_inject_test.go`, `farm_harvest_test.go`, `farm_report_test.go`, `graph_test.go`, and `render_test.go` cover registration / connection, the linear `Run` flow, non-trivial topologies, routing / splitting, external injection, `Harvest`, remote reporting, graph algorithms and structure rendering.
