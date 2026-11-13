# demo/demo_prune/main.go

> 📅 最后更新日期: 2026/11/13

## 作用

`demo/demo_prune/main.go` 是 CelestialGrow 项目演示 **prune（修剪）机制** 的示例，展示了如何用 `pkg/api` 提供的 `WithPruneIf` 对种子做并发安全的**去重**：重复种子在进入 cultivator 之前就被标记为 `prune`，不再被培育，也不产生下游 fruit。

与 [`demo_farm/main.go`](../demo_farm/main.md) 相比，本示例聚焦两点：

- **单 plot 场景**（`singlePlotDedup`）：对一个含重复值的种子列表直接去重后翻倍。
- **多 plot 场景**（`farmDedup`）：root 翻倍产生重复值，head 在去重的同时加一。

两条路径都复用了 `WithPruneIf` 谓词，只是接入位置不同。

## 代码结构

文件由四段组成：

1. **包与导入**：以 `package main` 形式提供可执行入口，导入 `fmt`、`sync` 与 `pkg/api`（别名 `grow`）。
2. **`deduplicator` 去重器**：内部用一个 `sync.Mutex` 保护的 `map[int]struct{}` 维护「已见种子」集合，并提供 `pruneIf` 谓词方法。
3. **两个 cultivator 与一个谓词方法**：`double`、`addOne` 作为 cultivator，`deduplicator.pruneIf` 作为 `WithPruneIf` 谓词。
4. **两个场景 + `main`**：`singlePlotDedup` 与 `farmDedup` 分别演示单/多 plot 去重，`main` 依次调用二者。

## 关键对象

### `deduplicator`

```go
// deduplicator 维护一个并发安全的已见种子集合。
type deduplicator struct {
	mu   sync.Mutex
	seen map[int]struct{}
}

// newDeduplicator 创建一个空的去重器。
func newDeduplicator() *deduplicator {
	return &deduplicator{seen: make(map[int]struct{})}
}

// pruneIf 生成 WithPruneIf 谓词：种子已见过则修剪（跳过），
// 否则登记为已见并继续培育。加锁保证多 tender 并发下仍只处理一次。
func (d *deduplicator) pruneIf(seed int) bool {
	d.mu.Lock()
	defer d.mu.Unlock()
	if _, ok := d.seen[seed]; ok {
		return true
	}
	d.seen[seed] = struct{}{}
	return false
}
```

- `pruneIf` 返回值被 `WithPruneIf` 直接采用：`true` 表示该 seed 应被修剪，`false` 表示继续培育。
- 加锁保证在多 tender 并发下仍「只处理一次」：即使有两个协程同时遇到相同 seed，也只有一个能把 `false` 与登记原子地完成，另一个拿到 `true`。

## 关键 API

本示例用到的相关 API（构造与配置来自 `pkg/api`，计数方法由 `pkg/plot` 的 `basePlot` 提供）：

| 调用 | 来源 | 作用 |
|------|------|------|
| `grow.WithPruneIf[S any](fn func(S) bool)` | `pkg/api` → `plot.WithPruneIf` | 为 Plot 注入修剪谓词；在 cultivator 之前对每个 seed 求值，命中则跳过培育。 |
| `grow.NewSinglePlot[S, F](name, cultivator, opts...)` | `pkg/api` → `plot.NewSinglePlot` | 创建单进单出节点；`WithPruneIf` 通过 `opts` 注入。 |
| `p.GetPruneNum() int` | `pkg/plot` 的 `basePlot` 方法（经 `SinglePlot` 提升） | 读取该 Plot 累计被修剪的 seed 数。 |
| `p.GetFruitNum() int` / `p.GetWeedNum() int` | 同上 | 读取果实/杂草计数，与 `GetPruneNum` 一起展示整体状态。 |

> 这些计数方法读取 `MetricsWatcher` 的指标快照。`SinglePlot.Run` 与 `Farm.Run` 都会经 `pkg/assembly` 自动注册 `MetricsWatcher`，因此运行结束后可直接读取；若未装配指标观察者，它们会返回 `0`。

## 场景一：`singlePlotDedup`

```go
	seeds := []int{1, 2, 3, 2, 1, 4, 4, 5}

	p := grow.NewSinglePlot("single_dedup", double,
		grow.WithTenders(3),
		grow.WithPruneIf(newDeduplicator().pruneIf),
	)
	p.Run(seeds, "INFO")
```

- 输入 8 个 seed，其中 `1`、`2`、`4` 各重复出现。
- `WithPruneIf` 维护「已见集合」，首次出现的 5 个不同值被培育（去重后翻倍），重复的 3 个被修剪。
- `p.Run` 是 single-plot 的同步运行入口，不需要 Farm；第二个参数为日志级别。
- 输出：`fruit=5（去重后翻倍）, prune=3（重复被修剪）, weed=0`。

## 场景二：`farmDedup`

```go
	root := grow.NewSinglePlot("double", double, grow.WithTenders(3))
	head := grow.NewSinglePlot("dedup_add", addOne,
		grow.WithTenders(3),
		grow.WithPruneIf(newDeduplicator().pruneIf),
	)

	farm := grow.NewFarm("farm_dedup")
	if err := farm.AddPlot(root, head); err != nil {
		panic(err)
	}
	if err := farm.Connect([]grow.PlotNode{root}, []grow.PlotNode{head}); err != nil {
		panic(err)
	}
	if err := farm.Run(map[string][]any{
		"double": {1, 2, 2, 3, 3, 3},
	}, "INFO"); err != nil {
		panic(err)
	}
```

- `root` 把 6 个 seed 翻倍后转发给 `head`，产生 `2, 4, 4, 6, 6, 6`。
- `head` 对这 6 个 fruit（作为其 seed）做去重：`2, 4, 6` 被培育并各自加一，得到 `3, 5, 7`；重复的 3 个被修剪。
- 输出：`dedup_add: fruit=3（去重+1 -> 3,5,7）, prune=3（重复被修剪）, weed=0`。

## 运行方式

在项目根目录下执行：

```bash
go run ./demo/demo_prune
```

执行成功后终端会打印四行摘要：

```text
[single plot] input=[1 2 3 2 1 4 4 5]
[single plot] fruit=5 (去重后翻倍), prune=3 (重复被修剪), weed=0
[farm] double:   seed=6 -> fruit=6 (翻倍转发 2,4,4,6,6,6)
[farm] dedup_add: fruit=3 (去重+1 -> 3,5,7), prune=3 (重复被修剪), weed=0
```

同时当前工作目录下会生成运行产物：

- `logs/grow_log(YYYY-MM-DD).log`：两次 `Run` 的日志按天追加到同一文件，日志级别均为 `"INFO"`。
- `lifecycles/YYYY-MM-DD/`：`singlePlotDedup` 与 `farmDedup` 各调用一次 `Run`，各自生成一个 `grow_lifecycle(HH-MM-SS.mmm).sqlite3`，因此该目录下会出现两个生命周期库。

## 完整源码

以下代码与 `demo/demo_prune/main.go` 源码完全一致，可直接复制运行：

```go
package main

import (
	"fmt"
	"sync"

	grow "github.com/Mr-xiaotian/CelestialGrow/pkg/api"
)

// deduplicator 维护一个并发安全的已见种子集合。
type deduplicator struct {
	mu   sync.Mutex
	seen map[int]struct{}
}

// newDeduplicator 创建一个空的去重器。
func newDeduplicator() *deduplicator {
	return &deduplicator{seen: make(map[int]struct{})}
}

// pruneIf 生成 WithPruneIf 谓词：种子已见过则修剪（跳过），
// 否则登记为已见并继续培育。加锁保证多 tender 并发下仍只处理一次。
func (d *deduplicator) pruneIf(seed int) bool {
	d.mu.Lock()
	defer d.mu.Unlock()
	if _, ok := d.seen[seed]; ok {
		return true
	}
	d.seen[seed] = struct{}{}
	return false
}

// double 将种子翻倍。
func double(num int) (int, error) {
	return num * 2, nil
}

// addOne 为种子加一。
func addOne(num int) (int, error) {
	return num + 1, nil
}

// singlePlotDedup 单 plot 场景：对含重复值的种子列表去重后翻倍。
func singlePlotDedup() {
	seeds := []int{1, 2, 3, 2, 1, 4, 4, 5}

	p := grow.NewSinglePlot("single_dedup", double,
		grow.WithTenders(3),
		grow.WithPruneIf(newDeduplicator().pruneIf),
	)
	p.Run(seeds, "INFO")

	fmt.Printf("[single plot] input=%v\n", seeds)
	fmt.Printf("[single plot] fruit=%d (去重后翻倍), prune=%d (重复被修剪), weed=%d\n",
		p.GetFruitNum(), p.GetPruneNum(), p.GetWeedNum())
}

// farmDedup 多 plot 场景：root 翻倍产生重复值，head 去重后加一。
func farmDedup() {
	root := grow.NewSinglePlot("double", double, grow.WithTenders(3))
	head := grow.NewSinglePlot("dedup_add", addOne,
		grow.WithTenders(3),
		grow.WithPruneIf(newDeduplicator().pruneIf),
	)

	farm := grow.NewFarm("farm_dedup")
	if err := farm.AddPlot(root, head); err != nil {
		panic(err)
	}
	if err := farm.Connect([]grow.PlotNode{root}, []grow.PlotNode{head}); err != nil {
		panic(err)
	}
	if err := farm.Run(map[string][]any{
		"double": {1, 2, 2, 3, 3, 3},
	}, "INFO"); err != nil {
		panic(err)
	}

	fmt.Println("[farm] double:   seed=6 -> fruit=6 (翻倍转发 2,4,4,6,6,6)")
	fmt.Printf("[farm] dedup_add: fruit=%d (去重+1 -> 3,5,7), prune=%d (重复被修剪), weed=%d\n",
		head.GetFruitNum(), head.GetPruneNum(), head.GetWeedNum())
}

// main 演示用 prune 机制做种子去重：单 plot 与 farm 两个场景。
func main() {
	singlePlotDedup()
	farmDedup()
}
```

## 注意事项

1. **谓词优先于 cultivator**：`pruneIf` 在 `tend` 内部、调用 cultivator **之前**求值；命中即走 `pruneSeed` 通路，不会执行业务逻辑，也不会向下游产出。
2. **并发安全是调用方职责**：`WithPruneIf` 只是求值谓词，不保证谓词内部状态并发安全；本示例用 `sync.Mutex` 保护的 `deduplicator` 来保证多 tender 下只处理一次。
3. **修剪计入完成**：被修剪的 seed 计入 `GetCompleted`（`fruit + weed + prune`），也计入 `pruneNum`，因此与「失败」区分开——修剪是设计内的主动跳过，而非错误。
4. **持久化可见**：被修剪的 seed 会在生命周期日志/库中留下 `SeedPruneEvent` 事件，状态为 `prune`，与 `ripen` / `wither` 并列。
5. **谓词只作用于本级 seed**：`WithPruneIf` 对 Plot 收到的每个输入 seed 求值；若要根 node 也去重，需在创建根 Plot 时同样传入谓词。
