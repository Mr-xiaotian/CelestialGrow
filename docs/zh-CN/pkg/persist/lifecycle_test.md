# pkg/persist/lifecycle_test.go

> 📅 最后更新日期: 2026/11/13

## 作用

`pkg/persist/lifecycle_test.go` 验证 `LifecycleWatcher` 的 **事件翻译**：`scarecrow` 事件经过 `OnEvent` 后，是否被正确转换成 `LifecycleRecord` 并投递到队列。测试不打开 SQLite，而是用包内构造函数 `newLifecycleWatcher(handler)` 注入捕获型 handler，绕开 `LifecycleRecordHandler` 的文件副作用。

## 测试夹具

```go
type captureLifecycleHandler struct {
    mu      sync.Mutex
    records []LifecycleRecord
}

func (h *captureLifecycleHandler) BeforeStart() error { return nil }
func (h *captureLifecycleHandler) HandleRecord(record LifecycleRecord) error { /* 加锁追加 */ }
func (h *captureLifecycleHandler) AfterStop() error { return nil }
func (h *captureLifecycleHandler) snapshot() []LifecycleRecord { /* 加锁拷贝 */ }
```

- 实现 `funnel.RecordHandler[LifecycleRecord]`，但**不做任何 IO**，只把记录追加到切片。
- `sync.Mutex` 保护 `records`：`HandleRecord` 在 `Spout` 的后台 goroutine 中执行，`snapshot` 在主测试 goroutine 中读取。
- `snapshot` 返回副本，避免测试断言期间被后台写入干扰。

> 注入自定义 handler 后，`DBPath()` 会返回空串（它只识别默认的 `*LifecycleRecordHandler`），因此本测试不涉及数据库路径。

## 测试覆盖点

| 测试 | 覆盖能力 | 关键不变量 |
|------|----------|-----------|
| `TestLifecycleWatcherTranslatesEvents` | 投递 7 个事件，覆盖 4 类种子结果事件 + 2 类被忽略事件 | 只产生 5 条记录；`Kind` / `InputEventID` / `CurrentEventID` / `ParentIDs` / JSON 载荷 / 时间戳全部符合映射规则 |

## `TestLifecycleWatcherTranslatesEvents`

### 驱动方式

```go
handler := &captureLifecycleHandler{}
obs := newLifecycleWatcher(handler)
if err := obs.Start(); err != nil { t.Fatalf(...) }

now := time.UnixMilli(1_700_000_000_123)
events := []scarecrow.Event{
    scarecrow.SeedInputEvent{Plot: "a", Seed: 1, SeedID: 1, Time: now},
    scarecrow.SeedRipenEvent{Plot: "a", Seed: 1, Fruit: 2, SeedID: 1, FruitID: 2, Time: now},
    scarecrow.SeedInputEvent{Plot: "b", From: "a", Seed: 2, SeedID: 3, Parents: []int{2}, Time: now},
    scarecrow.SeedWitherEvent{Plot: "b", Seed: 2, Err: errors.New("boom"), SeedID: 3, WeedID: 4, Time: now},
    scarecrow.SeedPruneEvent{Plot: "b", Seed: 9, SeedID: 5, PruneID: 6, Time: now},
    scarecrow.PlotStartEvent{Plot: "b", StartTime: now},
    scarecrow.SealMergeEvent{Plot: "b", ParentIDs: []int{7}, SealID: 8, Time: now},
}
for _, event := range events {
    if err := obs.OnEvent(event); err != nil { t.Fatalf(...) }
}

if err := obs.Stop(); err != nil { t.Fatalf(...) }
records := handler.snapshot()
if len(records) != 5 { t.Fatalf(...) }
```

- 先 `Start` 启动消费 goroutine，再逐个 `OnEvent`，最后 `Stop` 排空队列后才 `snapshot`——直接读切片会因异步消费而不稳定。
- 固定时间戳 `1_700_000_000_123` 毫秒，用来验证 `TS` 的毫秒精度。

### 断言内容

| 索引 | 来源事件 | `Kind` | `InputEventID` | `CurrentEventID` | 其它断言 |
|:----:|----------|--------|----------------|------------------|----------|
| 0 | `SeedInputEvent`（plot `a`） | `seed` | 1 | 1 | `PlotName == "a"`、`SeedJSON == "1"`、`ParentIDs == nil`、`TS ≈ 1700000000.123`（误差 `1e-6`） |
| 1 | `SeedRipenEvent` | `ripen` | 1 | 2 | `FruitJSON == "2"`、`ParentIDs == [1]` |
| 2 | `SeedInputEvent`（plot `b`，`Parents: [2]`） | `seed` | 3 | 3 | `PlotName == "b"`、`SeedJSON == "2"`、`ParentIDs == [2]` |
| 3 | `SeedWitherEvent` | `wither` | 3 | 4 | `WitherMessage == "boom"`、`WitherType != ""` |
| 4 | `SeedPruneEvent` | `prune` | 5 | 6 | — |

- `PlotStartEvent` 与 `SealMergeEvent` 被观察者显式忽略，因此 7 个事件只产生 5 条记录——这是「忽略分支」的间接断言。
- 索引 2 覆盖「上游投递」场景：`From` 字段不影响持久化，父边来自 `Parents`；`Seed` 为 `2` 时 `SeedJSON` 正好是 `"2"`。
- 索引 3 覆盖错误字段：`WitherType` 由 `%T` 生成，只断言非空（具体类型名随 Go 版本稳定但不值得硬编码），`WitherMessage` 断言错误文本。
- 索引 4 的 `Seed` 字段刻意填 `9`，但 prune 记录不写种子 JSON，说明状态推进不依赖该字段。

## 运行方式

```bash
# 运行本文件
go test ./pkg/persist -run 'TestLifecycleWatcher'

# 查看详细结果
go test ./pkg/persist -run 'TestLifecycleWatcher' -v

# 运行整包
go test ./pkg/persist/...
```

## 注意事项

- **`Stop` 是同步点**：`Spout.Stop` 关闭通道并等待消费循环排空（最多 1s），之后 `snapshot` 才能看到全部记录；若只调用 `OnEvent` 不 `Stop`，断言会因记录尚未消费而随机失败。
- **`Start` 不可省略**：`OnEvent` 本身只向 `Inlet` 投递，消费依赖 `Start` 启动的 goroutine。
- **不覆盖落库逻辑**：`captureLifecycleHandler` 不写 SQL，`HandleRecord` 的 Kind 路由、`InsertLifecycleEvent` / `Promote*` 的调用顺序均由 [`sqlite_test.md`](./sqlite_test.md) 与源码保证；本测试只锁定「事件 → 记录」的字段映射。
- **不覆盖错误分支**：`unsupported lifecycle operation`、`生命周期 sqlite 未初始化` 等错误未在此文件断言；`OnEvent` 对未识别事件返回 `nil`，因此也没有错误路径可测。
- **时间断言用 `math.Abs` 容差**：`TS` 是 `float64(UnixMilli())/1000`，浮点比较必须留容差（测试用 `1e-6`）。
