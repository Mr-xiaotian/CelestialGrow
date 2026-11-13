# pkg/plot/plot_single.go

> 📅 Last Updated: 2026/11/13

`plot_single.go` defines `SinglePlot[S, F]`, the most commonly used node type in `pkg/plot`: it packages "receive seed → run cultivator concurrently → produce one fruit → broadcast to all connected downstreams" into a connectable, observable, retryable generic node.

`SinglePlot` itself is very thin—it only handles the most basic success semantics (**one seed grows into one fruit, and that fruit is forwarded as-is to every connected downstream**), while all the other common capabilities (state machine, concurrent scheduling, retries, pruning, seal propagation, event emission, standalone batch runs, result export) are provided by the embedded `basePlot[S, F, F]`; see `plot.md` for details.

## Role

- Exposes the generic concurrent node `SinglePlot[S, F]` (`S` is the seed type and `F` is the fruit type).
- Reuses the shared skeleton through the embedded `*basePlot[S, F, F]`, so that `SinglePlot` satisfies the `PlotNode` interface and can be registered, connected, and scheduled by `pkg/farm`.
- Injects the success hook `ripenSeed`: it emits the ripen event and forwards one yield to each downstream.

## Core objects

### The `SinglePlot[S, F]` struct

```go
type SinglePlot[S any, F any] struct {
	*basePlot[S, F, F]
}
```

`SinglePlot` has no fields of its own; all state and capabilities come from `*basePlot[S, F, F]`.

| Generic parameter | Meaning |
|---------|------|
| `S` | Seed input type, i.e. the parameter type of `cultivator` |
| `F` | Fruit output type, i.e. the return type of `cultivator` |

The third generic parameter of `basePlot` (the downstream yield type `Y`) is instantiated as `F` in this type, i.e. **the fruit type of this node is the same as the type it outputs downstream**. The three node kinds compare as follows:

| Node | Underlying `basePlot` | Result type (fruit) | Downstream yield type `Y` |
|------|----------------|------------------|--------------------|
| `SinglePlot[S, F]` | `basePlot[S, F, F]` | `F` | `F` |
| `SplitPlot[S, F]` | `basePlot[S, []F, F]` | `[]F` | `F` |
| `RoutePlot[S, Y]` | `basePlot[S, map[string]Y, Y]` | `map[string]Y` | `Y` |

> `ConnectTo` uses a type assertion to check whether "the upstream's `Y`" and "the downstream's `S`" match. Therefore `SinglePlot[S, F]` can only connect to a downstream node with `S == F`; otherwise the connection immediately returns a type-incompatibility error.

## Public functions

### `NewSinglePlot[S, F](name string, cultivator func(S) (F, error), opts ...Option) *SinglePlot[S, F]`

Creates a `SinglePlot` instance.

| Parameter | Meaning |
|------|------|
| `name` | plot name, must be unique within a `Farm` |
| `cultivator` | cultivation function for a single seed, returns a fruit or an error |
| `opts` | optional configuration (see `option.md`), applied in order after `defaultOptions()` |

Implementation notes:

1. First declare `var p *SinglePlot[S, F]`, then build the shared skeleton with `newBasePlot[S, F, F]`, and finally assign `base` to `p`;
2. The `ripenSeed` hook passed to `newBasePlot` is a closure that calls `p.ripenSeed(...)` internally. It captures `p` before it has been assigned, but the closure is only invoked on the `sprout` / `tend` path after `StartAsync`, by which time `p` is definitely assigned;
3. Channel allocation, default `EventClient` and `Hub` initialization, and `context` derivation are all done inside `newBasePlot` (see the "Construction" section of `plot.md`).

### `ripenSeed(seedPayload runtime.Payload[S], fruit F, startTime time.Time)` (unexported)

The success path implementation of `SinglePlot`, written as a hook into the `basePlot.ripenSeed` field and called by `basePlot.tend` when `cultivator` returns a `nil` error. Steps:

1. Take `seedPayload.EventID` as `seedID` and allocate a `fruitID` via `eventClient.Emit("fruit", []int{seedID})`;
2. Compute the readable representation: the seed is truncated to 50 runes and the fruit to 25 runes (see `helper.md`);
3. Emit `scarecrow.SeedRipenEvent` (`Plot`, `Seed`, `SeedRepr`, `Fruit`, `FruitRepr`, `Elapsed`, `SeedID`, `FruitID`, `Time`)—the metrics watcher increments the success count from it, and the lifecycle watcher records a `ripen` entry from it (`FruitJSON` is the fruit JSON);
4. Iterate over `p.yieldChans` (`downstream plot name → that downstream's seed channel`), and for **every** connected downstream:
   - `eventClient.Emit("seed", []int{fruitID})` allocates the downstream seed event ID (the parent event is `fruitID`);
   - Emit `scarecrow.SeedInputEvent` (`Plot` is the downstream name, `From` is this node's name, `Seed` is the fruit, and `Parents` is `[]int{fruitID}`); the metrics watcher thereby adds both the receiver's upstream input and the source's downstream output;
   - Send `runtime.Payload[F]{Value: fruit, EventID: downstreamSeedID}` to that downstream channel.

> Semantic note: the success path of `SinglePlot` is "**one fruit is forwarded as one downstream yield**", and it forwards **once to every connected downstream** (broadcast). If you need "targeted forwarding by destination", use `RoutePlot` (see `plot_route.md`); if you need "one seed split into multiple downstream yields", use `SplitPlot` (see `plot_split.md`).

## Key flows

### From seed to downstream yield

```mermaid
flowchart LR
    SC[seedChan] --> T[tend]
    T -->|pruneIf hit| P[pruneSeed<br/>no downstream output]
    T -->|failure| W[witherSeed<br/>no downstream output]
    T -->|success| R[SinglePlot.ripenSeed]
    R --> F["fruit event<br/>fruitID"]
    F --> E1["SeedRipenEvent<br/>1 ripen record"]
    F --> D1["downstream A<br/>SeedInputEvent + yield"]
    F --> D2["downstream B<br/>SeedInputEvent + yield"]
    R --> HUB[Hub<br/>Metrics / Lifecycle / Log]
```

Before calling `cultivator(seed)`, `basePlot.tend` first evaluates the prune predicate; if it hits, it goes straight to `basePlot.pruneSeed` (without calling `cultivator`); otherwise it branches on the result:

- **Success** (`err == nil`): calls the injected `ripenSeed`, i.e. the implementation in this file;
- **Failure** (retries exhausted, rejected by `retryIf`, or a `panic` caught by `recover`): goes through `basePlot.witherSeed` and **forwards to no downstream**;
- **Pruned** (the `WithPruneIf` predicate returns `true`): goes through `basePlot.pruneSeed` and **forwards to no downstream**.

For pruning, the retry loop, seal propagation, concurrency model, and other common logic, see `plot.md`.

### Metrics Semantics

One success produces exactly one `SeedRipenEvent`, therefore:

- This node's `GetFruitNum()` (`Succeeded`) increases by 1;
- Each connected downstream receives its own `SeedInputEvent` (with a non-empty `From`), its `InputTotal` increases by 1, and this node's `DownstreamCounts` for that target increases by 1.

## Usage examples

### Standalone mode

```go
package main

import (
	"fmt"

	"github.com/Mr-xiaotian/CelestialGrow/pkg/plot"
)

func main() {
	p := plot.NewSinglePlot("double",
		func(seed int) (int, error) { return seed * 2, nil },
		plot.WithTenders(4),
	)

	p.Run([]int{1, 2, 3, 4, 5}, "INFO")

	records, err := p.Harvest()
	if err != nil {
		panic(err)
	}
	for _, r := range records {
		fmt.Println(r.SeedJSON, r.Status, r.FruitJSON)
	}
}
```

### With retry

```go
package main

import (
	"errors"
	"time"

	"github.com/Mr-xiaotian/CelestialGrow/pkg/plot"
)

var errPermanent = errors.New("permanent")

func main() {
	p := plot.NewSinglePlot("flaky",
		func(seed int) (int, error) {
			if seed == 0 {
				return 0, errPermanent
			}
			return seed * 2, nil
		},
		plot.WithTenders(2),
		plot.WithMaxRetries(3),
		plot.WithRetryDelay(func(attempt int) time.Duration {
			return time.Duration(attempt) * 100 * time.Millisecond
		}),
		plot.WithRetryIf(func(err error) bool {
			return !errors.Is(err, errPermanent)
		}),
	)

	p.Run([]int{0, 1, 2}, "DEBUG")
}
```

### Chaining two one-in-one-out nodes in Farm mode

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

	addOne := plot.NewSinglePlot("addOne", func(seed int) (int, error) {
		return seed + 1, nil
	}, plot.WithTenders(2))

	f := farm.NewFarm("demo")
	if err := f.AddPlot(double, addOne); err != nil {
		panic(err)
	}
	if err := f.Connect([]plot.PlotNode{double}, []plot.PlotNode{addOne}); err != nil {
		panic(err)
	}
	if err := f.Run(map[string][]any{"double": {1, 2, 3}}, "INFO"); err != nil {
		panic(err)
	}

	fmt.Println("done")
}
```

> `Farm.Connect` internally calls `ConnectTo` to establish the "int → int" edge: the upstream `Y = int` and the downstream `S = int`, so the types match.

## Notes

- `NewSinglePlot` uses `runtime.NumCPU()` as the concurrency level and channel buffer by default; I/O-intensive workloads can tune this with `WithTenders` / `WithChanSize`.
- `Run(seeds, logLevel)` is a **blocking** call; it returns only after all seeds have been processed.
- When the upstream `F` and the downstream `S` types do not match, `ConnectTo` returns an error immediately rather than panicking; it is recommended to check the error after `Farm.AddPlot` / `Farm.Connect`.
- `SinglePlot` provides no routing or splitting capability; its forwarding is "broadcast to all downstreams", so do not use it for targeted delivery.
- If a downstream has stopped consuming and its `seedChan` buffer is full, the send in `ripenSeed` blocks, which in turn slows down this node's tenders.
