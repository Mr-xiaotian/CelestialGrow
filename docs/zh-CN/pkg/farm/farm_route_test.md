# pkg/farm/farm_route_test.go

> 📅 最后更新日期: 2026/09/24

## 作用

`farm_route_test.go` 以 `package farm_test` 覆盖 `RoutePlot` 在 `Farm` 中的端到端行为：`Farm.Connect` 建立 `route → {left, right}` 的多下游连接后，`Run` 是否能按路由表把 yield 定向转发到对应下游。

## 测试重点

- **定向转发**：同一个上游 seed 按 `map[string]Y` 路由表的 key 分派到不同下游。
- **下游 seed 计数**：`left` / `right` 的 `GetSeedNum` 来自上游 yield 计数器（本用例中二者不本地播种子），验证 `ConnectTo` 的跨边 yield 计数接线。
- **未连接目标静默跳过**：路由到未连接的下游不影响其余路由项，也不阻塞上游。
- **顺序无关断言**：并发转发下不假设下游接收顺序。

## 测试用例

| 测试函数 | 验证点 |
| --- | --- |
| `TestFarmRunRoutePlot` | `route`（`int → map[string]string`）按奇偶分派：偶数 → `left`、奇数 → `right`。4 颗 seed 后 `route.GetFruitNum() == 4`，`left` / `right` 各 `GetSeedNum() == 2`，且实收 seed 集合分别为 `{even-2, even-4}` 与 `{odd-1, odd-3}` |
| `TestFarmRunRoutePlotSkipsUnconnectedTarget` | 路由表同时含已连接的 `left` 与未注册的 `ghost`；只有 `left` 收到 `kept-1`，`left.GetSeedNum() == 1`，`ghost` 被静默跳过 |

## 关键细节

- **并发收集加锁**：两个用例都通过 `sync.Mutex` 保护下游 `received` map / 切片，因为 `RoutePlot.ripenSeed` 在多个 tender 协程上并发执行。
- **`assertSeeds` 辅助函数**：先对实收集合排序再逐项比对，避免依赖下游并发接收顺序。
- **依赖上游 yield 计数**：`left` / `right` 没有本地 `Seed`，其 `GetSeedNum` 完全由上游登记到 `upstreamYieldCounter` 的计数器贡献；这是对「per-edge 下游 yield 计数」重构的直接回归。
- **`WithTenders(2)`**：路由节点与下游都显式配置 2 个 tender，提高并发覆盖。

## 关联源码

- `pkg/farm/farm.go` 的 `Connect` / `Run`
- `pkg/farm/graph.go` 的 `SourceNodes` / `AddEdge`
- `pkg/plot/plot_route.go` 的 `RoutePlot.ripenSeed`（跳过未连接下游）
- `pkg/plot/plot_base.go` 的 `ConnectTo`（yield 计数器双向接线）
- `pkg/plot/counter.go` 的 `GetSeedNum` / `GetFruitNum`

## 运行方式

```bash
go test ./pkg/farm/ -run 'TestFarmRunRoutePlot' -v
```
