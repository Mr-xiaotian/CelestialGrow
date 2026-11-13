# pkg/plot/plot_single.go

> 📅 最后更新日期: 2026/11/13

`plot_single.go` 定义 `pkg/plot` 中最常用的节点类型 `SinglePlot[S, F]`：把「接收 seed → 并发执行 cultivator → 产出一颗 fruit → 广播给所有已连接下游」封装为可连接、可观察、可重试的泛型节点。

`SinglePlot` 本身非常薄——它只负责最朴素的成功语义（**一颗 seed 培育出一颗 fruit，并把该 fruit 原样转发给每一个已连接下游**），其余通用能力（状态机、并发调度、重试、修剪、seal 传播、事件发送、standalone 跑批、结果导出）全部由内嵌的 `basePlot[S, F, F]` 提供，详见 `plot.md`。

## 作用

- 暴露泛型并发节点 `SinglePlot[S, F]`（`S` 为种子类型，`F` 为果实类型）。
- 通过内嵌 `*basePlot[S, F, F]` 复用共享骨架，从而使 `SinglePlot` 满足 `PlotNode` 接口，可被 `pkg/farm` 注册、连边、调度。
- 注入成功钩子 `ripenSeed`：发出成熟事件，并向所有下游各转发一颗 yield。

## 核心对象

### `SinglePlot[S, F]` 结构

```go
type SinglePlot[S any, F any] struct {
	*basePlot[S, F, F]
}
```

`SinglePlot` 没有自己的字段，全部状态与能力都来自 `*basePlot[S, F, F]`。

| 泛型参数 | 语义 |
|---------|------|
| `S` | 种子（seed）输入类型，即 `cultivator` 的入参类型 |
| `F` | 果实（fruit）输出类型，即 `cultivator` 的返回类型 |

`basePlot` 的第三个泛型参数（下游 yield 类型 `Y`）在本类型中被实例化为 `F`，即**本节点的果实类型与向下游输出的类型一致**。三种节点对比如下：

| 节点 | 底层 `basePlot` | 结果类型（fruit） | 下游 yield 类型 `Y` |
|------|----------------|------------------|--------------------|
| `SinglePlot[S, F]` | `basePlot[S, F, F]` | `F` | `F` |
| `SplitPlot[S, F]` | `basePlot[S, []F, F]` | `[]F` | `F` |
| `RoutePlot[S, Y]` | `basePlot[S, map[string]Y, Y]` | `map[string]Y` | `Y` |

> `ConnectTo` 会用类型断言校验「上游的 `Y`」与「下游的 `S`」是否一致。因此 `SinglePlot[S, F]` 只能连到 `S == F` 的下游节点，否则连接时立即返回类型不兼容错误。

## 公开函数

### `NewSinglePlot[S, F](name string, cultivator func(S) (F, error), opts ...Option) *SinglePlot[S, F]`

创建一个 `SinglePlot` 实例。

| 参数 | 含义 |
|------|------|
| `name` | plot 名称，在 `Farm` 中需唯一 |
| `cultivator` | 单颗种子的培育函数，返回果实或错误 |
| `opts` | 可选配置（见 `option.md`），在 `defaultOptions()` 之后依次应用 |

实现要点：

1. 先声明 `var p *SinglePlot[S, F]`，再用 `newBasePlot[S, F, F]` 构造共享骨架，最后把 `base` 赋值给 `p`；
2. 传给 `newBasePlot` 的 `ripenSeed` 钩子是一个闭包，内部调用 `p.ripenSeed(...)`。它捕获了尚未赋值的 `p`，但该闭包只在 `StartAsync` 之后的 `sprout` / `tend` 路径上被调用，彼时 `p` 一定已完成赋值；
3. channel 分配、默认 `EventClient` 与 `Hub` 初始化、`context` 派生都在 `newBasePlot` 内完成（见 `plot.md` 的「构造」一节）。

### `ripenSeed(seedPayload runtime.Payload[S], fruit F, startTime time.Time)`（未导出）

`SinglePlot` 的成功路径实现，作为钩子写入 `basePlot.ripenSeed` 字段，由 `basePlot.tend` 在 `cultivator` 返回 `nil` 错误时调用。步骤：

1. 取 `seedPayload.EventID` 作为 `seedID`，通过 `eventClient.Emit("fruit", []int{seedID})` 分配 `fruitID`；
2. 计算可读表示：seed 截断到 50 rune、fruit 截断到 25 rune（见 `helper.md`）；
3. 发出 `scarecrow.SeedRipenEvent`（`Plot`、`Seed`、`SeedRepr`、`Fruit`、`FruitRepr`、`Elapsed`、`SeedID`、`FruitID`、`Time`）——指标观察者据此把成功数 +1，生命周期观察者据此落一条 `ripen` 记录（`FruitJSON` 为果实 JSON）；
4. 遍历 `p.yieldChans`（`下游 plot 名 → 该下游的 seed 通道`），对**每一个**已连接下游：
   - `eventClient.Emit("seed", []int{fruitID})` 分配下游 seed 事件 ID（父事件为 `fruitID`）；
   - 发出 `scarecrow.SeedInputEvent`（`Plot` 为下游名、`From` 为本节点名、`Seed` 为 fruit、`Parents` 为 `[]int{fruitID}`），指标观察者据此同时累加接收方的上游输入与来源方的下游产出；
   - 向该下游通道发送 `runtime.Payload[F]{Value: fruit, EventID: downstreamSeedID}`。

> 语义要点：`SinglePlot` 的成功路径是「**一颗 fruit 转发为一颗下游 yield**」，并且**向所有已连接下游都转发一次**（广播）。需要「按目标定向转发」请用 `RoutePlot`（见 `plot_route.md`），需要「一颗 seed 拆成多颗下游 yield」请用 `SplitPlot`（见 `plot_split.md`）。

## 关键流程

### 从 seed 到下游 yield

```mermaid
flowchart LR
    SC[seedChan] --> T[tend]
    T -->|pruneIf 命中| P[pruneSeed<br/>无下游产出]
    T -->|失败| W[witherSeed<br/>无下游产出]
    T -->|成功| R[SinglePlot.ripenSeed]
    R --> F["fruit 事件<br/>fruitID"]
    F --> E1["SeedRipenEvent<br/>1 条 ripen 记录"]
    F --> D1["下游 A<br/>SeedInputEvent + yield"]
    F --> D2["下游 B<br/>SeedInputEvent + yield"]
    R --> HUB[Hub<br/>Metrics / Lifecycle / Log]
```

`basePlot.tend` 调用 `cultivator(seed)` 前先判断修剪谓词，若命中直接走 `basePlot.pruneSeed`（不调用 `cultivator`）；否则按结果分流：

- **成功**（`err == nil`）：调用注入的 `ripenSeed`，即本文件的实现；
- **失败**（重试耗尽、`retryIf` 拒绝、或 `panic` 被 `recover` 捕获）：走 `basePlot.witherSeed`，**不向任何下游转发**；
- **修剪**（`WithPruneIf` 谓词返回 `true`）：走 `basePlot.pruneSeed`，**不向任何下游转发**。

修剪、重试循环、seal 传播、并发模型等通用逻辑见 `plot.md`。

### 指标口径

一次成功只产生一条 `SeedRipenEvent`，因此：

- 本节点的 `GetFruitNum()`（`Succeeded`）+1；
- 每个已连接下游各自收到一条 `SeedInputEvent`（`From` 非空），其 `InputTotal` +1，本节点的 `DownstreamCounts` 对应目标 +1。

## 使用示例

### Standalone 模式

```go
package main

import (
	"fmt"

	"github.com/Mr-xiaotian/CelestialGrow/pkg/plot"
)

func main() {
	p := plot.NewSinglePlot("double",
		func(seed int) (int, error) { return seed * 2, nil },
		plot.WithTenders(4),
	)

	p.Run([]int{1, 2, 3, 4, 5}, "INFO")

	records, err := p.Harvest()
	if err != nil {
		panic(err)
	}
	for _, r := range records {
		fmt.Println(r.SeedJSON, r.Status, r.FruitJSON)
	}
}
```

### 带重试

```go
package main

import (
	"errors"
	"time"

	"github.com/Mr-xiaotian/CelestialGrow/pkg/plot"
)

var errPermanent = errors.New("permanent")

func main() {
	p := plot.NewSinglePlot("flaky",
		func(seed int) (int, error) {
			if seed == 0 {
				return 0, errPermanent
			}
			return seed * 2, nil
		},
		plot.WithTenders(2),
		plot.WithMaxRetries(3),
		plot.WithRetryDelay(func(attempt int) time.Duration {
			return time.Duration(attempt) * 100 * time.Millisecond
		}),
		plot.WithRetryIf(func(err error) bool {
			return !errors.Is(err, errPermanent)
		}),
	)

	p.Run([]int{0, 1, 2}, "DEBUG")
}
```

### Farm 模式串联两个单进单出节点

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

	addOne := plot.NewSinglePlot("addOne", func(seed int) (int, error) {
		return seed + 1, nil
	}, plot.WithTenders(2))

	f := farm.NewFarm("demo")
	if err := f.AddPlot(double, addOne); err != nil {
		panic(err)
	}
	if err := f.Connect([]plot.PlotNode{double}, []plot.PlotNode{addOne}); err != nil {
		panic(err)
	}
	if err := f.Run(map[string][]any{"double": {1, 2, 3}}, "INFO"); err != nil {
		panic(err)
	}

	fmt.Println("done")
}
```

> `Farm.Connect` 内部会调用 `ConnectTo` 建立「int → int」这条边：上游 `Y = int`、下游 `S = int`，类型匹配。

## 注意事项

- `NewSinglePlot` 默认使用 `runtime.NumCPU()` 作为并发数与 channel 缓冲；I/O 密集型业务可结合 `WithTenders` / `WithChanSize` 调整。
- `Run(seeds, logLevel)` 是**阻塞**调用，直到所有种子处理完成才返回。
- 上游 `F` 与下游 `S` 类型不匹配时，`ConnectTo` 会立即返回错误而不会 panic；建议在 `Farm.AddPlot` / `Farm.Connect` 后检查错误。
- `SinglePlot` 不提供路由与拆分能力，其转发是「全下游广播」，不要当作定向投递使用。
- 下游若已停止消费且 `seedChan` 缓冲已满，`ripenSeed` 的发送处会阻塞，进而拖慢本节点的 tender。
