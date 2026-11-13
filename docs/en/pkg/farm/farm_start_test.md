# pkg/farm/farm_start_test.go

> 📅 Last Updated: 2026/11/13

## Purpose

`farm_start_test.go` covers the end-to-end behavior of `Farm.Run` under the simplest "linear" topology: a single source, a single sink, where `Run` internally injects seeds through `InjectTasks` and terminates the source through `InjectTerminations`, verifying:

- all seeds are processed
- downstream results match the upstream transformation
- after all plots exit, the state is `done` (`int == 2`)

## Test Cases

### `TestFarmRunLinear`

- Creates `root` (`seed * 2`, `WithTenders(2)`) and `head` (collects seeds, `WithTenders(2)`).
- `Connect(root → head)`.
- During `Run`, injects `{1, 2, 3}` into `root` and passes `"INFO"` as the log level.
- Expectations:
  - `head` receives 3 seeds in total, `[2, 4, 6]` after sorting.
  - `root.GetState() == 2`, `head.GetState() == 2` (i.e. `notifyFinish` has fired).
- `head` internally uses a `sync.Mutex` to protect the shared `results` slice.

## Key Details

- **Concurrency safety**: in the test, the downstream `head`'s cultivator locks `results`; this matches the concurrent dispatch model of the `tend` goroutines in `pkg/plot`.
- **State machine verification**: `GetState() == 2` indicates that `sprout` has exited and `WaitAsync` has returned, verifying the wait order of `Farm.start`.
- **Success path only**: `Run` takes the termination fallback only when injection fails; all seeds in this case have matching types, so what is verified is the normal "inject seeds → terminate source → wait" chain.
- **source seal**: `sourceNodes` contains only `root`, and `Run` calls `Seal()` on it automatically; through the final state the test indirectly confirms there is no regression of "forced termination before all upstream nodes arrive".

## Related Source

- `Run` / `start` / `InjectTasks` / `InjectTerminations` in `pkg/farm/farm.go`
- `Seed` / `Seal` / `sprout` / `tend` in `pkg/plot/plot.go`
- `sourceInput = "__input__"` in `pkg/plot/constant.go`

## How to Run

```bash
go test ./pkg/farm/ -run 'TestFarmRunLinear' -v
```
