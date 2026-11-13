# pkg/scarecrow/event_test.go

> 📅 最后更新日期: 2026/11/13

## 作用

`pkg/scarecrow/event_test.go` 验证**事件字段是否足以重建运行指标**，以及事件在分发过程中是否保持完整载荷与血缘 ID。它不测 `MetricsWatcher` 本身，而是用一个独立的事件探针（`metricsProbe`）复刻指标口径，从而把「事件定义」与「指标实现」解耦验证。

## 测试重点

| 测试 | 覆盖内容 |
|------|----------|
| `TestSyntheticLifecycleFeedsMetrics` | 通过 `Hub` 依次注入 17 条覆盖全部 11 种事件的人工事件流，断言 Farm / Plot 生命周期、输入来源分类、结果计数、上下游流量与待完成数 |
| `TestEventsCarryPayloadAndIDs` | 断言 `SeedRipenEvent` / `SeedWitherEvent` / `SeedReplantEvent` / `SeedInputEvent` 的载荷、错误、时间戳、`Attempt`、`Parents` 与 ID 互异性在分发后保持不变 |

## 测试辅助类型

| 类型 | 作用 |
|------|------|
| `nodeProbe` | 单个 Plot 的探针状态：起止标志与时间、`external`、`upstream`、`downstream`、`succeeded` / `failed` / `skipped` |
| `metricsProbe` | 探针集合，实现 `Watcher`；`OnEvent` 用 `type switch` 按 `MetricsWatcher` 的口径累积 |

`nodeProbe.pending()` 返回 `external + Σupstream - (succeeded + failed + skipped)`，与 `PlotMetrics.Pending` 的算法同源（但没有 0 下界钳制，因为测试流是完整的）。

## 关键断言与设计意图

### 合成生命周期：`TestSyntheticLifecycleFeedsMetrics`

事件顺序（`a`、`b` 为两个 Plot，`a → b` 有一条边）：

```text
FarmStart
→ PlotStart(a) → PlotStart(b)
→ SeedInput(a, 外部 ×2) → SeedReplant(a) → SeedRipen(a) → SeedPrune(a)
→ SeedInput(b, From=a) → SeedRipen(b)
→ SeedInput(b, 外部 ×1) → SeedWither(b)
→ SealInput(a) → SealMerge(b)
→ PlotEnd(a) → PlotEnd(b) → FarmEnd
```

验证点：

| 断言 | 说明 |
|------|------|
| `graphStarted && graphEnded` | Farm 首尾事件都被观察到，且分发不丢事件 |
| `a.started && a.ended`、`b` 同理 | Plot 生命周期事件完整 |
| `a.external == 2` | 仅 `From == ""` 的两条 `SeedInputEvent` 计入外部输入 |
| `a` 结果计数 `(succeeded, failed, skipped) == (1, 0, 1)` | `SeedReplantEvent` 不计入任何结果计数 |
| `a.downstream["b"] == 1` | 上游投递在**来源方**记为下游流量 |
| `b.external == 1`、`b.upstream["a"] == 1` | 接收方按 `From` 区分外部输入与上游输入 |
| `b` 结果计数 `(1, 1, 0)` | 成熟与枯萎各自计数 |
| 两节点 `pending() == 0` | 输入与结果一一对应，没有遗漏事件 |

Hub 使用 `WithErrorHandler(func(err error) { t.Errorf(...) })`，因此任何观察者错误都会直接让测试失败——这同时验证了「人工事件流不会触发 Hub 错误路径」。

### 载荷与 ID：`TestEventsCarryPayloadAndIDs`

- 用 `WatcherFunc` 收集事件到 `[]Event`，再逐条做**值类型断言**（`got[0].(SeedRipenEvent)`）——验证事件以结构体值（而非指针）分发。
- `success.Seed` 与 `seed`（匿名结构体值）比较、`success.Fruit == "fruit"`：验证 `any` 载荷原样传递。
- `SeedID != FruitID`、`WeedID != SeedID`：验证生产者必须为下游事件分配新 ID。
- `errors.Is(fail.Err, boom)`、`errors.Is(retry.Err, boom)`：验证 `error` 未被包装或丢失。
- `!success.Time.Equal(...)` 使用 `time.Equal` 比较而不是 `==`，避免 `time.Time` 内部单调时钟字段导致的误判。
- `input.Parents == [2]` 且 `input.From == "a"`：验证投递种子的血缘与来源同时保留。

## 运行方式

```bash
go test ./pkg/scarecrow -run 'TestSyntheticLifecycleFeedsMetrics|TestEventsCarryPayloadAndIDs' -v
```

不带 `-race` 也能通过；本文件不涉及并发，并发分发由 [`hub_test.md`](./hub_test.md) 覆盖。

## 注意事项

- 测试直接构造事件结构体，绕过 `pkg/plot` 的生产逻辑；因此它验证的是**事件契约能否支撑指标**，而不是生产方的取值是否合理（如 `Elapsed` 精度、`SeedRepr` 截断）。
- 新增事件类型时，`metricsProbe.OnEvent` 的 `type switch` 不会报编译错误（`Event` 是封闭接口，但 `switch` 可省略分支），需要人工确认新事件是否应影响探针状态。
- 修改事件字段名会同时破坏本文件与 `pkg/persist` 的测试，属于预期的连锁影响。
