# pkg/persist/sqlite_test.go

> 最后更新日期: 2026/09/01

## 作用

`pkg/persist/sqlite_test.go` 是 `pkg/persist` 包中**唯一**的测试文件，它只针对 [`sqlite.md`](./sqlite.md) 中描述的 SQLite 存储层做端到端验证，不涉及 [`lifecycle.md`](./lifecycle.md) 的 `LifecycleRecordHandler` / `LifecycleInlet` 与 [`log.md`](./log.md) 的日志落盘。三个测试函数都使用 `t.TempDir()` 拿到独立的临时文件作为 `OpenLifecycleSQLite` 的入口，确保测试之间互不影响且无副作用。

> 当前文件**没有**针对 `LifecycleRecordHandler.HandleRecord` 路由（`seed` / `fruit` / `weed`）与 `LogInlet` / `LogRecordHandler` 的单元测试；如后续追加，按 `pkg/funnel` Spout 单 goroutine 调度的特性，应当用内存 channel + 同步收口的方式驱动。

## 测试覆盖点

| 测试 | 覆盖能力 | 关键不变量 |
|------|----------|-----------|
| `TestLifecycleSQLiteEventRoundTrip` | 打开 DB → 写 seed + 父边 → 回读事件与父边 | `events` 主键可写；`event_parents` 多对多边可写；`LoadLifecycleEvent` / `LoadLifecycleEventParents` 行为对称 |
| `TestLifecycleSQLiteStatusRoundTrip` | 写两条事件 → `UpsertLifecycleStatus(pending)` → `PromoteLifecycleStatusSuccess` → 回读 | 状态快照可 Upsert；`PromoteLifecycleStatusSuccess` 只刷字段、保留 `task_json` / `plot` |
| `TestLifecycleSQLiteStatusPairs` | 多 plot 多事件混插 → 多次 Upsert + 一次 success + 一次 failed → 按 plot 维度查询 | `LoadLifecycleStatuses` 按 `ts, input_event_id` 升序；`plot` 过滤准确；`Status` / `ResultJSON` / `ErrorType` / `ErrorMessage` 字段都正确落地 |

## 关键测试细节

### 共享夹具与清理

```go
dbPath := filepath.Join(t.TempDir(), "lifecycle.sqlite3")
db, err := OpenLifecycleSQLite(dbPath)
if err != nil { t.Fatalf("OpenLifecycleSQLite() error = %v", err) }
defer db.Close()
```

- 每个测试独立申请一个 `t.TempDir()`，再由 SQLite 端创建 `lifecycle.sqlite3`。
- `defer db.Close()` 在测试函数返回前关闭数据库，触发 WAL 落盘；测试结束后 `t.TempDir()` 会被自动清理，无需手动 `os.Remove`。
- 由于 `OpenLifecycleSQLite` 内部会触发 PRAGMA + 建表脚本，**测试同时验证了连接配置与 schema 创建路径**。

### `TestLifecycleSQLiteEventRoundTrip`

```go
InsertLifecycleEvent(db, LifecycleEventRecord{EventID: 1, EventType: "seed", Plot: "source", TS: 1.0}, nil)
InsertLifecycleEvent(db, LifecycleEventRecord{EventID: 2, EventType: "fruit", Plot: "source", TS: 2.0}, []int{1})

loadedEvent, _ := LoadLifecycleEvent(db, 2)
parentIDs, _ := LoadLifecycleEventParents(db, 2)
```

- 写一条没有父边的 `seed` 事件（`parentIDs == nil`），写一条有 1 个父边的 `fruit` 事件。
- 回读断言：`loadedEvent.EventType == "fruit"`、`parentIDs == []int{1}`。
- 验证了「事件本体 + 父边」在同一事务中写入并能独立读出。

### `TestLifecycleSQLiteStatusRoundTrip`

```go
UpsertLifecycleStatus(db, LifecycleStatusRecord{
    InputEventID: 1, CurrentEventID: 1, TaskJSON: `{"value":"alpha"}`,
    Plot: "stage_a", Status: "pending", ResultJSON: "null", TS: 1.0,
})
PromoteLifecycleStatusSuccess(db, 1, 3, `{"ok":true}`, 3.0)

loadedStatus, _ := LoadLifecycleStatus(db, 1)
// 断言: CurrentEventID == 3, Status == "success", ResultJSON == `{"ok":true}`
```

- 走完「`pending` 状态 → `success` 晋升」全链路。
- 验证 `PromoteLifecycleStatusSuccess` **只**改 `current_event_id` / `status` / `result_json` / `ts`，不破坏 `task_json` 与 `plot`（`task_json` 仍然为 `{"value":"alpha"}`）。
- 同时验证 `LoadLifecycleStatus` 能读出 `Status == "success"`。

### `TestLifecycleSQLiteStatusPairs`

```go
// stage_a: event 1(seed) -> 2(fruit), event 3(seed) -> 4(weed)
// stage_b: event 5(seed) -> 6(fruit)
UpsertLifecycleStatus(... InputEventID: 1) + PromoteLifecycleStatusSuccess(1, 2, ...)
UpsertLifecycleStatus(... InputEventID: 3) + PromoteLifecycleStatusFailed (3, 4, "*errors.errorString", "boom", ...)
UpsertLifecycleStatus(... InputEventID: 5) + PromoteLifecycleStatusSuccess(5, 6, ...)

statuses, _ := LoadLifecycleStatuses(db, "stage_a")
// 断言: len(statuses) == 2; [0] = alpha/success/{"ok":true}; [1] = beta/failed/*errors.errorString/"boom"
```

- 同时验证三件事：
  1. **按 plot 过滤**：`stage_b` 的快照不会出现在 `stage_a` 的查询结果中。
  2. **混合状态读出**：`success` 与 `failed` 两条快照都能正确反序列化。
  3. **排序约定**：`LoadLifecycleStatuses` 内部 `ORDER BY ts, input_event_id`，结果按 `ts` 升序（`1.0` 在 `3.0` 之前）。
- 错误信息使用 `*errors.errorString`（标准库 `errors.New` 产生的类型），`ErrorType` / `ErrorMessage` 都按 `fmt.Sprintf` 形式落地。

## 运行方式

```bash
# 运行本包全部测试
go test ./pkg/persist/...

# 仅运行本文件
go test ./pkg/persist -run 'TestLifecycleSQLite'

# 带 -v 查看每个用例的子断言
go test ./pkg/persist -run 'TestLifecycleSQLite' -v
```

## 注意事项

- **未覆盖的路径**：当前测试**不**直接覆盖 `HandleRecord` 的 `seed` / `fruit` / `weed` 路由，也不验证 `LoadStatuses` 在 `sqliteDB == nil` 时的「按 `SQLitePath` 重新打开」分支。补充测试时可使用 `pkg/funnel.NewSpout` + `chan LifecycleRecord` 来驱动。
- **测试依赖 SQLite 驱动**：`go test` 会通过 `modernc.org/sqlite` 解析 `import _ "modernc.org/sqlite"`，该驱动在 `go.mod` 中已声明（`v1.57.0`），无需额外准备。
- **临时目录自动清理**：`t.TempDir()` 在测试结束时被删除，因此即使测试中途 `t.Fatalf` 也会清理；`defer db.Close()` 仍建议保留，避免 WAL 辅助文件遗留在临时目录。
- **错误信息匹配**：`*errors.errorString` 是 `errors.New` 生成的内部类型名，第三方错误包装（如 `fmt.Errorf("%w", ...)`）的 `ErrorType` 会是 `*fmt.wrapError` 之类；不要把本测试的断言照搬到自定义错误上。
- **PRAGMA 副作用**：测试在 `OpenLifecycleSQLite` 中会启用 WAL，临时目录下会出现 `lifecycle.sqlite3-wal` / `lifecycle.sqlite3-shm`；这不会影响断言，但跨进程读取同一路径时需要先 `db.Close()`。
