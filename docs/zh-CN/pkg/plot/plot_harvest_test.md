# pkg/plot/plot_harvest_test.go

> 📅 最后更新日期: 2026/09/24

`plot_harvest_test.go` 是 `plot` 包的「**结果快照**」测试集。它通过 `Plot.Run` 跑批后调用 `Plot.Harvest`，拉取每颗 seed 的最终生命周期状态（`ripen` / `wither` + `FruitJSON` + `WitherMessage`），验证：

- 全部失败、部分失败、全部成功三种结果分布；
- `Counter` 与 `GetState` 在跑批结束时是否正确归位；
- 失败时的错误消息能否原样落到 `LifecycleStatusRecord.WitherMessage`。

本文件还向同包的其它测试文件（`plot_retry_test.go`、`plot_route_test.go`、`plot_split_test.go`）导出两个辅助函数。

## 测试辅助函数

```go
type harvestable interface {
	Harvest() ([]persist.LifecycleStatusRecord, error)
}

func mustHarvest(t *testing.T, plot harvestable) []persist.LifecycleStatusRecord
```

`harvestable` 是测试内定义的局部接口，只要求实现 `Harvest()`。因此 `Plot`、`RoutePlot`、`SplitPlot` 都能直接传入 `mustHarvest`——它们都通过内嵌 `basePlot` 获得同一个 `Harvest` 方法。`mustHarvest` 出错时直接 `t.Fatalf`，否则返回状态记录切片。

```go
func indexStatusesBySeed(records []persist.LifecycleStatusRecord) map[string]persist.LifecycleStatusRecord
```

以 `record.SeedJSON` 为键把记录切片转成 map，便于按 seed 值做单点断言（`int` seed 的 JSON 文本即 `"1"`、`"2"` 等）。

## 用例

### `TestPlot_AllError` —— 全部失败

- **cultivator**：`func(seed int) (string, error) { return "", errors.New("always fail") }`
- **配置**：`plot.NewPlot("test_all_error", cultivator, plot.WithTenders(2))`
- **seeds**：`[]int{1, 2, 3, 4, 5}`

验证：

1. `mustHarvest` 返回 5 条记录；
2. 每条 `record.Status == "wither"` 且 `record.WitherMessage == "always fail"`；
3. `plot.GetCompleted() == 5`；
4. `int(plot.GetState()) == 2`（`done`）。

> 用途：保证 `basePlot.witherSeed` 路径上 `SeedWither` 生命周期被正确写入、错误消息不被吞掉。

### `TestPlot_PartialError` —— 部分失败

- **cultivator**：`seed % 2 == 0` → 返回 `(0, errors.New("even number error"))`；否则返回 `(seed*10, nil)`。
- **配置**：`plot.NewPlot("test_partial_error", cultivator, plot.WithTenders(2))`
- **seeds**：`[]int{1, 2, 3, 4, 5}`

验证：

1. `mustHarvest` 返回 5 条记录；
2. 用 `indexStatusesBySeed` 按 `strconv.Itoa(seed)` 查每条记录：
   - 偶数 seed：`Status == "wither"` 且 `WitherMessage == "even number error"`；
   - 奇数 seed：`Status == "ripen"` 且 `FruitJSON == strconv.Itoa(seed*10)`；
3. 成功计数 = 3、失败计数 = 2、`GetCompleted() == 5`。

> 用途：保证 `ripenSeed` 与 `witherSeed` 的分流条件互斥，且 `SeedJSON` 能与输入 seed 严格对齐。

### `TestPlot_AllSuccess` —— 全部成功

- **cultivator**：`func(seed int) (int, error) { return seed * 2, nil }`
- **配置**：`plot.NewPlot("test_all_success", cultivator, plot.WithTenders(3))`
- **seeds**：`[]int{1, 2, 3, 4, 5}`

验证：

1. `mustHarvest` 返回 5 条记录；
2. 每条 `Status == "ripen"` 且 `FruitJSON == strconv.Itoa(seed*2)`；
3. `int(plot.GetState()) == 2`（`done`）。

> 用途：覆盖「零失败」的纯成功路径，验证 `FruitJSON` 序列化与状态机收尾。

## 共同前置条件

- 测试包：`package plot_test`（黑盒测试），不直接访问 `Plot` 未导出字段；
- 不显式调用 `StartAsync` / `Seal` / `StopSpouts`，全部由 `Plot.Run` 内部完成；
- `Harvest` 依赖 `Run` 已创建 `lifecycleSpout` 并绑定 `LifecycleRecordHandler`，因此可以走 `LoadStatuses` 接口拿到状态快照；
- 三个用例的 cultivator 都不返回「瞬时错误后成功」，因此不受 `maxRetries` 默认值（`1`）影响——失败用例是**确定性失败**，成功用例是**一次成功**。

## 运行方式

```bash
go test ./pkg/plot -run "TestPlot_AllError|TestPlot_PartialError|TestPlot_AllSuccess" -v
```

或一次性跑本文件全部 `TestPlot_` 前缀用例：

```bash
go test ./pkg/plot -run "TestPlot_" -v
```

## 注意事项

- `mustHarvest` 直接 `t.Fatalf` 失败；任何 `plot %q lifecycle spout is nil` 或 `lifecycle handler does not support status queries` 错误都会让测试整体失败。
- 三个用例都只验证生命周期 SQLite 快照，不验证日志文件内容；如需日志行为请参考 `pkg/persist` 的相关测试。
- 状态字符串以源码为准：成功为 `"ripen"`、失败为 `"wither"`（由 `pkg/persist` 的生命周期表口径决定）。若该口径调整，本文件与 `plot_retry_test.md` 中提到的断言都需同步更新。
- `indexStatusesBySeed` 的键是 `SeedJSON`（seed 的 JSON 文本），不是格式化后的结构体字符串，因此断言里用 `strconv.Itoa(seed)` 而不是 `fmt.Sprintf("%+v", seed)`。
