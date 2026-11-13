# pkg/scarecrow/metrics.go

> 📅 最后更新日期: 2026/11/13

## 作用

`pkg/scarecrow/metrics.go` 从事件流**重建每个 Plot 的运行指标**：状态、起止时间、输入来源、成熟/枯萎/剪枝计数，以及待完成数量。`MetricsWatcher` 是一个纯内存累加器（无子协程、无 I/O），注册到 `Hub` 后即为 `Plot` 的 `GetSeedNum` / `GetFruitNum` / `IsFinish` 等查询提供数据来源，也是 `pkg/persist` 日志观察者补全计数的依据。

## 核心对象

### `PlotStatus`

```go
type PlotStatus int

const (
    PlotNotStarted PlotStatus = iota // 尚未收到 PlotStartEvent
    PlotRunning                      // 正在运行
    PlotStopped                      // 已结束
)
```

| 常量 | 值 | `String()` 输出 |
|------|---|-----------------|
| `PlotNotStarted` | 0 | `not_started` |
| `PlotRunning` | 1 | `running` |
| `PlotStopped` | 2 | `stopped` |
| 其他值 | — | `not_started`（`String()` 的 `default` 分支） |

> `PlotStatus` 与 `plot.GetState()` 的 `0=idle / 1=running / 2=done` 数值一致，但它们是两套独立类型，不要互相赋值。

### `PlotMetrics`

```go
type PlotMetrics struct {
    Plot   string
    Status PlotStatus

    StartTime time.Time
    EndTime   time.Time

    ExternalInput int
    UpstreamInput int
    InputTotal    int

    Succeeded int
    Failed    int
    Skipped   int
    Processed int
    Pending   int

    UpstreamCounts   map[string]int
    DownstreamCounts map[string]int
}
```

| 字段 | 含义 | 计算方式 |
|------|------|----------|
| `Plot` | Plot 名称 | 快照键名 |
| `Status` | 运行状态 | 由 `PlotStartEvent` / `PlotEndEvent` 更新，初值 `PlotNotStarted` |
| `StartTime` / `EndTime` | 起止时间 | 直接取事件的 `StartTime` / `EndTime`；未发生的保持零值 |
| `ExternalInput` | 外部注入种子数 | `SeedInputEvent.From == ""` 时 +1 |
| `UpstreamInput` | 上游投递种子数 | `UpstreamCounts` 所有值之和 |
| `InputTotal` | 输入总数 | `ExternalInput + UpstreamInput` |
| `Succeeded` | 成熟数 | 每条 `SeedRipenEvent` +1 |
| `Failed` | 枯萎数 | 每条 `SeedWitherEvent` +1 |
| `Skipped` | 剪枝数 | 每条 `SeedPruneEvent` +1 |
| `Processed` | 已处理数 | `Succeeded + Failed + Skipped` |
| `Pending` | 已输入但未出结果 | `InputTotal - Processed`，**小于 0 时取 0** |
| `UpstreamCounts` | 来源 Plot → 投递数量 | 每次上游投递在接收方累加 |
| `DownstreamCounts` | 目标 Plot → 产出数量 | 每次上游投递在**来源方**累加 |

`PlotMetrics` 是**值快照**：`UpstreamCounts` / `DownstreamCounts` 在每次 `GetPlot` / `GetPlots` 时重新分配并拷贝，调用方修改不会影响 `MetricsWatcher` 内部状态，也不存在数据竞争。

### `MetricsWatcher`

```go
type MetricsWatcher struct {
    mu    sync.RWMutex
    plots map[string]*plotMetrics
}

func NewMetricsWatcher() *MetricsWatcher
```

| 成员 | 说明 |
|------|------|
| `mu` | 读写锁，保护 `plots` 与所有 `plotMetrics`；`OnEvent` 持写锁，读取接口持读锁 |
| `plots` | Plot 名称 → 内部可变存储格；随首个相关事件按需创建（幂等） |
| `plot(name)`（未导出） | 按需建格；`plots` 为 `nil` 时先初始化，因此**零值 `MetricsWatcher` 也可用** |
| `snapshot(name)`（未导出） | 在调用方持锁的前提下复制映射并计算派生字段 |

内部存储格 `plotMetrics` 与快照的区别：存储格使用私有字段（`external` / `success` / `fail` / `skip` / `upstream` / `downstream` / `status` / `start` / `end`），只暴露快照结构体。

### 读取接口

| 方法 | 签名 | 行为 |
|------|------|------|
| `GetPlot` | `func (m *MetricsWatcher) GetPlot(name string) (PlotMetrics, bool)` | 未登记时返回零值与 `false`；已登记返回拷贝快照 |
| `GetPlots` | `func (m *MetricsWatcher) GetPlots() map[string]PlotMetrics` | 返回全部已登记 Plot 的新映射；无数据时返回空映射（非 nil） |

## 关键事件映射

| 事件 | 对指标的影响 |
|------|--------------|
| `PlotStartEvent` | `status = PlotRunning`，`start = ev.StartTime` |
| `PlotEndEvent` | `status = PlotStopped`，`end = ev.EndTime` |
| `SeedInputEvent`（`From == ""`） | `external++` |
| `SeedInputEvent`（`From != ""`） | 接收方 `upstream[From]++`；同时 `plot(From).downstream[Plot]++` |
| `SeedRipenEvent` | `success++` |
| `SeedWitherEvent` | `fail++` |
| `SeedPruneEvent` | `skip++` |
| `FarmStartEvent` / `FarmEndEvent` / `SeedReplantEvent` / `SealInputEvent` / `SealMergeEvent` | 忽略，不影响 Plot 指标 |

```mermaid
flowchart LR
    PS[PlotStartEvent] --> C[plotMetrics 存储格]
    PE[PlotEndEvent] --> C
    SI[SeedInputEvent] -->|From 为空| EXT[external++]
    SI -->|From 非空| UP[接收方 upstream[From]++<br/>来源方 downstream[Plot]++]
    SR[SeedRipenEvent] --> SUC[success++]
    SW[SeedWitherEvent] --> FAIL[fail++]
    SP[SeedPruneEvent] --> SKIP[skip++]
    C --> SNAP[snapshot<br/>InputTotal/Processed/Pending]
    EXT --> SNAP
    UP --> SNAP
    SUC --> SNAP
    FAIL --> SNAP
    SKIP --> SNAP
```

## 重要细节

- **格随事件建立**：任何带 `Plot` 名称的事件都会创建对应格；`SeedInputEvent` 还会为 `From` 创建来源方格。因此 `GetPlots()` 可能包含「从未启动、只在血缘计数中出现」的 Plot，其 `Status` 为 `PlotNotStarted`、`InputTotal` 为 0。
- **状态不参与派生**：即使 `Status == PlotStopped`，后续到达的种子事件仍会继续累加；`MetricsWatcher` 不做「结束后丢弃」的判断。
- **`Pending` 有下界 0**：事件流不完整（例如只回放结果、没有输入）时 `Processed > InputTotal`，差值被钳到 0，不出现负数。
- **重试不计数**：`SeedReplantEvent` 被忽略，`Succeeded + Failed + Skipped` 仍与输入一一对应，`Pending` 不会被重试次数污染。
- **并发安全**：`OnEvent` 全程持写锁，`GetPlot` / `GetPlots` 持读锁；多个 Plot 的并发事件因此被串行化，读与写共用同一把 `RWMutex`。
- **快照隔离**：`GetPlot` / `GetPlots` 返回的新映射是深拷贝的一层（值拷贝 + 映射重建），调用方可安全地长期持有并在其他协程读取。
- **指标不含 `Elapsed`**：耗时不进入 `PlotMetrics`，需要时用 `EndTime.Sub(StartTime)` 自行计算，或直接消费 `PlotEndEvent.Elapsed`。

## 使用示例

### 直接喂事件

```go
package main

import (
    "fmt"
    "time"

    "github.com/Mr-xiaotian/CelestialGrow/pkg/scarecrow"
)

func main() {
    metrics := scarecrow.NewMetricsWatcher()
    now := time.Now()

    _ = metrics.OnEvent(scarecrow.PlotStartEvent{Plot: "harvester", Tenders: 2, StartTime: now})
    _ = metrics.OnEvent(scarecrow.SeedInputEvent{Plot: "harvester", SeedID: 1, Time: now})
    _ = metrics.OnEvent(scarecrow.SeedRipenEvent{Plot: "harvester", SeedID: 1, FruitID: 2, Time: now})
    _ = metrics.OnEvent(scarecrow.SeedWitherEvent{Plot: "harvester", SeedID: 3, WeedID: 4, Time: now})

    snapshot, ok := metrics.GetPlot("harvester")
    if !ok {
        panic("plot not registered")
    }
    fmt.Println(snapshot.Status)                       // running
    fmt.Println(snapshot.InputTotal, snapshot.Pending) // 2 0
    fmt.Println(snapshot.Succeeded, snapshot.Failed)   // 1 1
}
```

### 经 Hub 注册后检索

```go
package main

import (
    "fmt"

    "github.com/Mr-xiaotian/CelestialGrow/pkg/scarecrow"
)

func main() {
    hub := scarecrow.NewHub()
    _ = hub.AddWatcher(scarecrow.NewMetricsWatcher())

    _ = hub.OnEvent(scarecrow.SeedInputEvent{Plot: "a", SeedID: 1})
    _ = hub.OnEvent(scarecrow.SeedInputEvent{Plot: "b", From: "a", SeedID: 2, Parents: []int{1}})

    metrics, ok := scarecrow.Find[*scarecrow.MetricsWatcher](hub)
    if !ok {
        panic("metrics watcher not found")
    }
    plots := metrics.GetPlots()
    fmt.Println(plots["a"].DownstreamCounts["b"]) // 1
    fmt.Println(plots["b"].UpstreamCounts["a"])   // 1
    fmt.Println(plots["b"].UpstreamInput)         // 1
}
```

`hub` 可以是 Plot 的 Hub，也可以是嵌套了 Farm Hub 的层级——`Find` 会先查直属、再查嵌套，`basePlot.currentMetrics` 正是这种用法。

## 注意事项

- `MetricsWatcher` 只反映**它实际收到的事件**；未注册到某个 Plot/Farm 的 Hub，就看不到那些事件。
- 同一 `MetricsWatcher` 不要重复注册到同一个 Hub：事件会被处理两次，计数翻倍。
- `Status` 只由 Plot 生命周期事件驱动；手工回放事件时若不发 `PlotStartEvent`，`Status` 会一直是 `PlotNotStarted`。
- 读取接口返回的是快照，不是实时视图；连续两次 `GetPlot` 之间的新事件不会体现在前一次结果中。
- 如果希望 `persist.LogWatcher` 在 `PlotEndEvent` 时打印准确的成熟/枯萎/剪枝数，`MetricsWatcher` 必须比它**更早**注册（`pkg/assembly` 已按此顺序装配）。
- 需要跨进程或落盘的分析能力请使用 `pkg/persist` 的生命周期与日志观察者，它们复用了本文件的计数语义。
