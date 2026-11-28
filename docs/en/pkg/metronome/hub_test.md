# pkg/metronome/hub_test.go

> 📅 Last Updated: 2026/11/26

`pkg/metronome/hub_test.go` covers four groups of `Hub` responsibilities: **dispatch** (order, period filtering, nesting), **structural validation** (self cycle, indirect cycle, duplicate registration), **error isolation** (error, panic, ticker handler priority, default handler panic), and **lifecycle orchestration** (start in order, stop in reverse order, rollback on failure, runtime add/remove). All tests call `hub.OnTick(...)` directly and synchronously and **rely on neither `Clock` nor real time**, so they are fully deterministic, with no sleeps and no timeouts.

## Purpose

- Turns the core contract "registration order = dispatch order = start order, with stops in reverse" into assertions.
- Proves that a single ticker's failure does not affect other tickers in the same round, and that errors can be traced back to the original error through `errors.Is`.
- Proves that `AddTicker` / `RemoveTicker` at runtime immediately drive `Lifecycle` when the Hub is started.

## Test List

| Test function | Theme | Key assertions |
|---------|------|---------|
| `TestHubFansOutInRegistrationOrder` | Dispatch order | Three `TickerFunc`s record `a, b, c` in turn |
| `TestHubFiltersByTickerPeriod` | Period filtering | Period 2 receives `1,3`; period 1 receives `1,2,3,4` |
| `TestHubNestsChildHub` | Hierarchical dispatch | One `OnTick` on the parent Hub triggers a ticker inside the child Hub once |
| `TestHubRejectsCyclicRegistration` | Cycle detection | Self registration fails; `a→b` succeeds; `b→a` fails |
| `TestHubRejectsDuplicateRegistration` | Deduplication | Registering the same pointer instance a second time fails |
| `TestHubIsolatesTickerError` | Error isolation | Healthy tickers still run; exactly 1 error is reported and `errors.Is` can match it |
| `TestHubRecoversTickerPanic` | Panic isolation | Healthy tickers still run; the reported message contains `kaboom` |
| `TestHubUsesTickerErrorHandlerBeforeDefaultReporter` | Handler priority | The ticker's `HandleTickError` is called and the Hub default handler is not |
| `TestHubSurvivesDefaultErrorHandlerPanic` | Safety net | Dispatch continues after the default handler panics |
| `TestHubLifecycleStartStopOrder` | Lifecycle order | `start:a, start:b, stop:b, stop:a` |
| `TestHubStartRollbackOnFailure` | Start rollback | `start:a, start:b, stop:a`, and `Start` returns an error |
| `TestHubAddTickerStartsImmediatelyWhenRunning` | Runtime add | `AddTicker` on a started Hub immediately does `start:a` |
| `TestHubRemoveTickerStopsImmediatelyWhenRunning` | Runtime remove | `start:a, stop:a` |

## Test Details

### Dispatch and filtering

#### `TestHubFansOutInRegistrationOrder`

- Uses `WithErrorHandler(func(error) {})` to silence the default handler (this test produces no errors; it is a defensive configuration).
- Registers three `TickerFunc`s (`tag` being `a/b/c`), each appending to `got` under a mutex.
- After `hub.OnTick(TickEvent{Seq: 1})` it asserts `[]string{"a", "b", "c"}`.
- **Implicit verification**: `TickerFunc` is not comparable, so `sameTicker` does not deduplicate and the three function adapters can coexist; with the same `*struct` pointer instead, the second registration would be rejected.
- The shared slice `got` is locked only to express "dispatch is synchronous"; in reality there is no concurrency.

#### `TestHubFiltersByTickerPeriod`

- Two `periodProbe`s: `odd` (period 2) and `all` (period 1), registered with `odd` first.
- Four consecutive `OnTick` calls with `Seq` 1..4 (**events built by hand, not going through the Clock**).
- Asserts `odd → [1,3]` and `all → [1,2,3,4]`, i.e. filtering uses `(Seq-1)%period == 0`, so period 2 hits the **odd beats** rather than the even ones.
- Boundary value: this is the easiest place to get wrong (with `Seq%period` the expectation would become `2,4`).
- The test never calls `Start`, showing that **dispatch does not require the lifecycle to be started**.

#### `TestHubNestsChildHub`

- The `child` registers a `TickerFunc` that increments `calls`; then `parent.AddTicker(child)`.
- After `parent.OnTick(TickEvent{Seq: 1})`, `calls == 1`.
- This verifies that `Hub` itself satisfies `Ticker` and can be a child of a parent node; it also indirectly shows that dispatch is synchronous and does not deliver twice.

### Structural validation

#### `TestHubRejectsCyclicRegistration`

```go
a.AddTicker(a) // 自环 → error
a.AddTicker(b) // 成功
b.AddTicker(a) // a 已在 b 的子树中 → error
```

- It only asserts `err == nil / != nil` and does not check error texts; therefore the wording of "cannot add a hub to itself" and "cyclic hub registration rejected" can evolve independently.
- The order matters: `b→a` can only form an indirect cycle after `a→b` has succeeded.

#### `TestHubRejectsDuplicateRegistration`

- The same `*periodProbe` is registered twice and the second must fail.
- It relies on `sameTicker` comparing comparable pointer types by instance; with `TickerFunc` or a value type containing a slice it would not fail (see `ticker.md`).

### Error isolation

#### `TestHubIsolatesTickerError`

- The registration order is deliberately "bad first, good after": the bad one returns `boom` and the good one increments `calls`.
- Assertions: `calls == 1` (the bad one does not interrupt dispatch), `len(reported) == 1` (reported exactly once), `errors.Is(reported[0], boom)` (the `%w` wrapping is transparent).
- If `deliver` were changed to not wrap and use `fmt.Errorf("%v")` directly, `errors.Is` would fail — this is a hard constraint on the error chain.

#### `TestHubRecoversTickerPanic`

- The first ticker does `panic("kaboom")`, the second is healthy.
- It asserts the healthy ticker is still called and the reported error text contains `kaboom` (`strings.Contains`, no full-format comparison).
- The point: the panic is converted into an error by `deliver`'s `recover` instead of crashing the test process.

#### `TestHubUsesTickerErrorHandlerBeforeDefaultReporter`

- `handlingProbe` implements `OnTick` (returning an error) and `HandleTickError` (recording into `handled`).
- The Hub is configured with `WithErrorHandler` (recording into `reported`).
- It asserts `len(handled) == 1` and `len(reported) == 0`: the ticker's own handler **completely swallows** the error and the Hub default handler is not used.

#### `TestHubSurvivesDefaultErrorHandlerPanic`

- The function passed to `WithErrorHandler` panics directly with `panic("handler boom")`; `log` output is redirected to `io.Discard` and restored in a `defer`.
- It registers a "always fails" ticker and a "healthy" ticker; after `OnTick` the healthy ticker has `calls == 1`.
- This shows `report`'s `recover` works: a default handler crash does not interrupt this round of dispatch, and the error degrades to a log.
- The test does not assert the log content (it has been discarded), only that control flow continues.

### Lifecycle

#### `TestHubLifecycleStartStopOrder`

- Two `lifecycleProbe`s (`a`, `b`) share one `lifecycleRecorder`.
- `Hub.Start()` → `Hub.Stop()` should yield the call sequence `start:a, start:b, stop:b, stop:a`.
- It locks down both rules, "`Start` in order, `Stop` in reverse"; `lifecycleProbe.OnTick` always returns `nil` and this test does not care about dispatch.

#### `TestHubStartRollbackOnFailure`

- `a` starts normally and `b` has `startErr = errors.New("boom")`.
- `hub.Start()` must return an error; the expected call sequence is `start:a, start:b, stop:a` — that is, after `b` fails, the already started `a` is **immediately rolled back in reverse order**.
- It asserts neither the error text nor the `started` flag; therefore "after a failure `started` stays false and can be retried" is not directly covered.

#### `TestHubAddTickerStartsImmediatelyWhenRunning`

- First `hub.Start()` (with no tickers at that point, so `started = true`), then `AddTicker(a)`.
- The expected sequence is `start:a`: a `Lifecycle` item added at runtime is started immediately.
- The `defer hub.Stop()` appends `stop:a`, but the assertion happens before `Stop`, so it is unaffected.

#### `TestHubRemoveTickerStopsImmediatelyWhenRunning`

- First `AddTicker(a)` (the Hub is not started, so no `Start` call), then `Start()` (`start:a`), then `RemoveTicker(a)`.
- The expected sequence is `start:a, stop:a`: stop immediately after removal.
- Complementary to the previous test, it covers the timing combination "not started at registration / stopped only at runtime".

## Test Stub Types

| Type | Key point | Description |
|------|--------|------|
| `periodProbe` | `mu` protects `seqs`, `snapshot()` returns a copy | Implements `Periodic`, used for filtering and deduplication tests; `mu` is a defensive provision for "may be called by the clock goroutine" and is not necessary in the current synchronous tests |
| `handlingProbe` | Function fields inject `onTick` / `onHandle`, **no lock** | Implements `ErrorHandler`; no locking is needed under synchronous calls |
| `lifecycleRecorder` | `mu` + `calls`, `add` / `snapshot` | Records and snapshots the lifecycle call sequence; the snapshot returns a copy to avoid aliasing |
| `lifecycleProbe` | `name`, `recorder`, `startErr`, `stopErr` | Implements both `Ticker` and `Lifecycle`; `Start`/`Stop` record first, then return the preset error |

`lifecycleProbe.stopErr` is not used in the current tests and can be seen as a reserved field (the stop-error aggregation path is not covered yet).

## Concurrency and Timing Assumptions

- **Everything is synchronous**: `Hub.OnTick` is called directly in the test goroutine, and `Lifecycle`'s `Start`/`Stop` also run synchronously. Bare counters such as `var calls int` are therefore safe, and `periodProbe.mu` only expresses "production may be concurrent".
- **There is no real clock**: `TickEvent{Seq: n}` is built by hand in the test and `FiredAt` is the zero value — the dispatch logic of `Hub` does not read `FiredAt`, so the assertions are unaffected.
- **There is no `t.Parallel()`**: the global replacement of `log.SetOutput` (`TestHubSurvivesDefaultErrorHandlerPanic`) does not conflict with other tests.
- **Order sensitive**: several assertions depend on registration order, so when adding tests do not insert a ticker in the middle of an existing registration chain without updating the expected sequence.

## How to Run

```bash
# 全部 Hub 测试
go test ./pkg/metronome/ -run 'TestHub' -v

# 按主题过滤
go test ./pkg/metronome/ -run 'TestHub(FansOut|Filters|Nests)' -v
go test ./pkg/metronome/ -run 'TestHub(Rejects|Lifecycle|Start|AddTicker|RemoveTicker)' -v

# 单个用例
go test ./pkg/metronome/ -run 'TestHubStartRollbackOnFailure' -v

# 重复执行（这些测试是确定性的，重复仍应全绿）
go test ./pkg/metronome/ -run 'TestHub' -count=50

# 竞态检测
go test ./pkg/metronome/ -run 'TestHub' -race
```

## Notes

- **Assertion style**: order uses `slices.Equal`; the error chain uses `errors.Is` (relying on `%w`); panic text uses `strings.Contains`. When changing error wrapping, the first two are hard constraints, while the third only requires the original panic value to be preserved.
- **Public APIs not covered** (present in the source but not directly verified by this test file):
  - `Contains` (and its behavior of always returning `false` for `TickerFunc`);
  - the generic `Find[T]` (indirectly covered by the tests in `pkg/assembly`);
  - the values and validation of `TickPeriod` / `SetTickPeriod` / `WithTickPeriod`;
  - the error branches of `AddTicker(nil)` and an invalid period;
  - the error branches of `RemoveTicker` for an unregistered item and for `nil`;
  - the `errors.Join` aggregation when several children fail to stop during `Stop`, and the reverse-order error collection of `stopReversed`;
  - the `errors.Join(startErr, stopErr)` branch when the `Start` rollback itself also fails;
  - the interleaving of a dispatch snapshot with concurrent `AddTicker`/`RemoveTicker` ("an already removed ticker may still receive one in-flight beat").
- **`tag := tag` in `TestHubFansOutInRegistrationOrder`** is an explicit copy for loop-variable capture; it is no longer required in Go 1.22+, but keeping it does not affect semantics.
- **Do not turn these tests into concurrent ones**: several stub types depend on the premise that "calls happen in the same goroutine" (`handlingProbe` has no lock, `calls` is a bare `int`), so switching to `t.Parallel()` plus a real clock would introduce races.
