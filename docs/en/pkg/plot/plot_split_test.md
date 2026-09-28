# pkg/plot/plot_split_test.go

> 📅 Last Updated: 2026/09/24

`plot_split_test.go` is the **result snapshot** test of `SplitPlot` and has a single case: it verifies that after a standalone batch run the split node records each seed as exactly **one** `ripen` entry and persists the complete slice of split results into `FruitJSON`—including the boundary case of "0 elements split out".

## Cases

### `TestSplitPlot_RunHarvest`

- **splitter**:
  ```go
  func(seed int) ([]string, error) {
      fruits := make([]string, 0, seed)
      for i := 0; i < seed; i++ {
          fruits = append(fruits, fmt.Sprintf("%d-%d", seed, i))
      }
      return fruits, nil
  }
  ```
- **Configuration**: `plot.NewSplitPlot("split_harvest", splitter, plot.WithTenders(2))`
- **seeds**: `[]int{0, 2}`

Verifies:

1. `mustHarvest(t, split)` returns 2 status records;
2. after indexing with `indexStatusesBySeed` keyed by `SeedJSON`:
   - `index["0"]` (`seed = 0`, the loop does not execute → empty slice): `Status == "ripen"`, `FruitJSON == "[]"`;
   - `index["2"]` (`seed = 2` → `["2-0","2-1"]`): `Status == "ripen"`, `FruitJSON == "[\"2-0\",\"2-1\"]"`.

> Coverage points:
> 1. The number of split-out elements can be 0, and even then one `ripen` entry is still recorded (`AddFruitNum(1)` is independent of `len(fruits)`);
> 2. The entire slice (rather than a single element) is recorded as the result of one success.

## Common preconditions

- Test package: `package plot_test` (black-box); it does not access the unexported fields of `SplitPlot`;
- It relies on the two helper functions provided by the other test files in the same package (both defined in `plot_harvest_test.go`):
  - `mustHarvest(t *testing.T, plot harvestable)`: wraps `Harvest()` and calls `t.Fatalf` on error; `harvestable` is the local test interface `interface{ Harvest() ([]persist.LifecycleStatusRecord, error) }`;
  - `indexStatusesBySeed(records)`: indexes by `record.SeedJSON` as the key.
- This case connects **no** downstream: `split.yieldChans` is empty, so the downstream delivery loop in `ripenSeed` never executes; `Run` relies on the trailing `Seal()` to trigger input closing before wrapping up normally.
- Therefore this case does **not** verify that "every split-out element becomes one downstream yield" nor the counting effect of `AddDownstreamYieldNum(len(fruits))`—that part is covered by `farm_split_test.go` in `pkg/farm`.

## How to run

```bash
go test ./pkg/plot -run "TestSplitPlot" -v
```

## Notes

- `fruits` is created with `make([]string, 0, seed)`, so when `seed = 0` it is a **non-nil empty slice** and serializes to `[]` rather than `null`; if the splitter were changed to return `nil`, this assertion would need to be changed to `null` accordingly.
- The key of `indexStatusesBySeed` is `SeedJSON` (the JSON text of an `int` seed), not a formatted struct string.
- This case only verifies whole-slice persistence for a single seed, not the number and order of elements the downstream actually receives.
