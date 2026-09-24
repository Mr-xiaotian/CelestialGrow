# pkg/funnel/spout.go

> 📅 最后更新日期: 2026/09/24

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

三个方法返回的 `error` 待遇不同：`BeforeStart` 的错误会由 `Start` 原样返回；`HandleRecord` 的错误会被 `spout()` **静默丢弃**；`AfterStop` 的错误会被 `Stop()` **静默丢弃**（`Stop` 只返回关闭超时错误）。因此不要在 handler 中把关键失败仅寄托于返回值。

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
- `bufferSize`：内部 `make(chan T, bufferSize)` 的容量。`0` 会创建无缓冲通道（合法，但会让 `Send` 立即面临背压）；传入**负数**会让 `make` panic，当前实现不做校验。
- `timeout`：`Stop` 阶段优雅关闭的最长等待时间。`0` 表示几乎立即走"强制取消"分支（`time.After(0)` 立即可读）。

### 公开方法

| 方法 | 签名 | 作用 |
|------|------|------|
| `GetQueue` | `func (s *Spout[T]) GetQueue() chan<- T` | 返回只写句柄，供 `Inlet` 绑定 |
| `Start` | `func (s *Spout[T]) Start() error` | 调 `handler.BeforeStart` + 启动消费 goroutine |
| `Stop` | `func (s *Spout[T]) Stop() error` | 先 `close(ch)` 优雅退出；超时则 `cancel()` 强退，并最终 `handler.AfterStop()` |
| `Handler` | `func (s *Spout[T]) Handler() RecordHandler[T]` | 返回当前注入的 handler，便于调试或测试断言 |

> `Spout` 不提供同步的「取一条」方法；记录仅由后台 goroutine 自动从通道拉取，对外只暴露「启停 + 队列入口」。

#### `GetQueue() chan<- T`

- 每次调用都返回同一个 `chan T`（编译器会从双向通道派生只写类型）。
- 调用方通常是 `funnel.NewInlet[T](spout.GetQueue(), timeout)` 或 `persist.NewLogInlet(spout.GetQueue(), ...)`。
- **不要**在其他地方写入或关闭此通道——所有权归 `Spout`。

#### `Start() error`

1. 调 `s.handler.BeforeStart()`；若返回非 `nil` 错误则**不会**启动 goroutine，直接返回该错误。
2. `s.wg.Add(1)` 后 `go s.spout()`。
3. 成功后立即返回（非阻塞）；错误返回前 `BeforeStart` 可能已经打开了文件等资源，调用方应自行决定是否需要补偿清理。

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

关键点：

- **必须** `close(s.ch)` 后才能让 `<-s.ch` 的 `ok=false` 分支命中，从而让 `spout()` 优雅返回。
- 内部 `ctx` 仅在**超时**时才会被 `cancel()`，因此**正常路径下 `ctx` 永远不会被触发**；这是设计选择，避免误杀仍在处理中的 handler。
- `AfterStop` 在**所有**退出路径下都会被调用，确保资源不泄漏（`defer` 语义）。
- 返回 `error` 仅在超时场景下为非 `nil`，调用方可据此判定是否需要重试/告警。

#### `Handler() RecordHandler[T]`

- 返回构造时注入的 handler（接口值的副本）。可用于运行时上报 handler 状态、单元测试断言等。
- 只能读取，**不能**用来替换 handler：`handler` 字段不导出且没有 setter，修改这个副本不会影响 `Spout` 内部持有的实例。

### 内部方法 `spout()`（不导出）

消费循环：

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

- 是 `Start` 启动的 goroutine 入口；`wg.Done()` 在 `defer` 中保证计数归零。
- 串行处理：单 goroutine 顺序调用 `HandleRecord`，无需在 handler 内自加锁。
- 两条退出路径：`ok=false`（通道被 `Stop` 关闭）或 `ctx.Done()`（`Stop` 超时强退）。

## 与 Plot / persist 的对接点

- `persist.LogRecordHandler` / `persist.LifecycleRecordHandler` 都实现了 `RecordHandler[T]`：
  - `BeforeStart`：创建 `logs/` 目录并打开按日期命名的日志文件 / 创建 `lifecycles/<date>/` 目录并打开 SQLite。
  - `HandleRecord`：格式化为一行文本追加写入文件 / 按 `Kind`（`seed` / `ripen` / `wither`）插入事件并更新状态快照。
  - `AfterStop`：关闭日志文件 / 关闭 SQLite 连接。
- `Farm.NewFarm` 在构造时同时创建两个 `Spout`：`logSpout`（处理 `persist.LogRecord`）和 `lifecycleSpout`（处理 `persist.LifecycleRecord`），并把它们的 `GetQueue()` 句柄分别喂给 `LogInlet` 和 `LifecycleInlet`。
- `Plot` 在 Farm 模式下不直接持有 `Spout`——通道由 `Farm.Run` 通过 `BindInlet` 注入；standalone 模式下则由 `Plot.Run` 自己创建两个 `Spout`，并用 `StartSpouts` / `StopSpouts` 控制启停。

```text
persist.LogInlet / LifecycleInlet ──内嵌──▶ funnel.Inlet[T] ──Send(T)──┐
                                                                      ▼
                                              chan T（Spout 内部缓冲通道）
                                                                      │
                          ┌──GetQueue() 把同一个 chan T 暴露给 Inlet───┘
                          ▼
funnel.Spout[T]（后台消费 goroutine）──HandleRecord(T)──▶ persist.LogRecordHandler / LifecycleRecordHandler
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
3. `Spout.Stop` 内部会 `close(ch)`，因此**必须先确认没有任何 goroutine 还会调用 `Send`**，再调用 `Stop`；并发写入已关闭的通道会 panic（`send on closed channel`）。注意 `inlet.Close()` 只取消 `ctx`，它**不能**阻止已进入 `select` 的 `Send` 命中写入分支而 panic——`Close` 只保证「阻塞中的 `Send` 快速返回 `context.Canceled`」。**推荐顺序**：上游停止调用 `Send` → `inlet.Close()` → `spout.Stop()`。

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
- `Stop` 之后的 `Spout` 实例不可复用：`ch` 已 `close`，再 `Start` 只会启动一个立即因 `ok=false` 而退出的 goroutine，而再次 `Stop` 会因重复 `close` 已关闭的通道而 panic（`close of closed channel`）。
- `Handler()` 返回的引用是只读的，外部修改其内部状态是允许的（例如查询已写入条数），但替换整个 handler 不会生效。
