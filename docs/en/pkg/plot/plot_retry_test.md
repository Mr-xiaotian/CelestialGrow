# pkg/plot/plot_retry_test.go

> 📅 Last Updated: 2026/09/24

`plot_retry_test.go` focuses on the **retry semantics** of `Plot`: it verifies how the three options `WithMaxRetries` / `WithRetryDelay` / `WithRetryIf` affect the retry loop in `basePlot.tend`, and finally solidifies the results into lifecycle snapshots through `Plot.Harvest`.

## Cases

### `TestPlot_RetrySuccess` — success after retries

- **cultivator**:
  ```go
  func(seed int) (int, error) {
      n := attempts.Add(1)
      if n <= 2 {
          return 0, errors.New("transient error")
      }
      return seed * 10, nil
  }
  ```
- **Configuration**: `plot.NewPlot("test_retry_success", cultivator, plot.WithTenders(1), plot.WithMaxRetries(3))`
- **seeds**: `[]int{1}`

Verifies:

1. `mustHarvest` returns 1 record;
2. `Status == "ripen"`, `FruitJSON == "10"`;
3. `attempts.Load() == 3` (the first 2 attempts fail + the 3rd succeeds).

> Coverage points: with the default `retryDelay = 0`, a transient error can eventually succeed after several retries, and the `SeedRipen` lifecycle is written correctly.

### `TestPlot_RetryExhausted` — retries exhausted and still failing

- **cultivator**: `func(seed int) (int, error) { attempts.Add(1); return 0, errors.New("permanent error") }` (always fails)
- **Configuration**: `plot.NewPlot("test_retry_exhausted", cultivator, plot.WithTenders(1), plot.WithMaxRetries(2))`
- **seeds**: `[]int{1}`

Verifies:

1. 1 record: `Status == "wither"`, `WitherMessage == "permanent error"`;
2. `attempts.Load() == 3` (1 original attempt + 2 retries).

> Coverage points: after `maxRetries+1` attempts the loop exits and enters `witherSeed`; the last failure does **not** write a `SeedReplant` log (that log is written only when `attempt <= maxRetries`).

### `TestPlot_RetryIf` — the error filter blocks retries

- **cultivator**: always returns `errors.New("permanent")`.
- **Configuration**: `plot.NewPlot("test_retry_if", cultivator, plot.WithTenders(1), plot.WithMaxRetries(3), plot.WithRetryIf(func(err error) bool { return !errors.Is(err, permanent) }))`
- **seeds**: `[]int{1}`

Verifies:

1. 1 record: `Status == "wither"`, `WitherMessage == "permanent"`;
2. `attempts.Load() == 1` (blocked by `retryIf` on the first attempt, no further retries).

> Coverage points: a custom `retryIf` can precisely filter out "non-retryable" errors; in practice this is commonly used to exclude errors like `ErrPermanent` from the retry loop.

### `TestPlot_RetryDelay` — custom retry interval

- **cultivator**:
  ```go
  func(seed int) (int, error) {
      n := attempts.Add(1)
      if n <= 1 {
          return 0, errors.New("transient")
      }
      return seed, nil
  }
  ```
- **Configuration**: `plot.NewPlot("test_retry_delay", cultivator, plot.WithTenders(1), plot.WithMaxRetries(2), plot.WithRetryDelay(func(attempt int) time.Duration { return 100 * time.Millisecond }))`
- **seeds**: `[]int{1}`
- **Extra**: `start := time.Now()`, and `elapsed := time.Since(start)` after `Run` finishes.

Verifies:

1. 1 record: `Status == "ripen"`, `FruitJSON == "1"`;
2. `elapsed >= 100*time.Millisecond`, proving that it really waited 100ms before retrying.

> Coverage points: `retryDelay(attempt)` is passed to `time.Sleep` when `attempt <= maxRetries`; it is commonly used to implement exponential backoff or fixed-interval throttling.

## Common preconditions

- Test package: `package plot_test` (black-box); it does not access the unexported fields of `Plot` directly;
- `attempts` uses `sync/atomic.Int32` for goroutine-safe accumulation, ensuring that the retry-count assertions are not affected by concurrency;
- It reuses `mustHarvest(t, plot harvestable)` defined in `plot_harvest_test.go` (`harvestable` being the local test interface `interface{ Harvest() ([]persist.LifecycleStatusRecord, error) }`) to fetch snapshots, thereby implicitly verifying the wiring of `Harvest` + `lifecycleSpout`;
- All four cases use `WithTenders(1)`: concurrent tenders would not change the total number of calls, but they would make the timing of `attempts.Add(1)` hard to assert; only after serialization is the retry count deterministic.

## How to run

```bash
go test ./pkg/plot -run "TestPlot_Retry" -v
```

Or run all tests in the plot package at once:

```bash
go test ./pkg/plot/... -v
```

## Notes

- The status strings follow the source code: `"ripen"` for success and `"wither"` for failure; the result field is `FruitJSON` and the error field is `WitherMessage`.
- `TestPlot_RetryDelay` uses `elapsed >= 100*time.Millisecond` as a lower bound and does **not** assert strict equality; CI jitter or scheduling delays will make the actual elapsed time slightly larger, which is expected behavior.
- `TestPlot_RetryIf` relies on `errors.Is` semantics; if `retryIf` later switches to `==` comparison internally, this test must be adjusted accordingly.
- These cases do not explicitly assert how many times the `SeedReplant` log was called; they only infer it from the total of `attempts` and the final state. If you need to assert the log count, you must use the test utilities of `pkg/persist`.
- `TestPlot_RetryExhausted` asserts `attempts.Load() == 3`, i.e. `1 + maxRetries`; changing the default `maxRetries` does not affect this case (the case explicitly passes `WithMaxRetries(2)`).
