# pkg/plot/plot_split_test.go

> 📅 最后更新日期: 2026/09/24

`plot_split_test.go` 是 `SplitPlot` 的**结果快照**测试，只有一个用例：验证拆分节点在 standalone 跑批后，会把每个 seed 记录为**一条** `ripen`，并把完整的拆分结果切片持久化到 `FruitJSON`——包括「拆出 0 个元素」的边界情况。

## 用例

### `TestSplitPlot_RunHarvest`

- **splitter**：
  ```go
  func(seed int) ([]string, error) {
      fruits := make([]string, 0, seed)
      for i := 0; i < seed; i++ {
          fruits = append(fruits, fmt.Sprintf("%d-%d", seed, i))
      }
      return fruits, nil
  }
  ```
- **配置**：`plot.NewSplitPlot("split_harvest", splitter, plot.WithTenders(2))`
- **seeds**：`[]int{0, 2}`

验证：

1. `mustHarvest(t, split)` 返回 2 条状态记录；
2. 用 `indexStatusesBySeed` 以 `SeedJSON` 为键建立索引后：
   - `index["0"]`（`seed = 0`，循环不执行 → 空切片）：`Status == "ripen"`，`FruitJSON == "[]"`；
   - `index["2"]`（`seed = 2` → `["2-0","2-1"]`）：`Status == "ripen"`，`FruitJSON == "[\"2-0\",\"2-1\"]"`。

> 覆盖要点：
> 1. 拆分出的元素数量可以是 0，此时仍然记 1 条 `ripen`（`AddFruitNum(1)` 与 `len(fruits)` 无关）；
> 2. 整份切片（而非单个元素）作为一次成功的结果被记录。

## 共同前置条件

- 测试包：`package plot_test`（黑盒），不访问 `SplitPlot` 未导出字段；
- 依赖同包内其他测试文件提供的两个辅助函数（都定义在 `plot_harvest_test.go`）：
  - `mustHarvest(t *testing.T, plot harvestable)`：封装 `Harvest()` 并在出错时 `t.Fatalf`；`harvestable` 是测试内定义的局部接口 `interface{ Harvest() ([]persist.LifecycleStatusRecord, error) }`；
  - `indexStatusesBySeed(records)`：以 `record.SeedJSON` 为键建索引。
- 本用例**没有**连接任何下游：`split.yieldChans` 为空，`ripenSeed` 的下游投递循环不会执行；`Run` 依靠末尾的 `Seal()` 触发输入关闭后正常收尾。
- 因此本用例**不**验证「拆出的每个元素都变成一颗下游 yield」以及 `AddDownstreamYieldNum(len(fruits))` 的计数效果——那部分由 `pkg/farm` 的 `farm_split_test.go` 覆盖。

## 运行方式

```bash
go test ./pkg/plot -run "TestSplitPlot" -v
```

## 注意事项

- `fruits` 使用 `make([]string, 0, seed)` 创建，因此 `seed = 0` 时是**非 nil 的空切片**，JSON 序列化为 `[]` 而不是 `null`；如果 splitter 改为返回 `nil`，本断言需要相应改为 `null`。
- `indexStatusesBySeed` 的键是 `SeedJSON`（`int` seed 的 JSON 文本），不是格式化后的结构体字符串。
- 该用例只验证单 seed 的整片持久化，不验证下游实际收到的元素个数与顺序。
