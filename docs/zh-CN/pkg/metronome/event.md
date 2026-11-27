# pkg/metronome/event.go

> 📅 最后更新日期: 2026/11/26

`pkg/metronome/event.go` 只定义了一个值对象 `TickEvent`——节拍系统在每次触发时向所有 `Ticker` 传递的唯一载荷。它把「第几拍」与「什么时候真正触发」打包在一起，让接收方既能做节流/采样判断，也能做时间敏感的测量。

## 作用

- 作为 `Ticker.OnTick(event TickEvent)` 的唯一参数，贯穿 `Clock → Hub → 子 Hub → 叶子 Ticker` 整条分发链。
- 携带**单调递增的序号** `Seq`，供 `Periodic` 过滤（`(Seq-1)%period == 0`）和业务侧的"每 N 拍执行一次"逻辑使用。
- 携带**实际触发时刻** `FiredAt`，供需要计算延迟、采样周期或对齐日志时间戳的 ticker 使用。

## 核心对象

### `type TickEvent struct`

```go
type TickEvent struct {
    Seq     int       // 节拍序号，从 1 开始
    FiredAt time.Time // 实际触发时间
}
```

| 字段 | 类型 | 语义 |
|------|------|------|
| `Seq` | `int` | 节拍序号，**从 1 开始**单调递增。它是"时钟第几次真正触发"的计数，而不是"第几次被投递到某个 ticker"的计数。 |
| `FiredAt` | `time.Time` | 时钟读取 `time.Now()` 得到的实际触发时刻，带有**单调时钟分量**。 |

该结构体没有方法，也不持有任何锁，可自由复制、跨 goroutine 传递。

## 重要细节

### `Seq` 的语义边界

- **起点是 1**：`Clock.run` 里 `seq := 0`，每次真正落到网格触发点上先 `seq++`，因此首个事件是 `Seq: 1`。测试 `TestClockTicksRoot` 直接断言收到 `[]int{1, 2, 3}`。
- **只在 Clock 层生成**：`Hub.OnTick` 原样透传上游事件的 `Seq`，不会重编号。因此在一个嵌套 Hub 树中，所有 ticker 看到的 `Seq` 属于同一个时钟序列。
- **被周期过滤的拍仍占用序号**：某个 `TickPeriod() = 2` 的 ticker 只会收到 `Seq = 1, 3, 5, ...`，但 `2, 4` 这些序号确实被时钟消耗掉了。这与 `TestClockRespectsRootTickPeriod` / `TestHubFiltersByTickerPeriod` 的期望一致。
- **重启会归零**：`Clock` 被 `Stop` 后再次 `Start`，`run` 重新从 `seq = 0` 开始，新序列首个事件仍是 `Seq: 1`。不要把它当作跨重启的全局唯一 ID。
- **类型是 `int`**：极端长时间运行（每毫秒一拍约需 29 万年）才会触及溢出，实践中无需担心，但若把 `Seq` 持久化成"全局唯一序号"则要注意这一点。

### `FiredAt` 与单调时钟

`FiredAt` 来自 `time.Now()`，Go 运行时会为它附带单调时钟读数。`Clock` 内部用 `time.Until` / `Before` / `Sub` 做调度运算，这些操作优先使用单调分量，因此：

- 系统墙钟（NTP 校时、手工改时间、时区切换）**不会**导致节拍加速、倒退或停摆；
- 但把 `FiredAt` 格式化成字符串时看到的是墙钟时间，可能与内部调度用的单调读数存在偏差。

`FiredAt` 是**实际触发时间**而非计划时间：如果上一拍处理耗时较长，当前事件仍记录真实的 `time.Now()`，会明显晚于网格点（详见 [`clock.md`](clock.md) 的锚定与跳拍一节）。

## 使用示例

### 在 ticker 中按序号节流

```go
package main

import (
    "fmt"
    "time"

    "github.com/Mr-xiaotian/CelestialGrow/pkg/metronome"
)

// EveryFiveTicks 只在第 1、6、11… 拍执行。
type EveryFiveTicks struct{}

func (EveryFiveTicks) OnTick(event metronome.TickEvent) error {
    if event.Seq%5 != 1 {
        return nil
    }
    fmt.Printf("第 %d 拍，于 %s\n", event.Seq, event.FiredAt.Format(time.RFC3339))
    return nil
}
```

> 更推荐的做法是实现 `metronome.Periodic`（返回 `TickPeriod() = 5`），由 `Hub` 在分发前统一过滤，避免每个 ticker 重复写取模逻辑；两者语义等价（`Periodic` 使用的是 `(Seq-1)%period == 0`）。

### 用 `FiredAt` 观测调度延迟

```go
type LagProbe struct{ last time.Time }

func (p *LagProbe) OnTick(event metronome.TickEvent) error {
    if !p.last.IsZero() {
        fmt.Println("相邻两拍实际间隔:", event.FiredAt.Sub(p.last))
    }
    p.last = event.FiredAt
    return nil
}
```

因为 `Clock` 采用"跳过错拍、不追赶"策略，长耗时处理之后这个间隔会大于名义周期，属于预期行为。

## 注意事项

- **不要改写事件**：`TickEvent` 以值传递，ticker 对参数的修改不会影响其他接收者；也不需要加锁。
- **不要依赖 `Seq` 做业务主键**：它随 `Clock` 重启归零，且跳过的节拍不会补号。
- **`FiredAt` 可能晚于预期**：它记录真实触发时间，调度滞后时不会伪造计划时间。
- **`FiredAt` 的零值无意义**：手工构造 `TickEvent{Seq: 1}` 时（如单元测试直接调用 `Hub.OnTick`）`FiredAt` 为零值，仅依赖 `Seq` 的逻辑不受影响。
