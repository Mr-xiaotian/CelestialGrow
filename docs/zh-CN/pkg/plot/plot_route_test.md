# pkg/plot/plot_route_test.go

> 📅 最后更新日期: 2026/09/24

`plot_route_test.go` 是 `RoutePlot` 的**结果快照**测试，只有一个用例：验证路由节点在 standalone 跑批后，会把每个 seed 成功记录为**一条** `ripen`，并把整张路由表原样持久化到 `FruitJSON`。

## 用例

### `TestRoutePlot_RunHarvest`

- **cultivator（路由函数）**：
  ```go
  func(seed int) (map[string]string, error) {
      return map[string]string{
          "left":  fmt.Sprintf("L%d", seed),
          "right": fmt.Sprintf("R%d", seed),
      }, nil
  }
  ```
- **配置**：`plot.NewRoutePlot("route_harvest", cultivator, plot.WithTenders(2))`
- **seeds**：`[]int{1, 2}`

验证：

1. `mustHarvest(t, route)` 返回 2 条状态记录；
2. 用 `indexStatusesBySeed` 以 `SeedJSON` 为键建立索引后：
   - `index["1"]`：`Status == "ripen"`，`FruitJSON == {"left":"L1","right":"R1"}`；
   - `index["2"]`：`Status == "ripen"`，`FruitJSON == {"left":"L2","right":"R2"}`。

> 覆盖要点：路由表的**整体**（而非单个 value）作为一次成功的结果被记录，说明 `RoutePlot` 的结果类型是 `map[string]Y` 且只对应一条 `ripen`。

## 共同前置条件

- 测试包：`package plot_test`（黑盒），不访问 `RoutePlot` 未导出字段；
- 依赖同包内其他测试文件提供的两个辅助函数（都定义在 `plot_harvest_test.go`）：
  - `mustHarvest(t *testing.T, plot harvestable)`：封装 `Harvest()` 并在出错时 `t.Fatalf`；`harvestable` 是测试内定义的局部接口 `interface{ Harvest() ([]persist.LifecycleStatusRecord, error) }`，因此 `Plot` / `RoutePlot` / `SplitPlot` 都能直接传入；
  - `indexStatusesBySeed(records)`：以 `record.SeedJSON` 为键建索引。
- 本用例**没有**连接任何下游：`route.yieldChans` 为空，`ripenSeed` 的投递循环不会执行；`Run` 依靠末尾的 `Seal()` 触发输入关闭后正常收尾。
- 因此本用例**不**验证 `AddDownstreamYieldNum` / `SeedInput` 等下游转发行为——那部分由 `pkg/farm` 的 `farm_route_test.go` 覆盖。

## 运行方式

```bash
go test ./pkg/plot -run "TestRoutePlot" -v
```

## 注意事项

- `FruitJSON` 由 `persist` 的 JSON 序列化得到，`map[string]string` 会被 `encoding/json` **按键名排序**输出为 `{"left":...,"right":...}`；断言写死了这一顺序，若序列化实现改为保留插入顺序，本测试需要同步调整。
- `indexStatusesBySeed` 的键是 `SeedJSON`（`int` seed 的 JSON 文本，如 `"1"`、`"2"`），不是格式化后的结构体字符串。
- 该用例只验证单 seed 的整表持久化，不验证「不同下游收到不同 yield」的投递效果；如需验证投递，请参考 `pkg/farm/farm_route_test.go`。
