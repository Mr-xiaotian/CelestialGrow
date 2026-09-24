# pkg/farm/farm_structure_test.go

> 📅 Last Updated: 2026/09/24

## Purpose

`farm_structure_test.go` is the largest test file in `pkg/farm`, covering the end-to-end behavior of `Farm` under **non-trivial topologies**. It verifies:

- the multi-source multi-sink "1→2→1" diamond structure (`TestFarmStructure121`)
- `fruit` / `weed` counting when nodes partially fail (`TestFarmStructure121PartialFailure`)
- multiple **mutually disconnected** subgraphs running in parallel (`TestFarmStructureDisconnectedComponents`)
- fan-in convergence when multiple sources have different speeds (`TestFarmStructure21FaninDifferentSpeed`)

## Test Cases

### `TestFarmStructure121`

- Topology: `root` (source) → `{midA, midB}` → `head` (sink)
- 50 seeds (`0..49`) are input to `root`; `midA` outputs `seed*10+1`, `midB` outputs `seed*10+2`.
- Expectation: `head` receives 100 fruits in total, and for each `i ∈ [0,50)`, `i*10+1` and `i*10+2` each appear exactly once.
- All plots end in state `state == 2`.

### `TestFarmStructure121PartialFailure`

- The same diamond topology, 20 seeds (`0..19`).
- Failure injection:
  - `root`: even seeds return an error → 10 fruits / 10 weeds each
  - `midB`: `seed > 10` fails → 5 fruits / 5 weeds
  - `midA`: all succeed
- Expectations:
  - `root.GetFruitNum() == 10` and `GetWeedNum() == 10`
  - `midA.GetFruitNum() == 10` (aligned with `root`'s actual fruit count)
  - `midB.GetFruitNum() == 5` / `GetWeedNum() == 5`
  - `head.GetFruitNum() == 15` (10 from `midA` + 5 from `midB`)
- All plots end in state `state == 2`.

### `TestFarmStructureDisconnectedComponents`

- Two mutually disconnected subgraphs within the same `Farm`:
  - Subgraph A: `rootA` → `{midA1, midA2}`
  - Subgraph B: `{rootB1, rootB2}` → `headB`
- Both subgraphs are fed 50 seeds.
- Expectations:
  - `resultsA` (collected jointly by `midA1` / `midA2`) contains 50 distinct values, each counted 2 times (once each from `midA1` and `midA2`).
  - `resultsB` (collected by `headB`) contains 100 distinct values, with `seed*10+3` and `seed*10+4` produced by each original seed appearing once each.
- Verifies that `Farm.SourceNodes` correctly identifies **two** source SCCs (`{rootA}` and `{rootB1, rootB2}`) and triggers `Seal()` for each source.

### `TestFarmStructure21FaninDifferentSpeed`

- Topology: `{rootFast, rootSlow}` → `head`.
- `rootSlow` sleeps 10ms per item, `rootFast` does not sleep; both use `WithChanSize(50)`.
- 50 seeds each.
- Expectation: `head` receives 100 in total, each counted only once, and all plots end in state `state == 2`.
- Verifies that fan-in still completes correctly when multiple sources have different speeds (depends on `Plot`'s `chan` buffering and the `select` scheduling of `sprout`).

## Key Details

- **Multiple connected components**: `TestFarmStructureDisconnectedComponents` indirectly covers the fact that `SourceNodes` takes one representative node from **each** Source SCC — meaning `Farm.Run` sends `Seal()` to `rootA`, `rootB1`, and `rootB2`.
- **fan-out / fan-in counting**: `counts[seed]` comes from `head`'s `cultivator`, which is called concurrently, so the test protects it with a `sync.Mutex`.
- **Failure routing**: `basePlot.witherSeed` only records the failure and does not forward it downstream; the number of fruits received by `head` is strictly equal to `midA.GetFruitNum() + midB.GetFruitNum()`, and this assertion is verified in `TestFarmStructure121PartialFailure`.
- **Channel configuration**: `WithChanSize` is explicitly enlarged in `TestFarmStructure21FaninDifferentSpeed` to prevent a slow source from blocking writes from a fast source.

## Related Source

- `pkg/farm/farm.go`: `Run`, `SourceNodes`, `AddPlot`, `Connect`
- `pkg/farm/graph.go`: `SourceNodes`, `TarjanSCC`
- `pkg/plot/plot_base.go`: `GetState` / `sprout` / `tend` / `witherSeed`
- `pkg/plot/plot.go`: `Plot.ripenSeed`
- `pkg/plot/counter.go`: `GetSeedNum` / `GetFruitNum` / `GetWeedNum`

## How to Run

```bash
go test ./pkg/farm/ -run 'TestFarmStructure' -v
```
