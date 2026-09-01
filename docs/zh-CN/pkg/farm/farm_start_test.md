# pkg/farm/farm_start_test.go

> 最后更新日期: 2026/09/01

## 作用

`farm_start_test.go` 覆盖 `Farm.Run` 在最简单「线性」拓扑下的端到端行为：单一源、单一汇、`Run` 注入多颗种子，验证：

- 全部种子被处理
- 下游结果与上游变换一致
- 所有 plot 退出后状态为 `done`（`int == 2`）

## 测试用例

### `TestFarmRunLinear`

- 创建 `root`（`seed * 2`，`WithTends(2)`）和 `head`（收集 seed，`WithTends(2)`）。
- `Connect(root → head)`。
- `Run` 时给 `root` 注入 `{1, 2, 3}`。
- 期望：
  - `head` 共收到 3 个 seed，排序后为 `[2, 4, 6]`。
  - `root.GetState() == 2`、`head.GetState() == 2`（即 `Plot.notifyFinish` 已触发）。
- `head` 内部使用 `sync.Mutex` 保护共享 `results` 切片。

## 关键细节

- **并发安全**：测试中下游 `head` 的 cultivator 对 `results` 加锁；这与 `pkg/plot` 中 `tend` 协程并发派发模型一致。
- **状态机校验**：`GetState() == 2` 说明 `Plot.sprout` 已经退出 `WaitAsync`，验证 `Farm.Run` 的 `WaitAsync` 顺序在所有 plot 完成后才返回。
- **不验证 source seal**：`sourceNodes` 仅 1 个（`root`），其 seal 在 `Run` 末尾通过 `Seal()` 注入；测试通过最终状态间接确认 `Seal` 不会导致「上游未到齐就强终止」回归。

## 关联源码

- `pkg/farm/farm.go` 的 `Run` 流程
- `pkg/plot/plot.go` 的 `Seed` / `Seal` / `sprout` / `tend`
- `pkg/plot/constant.go` 中 `sourceInput = "__input__"`

## 运行方式

```bash
go test ./pkg/farm/ -run 'TestFarmRunLinear' -v
```
