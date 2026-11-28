# pkg/scarecrow/hub.go

> 📅 Last Updated: 2026/11/26

## Purpose

`pkg/scarecrow/hub.go` implements the event dispatch center `Hub`: it fans out `Event` to all `Watcher`s **synchronously** in registration order, and isolates each observer's `error` and `panic`, reporting them to a configurable `ErrorHandler`. `Hub` itself implements `Watcher`, so it can be registered into another `Hub` to form hierarchical dispatch (Plot Hub → Farm Hub), and the generic function `Find` can search a hierarchy for an observer by type. It also implements `Lifecycle`, uniformly starting and stopping the registered observers (`Start` in order, `Stop` in reverse order).

## Core Objects

### `HubOption` and `WithErrorHandler`

```go
type HubOption func(*Hub)

func WithErrorHandler(handler func(error)) HubOption
```

| Option | Description |
|------|------|
| `WithErrorHandler(handler)` | Sets the handler used when an observer fails or panics. Passing `nil` is equivalent to not setting one, falling back to `defaultErrorHandler` |

### `Hub`

```go
type Hub struct {
    mu        sync.RWMutex
    listeners []Watcher
    onError   func(error)

    lifecycleMu sync.Mutex
    state       hubState
    lifecycle   []Lifecycle
}
```

| Field | Description |
|------|------|
| `mu` | RWMutex guarding `listeners`: writes (`AddWatcher`) take the write lock, dispatch only takes the read lock while taking a snapshot |
| `listeners` | Observer list, kept in registration order; duplicates are allowed |
| `onError` | Error handler; `nil` in a zero-value Hub, in which case `report` degrades to the default output |
| `lifecycleMu` | Mutex serializing `Start` / `Stop`, independent of `mu` |
| `state` | Lifecycle state (`hubIdle` / `hubRunning` / `hubStopped`) |
| `lifecycle` | `Lifecycle` children successfully started by `Start`, kept in start order so `Stop` can unwind them in reverse |

Lifecycle states:

```go
type hubState int

const (
    hubIdle    hubState = iota // 尚未启动
    hubRunning                 // 已启动，子项正在运行
    hubStopped                 // 已停止，不可再次启动
)
```

| State | Meaning |
|------|------|
| `hubIdle` | Initial state of a new or zero-value Hub; `Start` may run |
| `hubRunning` | Entered after a successful `Start`; a repeated `Start` returns `nil` directly |
| `hubStopped` | Entered after `Stop` or after a failed `Start`; another `Start` returns an error and `Stop` has no side effect |

### Constructor and Default Handler

| Symbol | Signature | Behavior |
|------|------|------|
| `NewHub` | `func NewHub(opts ...HubOption) *Hub` | Applies the options in order; fills in `defaultErrorHandler` when `onError` is still `nil` |
| `defaultErrorHandler` (unexported) | `func(err error)` | `log.Printf("scarecrow: %v", err)`, landing on standard error by default |

### `AddWatcher`

```go
func (h *Hub) AddWatcher(listener Watcher) error
```

| Error | Trigger |
|------|----------|
| `scarecrow: cannot add nil listener` | `listener == nil` (the interface value itself is nil) |
| `scarecrow: cannot add a hub to itself` | Registering a Hub into itself |
| `scarecrow: cyclic hub registration rejected: target hub already contains this hub` | The target Hub's nested hierarchy already contains the current Hub, so registering would create a cycle |

- **Duplicate registration** of the same instance is allowed; each dispatch calls it once more.
- Cycle detection (`rejectCycle`) runs **before locking**: it walks the candidate Hub depth first and rejects as soon as it meets the current Hub; a `seen` set prevents traversal from running forever on abnormal topologies.

### `Start` / `Stop`

```go
func (h *Hub) Start() error
func (h *Hub) Stop() error
```

`Hub` implements `Lifecycle` through these two methods and manages **only observers that implement `Lifecycle` when registered**; plain `Watcher`s are skipped and only take part in event dispatch. A nested `Hub` implements `Lifecycle` itself as well, so lifecycle recurses along the hierarchy.

| Method | Behavior | Error |
|------|------|------|
| `Start` | Takes a snapshot of `listeners` under `lifecycleMu` and calls each `Lifecycle.Start` in registration order; on success records the children and switches to `hubRunning`. Returns `nil` directly when already `hubRunning` | Returns `scarecrow: hub already stopped` when already `hubStopped`; when a child fails to start it is wrapped as `scarecrow: start %T: %w`, the children started in this call are stopped in **reverse order**, and the Hub switches to `hubStopped` (failure is final) |
| `Stop` | Calls `Stop` under `lifecycleMu` in the **reverse order** of startup, clears the children and switches to `hubStopped`; returns `nil` directly when not started or already stopped, and may be called repeatedly | Joins all child errors with `errors.Join`, each wrapped as `scarecrow: stop %T: %w` |

Additional semantics:

- If the rollback fails, `Start` returns the start error and the rollback error together via `errors.Join`.
- `Start` only starts the observers in the **current snapshot**; a `Lifecycle` added with `AddWatcher` after `Start` is not started automatically.
- `Stop` does not remove `listeners`: event dispatch and `Find` lookups keep working after stopping.
- Duplicate registration is allowed; when the same `Lifecycle` instance is registered twice, `Start` / `Stop` each call it twice as well.
- A zero-value `Hub` can call `Start` directly, and `onError` still falls back to the default handler.

### `OnEvent`

```go
func (h *Hub) OnEvent(event Event) error
```

- When `event == nil` it returns `nil` directly and calls no observer.
- Takes one snapshot of `listeners` under the read lock, then iterates **outside the lock**, so a watcher can safely register another watcher from inside.
- A single observer's error or panic is wrapped by `deliver` and handed to `report`; the remaining observers continue.
- **Always returns `nil`**: observer failures are consumed by the error handler and never flow back into the execution path (so the event emission sites in `plot` and `farm.emit` may ignore the return value).

### `Find`

```go
func Find[T Watcher](hub *Hub) (T, bool)
```

The generic parameter `T` is the target observer type (an interface or a concrete type such as `*MetricsWatcher`).

| Rule | Description |
|------|------|
| Search scope | `hub` and its nested Hubs (`Hub` itself is also a `Watcher`) |
| Priority | Matches **direct** observers first (in registration order), then recurses into nested Hubs |
| Not found | Returns the zero value of `T` and `false` |
| `hub == nil` | Returns the zero value and `false` immediately |
| Defense | Records visited Hubs in a `seen` set, preventing infinite recursion on abnormal topologies |

### `deliver` / `report` (unexported)

| Function | Behavior |
|------|------|
| `deliver` | `defer recover()`; wraps an observer's returned error as `scarecrow: %T failed on %T: %w` and a panic as `scarecrow: %T panicked on %T: %v` |
| `report` | Reads `h.onError`; uses `defaultErrorHandler` when it is `nil`; if the handler itself panics, uses `defaultErrorHandler` again to print `scarecrow: error handler panicked: %v` |

## Key Flows

### Dispatch

```mermaid
flowchart TD
    A[OnEvent event] --> B{event == nil?}
    B -->|yes| Z[return nil]
    B -->|no| C[RLock to snapshot listeners]
    C --> D[iterate in registration order after unlocking]
    D --> E[deliver: call watcher.OnEvent]
    E --> F{error returned?}
    F -->|yes| G[wrap failed on]
    F -->|no| H{panic occurred?}
    H -->|yes| I[wrap panicked on]
    G --> J[report -> ErrorHandler]
    I --> J
    H -->|no| K[next]
    J --> K
    K --> D
    D -->|iteration ends| Z
```

### Lifecycle

```mermaid
sequenceDiagram
    participant A as caller
    participant H as Hub
    participant L1 as Lifecycle A
    participant L2 as Lifecycle B (nested Hub)

    A->>H: Start()
    H->>L1: Start()
    H->>L2: Start() (recursively starts its children)
    Note over H: state = hubRunning
    A->>H: Start()
    H-->>A: nil (already started, no side effect)
    A->>H: Stop()
    H->>L2: Stop() (stops the last started first)
    H->>L1: Stop()
    Note over H: state = hubStopped
    A->>H: Stop()
    H-->>A: nil (already stopped, no side effect)
```

### Nested Topologies (actual project usage)

```mermaid
flowchart LR
    subgraph PlotA[Plot a Hub]
        PA[metrics / plot-level watcher]
        FA[Farm Hub]
    end
    subgraph PlotB[Plot b Hub]
        PB[metrics / plot-level watcher]
        FB[Farm Hub]
    end
    subgraph FarmHub[Farm Hub]
        FM[Farm-level watcher<br/>log / lifecycle]
    end

    PA --> FA
    PB --> FB
    FA --> FM
    FB --> FM
```

`Farm.start` registers the Farm Hub into every Plot Hub via `plot.AddWatcher(f.watcherHub)`, so Farm-level watchers receive events from all Plots in that Farm; starting from a Plot Hub, `Find` first checks direct observers and then enters the nested Farm Hub, so it can find a `MetricsWatcher` registered at Farm level (`basePlot.currentMetrics` relies on exactly this path).

## Important Details

- **Copy-on-write semantics**: `AddWatcher` does `append` inside the write lock, which can only replace the underlying array and never rewrites already published elements; `OnEvent` takes one slice snapshot under the read lock and iterates outside it. A snapshot taken during concurrent appends is therefore still complete, and iteration needs no lock.
- **Snapshot boundary**: a watcher registered after dispatch has started does not receive the current event, only later ones (the comment on `Farm.AddWatcher` also stresses "only later events are delivered").
- **Registration order is call order**: within one level, calls are strictly serial in `AddWatcher` order; a blocking watcher delays every later watcher.
- **Known race in cycle detection**: detection completes before locking, so the extreme concurrent case of two Hubs registering each other at the same time can still slip past it; the source comment explicitly says "building the Hub topology concurrently at runtime is not recommended". The `seen` inside `Find` is a fallback for abnormal topologies.
- **Zero value is usable**: `var h Hub` can call `AddWatcher` / `OnEvent` / `Start` directly; when `onError` is `nil`, `report` automatically falls back to the default handler.
- **Lifecycle and dispatch are decoupled**: `Start` / `Stop` only manage `Lifecycle` children; `OnEvent` does not check the Hub state, so events are still dispatched as usual after `Stop`.
- **Start/stop order is fixed**: `Start` follows registration order and `Stop` the reverse of startup order; a blocking child at the same level delays the later ones.
- **A start failure is final**: after `Start` fails, the children already started are rolled back and stopped, and the Hub enters `hubStopped` and cannot be started again (the source comment points out that a stopped child cannot be restarted, e.g. spout's channel is already closed).
- **The error handler is protected as well**: `report` wraps the handler call in `recover`, so a panicking handler cannot break `OnEvent`'s promise of "always returns nil".

## Usage Examples

### Basic Dispatch and Error Isolation

```go
package main

import (
    "errors"
    "fmt"

    "github.com/Mr-xiaotian/CelestialGrow/pkg/scarecrow"
)

func main() {
    hub := scarecrow.NewHub(scarecrow.WithErrorHandler(func(err error) {
        fmt.Println("reported:", err)
    }))

    _ = hub.AddWatcher(scarecrow.WatcherFunc(func(scarecrow.Event) error {
        return errors.New("boom")
    }))
    _ = hub.AddWatcher(scarecrow.WatcherFunc(func(scarecrow.Event) error {
        fmt.Println("healthy watcher still runs")
        return nil
    }))

    // OnEvent 永远返回 nil，故障通过 ErrorHandler 上报。
    fmt.Println(hub.OnEvent(scarecrow.PlotStartEvent{Plot: "harvester"}))
    // reported: scarecrow: scarecrow.WatcherFunc failed on scarecrow.PlotStartEvent: boom
    // healthy watcher still runs
    // <nil>
}
```

### Uniformly Starting and Stopping a `Lifecycle`

```go
package main

import (
    "fmt"

    "github.com/Mr-xiaotian/CelestialGrow/pkg/scarecrow"
)

// closer 同时是 Watcher 与 Lifecycle，Start/Stop 按 Hub 记录的注册顺序执行。
type closer struct{ name string }

func (c *closer) OnEvent(scarecrow.Event) error { return nil }

func (c *closer) Start() error {
    fmt.Println("start", c.name)
    return nil
}

func (c *closer) Stop() error {
    fmt.Println("stop", c.name)
    return nil
}

func main() {
    hub := scarecrow.NewHub()
    _ = hub.AddWatcher(&closer{name: "a"})
    _ = hub.AddWatcher(&closer{name: "b"})

    if err := hub.Start(); err != nil {
        panic(err)
    }
    if err := hub.Stop(); err != nil {
        panic(err)
    }

    // 输出顺序：start a、start b、stop b、stop a
    // 停止后再次 Start 会返回 scarecrow: hub already stopped。
    _ = hub.Start()
}
```

### Nested Hubs and `Find`

```go
package main

import (
    "fmt"

    "github.com/Mr-xiaotian/CelestialGrow/pkg/scarecrow"
)

func main() {
    metrics := scarecrow.NewMetricsWatcher()

    farmHub := scarecrow.NewHub()
    _ = farmHub.AddWatcher(metrics)

    plotHub := scarecrow.NewHub()
    _ = plotHub.AddWatcher(farmHub) // 把 Farm Hub 嵌套进 Plot Hub

    _ = plotHub.OnEvent(scarecrow.PlotStartEvent{Plot: "harvester", Tenders: 2})
    _ = plotHub.OnEvent(scarecrow.SeedInputEvent{Plot: "harvester", SeedID: 1})

    found, ok := scarecrow.Find[*scarecrow.MetricsWatcher](plotHub)
    if !ok {
        panic("metrics watcher not found")
    }
    snapshot, _ := found.GetPlot("harvester")
    fmt.Println(snapshot.Status, snapshot.InputTotal) // running 1
}
```

### Rejecting Cycles

```go
package main

import (
    "fmt"

    "github.com/Mr-xiaotian/CelestialGrow/pkg/scarecrow"
)

func main() {
    a, b := scarecrow.NewHub(), scarecrow.NewHub()
    _ = a.AddWatcher(b)

    if err := b.AddWatcher(a); err != nil {
        // scarecrow: cyclic hub registration rejected: target hub already contains this hub
        fmt.Println(err)
    }
}
```

## Notes

- Cycle detection is not a safety guarantee for concurrently built topologies; build the Hub hierarchy once during startup.
- `Find` returns only the **first** match; with several instances of the same type the outcome depends on registration order and level (direct matches first).
- `WithErrorHandler(nil)` does not disable error reporting, it falls back to the default `log.Printf` output; pass an empty function `func(error) {}` if you want silence.
- The default handler uses the standard library `log`, whose target can be redirected globally via `log.SetOutput` (tests commonly use `io.Discard`).
- `Hub` dispatches synchronously, so watcher time adds directly to the event production path; make slow watchers asynchronous yourself (see [`watcher.md`](./watcher.md)).
