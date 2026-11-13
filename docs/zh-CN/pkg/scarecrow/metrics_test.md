# pkg/scarecrow/metrics_test.go

> 📅 最后更新日期: 2026/11/13

## 作用

`pkg/scarecrow/metrics_test.go` 覆盖 `MetricsWatcher` 的计数口径与快照接口：输入来源分类、结果计数与待完成数、状态与时间戳、批量视图、零值可用性。它是 [`metrics.md`](./metrics.md) 中字段计算表的可执行版本。

## 测试分布

| 测试 | 覆盖内容 |
|------|----------|
| `TestMetricsWatcherCountsInputs` | 外部输入 vs 上游输入；同时校验接收方 `UpstreamCounts` 与来源方 `DownstreamCounts` |
| `TestMetricsWatcherCountsResults` | 4 条输入 + 各 1 条成熟/枯萎/剪枝，校验 `Processed` 与 `Pending` |
| `TestMetricsWatcherTracksStatus` | `PlotStartEvent` → `running` 且 `EndTime` 为零值；`PlotEndEvent` → `stopped` 且 `EndTime` 更新 |
| `TestMetricsWatcherFarmView` | `GetPlots` 返回两个 Plot 的快照；未登记的 `ghost` 返回 `false` |
| `TestMetricsWatcherZeroValue` | `var metrics MetricsWatcher` 直接 `OnEvent` 可用（`plots` 懒初始化） |

## 关键断言与设计意图

### 输入来源：`TestMetricsWatcherCountsInputs`

```text
SeedInputEvent{Plot: "a", SeedID: 1}              // 外部注入
SeedInputEvent{Plot: "b", From: "a", SeedID: 2}   // a 投递给 b
```

| 断言 | 意图 |
|------|------|
| `a.ExternalInput == 1`、`a.UpstreamInput == 0`、`a.InputTotal == 1` | `From == ""` 才计外部输入 |
| `a.DownstreamCounts["b"] == 1` | 投递行为在**来源方**记账 |
| `b.ExternalInput == 0`、`b.UpstreamInput == 1`、`b.InputTotal == 1` | `UpstreamInput` 由映射求和得出 |
| `b.UpstreamCounts["a"] == 1` | 接收方按来源分别计数 |

这一测试同时验证了「双向记账」：同一条 `SeedInputEvent` 会写入两个 Plot 的存储格（接收方 `upstream` 与来源方 `downstream`），因此即使来源方从未启动，也会出现在 `GetPlots()` 中。

### 结果与待完成：`TestMetricsWatcherCountsResults`

- 连续 4 条外部输入后，分别发 1 条成熟、枯萎、剪枝。
- 断言 `(Succeeded, Failed, Skipped) == (1, 1, 1)`、`Processed == 3`、`InputTotal == 4`、`Pending == 1`——直接锁定 `Processed` 为三者之和、`Pending` 为输入减已处理的口径。
- 所有 `OnEvent` 的错误在此测试中被忽略（`_ =`），因为该路径不产生错误。

### 状态与时间：`TestMetricsWatcherTracksStatus`

- 启动后立刻读取快照：`Status == PlotRunning`、`StartTime` 等于事件时间、`EndTime.IsZero()` 为真——验证「未结束不设置结束时间」。
- 结束后：`Status == PlotStopped`、`EndTime` 等于事件时间，且 `StartTime` 不被覆盖（测试只读取了状态与 `EndTime`，但实现上二者独立字段）。
- 用 `time.Time.Equal` 而非 `==` 比较，规避单调时钟字段差异。

### 批量视图：`TestMetricsWatcherFarmView`

- 两个 Plot 各发一条启动事件，`GetPlots()` 长度必须为 2，且两者状态都是 `running`。
- 对 `"ghost"` 调 `GetPlot` 返回 `false`，验证「未登记不臆造数据」。

### 零值：`TestMetricsWatcherZeroValue`

- `var metrics MetricsWatcher` 未调用 `NewMetricsWatcher`，直接 `OnEvent` 后 `GetPlot("a")` 应返回 `ExternalInput == 1`。这验证 `plot()` 中 `m.plots == nil` 时的懒初始化分支。

## 运行方式

```bash
go test ./pkg/scarecrow -run 'TestMetricsWatcher' -v
```

本文件不涉及并发与 I/O，无需 `-race`；并发保护由实现中的 `RWMutex` 提供，但当前测试未并发调用。

## 注意事项

- **未覆盖 `Pending` 的 0 下界钳制**：所有测试都满足 `Processed <= InputTotal`，当事件流只有结果没有输入时的 `pending < 0 → 0` 分支没有测试；修改该逻辑时需自行补测。
- **未覆盖未知 `PlotStatus` 的 `String()` 默认分支**（返回 `not_started`），也未覆盖 `PlotStartEvent` 重复到达时的覆盖语义。
- 测试直接构造事件而不经过 `Hub`，因此不验证并发加锁行为；`GetPlot` 返回的快照是否可安全长期持有也只由实现保证，未在测试中断言。
- 断言使用完整结构体字段组合（如三元组 `1/0/1`），字段重命名会立刻编译失败，属于预期的连锁影响。
