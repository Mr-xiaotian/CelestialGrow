# pkg/plot/plot_split.go

> 📅 Last Updated: 2026/11/13

`plot_split.go` defines `SplitPlot[S, F]`: a node that does **one-to-many splitting**. Its `cultivator` (whose constructor parameter is named `splitter`) returns `[]F`, and the node then forwards each element of the slice as a separate downstream yield.

It fits scenarios where "one task derives N subtasks" (for example, splitting one directory into several files, or one order into several line items).

## Role

- Provides the generic node `SplitPlot[S, F]`: one seed → multiple downstream yields.
- Reuses the shared `basePlot[S, []F, F]` skeleton (see `plot.md`) and implements only the `ripenSeed` success hook.
- Keeps the accounting rule that **one seed records exactly one `ripen` entry**: however many elements are split out, it still counts as one successful cultivation.

## Core objects

### The `SplitPlot[S, F]` struct

```go
type SplitPlot[S any, F any] struct {
	*basePlot[S, []F, F]
}
```

| Generic parameter | Meaning |
|---------|------|
| `S` | Seed input type, i.e. the parameter type of `splitter` |
| `F` | The type of **a single element** after splitting; the result type is `[]F` and the type output downstream is `F` |

## Public functions

### `NewSplitPlot[S, F](name string, splitter func(S) ([]F, error), opts ...Option) *SplitPlot[S, F]`

| Parameter | Meaning |
|------|------|
| `name` | plot name, must be unique within a `Farm` |
| `splitter` | split function, returns `[]F` or an error |
| `opts` | optional configuration (see `option.md`) |

The implementation notes are the same as for `NewSinglePlot`: first declare `var p *SplitPlot[S, F]`, then build `newBasePlot[S, []F, F]` and inject a closure that calls `p.ripenSeed(...)`, and finally assign `p`.

> Note: a `splitter` returning `(nil, nil)` is also a legal success—in that case no downstream yield is produced, but one `ripen` entry is still recorded (`FruitJSON` is `null`).

### `ripenSeed(seedPayload runtime.Payload[S], fruits []F, startTime time.Time)` (unexported)

The success path, called by `basePlot.tend` when `splitter` returns a `nil` error:

1. `eventClient.Emit("fruit", []int{seedID})` allocates a **single unique** `fruitID` (the whole split shares one fruit event);
2. Compute the readable representation: the seed is truncated to 50 runes and the entire slice to 25 runes (see `helper.md`);
3. Emit `scarecrow.SeedRipenEvent` (`Fruit` is the entire slice and `FruitRepr` is its readable representation)—**only 1 `ripen` / 1 success is recorded**, regardless of `len(fruits)`;
4. Iterate over `p.yieldChans` (that is, **all** connected downstreams), and for each downstream:
   - In an inner loop over `fruits`, for **each element**:
     - `eventClient.Emit("seed", []int{fruitID})` allocates an independent downstream seed event ID (the parent event is the same `fruitID` in every case);
     - Emit `scarecrow.SeedInputEvent` (`Plot` is the downstream name, `From` is this node's name, `Seed` is that element, and `Parents` is `[]int{fruitID}`);
     - Send `runtime.Payload[F]{Value: fruit, EventID: downstreamSeedID}`.

> Counting semantics: this node's `Succeeded` increases by 1 per success, regardless of how many elements are split out; a downstream's `InputTotal` increases by 1 for each element it receives, so "the total seed counts before and after splitting do not match" is expected behavior.

## Key flows

### Event IDs and parent-child relationships

```mermaid
flowchart LR
    S["seed event<br/>seedID"] --> FI["fruit event<br/>fruitID (one)"]
    FI --> D1["downstream seed event #1<br/>parent = fruitID"]
    FI --> D2["downstream seed event #2<br/>parent = fruitID"]
    FI --> D3["downstream seed event #N<br/>parent = fruitID"]
```

- One split allocates only one `fruitID`, and all `N` downstream seed events use it as their parent event;
- The lifecycle watcher records one `seed` entry for each element (`InputEventID` differs each time), but this node has only one `ripen` entry.

### Differences from `SinglePlot` / `RoutePlot`

| Aspect | `SinglePlot` | `SplitPlot` | `RoutePlot` |
|--------|--------------|-------------|-------------|
| Result type | `F` | `[]F` | `map[string]Y` |
| Delivery scope | 1 each to all connected downstreams | `len(fruits)` each to all connected downstreams | Only targets hit by the routing table |
| Downstream input count increment | Every downstream +1 | Every downstream +`len(fruits)` | Every hit target +1 |
| Success count | +1 | +1 (independent of the element count) | +1 |
| Data received by destination downstreams | Same | Same (the same slice) | May differ |

### Empty slice and broadcast to all downstreams

- `fruits` is empty (`nil` or `[]F{}`): the inner loop does not execute, so **downstreams receive no data** (but they still receive the seal from the upstream's wrap-up as usual);
- `fruits` has `N` elements: **every connected downstream receives all `N` elements**—`SplitPlot` is "split + broadcast", not "dispatch by target" (use `RoutePlot` for dispatch by target).

## Usage examples

### Standalone splitting

```go
package main

import (
	"fmt"
	"strings"

	"github.com/Mr-xiaotian/CelestialGrow/pkg/plot"
)

func main() {
	split := plot.NewSplitPlot("split", func(seed string) ([]string, error) {
		if seed == "" {
			return nil, nil
		}
		return strings.Split(seed, ","), nil
	}, plot.WithTenders(2))

	split.Run([]string{"a,b,c", ""}, "INFO")

	records, err := split.Harvest()
	if err != nil {
		panic(err)
	}
	for _, r := range records {
		fmt.Println(r.SeedJSON, r.Status, r.FruitJSON)
	}
}
```

### Splitting and then processing within a Farm

```go
f := farm.NewFarm("split_demo")

// split：把 "1,2,3" 拆成 int 列表
split := plot.NewSplitPlot("split", func(seed string) ([]int, error) {
	parts := strings.Split(seed, ",")
	values := make([]int, 0, len(parts))
	for _, part := range parts {
		v, err := strconv.Atoi(strings.TrimSpace(part))
		if err != nil {
			return nil, err
		}
		values = append(values, v)
	}
	return values, nil
})

// head：对每个拆出来的 int 再处理
head := plot.NewSinglePlot("head", func(seed int) (int, error) {
	return seed * 10, nil
}, plot.WithTenders(2))

_ = f.AddPlot(split, head)
_ = f.Connect([]plot.PlotNode{split}, []plot.PlotNode{head}) // 上游 yield int → 下游 seed int
_ = f.Run(map[string][]any{"split": {"1,2,3"}}, "INFO")
```

## Notes

- The `F` of `SplitPlot` is the **element type**, so when connecting, the downstream's `S` must equal `F` (not `[]F`); this is the type-mismatch pitfall you are most likely to hit.
- Metrics advance at **seed granularity** (each `ripenSeed` increases `Succeeded` by only 1); they do not increase by 5 just because 5 elements were split out.
- A downstream's `InputTotal` grows because each element produces its own `SeedInputEvent`, so what the downstream sees is the seed volume after splitting.
- The slice returned by `splitter` is broadcast to all downstreams; do not modify the slice elements in a downstream (this can cause a data race), or have `splitter` return a freshly created slice each time.
- If the number of `fruits` elements is very large, keep an eye on the `seedChan` buffer and the downstream concurrency; when the buffer is full the upstream's `ripenSeed` blocks at the send.
