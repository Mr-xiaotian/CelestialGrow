# pkg/farm/farm.go

> 📅 最后更新日期: 2026/11/13

## 作用

`farm` 包是 CelestialGrow 的「图级」调度器，`farm.go` 提供 `Farm` 类型，负责把多个 `plot.PlotNode` 节点组织成一张静态有向图，并完成：

- 节点注册与名称唯一性校验
- 组到组（hyper-edge）式的全连接建立
- Farm 级观察者注册，并通过**层级 Hub** 同时接收 Farm 事件与所有 Plot 事件
- 运行期资源装配（`assembly.StartFarmResources` 统一创建/启动/回收日志与生命周期观察者）
- 运行前渲染图结构（`getStructureList` → `RenderStructureList`）写入 `FarmStartEvent`
- 源节点探测（`SourceNodes`）、初始种子注入、终止信号注入与等待退出

`Farm` 只管理编排层，不关心 Plot 的泛型种子 / 果实类型——这一点由 `plot.PlotNode` 接口抹除。图算法本身位于同目录的 `graph.go`，结构渲染位于 `render.go`，详见 [`graph.md`](./graph.md) 与 [`render.md`](./render.md)。

## 核心对象

### `Farm` 结构体

```go
type Farm struct {
    name        string
    plots       map[string]plot.PlotNode
    sourceNodes []string
    *OrderGraph

    eventClient runtime.EventClient
    hub         *scarecrow.Hub
}
```

| 字段 | 作用 |
| --- | --- |
| `name` | Farm 名称，写入 `FarmStartEvent` / `FarmEndEvent` 与日志 |
| `plots` | 按 plot 名称索引的节点表，用于注册校验与查找 |
| `sourceNodes` | `Run` / `Start` 开始时一次性计算的「源节点代表」，每个 Source SCC 取一个 |
| `*OrderGraph` | 嵌入的有向图，记录 plot 间的连边，用于拓扑与 SCC 分析 |
| `eventClient` | 共享的运行时事件客户端，通过 `AddPlot` 注入到每个 plot |
| `hub` | Farm 级观察者 Hub；也是注册到每个 Plot 的嵌套 Hub |

> `Farm` 不再持有 spout / inlet / 持久化观察者。所有运行期资源由 `pkg/assembly` 装配，Farm 只暴露一个 Hub。

### `PlotNode` 接口契约

`Farm` 通过 `plot.PlotNode` 接口与具体节点解耦。该接口定义在 `pkg/plot/plot.go`，`Farm` 实际调用以下方法：

| 方法 | 用途 |
| --- | --- |
| `GetName() string` | 唯一标识与连边匹配 |
| `GetState() int32` | 读取节点状态（`0=idle` / `1=running` / `2=done`） |
| `GetSeedChanAny() any` | 以 `any` 暴露 `seedChan`，供上游 `ConnectTo` 做类型断言 |
| `ConnectTo(next PlotNode) error` | 校验类型兼容、登记下游产出通道与上游名；类型不匹配时返回 error |
| `AddUpstream(name string)` | 由上游 `ConnectTo` 调用，登记上游名用于 seal 聚合 |
| `AddWatcher(listener scarecrow.Watcher) error` | 注册观察者；Farm 在启动时把 Farm Hub 注册进来 |
| `SetEventClient(runtime.EventClient)` | `AddPlot` 时统一注入同一事件客户端 |
| `StartAsync()` / `WaitAsync()` | 异步生命周期控制 |
| `SeedAny(seed any) error` | 注入初始种子（`InjectTasks` 逐颗调用） |
| `Seal()` | 注入外部终止信号（`InjectTerminations` 调用） |

## 公开符号

| 符号 | 签名 | 用途 |
| --- | --- | --- |
| `NewFarm` | `func NewFarm(name string) *Farm` | 构造 Farm：初始化 plot 表、空图、本地事件客户端与空 Hub |
| `PlotCount` | `func (f *Farm) PlotCount() int` | 返回已注册 plot 数量 |
| `HasPlot` | `func (f *Farm) HasPlot(name string) bool` | 判断指定 plot 是否已注册 |
| `GetPlot` | `func (f *Farm) GetPlot(name string) (plot.PlotNode, bool)` | 按名称取 plot，`ok` 表示是否存在 |
| `AddPlot` | `func (f *Farm) AddPlot(plots ...plot.PlotNode) error` | 注册一个或多个 plot，要求非 nil、名称非空且唯一 |
| `Connect` | `func (f *Farm) Connect(fromPlots, toPlots []plot.PlotNode) error` | 在源组与目标组之间建立笛卡尔积式连接 |
| `AddWatcher` | `func (f *Farm) AddWatcher(watcher scarecrow.Watcher) error` | 注册 Farm 级观察者（同时接收 Farm 与 Plot 事件） |
| `Run` | `func (f *Farm) Run(inputs map[string][]any, logLevel string) error` | 同步运行：启动 → 注入种子 → 终止 source → 等待退出 |
| `Start` | `func (f *Farm) Start(logLevel string) error` | 同步运行但不做任何注入，由外部驱动 |
| `InjectTasks` | `func (f *Farm) InjectTasks(tasks map[string][]any) error` | 向指定 Plot 注入种子，可在运行期调用 |
| `InjectTerminations` | `func (f *Farm) InjectTerminations(nodes []string) error` | 向指定 Plot 注入终止信号，可在运行期调用 |

### `Run` / `Start` / `Inject*` 的语义差异

| 方法 | 是否启动 Plot | 是否注入种子 | 是否注入终止信号 | 阻塞 | 典型场景 |
| --- | :---: | :---: | :---: | :---: | --- |
| `Run(inputs, logLevel)` | ✅ | ✅ `inputs` | ✅ `sourceNodes` | 是 | 一次性批处理 |
| `Start(logLevel)` | ✅ | ❌ | ❌ | 是 | 外部驱动（如 reporter、交互式注入） |
| `InjectTasks(tasks)` | ❌ | ✅ `tasks` | ❌ | 否 | 运行期追加种子 |
| `InjectTerminations(nodes)` | ❌ | ❌ | ✅ `nodes` | 否 | 运行期终止指定 Plot |

- `Run` = `validateRunInputs` + `start(logLevel, prepare)`，`prepare` 依次执行 `InjectTasks(inputs)` 与 `InjectTerminations(f.sourceNodes)`。
- `Start` 不做输入校验，直接调用 `start(logLevel, nil)`；**没有任何外部注入终止信号时 `Start` 不会返回**。
- `InjectTasks` / `InjectTerminations` 只操作已注册的 Plot，不触发资源装配，也不改变 `sourceNodes`。

> 日志级别不再由 `NewFarm` 决定：`logLevel` 是每次运行（`Run` / `Start`）的参数，空字符串按 `INFO` 处理。

## 关键流程

### `AddPlot → Connect → Run` 全链路

```mermaid
sequenceDiagram
    autonumber
    participant U as 调用方
    participant F as Farm
    participant A as assembly
    participant H as Farm Hub
    participant P as PlotNode

    U->>F: NewFarm(name)
    Note over F: 初始化 plots 表 / OrderGraph / Hub

    U->>F: AddPlot(p1, p2, ...)
    loop 每个 plot
        F->>F: 校验非 nil / 名称非空 / 唯一
        F->>F: AddNode(name)
        F->>P: SetEventClient(eventClient)
    end

    U->>F: Connect(froms, tos)
    F->>F: uniquePlots 去重 + 过滤 nil
    F->>F: requireRegistered(froms / tos)
    loop from × to 笛卡尔积
        F->>P: from.ConnectTo(to)
        Note over P: 登记 yieldChans 与 upstreams
        F->>F: AddEdge(from, to)
    end

    U->>F: Run(inputs, logLevel)
    F->>F: validateRunInputs(inputs)
    F->>F: sourceNodes = SourceNodes(OrderGraph)
    F->>A: StartFarmResources(F.hub, logLevel)
    A->>H: AddWatcher(Metrics / Lifecycle / Log)
    F->>H: emit(FarmStartEvent)
    loop 每个 plot
        F->>P: AddWatcher(F.hub)
    end
    loop 每个 plot
        F->>P: StartAsync()
    end
    F->>F: InjectTasks(inputs)
    F->>F: InjectTerminations(sourceNodes)
    Note over F: 注入失败 → 兜底 InjectTerminations(sourceNodes)
    loop 每个 plot
        F->>P: WaitAsync()
    end
    F->>H: emit(FarmEndEvent)
    F->>A: resources.Stop()
```

### `start` 内部流程（`Run` / `Start` 共用）

1. 记录 `startTime`。
2. `assembly.StartFarmResources(f.hub, logLevel)` 装配运行期资源：创建 `MetricsWatcher`、`LifecycleWatcher`、`LogWatcher`，启动后注册到 Farm Hub。失败时立即返回，Plot 尚未启动。
3. `defer resources.Stop()`：运行结束时停止并刷盘。
4. 发射 `scarecrow.FarmStartEvent`，包含 `Farm`、`IsDAG`、`Plots`、`Edges`、`SourcePlots`、`Structure`、`StartTime`。
5. `defer` 发射 `scarecrow.FarmEndEvent`（`Elapsed` 为 `time.Since(startTime)`）。
6. 对每个 plot 调用 `plot.AddWatcher(f.hub)`——**Farm Hub 作为嵌套 Hub 注册到每个 Plot**。
7. 对每个 plot 调用 `StartAsync()`。
8. 调用 `prepare()`（`Run` 时为注入逻辑，`Start` 时为 `nil`）。
9. 若 `prepare` 返回错误，仍执行一次 `InjectTerminations(f.sourceNodes)` 作为兜底（忽略其错误），避免等待一个永远不会收到终止信号的图。
10. 对每个 plot 调用 `WaitAsync()`，全部退出后返回 `prepareErr`。

> `start` 只返回装配错误或注入错误；Plot 内部的培育失败以 `SeedWitherEvent` 形式进入观察者体系，不会让 `Run` / `Start` 返回 error。

### 层级 Hub 分发

```mermaid
flowchart LR
    subgraph PlotHub[Plot 内部 Hub]
        PE[Plot.emit] --> PH[hub]
    end
    PH -->|AddWatcher 注册| FH[Farm Hub]
    FH --> M[MetricsWatcher]
    FH --> L[LifecycleWatcher]
    FH --> G[LogWatcher]
    FH --> W[用户 AddWatcher]
    FE[Farm.emit] --> FH

    style FH fill:#eef
```

- Plot 事件（`PlotStart`、`SeedRipen` 等）由 Plot 自身 Hub 分发给已注册的观察者，Farm Hub 是其中之一，因此会继续向上分发。
- Farm 事件（`FarmStartEvent` / `FarmEndEvent`）直接进入 Farm Hub，**不会**下发到 Plot。
- `scarecrow.Find` 可沿嵌套 Hub 查找观察者，`plot.Harvest()` 正是借此在 Plot Hub → Farm Hub 链上找到 `LifecycleWatcher`。
- `Hub` 的写时复制语义允许运行期追加观察者，但只对之后的事件生效；重复注册同一 Hub 会导致重复分发。

### `sourceNodes` 的计算

`Run` 与 `Start` 在启动前都执行 `f.sourceNodes = SourceNodes(f.OrderGraph)`：

1. `TarjanSCC` 求强连通分量；
2. 计算凝聚图中入度为 0 的 Source SCC（`SourceSCCs`）；
3. 每个 Source SCC 取 `scc[0]` 作为代表节点（`SourceNodes`）。

这样即使 source 处于一个强连通环内，也只对一个代表节点发终止信号，避免重复 seal。孤立节点（`AddPlot` 后未连边）同样会成为 Source SCC，因此也会被注入终止信号。

## 错误场景

### `AddPlot`

- 任一 plot 为 `nil` → `plot is nil`
- 名称为空 → `plot name cannot be empty`
- 名称重复 → `plot %q already exists`

遇到第一个错误即返回；此前已成功加入的 plot **不会回滚**。

### `Connect`

- 去重后 `fromPlots` 为空 → `from plots cannot be empty`
- 去重后 `toPlots` 为空 → `to plots cannot be empty`
- 任意 plot 为 `nil` 或未注册 → `plot is nil` / `plot %q is not registered in farm`
- `from.ConnectTo(to)` 类型断言失败 → 透传 `plot %q yield type is incompatible with plot %q seed type`

> ⚠️ **部分失败不撤销**：内层循环一旦失败立即返回，已建立的连接（`yieldChans`、`upstreams`、图边）保留。再次 `Connect` 时图层面会因重复边被忽略，但 Plot 内部映射仍保留旧状态。

### `AddWatcher`

- `watcher` 为 `nil` → `scarecrow: cannot add nil listener`
- 会形成 Hub 环 → `scarecrow: cyclic hub registration rejected: ...` / `scarecrow: cannot add a hub to itself`

### `Run`

| 阶段 | 错误来源 | 说明 |
| --- | --- | --- |
| 校验 | `validateRunInputs` | `inputs` 中存在未注册的 plot → `plot %q is not registered in farm`，此时**未启动任何 Plot** |
| 装配 | `assembly.StartFarmResources` | Hub 为 nil、watcher 启动失败等；Plot 尚未启动 |
| 注册 | `plot.AddWatcher(f.hub)` | Hub 环或 nil 观察者；在 `StartAsync` 之前返回 |
| 注入 | `InjectTasks` | 未注册 plot、`SeedAny` 类型断言失败（`plot %q seed type mismatch: got %T`） |
| 注入 | `InjectTerminations` | 未注册 plot |
| 等待 | `WaitAsync` | 不返回错误 |

### `InjectTasks`

- 先对所有 key 做 `validateRunInputs`：任一目标未注册 → 返回错误，**不注入任何种子**。
- 随后按 map 迭代顺序逐颗 `SeedAny`；中途某颗类型不匹配会立即返回，此前已注入的种子**无法撤回**。

### `InjectTerminations`

- 先校验所有名称均已注册；任一未注册 → 返回错误，**不注入任何终止信号**。
- 校验通过后逐个调用 `Seal()`，注入外部终止信号（`Source == "__input__"`），具有强终止语义：不再等待未到达的上游 seal。
- 向某个 Plot 注入终止信号后，再向它注入种子将不会被处理。

## 重要细节

### 组到组笛卡尔积

`Connect(froms, tos)` 先对两端做 `uniquePlots`（去重 + 过滤 `nil`），再建立 `len(froms) × len(tos)` 条边：

```go
farm.Connect([]plot.PlotNode{root}, []plot.PlotNode{midA, midB})
// 等价于 root → midA 与 root → midB 两条独立边
```

只有「源组 × 目标组」之间会连边，同组内节点不会互相连接。

### 注入失败的终止兜底

`start` 中 `prepare()` 失败后仍调用 `InjectTerminations(f.sourceNodes)`：

- 避免 `WaitAsync` 永久阻塞在一个没有终止信号的图上；
- 兜底错误被显式忽略（`_ =`），返回值仍是原始的 `prepareErr`；
- 该兜底只终止 `sourceNodes`，下游收到 seal 信号后按正常传播路径退出。

### 事件与日志

- `Farm` 只发射两个事件：`FarmStartEvent`、`FarmEndEvent`，定义在 `pkg/scarecrow/event.go`。
- `Structure` 字段来自 `getStructureList()`，即 `RenderStructureList(f.Nodes(), f.OutEdges(), f.sourceNodes)`。
- `persist.LogWatcher` 消费 `FarmStartEvent` 时逐行输出结构，消费 `FarmEndEvent` 时输出耗时。
- `Farm` 自身不写数据库；生命周期与日志落盘全部由 `assembly` 注册的观察者完成。

### 与 `pkg/runtime` 的协作

`eventClient` 在 `AddPlot` 时注入，所有 plot 共享同一事件 ID 命名空间，保证跨 plot 的 seed → fruit → seal 事件链可追溯。

### 并发控制

- `Run` / `Start` 是**同步**调用：依次 `StartAsync` 后通过 `WaitAsync` 等待全部 plot 退出。
- `StartAsync` 内部使用 `sync.WaitGroup.Go`（Go 1.25 写法）启动每个 plot 的 `sprout` 调度器。
- `InjectTasks` / `InjectTerminations` 设计为可在 `Start` 阻塞期间由其它协程调用；`Farm` 的 `plots` 表本身不加锁，因此**注册/连接阶段不能与运行阶段并发**。

## 使用示例

### 批处理：`Run`

```go
package main

import (
    "fmt"

    "github.com/Mr-xiaotian/CelestialGrow/pkg/farm"
    "github.com/Mr-xiaotian/CelestialGrow/pkg/plot"
)

func main() {
    double := plot.NewSinglePlot("double", func(seed int) (int, error) {
        return seed * 2, nil
    }, plot.WithTenders(2))

    format := plot.NewSinglePlot("format", func(seed int) (string, error) {
        return fmt.Sprintf("result=%d", seed), nil
    })

    f := farm.NewFarm("demo_farm")
    if err := f.AddPlot(double, format); err != nil {
        panic(err)
    }
    if err := f.Connect([]plot.PlotNode{double}, []plot.PlotNode{format}); err != nil {
        panic(err)
    }

    if err := f.Run(map[string][]any{
        "double": {1, 2, 3, 4},
    }, "INFO"); err != nil {
        panic(err)
    }
}
```

### 外部驱动：`Start` + `InjectTasks` / `InjectTerminations`

```go
package main

import (
    "fmt"

    "github.com/Mr-xiaotian/CelestialGrow/pkg/farm"
    "github.com/Mr-xiaotian/CelestialGrow/pkg/plot"
    "github.com/Mr-xiaotian/CelestialGrow/pkg/scarecrow"
)

func main() {
    root := plot.NewSinglePlot("root", func(seed int) (int, error) {
        return seed * 2, nil
    }, plot.WithTenders(2), plot.WithChanSize(8))

    f := farm.NewFarm("external_driven")
    if err := f.AddPlot(root); err != nil {
        panic(err)
    }

    if err := f.AddWatcher(scarecrow.WatcherFunc(func(event scarecrow.Event) error {
        switch ev := event.(type) {
        case scarecrow.PlotStartEvent:
            fmt.Println("plot started:", ev.Plot)
        case scarecrow.FarmEndEvent:
            fmt.Println("farm done in", ev.Elapsed)
        }
        return nil
    })); err != nil {
        panic(err)
    }

    go func() {
        for i := 0; i < 3; i++ {
            if err := f.InjectTasks(map[string][]any{"root": {i}}); err != nil {
                panic(err)
            }
        }
        if err := f.InjectTerminations([]string{"root"}); err != nil {
            panic(err)
        }
    }()

    if err := f.Start("INFO"); err != nil {
        panic(err)
    }
}
```

> `Start` 会阻塞到所有 Plot 退出；必须确保有外部协程（或信号处理逻辑）最终调用 `InjectTerminations`，否则调用不会返回。

### 运行结束后读取结果

```go
records, err := root.Harvest()
if err != nil {
    panic(err)
}
for _, record := range records {
    fmt.Println(record.Status) // 例如 ripen
}
```

`Harvest` 依赖 Farm Hub 已注册到 Plot Hub，从而能沿嵌套 Hub 找到 `LifecycleWatcher`。

## 注意事项

- **非重入**：同一个 `Farm` 实例不支持并发或重复 `Run` / `Start`。每次 `start` 都会再次把 Farm Hub 注册到每个 Plot，导致事件重复分发；Plot 的 `state` 也停留在 `done`。
- **注入顺序**：`InjectTasks` 依赖 `plots[name]` 已注册。运行期注入前不要修改注册表。
- **终止不可逆**：`Seal()` 之后该 Plot 的 `sprout` 不再消费种子；`InjectTasks` 不报错，但种子只会滞留在 `seedChan` 中，缓冲写满后调用会被阻塞。
- **图与节点集合**：`OrderGraph` 的节点可能由 `AddEdge` 自动补全，但正常路径下 `AddPlot` 已注册全部节点；`sourceNodes` 使用图视图而非 `plots` 视图。
- **日志级别**：`logLevel` 取值见 `pkg/persist` 的级别表，未知值按 `INFO` 处理。
- **测试覆盖**：`farm_connect_test.go`、`farm_start_test.go`、`farm_structure_test.go`、`farm_route_test.go`、`farm_split_test.go`、`farm_inject_test.go`、`farm_harvest_test.go`、`graph_test.go`、`render_test.go` 覆盖注册/连接、Run 线性流、非平凡拓扑、路由/拆分、外部注入、Harvest、图算法与结构渲染。
