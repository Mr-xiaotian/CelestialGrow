# pkg/funnel/inlet.go

> 📅 Last Updated: 2026/11/13

`pkg/funnel/inlet.go` defines the `Inlet[T]` generic abstraction - the **producer side** (writer) in CelestialGrow's asynchronous consumption infrastructure. It feeds records from upstream components into a buffered Go channel, which the counterpart `Spout[T]` consumes asynchronously. `Inlet` carries its own independent `context.Context` that `Close` can cancel proactively; every send is also protected by a timeout, so upstream is never blocked indefinitely by a slow consumer.

> **Note on naming**: although `Inlet` intuitively sounds like an "entry point", in this package it is the **write side / producer side**; the real "consumption loop entry point" is `Spout`. `Inlet` "pours" records into the channel, while `Spout` "spouts" records out of the channel and hands them to a `RecordHandler`.

## Purpose

- Provide a safe `Send` with **timeout** and **context cancellation**, avoiding unbounded blocking.
- Declare "write-only" semantics explicitly via `chan<- T`, handing channel ownership to the counterpart `Spout`.
- Serve as the write channel for the watchers of the `persist` layer: `persist.LogWatcher` / `persist.LifecycleWatcher` each hold a `*funnel.Inlet[T]`, and `OnEvent` only translates an event into a record and delivers it.

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

- **Generic parameter `T`**: the type of a single record. For example, `persist.LogWatcher` uses `T = persist.LogRecord`.
- **No fields are exported**; the outside can only construct it through `NewInlet`.

### Construction: `NewInlet[T any]`

```go
func NewInlet[T any](ch chan<- T, timeout time.Duration) *Inlet[T]
```

- `ch`: must be the "write-only" handle obtained from `Spout.GetQueue()` (Go automatically converts a bidirectional `chan T` into a `chan<- T`).
- `timeout`: the maximum wait for a single `Send`. `0` is **not** "no timeout" - the channel from `time.After(0)` becomes readable immediately, so when the write branch is not ready `Send` returns a timeout error right away; pass a positive duration.
- Internally an independent `ctx` is created rooted at `context.Background()`, so cancelling one `Inlet` does not affect other `Inlet`s.

### Public Methods

| Method | Signature | Purpose |
|------|------|------|
| `Send` | `func (s *Inlet[T]) Send(record T) error` | Write one record; returns an error on context cancellation or timeout |
| `Close` | `func (s *Inlet[T]) Close()` | Cancel the internal `ctx`, making a blocked `Send` return an error immediately |

#### `Send(record T) error`

The behavior is a `select` racing three branches:

1. `s.ch <- record`: written successfully, returns `nil`.
2. `<-s.ctx.Done()`: returns `s.ctx.Err()` (normally `context.Canceled`).
3. `<-time.After(s.timeout)`: returns `fmt.Errorf("inlet send timeout after %v", s.timeout)`, for example `inlet send timeout after 1s`.

> **Backpressure strategy**: when the channel is full and the counterpart `Spout` is not consuming yet, `Send` blocks for up to `timeout`; a timeout means upstream pressure is too high, so callers are advised to record metrics or trigger degradation.
>
> **Beware of `select` randomness**: when the write branch and `ctx.Done()` are both ready (the channel still has room and `Close` has been called), Go picks randomly among the ready branches, so a `Send` after `Close` **may still write successfully** and return `nil`. When you need "definitely never send again" semantics, the caller should stop calling `Send` itself.

#### `Close()`

- Calls `s.cancel()` to cancel the internal `ctx`; it does **not** close the channel directly - channel ownership lies with `Spout` (`Spout.Stop` is responsible for `close(ch)`).
- Can be called repeatedly (`context.CancelFunc` is idempotent).

## Concurrency Semantics

- `Send` performs only the atomic operation "write to the channel" and **can be called concurrently by multiple goroutines** without external locking. This is exactly what the `persist` watchers rely on: `Hub` may call `OnEvent` synchronously from several Plot goroutines, which then lands concurrently on the same `Inlet`.
- Concurrent calls to `Close` and `Send` produce no data race (`cancel` is concurrency-safe), but as noted above, `Close` does not guarantee that "no write succeeds afterwards".
- `Inlet` itself does not synchronize the handler: multiple records written concurrently enter the same queue and are eventually passed to `HandleRecord` serially by the **single** consumer goroutine of `Spout`.

## Call Relationships with persist / assembly

```text
persist.LogWatcher / LifecycleWatcher
   │ OnEvent(event)              // Hub 分发进来的业务事件
   │   └─ 翻译为 LogRecord / LifecycleRecord
   ▼
funnel.Inlet[T].Send(record)
   │
   ▼  chan<- T（来自 Spout.GetQueue()）
funnel.Spout[T] ──HandleRecord──▶ RecordHandler[T]
```

Key points:

- `persist.LogWatcher` / `persist.LifecycleWatcher` **hold** (rather than embed) a `*funnel.Inlet[T]`, constructed by `funnel.NewInlet(spout.GetQueue(), time.Second)` in the unexported `newLogWatcher` / `newLifecycleWatcher`.
- Both watchers use the same queue parameters: `funnel.NewSpout(handler, 100, time.Second)` + `funnel.NewInlet(..., time.Second)`, i.e. **100 records of buffering**, with the `Send` timeout and the graceful-shutdown timeout of `Spout.Stop` both set to **1 second**.
- `LogWatcher` has another level filter between `OnEvent` and `Send`: the private method `log(level, message, at)` drops records below `minLevel`, and those records **do not** occupy the channel.
- Errors from `Send` are returned as-is through `OnEvent` (`LogWatcher.OnEvent` / `LifecycleWatcher.OnEvent` return the `Send` error directly), but `Hub` catches watcher errors and hands them to the `ErrorHandler` while always returning `nil`, so a timeout never interrupts business execution.
- `Plot` / `Farm` **no longer** hold `Inlet` or `Spout` directly: they only hold `scarecrow.Hub`, and the watchers are created and started by `StartPlotResources` / `StartFarmResources` in `pkg/assembly`; at shutdown, `Resources.Stop` (internally `sync.Once`) stops the log watcher first and then the lifecycle watcher.

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

func (h *FileHandler) BeforeStart() error       { return nil }
func (h *FileHandler) HandleRecord(r int) error { h.count++; fmt.Println("got:", r); return nil }
func (h *FileHandler) AfterStop() error         { fmt.Println("total:", h.count); return nil }

func main() {
    handler := &FileHandler{}

    // 1) 构造 Spout（消费端），它持有双向 channel 并暴露只写句柄。
    spout := funnel.NewSpout[int](handler, 8, 2*time.Second)
    if err := spout.Start(); err != nil {
        panic(err)
    }

    // 2) 构造 Inlet（生产端），绑定到 spout 的写入句柄。
    inlet := funnel.NewInlet[int](spout.GetQueue(), 500*time.Millisecond)

    // 3) 发送一批记录；通道满时 Inlet 会在 500ms 内等待。
    for i := 0; i < 5; i++ {
        if err := inlet.Send(i); err != nil {
            fmt.Println("send err:", err)
        }
    }

    // 4) 先停写入，再停消费：Stop 会 close(ch) 并等待剩余记录处理完。
    inlet.Close()
    if err := spout.Stop(); err != nil {
        fmt.Println("stop err:", err)
    }
}
```

> Order matters: `Spout.Stop()` closes the channel, and you must make sure there is no `Send` at that moment. `inlet.Close()` only makes a **blocked** `Send` return early; it cannot stop a `Send` that has already entered `select` from hitting the write branch.

## Notes

- The internal `ctx` of `Inlet` is cancelled only on `Close`; the `Send` timeout and `ctx` are **parallel** conditions, so the caller must handle both kinds of error (`context.Canceled` and `inlet send timeout after ...`).
- `Send` errors are **not retried**. Retry policies belong to the upper layer; do not introduce hidden loops inside `Inlet`.
- Channel ownership belongs to `Spout` (`Spout.Stop` is responsible for `close(ch)` internally); `Inlet` must never `close(ch)`: closing twice panics, and any later `Send` that hits the write branch also panics (`send on closed channel`).
- The zero value of `Inlet` is unusable: when `ctx` / `cancel` are `nil`, `Send` panics. It must be constructed via `funnel.NewInlet`; types that hold it (such as `persist.LogWatcher`) use a **pointer** field, and it should likewise be treated as a member that can only be initialized in the constructor.
- With `timeout=0`, `time.After(0)` is immediately readable, so `Send` almost always takes the timeout branch (when `ctx` is already cancelled the two branches are chosen at random) - always set a sensible upper bound in production.
