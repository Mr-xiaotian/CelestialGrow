# pkg/plot/plot_route_test.go

> 📅 Last Updated: 2026/09/24

`plot_route_test.go` is the **result snapshot** test of `RoutePlot` and has a single case: it verifies that after a standalone batch run the routing node records each successful seed as exactly **one** `ripen` entry and persists the whole routing table as-is into `FruitJSON`.

## Cases

### `TestRoutePlot_RunHarvest`

- **cultivator (the routing function)**:
  ```go
  func(seed int) (map[string]string, error) {
      return map[string]string{
          "left":  fmt.Sprintf("L%d", seed),
          "right": fmt.Sprintf("R%d", seed),
      }, nil
  }
  ```
- **Configuration**: `plot.NewRoutePlot("route_harvest", cultivator, plot.WithTenders(2))`
- **seeds**: `[]int{1, 2}`

Verifies:

1. `mustHarvest(t, route)` returns 2 status records;
2. after indexing with `indexStatusesBySeed` keyed by `SeedJSON`:
   - `index["1"]`: `Status == "ripen"`, `FruitJSON == {"left":"L1","right":"R1"}`;
   - `index["2"]`: `Status == "ripen"`, `FruitJSON == {"left":"L2","right":"R2"}`.

> Coverage points: the routing table as a **whole** (rather than a single value) is recorded as the result of one success, showing that the result type of `RoutePlot` is `map[string]Y` and corresponds to exactly one `ripen`.

## Common preconditions

- Test package: `package plot_test` (black-box); it does not access the unexported fields of `RoutePlot`;
- It relies on the two helper functions provided by the other test files in the same package (both defined in `plot_harvest_test.go`):
  - `mustHarvest(t *testing.T, plot harvestable)`: wraps `Harvest()` and calls `t.Fatalf` on error; `harvestable` is the local test interface `interface{ Harvest() ([]persist.LifecycleStatusRecord, error) }`, so `Plot` / `RoutePlot` / `SplitPlot` can all be passed directly;
  - `indexStatusesBySeed(records)`: indexes by `record.SeedJSON` as the key.
- This case connects **no** downstream: `route.yieldChans` is empty, so the delivery loop in `ripenSeed` never executes; `Run` relies on the trailing `Seal()` to trigger input closing before wrapping up normally.
- Therefore this case does **not** verify downstream forwarding behaviors such as `AddDownstreamYieldNum` / `SeedInput`—that part is covered by `farm_route_test.go` in `pkg/farm`.

## How to run

```bash
go test ./pkg/plot -run "TestRoutePlot" -v
```

## Notes

- `FruitJSON` comes from `persist`'s JSON serialization; `encoding/json` outputs `map[string]string` as `{"left":...,"right":...}`, **sorted by key name**; the assertions hard-code this order, so if the serialization implementation switches to preserving insertion order, this test needs to be adjusted accordingly.
- The key of `indexStatusesBySeed` is `SeedJSON` (the JSON text of an `int` seed, such as `"1"`, `"2"`), not a formatted struct string.
- This case only verifies whole-table persistence for a single seed, not the delivery effect of "different downstreams receiving different yields"; to verify delivery, refer to `pkg/farm/farm_route_test.go`.
