# pkg/assembly/assembly_test.go

> 📅 Last Updated: 2026/11/26

This file is the end-to-end assembly test of `pkg/assembly`, using the external test package `assembly_test` (accessing only through the public API, never touching unexported fields). The tests cover three paths — local assembly, remote reporting assembly, and argument validation — with 5 test functions plus 2 helper definitions.

## Test Coverage Points

| Test | Capability covered | Key invariants |
|------|----------|-----------|
| `TestStartLocalResourcesAssembleLifecycle` | Local assembly + event persistence + repeated Stop + post-run query | Returns a non-nil `StopFunc`; `SeedInputEvent` is written to SQLite through the Hub; two consecutive `stop()` calls both return `nil`; `scarecrow.Find` can retrieve the `*persist.LifecycleWatcher`; `DBPath()` is non-empty; the snapshot status is `seed` and `SeedJSON` is `"42"` |
| `TestStartReportResourcesAssembleReporter` | Remote assembly + push path | `*reporter.PushWatcher` is registered on the watcher Hub; `*reporter.SnapshotTicker` and `*reporter.InjectionTicker` are registered on the ticker Hub; `FarmStartEvent` eventually triggers `POST /api/push_graph_meta` |
| `TestStartLocalResourcesRejectNilHub` | watcher Hub boundary of the local entry | Returns a non-nil error when `watcherHub == nil` |
| `TestStartLocalResourcesRejectNilTickerHub` | ticker Hub boundary of the local entry | Returns a non-nil error when `tickerHub == nil` |
| `TestStartReportResourcesRejectInvalidArguments` | argument boundaries of the remote entry | `report == nil` and `injectionTarget == nil` each return a non-nil error |

The core chain of the tests is: **create two Hubs → assemble resources → send an event to the Hub → Stop → retrieve the observer from the Hub → read back the state snapshots by database path / wait for the HTTP request to arrive**.

## Key Test Details

### Helper definitions

```go
func waitForPath(t *testing.T, paths <-chan string, want string) {
    t.Helper()
    timeout := time.After(3 * time.Second)
    for {
        select {
        case got := <-paths:
            if got == want {
                return
            }
        case <-timeout:
            t.Fatalf("did not receive %s", want)
        }
    }
}

type injectionTargetStub struct{}

func (injectionTargetStub) InjectTasks(map[string][]any) error { return nil }
func (injectionTargetStub) InjectTerminations([]string) error  { return nil }
```

- `waitForPath` allows up to 3 seconds for the asynchronous push and ignores non-matching paths.
- `injectionTargetStub` implements the two methods of `reporter.InjectionTarget`, avoiding a dependency on a real `*farm.Farm` in the remote test.

### `TestStartLocalResourcesAssembleLifecycle`

```go
hub := scarecrow.NewHub(scarecrow.WithErrorHandler(func(err error) {
    t.Errorf("unexpected watcher error: %v", err)
}))
tickerHub, err := metronome.NewHub(metronome.WithErrorHandler(func(err error) {
    t.Errorf("unexpected ticker error: %v", err)
}))
if err != nil {
    t.Fatalf("NewHub() = %v", err)
}

stop, err := assembly.StartLocalResources(hub, tickerHub, "DEBUG", metronome.DefaultInterval)
if err != nil {
    t.Fatalf("StartLocalResources() = %v", err)
}

now := time.Now()
if err := hub.OnEvent(scarecrow.SeedInputEvent{
    Plot: "node", Seed: 42, SeedRepr: "42", SeedID: 1, Time: now,
}); err != nil {
    t.Fatalf("OnEvent() = %v", err)
}

if err := stop(); err != nil {
    t.Fatalf("stop() = %v", err)
}
if err := stop(); err != nil {
    t.Fatalf("second stop() = %v", err)
}
```

Key points:

1. **Both Hubs' error handlers fail the test directly**: `Hub.OnEvent` routes observer failures only through the `ErrorHandler` and always returns `nil`, so the handler is the only way to catch "an observer errored while processing an event".
2. **The log level is `DEBUG`**: `SeedInputEvent` is mapped to a `DEBUG`-level log in `LogWatcher`; passing `DEBUG` guarantees the event is not filtered out by the minimum level (this test does not check the log file contents).
3. **`StopFunc` is called twice in a row and both must return `nil`**: this verifies the `stopped` idempotency guard. The underlying `funnel.Spout.Stop` calls `close` on the queue channel, so calling it twice directly would panic with `close of closed channel`; these two lines therefore guard the idempotent implementation.
4. **Stop before querying**: `Stop` waits for the consumption goroutine to finish the queue and closes SQLite; afterwards `LoadLifecycleStatusesFromPath` reopens the database file by path, avoiding reading data that has not been flushed.

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

- `scarecrow.Find` looks for the first `*persist.LifecycleWatcher` in the Hub and its nested Hubs, verifying that the observer was indeed registered on **the hub passed in**.
- A non-empty `DBPath()` shows that `LifecycleWatcher.Start` has already reached `BeforeStart` and created the SQLite file; combined with a successful `Find`, it confirms that the observer is both started and registered.
- `len(records) == 1` verifies that this plot has exactly one state snapshot corresponding to the input event.
- `Status == "seed"` because only a `SeedInputEvent` was sent, with no subsequent ripen / wither / prune event; `SeedJSON == "42"` comes from `persist`'s JSON serialization of the `Seed` field (`int` 42 → `42`).

### `TestStartReportResourcesAssembleReporter`

The test first starts a local service that records request paths with `httptest.NewServer`:

```go
paths := make(chan string, 16)
server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
    select {
    case paths <- r.URL.Path:
    default:
    }
    w.WriteHeader(http.StatusOK)
}))
defer server.Close()
```

During assembly, `server.URL` and the session identifier are passed to `reporter.Config`:

```go
stop, err := assembly.StartReportResources(
    hub,
    tickerHub,
    injectionTargetStub{},
    "INFO",
    metronome.DefaultInterval,
    &reporter.Config{URL: server.URL, SessionID: "farm@1"},
)
if err != nil {
    t.Fatalf("StartReportResources() = %v", err)
}
defer func() {
    if err := stop(); err != nil {
        t.Fatalf("stop() = %v", err)
    }
}()
```

After assembly, it first checks that all three kinds of remote resources are registered:

```go
if _, ok := scarecrow.Find[*reporter.PushWatcher](hub); !ok {
    t.Fatal("push watcher is not registered")
}
if _, ok := metronome.Find[*reporter.SnapshotTicker](tickerHub); !ok {
    t.Fatal("snapshot ticker is not registered")
}
if _, ok := metronome.Find[*reporter.InjectionTicker](tickerHub); !ok {
    t.Fatal("injection ticker is not registered")
}
```

Then it sends a Farm start event and waits for the push to actually reach the server:

```go
if err := hub.OnEvent(scarecrow.FarmStartEvent{
    Farm:        "farm",
    Plots:       []string{"root"},
    Edges:       map[string][]string{"root": {}},
    SourcePlots: []string{"root"},
    StartTime:   time.Now(),
}); err != nil {
    t.Fatalf("OnEvent(FarmStartEvent) = %v", err)
}

waitForPath(t, paths, "/api/push_graph_meta")
```

- `PushWatcher.OnEvent` only writes the record into the background queue; the HTTP request is issued by the consumption goroutine of `Spout`, so it must be awaited with `waitForPath` and cannot be asserted immediately.
- The endpoint path comes from the fixed mapping in `pushHandler.handle`: `FarmStartEvent → /api/push_graph_meta`. The test does not assert the request body.
- `SnapshotPeriod` / `InjectionPeriod` are not set (zero value) and fall back to the default periods 1 / 5 respectively. The test only waits for the graph metadata push and does not assert on periodic requests; during the wait the snapshot ticker may also emit `/api/push_snapshot`, which `waitForPath` ignores as a non-matching path.
- `defer stop()` is registered immediately after successful assembly, ensuring the consumption loops and clock are stopped before the test ends; event sending and assertions both complete before the `defer` runs.

### `TestStartLocalResourcesRejectNilHub`

```go
tickerHub, err := metronome.NewHub()
if err != nil {
    t.Fatalf("NewHub() = %v", err)
}
if _, err := assembly.StartLocalResources(nil, tickerHub, "INFO", metronome.DefaultInterval); err == nil {
    t.Fatal("StartLocalResources(nil, tickerHub) = nil, want error")
}
```

- It only asserts "there is an error" without matching the concrete error text, so changes to the error message do not break the test.
- This path returns before creating any observer, so it produces no directory or goroutine side effects.

### `TestStartLocalResourcesRejectNilTickerHub`

```go
watcherHub := scarecrow.NewHub()
if _, err := assembly.StartLocalResources(watcherHub, nil, "INFO", metronome.DefaultInterval); err == nil {
    t.Fatal("StartLocalResources(watcherHub, nil) = nil, want error")
}
```

- Covers the second precondition check, where the ticker Hub is nil; likewise it only asserts that an error exists.

### `TestStartReportResourcesRejectInvalidArguments`

```go
if _, err := assembly.StartReportResources(
    watcherHub,
    tickerHub,
    injectionTargetStub{},
    "INFO",
    metronome.DefaultInterval,
    nil,
); err == nil {
    t.Fatal("nil report config must be rejected")
}

if _, err := assembly.StartReportResources(
    watcherHub,
    tickerHub,
    nil,
    "INFO",
    metronome.DefaultInterval,
    &reporter.Config{URL: "http://example.com", SessionID: "farm@1"},
); err == nil {
    t.Fatal("nil injection target must be rejected")
}
```

- Covers the two validation branches `report == nil` and `injectionTarget == nil` in turn.
- Both calls happen before any observer is registered, so they do not pollute later cases (each test also creates its own Hubs).

## How to Run

```bash
# 运行本包全部测试
go test ./pkg/assembly/...

# 仅运行本地装配相关用例
go test ./pkg/assembly -run 'TestStartLocalResources'

# 仅运行远程装配相关用例
go test ./pkg/assembly -run 'TestStartReportResources'

# 查看详细结果
go test ./pkg/assembly -v
```

## Notes

- **Runtime artifacts are left behind**: `TestStartLocalResourcesAssembleLifecycle` sets no working directory, so `LifecycleRecordHandler` / `LogRecordHandler` create `lifecycles/<date>/grow_lifecycle(<time>).sqlite3` and `logs/grow_log(<date>).log` by relative path, located in the working directory where `go test` runs (usually `pkg/assembly/`). The tests have no cleanup logic; these directories are already ignored by `.gitignore`.
- **Uncovered paths**:
  - the contents of the log observer (whether records are really written to the file, whether level filtering is correct) are not asserted;
  - the aggregation results of the metrics observer (`GetPlot` / `GetPlots`) are not asserted;
  - the push request body, the request method, and failed retries are not asserted, only that the path arrives;
  - the periodic triggering of the snapshot ticker and injection ticker, and the remote injection interface, are not driven;
  - the various rollback branches of a failed startup (`AddWatcher` / `AddTicker` / `Hub.Start` failures) are not covered, and would require injecting a failing handler or an unwritable directory to trigger.
- **`StopFunc` errors are received**: the local case requires both `stop()` calls to return `nil`; the remote case checks once inside `defer`. An underlying flush timeout makes the test fail.
- **The error handler makes observer failures fail immediately**: calling `t.Errorf` inside `WithErrorHandler` only affects the current test; if an observer panics, the Hub wraps it into an error and hands it to that handler, which is caught as well.
- **The tests rely on a real filesystem and the SQLite driver**: `modernc.org/sqlite` must be available; if the working directory is not writable or the disk is full, `Start` returns an error and the local case fails.
- **The remote case relies on the local network stack**: `httptest.NewServer` listens on the loopback address; if the environment forbids listening on local ports, the remote case fails.
