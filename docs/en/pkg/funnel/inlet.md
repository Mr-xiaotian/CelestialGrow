# pkg/funnel/inlet.go

> 📅 Last Updated: 2026/09/24

`pkg/funnel/inlet.go` defines the `Inlet[T]` generic abstraction — the **producer side** (writer) in CelestialGrow's asynchronous consumption infrastructure. It feeds records produced by upstream components (`Plot`, `Farm`) into a buffered Go channel, which is consumed asynchronously by the counterpart `Spout[T]`. `Inlet` carries its own `context.Context` that `Close` can cancel proactively, and every send is protected by a timeout so that upstream never gets blocked indefinitely by a slow consumer.

> **Note on naming**: Although `Inlet` intuitively sounds like an "entry point", in this package it is the **write side / producer side**; the real "consumption loop entry point" is `Spout`. `Inlet` "pours" records into the channel, while `Spout` "spouts" records out of the channel and hands them to a `RecordHandler`.

## Purpose

- Provide a safe `Send` with **timeout** and **context cancellation**, avoiding unbounded blocking.
- Declare "write-only" semantics explicitly via `chan<- T`, handing channel ownership to the counterpart `Spout`.
- Provide an embeddable "producer base class" for upper layers (`persist.LogInlet`, `persist.LifecycleInlet`).

## Core Objects

### `type Inlet[T any]`

```go
type Inlet[T any] struct {
    ch      chan<- T        // 只写通道，由对端 Spout 持有
    timeout time.Duration   // 单次 Send 的最大等待时间
    ctx     context.Context // 内部 context，Close 时被取消
    cancel  context.CancelFunc
}
```

- **Generic parameter `T`**: the type of a single record. For example, `persist.LogInlet` uses `T = persist.LogRecord`.
- **No fields are exported**; the outside can only construct it through `NewInlet`.

### Construction: `NewInlet[T any]`

```go
func NewInlet[T any](ch chan<- T, timeout time.Duration) *Inlet[T]
```

- `ch`: must be the "write-only" handle obtained from `Spout.GetQueue()` (Go automatically converts a bidirectional `chan T` into a `chan<- T`).
- `timeout`: the maximum wait for a single `Send`. `0` is **not** "no timeout" — the channel from `time.After(0)` becomes readable immediately, so when the write branch is not ready `Send` returns a timeout error right away; pass a positive duration.
- Internally, an independent `ctx` is created rooted at `context.Background()`, so cancelling one `Inlet` does not affect other `Inlet`s.

### Public Methods

| Method | Signature | Purpose |
|------|------|------|
| `Send` | `func (s *Inlet[T]) Send(record T) error` | Write one record; returns an error on context cancellation or timeout |
| `Close` | `func (s *Inlet[T]) Close()` | Cancel the internal `ctx`, making all blocked `Send` calls return an error immediately |

#### `Send(record T) error`

The behavior is a `select` racing three branches:

1. `s.ch <- record`: written successfully, returns `nil`.
2. `<-s.ctx.Done()`: returns `s.ctx.Err()` (`context.Canceled`).
3. `<-time.After(s.timeout)`: returns `fmt.Errorf("inlet send timeout after %v", s.timeout)`.

> **Backpressure strategy**: when the channel is full and the counterpart `Spout` has not consumed yet, `Send` blocks for up to `timeout`; a timeout is treated as excessive upstream pressure, so callers are advised to record metrics or trigger degradation. `Close` makes all blocked `Send` calls return `context.Canceled` immediately through branch 2.
>
> **Beware of `select` randomness**: when the write branch and `ctx.Done()` are both ready (the channel still has room and `Close` has been called), Go picks randomly among the ready branches, so a `Send` after `Close` **may still succeed** and return `nil`. When you need "never send again" semantics, the caller should simply stop calling `Send`.

#### `Close()`

- Calls `s.cancel()` to cancel the internal `ctx`; it does **not** close the channel directly — channel ownership lies with `Spout` (`Spout.Stop` is responsible for `close(ch)`).
- Can be called repeatedly; it is idempotent.

## Integration Points with Plot / persist

```text
Plot 内部调用
   │ logInlet.SeedRipen(...)        // 业务侧
   │ lifecycleInlet.SeedRipen(...)
   ▼
persist.LogInlet  ──内嵌──▶  funnel.Inlet[LogRecord]
persist.LifecycleInlet ──内嵌──▶  funnel.Inlet[LifecycleRecord]
                            │
                            ▼  Send(record)
                       chan<- LogRecord  ◀── 来自 Spout.GetQueue()
                            │
                            ▼
funnel.Spout[LogRecord]  ──回调──▶  persist.LogRecordHandler.HandleRecord
```

Key points:

- `persist.LogInlet` **embeds** `funnel.Inlet[LogRecord]` and exposes semantic methods such as `FarmStart` / `FarmEnd` / `PlotStart` / `PlotEnd` / `SeedInput` / `SeedRipen` / `SeedWither` / `SeedReplant`; its private `log` method still goes through `Inlet.Send`. Logs below `minLevel` are dropped at the `log` entry point and do **not** occupy the channel.
- `persist.LifecycleInlet` likewise embeds `Inlet[LifecycleRecord]` and layers the three lifecycle hooks `SeedInput` / `SeedRipen` / `SeedWither` on top of it.
- `Plot.BindInlet` is called uniformly by `Farm.Run`, passing `f.logSpout.GetQueue()` and `f.lifecycleSpout.GetQueue()`, thereby binding the "producer side" and "consumer side" to the same channel.
- In standalone mode, `Plot` holds the `Spout` itself and controls start/stop via `StartSpouts` / `StopSpouts`; the channel likewise comes from `Spout.GetQueue()`.

## Usage Example

A minimal `Inlet` + `Spout` combination, demonstrating the asynchronous persistence flow of an integer counter:

```go
package main

import (
    "fmt"
    "time"

    "github.com/Mr-xiaotian/CelestialGrow/pkg/funnel"
)

// FileHandler 演示用：把每条记录落到标准输出。
type FileHandler struct{ count int }

func (h *FileHandler) BeforeStart() error         { return nil }
func (h *FileHandler) HandleRecord(r int) error   { h.count++; fmt.Println("got:", r); return nil }
func (h *FileHandler) AfterStop() error           { fmt.Println("total:", h.count); return nil }

func main() {
    handler := &FileHandler{}

    // 1) 构造 Spout（消费端），它持有双向 channel 并暴露只写句柄。
    spout := funnel.NewSpout[int](handler, 8, 2*time.Second)
    if err := spout.Start(); err != nil {
        panic(err)
    }
    defer spout.Stop()

    // 2) 构造 Inlet（生产端），绑定到 spout 的写入句柄。
    inlet := funnel.NewInlet[int](spout.GetQueue(), 500*time.Millisecond)
    defer inlet.Close()

    // 3) 发送一批记录；通道满时 Inlet 会在 500ms 内等待。
    for i := 0; i < 5; i++ {
        if err := inlet.Send(i); err != nil {
            fmt.Println("send err:", err)
        }
    }

    time.Sleep(100 * time.Millisecond) // 给 spout 时间消费
}
```

## Notes

- The internal `ctx` of `Inlet` is only cancelled on `Close`; the `Send` timeout and `ctx` are **parallel** conditions, and the caller needs to handle both kinds of error.
- `Send` errors are **not retried**. Retry policies should be implemented at the upper layer (such as `Plot`'s `maxRetries`) to avoid introducing hidden loops inside `Inlet`.
- Channel ownership belongs to `Spout` (`Spout.Stop` is responsible for `close(ch)` internally); `Inlet` must never `close(ch)`: closing twice panics, and any subsequent `Send` that hits the write branch will also panic (`send on closed channel`).
- Subtypes that embed `Inlet` (such as `persist.LogInlet`) embed a **value** (`funnel.Inlet[LogRecord]`), and the zero value is unusable: the constructor must call `funnel.NewInlet` to complete initialization, otherwise `Send` / `Close` panic because `ctx` / `cancel` are `nil`.
- With `timeout=0`, `time.After(0)` is immediately readable and `Send` almost always takes the timeout branch (when `ctx` is already cancelled, the two branches are selected randomly) — always set a sensible upper bound in production.
