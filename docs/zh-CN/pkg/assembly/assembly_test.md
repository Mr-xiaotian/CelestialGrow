# pkg/assembly/assembly_test.go

> 📅 最后更新日期: 2026/11/13

本文件是 `pkg/assembly` 的端到端装配测试，采用外部测试包 `assembly_test`（只通过公开 API 访问，不触碰未导出字段）。两个测试函数都以 `starters` 表驱动，对 `StartPlotResources` 与 `StartFarmResources` 各跑一遍，确保两个入口的行为一致。

测试的核心验证链路是：**创建 Hub → 装配资源 → 向 Hub 发事件 → Stop → 从 Hub 取回生命周期观察者 → 按数据库路径回读状态快照**。因此它覆盖了 Hub 注册、事件分发、SQLite 落盘与 `Stop` 幂等。

## 测试覆盖点

| 测试 | 覆盖能力 | 关键不变量 |
|------|----------|-----------|
| `TestStartResourcesAssembleLifecycle` | 两个入口的完整装配 + 事件落盘 + 重复 Stop + 运行后查询 | 装配成功返回非 nil `Resources`；`SeedInputEvent` 经 Hub 写入 SQLite；`Stop` 连续调用两次不 panic；`scarecrow.Find` 能取回 `*persist.LifecycleWatcher`；`DBPath()` 非空；快照状态为 `seed`、`SeedJSON` 为 `"42"` |
| `TestStartResourcesRejectNilHub` | 两个入口的 nil 边界 | `hub == nil` 时返回非 nil error 且 `Resources` 为 nil |

测试中每个用例都带独立子测试名（`StartPlotResources` / `StartFarmResources`），失败时能直接定位到入口。

## 关键测试细节

### 共享表驱动夹具

```go
type startFunc func(hub *scarecrow.Hub, logLevel string) (*assembly.Resources, error)

var starters = []struct {
	name  string
	start startFunc
}{
	{"StartPlotResources", assembly.StartPlotResources},
	{"StartFarmResources", assembly.StartFarmResources},
}
```

- `startFunc` 显式写出两个入口的共同签名，把入口当作值传递；若将来两个入口签名分叉，这里会先编译失败。
- `starters` 是包级变量，两个测试函数共用。新增装配入口时只需在此追加一行，两个测试都会自动覆盖。
- 两个入口当前都转发到 `startResources`，该写法实际验证的是「两个入口没有分叉」。

### `TestStartResourcesAssembleLifecycle`

对每个入口执行：

```go
hub := scarecrow.NewHub(scarecrow.WithErrorHandler(func(err error) {
	t.Errorf("unexpected watcher error: %v", err)
}))

resources, err := starter.start(hub, "DEBUG")
if err != nil {
	t.Fatalf("%s() = %v", starter.name, err)
}

now := time.Now()
if err := hub.OnEvent(scarecrow.SeedInputEvent{
	Plot: "node", Seed: 42, SeedRepr: "42", SeedID: 1, Time: now,
}); err != nil {
	t.Fatalf("OnEvent() = %v", err)
}

resources.Stop()
resources.Stop() // 幂等
```

要点：

1. **Hub 的错误处理器直接记为测试失败**：`Hub.OnEvent` 对观察者故障只走 `ErrorHandler` 且始终返回 nil，因此必须借助处理器捕获「观察者处理事件时报错」；否则观察者故障会被静默吞掉。
2. **日志级别传 `DEBUG`**：`SeedInputEvent` 在 `LogWatcher` 中被映射为 `DEBUG` 级日志；传 `DEBUG` 可保证该事件不会被最低级别过滤（本测试并不校验日志文件内容）。
3. **`Stop` 连续调用两次**：验证 `Resources.stopOnce`。底层的 `funnel.Spout.Stop` 会 `close` 队列通道，直接重复调用会 `close of closed channel` panic，因此这一行实际是在守护幂等实现。
4. **先 Stop 再查询**：`Stop` 会等待消费协程处理完队列并关闭 SQLite；随后 `LoadLifecycleStatusesFromPath` 重新按路径打开数据库文件读取，避免读到未刷盘的数据。

查询与断言：

```go
lifecycle, ok := scarecrow.Find[*persist.LifecycleWatcher](hub)
if !ok {
	t.Fatal("lifecycle watcher is not registered")
}
dbPath := lifecycle.DBPath()
if dbPath == "" {
	t.Fatal("lifecycle database path is empty")
}

records, err := persist.LoadLifecycleStatusesFromPath(dbPath, "node")
if err != nil {
	t.Fatalf("LoadLifecycleStatusesFromPath() = %v", err)
}
if len(records) != 1 {
	t.Fatalf("records = %d, want 1", len(records))
}
if records[0].Status != "seed" || records[0].SeedJSON != "42" {
	t.Fatalf("record = %#v, want seed/42", records[0])
}
```

- `scarecrow.Find` 在 Hub 及其嵌套 Hub 中查找第一个 `*persist.LifecycleWatcher`，验证观察者确实被注册到了**传入的 hub**（Plot 级直接注册、Farm 级同样注册在 Farm Hub 上）。
- `DBPath()` 非空说明 `LifecycleWatcher.Start` 已经执行到 `BeforeStart` 并创建了 SQLite 文件；配合 `Find` 成功，可确认该观察者既已启动又已注册。
- `len(records) == 1` 验证该 plot 只有一条输入事件对应的状态快照（快照以输入事件 ID 为主键做 upsert），同时也说明事件确实被消费并落盘。
- `Status == "seed"` 是因为只发送了 `SeedInputEvent`，没有后续 ripen / wither / prune 事件；`SeedJSON == "42"` 来自 `persist` 对 `Seed` 字段的 JSON 序列化（`int` 42 → `42`）。

### `TestStartResourcesRejectNilHub`

```go
resources, err := starter.start(nil, "INFO")
if err == nil {
	t.Fatalf("%s(nil) = nil, want error", starter.name)
}
if resources != nil {
	t.Fatalf("%s(nil) resources = %#v, want nil", starter.name, resources)
}
```

- 只断言「有错误、资源为 nil」两个契约，不匹配具体错误文案，因此错误信息调整不会破坏测试。
- 从实现看，该路径在创建观察者之前就返回，因此不会产生目录或协程副作用；测试本身只断言「有错误 + 资源为 nil」这一返回值契约。

## 运行方式

```bash
# 运行本包全部测试
go test ./pkg/assembly/...

# 仅运行本文件
go test ./pkg/assembly -run 'TestStartResources'

# 查看两个入口的子测试
go test ./pkg/assembly -run 'TestStartResources' -v
```

## 注意事项

- **会留下运行时产物**：测试不设置工作目录，`LifecycleRecordHandler` / `LogRecordHandler` 按相对路径创建 `lifecycles/<日期>/grow_lifecycle(<时间>).sqlite3` 与 `logs/grow_log(<日期>).log`，位置是执行 `go test` 时的工作目录（通常是 `pkg/assembly/`）。测试没有清理逻辑，这些目录已由 `.gitignore` 忽略。
- **未覆盖的路径**：
  - 日志观察者的内容（是否真的写入文件、级别过滤是否正确）没有断言；
  - 指标观察者的聚合结果（`GetPlot` / `GetPlots`）没有断言；
  - 启动失败的各类回滚分支（`lifecycle.Start` / `log.Start` / `AddWatcher` 失败）没有覆盖，需要注入失败 handler 或不可写目录才能触发；
  - `Resources` 内部两个观察者的注册顺序、观察者数量没有显式断言，`Find` 只能证明生命周期观察者存在。
- **`Stop` 的错误被丢弃**：测试调用 `resources.Stop()` 不接收返回值，因为该方法本身无返回值；底层刷盘超时也不会让测试失败。
- **错误处理器会让观察者故障立即失败**：`WithErrorHandler` 中调用 `t.Errorf` 只影响当前子测试；如果观察者 panic，Hub 会把它包装成错误交给该处理器，同样会被捕获。
- **测试依赖真实文件系统与 SQLite 驱动**：`modernc.org/sqlite` 需可用；工作目录不可写或磁盘满时，`Start` 会返回错误并使第一个测试失败。
