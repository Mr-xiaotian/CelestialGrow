# pkg/funnel/inlet.go

> 最后更新日期: 2026/09/01

`pkg/funnel/inlet.go` 定义了 `Inlet[T]` 泛型抽象——CelestialGrow 异步消费基础设施中的**生产端**（写入者）。它把上游组件（`Plot`、`Farm`）产出的记录送入一个带缓冲的 Go channel，由对端的 `Spout[T]` 异步消费。`Inlet` 内置了独立的 `context.Context`，可被 `Close` 主动取消，同时每次发送都带超时保护，避免上游被慢消费者无限阻塞。

> **命名提示**：尽管 `Inlet` 直观上像"入口"，但在本包中它是**写入端 / 生产端**；真正的"消费循环入口"是 `Spout`。`Inlet` 把记录"灌入"通道，`Spout` 从通道"喷出"记录并交给 `RecordHandler` 处理。

## 作用

- 提供带**超时**与**上下文取消**的安全 `Send`，避免无界阻塞。
- 通过 `chan<- T` 显式声明"只写"语义，把通道所有权交给对端的 `Spout`。
- 为上层（`persist.LogInlet`、`persist.LifecycleInlet`）提供可嵌入的"生产者基类"。

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

- **泛型参数 `T`**：单条记录的类型。例如 `persist.LogInlet` 使用 `T = persist.LogRecord`。
- **不导出**任何字段，外部只能通过 `NewInlet` 构造。

### 构造：`NewInlet[T any]`

```go
func NewInlet[T any](ch chan<- T, timeout time.Duration) *Inlet[T]
```

- `ch`：必须是从 `Spout.GetQueue()` 取到的"只写"句柄（Go 会自动把双向 `chan T` 转成 `chan<- T`）。
- `timeout`：单次 `Send` 的最大等待时长。`0` 表示不设超时（`time.After(0)` 立即可读，仅受 `ctx` 与通道容量影响）。
- 内部以 `context.Background()` 为根创建独立 `ctx`，因此一个 `Inlet` 的取消不会影响其他 `Inlet`。

### 公开方法

| 方法 | 签名 | 作用 |
|------|------|------|
| `Send` | `func (s *Inlet[T]) Send(record T) error` | 写入一条记录；遇上下文取消或超时返回错误 |
| `Close` | `func (s *Inlet[T]) Close()` | 取消内部 `ctx`，让所有阻塞中的 `Send` 立即返回 |

#### `Send(record T) error`

行为是 `select` 三个分支竞争：

1. `s.ch <- record`：成功写入，返回 `nil`。
2. `<-s.ctx.Done()`：返回 `s.ctx.Err()`（`context.Canceled`）。
3. `<-time.After(s.timeout)`：返回 `fmt.Errorf("inlet send timeout after %v", s.timeout)`。

> **背压策略**：当通道已满且对端 `Spout` 暂未消费时，`Send` 会在 `timeout` 内阻塞等待；超时即视为上游压力过大，建议调用方记录指标或触发降级。`Close` 会让所有阻塞中的 `Send` 立即通过第 2 个分支返回 `context.Canceled`。

#### `Close()`

- 调用 `s.cancel()` 取消内部 `ctx`，**不**直接关闭通道——通道的所有权在 `Spout` 侧（`Spout.Stop` 负责 `close(ch)`）。
- 可被重复调用，幂等。

## 与 Plot / persist 的对接点

```text
Plot 内部调用
   │ logInlet.SeedRipen(...)        // 业务侧
   │ lifecycleInlet.SeedSuccess(...)
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

关键点：

- `persist.LogInlet` **内嵌** `funnel.Inlet[LogRecord]`，对外暴露 `StartFarm` / `EndFarm` / `SeedRipen` 等语义化方法；其 `log` 私有方法内部仍走 `Inlet.Send`。低于 `minLevel` 的日志在 `log` 入口被丢弃，**不会**占用通道。
- `persist.LifecycleInlet` 同样内嵌 `Inlet[LifecycleRecord]`，并提供 `SeedIn` / `SeedSuccess` / `SeedFailed` 等生命周期埋点。
- `Plot.BindInlet` 由 `Farm.Run` 统一调用，传入 `f.logSpout.GetQueue()` 与 `f.lifecycleSpout.GetQueue()`，从而把"生产端"和"消费端"绑定到同一个通道上。
- standalone 模式下，`Plot` 自己持有 `Spout` 并通过 `StartSpouts` / `StopSpouts` 控制启停，通道同样来自 `Spout.GetQueue()`。

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

## 注意事项

- `Inlet` 内部 `ctx` 仅在 `Close` 时被取消；`Send` 的超时与 `ctx` 是**并列**关系，调用方需要同时处理两种错误。
- `Send` 的错误**不重试**。重试策略应在上层（如 `Plot` 的 `maxRetries`）实现，避免在 `Inlet` 内引入隐性循环。
- 通道的所有权在 `Spout`，`Inlet` 永远不要 `close(ch)`；否则 `Spout` 的 range/select 会出现 `send on closed channel`。
- 嵌入 `Inlet` 的子类型（如 `LogInlet`）应保留 `*Inlet[T]` 的零值可用语义：构造时立即调用 `funnel.NewInlet` 初始化，避免空指针。
- `timeout=0` 时 `time.After(0)` 立即触发，"超时"会先于 `ctx` 取消生效——生产环境务必设置一个合理上限。
