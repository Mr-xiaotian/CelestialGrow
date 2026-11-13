# pkg/farm/farm_start_test.go

> 📅 最后更新日期: 2026/11/13

## 作用

`farm_start_test.go` 覆盖 `Farm.Run` 在最简单「线性」拓扑下的端到端行为：单一源、单一汇，`Run` 内部分别通过 `InjectTasks` 注入种子、通过 `InjectTerminations` 终止 source，验证：

- 全部种子被处理
- 下游结果与上游变换一致
- 所有 plot 退出后状态为 `done`（`int == 2`）

## 测试用例

### `TestFarmRunLinear`

- 创建 `root`（`seed * 2`，`WithTenders(2)`）和 `head`（收集 seed，`WithTenders(2)`）。
- `Connect(root → head)`。
- `Run` 时给 `root` 注入 `{1, 2, 3}`，日志级别传 `"INFO"`。
- 期望：
  - `head` 共收到 3 个 seed，排序后为 `[2, 4, 6]`。
  - `root.GetState() == 2`、`head.GetState() == 2`（即 `notifyFinish` 已触发）。
- `head` 内部使用 `sync.Mutex` 保护共享 `results` 切片。

## 关键细节

- **并发安全**：测试中下游 `head` 的 cultivator 对 `results` 加锁；这与 `pkg/plot` 中 `tend` 协程并发派发模型一致。
- **状态机校验**：`GetState() == 2` 说明 `sprout` 已退出且 `WaitAsync` 已返回，验证 `Farm.start` 的等待顺序。
- **仅覆盖成功路径**：`Run` 在注入失败时才走终止兜底；本用例所有种子类型匹配，因此验证的是正常「注入种子 → 终止 source → 等待」链路。
- **source seal**：`sourceNodes` 只有 `root`，`Run` 自动对它 `Seal()`；测试通过最终状态间接确认不会出现「上游未到齐就强终止」回归。

## 关联源码

- `pkg/farm/farm.go` 的 `Run` / `start` / `InjectTasks` / `InjectTerminations`
- `pkg/plot/plot.go` 的 `Seed` / `Seal` / `sprout` / `tend`
- `pkg/plot/constant.go` 中 `sourceInput = "__input__"`

## 运行方式

```bash
go test ./pkg/farm/ -run 'TestFarmRunLinear' -v
```
