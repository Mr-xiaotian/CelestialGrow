# pkg/metronome/hub_test.go

> 📅 最后更新日期: 2026/11/26

`pkg/metronome/hub_test.go` 覆盖 `Hub` 的四组职责：**分发**（顺序、周期过滤、嵌套）、**结构校验**（自环、间接环、重复注册）、**错误隔离**（error、panic、ticker 处理器优先、默认处理器 panic）、**生命周期编排**（正序启动、逆序停止、失败回滚、运行期增删）。所有测试都直接同步调用 `hub.OnTick(...)`，**不依赖 `Clock` 与真实时间**，因此完全确定性、无睡眠、无超时。

## 作用

- 把"注册顺序 = 分发顺序 = 启动顺序，停止逆序"这一核心契约固化为断言。
- 证明单个 ticker 的失败不会波及同轮其他 ticker，且错误可通过 `errors.Is` 穿透到原始错误。
- 证明运行期 `AddTicker` / `RemoveTicker` 在 Hub 已启动时会立即联动 `Lifecycle`。

## 测试清单

| 测试函数 | 主题 | 断言要点 |
|---------|------|---------|
| `TestHubFansOutInRegistrationOrder` | 分发顺序 | 三个 `TickerFunc` 依次记录 `a, b, c` |
| `TestHubFiltersByTickerPeriod` | 周期过滤 | 周期 2 收到 `1,3`；周期 1 收到 `1,2,3,4` |
| `TestHubNestsChildHub` | 层级分发 | 父 Hub 的一次 `OnTick` 触发子 Hub 内的 ticker 一次 |
| `TestHubRejectsCyclicRegistration` | 环检测 | 自注册失败；`a→b` 成功；`b→a` 失败 |
| `TestHubRejectsDuplicateRegistration` | 判重 | 同一指针实例第二次注册失败 |
| `TestHubIsolatesTickerError` | 错误隔离 | 健康 ticker 仍执行；恰上报 1 个可 `errors.Is` 的错误 |
| `TestHubRecoversTickerPanic` | panic 隔离 | 健康 ticker 仍执行；上报信息含 `kaboom` |
| `TestHubUsesTickerErrorHandlerBeforeDefaultReporter` | 处理器优先级 | ticker 的 `HandleTickError` 被调用，Hub 默认处理器不被调用 |
| `TestHubSurvivesDefaultErrorHandlerPanic` | 兜底 | 默认处理器 panic 后仍继续分发 |
| `TestHubLifecycleStartStopOrder` | 生命周期顺序 | `start:a, start:b, stop:b, stop:a` |
| `TestHubStartRollbackOnFailure` | 启动回滚 | `start:a, start:b, stop:a`，`Start` 返回错误 |
| `TestHubAddTickerStartsImmediatelyWhenRunning` | 运行期增 | 已启动 Hub 上 `AddTicker` 立即 `start:a` |
| `TestHubRemoveTickerStopsImmediatelyWhenRunning` | 运行期删 | `start:a, stop:a` |

## 各测试详解

### 分发与过滤

#### `TestHubFansOutInRegistrationOrder`

- 用 `WithErrorHandler(func(error) {})` 静默默认处理器（本测试不会产生错误，属于防御性配置）。
- 注册三个 `TickerFunc`（`tag` 为 `a/b/c`），每个在互斥锁保护下追加到 `got`。
- `hub.OnTick(TickEvent{Seq: 1})` 后断言 `[]string{"a", "b", "c"}`。
- **隐含验证**：`TickerFunc` 不可比较，`sameTicker` 不判重，因此三个函数适配器可以共存；若换成同一个 `*struct` 指针，第二个注册会被拒绝。
- 共享切片 `got` 加锁只是为了表达"分发是同步的"，实际上没有并发。

#### `TestHubFiltersByTickerPeriod`

- 两个 `periodProbe`：`odd`（周期 2）、`all`（周期 1），注册顺序 `odd` 在前。
- 连续 `OnTick` 4 次，`Seq` 分别为 1..4（**手工构造事件，不经过 Clock**）。
- 断言 `odd → [1,3]`、`all → [1,2,3,4]`，即过滤使用 `(Seq-1)%period == 0`，周期 2 命中**奇数拍**而非偶数拍。
- 边界价值：这是最容易写错的上车点（若用 `Seq%period`，期望会变成 `2,4`）。
- 该测试未调用 `Start`，说明**分发不要求生命周期已启动**。

#### `TestHubNestsChildHub`

- `child` 内注册一个自增 `calls` 的 `TickerFunc`；`parent.AddTicker(child)`。
- `parent.OnTick(TickEvent{Seq: 1})` 后 `calls == 1`。
- 验证 `Hub` 自身满足 `Ticker`，可作为父节点的子项；也间接说明分发同步、不重复投递。

### 结构校验

#### `TestHubRejectsCyclicRegistration`

```go
a.AddTicker(a) // 自环 → error
a.AddTicker(b) // 成功
b.AddTicker(a) // a 已在 b 的子树中 → error
```

- 只断言 `err == nil / != nil`，不校验错误文本；因此"cannot add a hub to itself"与"cyclic hub registration rejected"的措辞可独立演进。
- 顺序关键：必须先在 `a→b` 成功后，`b→a` 才能构成间接环。

#### `TestHubRejectsDuplicateRegistration`

- 同一个 `*periodProbe` 注册两次，第二次必须失败。
- 依赖 `sameTicker` 对可比较指针类型做实例判等；换成 `TickerFunc` 或含 slice 的值类型则不会失败（见 `ticker.md`）。

### 错误隔离

#### `TestHubIsolatesTickerError`

- 注册顺序刻意是"先坏后好"：坏的返回 `boom`，好的自增 `calls`。
- 断言：`calls == 1`（坏的不中断分发）、`len(reported) == 1`（恰好上报一次）、`errors.Is(reported[0], boom)`（`%w` 包装可穿透）。
- 若 `deliver` 改成不包装直接 `fmt.Errorf("%v")`，`errors.Is` 会失败——这是对错误链的硬约束。

#### `TestHubRecoversTickerPanic`

- 第一个 ticker `panic("kaboom")`，第二个健康。
- 断言健康 ticker 仍被调用，且上报错误文本包含 `kaboom`（`strings.Contains`，不比对完整格式）。
- 意义：panic 被 `deliver` 的 `recover` 转成错误，而不是让测试进程崩溃。

#### `TestHubUsesTickerErrorHandlerBeforeDefaultReporter`

- `handlingProbe` 实现 `OnTick`（返回错误）与 `HandleTickError`（记录到 `handled`）。
- Hub 配置了 `WithErrorHandler`（记录到 `reported`）。
- 断言 `len(handled) == 1` 且 `len(reported) == 0`：ticker 自己的处理器**完全吞掉**错误，不再走 Hub 默认处理器。

#### `TestHubSurvivesDefaultErrorHandlerPanic`

- `WithErrorHandler` 传入的函数直接 `panic("handler boom")`；`log` 输出被重定向到 `io.Discard` 并在 `defer` 恢复。
- 注册"必失败"与"健康"两个 ticker，`OnTick` 后健康 ticker 的 `calls == 1`。
- 说明 `report` 的 `recover` 生效：默认处理器崩溃不会中断本轮分发，错误退化为日志。
- 测试未断言日志内容（已被 discard），只断言控制流继续。

### 生命周期

#### `TestHubLifecycleStartStopOrder`

- 两个 `lifecycleProbe`（`a`、`b`）共享一个 `lifecycleRecorder`。
- `Hub.Start()` → `Hub.Stop()`，期望调用序列 `start:a, start:b, stop:b, stop:a`。
- 同时锁定"`Start` 正序、`Stop` 逆序"两条规则；`lifecycleProbe.OnTick` 恒返回 `nil`，本测试不关心分发。

#### `TestHubStartRollbackOnFailure`

- `a` 正常启动，`b` 的 `startErr = errors.New("boom")`。
- `hub.Start()` 必须返回错误；调用序列期望 `start:a, start:b, stop:a`——即 `b` 失败后**立刻逆序回滚**已启动的 `a`。
- 未断言错误文本、也未断言 `started` 标志；因此"失败后 `started` 仍为 false、可重试"这一点没有直接覆盖。

#### `TestHubAddTickerStartsImmediatelyWhenRunning`

- 先 `hub.Start()`（此时没有任何 ticker，`started = true`），再 `AddTicker(a)`。
- 期望序列 `start:a`：运行期新增的 `Lifecycle` 项被立即启动。
- `defer hub.Stop()` 会追加 `stop:a`，但断言发生在 `Stop` 之前，因此不受影响。

#### `TestHubRemoveTickerStopsImmediatelyWhenRunning`

- 先 `AddTicker(a)`（Hub 未启动，不调用 `Start`），再 `Start()`（`start:a`），再 `RemoveTicker(a)`。
- 期望 `start:a, stop:a`：摘除后立即停止。
- 与上一个测试互补，覆盖"注册时未启动 / 运行时才停止"的时序组合。

## 测试桩类型

| 类型 | 关键点 | 说明 |
|------|--------|------|
| `periodProbe` | `mu` 保护 `seqs`，`snapshot()` 返回副本 | 实现 `Periodic`，用于过滤与判重测试；`mu` 是为"可能被时钟 goroutine 调用"预留的防御，在当前同步测试中并非必需 |
| `handlingProbe` | 函数字段注入 `onTick` / `onHandle`，**无锁** | 实现 `ErrorHandler`；同步调用下无需加锁 |
| `lifecycleRecorder` | `mu` + `calls`，`add` / `snapshot` | 记录并快照生命周期调用序列，快照返回副本避免别名 |
| `lifecycleProbe` | `name`、`recorder`、`startErr`、`stopErr` | 同时实现 `Ticker` 与 `Lifecycle`；`Start`/`Stop` 先记录再返回预设错误 |

`lifecycleProbe.stopErr` 在当前测试中未被使用，可视为预留字段（停止错误聚合路径尚未覆盖）。

## 并发与时序假设

- **全部同步**：`Hub.OnTick` 在测试 goroutine 中直接调用，`Lifecycle` 的 `Start`/`Stop` 也同步执行。因此 `var calls int` 这类裸计数器是安全的，`periodProbe.mu` 只是表达"生产环境可能并发"。
- **没有真实时钟**：`TickEvent{Seq: n}` 由测试手工构造，`FiredAt` 为零值——`Hub` 的分发逻辑不读取 `FiredAt`，所以不影响断言。
- **没有 `t.Parallel()`**：`log.SetOutput` 的全局替换（`TestHubSurvivesDefaultErrorHandlerPanic`）不会与其他测试冲突。
- **顺序敏感**：多个断言依赖注册顺序，因此新增测试时不要在既有注册链中间插入 ticker 而不更新期望序列。

## 运行方式

```bash
# 全部 Hub 测试
go test ./pkg/metronome/ -run 'TestHub' -v

# 按主题过滤
go test ./pkg/metronome/ -run 'TestHub(FansOut|Filters|Nests)' -v
go test ./pkg/metronome/ -run 'TestHub(Rejects|Lifecycle|Start|AddTicker|RemoveTicker)' -v

# 单个用例
go test ./pkg/metronome/ -run 'TestHubStartRollbackOnFailure' -v

# 重复执行（这些测试是确定性的，重复仍应全绿）
go test ./pkg/metronome/ -run 'TestHub' -count=50

# 竞态检测
go test ./pkg/metronome/ -run 'TestHub' -race
```

## 注意事项

- **断言风格**：顺序用 `slices.Equal`；错误链用 `errors.Is`（依赖 `%w`）；panic 文本用 `strings.Contains`。修改错误包装方式时，前两者是硬约束、第三者只要求保留原始 panic 值。
- **未覆盖的公开 API**（源码存在但本测试文件未直接验证）：
  - `Contains`（以及它对 `TickerFunc` 永远返回 `false` 的行为）；
  - 泛型 `Find[T]`（`pkg/assembly` 的测试间接覆盖）；
  - `TickPeriod` / `SetTickPeriod` / `WithTickPeriod` 的取值与校验；
  - `AddTicker(nil)`、非法周期的错误分支；
  - `RemoveTicker` 未注册项与 `nil` 的错误分支；
  - `Stop` 时多个子项停止失败的 `errors.Join` 聚合，以及 `stopReversed` 的逆序错误收集；
  - `Start` 回滚本身也失败时的 `errors.Join(startErr, stopErr)` 分支；
  - 分发快照与并发 `AddTicker`/`RemoveTicker` 的交错（"摘除后仍可能收到一拍在途节拍"）。
- **`TestHubFansOutInRegistrationOrder` 中的 `tag := tag`** 是循环变量捕获的显式拷贝；在 Go 1.22+ 已非必需，但保留不影响语义。
- **不要把这些测试改成并发**：多个桩类型依赖"调用发生在同一 goroutine"这一前提（`handlingProbe` 无锁、`calls` 是裸 `int`），改成 `t.Parallel()` + 真实时钟会引入竞争。
