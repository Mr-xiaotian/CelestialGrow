# pkg/funnel/spout.go

> 📅 Last Updated: 2026/09/24

`pkg/funnel/spout.go` defines the `Spout[T]` generic abstraction — the **consumer side** (reader) in CelestialGrow's asynchronous consumption infrastructure. It holds a buffered Go channel and, in a background goroutine, continuously reads records and hands them to a `RecordHandler[T]` for processing; it also makes the resource lifecycle explicit through the three-stage `BeforeStart` / `HandleRecord` / `AfterStop` interface.

> **Note on naming**: Although `Spout` intuitively sounds like an "outlet", in this package it is the **consumer side**. `Spout` continuously "spouts" records out of the internal channel to the handler, while the accompanying `Inlet` is the producer side that "pours" records in.

## Purpose

- Continuously read records from a buffered channel in a background goroutine.
- Inject "business processing" through the `RecordHandler[T]` interface, making it reusable across different persistence backends (file / SQLite).
- Provide two-stage shutdown: first `close(ch)` to trigger a graceful exit; if it does not finish within the timeout, `cancel()` forces it to return, ensuring callers never wait forever.

## Core Objects

### `type RecordHandler[T any]` (interface)

```go
type RecordHandler[T any] interface {
    BeforeStart() error
    HandleRecord(record T) error
    AfterStop() error
}
```

| Method | When it is called | Typical use |
|------|-----------|----------|
| `BeforeStart() error` | Inside `Spout.Start`, **before** the consumption goroutine starts | Open files, establish database connections, initialize buffers |
| `HandleRecord(record T) error` | For every record read | Serialization / writing of a single record |
| `AfterStop() error` | The finalization phase of `Spout.Stop`, called **whether or not it timed out** | Close files, `fsync`, release connections |

The `error` values returned by the three methods are treated differently: an error from `BeforeStart` is returned as-is by `Start`; an error from `HandleRecord` is **silently discarded** by `spout()`; an error from `AfterStop` is **silently discarded** by `Stop()` (`Stop` only returns the shutdown timeout error). So do not rely solely on return values in a handler for critical failures.

### `type Spout[T any]`

```go
type Spout[T any] struct {
    ch      chan T               // 内部缓冲通道，对外通过 GetQueue 暴露只写句柄
    wg      sync.WaitGroup       // 跟踪消费 goroutine
    timeout time.Duration        // Stop 时优雅关闭的最大等待时间
    ctx     context.Context      // 内部 ctx，被 Stop 在超时时 cancel
    cancel  context.CancelFunc
    handler RecordHandler[T]     // 注入的处理器
}
```

- **Generic parameter `T`**: the type of a single record. For example, `funnel.NewSpout[persist.LogRecord](...)`.
- All fields are unexported; construction and lifecycle management are only possible through public methods.

### Construction: `NewSpout[T any]`

```go
func NewSpout[T any](handler RecordHandler[T], bufferSize int, timeout time.Duration) *Spout[T]
```

- `handler`: must implement `RecordHandler[T]`; all methods of the implementation are invoked serially.
- `bufferSize`: the capacity of the internal `make(chan T, bufferSize)`. `0` creates an unbuffered channel (legal, but makes `Send` face backpressure immediately); a **negative** value makes `make` panic — the current implementation performs no validation.
- `timeout`: the maximum wait for graceful shutdown in the `Stop` phase. `0` means the "forced cancel" branch is taken almost immediately (`time.After(0)` is immediately readable).

### Public Methods

| Method | Signature | Purpose |
|------|------|------|
| `GetQueue` | `func (s *Spout[T]) GetQueue() chan<- T` | Returns the write-only handle for `Inlet` to bind to |
| `Start` | `func (s *Spout[T]) Start() error` | Calls `handler.BeforeStart` + starts the consumption goroutine |
| `Stop` | `func (s *Spout[T]) Stop() error` | First `close(ch)` for a graceful exit; on timeout `cancel()` forces exit, and finally `handler.AfterStop()` |
| `Handler` | `func (s *Spout[T]) Handler() RecordHandler[T]` | Returns the currently injected handler, handy for debugging or test assertions |

> `Spout` does not provide a synchronous "take one" method; records are pulled from the channel automatically only by the background goroutine, and only "start/stop + queue entry" is exposed externally.

#### `GetQueue() chan<- T`

- Each call returns the same `chan T` (the compiler derives the write-only type from the bidirectional channel).
- Callers are typically `funnel.NewInlet[T](spout.GetQueue(), timeout)` or `persist.NewLogInlet(spout.GetQueue(), ...)`.
- **Do not** write to or close this channel elsewhere — ownership belongs to `Spout`.

#### `Start() error`

1. Calls `s.handler.BeforeStart()`; if it returns a non-`nil` error, the goroutine is **not** started and that error is returned directly.
2. `s.wg.Add(1)` followed by `go s.spout()`.
3. Returns immediately on success (non-blocking); before an error is returned, `BeforeStart` may already have opened resources such as files, and the caller should decide whether compensating cleanup is needed.

#### `Stop() error`

```text
Stop()
  ├─ close(s.ch)                      // 触发 spout() 在 <-s.ch 处返回
  ├─ 等待 wg.Wait() done 通道，最多 timeout
  │     ├─ 正常 done        → err = nil
  │     └─ timeout 触发     → s.cancel() 强制 spout 走 <-s.ctx.Done()
  │                            err = errors.New("shutdown timeout")
  └─ 无论是否超时，都执行 s.handler.AfterStop()          // 返回值被丢弃
```

Key points:

- `close(s.ch)` is **required** before the `ok=false` branch of `<-s.ch` can be hit, letting `spout()` return gracefully.
- The internal `ctx` is only `cancel()`ed on **timeout**, so **on the normal path `ctx` is never triggered**; this is a design choice to avoid killing a handler that is still processing.
- `AfterStop` is called on **all** exit paths, ensuring resources are not leaked (`defer` semantics).
- The returned `error` is non-`nil` only in the timeout case, so callers can use it to decide whether a retry/alert is needed.

#### `Handler() RecordHandler[T]`

- Returns the handler injected at construction time (a copy of the interface value). Useful for reporting handler state at runtime, unit test assertions, and so on.
- Read-only, and **cannot** be used to replace the handler: the `handler` field is unexported and has no setter, so modifying this copy does not affect the instance held internally by `Spout`.

### Internal Method `spout()` (unexported)

The consumption loop:

```go
for {
    select {
    case record, ok := <-s.ch:
        if !ok { return }                    // 通道关闭：优雅退出
        s.handler.HandleRecord(record)       // 返回值被丢弃
    case <-s.ctx.Done():
        return                              // Stop 超时：强制退出
    }
}
```

- It is the goroutine entry started by `Start`; `wg.Done()` in a `defer` guarantees the counter reaches zero.
- Serial processing: a single goroutine calls `HandleRecord` in order, so the handler needs no locking of its own.
- Two exit paths: `ok=false` (the channel was closed by `Stop`) or `ctx.Done()` (`Stop` timed out and forced exit).

## Integration Points with Plot / persist

- `persist.LogRecordHandler` / `persist.LifecycleRecordHandler` both implement `RecordHandler[T]`:
  - `BeforeStart`: creates the `logs/` directory and opens a date-named log file / creates the `lifecycles/<date>/` directory and opens SQLite.
  - `HandleRecord`: formats into a single line of text appended to the file / inserts the event by `Kind` (`seed` / `ripen` / `wither`) and updates the state snapshot.
  - `AfterStop`: closes the log file / closes the SQLite connection.
- `Farm.NewFarm` creates both `Spout`s at construction time: `logSpout` (handling `persist.LogRecord`) and `lifecycleSpout` (handling `persist.LifecycleRecord`), and feeds their `GetQueue()` handles to `LogInlet` and `LifecycleInlet` respectively.
- `Plot` does not hold a `Spout` directly in Farm mode — the channel is injected by `Farm.Run` through `BindInlet`; in standalone mode, `Plot.Run` creates the two `Spout`s itself and controls start/stop with `StartSpouts` / `StopSpouts`.

```text
persist.LogInlet / LifecycleInlet ──内嵌──▶ funnel.Inlet[T] ──Send(T)──┐
                                                                      ▼
                                              chan T（Spout 内部缓冲通道）
                                                                      │
                          ┌──GetQueue() 把同一个 chan T 暴露给 Inlet───┘
                          ▼
funnel.Spout[T]（后台消费 goroutine）──HandleRecord(T)──▶ persist.LogRecordHandler / LifecycleRecordHandler
```

## Collaboration with Inlet (inlet ← spout)

```go
// 1. Spout 持有通道
spout := funnel.NewSpout[persist.LogRecord](&persist.LogRecordHandler{}, 100, time.Second)

// 2. 启动消费循环
spout.Start()
defer spout.Stop()

// 3. Inlet 绑定到 Spout 暴露的只写句柄
inlet := funnel.NewInlet[persist.LogRecord](spout.GetQueue(), 500*time.Millisecond)
defer inlet.Close()

// 4. 上游只需调 inlet.Send(...)；通道满了会等 500ms，超时返回错误
```

Ordering guarantees:

1. `GetQueue` and constructing the `Inlet` must happen only after `Spout.Start` — otherwise sent data may be lost (no consumer).
2. The order `defer inlet.Close()` + `defer spout.Stop()` guarantees "stop writing first, then stop reading", preventing the writer from trying to send data after `Stop`.
3. `Spout.Stop` calls `close(ch)` internally, so you **must first confirm that no goroutine will call `Send` anymore** before calling `Stop`; writing concurrently to a closed channel panics (`send on closed channel`). Note that `inlet.Close()` only cancels `ctx`; it **cannot** prevent a `Send` that has already entered `select` from hitting the write branch and panicking — `Close` only guarantees that "a blocked `Send` returns `context.Canceled` quickly". **Recommended order**: upstream stops calling `Send` → `inlet.Close()` → `spout.Stop()`.

## Usage Example

A minimal `Spout` + `Inlet` combination (companion to `inlet.md`):

```go
package main

import (
    "fmt"
    "sync"
    "time"

    "github.com/Mr-xiaotian/CelestialGrow/pkg/funnel"
)

type PrintHandler struct {
    mu  sync.Mutex
    cnt int
}

func (p *PrintHandler) BeforeStart() error       { fmt.Println("spout start"); return nil }
func (p *PrintHandler) HandleRecord(r int) error { p.mu.Lock(); p.cnt++; p.mu.Unlock(); return nil }
func (p *PrintHandler) AfterStop() error         { fmt.Println("spout stop, total:", p.cnt); return nil }

func main() {
    handler := &PrintHandler{}

    spout := funnel.NewSpout[int](handler, 16, time.Second)
    if err := spout.Start(); err != nil {
        panic(err)
    }

    inlet := funnel.NewInlet[int](spout.GetQueue(), 200*time.Millisecond)
    for i := 0; i < 100; i++ {
        _ = inlet.Send(i)
    }
    inlet.Close() // 先停写入
    if err := spout.Stop(); err != nil {
        fmt.Println("shutdown timeout:", err)
    }
}
```

## Notes

- `Spout` consumes **serially on a single goroutine**; `HandleRecord` should not perform time-consuming operations (such as synchronous network requests), or it will become the bottleneck of the whole pipeline. If parallel processing is needed, dispatch again inside `HandleRecord`.
- `bufferSize` and `timeout` are the key tuning points:
  - The larger `bufferSize` is, the more burst traffic can be absorbed, but the higher the memory usage.
  - The larger `timeout` is, the more "gentle" the shutdown; but Farm / CLI exit will be forced to wait longer.
- The error from `RecordHandler.HandleRecord` is currently ignored — **do not** put critical failure state into its return value; if failures must be observable, instrument inside the handler (see the design of `persist.LogInlet` / `LifecycleInlet`).
- A `Spout` instance cannot be reused after `Stop`: `ch` is already `close`d, so calling `Start` again only starts a goroutine that exits immediately due to `ok=false`, and calling `Stop` again panics from closing an already closed channel twice (`close of closed channel`).
- The reference returned by `Handler()` is read-only for replacement purposes; externally modifying its internal state is allowed (for example, querying the number of written records), but replacing the whole handler has no effect.
