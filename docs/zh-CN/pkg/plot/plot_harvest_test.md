# pkg/plot/plot_harvest_test.go

> 最后更新日期: 2026/09/01

`plot_harvest_test.go` 是 `plot` 包的「**结果快照**」测试集。它通过 `Plot.Run` 跑批后调用 `Plot.Harvest` 拉取每颗 seed 的最终状态（`success` / `failed` + 结果 JSON + 错误信息），验证：

- 全部失败、部分失败、全部成功 三种结果分布；
- `Counter` 与 `Plot.GetState` 在跑批结束时是否正确归位；
- 失败时错误消息能否原样落到 `LifecycleStatusRecord.ErrorMessage`。

## 测试辅助函数

```go
func mustHarvest[S any, F any](t *testing.T, plot *plot.Plot[S, F]) []persist.LifecycleStatusRecord
```

封装 `plot.Harvest()`：失败直接 `t.Fatalf`；返回生命周期状态记录切片。

```go
func indexStatusesByTask(records []persist.LifecycleStatusRecord) map[string]persist.LifecycleStatusRecord
```

以 `TaskJSON` 为键把 `[]LifecycleStatusRecord` 转成 map，便于按 seed 值做单点断言。

## 用例

### `TestPlot_AllError` —— 全部失败

- **cultivator**：`func(int) (string, error) { return "", errors.New("always fail") }`
- **配置**：`WithTends(2)`
- **seeds**：`[1, 2, 3, 4, 5]`

验证：

1. `mustHarvest` 返回 5 条记录；
2. 每条 `record.Status == "failed"`、`record.ErrorMessage == "always fail"`；
3. `plot.GetCompleted() == 5`；
4. `plot.GetState() == 2`（`done`）。

> 用途：保证 `bearWeed` 路径上 `SeedFailed` 生命周期被正确写入、`ErrorMessage` 不被吞掉。

### `TestPlot_PartialError` —— 部分失败

- **cultivator**：`seed % 2 == 0` → 失败；其余 → 返回 `seed*10`。
- **配置**：`WithTends(2)`
- **seeds**：`[1, 2, 3, 4, 5]`

验证：

1. `mustHarvest` 返回 5 条；
2. 用 `indexStatusesByTask` 按 `strconv.Itoa(seed)` 查每条记录：
   - 偶数 seed：`Status == "failed"` 且 `ErrorMessage == "even number error"`；
   - 奇数 seed：`Status == "success"` 且 `ResultJSON == strconv.Itoa(seed*10)`；
3. 成功计数 = 3、失败计数 = 2、`GetCompleted == 5`。

> 用途：保证 `bearFruit` 与 `bearWeed` 分流条件互斥，且 `TaskJSON`（即 seed 的 JSON 字符串）能与输入 seed 严格对齐。

### `TestPlot_AllSuccess` —— 全部成功

- **cultivator**：`func(int) (int, error) { return seed * 2, nil }`
- **配置**：`WithTends(3)`
- **seeds**：`[1, 2, 3, 4, 5]`

验证：

1. `mustHarvest` 返回 5 条；
2. 每条 `Status == "success"` 且 `ResultJSON == strconv.Itoa(seed*2)`；
3. `plot.GetState() == 2`（`done`）。

> 用途：覆盖「零失败」的纯成功路径，验证 `ResultJSON` 序列化与状态机收尾。

## 共同前置条件

- 测试包：`package plot_test`（黑盒测试），不直接访问 `Plot` 未导出字段；
- 不显式调用 `StartAsync` / `Seal` / `StopSpouts`，全部由 `Plot.Run` 内部完成；
- `Harvest` 依赖 `lifecycleSpout` 已被 `Run` 创建并绑定 `LifecycleRecordHandler`，因此可以走 `LoadStatuses` 接口拿到状态快照。

## 运行方式

```bash
go test ./pkg/plot/... -run TestPlot_ -v
```

或限定本测试文件：

```bash
go test ./pkg/plot -run "TestPlot_AllError|TestPlot_PartialError|TestPlot_AllSuccess" -v
```

## 注意事项

- `mustHarvest` 直接 `Fatalf` 失败；任何 `lifecycle handler does not support status queries` 或 `lifecycle spout is nil` 错误都会让测试整体失败。
- 三个用例都不验证日志文件内容，只验证生命周期 SQLite 快照；如需日志行为请参考 `pkg/persist` 的相关测试。
- `TestPlot_PartialError` 默认依赖「`TaskJSON == strconv.Itoa(seed)`」这一具体序列化结果；如果未来 `TaskJSON` 格式调整（例如改用 JSON 字符串），本测试需要同步更新。
