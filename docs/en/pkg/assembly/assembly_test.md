# pkg/assembly/assembly_test.go

> 📅 Last Updated: 2026/11/13

This file is the end-to-end assembly test of `pkg/assembly`. It uses the external test package `assembly_test` (accessing only through the public API, never touching unexported fields). Both test functions are driven by a `starters` table and run once against `StartPlotResources` and once against `StartFarmResources`, ensuring that the two entries behave identically.

The core verification chain of the tests is: **create a Hub → assemble resources → send events to the Hub → Stop → retrieve the lifecycle observer from the Hub → read back the state snapshots by database path**. It therefore covers Hub registration, event dispatch, SQLite persistence, and the idempotency of `Stop`.

## Test Coverage Points

| Test | Capability covered | Key invariants |
|------|----------|-----------|
| `TestStartResourcesAssembleLifecycle` | Full assembly of both entries + event persistence + repeated Stop + post-run query | Assembly returns a non-nil `Resources` on success; `SeedInputEvent` is written to SQLite through the Hub; calling `Stop` twice in a row does not panic; `scarecrow.Find` can retrieve the `*persist.LifecycleWatcher`; `DBPath()` is non-empty; the snapshot state is `seed` and `SeedJSON` is `"42"` |
| `TestStartResourcesRejectNilHub` | Nil boundary of both entries | With `hub == nil` it returns a non-nil error and `Resources` is nil |

Every case carries its own subtest name (`StartPlotResources` / `StartFarmResources`), so a failure points directly at the entry.

## Key Test Details

### Shared table-driven fixture

```go
type startFunc func(hub *scarecrow.Hub, logLevel string) (*assembly.Resources, error)

var starters = []struct {
	name  string
	start startFunc
}{
	{"StartPlotResources", assembly.StartPlotResources},
	{"StartFarmResources", assembly.StartFarmResources},
}
```

- `startFunc` spells out the shared signature of the two entries and passes the entries around as values; if the signatures ever diverge, compilation fails here first.
- `starters` is a package-level variable shared by both test functions. When a new assembly entry is added, appending a single line here gives it coverage in both tests automatically.
- Both entries currently forward to `startResources`, so this arrangement actually verifies that "the two entries have not diverged".

### `TestStartResourcesAssembleLifecycle`

For each entry it executes:

```go
hub := scarecrow.NewHub(scarecrow.WithErrorHandler(func(err error) {
	t.Errorf("unexpected watcher error: %v", err)
}))

resources, err := starter.start(hub, "DEBUG")
if err != nil {
	t.Fatalf("%s() = %v", starter.name, err)
}

now := time.Now()
if err := hub.OnEvent(scarecrow.SeedInputEvent{
	Plot: "node", Seed: 42, SeedRepr: "42", SeedID: 1, Time: now,
}); err != nil {
	t.Fatalf("OnEvent() = %v", err)
}

resources.Stop()
resources.Stop() // idempotent
```

Key points:

1. **The Hub's error handler fails the test directly**: `Hub.OnEvent` routes observer failures only through the `ErrorHandler` and always returns nil, so the handler is the only way to catch "an observer errored while processing an event"; otherwise observer failures would be silently swallowed.
2. **The log level is `DEBUG`**: `SeedInputEvent` maps to a `DEBUG`-level log in `LogWatcher`; passing `DEBUG` guarantees the event is not filtered out by the minimum level (this test does not check the log file contents).
3. **`Stop` is called twice in a row**: this verifies `Resources.stopOnce`. The underlying `funnel.Spout.Stop` calls `close` on the queue channel, so calling it twice directly would panic with `close of closed channel`; this line therefore guards the idempotent implementation.
4. **Stop before querying**: `Stop` waits for the consumption goroutine to drain the queue and closes SQLite; afterwards `LoadLifecycleStatusesFromPath` reopens the database file by path, avoiding reading data that has not been flushed.

Query and assertions:

```go
lifecycle, ok := scarecrow.Find[*persist.LifecycleWatcher](hub)
if !ok {
	t.Fatal("lifecycle watcher is not registered")
}
dbPath := lifecycle.DBPath()
if dbPath == "" {
	t.Fatal("lifecycle database path is empty")
}

records, err := persist.LoadLifecycleStatusesFromPath(dbPath, "node")
if err != nil {
	t.Fatalf("LoadLifecycleStatusesFromPath() = %v", err)
}
if len(records) != 1 {
	t.Fatalf("records = %d, want 1", len(records))
}
if records[0].Status != "seed" || records[0].SeedJSON != "42" {
	t.Fatalf("record = %#v, want seed/42", records[0])
}
```

- `scarecrow.Find` looks for the first `*persist.LifecycleWatcher` in the Hub and its nested Hubs, verifying that the observer was indeed registered on **the hub passed in** (registered directly at the Plot level, and likewise on the Farm Hub at the Farm level).
- A non-empty `DBPath()` shows that `LifecycleWatcher.Start` has already reached `BeforeStart` and created the SQLite file; combined with a successful `Find`, it confirms that the observer is both started and registered.
- `len(records) == 1` verifies that this plot has exactly one state snapshot corresponding to the input event (snapshots are upserted with the input event ID as the primary key), and also shows that the event really was consumed and persisted.
- `Status == "seed"` because only a `SeedInputEvent` was sent, with no subsequent ripen / wither / prune event; `SeedJSON == "42"` comes from `persist`'s JSON serialization of the `Seed` field (`int` 42 → `42`).

### `TestStartResourcesRejectNilHub`

```go
resources, err := starter.start(nil, "INFO")
if err == nil {
	t.Fatalf("%s(nil) = nil, want error", starter.name)
}
if resources != nil {
	t.Fatalf("%s(nil) resources = %#v, want nil", starter.name, resources)
}
```

- It only asserts the two contracts "there is an error" and "the resources are nil", without matching the concrete error text, so changes to the error message do not break the test.
- Looking at the implementation, this path returns before creating any observer, so it produces no directory or goroutine side effects; the test itself only asserts the return-value contract of "an error plus nil resources".

## How to Run

```bash
# run all tests of this package
go test ./pkg/assembly/...

# run only this file
go test ./pkg/assembly -run 'TestStartResources'

# inspect the subtests of the two entries
go test ./pkg/assembly -run 'TestStartResources' -v
```

## Notes

- **Runtime artifacts are left behind**: the tests set no working directory, so `LifecycleRecordHandler` / `LogRecordHandler` create `lifecycles/<date>/grow_lifecycle(<time>).sqlite3` and `logs/grow_log(<date>).log` by relative path, located in the working directory where `go test` runs (usually `pkg/assembly/`). The tests have no cleanup logic; these directories are already ignored by `.gitignore`.
- **Uncovered paths**:
  - the contents of the log observer (whether records are really written to the file, whether level filtering is correct) are not asserted;
  - the aggregation results of the metrics observer (`GetPlot` / `GetPlots`) are not asserted;
  - the various rollback branches of a failed startup (`lifecycle.Start` / `log.Start` / `AddWatcher` failures) are not covered, and would require injecting a failing handler or an unwritable directory to trigger;
  - the registration order and the number of the two internal observers of `Resources` are not explicitly asserted; `Find` can only prove that the lifecycle observer exists.
- **`Stop` errors are discarded**: the test calls `resources.Stop()` without receiving a return value, because the method itself returns nothing; an underlying flush timeout does not fail the test either.
- **The error handler makes observer failures fail immediately**: calling `t.Errorf` inside `WithErrorHandler` only affects the current subtest; if an observer panics, the Hub wraps it into an error and hands it to that handler, which is also caught.
- **The tests rely on a real filesystem and the SQLite driver**: `modernc.org/sqlite` must be available; if the working directory is not writable or the disk is full, `Start` returns an error and the first test fails.
