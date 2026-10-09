# pkg/plot/plot_prune_test.go

> 📅 最后更新日期: 2026/10/09

`plot_prune_test.go` 聚焦 `Plot` 的**修剪（prune）语义**：验证 `WithPruneIf` 谓词如何决定一颗种子是「直接跳过」还是「正常培育」，以及修剪计数 `GetPruneNum` 与完成数 `GetCompleted` 在混合场景下是否守恒。

## 用例

### `TestPlot_PruneSkipsCultivator` —— 全量命中修剪

- **cultivator**：
  ```go
  func(seed int) (int, error) {
      cultivations.Add(1)
      return 0, errors.New("cultivator should never be called")
  }
  ```
- **配置**：`plot.NewPlot("test_prune_all", cultivator, plot.WithTenders(1), plot.WithPruneIf(func(seed int) bool { return true }))`
- **seeds**：`[]int{1, 2, 3}`

验证：

1. `cultivations.Load() == 0`——谓词恒为 `true`，`cultivator` **一次都没有被调用**；
2. `mustHarvest` 返回 3 条记录，每条 `Status == "prune"`；
3. `plot.GetPruneNum() == 3` 且 `plot.GetCompleted() == 3`。

> 覆盖要点：修剪发生在 `cultivator` 之前，全量命中时培育逻辑完全旁路，且修剪种子照常计入完成数，`GetCompleted` 与种子总数守恒。

### `TestPlot_PruneMiss` —— 全部未命中

- **cultivator**：`func(seed int) (int, error) { return seed * 10, nil }`
- **配置**：`plot.NewPlot("test_prune_miss", cultivator, plot.WithTenders(2), plot.WithPruneIf(func(seed int) bool { return false }))`
- **seeds**：`[]int{1, 2, 3}`

验证：

1. 每颗 seed 的 `Status == "ripen"`、`FruitJSON == strconv.Itoa(seed*10)`；
2. `plot.GetPruneNum() == 0`；
3. `plot.GetFruitNum() == 3`。

> 覆盖要点：谓词恒为 `false` 时，行为与**未设置 `WithPruneIf` 完全一致**——即修剪是一个「零侵入」的可选能力，老代码不受影响。

### `TestPlot_PruneMixed` —— 混合场景

- **cultivator**：`seed % 2 == 0` 时返回错误（wither），否则返回 `seed * 10`（ripen）。
- **配置**：`plot.NewPlot("test_prune_mixed", cultivator, plot.WithTenders(2), plot.WithPruneIf(func(seed int) bool { return seed > 3 }))`
- **seeds**：`[]int{1, 2, 3, 4, 5}`

验证：

1. 5 条记录，`Status` 分布为：`prune`（`seed > 3`：4、5）2 颗、`wither`（偶数 2）1 颗、`ripen`（奇数 1、3）2 颗；
2. `successCount == 2`、`failedCount == 1`、`pruneCount == 2`；
3. `plot.GetFruitNum() == 2`、`plot.GetWeedNum() == 1`、`plot.GetPruneNum() == 2`；
4. `plot.GetCompleted() == 5`——三种结果之和等于种子总数，严格守恒。

> 覆盖要点：prune / ripen / wither 三态在同一个 plot 中可共存，且 `GetCompleted`（果实 + 杂草 + 剪枝）始终与输入种子数相等，保证进度条与 `IsFinish` 在剪枝场景下不会失真。

## 共同前置条件

- 测试包：`package plot_test`（黑盒），不直接访问 `Plot` 未导出字段。
- 复用 `plot_harvest_test.go` 中定义的 `mustHarvest(t, plot harvestable)` 拉取生命周期快照。
- `indexStatusesBySeed(records)` 把记录按 `SeedJSON` 建立索引以便按种子上断言；`SeedJSON` 是 `strconv.Itoa(seed)`，例如种子 `1` 的键为 `"1"`。
- 计数断言依赖 `GetPruneNum()` / `GetFruitNum()` / `GetWeedNum()` / `GetCompleted()`，这些方法在 `pkg/plot/counter.go` 定义。

## 运行方式

```bash
go test ./pkg/plot -run "TestPlot_Prune" -v
```

或一次性跑全部 plot 包测试：

```bash
go test ./pkg/plot/... -v
```

## 注意事项

- 状态字符串以源码为准：修剪为 `"prune"`、成功为 `"ripen"`、失败为 `"wither"`。
- 修剪与 `wither` 的关键区别：`wither` 是「培育失败」、`prune` 是「培育前被跳过」；两者都计为完成，但**只有** `wither` 才写 `WitherMessage`（`prune` 记录没有 WitherMessage）。
- `TestPlot_PruneMixed` 用 `seed > 3` 作谓词、`seed % 2` 作 wither 决定，二者正交，从而能确定性地覆盖三态共存。
- 这些用例统一使用 `WithTenders` 并发，计数断言基于 `atomic` 计数器，不依赖于严格串行执行。