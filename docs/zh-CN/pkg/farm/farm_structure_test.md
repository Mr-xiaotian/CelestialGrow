# pkg/farm/farm_structure_test.go

> 最后更新日期: 2026/09/01

## 作用

`farm_structure_test.go` 是 `pkg/farm` 中体量最大的测试文件，覆盖 `Farm` 在**非平凡拓扑**下的端到端行为，验证：

- 多源多汇的「1→2→1」菱形结构（`TestFarmStructure121`）
- 节点部分失败时的 `fruit` / `weed` 计数（`TestFarmStructure121PartialFailure`）
- 多个**互不连通**的子图并行运行（`TestFarmStructureDisconnectedComponents`）
- 多个 source 速度差异下的 fan-in 汇合（`TestFarmStructure21FaninDifferentSpeed`）

## 测试用例

### `TestFarmStructure121`

- 拓扑：`root`（source）→ `{midA, midB}` → `head`（sink）
- 50 个种子 (`0..49`) 输入 `root`；`midA` 输出 `seed*10+1`，`midB` 输出 `seed*10+2`。
- 期望：`head` 共收到 100 个 fruit，且对每个 `i ∈ [0,50)`，`i*10+1` 和 `i*10+2` 各出现恰好 1 次。
- 所有 plot 终态 `state == 2`。

### `TestFarmStructure121PartialFailure`

- 同一菱形拓扑，20 个种子（`0..19`）。
- 失败注入：
  - `root`：偶数 seed 返回 error → fruit/weed 各 10
  - `midB`：`seed > 10` 失败 → 5 fruit / 5 weed
  - `midA`：全部成功
- 期望：
  - `root.GetFruitNum() == 10` 且 `GetWeedNum() == 10`
  - `midA.GetFruitNum() == 10`（与 `root` 实际 fruit 数对齐）
  - `midB.GetFruitNum() == 5` / `GetWeedNum() == 5`
  - `head.GetFruitNum() == 15`（10 from `midA` + 5 from `midB`）
- 全部 plot 终态 `state == 2`。

### `TestFarmStructureDisconnectedComponents`

- 同一 `Farm` 内两个互不连通的子图：
  - 子图 A：`rootA` → `{midA1, midA2}`
  - 子图 B：`{rootB1, rootB2}` → `headB`
- 两个子图都输入 50 颗 seed。
- 期望：
  - `resultsA`（`midA1` / `midA2` 共同收集）含 50 个不同值，每个值计数为 2（来自 `midA1` 与 `midA2` 各一次）。
  - `resultsB`（`headB` 收集）含 100 个不同值，每个原始 seed 产生的 `seed*10+3` 与 `seed*10+4` 各 1 次。
- 验证 `Farm.SourceNodes` 能正确识别**两个** source SCC（`{rootA}` 与 `{rootB1, rootB2}`），并对每个 source 触发 `Seal()`。

### `TestFarmStructure21FaninDifferentSpeed`

- 拓扑：`{rootFast, rootSlow}` → `head`。
- `rootSlow` 单次 sleep 10ms，`rootFast` 不 sleep；二者都使用 `WithChanSize(50)`。
- 各 50 颗 seed。
- 期望：`head` 共收到 100 颗，每颗仅 1 次计数，且所有 plot 终态 `state == 2`。
- 验证多 source 速度差异下 fan-in 仍能正确完成（依赖 `Plot` 的 `chan` 缓冲与 `sprout` 的 `select` 调度）。

## 关键细节

- **多连通分量**：`TestFarmStructureDisconnectedComponents` 间接覆盖了 `SourceNodes` 会从**每个** Source SCC 取一个代表节点——这意味着 `Farm.Run` 会向 `rootA`、`rootB1`、`rootB2` 都发送 `Seal()`。
- **fan-out / fan-in 计数**：`counts[seed]` 来自 `head` 的 `cultivator`，被并发调用，因此测试使用 `sync.Mutex` 保护。
- **失败路由**：`Plot.bearWeed` 仅记录失败，不向下游转发；`head` 收到的 fruit 数与 `midA.GetFruitNum() + midB.GetFruitNum()` 严格相等，这一断言在 `TestFarmStructure121PartialFailure` 中被验证。
- **通道配置**：`WithChanSize` 在 `TestFarmStructure21FaninDifferentSpeed` 中显式放大，避免慢 source 阻塞快 source 写入。

## 关联源码

- `pkg/farm/farm.go`：`Run`、`SourceNodes`、`AddPlot`、`Connect`
- `pkg/farm/graph.go`：`SourceNodes`、`TarjanSCC`
- `pkg/plot/plot.go`：`GetFruitNum` / `GetWeedNum` / `GetState` / `sprout` / `tend` / `bearFruit` / `bearWeed`

## 运行方式

```bash
go test ./pkg/farm/ -run 'TestFarmStructure' -v
```
