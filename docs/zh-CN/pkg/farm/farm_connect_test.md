# pkg/farm/farm_connect_test.go

> 最后更新日期: 2026/09/01

## 作用

`farm_connect_test.go` 覆盖 `pkg/farm/farm.go` 中**注册（`AddPlot`）**与**连接（`Connect`）**路径的语义正确性。测试文件以 `package farm_test` 的形式通过 `pkg/farm` 对外符号验证行为，是 `Farm` 公共 API 的「黑盒契约」。

## 测试用例

| 测试函数 | 验证点 |
| --- | --- |
| `TestFarmAddPlot` | 多个 plot 一次性注册；`PlotCount` / `HasPlot` / `GetPlot` 全部一致；注册后**未**建立任何边 |
| `TestFarmAddPlotDuplicateName` | 重复名称的 plot 第二次 `AddPlot` 返回 error |
| `TestFarmConnectHyperEdge` | `Connect` 对重复目标（如 `targetA` 出现两次）自动去重；`source` 正确连到所有 target，但反向边不存在 |
| `TestFarmConnectTypeMismatch` | 上游 `F` 与下游 `S` 类型不一致时 `Connect` 返回 error（透传 `ConnectTo` 的断言失败） |

## 关键断言

- `TestFarmAddPlot` 同时校验 `GetPlot` 拿到的指针与原 `plot` 一致——`Farm.plots` 是按指针保存的，不做值拷贝。
- `TestFarmConnectHyperEdge` 中重复出现的 `targetA` 不会导致 `source → targetA` 被建两次：内部 `uniquePlots` 在调用 `Connect` 之前完成去重。
- `TestFarmConnectTypeMismatch` 中上下游 `Plot[S, F]` 的 `F` 与 `S` 不同，`ConnectTo` 内的 `.(chan runtime.Payload[F])` 类型断言失败，因此 `Connect` 整体返回 error，且 `OrderGraph` 中不会留下 `AddEdge` 记录。

## 关联源码

- `AddPlot` 错误返回：`pkg/farm/farm.go` 中的 `AddPlot` / `requireRegistered`
- `Connect` 错误返回：`pkg/farm/farm.go` 中的 `Connect` / `uniquePlots` / `requireRegistered`
- 类型断言：`pkg/plot/plot.go` 中的 `Plot.ConnectTo`

## 运行方式

```bash
go test ./pkg/farm/ -run 'TestFarmAdd|TestFarmConnect' -v
```
