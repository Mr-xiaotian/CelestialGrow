# pkg/reporter/snapshot.go

> 📅 最后更新日期: 2026/11/26

`pkg/reporter/snapshot.go` 定义**图级状态快照**的对外契约与周期驱动器：`MetricsView`（数据来源）、`NodeStatus`（Web 端载荷）、`SnapshotSink`（写入目标）、`SnapshotTicker`（按拍投影）。它只做「指标 → 载荷」的纯投影与转发，**不执行任何网络 IO**。

## 作用

- 把 `scarecrow.PlotMetrics` 投影成与 celestialflow-web 对齐的 `NodeStatus`。
- 通过 `SnapshotSink` 把快照交给下游；生产装配中下游是 `PushWatcher`，再由其异步 POST 到 `/api/push_snapshot`。
- 作为 `metronome.Ticker`（仅 `OnTick` + `TickPeriod`）挂到节拍 Hub 上，无需 `Start` / `Stop`。

## 核心对象

### 常量

```go
const DefaultSnapshotPeriod = 1 // 每拍都推
```

### `type MetricsView`（接口）

```go
type MetricsView interface {
    GetPlots() map[string]scarecrow.PlotMetrics
}
```

| 约定 | 说明 |
|------|------|
| 键 | Plot 名称 |
| 值 | `scarecrow.PlotMetrics` 快照（状态、计数、起止时间、上下游计数） |
| 所有权限 | 调用方只读，不得修改返回的 map 或其中的计数字典 |
| 线程安全 | `GetPlots` 可能被节拍 goroutine 调用，实现需自行加锁 |

标准实现是 `*scarecrow.MetricsWatcher`：它在读锁内复制指标与计数字典，返回不可变快照，因此可以直接交给 `SnapshotTicker`。

### `type NodeStatus`

```go
type NodeStatus struct {
    Status           int            `json:"status"`
    TasksInput       int            `json:"tasks_input"`
    TasksProcessed   int            `json:"tasks_processed"`
    TasksPending     int            `json:"tasks_pending"`
    TasksSucceeded   int            `json:"tasks_succeeded"`
    TasksFailed      int            `json:"tasks_failed"`
    TasksSkipped     int            `json:"tasks_skipped"`
    UpstreamCounts   map[string]int `json:"upstream_counts"`
    DownstreamCounts map[string]int `json:"downstream_counts"`
    StartTime        float64        `json:"start_time"`
    EndTime          float64        `json:"end_time"`
}
```

字段名与语义必须与 celestialflow-web 的 `NodeStatus` 保持一致：

| Go 字段 | JSON 键 | 类型 | 语义 | 来源 |
|---------|---------|------|------|------|
| `Status` | `status` | `int` | `0` 未运行 / `1` 运行中 / `2` 已停止 | `int(PlotMetrics.Status)` |
| `TasksInput` | `tasks_input` | `int` | 输入任务总数（外部注入 + 上游投递） | `InputTotal` |
| `TasksProcessed` | `tasks_processed` | `int` | 已处理总数（成功 + 失败 + 跳过） | `Processed` |
| `TasksPending` | `tasks_pending` | `int` | 已输入但尚无结果 | `Pending` |
| `TasksSucceeded` | `tasks_succeeded` | `int` | 成功数 | `Succeeded` |
| `TasksFailed` | `tasks_failed` | `int` | 失败数 | `Failed` |
| `TasksSkipped` | `tasks_skipped` | `int` | 跳过数 | `Skipped` |
| `UpstreamCounts` | `upstream_counts` | `map[string]int` | 各上游 → 本节点的投递数 | `UpstreamCounts` |
| `DownstreamCounts` | `downstream_counts` | `map[string]int` | 本节点 → 各下游的产出数 | `DownstreamCounts` |
| `StartTime` | `start_time` | `float64` | 启动 Unix 浮点秒；未启动为 `0` | `StartTime`（零值→`0`） |
| `EndTime` | `end_time` | `float64` | 结束 Unix 浮点秒；未结束为 `0` | `EndTime`（零值→`0`） |

> **刻意不含 `elapsed_time`**：该值每拍都变，会让服务端把内容等价的快照误判为更新；前端改由 `start_time` / `end_time` 与浏览器时钟自行派生。

### `type SnapshotSink`（接口）

```go
type SnapshotSink interface {
    PushSnapshot(snapshot map[string]NodeStatus) error
}
```

- 生产实现是 `*PushWatcher`（入队后异步推送）。
- 接口只有写入语义，没有 `Close`；生命周期由 sink 自己管理。
- 返回的 error 会被 `SnapshotTicker.OnTick` **原样**上抛，不做包装。

### `type SnapshotTicker`

```go
type SnapshotTicker struct {
    metricsView MetricsView
    sink        SnapshotSink
    period      int
}
```

#### 构造：`NewSnapshotTicker`

```go
func NewSnapshotTicker(metricsView MetricsView, sink SnapshotSink, period int) (*SnapshotTicker, error)
```

校验顺序与错误：

| 顺序 | 条件 | 错误 |
|:----:|------|------|
| 1 | `metricsView == nil` | `reporter: metrics view is nil` |
| 2 | `sink == nil` | `reporter: snapshot sink is nil` |
| 3 | `period == 0` → 替换为 `1`；随后 `period < 1` | `reporter: snapshot period must be positive, got %d` |

- 接口判空只看「接口值是否为 nil」：把 `(*SomeView)(nil)` 这类**带类型的 nil 指针**传进来不会被拒绝，调用 `GetPlots` 时可能 panic。
- 负数周期不会退化为默认值，而是直接报错。

#### 方法

| 方法 | 签名 | 行为 |
|------|------|------|
| `TickPeriod` | `func (t *SnapshotTicker) TickPeriod() int` | 返回生效后的周期（已填充默认值） |
| `OnTick` | `func (t *SnapshotTicker) OnTick(event metronome.TickEvent) error` | 读一次指标、投影、交给 sink |

`OnTick` 细节：

1. `event` 完全未使用。
2. `toStatusSnapshot(t.metricsView.GetPlots())` 每次全量投影：节点数 N、计数条目数 E 时开销为 `O(N + E)`。
3. 直接 `return t.sink.PushSnapshot(snapshot)`，错误不包装、不重试。
4. 不缓存、不判重：即使指标毫无变化，也会每拍生成并转发一份等价快照。判重由服务端负责。

## 关键流程

```mermaid
flowchart LR
    MW[MetricsWatcher<br/>scarecrow] -->|GetPlots| MV[MetricsView]
    MV -->|map[string]PlotMetrics| T[SnapshotTicker.OnTick]
    T -->|toStatusSnapshot| NS[map[string]NodeStatus]
    NS -->|SnapshotSink.PushSnapshot| PW[PushWatcher]
    PW -->|Inlet.Send| Q[(异步队列)]
    Q -->|POST /api/push_snapshot| API[上报服务]
```

`SnapshotTicker` 与 `PushWatcher` 的职责切分：

- ticker 决定**何时**取数（节拍周期）；
- watcher 决定**如何**发送（会话标识、时间戳、JSON、重试策略=无）。

## 使用示例

### 直接消费快照（自定义 sink）

```go
package main

import (
    "fmt"

    "github.com/Mr-xiaotian/CelestialGrow/pkg/metronome"
    "github.com/Mr-xiaotian/CelestialGrow/pkg/reporter"
    "github.com/Mr-xiaotian/CelestialGrow/pkg/scarecrow"
)

// printSink 把快照打印到标准输出，演示 SnapshotSink 的最小实现。
type printSink struct{}

func (printSink) PushSnapshot(snapshot map[string]reporter.NodeStatus) error {
    for name, status := range snapshot {
        fmt.Printf("%s: status=%d input=%d pending=%d\n",
            name, status.Status, status.TasksInput, status.TasksPending)
    }
    return nil
}

func main() {
    metrics := scarecrow.NewMetricsWatcher() // 实现 MetricsView

    ticker, err := reporter.NewSnapshotTicker(metrics, printSink{}, 0) // 0 → 每拍
    if err != nil {
        panic(err)
    }
    fmt.Println("period:", ticker.TickPeriod())

    // 通常由 metronome 驱动；这里手动触发一次。
    if err := ticker.OnTick(metronome.TickEvent{Seq: 1}); err != nil {
        fmt.Println("snapshot failed:", err)
    }
}
```

### 注册到节拍 Hub（生产装配的等价写法）

```go
package main

import (
    "fmt"
    "time"

    "github.com/Mr-xiaotian/CelestialGrow/pkg/metronome"
    "github.com/Mr-xiaotian/CelestialGrow/pkg/reporter"
    "github.com/Mr-xiaotian/CelestialGrow/pkg/scarecrow"
)

func main() {
    // 指标源同时作为 MetricsView 传给快照 ticker 与推送观察者。
    metrics := scarecrow.NewMetricsWatcher()

    pushWatcher, err := reporter.NewPushWatcher("demo@1", "http://127.0.0.1:5005", metrics)
    if err != nil {
        panic(err)
    }
    ticker, err := reporter.NewSnapshotTicker(metrics, pushWatcher, 2) // 每 2 拍
    if err != nil {
        panic(err)
    }

    watcherHub := scarecrow.NewHub()
    if err := watcherHub.AddWatcher(pushWatcher); err != nil {
        panic(err)
    }
    tickerHub, err := metronome.NewHub()
    if err != nil {
        panic(err)
    }
    if err := tickerHub.AddTicker(ticker); err != nil { // SnapshotTicker 不是 Lifecycle
        panic(err)
    }
    clock, err := metronome.NewClock(time.Second, tickerHub)
    if err != nil {
        panic(err)
    }

    // 先启动 watcher（PushWatcher.Start），再启动节拍。
    if err := watcherHub.Start(); err != nil {
        panic(err)
    }
    if err := tickerHub.Start(); err != nil {
        panic(err)
    }
    if err := clock.Start(); err != nil {
        panic(err)
    }

    time.Sleep(2500 * time.Millisecond) // 供观察两拍

    _ = clock.Stop()
    if err := tickerHub.Stop(); err != nil {
        fmt.Println(err)
    }
    if err := watcherHub.Stop(); err != nil { // 排空推送队列并断开会话
        fmt.Println(err)
    }
}
```

`SnapshotTicker` 没有 `Start` / `Stop`，`metronome.Hub` 只会把它当作纯 ticker 参与节拍分发。它的 sink 是否已启动由装配方保证——`pkg/assembly` 先 `watcherHub.Start()`（内部启动 `PushWatcher`），再 `tickerHub.Start()` 与 `clock.Start()`。

## 注意事项

- **投影不做深拷贝**：`toStatusSnapshot` 直接引用 `PlotMetrics.UpstreamCounts` / `DownstreamCounts`。对 `scarecrow.MetricsWatcher` 安全（它每次返回新 map），但自定义 `MetricsView` 若复用字典，sink 必须在保留数据前自行复制——`PushWatcher.pushSnapshotAt` 已通过 `copySnapshot` 做到这一点。
- **时间零值语义**：`StartTime` / `EndTime` 的 `time.Time` 零值被投影为 `0`，而不是负的 Unix 时间戳；前端据此判断「未启动/未结束」。
- **状态是整数快照**：`PlotStatus` 的 `String()`（`not_started`/`running`/`stopped`）不会出现在载荷里，Web 端依赖 `0/1/2` 数值。
- **错误不包装**：`OnTick` 返回的就是 sink 的 error，`errors.Is` 可直接命中；若上层需要附加上下文需自行包装。
- **无本地判重**：等价快照每拍都会进入队列，服务端靠内容判重（因此 `elapsed_time` 被刻意排除）。高频周期 + 大图会放大队列压力，`DefaultPushBufferSize = 64` 是唯一的缓冲上限。
- **不校验节点集合**：空图会推送空的 `snapshot: {}`；从未启动的 Plot 也可能因收到过种子/上游事件而出现在 `MetricsWatcher` 中（状态为 `0`）。
- **`GetPlots` 的耗时在节拍 goroutine 中**：持有指标读锁的时间与节点数成正比；若指标写入频繁（大量种子事件），会与快照读取竞争同一把 `RWMutex`。
