# pkg/persist/lifecycle_test.go

> 📅 Last Updated: 2026/11/13

## Purpose

`pkg/persist/lifecycle_test.go` verifies the **event translation** of `LifecycleWatcher`: whether a `scarecrow` event, after passing through `OnEvent`, is correctly converted into a `LifecycleRecord` and delivered to the queue. The test does not open SQLite; instead it uses the package-private constructor `newLifecycleWatcher(handler)` to inject a capturing handler, bypassing the file side effects of `LifecycleRecordHandler`.

## Test Fixtures

```go
type captureLifecycleHandler struct {
    mu      sync.Mutex
    records []LifecycleRecord
}

func (h *captureLifecycleHandler) BeforeStart() error { return nil }
func (h *captureLifecycleHandler) HandleRecord(record LifecycleRecord) error { /* 加锁追加 */ }
func (h *captureLifecycleHandler) AfterStop() error { return nil }
func (h *captureLifecycleHandler) snapshot() []LifecycleRecord { /* 加锁拷贝 */ }
```

- It implements `funnel.RecordHandler[LifecycleRecord]` but performs **no IO at all**, only appending records to a slice.
- `sync.Mutex` protects `records`: `HandleRecord` runs in the `Spout` background goroutine while `snapshot` reads in the main test goroutine.
- `snapshot` returns a copy so that background writes cannot interfere during test assertions.

> After a custom handler is injected, `DBPath()` returns an empty string (it only recognizes the default `*LifecycleRecordHandler`), so this test does not involve the database path.

## Test Coverage Points

| Test | Capability covered | Key invariant |
|------|----------|-----------|
| `TestLifecycleWatcherTranslatesEvents` | Delivers 7 events, covering 4 seed result event types + 2 ignored event types | Only 5 records are produced; `Kind` / `InputEventID` / `CurrentEventID` / `ParentIDs` / JSON payload / timestamp all follow the mapping rules |

## `TestLifecycleWatcherTranslatesEvents`

### How it is driven

```go
handler := &captureLifecycleHandler{}
obs := newLifecycleWatcher(handler)
if err := obs.Start(); err != nil { t.Fatalf(...) }

now := time.UnixMilli(1_700_000_000_123)
events := []scarecrow.Event{
    scarecrow.SeedInputEvent{Plot: "a", Seed: 1, SeedID: 1, Time: now},
    scarecrow.SeedRipenEvent{Plot: "a", Seed: 1, Fruit: 2, SeedID: 1, FruitID: 2, Time: now},
    scarecrow.SeedInputEvent{Plot: "b", From: "a", Seed: 2, SeedID: 3, Parents: []int{2}, Time: now},
    scarecrow.SeedWitherEvent{Plot: "b", Seed: 2, Err: errors.New("boom"), SeedID: 3, WeedID: 4, Time: now},
    scarecrow.SeedPruneEvent{Plot: "b", Seed: 9, SeedID: 5, PruneID: 6, Time: now},
    scarecrow.PlotStartEvent{Plot: "b", StartTime: now},
    scarecrow.SealMergeEvent{Plot: "b", ParentIDs: []int{7}, SealID: 8, Time: now},
}
for _, event := range events {
    if err := obs.OnEvent(event); err != nil { t.Fatalf(...) }
}

if err := obs.Stop(); err != nil { t.Fatalf(...) }
records := handler.snapshot()
if len(records) != 5 { t.Fatalf(...) }
```

- It first calls `Start` to launch the consume goroutine, then `OnEvent` one by one, and finally `Stop` to drain the queue before `snapshot` — reading the slice directly would be unstable because consumption is asynchronous.
- The fixed timestamp `1_700_000_000_123` milliseconds is used to verify the millisecond precision of `TS`.

### Assertions

| Index | Source event | `Kind` | `InputEventID` | `CurrentEventID` | Other assertions |
|:----:|----------|--------|----------------|------------------|----------|
| 0 | `SeedInputEvent` (plot `a`) | `seed` | 1 | 1 | `PlotName == "a"`, `SeedJSON == "1"`, `ParentIDs == nil`, `TS ≈ 1700000000.123` (tolerance `1e-6`) |
| 1 | `SeedRipenEvent` | `ripen` | 1 | 2 | `FruitJSON == "2"`, `ParentIDs == [1]` |
| 2 | `SeedInputEvent` (plot `b`, `Parents: [2]`) | `seed` | 3 | 3 | `PlotName == "b"`, `SeedJSON == "2"`, `ParentIDs == [2]` |
| 3 | `SeedWitherEvent` | `wither` | 3 | 4 | `WitherMessage == "boom"`, `WitherType != ""` |
| 4 | `SeedPruneEvent` | `prune` | 5 | 6 | — |

- `PlotStartEvent` and `SealMergeEvent` are explicitly ignored by the watcher, so 7 events produce only 5 records — this is the indirect assertion of the "ignore branch".
- Index 2 covers the "upstream delivery" scenario: the `From` field does not affect persistence and the parent edges come from `Parents`; when `Seed` is `2`, `SeedJSON` is exactly `"2"`.
- Index 3 covers the error fields: `WitherType` is produced by `%T` and is only asserted to be non-empty (the concrete type name is stable across Go versions but not worth hardcoding), while `WitherMessage` asserts the error text.
- The `Seed` field of index 4 is deliberately set to `9`, but prune records do not write the seed JSON, showing that status advancement does not depend on that field.

## How to Run

```bash
# Run this file
go test ./pkg/persist -run 'TestLifecycleWatcher'

# Show detailed results
go test ./pkg/persist -run 'TestLifecycleWatcher' -v

# Run the whole package
go test ./pkg/persist/...
```

## Notes

- **`Stop` is the synchronization point**: `Spout.Stop` closes the channel and waits for the consume loop to drain (at most 1s), and only then can `snapshot` see all records; if you only call `OnEvent` without `Stop`, the assertions fail randomly because records have not been consumed yet.
- **`Start` cannot be omitted**: `OnEvent` itself only delivers to the `Inlet`, and consumption depends on the goroutine started by `Start`.
- **Persistence is not covered**: `captureLifecycleHandler` writes no SQL; the Kind routing of `HandleRecord` and the call order of `InsertLifecycleEvent` / `Promote*` are guaranteed by [`sqlite_test.md`](./sqlite_test.md) and the source code; this test only locks down the field mapping of "event → record".
- **Error branches are not covered**: errors such as `unsupported lifecycle operation` and `生命周期 sqlite 未初始化` are not asserted in this file; `OnEvent` returns `nil` for unrecognized events, so there is no error path to test either.
- **Time assertions use a `math.Abs` tolerance**: `TS` is `float64(UnixMilli())/1000`, so floating-point comparison must leave a tolerance (the test uses `1e-6`).
