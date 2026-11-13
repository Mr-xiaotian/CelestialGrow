# pkg/plot/option.go

> 📅 Last Updated: 2026/11/13

`option.go` defines the "functional options" configuration layer for all nodes. Every `Option` is a small closure that mutates `*plotOptions` once; `newBasePlot` first takes `defaultOptions()` and then applies the `Option`s passed in by the user in order, so `NewSinglePlot` / `NewSplitPlot` / `NewRoutePlot` share the same configuration.

## Purpose

- Encapsulate tunable parameters such as `numTenders` / `chanSize` / `maxRetries` / `retryDelay` / `retryIf` / `pruneIf` as "functional options";
- Keep the constructor signatures of the various nodes concise (`name, cultivator, opts...`) while supporting the addition of new configuration in the future without breaking the API.
- `plotOptions` is embedded directly by `basePlot`, so the options take effect equally for all three kinds of nodes (`SinglePlot` / `SplitPlot` / `RoutePlot`).

## Core Types

```go
type Option func(*plotOptions)

type plotOptions struct {
    numTenders int
    chanSize   int
    maxRetries int
    retryDelay func(attempt int) time.Duration
    retryIf    func(error) bool
    pruneIf    func(any) bool
}
```

`Option` is a function that "takes `*plotOptions`, mutates it in place, and returns nothing", the common optional-parameter pattern in Go.

> The log level is **not** in `plotOptions`: it is passed by `Run(seeds, logLevel)` to `assembly.StartPlotResources` at runtime, so different runs of the same plot can use different log levels.

## Defaults

The implementation of `defaultOptions()`:

| Field | Default | Meaning |
|------|--------|------|
| `numTenders` | `runtime.NumCPU()` | Number of concurrent tenders (tend goroutines) |
| `chanSize` | `runtime.NumCPU()` | `seedChan` buffer size |
| `maxRetries` | `1` | Maximum number of retries (excluding the first attempt), i.e. "retry once by default" |
| `retryDelay` | `func(attempt int) time.Duration { return 0 }` | No wait before retrying |
| `retryIf` | `func(error) bool { return true }` | Retry on any error |
| `pruneIf` | `nil` | No pruning |

## Overview of the `WithXxx` Functions

The table below covers all public `WithXxx` functions in `option.go`; the parameters, defaults, and effects all take the source code as the single source of truth.

| Function | Parameter | Default | Effect |
|------|------|--------|------|
| `WithTenders(n int) Option` | `n`: number of tender goroutines | `runtime.NumCPU()` | Sets `numTenders`, controlling the size of the `sem` semaphore in `sprout`, and thereby determining the maximum concurrent cultivation count |
| `WithChanSize(n int) Option` | `n`: channel buffer size | `runtime.NumCPU()` | Sets `chanSize`, used to allocate the buffer of this node's `seedChan`; the downstream `yieldChans` get no extra buffer (they reuse the downstream's `seedChan` directly) |
| `WithMaxRetries(n int) Option` | `n`: maximum number of retries (excluding the first attempt) | `1` | Sets `maxRetries`. `WithMaxRetries(2)` means at most 3 executions (1 original + 2 retries) |
| `WithRetryDelay(fn func(attempt int) time.Duration) Option` | `fn`: retry interval policy | `func(int) time.Duration { return 0 }` | Sets `retryDelay`. `attempt` starts at 1 and increments, commonly used to implement "exponential backoff" |
| `WithRetryIf(fn func(error) bool) Option` | `fn`: error filter | `func(error) bool { return true }` | Sets `retryIf`. Only errors for which it returns `true` take part in the next retry; returning `false` immediately terminates the retry loop and goes to `witherSeed` |
| `WithPruneIf[S any](fn func(S) bool) Option` | `fn`: prune predicate | Not set (`nil`, i.e. no pruning) | Sets `pruneIf`. The predicate is evaluated **before** the `cultivator`; a seed for which it returns `true` is terminated directly as `prune`, without triggering a retry, calling the `cultivator`, or producing anything to any downstream |

> The source comment of `WithChanSize` still says "seedChan/fruitChan", but the current skeleton only uses that capacity for `seedChan`; `fruitChan` no longer exists.

## Example of Collaboration with Retry

The example below combines three Options to implement "at most 3 retries, linear backoff by attempt, no retry for permanent errors":

```go
p := plot.NewSinglePlot("flaky",
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

> For the concrete behavior of the retry loop, see the "Retry loop `tend`" section of `plot.md`: with `maxRetries=3` it runs at most 4 times (1 original + 3 retries); when the last attempt fails no `SeedReplant` event is emitted, but `retryDelay(4)` is still executed once.

## Example of Collaboration with Pruning

The following uses `WithPruneIf` to implement "duplicate seeds are pruned and skipped directly, without being cultivated again":

```go
var seenSeeds sync.Map // 注意：需 import "sync"

p := plot.NewSinglePlot("dedup",
    func(seed int) (int, error) { return seed * 10, nil },
    plot.WithTenders(2),
    plot.WithPruneIf(func(seed int) bool {
        _, loaded := seenSeeds.LoadOrStore(seed, struct{}{})
        return loaded // 已见即修剪
    }),
)
```

> For the pruning semantics, see the "Prune path `pruneSeed`" section of `plot.md`: a pruned seed is cut off before the `cultivator`, counted into `Skipped`, and `Processed` is conserved.

## Notes

- The `n` in `WithMaxRetries(n)` **does not include** the first execution; `n=0` means "no retry".
- The `attempt` of `WithRetryDelay` is 1-based. The source calls `Sleep(retryDelay(attempt))` after every failure, **including the last one** (when no further retry will happen), so the backoff function is called one extra time at a position where an idle wait may occur.
- The predicate of `WithPruneIf` is evaluated before the `cultivator` and **takes precedence over retry**: once the predicate returns `true`, the seed neither calls the `cultivator` nor enters the retry loop, and goes straight to `pruneSeed`. The predicate defaults to `nil` (no pruning).
- Inside `WithPruneIf`, a type assertion restores `any` to `S`; if the assertion fails, it is treated as "no pruning". The generic parameter `S` is inferred from the parameter type of the passed predicate and need not be specified explicitly.
- The log level is controlled by the `logLevel` parameter of `Run`; the `WithLogLevel` Option no longer exists.
- Because `Option` is simple closure stacking, **a later Option with the same name overrides an earlier one**; if the business needs a more complex merge strategy, it can process it in the outer layer and pass it only once.
- The defaults of `numTenders` / `chanSize` are both `runtime.NumCPU()`, and inside a container that value reflects the number of CPUs visible to the host; for I/O-intensive workloads it is advisable to increase them explicitly with `WithTenders` / `WithChanSize`.
