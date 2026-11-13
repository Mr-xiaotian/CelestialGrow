# pkg/plot/plot_prune_test.go

> 📅 Last Updated: 2026/11/13

`plot_prune_test.go` focuses on the **prune semantics** of `SinglePlot`: it verifies how the `WithPruneIf` predicate decides whether a seed is "skipped directly" or "cultivated normally", and whether the prune count `GetPruneNum` and the completed count `GetCompleted` are conserved in mixed scenarios.

## Cases

### `TestPlot_PruneSkipsCultivator` — every seed is pruned

- **cultivator**:
  ```go
  func(seed int) (int, error) {
      cultivations.Add(1)
      return 0, errors.New("cultivator should never be called")
  }
  ```
- **Configuration**: `plot.NewSinglePlot("test_prune_all", cultivator, plot.WithTenders(1), plot.WithPruneIf(func(seed int) bool { return true }))`
- **seeds**: `[]int{1, 2, 3}`

Verifies:

1. `cultivations.Load() == 0`—the predicate is always `true`, so `cultivator` is **never called**;
2. `mustHarvest` returns 3 records, each with `Status == "prune"`;
3. `plot.GetPruneNum() == 3` and `plot.GetCompleted() == 3`.

> Coverage points: pruning happens before the `cultivator`, so when every seed hits it the cultivation logic is bypassed entirely; pruned seeds still count toward the completed count, and `GetCompleted` is conserved with the total number of seeds.

### `TestPlot_PruneMiss` — no seed is pruned

- **cultivator**: `func(seed int) (int, error) { return seed * 10, nil }`
- **Configuration**: `plot.NewSinglePlot("test_prune_miss", cultivator, plot.WithTenders(2), plot.WithPruneIf(func(seed int) bool { return false }))`
- **seeds**: `[]int{1, 2, 3}`

Verifies:

1. every seed has `Status == "ripen"` and `FruitJSON == strconv.Itoa(seed*10)`;
2. `plot.GetPruneNum() == 0`;
3. `plot.GetFruitNum() == 3`.

> Coverage points: when the predicate is always `false`, the behavior is **exactly the same as when `WithPruneIf` is not set**—pruning is a "zero-intrusion" optional capability, and existing code is unaffected.

### `TestPlot_PruneMixed` — mixed scenario

- **cultivator**: returns an error (wither) when `seed % 2 == 0`, otherwise returns `seed * 10` (ripen).
- **Configuration**: `plot.NewSinglePlot("test_prune_mixed", cultivator, plot.WithTenders(2), plot.WithPruneIf(func(seed int) bool { return seed > 3 }))`
- **seeds**: `[]int{1, 2, 3, 4, 5}`

Verifies:

1. 5 records with the `Status` distribution: `prune` (`seed > 3`: 4, 5) 2 seeds, `wither` (the even seed 2) 1 seed, `ripen` (the odd seeds 1, 3) 2 seeds;
2. `successCount == 2`, `failedCount == 1`, `pruneCount == 2`;
3. `plot.GetFruitNum() == 2`, `plot.GetWeedNum() == 1`, `plot.GetPruneNum() == 2`;
4. `plot.GetCompleted() == 5`—the sum of the three outcomes equals the total number of seeds, strictly conserved.

> Coverage points: prune / ripen / wither can coexist in the same plot, and `GetCompleted` (ripened + withered + pruned) always equals the number of input seeds, ensuring that progress display and `IsFinish` do not become wrong in pruning scenarios.

## Common preconditions

- Test package: `package plot_test` (black-box); it does not access the unexported fields of `SinglePlot` directly.
- It reuses `mustHarvest(t, plot harvestable)` defined in `plot_harvest_test.go` to fetch lifecycle snapshots.
- `indexStatusesBySeed(records)` indexes records by `SeedJSON` so that assertions can be made per seed; `SeedJSON` is `strconv.Itoa(seed)`, for example the key for seed `1` is `"1"`.
- The count assertions rely on `GetPruneNum()` / `GetFruitNum()` / `GetWeedNum()` / `GetCompleted()`: these methods are defined on `basePlot` in `pkg/plot/plot.go` and read the `PlotMetrics` snapshot (`Skipped` / `Succeeded` / `Failed` / `Processed`) rebuilt by `scarecrow.MetricsWatcher` from the event stream; there is no separate `Counter` any more.

## How to run

```bash
go test ./pkg/plot -run "TestPlot_Prune" -v
```

Or run all tests in the plot package at once:

```bash
go test ./pkg/plot/... -v
```

## Notes

- The status strings follow the source code: `"prune"` for pruned, `"ripen"` for success, and `"wither"` for failure.
- The key difference between pruning and `wither`: `wither` is "cultivation failed", while `prune` is "skipped before cultivation"; both count as completed, but **only** `wither` writes `WitherMessage` (`prune` records have no `WitherMessage`).
- `TestPlot_PruneMixed` uses `seed > 3` as the predicate and `seed % 2` as the wither decision; the two are orthogonal, so the coexistence of the three states is covered deterministically.
- These cases all use `WithTenders` concurrency; metrics are accumulated under a lock by `MetricsWatcher` during event dispatch, so the assertions do not rely on strictly serial execution.
