# pkg/farm/farm_start_test.go

> 📅 Last Updated: 2026/09/24

## Purpose

`farm_start_test.go` covers the end-to-end behavior of `Farm.Run` under the simplest "linear" topology: a single source, a single sink, and `Run` injecting multiple seeds. It verifies:

- all seeds are processed
- downstream results match the upstream transformation
- after all plots exit, the state is `done` (`int == 2`)

## Test Cases

### `TestFarmRunLinear`

- Creates `root` (`seed * 2`, `WithTenders(2)`) and `head` (collects seeds, `WithTenders(2)`).
- `Connect(root → head)`.
- Injects `{1, 2, 3}` into `root` during `Run`.
- Expectations:
  - `head` receives 3 seeds in total, `[2, 4, 6]` after sorting.
  - `root.GetState() == 2`, `head.GetState() == 2` (i.e. `Plot.notifyFinish` has fired).
- `head` internally uses a `sync.Mutex` to protect the shared `results` slice.

## Key Details

- **Concurrency safety**: in the test, the downstream `head`'s cultivator locks `results`; this matches the concurrent dispatch model of the `tend` goroutines in `pkg/plot`.
- **State machine verification**: `GetState() == 2` indicates that `Plot.sprout` has already exited `WaitAsync`, verifying that `Farm.Run`'s `WaitAsync` order returns only after all plots have finished.
- **Source seal not verified**: `sourceNodes` contains only 1 node (`root`), whose seal is injected via `Seal()` at the end of `Run`; the test indirectly confirms through the final state that `Seal` does not cause a regression of "forced termination before upstream is complete".

## Related Source

- The `Run` flow in `pkg/farm/farm.go`
- `Seed` / `Seal` / `sprout` / `tend` in `pkg/plot/plot_base.go`
- `sourceInput = "__input__"` in `pkg/plot/constant.go`

## How to Run

```bash
go test ./pkg/farm/ -run 'TestFarmRunLinear' -v
```
