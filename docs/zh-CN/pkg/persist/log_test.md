# pkg/persist/log_test.go

> 📅 最后更新日期: 2026/11/13

## 作用

`pkg/persist/log_test.go` 验证 `LogWatcher` 的两条核心行为：

1. **事件 → 文本映射**：每种 `scarecrow` 事件生成的日志级别与文案是否正确；
2. **级别过滤**：低于 `minLevel` 的事件是否被静默丢弃。

测试用包内构造函数 `newLogWatcher(handler, metrics, level)` 注入捕获型 handler，不写真实日志文件。

## 测试夹具

```go
type captureLogHandler struct {
    mu      sync.Mutex
    records []LogRecord
}

func (h *captureLogHandler) BeforeStart() error { return nil }
func (h *captureLogHandler) HandleRecord(record LogRecord) error { /* 加锁追加 */ }
func (h *captureLogHandler) AfterStop() error { return nil }
func (h *captureLogHandler) snapshot() []LogRecord { /* 加锁拷贝 */ }
```

- 实现 `funnel.RecordHandler[LogRecord]`，只记录不落盘。
- `sync.Mutex` 保护切片：`HandleRecord` 在 `Spout` 的后台 goroutine 中调用，`snapshot` 在测试 goroutine 中读取。

## 测试覆盖点

| 测试 | 覆盖能力 | 关键不变量 |
|------|----------|-----------|
| `TestLogWatcherTranslatesEvents` | 9 个事件 → 10 条日志，覆盖 Farm / Plot / Seed 全部映射 | 记录顺序、级别、文案与指标计数完全符合预期 |
| `TestLogWatcherFiltersLevels` | `minLevel = ERROR` 时投递 1 条 DEBUG + 1 条 ERROR | 只保留 ERROR 记录 |

## `TestLogWatcherTranslatesEvents`

### 驱动方式

```go
handler := &captureLogHandler{}
metrics := scarecrow.NewMetricsWatcher()
obs := newLogWatcher(handler, metrics, "DEBUG")
if err := obs.Start(); err != nil { t.Fatalf(...) }

for _, event := range events {
    if err := metrics.OnEvent(event); err != nil { t.Fatalf(...) } // 先喂指标
    if err := obs.OnEvent(event); err != nil { t.Fatalf(...) }
}
if err := obs.Stop(); err != nil { t.Fatalf(...) }
records := handler.snapshot()
```

- 最低级别设为 `DEBUG`，否则 `SeedInputEvent` 会被过滤，无法验证完整映射。
- **同一个事件先投给 `metrics`、再投给 `obs`**：`PlotEndEvent` 的计数从 `MetricsWatcher` 读取，顺序反了会读到旧快照。这也对应 `pkg/assembly` 中「指标观察者先注册」的真实顺序。

### 预期记录

`events` 依次为：`FarmStart` → `PlotStart` → `SeedInput` → `SeedReplant` → `SeedRipen` → `SeedWither` → `SeedPrune` → `PlotEnd` → `FarmEnd`，共产生 10 条记录（`FarmStart` 的 `Structure` 只有 1 行，额外贡献 1 条）：

| # | 级别 | 文案断言（`strings.Contains`） |
|:-:|------|-------------------------------|
| 0 | `INFO` | `Farm 'farm' start. Structure:` |
| 1 | `INFO` | `a -> b` |
| 2 | `INFO` | `Plot 'a' start with 2 tenders.` |
| 3 | `DEBUG` | `In 'a', Seed 1 input. [1*]` |
| 4 | `WARNING` | `attempt 1 withered: transient. Replanting. [1*]` |
| 5 | `SUCCESS` | `Seed 1 ripened. Fruit is 10. Use 1.00s. [1->2*]` |
| 6 | `ERROR` | `Seed 2 withered: boom. Use 1.00s. [3->4*]` |
| 7 | `INFO` | `Seed 3 pruned. Use 0.00s. [5->6*]` |
| 8 | `INFO` | `Plot 'a' end. Use 2.00s. 1 ripened, 1 withered, 1 pruned.` |
| 9 | `INFO` | `Farm 'farm' end. Use 3.00s.` |

- 断言采用「条数 + 逐条级别 + 子串包含」三段式：先 `len(records) != len(want)` 失败，再依次比较 `Level` 与 `Message`。
- 索引 8 同时验证 `MetricsWatcher` 集成：`SeedRipen` / `SeedWither` / `SeedPrune` 各 1 次，因此计数为 `1 ripened, 1 withered, 1 pruned`。
- 索引 3~7 的 `[seedID*]` / `[seedID->resultID*]` 后缀分别对应 `SeedID`、`FruitID`、`WeedID`、`PruneID` 字段。
- `SeedReplant` 的文案包含 `attempt 1`，且只有 `[1*]` 后缀（重试事件不产生新的事件 ID）。

## `TestLogWatcherFiltersLevels`

```go
obs := newLogWatcher(handler, scarecrow.NewMetricsWatcher(), "ERROR")
obs.Start()

obs.OnEvent(scarecrow.SeedInputEvent{Plot: "a", Seed: 1, SeedID: 1, Time: now}) // DEBUG，被丢弃
obs.OnEvent(scarecrow.SeedWitherEvent{Plot: "a", Seed: 1, SeedID: 1, WeedID: 2, Time: now}) // ERROR，保留

obs.Stop()
records := handler.snapshot()
// 断言: len(records) == 1 且 records[0].Level == "ERROR"
```

- `minLevel = levelOrder["ERROR"] = 5`，`DEBUG`（1）与 `INFO`（3）都会被过滤。
- `SeedWitherEvent` 未设置 `Err`，文案会渲染成 `<nil>`，但本测试只断言级别与条数，不依赖该文本。

## 运行方式

```bash
# 运行本文件
go test ./pkg/persist -run 'TestLogWatcher'

# 查看详细结果
go test ./pkg/persist -run 'TestLogWatcher' -v

# 运行整包
go test ./pkg/persist/...
```

## 注意事项

- **`Stop` 是同步点**：`Spout.Stop` 关闭通道并等待队列排空（最多 1s）后才 `snapshot`；不调用 `Stop` 会导致记录数不稳定。
- **`Start` 不可省略**：注入 handler 的 `BeforeStart` 是空实现，但消费 goroutine 仍由 `Start` 启动。
- **依赖指标投递顺序**：若把 `metrics.OnEvent` 放到 `obs.OnEvent` 之后，`PlotEnd` 的计数会偏小，索引 8 的断言将失败。
- **只覆盖翻译与过滤**：`LogRecordHandler` 的目录创建、文件打开、追加写入与关闭由源码与 [`log.md`](./log.md) 描述，本测试不触碰文件系统。
- **不断言 `FormatTime`**：测试使用 `time.Now()` 作为事件时间，不对格式化结果做硬编码断言；如需验证格式，应改用固定时间并断言 `2006-01-02 15:04:05` 形态。
