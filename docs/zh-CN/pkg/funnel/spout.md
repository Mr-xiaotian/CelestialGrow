# pkg/funnel/spout.go

> 最后更新日期: 2026/09/01

`pkg/funnel/spout.go` 定义了 `Spout[T]` 泛型抽象——CelestialGrow 异步消费基础设施中的**消费端**（读取者）。它持有一个带缓冲的 Go channel，在后台 goroutine 中持续读取记录并交给 `RecordHandler[T]` 处理；同时通过 `BeforeStart` / `HandleRecord` / `AfterStop` 三段式接口明确资源生命周期。

> **命名提示**：尽管 `Spout` 直观上像"出口"，但在本包中它是**消费端**。`Spout` 不断从内部通道"喷出"记录给 handler，配套的 `Inlet` 才是"灌入"记录的生产端。

## 作用

- 在后台 goroutine 中持续从带缓冲 channel 读取记录。
- 把"业务处理"通过 `RecordHandler[T]` 接口注入，便于在不同持久化后端（文件 / SQLite）之间复用。
- 提供两阶段关闭：先 `close(ch)` 触发优雅退出；超时未结束则 `cancel()` 强制返回，保证调用方不会无限等待。

## 核心对象

### `type RecordHandler[T any]`（接口）

```go
type RecordHandler[T any] interface {
    BeforeStart() error
    HandleRecord(record T) error
    AfterStop() error
}
```

| 方法 | 何时被调用 | 典型用途 |
|------|-----------|----------|
| `BeforeStart() error` | `Spout.Start` 内部、消费 goroutine 启动**前** | 打开文件、建立数据库连接、初始化 buffer |
| `HandleRecord(record T) error` | 每读到一条记录 | 单条记录的序列化 / 写入 |
| `AfterStop() error` | `Spout.Stop` 收尾阶段，**无论是否超时**都会调用 | 关闭文件、`fsync`、释放连接 |

返回 `error` 的方法（`BeforeStart` / `HandleRecord`）当前**不**被 `Spout` 用于中断流程（`HandleRecord` 的错误会被忽略），但保留扩展点。

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

- **泛型参数 `T`**：单条记录的类型。例如 `funnel.NewSpout[persist.LogRecord](...)`。
- 字段全部不导出，构造与生命周期管理只能通过公开方法。

### 构造：`NewSpout[T any]`

```go
func NewSpout[T any](handler RecordHandler[T], bufferSize int, timeout time.Duration) *Spout[T]
```

- `handler`：必须实现 `RecordHandler[T]`；实现类的所有方法都会被串行调用。
- `bufferSize`：内部 `make(chan T, bufferSize)` 的容量，**必须 ≥ 1**。当前实现未对 `0` 做防护，调用方需自行保证。
- `timeout`：`Stop` 阶段优雅关闭的最长等待时间。`0` 表示几乎立即走"强制取消"分支（`time.After(0)` 立即可读）。

### 公开方法

| 方法 | 签名 | 作用 |
|------|------|------|
| `GetQueue` | `func (b *Spout[T]) GetQueue() chan<- T` | 返回只写句柄，供 `Inlet` 绑定 |
| `Start` | `func (b *Spout[T]) Start() error` | 调 `handler.BeforeStart` + 启动消费 goroutine |
| `Stop` | `func (b *Spout[T]) Stop() error` | 先 `close(ch)` 优雅退出；超时则 `cancel()` 强退，并最终 `handler.AfterStop()` |
| `Handler` | `func (b *Spout[T]) Handler() RecordHandler[T]` | 返回当前注入的 handler，便于运行时反射或调试 |

> `Emit` / `Next` 等方法**并不存在**于 `Spout` 上；它通过后台 goroutine 自动从 channel 拉取数据，对外仅暴露"启停 + 队列入口"。

#### `GetQueue() chan<- T`

- 每次调用都返回同一个 `chan T`（编译器会从双向通道派生只写类型）。
- 调用方通常是 `funnel.NewInlet[T](spout.GetQueue(), timeout)` 或 `persist.NewLogInlet(spout.GetQueue(), ...)`。
- **不要**在其他地方写入或关闭此通道——所有权归 `Spout`。

#### `Start() error`

1. 调 `b.handler.BeforeStart()`；若返回非 `nil` 错误则**不会**启动 goroutine，直接返回。
2. `b.wg.Add(1)` 后 `go b.spout()`。
3. 成功后立即返回（非阻塞）；错误返回前 `BeforeStart` 可能已经打开了文件等资源，调用方应自行决定是否需要补偿清理。

#### `Stop() error`

```text
Stop()
  ├─ close(ch)                        // 触发 spout() 在 <-b.ch 处返回
  ├─ 等待 wg.Wait() done 通道，最多 timeout
  │     ├─ 正常 done        → err = nil
  │     └─ timeout 触发     → b.cancel() 强制 spout 走 <-b.ctx.Done()
  │                            err = errors.New("shutdown timeout")
  └─ 无论是否超时，都执行 b.handler.AfterStop()
```

关键点：

- **必须** `close(ch)` 后才能让 `<-b.ch` 的 `ok=false` 分支命中，从而让 `spout()` 优雅返回。
- 内部 `ctx` 仅在**超时**时才会被 `cancel()`，因此**正常路径下 `ctx` 永远不会被触发**；这是设计选择，避免误杀仍在处理中的 handler。
- `AfterStop` 在**所有**退出路径下都会被调用，确保资源不泄漏（`defer` 语义）。
- 返回 `error` 仅在超时场景下为非 `nil`，调用方可据此判定是否需要重试/告警。

#### `Handler() RecordHandler[T]`

- 返回构造时注入的 handler 引用。可用于运行时上报 handler 状态、单元测试断言等。
- 不应被外部用来替换 handler（替换在当前实现中不会生效，因为 `spout()` 持有的是字段引用）。

### 内部方法 `spout()`（不导出）

消费循环：

```go
for {
    select {
    case record, ok := <-b.ch:
        if !ok { return }                    // 通道关闭：优雅退出
        b.handler.HandleRecord(record)
    case <-b.ctx.Done():
        return                              // Stop 超时：强制退出
    }
}
```

- 是 `Start` 启动的 goroutine 入口；`wg.Done()` 在 `defer` 中保证计数归零。
- 串行处理：单 goroutine 顺序调用 `HandleRecord`，无需在 handler 内自加锁。

## 与 Plot / persist 的对接点

- `persist.LogRecordHandler` / `persist.LifecycleRecordHandler` 都实现了 `RecordHandler[T]`：
  - `BeforeStart`：打开日志文件 / SQLite 连接。
  - `HandleRecord`：按行格式化后写入文件 / 执行 SQL。
  - `AfterStop`：`fsync` + 关闭文件。
- `Farm.NewFarm` 在构造时同时创建两个 `Spout`：`logSpout`（处理 `persist.LogRecord`）和 `lifecycleSpout`（处理 `persist.LifecycleRecord`），并把它们的 `GetQueue()` 句柄分别喂给 `LogInlet` 和 `LifecycleInlet`。
- `Plot` 在 `BindInlet` 之前不需要直接接触 `Spout`；它只持有"上游绑定的 Inlet 视图"。

```text
funnel.Spout[T]  ──GetQueue──▶  chan T  ──Send──▶  funnel.Inlet[T]  ──内嵌──▶  persist.LogInlet / LifecycleInlet
        │                                                              ▲
        │ Handler()                                                    │
        ▼                                                              │
persist.LogRecordHandler / LifecycleRecordHandler  ◀──Plot 通过埋点方法间接写入── Plot / Farm 业务侧
```

## 与 Inlet 的协作（inlet ← spout）

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

时序保证：

1. `Spout.Start` 之后才能 `GetQueue` 并构造 `Inlet`——否则发送的数据可能丢失（无消费者）。
2. `defer inlet.Close()` + `defer spout.Stop()` 的顺序保证了"先停写入、再停读取"，避免写入方在 `Stop` 之后还尝试发数据。
3. `Spout.Stop` 内部 `close(ch)` 之前，所有 `Inlet` 应已经 `Close`，否则 `Send` 会在 `Stop` 期间因通道关闭而 panic（`send on closed channel`）。**正确的关闭顺序**：上游 `inlet.Close()` → 下游 `spout.Stop()`。

## 使用示例

最小化的 `Spout` + `Inlet` 组合（与 `inlet.md` 配套）：

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

## 注意事项

- `Spout` 是**单 goroutine 串行**消费；`HandleRecord` 不应做耗时操作（如同步网络请求），否则会成为整个流水线的瓶颈。如需并行处理，请在 `HandleRecord` 内部再分派。
- `bufferSize` 与 `timeout` 是关键调参点：
  - `bufferSize` 越大，能吸收的突发流量越多，但内存占用越高。
  - `timeout` 越大，关闭越"温和"；但 Farm / CLI 退出时会被迫等更久。
- `RecordHandler.HandleRecord` 当前错误被忽略，**不要**把关键错误状态写进它的返回值；如需失败可观测，应在 handler 内部自行埋点（参考 `persist.LogInlet` / `LifecycleInlet` 的设计）。
- `Stop` 之后 `Spout` 实例不可复用——`ch` 已 `close` 且 `ctx` 已 `cancel`，再 `Start` 会出现 `close of closed channel`。
- `Handler()` 返回的引用是只读的，外部修改其内部状态是允许的（例如查询已写入条数），但替换整个 handler 不会生效。
