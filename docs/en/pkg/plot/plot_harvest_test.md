# pkg/plot/plot_harvest_test.go

> 📅 Last Updated: 2026/11/13

`plot_harvest_test.go` is the "**result snapshot**" test set of the `plot` package. It runs a batch through `SinglePlot.Run` and then calls `Harvest` to fetch the final lifecycle status of each seed (`ripen` / `wither` + `FruitJSON` + `WitherMessage`), verifying:

- the three result distributions: all failures, partial failures, and all successes;
- whether the metrics rebuilt from the event stream (`GetCompleted`) and `GetState` settle into the correct values when the batch finishes;
- whether the error message on failure lands verbatim in `LifecycleStatusRecord.WitherMessage`.

This file also provides two helper functions to the other test files in the same package (`plot_prune_test.go`, `plot_retry_test.go`, `plot_route_test.go`, `plot_split_test.go`).

## Test helpers

```go
type harvestable interface {
	Harvest() ([]persist.LifecycleStatusRecord, error)
}

func mustHarvest(t *testing.T, plot harvestable) []persist.LifecycleStatusRecord
```

`harvestable` is a local interface defined inside the tests that only requires `Harvest()` to be implemented. Therefore `SinglePlot`, `RoutePlot`, and `SplitPlot` can all be passed directly to `mustHarvest`—they all obtain the same `Harvest` method through the embedded `basePlot`. When `mustHarvest` hits an error it calls `t.Fatalf` directly; otherwise it returns the status record slice.

```go
func indexStatusesBySeed(records []persist.LifecycleStatusRecord) map[string]persist.LifecycleStatusRecord
```

Turns the record slice into a map keyed by `record.SeedJSON`, making it easy to make point assertions by seed value (the JSON text of an `int` seed is `"1"`, `"2"`, and so on).

## Cases

### `TestPlot_AllError` — all failures

- **cultivator**: `func(seed int) (string, error) { return "", errors.New("always fail") }`
- **Configuration**: `plot.NewSinglePlot("test_all_error", cultivator, plot.WithTenders(2))`
- **seeds**: `[]int{1, 2, 3, 4, 5}`

Verifies:

1. `mustHarvest` returns 5 records;
2. every `record.Status == "wither"` and `record.WitherMessage == "always fail"`;
3. `plot.GetCompleted() == 5`;
4. `int(plot.GetState()) == 2` (`done`).

> Purpose: to guarantee that `SeedWitherEvent` is emitted correctly on the `basePlot.witherSeed` path, that the lifecycle record is written correctly, and that the error message is not swallowed.

### `TestPlot_PartialError` — partial failures

- **cultivator**: when `seed % 2 == 0` → returns `(0, errors.New("even number error"))`; otherwise returns `(seed*10, nil)`.
- **Configuration**: `plot.NewSinglePlot("test_partial_error", cultivator, plot.WithTenders(2))`
- **seeds**: `[]int{1, 2, 3, 4, 5}`

Verifies:

1. `mustHarvest` returns 5 records;
2. looks up each record with `indexStatusesBySeed` using `strconv.Itoa(seed)`:
   - even seeds: `Status == "wither"` and `WitherMessage == "even number error"`;
   - odd seeds: `Status == "ripen"` and `FruitJSON == strconv.Itoa(seed*10)`;
3. success count = 3, failure count = 2, `GetCompleted() == 5`.

> Purpose: to guarantee that the branching conditions of `ripenSeed` and `witherSeed` are mutually exclusive and that `SeedJSON` aligns exactly with the input seed.

### `TestPlot_AllSuccess` — all successes

- **cultivator**: `func(seed int) (int, error) { return seed * 2, nil }`
- **Configuration**: `plot.NewSinglePlot("test_all_success", cultivator, plot.WithTenders(3))`
- **seeds**: `[]int{1, 2, 3, 4, 5}`

Verifies:

1. `mustHarvest` returns 5 records;
2. every `Status == "ripen"` and `FruitJSON == strconv.Itoa(seed*2)`;
3. `int(plot.GetState()) == 2` (`done`).

> Purpose: to cover the pure success path with zero failures, verifying `FruitJSON` serialization and the state machine's wrap-up.

## Common preconditions

- Test package: `package plot_test` (black-box testing); it does not access the unexported fields of `SinglePlot` directly;
- It does not explicitly call `StartAsync` / `Seal`; all of that is done inside `Run(seeds, "INFO")`;
- `Run` registers `MetricsWatcher` and `LifecycleWatcher` through `assembly.StartPlotResources`, so `GetCompleted` can read the metrics rebuilt from the event stream, and `Harvest` can find the persisted database through `LifecycleWatcher.DBPath()`;
- Before `Run` returns it calls `resources.Stop()` to stop the watchers and close the database; `Harvest` then reopens the database with `persist.LoadLifecycleStatusesFromPath` for an offline query;
- The cultivator of all three cases never returns "transient error then success", so they are unaffected by the default `maxRetries` value (`1`)—the failure cases are **deterministic failures** and the success cases **succeed on the first attempt**.

## How to run

```bash
go test ./pkg/plot -run "TestPlot_AllError|TestPlot_PartialError|TestPlot_AllSuccess" -v
```

Or run all cases in this file with the `TestPlot_` prefix at once:

```bash
go test ./pkg/plot -run "TestPlot_" -v
```

## Notes

- `mustHarvest` fails directly with `t.Fatalf`; any `plot %q lifecycle watcher is not registered` or `plot %q lifecycle database is not ready` error makes the whole test fail.
- All three cases only verify the lifecycle SQLite snapshot, not the contents of the log files; for log behavior, refer to the relevant tests in `pkg/persist`.
- The status strings follow the source code: `"ripen"` for success and `"wither"` for failure (determined by the lifecycle table convention of `pkg/persist`). If that convention changes, the assertions mentioned in this file and in `plot_retry_test.md` must be updated accordingly.
- The key of `indexStatusesBySeed` is `SeedJSON` (the JSON text of the seed), not a formatted struct string, so the assertions use `strconv.Itoa(seed)` rather than `fmt.Sprintf("%+v", seed)`.
