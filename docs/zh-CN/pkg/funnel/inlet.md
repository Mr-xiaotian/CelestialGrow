# pkg/funnel/inlet.go

> 📅 最后更新日期: 2026/11/13

`pkg/funnel/inlet.go` 定义了 `Inlet[T]` 泛型抽象——CelestialGrow 异步消费基础设施中的**生产端**（写入者）。它把上游组件的记录送入一个带缓冲的 Go channel，由对端的 `Spout[T]` 异步消费。`Inlet` 内置了独立的 `context.Context`，可被 `Close` 主动取消；每次发送还带超时保护，避免上游被慢消费者无限阻塞。

> **命名提示**：尽管 `Inlet` 直观上像"入口"，但在本包中它是**写入端 / 生产端**；真正的"消费循环入口"是 `Spout`。`Inlet` 把记录"灌入"通道，`Spout` 从通道"喷出"记录并交给 `RecordHandler` 处理。

## 作用

- 提供带**超时**与**上下文取消**的安全 `Send`，避免无界阻塞。
- 通过 `chan<- T` 显式声明"只写"语义，把通道所有权交给对端的 `Spout`。
- 作为 `persist` 层观察者的写入通道：`persist.LogWatcher` / `persist.LifecycleWatcher` 各自持有一个 `*funnel.Inlet[T]`，`OnEvent` 只负责把事件翻译成记录后投递。

## 核心对象

### `type Inlet[T any]`

```go
type Inlet[T any] struct {
    ch      chan<- T        // 只写通道，由对端 Spout 持有
    timeout time.Duration   // 单次 Send 的最大等待时间
    ctx     context.Context // 内部 context，Close 时被取消
    cancel  context.CancelFunc
}
```

- **泛型参数 `T`**：单条记录的类型。例如 `persist.LogWatcher` 使用 `T = persist.LogRecord`。
- **不导出**任何字段，外部只能通过 `NewInlet` 构造。

### 构造：`NewInlet[T any]`

```go
func NewInlet[T any](ch chan<- T, timeout time.Duration) *Inlet[T]
```

- `ch`：必须是从 `Spout.GetQueue()` 取到的"只写"句柄（Go 会自动把双向 `chan T` 转成 `chan<- T`）。
- `timeout`：单次 `Send` 的最大等待时长。`0` **不是**「不设超时」——`time.After(0)` 的通道立即可读，写入分支未就绪时 `Send` 会立刻返回超时错误，请传入正数时长。
- 内部以 `context.Background()` 为根创建独立 `ctx`，因此一个 `Inlet` 的取消不会影响其他 `Inlet`。

### 公开方法

| 方法 | 签名 | 作用 |
|------|------|------|
| `Send` | `func (s *Inlet[T]) Send(record T) error` | 写入一条记录；遇上下文取消或超时返回错误 |
| `Close` | `func (s *Inlet[T]) Close()` | 取消内部 `ctx`，让阻塞中的 `Send` 立即返回错误 |

#### `Send(record T) error`

行为是 `select` 三个分支竞争：

1. `s.ch <- record`：成功写入，返回 `nil`。
2. `<-s.ctx.Done()`：返回 `s.ctx.Err()`（正常为 `context.Canceled`）。
3. `<-time.After(s.timeout)`：返回 `fmt.Errorf("inlet send timeout after %v", s.timeout)`，例如 `inlet send timeout after 1s`。

> **背压策略**：当通道已满且对端 `Spout` 暂未消费时，`Send` 会在 `timeout` 内阻塞等待；超时即视为上游压力过大，建议调用方记录指标或触发降级。
>
> **注意 `select` 的随机性**：当写入分支与 `ctx.Done()` 同时就绪（通道仍有空位且已被 `Close`）时，Go 会在就绪分支中随机选择，因此 `Close` 之后的 `Send` **仍可能成功写入**并返回 `nil`。需要「一定不再发送」的语义时，应由调用方自行停止调用 `Send`。

#### `Close()`

- 调用 `s.cancel()` 取消内部 `ctx`，**不**直接关闭通道——通道的所有权在 `Spout` 侧（`Spout.Stop` 负责 `close(ch)`）。
- 可被重复调用（`context.CancelFunc` 幂等）。

## 并发语义

- `Send` 只做「向 channel 写入」这一原子操作，**可被多个 goroutine 并发调用**，无需外部加锁。`persist` 的观察者正是依赖这一点：`Hub` 可能从多个 Plot 协程同步调用 `OnEvent`，进而并发落到同一个 `Inlet`。
- `Close` 与 `Send` 并发调用不会产生数据竞争（`cancel` 是并发安全的），但如上文所述，`Close` 不保证「之后再无写入成功」。
- `Inlet` 本身不负责同步 handler：并发写入的多个记录会进入同一个队列，最终由 `Spout` 的**单个**消费 goroutine 串行调用 `HandleRecord`。

## 与 persist / assembly 的调用关系

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

关键点：

- `persist.LogWatcher` / `persist.LifecycleWatcher` **持有**（而非内嵌）`*funnel.Inlet[T]`，在未导出的 `newLogWatcher` / `newLifecycleWatcher` 中由 `funnel.NewInlet(spout.GetQueue(), time.Second)` 构造。
- 两个观察者的队列参数一致：`funnel.NewSpout(handler, 100, time.Second)` + `funnel.NewInlet(..., time.Second)`，即**缓冲 100 条**，`Send` 超时与 `Spout.Stop` 的优雅关闭超时均为 **1 秒**。
- `LogWatcher` 在 `OnEvent` 与 `Send` 之间还有一层级别过滤：私有方法 `log(level, message, at)` 会丢弃低于 `minLevel` 的记录，这些记录**不会**占用通道。
- `Send` 的错误会沿 `OnEvent` 原样返回（`LogWatcher.OnEvent` / `LifecycleWatcher.OnEvent` 直接把 `Send` 的 error 作为返回值），但 `Hub` 会捕获观察者错误交给 `ErrorHandler` 并始终返回 `nil`，所以超时不会中断业务执行。
- `Plot` / `Farm` **不再**直接持有 `Inlet` 或 `Spout`：它们只持有 `scarecrow.Hub`，观察者由 `pkg/assembly` 的 `StartPlotResources` / `StartFarmResources` 创建并启动，运行结束时由 `Resources.Stop`（内部 `sync.Once`）先停日志、再停生命周期。

## 使用示例

最小化的 `Inlet` + `Spout` 组合，演示一个整数计数器的异步落盘流程：

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

> 顺序很重要：`Spout.Stop()` 会关闭通道，必须确保此时不再有任何 `Send`。`inlet.Close()` 只能让**阻塞中**的 `Send` 提前返回，不能阻止已进入 `select` 的 `Send` 命中写入分支。

## 注意事项

- `Inlet` 内部 `ctx` 仅在 `Close` 时被取消；`Send` 的超时与 `ctx` 是**并列**关系，调用方需要同时处理两种错误（`context.Canceled` 与 `inlet send timeout after ...`）。
- `Send` 的错误**不重试**。重试策略应在上层实现，避免在 `Inlet` 内引入隐性循环。
- 通道的所有权在 `Spout`（`Spout.Stop` 内部负责 `close(ch)`），`Inlet` 永远不要 `close(ch)`：重复关闭会 panic，且此后任何 `Send` 命中写入分支时同样会 panic（`send on closed channel`）。
- `Inlet` 零值不可用：`ctx` / `cancel` 为 `nil` 时 `Send` 会 panic。必须通过 `funnel.NewInlet` 构造；持有它的类型（如 `persist.LogWatcher`）使用**指针**字段，也应视为只能在构造函数内初始化的成员。
- `timeout=0` 时 `time.After(0)` 立即可读，`Send` 几乎总是走超时分支（`ctx` 已取消时两分支随机）——生产环境务必设置一个合理上限。
