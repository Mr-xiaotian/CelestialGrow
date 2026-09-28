# pkg/observer/progress.go

> 📅 Last Updated: 2026/09/24

## Purpose

`pkg/observer/progress.go` provides `ProgressBar`, the default terminal visualization implementation of the `Observer` interface. Built on the third-party library [`github.com/schollz/progressbar/v3`](https://github.com/schollz/progressbar), it renders Plot's cultivation progress as a live progress bar on standard error output, making it easy to observe the processing progress of large seed batches in CLI scenarios.

## Core Objects

### The `ProgressBar` type

```go
type ProgressBar struct {
    description string
    bar         *progressbar.ProgressBar
    mu          sync.Mutex
}
```

| Field | Purpose |
|------|------|
| `description` | The progress bar prefix description text, set by `NewProgressBar` and read-only afterwards |
| `bar` | The lazily created `progressbar.ProgressBar` instance; actually constructed only after the first `OnStart` / `OnProgress` / `OnFinish` receives a non-zero `total` |
| `mu` | A mutex protecting the `bar` field and the `bar.Set` / `bar.Finish` calls, ensuring concurrency safety |

`ProgressBar` is an exported type (obtainable from `pkg/api` via `api.NewProgressBar`), but the fields `description` / `bar` / `mu` are all unexported, so it can only be constructed via `NewProgressBar`; it implements `observer.Observer` and can be passed directly to `AddObserver` of any Plot node.

### The `NewProgressBar` constructor

```go
func NewProgressBar(description string) *ProgressBar
```

- `description`: the progress bar prefix text, displayed at the prefix position set by `OptionSetDescription`.
- Return value: a zero-state `*ProgressBar` with only `description` set; the underlying `bar` is still `nil` until the first callback carrying a non-zero `total` actually initializes it.

## How the Progress Bar Is Implemented

`ProgressBar` holds a `*progressbar.ProgressBar` instance from `schollz/progressbar/v3`. All three methods `OnStart` / `OnProgress` / `OnFinish` first lock `p.mu`, then call `ensureBar` to trigger on-demand lazy initialization, and finally operate on `bar` while holding the lock:

```go
func (p *ProgressBar) ensureBar(total int) {
    if total == 0 || p.bar != nil {
        return
    }
    p.bar = progressbar.NewOptions64(...)
}
```

- Lazy initialization: `bar` is constructed only when `total > 0` and it has not been created yet, so `OnStart(0)` does not draw immediately.
- Mutex: all `OnStart` / `OnProgress` / `OnFinish` entry points execute `p.mu.Lock(); defer p.mu.Unlock();`, preventing `bar` from being created twice or `Set` / `Finish` being called concurrently.
- Completion callback: `OptionOnCompletion` is registered, writing an extra newline to `os.Stderr` when the bar fills up, to avoid sticking to subsequent logs.

## Descriptors / Options

`ensureBar` sets the following options through `progressbar.NewOptions64`:

| Option | Purpose |
|------|------|
| `OptionSetDescription(p.description)` | Sets the progress bar prefix description, using the string passed to `NewProgressBar` |
| `OptionSetWriter(os.Stderr)` | Output target is `os.Stderr`, avoiding conflicts with stdout business output |
| `OptionSetWidth(10)` | Progress bar character width of 10 |
| `OptionShowTotalBytes(true)` | Displays the total in byte units |
| `OptionThrottle(time.Millisecond)` | Render throttling of 1ms, avoiding screen spam under highly concurrent `Set` calls |
| `OptionShowCount()` | Shows the current count |
| `OptionShowIts()` | Shows iterations (refresh rate per second) |
| `OptionOnCompletion(...)` | Outputs a newline on completion |
| `OptionSpinnerType(14)` | Uses spinner animation number 14 |
| `OptionFullWidth()` | Makes the progress bar span the full terminal width (when combined with a fixed width, the library decides) |
| `OptionSetRenderBlankState(true)` | Allows rendering a blank placeholder when `total == 0`; since `ensureBar` does not create `bar` at all when `total == 0`, this option currently has no real effect (it only takes effect on the first frame after `total > 0`) |

The color scheme comes from the default theme of `schollz/progressbar/v3` and is not customized via `OptionSetTheme`; to change colors, add a `progressbar.OptionSetTheme(...)` call in `ensureBar`.

## Concrete Behavior of the Three Callbacks

```go
func (p *ProgressBar) OnStart(total int) {
    p.mu.Lock()
    defer p.mu.Unlock()
    p.ensureBar(total)
}

func (p *ProgressBar) OnProgress(completed, total int) {
    p.mu.Lock()
    defer p.mu.Unlock()
    p.ensureBar(total)
    _ = p.bar.Set(completed)
}

func (p *ProgressBar) OnFinish(completed, total int) {
    p.mu.Lock()
    defer p.mu.Unlock()
    p.ensureBar(total)
    _ = p.bar.Set(total)
    _ = p.bar.Finish()
}
```

- `OnStart`: only ensures `bar` is created (only when `total > 0`), without advancing progress; this method receives only `total` and has no `completed` parameter.
- `OnProgress`: writes `completed` into `bar`, and `progressbar/v3` redraws internally.
- `OnFinish`: forces `bar` to fill up to `total` (even if the latest `completed < total`), then calls `Finish()` to mark the wrap-up, triggering `OptionOnCompletion` to write a newline.
- All errors returned by `bar.Set` / `bar.Finish` are explicitly ignored with `_ =`, following the common pattern where `progressbar/v3` returns an io error when the standard stream is closed, to avoid noisy logs.

## Usage Example

Used directly with `pkg/plot`:

```go
package main

import (
    "github.com/Mr-xiaotian/CelestialGrow/pkg/observer"
    "github.com/Mr-xiaotian/CelestialGrow/pkg/plot"
)

type Seed struct{ ID int }
type Fruit struct{ ID int }

func cultivate(s Seed) (Fruit, error) {
    return Fruit{ID: s.ID}, nil
}

func main() {
    p := plot.NewPlot[Seed, Fruit]("harvester", cultivate)
    p.AddObserver(observer.NewProgressBar("harvesting"))

    seeds := make([]Seed, 1000)
    for i := range seeds {
        seeds[i] = Seed{ID: i}
    }
    p.Run(seeds) // 内部会调用 StartAsync + Seed + Seal + WaitAsync
}
```

Business code usually only needs to import `pkg/api` — `api.NewProgressBar` returns `*observer.ProgressBar` and `api.Plot` is a type alias for `plot.Plot`, so `AddObserver` is equally available:

```go
import "github.com/Mr-xiaotian/CelestialGrow/pkg/api"

p := api.NewPlot[Seed, Fruit]("harvester", cultivate)
p.AddObserver(api.NewProgressBar("harvesting"))
```

After running, `os.Stderr` shows something like:

```
harvesting |█████████-| 1,000/1,000 [100%] 2.5s
```

## Notes

- **The output medium is fixed to `os.Stderr`**: set explicitly via `OptionSetWriter`; if the caller redirects stderr to `/dev/null`, the progress bar becomes invisible but does not affect Plot's normal operation.
- **The width is affected by two options at the same time**: `ensureBar` passes both `OptionSetWidth(10)` and `OptionFullWidth()`, and the actual rendered width is determined by the library in combination with the terminal column count; to obtain a stable width in a narrow terminal, remove `OptionFullWidth()` and keep the fixed `OptionSetWidth`.
- **Re-entrancy safety**: `bar` is assigned at most once (`ensureBar` returns directly when `p.bar != nil`), and concurrent `OnStart` / `OnProgress` / `OnFinish` triggers are serialized by `mu`, so no race occurs.
- **Errors ignored**: the `error` from `bar.Set` / `bar.Finish` is explicitly discarded, consistent with the convention in terminal UI scenarios that "a drawing failure should not affect the main flow".
- **Not reusable**: `ProgressBar` is tied to a single Plot lifecycle and has no `Reset` / `ResetTotal` methods; to reuse it in another batch of tasks, call `NewProgressBar` again.
