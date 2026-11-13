# demo/demo_prune/main.go

> 📅 Last Updated: 2026/11/13

## Purpose

`demo/demo_prune/main.go` is the CelestialGrow example that demonstrates the **prune mechanism**, showing how to use `WithPruneIf` from `pkg/api` to perform concurrent-safe **deduplication** of seeds: duplicate seeds are marked as `prune` before they enter the cultivator, so they are no longer cultivated and produce no downstream fruit.

Compared with [`demo_farm/main.go`](../demo_farm/main.md), this example focuses on two things:

- **Single plot scenario** (`singlePlotDedup`): deduplicates a seed list containing repeated values and then doubles each seed.
- **Multi-plot scenario** (`farmDedup`): root doubles the seeds and produces duplicates, and head deduplicates them while adding one.

Both paths reuse the `WithPruneIf` predicate; only the attachment point differs.

## Code Structure

The file consists of four parts:

1. **Package and imports**: it provides an executable entry as `package main` and imports `fmt`, `sync`, and `pkg/api` (alias `grow`).
2. **`deduplicator`**: internally keeps a "seen seeds" set as a `map[int]struct{}` guarded by a `sync.Mutex`, and exposes a `pruneIf` predicate method.
3. **Two cultivators and one predicate method**: `double` and `addOne` serve as cultivators, and `deduplicator.pruneIf` serves as the `WithPruneIf` predicate.
4. **Two scenarios + `main`**: `singlePlotDedup` and `farmDedup` demonstrate single-plot and multi-plot deduplication respectively, and `main` calls both in order.

## Key Objects

### `deduplicator`

```go
// deduplicator 维护一个并发安全的已见种子集合。
type deduplicator struct {
	mu   sync.Mutex
	seen map[int]struct{}
}

// newDeduplicator 创建一个空的去重器。
func newDeduplicator() *deduplicator {
	return &deduplicator{seen: make(map[int]struct{})}
}

// pruneIf 生成 WithPruneIf 谓词：种子已见过则修剪（跳过），
// 否则登记为已见并继续培育。加锁保证多 tender 并发下仍只处理一次。
func (d *deduplicator) pruneIf(seed int) bool {
	d.mu.Lock()
	defer d.mu.Unlock()
	if _, ok := d.seen[seed]; ok {
		return true
	}
	d.seen[seed] = struct{}{}
	return false
}
```

- The return value of `pruneIf` is used directly by `WithPruneIf`: `true` means the seed should be pruned, `false` means cultivation continues.
- The lock guarantees "process only once" even with concurrent tenders: if two goroutines encounter the same seed at the same time, only one of them can atomically complete the `false` result together with the registration, while the other gets `true`.

## Key APIs

The relevant APIs used in this example (constructors and configuration come from `pkg/api`, counting methods come from `basePlot` in `pkg/plot`):

| Call | Source | Purpose |
|------|------|------|
| `grow.WithPruneIf[S any](fn func(S) bool)` | `pkg/api` → `plot.WithPruneIf` | Injects the pruning predicate into a Plot; it is evaluated for every seed before the cultivator, and a hit skips cultivation. |
| `grow.NewSinglePlot[S, F](name, cultivator, opts...)` | `pkg/api` → `plot.NewSinglePlot` | Creates a single-in single-out node; `WithPruneIf` is injected through `opts`. |
| `p.GetPruneNum() int` | the `basePlot` method of `pkg/plot` (promoted through `SinglePlot`) | Reads the cumulative number of pruned seeds of this Plot. |
| `p.GetFruitNum() int` / `p.GetWeedNum() int` | same as above | Reads the fruit/weed counts, used together with `GetPruneNum` to show the overall state. |

> These counting methods read the metrics snapshot of `MetricsWatcher`. Both `SinglePlot.Run` and `Farm.Run` automatically register `MetricsWatcher` through `pkg/assembly`, so the values can be read right after the run; if no metrics observer has been assembled, they return `0`.

## Scenario 1: `singlePlotDedup`

```go
	seeds := []int{1, 2, 3, 2, 1, 4, 4, 5}

	p := grow.NewSinglePlot("single_dedup", double,
		grow.WithTenders(3),
		grow.WithPruneIf(newDeduplicator().pruneIf),
	)
	p.Run(seeds, "INFO")
```

- 8 seeds are input, among which `1`, `2`, and `4` each appear repeatedly.
- `WithPruneIf` maintains a "seen set": the 5 distinct values seen for the first time are cultivated (deduplicated, then doubled), and the 3 duplicates are pruned.
- `p.Run` is the synchronous run entry of a single plot and needs no Farm; the second argument is the log level.
- Output: `fruit=5 (deduplicated then doubled), prune=3 (duplicates pruned), weed=0`.

## Scenario 2: `farmDedup`

```go
	root := grow.NewSinglePlot("double", double, grow.WithTenders(3))
	head := grow.NewSinglePlot("dedup_add", addOne,
		grow.WithTenders(3),
		grow.WithPruneIf(newDeduplicator().pruneIf),
	)

	farm := grow.NewFarm("farm_dedup")
	if err := farm.AddPlot(root, head); err != nil {
		panic(err)
	}
	if err := farm.Connect([]grow.PlotNode{root}, []grow.PlotNode{head}); err != nil {
		panic(err)
	}
	if err := farm.Run(map[string][]any{
		"double": {1, 2, 2, 3, 3, 3},
	}, "INFO"); err != nil {
		panic(err)
	}
```

- `root` doubles the 6 seeds and forwards them to `head`, producing `2, 4, 4, 6, 6, 6`.
- `head` deduplicates these 6 fruits (used as its seeds): `2, 4, 6` are cultivated and each incremented to `3, 5, 7`; the 3 duplicates are pruned.
- Output: `dedup_add: fruit=3 (dedup+1 -> 3,5,7), prune=3 (duplicates pruned), weed=0`.

## How to Run

Execute the following in the project root:

```bash
go run ./demo/demo_prune
```

After a successful run, the terminal prints a four-line summary:

```text
[single plot] input=[1 2 3 2 1 4 4 5]
[single plot] fruit=5 (去重后翻倍), prune=3 (重复被修剪), weed=0
[farm] double:   seed=6 -> fruit=6 (翻倍转发 2,4,4,6,6,6)
[farm] dedup_add: fruit=3 (去重+1 -> 3,5,7), prune=3 (重复被修剪), weed=0
```

At the same time, the current working directory contains runtime artifacts:

- `logs/grow_log(YYYY-MM-DD).log`: the logs of both `Run` calls are appended to the same file per day, with log level `"INFO"` for both.
- `lifecycles/YYYY-MM-DD/`: `singlePlotDedup` and `farmDedup` each call `Run` once and each produce a `grow_lifecycle(HH-MM-SS.mmm).sqlite3`, so the directory ends up with two lifecycle databases.

## Full Source

The following code is identical to the source of `demo/demo_prune/main.go` and can be copied and run directly:

```go
package main

import (
	"fmt"
	"sync"

	grow "github.com/Mr-xiaotian/CelestialGrow/pkg/api"
)

// deduplicator 维护一个并发安全的已见种子集合。
type deduplicator struct {
	mu   sync.Mutex
	seen map[int]struct{}
}

// newDeduplicator 创建一个空的去重器。
func newDeduplicator() *deduplicator {
	return &deduplicator{seen: make(map[int]struct{})}
}

// pruneIf 生成 WithPruneIf 谓词：种子已见过则修剪（跳过），
// 否则登记为已见并继续培育。加锁保证多 tender 并发下仍只处理一次。
func (d *deduplicator) pruneIf(seed int) bool {
	d.mu.Lock()
	defer d.mu.Unlock()
	if _, ok := d.seen[seed]; ok {
		return true
	}
	d.seen[seed] = struct{}{}
	return false
}

// double 将种子翻倍。
func double(num int) (int, error) {
	return num * 2, nil
}

// addOne 为种子加一。
func addOne(num int) (int, error) {
	return num + 1, nil
}

// singlePlotDedup 单 plot 场景：对含重复值的种子列表去重后翻倍。
func singlePlotDedup() {
	seeds := []int{1, 2, 3, 2, 1, 4, 4, 5}

	p := grow.NewSinglePlot("single_dedup", double,
		grow.WithTenders(3),
		grow.WithPruneIf(newDeduplicator().pruneIf),
	)
	p.Run(seeds, "INFO")

	fmt.Printf("[single plot] input=%v\n", seeds)
	fmt.Printf("[single plot] fruit=%d (去重后翻倍), prune=%d (重复被修剪), weed=%d\n",
		p.GetFruitNum(), p.GetPruneNum(), p.GetWeedNum())
}

// farmDedup 多 plot 场景：root 翻倍产生重复值，head 去重后加一。
func farmDedup() {
	root := grow.NewSinglePlot("double", double, grow.WithTenders(3))
	head := grow.NewSinglePlot("dedup_add", addOne,
		grow.WithTenders(3),
		grow.WithPruneIf(newDeduplicator().pruneIf),
	)

	farm := grow.NewFarm("farm_dedup")
	if err := farm.AddPlot(root, head); err != nil {
		panic(err)
	}
	if err := farm.Connect([]grow.PlotNode{root}, []grow.PlotNode{head}); err != nil {
		panic(err)
	}
	if err := farm.Run(map[string][]any{
		"double": {1, 2, 2, 3, 3, 3},
	}, "INFO"); err != nil {
		panic(err)
	}

	fmt.Println("[farm] double:   seed=6 -> fruit=6 (翻倍转发 2,4,4,6,6,6)")
	fmt.Printf("[farm] dedup_add: fruit=%d (去重+1 -> 3,5,7), prune=%d (重复被修剪), weed=%d\n",
		head.GetFruitNum(), head.GetPruneNum(), head.GetWeedNum())
}

// main 演示用 prune 机制做种子去重：单 plot 与 farm 两个场景。
func main() {
	singlePlotDedup()
	farmDedup()
}
```

## Notes

1. **The predicate takes precedence over the cultivator**: `pruneIf` is evaluated inside `tend` **before** the cultivator is invoked; a hit follows the `pruneSeed` path, so no business logic runs and nothing is produced downstream.
2. **Concurrency safety is the caller's responsibility**: `WithPruneIf` only evaluates the predicate and does not guarantee the concurrency safety of its internal state; this example uses a `deduplicator` guarded by `sync.Mutex` to process each seed only once under multiple tenders.
3. **Pruning counts as completion**: pruned seeds count toward `GetCompleted` (`fruit + weed + prune`) and toward `pruneNum`, so they are distinguished from "failures" — pruning is a deliberate skip within the design, not an error.
4. **Persistence visibility**: pruned seeds leave a `SeedPruneEvent` in the lifecycle log/database with state `prune`, alongside `ripen` / `wither`.
5. **The predicate only applies to the current level's seeds**: `WithPruneIf` is evaluated for every seed a Plot receives; to deduplicate at the root node as well, pass the predicate when creating the root Plot too.
