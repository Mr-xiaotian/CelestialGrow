# pkg/reporter/snapshot_test.go

> 📅 最后更新日期: 2026/11/26

## 作用

`pkg/reporter/snapshot_test.go` 验证 `SnapshotTicker` 的投影与转发行为，并单独锁定 `NodeStatus` 的 JSON 字段契约。它不发起网络请求：`MetricsView` 与 `SnapshotSink` 都由本地探针替代。

## 测试分布

| 测试 | 覆盖内容 |
|------|----------|
| `TestSnapshotTickerPushesProjectedSnapshot` | `PlotMetrics → NodeStatus` 的字段映射、`TickPeriod` 返回值、sink 收到快照 |
| `TestSnapshotTickerPropagatesSinkError` | sink 错误原样传播（可 `errors.Is`） |
| `TestNewSnapshotTickerRejectsInvalidArguments` | nil MetricsView、nil sink、负数周期 |
| `TestNodeStatusJSONContract` | 序列化后 11 个 JSON 键全部存在 |
| `TestToStatusSnapshotPreservesNodeSet` | 投影保留全部节点键（含零值指标） |

测试辅助：

```go
type stubMetricsView struct{ plots map[string]scarecrow.PlotMetrics }
func (m stubMetricsView) GetPlots() map[string]scarecrow.PlotMetrics // 复制计数字典后返回

type snapshotSinkProbe struct {
    snapshot map[string]NodeStatus
    err      error
}
func (s *snapshotSinkProbe) PushSnapshot(snapshot map[string]NodeStatus) error // copySnapshot 后保存并返回 s.err

func sortedKeys[V any](m map[string]V) []string
```

`stubMetricsView` 在 `GetPlots` 中逐项 `copyIntMap`，模拟 `scarecrow.MetricsWatcher` 的「返回不可变快照」语义；`snapshotSinkProbe` 用生产代码的 `copySnapshot` 保留数据，避免断言时被后续修改影响。

## 关键断言与设计意图

### 投影映射：`TestSnapshotTickerPushesProjectedSnapshot`

输入指标：

```text
stage_a: Status=PlotStopped, StartTime=Unix(10,0), EndTime=Unix(20,0)
         InputTotal=7, Processed=6, Pending=1
         Succeeded=4, Failed=1, Skipped=1
         UpstreamCounts={"root":3}, DownstreamCounts={"leaf":2}
```

构造 `NewSnapshotTicker(metricsView, sink, 2)` 后：

| 断言 | 意图 |
|------|------|
| `TickPeriod() == 2` | 显式周期不被默认值覆盖 |
| `sink.snapshot["stage_a"]` 存在 | 节点名作为 map 键完整保留 |
| `Status == int(scarecrow.PlotStopped)` | `PlotStatus` 被转成 Web 端整数码（`2`） |
| `TasksInput == 7`、`TasksProcessed == 6`、`TasksPending == 1` | 三个核心计数正确映射 |
| `StartTime == 10.0`、`EndTime == 20.0` | `time.Time` 被转换为 Unix 浮点秒（整秒时无小数） |

注意 `time.Unix(10, 0)` 的 `Unix()` 为 10、`Nanosecond()` 为 0，因此期望值精确等于 `10.0`/`20.0`，可以直接用 `!=` 比较。`Succeeded`/`Failed`/`Skipped` 与上下游计数在本测试中未被断言（虽然输入里给了值）。

### 错误传播：`TestSnapshotTickerPropagatesSinkError`

```go
want := errors.New("boom")
ticker, _ := NewSnapshotTicker(stubMetricsView{plots: map[string]scarecrow.PlotMetrics{}}, &snapshotSinkProbe{err: want}, 1)
if err := ticker.OnTick(metronome.TickEvent{Seq: 1}); !errors.Is(err, want) {
    t.Fatalf("OnTick() error = %v, want wrap %v", err, want)
}
```

- 指标为空也不影响：`OnTick` 先投影出空 map，再调用 sink。
- `errors.Is` 能命中，说明 `OnTick` **没有重新包装或替换** sink 错误（当前是直接 `return`）。
- 失败信息里的 "want wrap" 措辞与实际行为略有偏差：实现并未 `%w` 包装，而是原样返回；用 `errors.Is` 断言对两者都成立。

### 构造校验：`TestNewSnapshotTickerRejectsInvalidArguments`

| 输入 | 预期拒绝原因 |
|------|-------------|
| `(nil, &snapshotSinkProbe{}, 1)` | nil MetricsView |
| `(stubMetricsView{}, nil, 1)` | nil sink |
| `(stubMetricsView{}, &snapshotSinkProbe{}, -1)` | 负数周期 |

只判断 `err == nil`，不比对错误文本；也没有覆盖 `period == 0 → 1` 的默认替换（该分支只能通过 `TickPeriod()` 间接观察，测试未做）。

### JSON 契约：`TestNodeStatusJSONContract`

构造一个填满全部字段的 `NodeStatus`，`json.Marshal` 后反序列化为 `map[string]map[string]any`，逐一检查键是否存在：

```text
status, tasks_input, tasks_processed, tasks_pending,
tasks_succeeded, tasks_failed, tasks_skipped,
upstream_counts, downstream_counts, start_time, end_time
```

- 这是**字段名回归测试**：任何 JSON tag 改名、删除字段都会让 Web 端解析失败，此测试会立即报警。
- 只断言「键存在」，不比对值，也不检查是否多出字段；值的映射正确性由前一个测试覆盖。
- 使用 `float64`/`map` 解码到 `any` 是刻意的：它模拟 Web 端 JSON 解析后的形状，而不是依赖 Go 结构体。

### 节点集合：`TestToStatusSnapshotPreservesNodeSet`

```go
snapshot := toStatusSnapshot(map[string]scarecrow.PlotMetrics{
    "a": {},
    "b": {},
})
// sortedKeys(snapshot) == []string{"a", "b"}
```

- 两个指标都是零值（`Status=PlotNotStarted`、时间为零、计数为 0），仍必须各生成一个 `NodeStatus`——投影不能因为"看起来没数据"就丢节点。
- 用排序后的键比较，规避 map 遍历顺序的不确定性。
- 未断言零值时间是否被映射为 `0`（这是 `toStatusSnapshot` 的另一条分支）。

## 运行方式

```bash
# 仅本文件的测试
go test ./pkg/reporter -run 'TestSnapshotTicker|TestNodeStatusJSONContract|TestToStatusSnapshot' -v

# 整个包
go test ./pkg/reporter -v
```

本文件无网络、无并发，不需要 `-race`；但同包的 `push_test.go` 会启动本地 HTTP 服务，跑整包时请确保回环地址可用。

## 注意事项

- **未覆盖零值时间分支**：`StartTime.IsZero()` / `EndTime.IsZero()` 时应输出 `0`，5 个测试都没有断言这一点（`TestToStatusSnapshotPreservesNodeSet` 只检查键）。
- **未覆盖上下游计数**：`UpstreamCounts` / `DownstreamCounts` 的映射在本文件没有任何断言；`push_test.go` 也只检查了空邻接表编码。
- **未覆盖默认周期**：`period == 0 → 1` 没有用例。
- **未覆盖空 map 的 JSON 形状**：`toStatusSnapshot` 本身不保证 `upstream_counts` 编码为 `{}`；这依赖 `copySnapshot`/`copyIntMap`，且没有在快照层面断言（`push_test.go` 只断言了 `edges` 的空切片）。
- **未覆盖接口的 typed-nil**：构造函数只做 `== nil` 判断，传入 `(*stub)(nil)` 不会被拒绝；测试未固定这一行为。
