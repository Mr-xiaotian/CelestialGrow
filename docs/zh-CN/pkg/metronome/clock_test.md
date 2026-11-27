# pkg/metronome/clock_test.go

> 📅 最后更新日期: 2026/11/26

`pkg/metronome/clock_test.go` 用真实时间去驱动 `Clock`（不使用假时钟），覆盖七类行为：构造校验、基础节拍序列、根 ticker 的周期过滤、运行期改周期的唤醒、启停幂等、根 ticker 错误处理器的优先级，以及**错误处理器自身 panic 后时钟仍存活**。测试大量依赖时序假设，但都留了数倍余量，因此默认运行稳定。

## 作用

- 锁定 `Clock` 的对外契约：`Seq` 从 1 开始、周期过滤在 `Clock` 层生效、`SetInterval` 必须在"下一拍"而非"旧周期到期"时生效。
- 锁定 `Stop` 的幂等性与"处理器 panic 不得让时钟停摆"这一可靠性承诺。
- 为 `deliver` 的两条错误路径（`OnTick` 返回 error、`HandleTickError` panic）提供回归保护。

## 测试清单

| 测试函数 | 覆盖行为 | 关键时序 |
|---------|---------|---------|
| `TestNewClockRejectsInvalidArguments` | 构造参数校验 | 无（纯同步） |
| `TestClockTicksRoot` | 基础触发 + `Seq` 从 1 递增 | 周期 10ms，3 拍，200ms 上限 |
| `TestClockRespectsRootTickPeriod` | 根 ticker 的 `TickPeriod` 过滤 | 周期 10ms，收 3 拍（实为 5 拍），250ms 上限 |
| `TestClockSetIntervalTakesEffectOnNextTick` | 改周期立即唤醒 | 200ms → 10ms，120ms 判定 |
| `TestClockIdempotentStartStop` | 重复 `Start` / `Stop` 返回 `nil` | 无（只有一个 10ms 周期） |
| `TestClockUsesRootErrorHandler` | 根实现 `ErrorHandler` 时优先调用它 | 周期 10ms，150ms 内轮询 |
| `TestClockSurvivesRootErrorHandlerPanic` | 处理器 panic 后时钟继续运行 | 周期 10ms，观察 40ms |

## 各测试详解

### `TestNewClockRejectsInvalidArguments`

- 两次构造：`NewClock(0, root)` 与 `NewClock(time.Millisecond, nil)`，都只断言 `err != nil`。
- **不校验错误文本**，因此错误消息措辞调整不会破坏测试；但"零/负周期"和"nil root"必须被拒绝。
- 边界提示：`interval = 0` 和 `nil` 是唯一两条构造期失败路径，正周期 + 非 nil root 永远成功。

### `TestClockTicksRoot`

```go
root := &channelTicker{ch: make(chan int, 4)}
clock, _ := NewClock(10*time.Millisecond, root)
```

- 启动后从 `root.ch` 收集 3 个 `Seq`，断言 `slices.Equal(got, []int{1, 2, 3})`。
- **验证两点**：`Seq` 首值为 1；正常情况下不丢拍、不重号。
- 时序假设：3 拍名义耗时 30ms，超时预算 200ms（≈6 倍余量）；`channelTicker` 的 channel 容量 4 > 3，`OnTick` 中的发送永不阻塞，因此不会反过来拖慢时钟。
- 收尾用 `defer` 调 `Stop()` 并检查错误，验证正常路径下 `Stop` 能在 5 秒内 join 成功。

### `TestClockRespectsRootTickPeriod`

- 根是 `periodChannelTicker{period: 2}`，即实现 `Periodic` 返回 2。
- 期望收到 `[]int{1, 3, 5}`——证明三点：
  1. `Clock` 在调用根 `OnTick` **之前**就做了 `shouldTick` 过滤；
  2. 被过滤掉的拍**仍消耗 `Seq`**（否则会是 `1, 2, 3`）；
  3. 过滤不会导致时钟停摆或补发。
- 时序：要拿到 3 次投递需经过 5 拍 ≈ 50ms，超时 250ms。

### `TestClockSetIntervalTakesEffectOnNextTick`

- 构造周期 200ms；`Start` 后先 `time.Sleep(20ms)`，确保 `run` 已经 `select` 在一个约 200ms 的 timer 上。
- 然后 `SetInterval(10 * time.Millisecond)`，记录起点 `start`，等待第一拍：
  - 若在 120ms 内收到 → 通过（说明唤醒生效，落点约为 `prevFire + 10ms`）；
  - 若 150ms 仍无 → `t.Fatal`；
  - 若收到但耗时 > 120ms → 也判失败（防止"刚好在旧 timer 的 200ms 处才触发"这种伪通过）。
- **这是 `wakeCh` 存在的唯一直接证据**：没有唤醒，200ms 的 timer 会让首拍落在 ~180ms 之后。
- 该测试只验证"不晚于 120ms"，不验证精确网格，因此对调度抖动宽容。

### `TestClockIdempotentStartStop`

- 连续两次 `Start`、两次 `Stop`，四次调用都必须返回 `nil`。
- 覆盖 `Start` 的"已运行直接返回"与 `Stop` 的"未运行直接返回"两条早退分支。
- 没有断言 goroutine 数量，因此"重复 Start 是否多起了一个循环"只能由 `Seq` 相关测试间接保证；本测试的职责仅是 API 幂等。

### `TestClockUsesRootErrorHandler`

- 根是 `clockHandlingProbe`：`OnTick` 恒返回 `errors.New("boom")`，`HandleTickError` 把错误追加到 `handled`。
- 启动后以 5ms 间隔轮询 `len(handled) == 0`，150ms 超时。
- 断言重点：**至少一次**错误被交给根的 `ErrorHandler`（而不是只打印日志）；不检查错误文本，也不要求恰好一次（10ms 周期在 150ms 内会产生多次）。
- 收尾 `Stop()` 并检查错误。
- ⚠️ 读侧 `len(handled)` 未加锁（`clockHandlingProbe.mu` 只保护写入路径），严格来说存在数据竞争；默认（非 `-race`）运行不受影响，若在 `-race` 下偶发报告，属于测试自身而非 `Clock` 的问题。

### `TestClockSurvivesRootErrorHandlerPanic`

- 先把 `log` 输出重定向到 `io.Discard`，避免默认处理器刷屏，并在 `defer` 中恢复。
- 根的 `OnTick` 返回错误，`HandleTickError` **直接 panic("handler boom")**。
- 运行 40ms（≈4 拍）后断言 `clock.IsRunning()` 为 `true`，再 `Stop()` 成功。
- 断言重点：
  - `IsRunning()` 为 `true` 只说明状态标志未被 `Stop` 改动，**真正的判据是测试进程仍然存活**：若 panic 逃出 `deliver`，整个 `go test` 二进制会崩溃，根本走不到断言；
  - `Stop` 能在 5 秒内正常 join（若时钟 goroutine 已死，`doneCh` 早已关闭，`Stop` 也会立即返回 `nil`，所以这一条只是辅助信号）。
- 时序假设：40ms 足够触发多次节拍，从而必然走到 panic 路径。

## 测试桩类型

| 类型 | 方法集 | 用途 |
|------|--------|------|
| `channelTicker` | `OnTick` 把 `event.Seq` 写入 `ch` | 最简单的节拍采集器，周期视为 1 |
| `periodChannelTicker` | `TickPeriod() = period` + `OnTick` 写 `ch` | 验证周期过滤 |
| `clockHandlingProbe` | `OnTick` / `HandleTickError` 由函数字段注入，`mu` 串行化回调 | 验证错误处理器优先级与 panic 路径 |

三者都是**指针接收者**，通过 `&channelTicker{...}` 注册；`channelTicker` 的 channel 必须带缓冲，否则 `OnTick` 会阻塞时钟 goroutine 并破坏时序断言。

## 运行方式

```bash
# 全部 Clock 测试
go test ./pkg/metronome/ -run 'TestClock|TestNewClock' -v

# 单个用例（定位时序问题时最有用）
go test ./pkg/metronome/ -run 'TestClockSetIntervalTakesEffectOnNextTick' -v
go test ./pkg/metronome/ -run 'TestClockSurvivesRootErrorHandlerPanic' -v

# 重复运行以暴露偶发时序问题
go test ./pkg/metronome/ -run 'TestClock' -count=20

# 显式检查数据竞争（注意上文提到的测试自身读写不同步）
go test ./pkg/metronome/ -run 'TestClock' -race
```

## 注意事项

- **测试使用真实时间**，不适合在极端负载的 CI 机器上与大量并行任务同跑；断言余量约为标称耗时的 5～6 倍，但仍非硬实时保证。
- **`log.SetOutput` 是全局状态**：`TestClockSurvivesRootErrorHandlerPanic` 临时替换后通过 `defer` 恢复。当前包内没有 `t.Parallel()`，因此不会与其他测试互相干扰。
- **未覆盖的路径**（阅读源码时的已知空白，不是缺陷清单）：
  - `Stop` 的 5 秒超时错误分支；
  - `SetInterval` 传入非正周期时的错误返回；
  - `DefaultInterval` 常量值本身；
  - 长耗时 `OnTick` 触发"跳过错拍、不追赶"的精确节拍序列（`skipped` 计算没有直接断言）；
  - `Clock` 停止后重新 `Start` 时 `Seq` 归零。
- **`defer` 中调用 `t.Fatalf`**（`TestClockTicksRoot` 等）会在 `Stop` 失败时终止当前测试 goroutine；它不会掩盖其他测试，但会让该用例的失败信息只显示 `Stop()` 的错误。
