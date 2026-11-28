# pkg/reporter/snapshot_test.go

> 📅 Last Updated: 2026/11/26

## Purpose

`pkg/reporter/snapshot_test.go` verifies the projection and forwarding behavior of `SnapshotTicker`, and separately locks down the JSON field contract of `NodeStatus`. It makes no network requests: both `MetricsView` and `SnapshotSink` are replaced by local probes.

## Test distribution

| Test | Coverage |
|------|----------|
| `TestSnapshotTickerPushesProjectedSnapshot` | Field mapping of `PlotMetrics → NodeStatus`, `TickPeriod` return value, sink receiving the snapshot |
| `TestSnapshotTickerPropagatesSinkError` | Sink errors propagate as-is (`errors.Is` works) |
| `TestNewSnapshotTickerRejectsInvalidArguments` | nil MetricsView, nil sink, negative period |
| `TestNodeStatusJSONContract` | All 11 JSON keys exist after serialization |
| `TestToStatusSnapshotPreservesNodeSet` | Projection keeps every node key (including zero-value metrics) |

Test helpers:

```go
type stubMetricsView struct{ plots map[string]scarecrow.PlotMetrics }
func (m stubMetricsView) GetPlots() map[string]scarecrow.PlotMetrics // 复制计数字典后返回

type snapshotSinkProbe struct {
    snapshot map[string]NodeStatus
    err      error
}
func (s *snapshotSinkProbe) PushSnapshot(snapshot map[string]NodeStatus) error // copySnapshot 后保存并返回 s.err

func sortedKeys[V any](m map[string]V) []string
```

`stubMetricsView` applies `copyIntMap` item by item inside `GetPlots`, simulating the "return an immutable snapshot" semantics of `scarecrow.MetricsWatcher`; `snapshotSinkProbe` retains data with the production `copySnapshot`, so assertions are not affected by later mutations.

## Key assertions and design intent

### Projection mapping: `TestSnapshotTickerPushesProjectedSnapshot`

Input metrics:

```text
stage_a: Status=PlotStopped, StartTime=Unix(10,0), EndTime=Unix(20,0)
         InputTotal=7, Processed=6, Pending=1
         Succeeded=4, Failed=1, Skipped=1
         UpstreamCounts={"root":3}, DownstreamCounts={"leaf":2}
```

After constructing `NewSnapshotTicker(metricsView, sink, 2)`:

| Assertion | Intent |
|------|------|
| `TickPeriod() == 2` | An explicit period is not overridden by the default |
| `sink.snapshot["stage_a"]` exists | The node name is fully preserved as the map key |
| `Status == int(scarecrow.PlotStopped)` | `PlotStatus` is converted to the web-side integer code (`2`) |
| `TasksInput == 7`, `TasksProcessed == 6`, `TasksPending == 1` | The three core counters map correctly |
| `StartTime == 10.0`, `EndTime == 20.0` | `time.Time` is converted to Unix float seconds (no fraction on whole seconds) |

Note that for `time.Unix(10, 0)`, `Unix()` is 10 and `Nanosecond()` is 0, so the expected values are exactly `10.0`/`20.0` and can be compared with `!=` directly. `Succeeded`/`Failed`/`Skipped` and the upstream/downstream counters are not asserted in this test (although values are provided in the input).

### Error propagation: `TestSnapshotTickerPropagatesSinkError`

```go
want := errors.New("boom")
ticker, _ := NewSnapshotTicker(stubMetricsView{plots: map[string]scarecrow.PlotMetrics{}}, &snapshotSinkProbe{err: want}, 1)
if err := ticker.OnTick(metronome.TickEvent{Seq: 1}); !errors.Is(err, want) {
    t.Fatalf("OnTick() error = %v, want wrap %v", err, want)
}
```

- Empty metrics do not matter: `OnTick` first projects an empty map, then calls the sink.
- `errors.Is` matching shows that `OnTick` **does not re-wrap or replace** the sink error (it currently just `return`s it).
- The "want wrap" wording in the failure message deviates slightly from the actual behavior: the implementation does not wrap with `%w` but returns the error as-is; asserting with `errors.Is` holds for both.

### Constructor validation: `TestNewSnapshotTickerRejectsInvalidArguments`

| Input | Expected rejection reason |
|------|-------------|
| `(nil, &snapshotSinkProbe{}, 1)` | nil MetricsView |
| `(stubMetricsView{}, nil, 1)` | nil sink |
| `(stubMetricsView{}, &snapshotSinkProbe{}, -1)` | Negative period |

Only `err == nil` is checked and error text is not compared; the default replacement `period == 0 → 1` is also not covered (that branch can only be observed indirectly via `TickPeriod()`, which the tests do not do).

### JSON contract: `TestNodeStatusJSONContract`

It builds a `NodeStatus` with all fields filled, `json.Marshal`s it, deserializes into `map[string]map[string]any`, and checks each key for existence:

```text
status, tasks_input, tasks_processed, tasks_pending,
tasks_succeeded, tasks_failed, tasks_skipped,
upstream_counts, downstream_counts, start_time, end_time
```

- This is a **field-name regression test**: any JSON tag rename or field removal would break web-side parsing, and this test would raise the alarm immediately.
- Only "the key exists" is asserted; values are not compared, and extra fields are not checked; correctness of value mapping is covered by the previous test.
- Decoding into `any` with `float64`/`map` is deliberate: it simulates the shape after web-side JSON parsing rather than relying on the Go struct.

### Node set: `TestToStatusSnapshotPreservesNodeSet`

```go
snapshot := toStatusSnapshot(map[string]scarecrow.PlotMetrics{
    "a": {},
    "b": {},
})
// sortedKeys(snapshot) == []string{"a", "b"}
```

- Both metrics are zero values (`Status=PlotNotStarted`, zero times, zero counters), yet each must still produce a `NodeStatus` — projection must not drop nodes just because they "look empty".
- Keys are compared after sorting, avoiding the nondeterminism of map iteration order.
- It does not assert whether zero-value times are mapped to `0` (that is another branch of `toStatusSnapshot`).

## How to run

```bash
# 仅本文件的测试
go test ./pkg/reporter -run 'TestSnapshotTicker|TestNodeStatusJSONContract|TestToStatusSnapshot' -v

# 整个包
go test ./pkg/reporter -v
```

This file has no network and no concurrency, so `-race` is not needed; but `push_test.go` in the same package starts a local HTTP server, so when running the whole package make sure the loopback address is available.

## Notes

- **Zero-value time branch not covered**: `StartTime.IsZero()` / `EndTime.IsZero()` should output `0`, and none of the 5 tests assert this (`TestToStatusSnapshotPreservesNodeSet` only checks keys).
- **Upstream/downstream counters not covered**: the mapping of `UpstreamCounts` / `DownstreamCounts` has no assertion in this file; `push_test.go` also only checks empty adjacency-list encoding.
- **Default period not covered**: `period == 0 → 1` has no case.
- **JSON shape of empty maps not covered**: `toStatusSnapshot` itself does not guarantee that `upstream_counts` is encoded as `{}`; that relies on `copySnapshot`/`copyIntMap` and is not asserted at the snapshot level (`push_test.go` only asserts the empty slice of `edges`).
- **Typed-nil interfaces not covered**: the constructor only does an `== nil` check, so passing `(*stub)(nil)` is not rejected; the tests do not pin this behavior.
