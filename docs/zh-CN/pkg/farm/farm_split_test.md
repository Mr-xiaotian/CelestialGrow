# pkg/farm/farm_split_test.go

> 📅 最后更新日期: 2026/09/24

## 作用

`farm_split_test.go` 以 `package farm_test` 覆盖 `SplitPlot` 在 `Farm` 中的端到端行为：上游 seed 被拆分成多个下游 yield 后，`Farm.Run` 是否把全部元素逐一转发给下游。

## 测试重点

- **一对多拆分转发**：一次 `SplitPlot` 成功产出多个下游 yield，下游 seed 数应为拆分元素之和。
- **计数语义区分**：`SplitPlot.GetFruitNum` 按「输入 seed 数」计数（每次拆分记 1 个 fruit），而非按拆分元素数；下游 `head.GetSeedNum` 才反映元素总数。
- **空拆分结果**：拆分函数返回 `nil` 切片时不向下游转发任何内容。

## 测试用例

| 测试函数 | 验证点 |
| --- | --- |
| `TestFarmRunSplitPlot` | `split`（`string → []int`，按逗号拆分）与 `head`（`int → int`，`seed*10`）。输入 `{"1,2,3", "4,5"}` 后：`split.GetFruitNum() == 2`（2 次成功拆分）、`head.GetSeedNum() == 5`、`head.GetFruitNum() == 5` |

## 关键细节

- **拆分函数覆盖空输入**：`split` 对空串返回 `nil, nil`，用于覆盖「拆分结果为空」分支（不下发 yield）。
- **数字解析错误**：拆分过程中 `strconv.Atoi` 失败会返回 error，从而走 `SplitPlot` 的 withered 路径，本用例输入均为合法整数，用于验证纯成功路径。
- **`head` 的 fan-in**：`head` 使用 `WithTenders(2)`，会并发处理来自 `split` 的多条 yield，因此 `head.GetSeedNum` 依赖上游 yield 计数器的正确登记。

## 关联源码

- `pkg/farm/farm.go` 的 `Connect` / `Run`
- `pkg/plot/plot_split.go` 的 `SplitPlot.ripenSeed`（逐元素发送下游 yield）
- `pkg/plot/counter.go` 的 `GetSeedNum` / `GetFruitNum` / `AddDownstreamYieldNum`

## 运行方式

```bash
go test ./pkg/farm/ -run 'TestFarmRunSplitPlot' -v
```
