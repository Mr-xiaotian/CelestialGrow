# pkg/persist/sqlite_test.go

> 📅 最終更新日: 2026/09/24

## 役割

`pkg/persist/sqlite_test.go` は `pkg/persist` パッケージで **唯一** のテストファイルであり、[`sqlite.md`](./sqlite.md) に記述された SQLite ストレージ層のエンドツーエンド検証のみを行い、[`lifecycle.md`](./lifecycle.md) の `LifecycleRecordHandler` / `LifecycleInlet` や [`log.md`](./log.md) のログのディスク書き込みには関与しません。3 つのテスト関数はいずれも `t.TempDir()` で独立した一時ファイルを取得し、`OpenLifecycleSQLite` の入口として使用することで、テスト同士が互いに影響せず副作用もないことを保証します。

> 現在のファイルには、`LifecycleRecordHandler.HandleRecord` のルーティング（`seed` / `ripen` / `wither`）および `LogInlet` / `LogRecordHandler` に対する単体テストは **ありません**。後から追加する場合は、`pkg/funnel` の Spout が単一 goroutine でスケジュールする特性に合わせ、インメモリ channel + 同期で締める方式で駆動するのが適切です。

## テストのカバレッジ

| テスト | カバーする能力 | 主要な不変条件 |
|------|----------|-----------|
| `TestLifecycleSQLiteEventRoundTrip` | DB をオープン → seed + 親辺を書き込み → イベントと親辺を再読み出し | `events` の主キーが書き込み可能；`event_parents` の多対多の辺が書き込み可能；`LoadLifecycleEvent` / `LoadLifecycleEventParents` の動作が対称 |
| `TestLifecycleSQLiteStatusRoundTrip` | 2 つのイベントを書き込み → `UpsertLifecycleStatusSeed` → `PromoteLifecycleStatusRipen` → 再読み出し | 状態スナップショットが挿入可能；昇格は `current_event_id` / `status` / `fruit_json` / `ts` のみを更新し、`seed_json` と `plot` は保持する |
| `TestLifecycleSQLiteStatusPairs` | 複数 plot・複数イベントを混在させて挿入 → 複数回の挿入 + 1 回の ripen + 1 回の wither → plot 単位で検索 | `LoadLifecycleStatuses` は `ts, input_event_id` の昇順；`plot` のフィルタリングが正確；`Status` / `SeedJSON` / `FruitJSON` / `WitherType` / `WitherMessage` の各フィールドが正しく永続化される |

## 主要なテストの詳細

### 共有フィクスチャとクリーンアップ

```go
dbPath := filepath.Join(t.TempDir(), "lifecycle.sqlite3")
db, err := OpenLifecycleSQLite(dbPath)
if err != nil { t.Fatalf("OpenLifecycleSQLite() error = %v", err) }
defer db.Close()
```

- 各テストは独立して `t.TempDir()` を取得し、その後 SQLite 側が `lifecycle.sqlite3` を作成します。
- `defer db.Close()` はテスト関数が戻る前にデータベースをクローズし、WAL のディスク書き出しを促します。テスト終了後は `t.TempDir()` が自動的にクリーンアップされるため、手動の `os.Remove` は不要です。
- `OpenLifecycleSQLite` の内部で PRAGMA + テーブル作成スクリプトが起動されるため、**テストは接続設定と schema 作成のパスも同時に検証しています**。

### `TestLifecycleSQLiteEventRoundTrip`

```go
InsertLifecycleEvent(db, 1, "seed", "source", 1.0, nil)
InsertLifecycleEvent(db, 2, "fruit", "source", 2.0, []int{1})

loadedEvent, _ := LoadLifecycleEvent(db, 2)
parentIDs, _ := LoadLifecycleEventParents(db, 2)
```

- 親辺を持たない `seed` イベント（`parentIDs == nil`）を 1 件、親辺を 1 つ持つ `fruit` イベントを 1 件書き込みます。
- 再読み出しのアサーション：`loadedEvent.EventType == "fruit"`、`parentIDs == []int{1}`。
- 「イベント本体 + 親辺」が同じトランザクションで書き込まれ、独立して読み出せることを検証しています。

### `TestLifecycleSQLiteStatusRoundTrip`

```go
UpsertLifecycleStatusSeed(db, 1, `{"value":"alpha"}`, "stage_a", 1.0)
PromoteLifecycleStatusRipen(db, 1, 3, `{"ok":true}`, 3.0)

loadedStatus, _ := LoadLifecycleStatus(db, 1)
// 断言: CurrentEventID == 3, Status == "ripen", FruitJSON == `{"ok":true}`
```

- 前提となるイベントは `{EventID: 1, EventType: "seed", Plot: "stage_a", TS: 1.0}` と `{EventID: 3, EventType: "fruit", Plot: "stage_a", TS: 3.0}` です（`[]LifecycleEventRecord` リテラルをループで書き込み）。
- 「`seed` スナップショット → `ripen` 昇格」の全経路を通します。
- `PromoteLifecycleStatusRipen` が `current_event_id` / `status` / `fruit_json` / `ts` **だけ** を変更し、`seed_json` と `plot` を壊さないことを検証します。
- 同時に、`LoadLifecycleStatus` の列順と `LifecycleStatusRecord` のフィールドが 1 対 1 で対応することも検証します（`WitherType` / `WitherMessage` が空文字列として読み出されることを含む）。

### `TestLifecycleSQLiteStatusPairs`

```go
// stage_a: event 1(seed) -> 2(fruit), event 3(seed) -> 4(weed)
// stage_b: event 5(seed) -> 6(fruit)
UpsertLifecycleStatusSeed(db, 1, `{"value":"alpha"}`, "stage_a", 1.0) + PromoteLifecycleStatusRipen(1, 2, `{"ok":true}`, 2.0)
UpsertLifecycleStatusSeed(db, 3, `{"value":"beta"}`,  "stage_a", 3.0) + PromoteLifecycleStatusWither(3, 4, "*errors.errorString", "boom", 4.0)
UpsertLifecycleStatusSeed(db, 5, `{"value":"gamma"}`, "stage_b", 5.0) + PromoteLifecycleStatusRipen(5, 6, `{"ok":"other"}`, 6.0)

statuses, _ := LoadLifecycleStatuses(db, "stage_a")
// 断言: len(statuses) == 2;
//   [0] = alpha/ripen/{"ok":true}
//   [1] = beta/wither/*errors.errorString/"boom"
```

- 同時に 3 つのことを検証します：
  1. **plot によるフィルタリング**：`stage_b` のスナップショットは `stage_a` の検索結果に現れません。
  2. **混在した状態の読み出し**：`ripen` と `wither` の 2 つのスナップショットがいずれも正しくデシリアライズされます。
  3. **ソートの約束**：`LoadLifecycleStatuses` の内部は `ORDER BY ts, input_event_id` で、結果は `ts` の昇順になります（`1.0` が `3.0` より前）。
- エラーメッセージには `*errors.errorString`（標準ライブラリ `errors.New` が生成する型）を使用し、`WitherType` / `WitherMessage` はいずれも `fmt.Sprintf` 形式で永続化されます。

## 実行方法

```bash
# 运行本包全部测试
go test ./pkg/persist/...

# 仅运行本文件
go test ./pkg/persist -run 'TestLifecycleSQLite'

# 带 -v 查看每个用例的子断言
go test ./pkg/persist -run 'TestLifecycleSQLite' -v
```

## 注意事項

- **カバーされていないパス**：現在のテストは `HandleRecord` の `seed` / `ripen` / `wither` ルーティングを直接カバーしておらず、`sqliteDB == nil` のときの `LoadStatuses` の「`SQLitePath` を再度オープンする」分岐も検証していません。補足のテストでは `pkg/funnel.NewSpout` + `chan LifecycleRecord` で駆動できます。
- **テスト内の `EventType` は業務が書き込む値と異なる**：テストはイベントグラフを構築するために `"fruit"` / `"weed"` を `events.event_type` として使用していますが、`lifecycle.go` の `HandleRecord` が実際に書き込むのは `"ripen"` / `"wither"` です。`events.event_type` には CHECK 制約がないため、どちらも通ります。テストはストレージ層のみを検証し列挙は検証しないので、イベント種別の命名を変更する際にこのファイルのリテラルに惑わされないでください。
- **状態値は `Promote*` が基準**：テストがアサートする状態は `"ripen"` / `"wither"`（`PromoteLifecycleStatusRipen` / `PromoteLifecycleStatusWither` にハードコードされた `status` と一致）で、初期スナップショットの状態は `"seed"` です。
- **テストは SQLite ドライバに依存**：`go test` は `modernc.org/sqlite` を通じて `import _ "modernc.org/sqlite"` を解決します。このドライバは `go.mod` で宣言済み（`v1.57.0`）なので、追加の準備は不要です。
- **一時ディレクトリの自動クリーンアップ**：`t.TempDir()` はテスト終了時に削除されるため、テストの途中で `t.Fatalf` が起きてもクリーンアップされます。ただし WAL の補助ファイルが一時ディレクトリに残るのを避けるため、`defer db.Close()` は引き続き残すことを推奨します。
- **エラーメッセージの一致**：`*errors.errorString` は `errors.New` が生成する内部型名であり、第三者によるエラーラップ（例えば `fmt.Errorf("%w", ...)`）の `WitherType` は `*fmt.wrapError` のようになります。本テストのアサーションを独自のエラーにそのまま流用しないでください。
- **PRAGMA の副作用**：テストは `OpenLifecycleSQLite` で WAL を有効化するため、一時ディレクトリに `lifecycle.sqlite3-wal` / `lifecycle.sqlite3-shm` が現れます。これはアサーションには影響しませんが、プロセスをまたいで同じパスを読み取る際は先に `db.Close()` する必要があります。
