# pkg/scarecrow/metrics_test.go

> 📅 Last Updated: 2026/11/13

## Purpose

`pkg/scarecrow/metrics_test.go` covers the counting rules and snapshot APIs of `MetricsWatcher`: input source classification, result counts and pending counts, status and timestamps, the batch view, and zero-value usability. It is the executable version of the field computation table in [`metrics.md`](./metrics.md).

## Test Distribution

| Test | Coverage |
|------|----------|
| `TestMetricsWatcherCountsInputs` | External input vs upstream input; also checks the receiver's `UpstreamCounts` and the source's `DownstreamCounts` |
| `TestMetricsWatcherCountsResults` | 4 inputs plus one ripened/withered/pruned event each, checking `Processed` and `Pending` |
| `TestMetricsWatcherTracksStatus` | `PlotStartEvent` → `running` with a zero `EndTime`; `PlotEndEvent` → `stopped` with an updated `EndTime` |
| `TestMetricsWatcherFarmView` | `GetPlots` returns snapshots of two Plots; the unregistered `ghost` returns `false` |
| `TestMetricsWatcherZeroValue` | `var metrics MetricsWatcher` can call `OnEvent` directly (`plots` is lazily initialized) |

## Key Assertions and Design Intent

### Input Sources: `TestMetricsWatcherCountsInputs`

```text
SeedInputEvent{Plot: "a", SeedID: 1}              // 外部注入
SeedInputEvent{Plot: "b", From: "a", SeedID: 2}   // a 投递给 b
```

| Assertion | Intent |
|------|------|
| `a.ExternalInput == 1`, `a.UpstreamInput == 0`, `a.InputTotal == 1` | Only `From == ""` counts as external input |
| `a.DownstreamCounts["b"] == 1` | The delivery is accounted on the **source** side |
| `b.ExternalInput == 0`, `b.UpstreamInput == 1`, `b.InputTotal == 1` | `UpstreamInput` is derived by summing the map |
| `b.UpstreamCounts["a"] == 1` | The receiver counts per source |

This test also verifies "two-sided accounting": one `SeedInputEvent` writes into two Plot cells (the receiver's `upstream` and the source's `downstream`), so even a source that never started appears in `GetPlots()`.

### Results and Pending: `TestMetricsWatcherCountsResults`

- After 4 consecutive external inputs, one ripened, one withered, and one pruned event are sent.
- Asserts `(Succeeded, Failed, Skipped) == (1, 1, 1)`, `Processed == 3`, `InputTotal == 4`, `Pending == 1`—directly locking in that `Processed` is the sum of the three and `Pending` is inputs minus processed.
- All `OnEvent` errors are ignored in this test (`_ =`), because that path produces no error.

### Status and Time: `TestMetricsWatcherTracksStatus`

- Reads the snapshot right after starting: `Status == PlotRunning`, `StartTime` equals the event time, `EndTime.IsZero()` is true—verifying "no end time is set before finishing".
- After finishing: `Status == PlotStopped`, `EndTime` equals the event time, and `StartTime` is not overwritten (the test reads only the status and `EndTime`, but they are independent fields in the implementation).
- Compares with `time.Time.Equal` rather than `==`, avoiding differences in the monotonic clock field.

### Batch View: `TestMetricsWatcherFarmView`

- Two Plots each send one start event; `GetPlots()` must have length 2 and both must be `running`.
- Calling `GetPlot` for `"ghost"` returns `false`, verifying "no data is invented for unregistered Plots".

### Zero Value: `TestMetricsWatcherZeroValue`

- With `var metrics MetricsWatcher` and no call to `NewMetricsWatcher`, `GetPlot("a")` after a direct `OnEvent` should return `ExternalInput == 1`. This covers the lazy-initialization branch in `plot()` where `m.plots == nil`.

## Running

```bash
go test ./pkg/scarecrow -run 'TestMetricsWatcher' -v
```

This file involves no concurrency or I/O, so `-race` is unnecessary; concurrency protection is provided by the `RWMutex` in the implementation, but the current tests never call it concurrently.

## Notes

- **The 0 lower-bound clamp of `Pending` is not covered**: every test satisfies `Processed <= InputTotal`, so the `pending < 0 → 0` branch for a stream with results but no inputs is untested; add a test yourself when changing that logic.
- **The `String()` default branch for an unknown `PlotStatus`** (returning `not_started`) is not covered, nor is the overwrite semantics when `PlotStartEvent` arrives twice.
- The tests build events directly without going through `Hub`, so they do not verify concurrent locking; whether a `GetPlot` snapshot can be safely kept long-term is guaranteed only by the implementation and is not asserted in tests.
- Assertions use whole field combinations (such as the triple `1/0/1`), so renaming a field fails compilation immediately; that chain reaction is expected.
