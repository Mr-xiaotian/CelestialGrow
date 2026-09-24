# pkg/plot/option.go

> 📅 Last Updated: 2026/09/24

`option.go` defines the "functional options" configuration layer for all nodes. Every `Option` is a small closure that mutates `*plotOptions` once; `newBasePlot` first takes `defaultOptions()` and then applies the `Option`s passed in by the user in order, so `NewPlot` / `NewSplitPlot` / `NewRoutePlot` share the same configuration.

## Purpose

- Encapsulate tunable parameters such as `numTenders` / `chanSize` / `maxRetries` / `retryDelay` / `retryIf` / `logLevel` as "functional options";
- Keep the constructor signatures of the various nodes concise (`name, cultivator, opts...`) while supporting the addition of new configuration in the future without breaking the API.
- `plotOptions` is embedded directly by `basePlot`, so the options take effect equally for all three kinds of nodes (`Plot` / `SplitPlot` / `RoutePlot`).

## Core Types

```go
type Option func(*plotOptions)

type plotOptions struct {
    numTenders int
    chanSize   int
    maxRetries int
    retryDelay func(attempt int) time.Duration
    retryIf    func(error) bool
    logLevel   string
}
```

`Option` is a function that "takes `*plotOptions`, mutates it in place, and returns nothing", the common optional-parameter pattern in Go.

## Defaults

The implementation of `defaultOptions()`:

| Field | Default | Meaning |
|------|--------|------|
| `numTenders` | `runtime.NumCPU()` | Number of concurrent tenders (tend goroutines) |
| `chanSize` | `runtime.NumCPU()` | `seedChan` buffer size |
| `maxRetries` | `1` | Maximum number of retries (excluding the first attempt), i.e. "retry once by default" |
| `retryDelay` | `func(attempt int) time.Duration { return 0 }` | No wait before retrying |
| `retryIf` | `func(error) bool { return true }` | Retry on any error |
| `logLevel` | `"INFO"` | Minimum log level |

## Overview of the `WithXxx` Functions

The table below covers all public `WithXxx` functions in `option.go`; the parameters, defaults, and effects all take the source code as the single source of truth.

| Function | Parameter | Default | Effect |
|------|------|--------|------|
| `WithTenders(n int) Option` | `n`: number of tender goroutines | `runtime.NumCPU()` | Sets `numTenders`, controlling the size of the `sem` semaphore in `sprout`, and thereby determining the maximum concurrent cultivation count |
| `WithChanSize(n int) Option` | `n`: channel buffer size | `runtime.NumCPU()` | Sets `chanSize`, used to allocate the buffer of this node's `seedChan`; the downstream `yieldChans` get no extra buffer (they reuse the downstream's `seedChan` directly) |
| `WithMaxRetries(n int) Option` | `n`: maximum number of retries (excluding the first attempt) | `1` | Sets `maxRetries`. `WithMaxRetries(2)` means at most 3 executions (1 original + 2 retries) |
| `WithRetryDelay(fn func(attempt int) time.Duration) Option` | `fn`: retry interval policy | `func(int) time.Duration { return 0 }` | Sets `retryDelay`. `attempt` starts at 1 and increments, commonly used to implement "exponential backoff" |
| `WithRetryIf(fn func(error) bool) Option` | `fn`: error filter | `func(error) bool { return true }` | Sets `retryIf`. Only errors for which it returns `true` take part in the next retry; returning `false` immediately terminates the retry loop and goes to `witherSeed` |
| `WithLogLevel(level string) Option` | `level`: log level string | `"INFO"` | Sets `logLevel`, which is passed to `persist.NewLogInlet` to filter the logs that are written |

## Example of Collaboration with Retry

The example below combines three Options to implement "at most 3 retries, linear backoff by attempt, no retry for permanent errors":

```go
p := plot.NewPlot("flaky",
    func(seed int) (int, error) { return doWork(seed) },
    plot.WithMaxRetries(3),
    plot.WithRetryDelay(func(attempt int) time.Duration {
        return time.Duration(attempt) * 100 * time.Millisecond
    }),
    plot.WithRetryIf(func(err error) bool {
        return !errors.Is(err, ErrPermanent)
    }),
)
```

> For the concrete behavior of the retry loop, see the "Retry loop `tend`" section of `plot_base.md`: with `maxRetries=3` it runs at most 4 times (1 original + 3 retries), and no `SeedReplant` log is written when the last attempt fails.

## Notes

- The `n` in `WithMaxRetries(n)` **does not include** the first execution; `n=0` means "no retry".
- The `attempt` of `WithRetryDelay` is 1-based, and `Sleep` actually happens only when `attempt <= maxRetries`, i.e. the parameter expresses "the wait before a retry".
- `WithLogLevel` only controls "the minimum level of logs written by `logInlet`" and does not change the event ID allocation of `eventClient`; the default `"INFO"` is passed to `persist.NewLogInlet`.
- Because `Option` is simple closure stacking, **a later Option with the same name overrides an earlier one**; if the business needs a more complex merge strategy, it can process it in the outer layer and pass it only once.
- The defaults of `numTenders` / `chanSize` are both `runtime.NumCPU()`, and inside a container that value reflects the number of CPUs visible to the host; for I/O-intensive workloads it is advisable to increase them explicitly with `WithTenders` / `WithChanSize`.
