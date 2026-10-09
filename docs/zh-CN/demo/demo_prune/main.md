# demo/demo_prune/main.go

> 📅 最后更新日期: 2026/10/09

## 作用

`demo/demo_prune/main.go`（对应源码 `demo/demo_prune/main.go`）是 CelestialGrow 项目演示 **prune（修剪）机制** 的示例，展示了如何用 `pkg/api` 提供的 `WithPruneIf` 对种子做并发安全的**去重**：重复种子在进入 cultivator 之前就被标记为 `prune`，不再被培育，也不产生下游 fruit。

与 [`demo_farm/main.go`](./demo_farm/main.md) 相比，本示例聚焦两点：

- **单 plot 场景**（`singlePlotDedup`）：对一个含重复值的种子列表直接去重后翻倍。
- **多 plot 场景**（`farmDedup`）：root 翻倍产生重复值，head 在去重的同时加一。

两条路径都复用了 `WithPruneIf` 谓词，只是接入位置不同。

## 代码结构

文件由四段组成：

1. **包与导入**：以 `package main` 形式提供可执行入口，导入 `fmt`、`sync` 与 `pkg/api`（别名 `grow`）。
2. **`deduplicator` 去重器**：内部用一个 `sync.Mutex` 保护的 `map[int]struct{}` 维护「已见种子」集合，并提供 `pruneIf` 谓词方法。
3. **三个 slab 函数**：`double` 与 `addOne` 作为 cultivator，`pruneIf` 作为 `WithPruneIf` 谓词。
4. **两个场景 + `main`**：`singlePlotDedup` 与 `farmDedup` 分别演示单/多 plot 去重，`main` 依次调用二者。

## 关键对象

### `deduplicator`

```go
type deduplicator struct {
	mu   sync.Mutex
	seen map[int]struct{}
}

func (d *deduplicator) pruneIf(seed int) bool {
	d.mu.Lock()
	defer d.mu.Unlock()
	if _, ok := d.seen[seed]; ok {
		return true // 已见即修剪
	}
	d.seen[seed] = struct{}{}
	return false // 首次见，继续培育
}
```

- `pruneIf` 返回值被 `WithPruneIf` 直接采用：`true` 表示该 seed 应被修剪，`false` 表示继续培育。
- 加锁保证在多 tender 并发下仍「只处理一次」：即使有两个协程同时遇到相同 seed，也只有一个能把 `false` 与登记原子地完成，另一个拿到 `true`。

## 关键 API

本示例用到的 prune 相关 API（均来自 `pkg/api`）：

| 调用 | 来源 | 作用 |
|------|------|------|
| `grow.WithPruneIf(fn func(S) bool)` | `pkg/api` → `plot.WithPruneIf` | 为 Plot 注入修剪谓词；在 cultivaor 之前对每个 seed 求值，命中则跳过培育。 |
| `p.GetPruneNum()` | `pkg/api` → `plot.Counter.GetPruneNum` | 读取该 Plot 累计被修剪的 seed 数。 |
| `p.GetFruitNum()` / `p.GetWeedNum()` | `pkg/api` → `plot.Counter` | 读取果实/杂草计数，与 `GetPruneNum` 一起展示整体状态。 |

## 场景一：`singlePlotDedup`

```go
seeds := []int{1, 2, 3, 2, 1, 4, 4, 5}

p := grow.NewPlot("single_dedup", double,
	grow.WithTenders(3),
	grow.WithPruneIf(newDeduplicator().pruneIf),
)
p.Run(seeds)
```

- 输入 8 个 seed，其中 `1`、`2`、`4` 各重复出现。
- `WithPruneIf` 维护「已见集合」，首次出现的 5 个不同值被培育（去重后翻倍），重复的 3 个被修剪。
- `p.Run` 是 single-plot 的同步运行入口，不需要 Farm。
- 输出：`fruit=5（去重后翻倍）, prune=3（重复被修剪）, weed=0`。

## 场景二：`farmDedup`

```go
root := grow.NewPlot("double", double, grow.WithTenders(3))
head := grow.NewPlot("dedup_add", addOne,
	grow.WithTenders(3),
	grow.WithPruneIf(newDeduplicator().pruneIf),
)

farm := grow.NewFarm("farm_dedup", "INFO")
farm.AddPlot(root, head)
farm.Connect([]grow.PlotNode{root}, []grow.PlotNode{head})
farm.Run(map[string][]any{
	"double": {1, 2, 2, 3, 3, 3},
})
```

- `root` 把 6 个 seed 翻倍后转发给 `head`，产生 `2, 4, 4, 6, 6, 6`。
- `head` 对这 6 个 fruit（作为其 seed）做去重：`2, 4, 6` 被培育并各自加一，得到 `3, 5, 7`；重复的 3 个被修剪。
- 输出：`dedup_add: fruit=3（去重+1 -> 3,5,7）, prune=3（重复被修剪）, weed=0`。

## 运行方式

在项目根目录下执行：

```bash
go run ./demo/demo_prune
```

执行成功后终端会打印两行摘要：

```text
[single plot] input=[1 2 3 2 1 4 4 5]
[single plot] fruit=5 (去重后翻倍), prune=3 (重复被修剪), weed=0
[farm] double:   seed=6 -> fruit=6 (翻倍转发 2,4,4,6,6,6)
[farm] dedup_add: fruit=3 (去重+1 -> 3,5,7), prune=3 (重复被修剪), weed=0
```

同时当前目录下会像其他 Farm 示例一样生成 `logs/` 与 `lifecycles/` 两类运行产物。

## 注意事项

1. **谓词优先于 cultivator**：`pruneIf` 在 `tend` 内部、调用 cultivator **之前**求值；命中即走 `pruneSeed` 通路，不会执行业务逻辑，也不会向下游产出。
2. **并发安全是调用方职责**：`WithPruneIf` 只是求值谓词，不保证谓词内部状态并发安全；本示例用 `sync.Mutex` 保护的 `deduplicator` 来保证多 tender 下只处理一次。
3. **修剪计入完成**：被修剪的 seed 计入 `GetCompleted`（`fruit + weed + prune`），也计入 `pruneNum`，因此与「失败」区分开——修剪是设计内的主动跳过，而非错误。
4. **持久化可见**：被修剪的 seed 会在生命周期日志/库中留下 `SeedPrune` 记录，状态为 `prune`，与 `ripen` / `wither` 并列。
5. **谓词只作用于本级 seed**：`WithPruneIf` 对 Plot 收到的每个输入 seed 求值；若要根 node 也去重，需在创建根 Plot 时同样传入谓词。