# pkg/metronome/clock_test.go

> 📅 Last Updated: 2026/11/26

`pkg/metronome/clock_test.go` drives `Clock` with real time (no fake clock) and covers seven kinds of behavior: construction validation, the basic tick sequence, period filtering of the root ticker, waking on a runtime period change, start/stop idempotence, the priority of the root ticker's error handler, and that **the clock stays alive after the error handler itself panics**. The tests lean heavily on timing assumptions, but all of them leave several times the required margin, so they are stable by default.

## Purpose

- Locks down the external contract of `Clock`: `Seq` starts at 1, period filtering takes effect at the `Clock` layer, and `SetInterval` must take effect on the "next beat" rather than "when the old period expires".
- Locks down the idempotence of `Stop` and the reliability promise that "a handler panic must not stall the clock".
- Provides regression protection for the two error paths of `deliver` (`OnTick` returning an error, and `HandleTickError` panicking).

## Test List

| Test function | Behavior covered | Key timing |
|---------|---------|---------|
| `TestNewClockRejectsInvalidArguments` | Construction argument validation | None (purely synchronous) |
| `TestClockTicksRoot` | Basic firing + `Seq` incrementing from 1 | Period 10ms, 3 beats, 200ms limit |
| `TestClockRespectsRootTickPeriod` | `TickPeriod` filtering of the root ticker | Period 10ms, 3 beats received (5 actually fired), 250ms limit |
| `TestClockSetIntervalTakesEffectOnNextTick` | Period change wakes immediately | 200ms → 10ms, 120ms verdict |
| `TestClockIdempotentStartStop` | Repeated `Start` / `Stop` return `nil` | None (only one 10ms period) |
| `TestClockUsesRootErrorHandler` | Prefer the root's `ErrorHandler` when implemented | Period 10ms, polling within 150ms |
| `TestClockSurvivesRootErrorHandlerPanic` | The clock keeps running after a handler panic | Period 10ms, observe for 40ms |

## Test Details

### `TestNewClockRejectsInvalidArguments`

- Two constructions: `NewClock(0, root)` and `NewClock(time.Millisecond, nil)`, both only asserting `err != nil`.
- **The error text is not checked**, so rewording the messages does not break the test; but "zero/negative period" and "nil root" must be rejected.
- Boundary note: `interval = 0` and `nil` are the only two construction-time failure paths, and a positive period plus a non-nil root always succeeds.

### `TestClockTicksRoot`

```go
root := &channelTicker{ch: make(chan int, 4)}
clock, _ := NewClock(10*time.Millisecond, root)
```

- After starting, it collects 3 `Seq` values from `root.ch` and asserts `slices.Equal(got, []int{1, 2, 3})`.
- **Two things are verified**: the first `Seq` value is 1; under normal conditions no beats are lost and no numbers are duplicated.
- Timing assumption: 3 beats nominally take 30ms and the timeout budget is 200ms (≈6x margin); the `channelTicker` channel has capacity 4 > 3, so the send inside `OnTick` never blocks and therefore cannot slow the clock down in turn.
- Teardown calls `Stop()` via `defer` and checks the error, verifying that `Stop` can join successfully within 5 seconds on the normal path.

### `TestClockRespectsRootTickPeriod`

- The root is `periodChannelTicker{period: 2}`, i.e. it implements `Periodic` and returns 2.
- It expects `[]int{1, 3, 5}` — proving three points:
  1. `Clock` applies the `shouldTick` filter **before** calling the root's `OnTick`;
  2. filtered beats **still consume `Seq`** (otherwise it would be `1, 2, 3`);
  3. filtering neither stalls the clock nor causes replays.
- Timing: getting 3 deliveries requires passing 5 beats ≈ 50ms, with a 250ms timeout.

### `TestClockSetIntervalTakesEffectOnNextTick`

- Constructs with a 200ms period; after `Start` it first does `time.Sleep(20ms)` to make sure `run` is already `select`ing on a ~200ms timer.
- Then it calls `SetInterval(10 * time.Millisecond)`, records the start point `start`, and waits for the first beat:
  - received within 120ms → pass (meaning the wakeup works and the landing point is around `prevFire + 10ms`);
  - still nothing at 150ms → `t.Fatal`;
  - received but taking > 120ms → also a failure (to prevent a false pass where "it only fired at the old 200ms timer").
- **This is the only direct evidence that `wakeCh` exists**: without the wakeup, the 200ms timer would put the first beat at ~180ms.
- The test only verifies "no later than 120ms" and not the exact grid, so it is lenient toward scheduling jitter.

### `TestClockIdempotentStartStop`

- Two consecutive `Start` calls and two `Stop` calls; all four must return `nil`.
- Covers the two early-exit branches of `Start` ("already running, return directly") and `Stop` ("not running, return directly").
- It does not assert the goroutine count, so "did a repeated `Start` spin up an extra loop" can only be inferred indirectly from the `Seq` tests; the sole job of this test is API idempotence.

### `TestClockUsesRootErrorHandler`

- The root is `clockHandlingProbe`: `OnTick` always returns `errors.New("boom")` and `HandleTickError` appends the error to `handled`.
- After starting, it polls `len(handled) == 0` at 5ms intervals with a 150ms timeout.
- The assertion focuses on: **at least one** error was handed to the root's `ErrorHandler` (rather than only being logged); it does not check the error text and does not require exactly one (a 10ms period produces several within 150ms).
- Teardown calls `Stop()` and checks the error.
- ⚠️ The read side `len(handled)` is not locked (`clockHandlingProbe.mu` only protects the write path), so strictly speaking there is a data race; the default (non-`-race`) run is unaffected, and an occasional report under `-race` is a problem of the test itself, not of `Clock`.

### `TestClockSurvivesRootErrorHandlerPanic`

- First redirects `log` output to `io.Discard` to keep the default handler from flooding the console, and restores it in a `defer`.
- The root's `OnTick` returns an error and `HandleTickError` **panics directly with "handler boom"**.
- After running for 40ms (≈4 ticks) it asserts `clock.IsRunning()` is `true`, then `Stop()` succeeds.
- The assertion focuses on:
  - `IsRunning()` being `true` only shows the state flag was not changed by `Stop`; **the real criterion is that the test process is still alive**: if the panic escaped `deliver`, the whole `go test` binary would crash and never reach the assertion;
  - `Stop` can join normally within 5 seconds (if the clock goroutine were already dead, `doneCh` would already be closed and `Stop` would also return `nil` immediately, so this one is just a supporting signal).
- Timing assumption: 40ms is enough to trigger several ticks and therefore necessarily reach the panic path.

## Test Stub Types

| Type | Method set | Purpose |
|------|--------|------|
| `channelTicker` | `OnTick` writes `event.Seq` into `ch` | The simplest tick collector, with a period treated as 1 |
| `periodChannelTicker` | `TickPeriod() = period` + `OnTick` writes `ch` | Verifies period filtering |
| `clockHandlingProbe` | `OnTick` / `HandleTickError` injected through function fields, `mu` serializes callbacks | Verifies error handler priority and the panic path |

All three use **pointer receivers** and are registered as `&channelTicker{...}`; the `channelTicker` channel must be buffered, otherwise `OnTick` would block the clock goroutine and break the timing assertions.

## How to Run

```bash
# 全部 Clock 测试
go test ./pkg/metronome/ -run 'TestClock|TestNewClock' -v

# 单个用例（定位时序问题时最有用）
go test ./pkg/metronome/ -run 'TestClockSetIntervalTakesEffectOnNextTick' -v
go test ./pkg/metronome/ -run 'TestClockSurvivesRootErrorHandlerPanic' -v

# 重复运行以暴露偶发时序问题
go test ./pkg/metronome/ -run 'TestClock' -count=20

# 显式检查数据竞争（注意上文提到的测试自身读写不同步）
go test ./pkg/metronome/ -run 'TestClock' -race
```

## Notes

- **The tests use real time**, so they are unsuitable for running alongside many parallel jobs on a heavily loaded CI machine; the assertion margin is about 5–6x the nominal duration, but that is still not a hard real-time guarantee.
- **`log.SetOutput` is global state**: `TestClockSurvivesRootErrorHandlerPanic` replaces it temporarily and restores it through `defer`. The package currently has no `t.Parallel()`, so tests do not interfere with each other.
- **Paths not covered** (known gaps from reading the source, not a defect list):
  - the 5-second timeout error branch of `Stop`;
  - the error returned by `SetInterval` for a non-positive period;
  - the value of the `DefaultInterval` constant itself;
  - the exact tick sequence produced by "skip missed beats, never catch up" when `OnTick` is slow (the `skipped` computation has no direct assertion);
  - `Seq` returning to zero when `Start` is called again after `Clock` was stopped.
- **Calling `t.Fatalf` inside `defer`** (as in `TestClockTicksRoot` and others) terminates the current test goroutine when `Stop` fails; it does not hide other tests, but the failure message of that case then only shows the `Stop()` error.
