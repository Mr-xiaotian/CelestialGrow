# pkg/farm/farm_split_test.go

> 📅 Last Updated: 2026/09/24

## Purpose

`farm_split_test.go`, as `package farm_test`, covers the end-to-end behavior of `SplitPlot` in a `Farm`: after an upstream seed is split into multiple downstream yields, whether `Farm.Run` forwards all elements one by one to the downstream.

## Test Focus

- **One-to-many split forwarding**: one successful `SplitPlot` produces multiple downstream yields, and the downstream seed count should be the sum of the split elements.
- **Distinguishing counting semantics**: `SplitPlot.GetFruitNum` counts by "number of input seeds" (each split records 1 fruit) rather than by the number of split elements; only the downstream `head.GetSeedNum` reflects the total number of elements.
- **Empty split result**: when the split function returns a `nil` slice, nothing is forwarded downstream.

## Test Cases

| Test function | What it verifies |
| --- | --- |
| `TestFarmRunSplitPlot` | `split` (`string → []int`, splitting on commas) and `head` (`int → int`, `seed*10`). After input `{"1,2,3", "4,5"}`: `split.GetFruitNum() == 2` (2 successful splits), `head.GetSeedNum() == 5`, `head.GetFruitNum() == 5` |

## Key Details

- **The split function covers empty input**: `split` returns `nil, nil` for an empty string, used to cover the "empty split result" branch (no yield is emitted).
- **Number parsing error**: a `strconv.Atoi` failure during splitting returns an error, which then takes the widhered path of `SplitPlot`; the inputs in this case are all valid integers, verifying the pure success path.
- **`head`'s fan-in**: `head` uses `WithTenders(2)` and processes multiple yields from `split` concurrently, so `head.GetSeedNum` depends on the upstream yield counters being registered correctly.

## Related Source

- `Connect` / `Run` in `pkg/farm/farm.go`
- `SplitPlot.ripenSeed` in `pkg/plot/plot_split.go` (sends downstream yields element by element)
- `GetSeedNum` / `GetFruitNum` / `AddDownstreamYieldNum` in `pkg/plot/counter.go`

## How to Run

```bash
go test ./pkg/farm/ -run 'TestFarmRunSplitPlot' -v
```
