# pkg/persist/sqlite_test.go

> 📅 Last Updated: 2026/09/24

## Purpose

`pkg/persist/sqlite_test.go` is the **only** test file in the `pkg/persist` package, and it performs end-to-end verification only for the SQLite storage layer described in [`sqlite.md`](./sqlite.md); it does not touch `LifecycleRecordHandler` / `LifecycleInlet` from [`lifecycle.md`](./lifecycle.md) or the log persistence of [`log.md`](./log.md). All three test functions use `t.TempDir()` to obtain an independent temporary file as the entry point of `OpenLifecycleSQLite`, ensuring that tests do not affect each other and have no side effects.

> The current file has **no** unit tests for the `LifecycleRecordHandler.HandleRecord` routing (`seed` / `ripen` / `wither`) or for `LogInlet` / `LogRecordHandler`; if they are added later, given the single-goroutine scheduling of `pkg/funnel` Spout, they should be driven with an in-memory channel plus a synchronous hand-off.

## Test Coverage Points

| Test | Capability covered | Key invariant |
|------|----------|-----------|
| `TestLifecycleSQLiteEventRoundTrip` | Open DB → write seed + parent edge → read back event and parent edges | The `events` primary key is writable; the many-to-many edges of `event_parents` are writable; `LoadLifecycleEvent` / `LoadLifecycleEventParents` behave symmetrically |
| `TestLifecycleSQLiteStatusRoundTrip` | Write two events → `UpsertLifecycleStatusSeed` → `PromoteLifecycleStatusRipen` → read back | The status snapshot can be inserted; promotion only refreshes `current_event_id` / `status` / `fruit_json` / `ts`, preserving `seed_json` and `plot` |
| `TestLifecycleSQLiteStatusPairs` | Mixed insertion of multiple plots and events → several inserts + one ripen + one wither → query by plot dimension | `LoadLifecycleStatuses` is ascending by `ts, input_event_id`; the `plot` filter is accurate; the `Status` / `SeedJSON` / `FruitJSON` / `WitherType` / `WitherMessage` fields all land correctly |

## Key Test Details

### Shared fixture and cleanup

```go
dbPath := filepath.Join(t.TempDir(), "lifecycle.sqlite3")
db, err := OpenLifecycleSQLite(dbPath)
if err != nil { t.Fatalf("OpenLifecycleSQLite() error = %v", err) }
defer db.Close()
```

- Each test requests its own `t.TempDir()`, and `lifecycle.sqlite3` is created by SQLite on that side.
- `defer db.Close()` closes the database before the test function returns, triggering the WAL flush; `t.TempDir()` is cleaned up automatically after the test, so no manual `os.Remove` is needed.
- Because `OpenLifecycleSQLite` internally triggers the PRAGMA + schema script, **the test also verifies the connection configuration and the schema creation path**.

### `TestLifecycleSQLiteEventRoundTrip`

```go
InsertLifecycleEvent(db, 1, "seed", "source", 1.0, nil)
InsertLifecycleEvent(db, 2, "fruit", "source", 2.0, []int{1})

loadedEvent, _ := LoadLifecycleEvent(db, 2)
parentIDs, _ := LoadLifecycleEventParents(db, 2)
```

- It writes a `seed` event with no parent edge (`parentIDs == nil`) and a `fruit` event with 1 parent edge.
- Read-back assertions: `loadedEvent.EventType == "fruit"`, `parentIDs == []int{1}`.
- It verifies that "the event body + parent edges" are written in the same transaction and can be read out independently.

### `TestLifecycleSQLiteStatusRoundTrip`

```go
UpsertLifecycleStatusSeed(db, 1, `{"value":"alpha"}`, "stage_a", 1.0)
PromoteLifecycleStatusRipen(db, 1, 3, `{"ok":true}`, 3.0)

loadedStatus, _ := LoadLifecycleStatus(db, 1)
// 断言: CurrentEventID == 3, Status == "ripen", FruitJSON == `{"ok":true}`
```

- The preceding events are `{EventID: 1, EventType: "seed", Plot: "stage_a", TS: 1.0}` and `{EventID: 3, EventType: "fruit", Plot: "stage_a", TS: 3.0}` (written in a loop over a `[]LifecycleEventRecord` literal).
- It walks the whole chain from the `seed` snapshot to the `ripen` promotion.
- It verifies that `PromoteLifecycleStatusRipen` changes **only** `current_event_id` / `status` / `fruit_json` / `ts` and does not break `seed_json` or `plot`.
- It also verifies that the column order of `LoadLifecycleStatus` corresponds one-to-one with the fields of `LifecycleStatusRecord` (including `WitherType` / `WitherMessage` read out as empty strings).

### `TestLifecycleSQLiteStatusPairs`

```go
// stage_a: event 1(seed) -> 2(fruit), event 3(seed) -> 4(weed)
// stage_b: event 5(seed) -> 6(fruit)
UpsertLifecycleStatusSeed(db, 1, `{"value":"alpha"}`, "stage_a", 1.0) + PromoteLifecycleStatusRipen(1, 2, `{"ok":true}`, 2.0)
UpsertLifecycleStatusSeed(db, 3, `{"value":"beta"}`,  "stage_a", 3.0) + PromoteLifecycleStatusWither(3, 4, "*errors.errorString", "boom", 4.0)
UpsertLifecycleStatusSeed(db, 5, `{"value":"gamma"}`, "stage_b", 5.0) + PromoteLifecycleStatusRipen(5, 6, `{"ok":"other"}`, 6.0)

statuses, _ := LoadLifecycleStatuses(db, "stage_a")
// 断言: len(statuses) == 2;
//   [0] = alpha/ripen/{"ok":true}
//   [1] = beta/wither/*errors.errorString/"boom"
```

- It verifies three things at once:
  1. **Filtering by plot**: the snapshot of `stage_b` does not appear in the query result for `stage_a`.
  2. **Reading mixed statuses**: both the `ripen` and the `wither` snapshot deserialize correctly.
  3. **The ordering convention**: `LoadLifecycleStatuses` internally does `ORDER BY ts, input_event_id`, and the result is ascending by `ts` (`1.0` before `3.0`).
- The error message uses `*errors.errorString` (the type produced by the standard library `errors.New`), and both `WitherType` / `WitherMessage` are persisted in `fmt.Sprintf` form.

## How to Run

```bash
# 运行本包全部测试
go test ./pkg/persist/...

# 仅运行本文件
go test ./pkg/persist -run 'TestLifecycleSQLite'

# 带 -v 查看每个用例的子断言
go test ./pkg/persist -run 'TestLifecycleSQLite' -v
```

## Notes

- **Uncovered paths**: the current tests do **not** directly cover the `seed` / `ripen` / `wither` routing of `HandleRecord`, nor do they verify the branch of `LoadStatuses` that reopens by `SQLitePath` when `sqliteDB == nil`. Additional tests can be driven with `pkg/funnel.NewSpout` + `chan LifecycleRecord`.
- **The `EventType` in the tests differs from the value written by the business code**: the tests use `"fruit"` / `"weed"` as `events.event_type` to build the event graph, while `HandleRecord` in `lifecycle.go` actually writes `"ripen"` / `"wither"`; `events.event_type` has no CHECK constraint, so both pass. The tests only verify the storage layer, not the enumeration, so do not be misled by the literals in this file when changing the event type naming.
- **Status values follow the `Promote*` functions**: the statuses asserted in the tests are `"ripen"` / `"wither"` (consistent with the hardcoded `status` in `PromoteLifecycleStatusRipen` / `PromoteLifecycleStatusWither`), and the initial snapshot status is `"seed"`.
- **The tests depend on the SQLite driver**: `go test` resolves `import _ "modernc.org/sqlite"` through `modernc.org/sqlite`, and the driver is already declared in `go.mod` (`v1.57.0`), so no extra preparation is needed.
- **The temporary directory is cleaned up automatically**: `t.TempDir()` is deleted when the test ends, so it is cleaned up even if the test calls `t.Fatalf` midway; keeping `defer db.Close()` is still recommended to avoid leaving WAL auxiliary files in the temporary directory.
- **Error message matching**: `*errors.errorString` is the internal type name generated by `errors.New`, and the `WitherType` of a third-party error wrapper (such as `fmt.Errorf("%w", ...)`) will be something like `*fmt.wrapError`; do not copy the assertions of this test onto custom errors.
- **PRAGMA side effects**: the tests enable WAL in `OpenLifecycleSQLite`, so `lifecycle.sqlite3-wal` / `lifecycle.sqlite3-shm` appear in the temporary directory; this does not affect the assertions, but reading the same path across processes requires `db.Close()` first.
