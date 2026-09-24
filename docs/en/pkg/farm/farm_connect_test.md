# pkg/farm/farm_connect_test.go

> 📅 Last Updated: 2026/09/24

## Purpose

`farm_connect_test.go` covers the semantic correctness of the **registration (`AddPlot`)** and **connection (`Connect`)** paths in `pkg/farm/farm.go`. The test file, written as `package farm_test`, verifies behavior through `pkg/farm`'s public symbols, serving as the "black-box contract" of the `Farm` public API.

## Test Cases

| Test function | What it verifies |
| --- | --- |
| `TestFarmAddPlot` | Multiple plots registered at once; `PlotCount` / `HasPlot` / `GetPlot` are all consistent; after registration **no** edges have been created |
| `TestFarmAddPlotDuplicateName` | A second `AddPlot` of a plot with a duplicate name returns an error |
| `TestFarmConnectHyperEdge` | `Connect` automatically deduplicates duplicate targets (e.g. `targetA` appearing twice); `source` is correctly connected to all targets, but the reverse edges do not exist |
| `TestFarmConnectTypeMismatch` | When the upstream `F` and downstream `S` types do not match, `Connect` returns an error (passing through the assertion failure from `ConnectTo`) |

## Key Assertions

- `TestFarmAddPlot` also verifies that the pointer obtained from `GetPlot` is identical to the original `plot` — `Farm.plots` stores pointers and does not copy values.
- The duplicated `targetA` in `TestFarmConnectHyperEdge` does not cause `source → targetA` to be created twice: the internal `uniquePlots` completes deduplication before `Connect` is called.
- In `TestFarmConnectTypeMismatch`, the upstream and downstream `Plot[S, F]` have different `F` and `S`, so the `.(chan runtime.Payload[Y])` type assertion on `next.GetSeedChanAny()` inside `ConnectTo` fails; therefore `Connect` returns an error as a whole, and no `AddEdge` record is left in the `OrderGraph`.

## Related Source

- `AddPlot` error returns: `AddPlot` / `requireRegistered` in `pkg/farm/farm.go`
- `Connect` error returns: `Connect` / `uniquePlots` / `requireRegistered` in `pkg/farm/farm.go`
- Type assertion: `basePlot.ConnectTo` in `pkg/plot/plot_base.go`

## How to Run

```bash
go test ./pkg/farm/ -run 'TestFarmAdd|TestFarmConnect' -v
```
