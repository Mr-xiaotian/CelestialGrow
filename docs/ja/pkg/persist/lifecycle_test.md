# pkg/persist/lifecycle_test.go

> 📅 最終更新日: 2026/11/13

## 役割

`pkg/persist/lifecycle_test.go` は `LifecycleWatcher` の **イベント変換** を検証します：`scarecrow` のイベントが `OnEvent` を通った後、正しく `LifecycleRecord` に変換されてキューへ送信されるかどうかです。テストは SQLite を開かず、パッケージ内コンストラクタ `newLifecycleWatcher(handler)` でキャプチャ用 handler を注入し、`LifecycleRecordHandler` のファイル副作用を回避します。

## テストフィクスチャ

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

- `funnel.RecordHandler[LifecycleRecord]` を実装しますが、**IO は一切行わず**、記録をスライスへ追加するだけです。
- `sync.Mutex` が `records` を保護します：`HandleRecord` は `Spout` のバックグラウンド goroutine で実行され、`snapshot` はメインのテスト goroutine で読み取ります。
- `snapshot` はコピーを返し、テストのアサーション中にバックグラウンドの書き込みが干渉するのを防ぎます。

> カスタム handler を注入すると `DBPath()` は空文字列を返します（デフォルトの `*LifecycleRecordHandler` しか認識しないため）。したがって本テストはデータベースパスに関与しません。

## テストのカバレッジ

| テスト | カバーする能力 | 主要な不変条件 |
|------|----------|-----------|
| `TestLifecycleWatcherTranslatesEvents` | 7 個のイベントを送信し、4 種類の種子結果イベント + 2 種類の無視されるイベントをカバー | 記録は 5 件だけ生成される；`Kind` / `InputEventID` / `CurrentEventID` / `ParentIDs` / JSON ペイロード / タイムスタンプがすべてマッピング規則に一致 |

## `TestLifecycleWatcherTranslatesEvents`

### 駆動方法

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

- 先に `Start` で消費 goroutine を起動し、次に 1 件ずつ `OnEvent` を呼び、最後に `Stop` でキューを排出してから `snapshot` を取ります——スライスを直接読むと非同期消費のため不安定になります。
- 固定タイムスタンプ `1_700_000_000_123` ミリ秒を使い、`TS` のミリ秒精度を検証します。

### アサーションの内容

| インデックス | 由来イベント | `Kind` | `InputEventID` | `CurrentEventID` | その他のアサーション |
|:----:|----------|--------|----------------|------------------|----------|
| 0 | `SeedInputEvent`（plot `a`） | `seed` | 1 | 1 | `PlotName == "a"`、`SeedJSON == "1"`、`ParentIDs == nil`、`TS ≈ 1700000000.123`（誤差 `1e-6`） |
| 1 | `SeedRipenEvent` | `ripen` | 1 | 2 | `FruitJSON == "2"`、`ParentIDs == [1]` |
| 2 | `SeedInputEvent`（plot `b`、`Parents: [2]`） | `seed` | 3 | 3 | `PlotName == "b"`、`SeedJSON == "2"`、`ParentIDs == [2]` |
| 3 | `SeedWitherEvent` | `wither` | 3 | 4 | `WitherMessage == "boom"`、`WitherType != ""` |
| 4 | `SeedPruneEvent` | `prune` | 5 | 6 | — |

- `PlotStartEvent` と `SealMergeEvent` はオブザーバに明示的に無視されるため、7 個のイベントから記録は 5 件しか生成されません——これが「無視分岐」の間接的なアサーションです。
- インデックス 2 は「上流からの送信」の場面をカバーします：`From` フィールドは永続化に影響せず、親辺は `Parents` に由来します。`Seed` が `2` のとき `SeedJSON` はちょうど `"2"` になります。
- インデックス 3 はエラーフィールドをカバーします：`WitherType` は `%T` で生成されるため非空のみをアサートし（具体的な型名は Go のバージョンで安定しますが、ハードコードする価値はありません）、`WitherMessage` はエラーテキストをアサートします。
- インデックス 4 の `Seed` フィールドは意図的に `9` を入れていますが、prune 記録は種子 JSON を書かないため、状態の進行がこのフィールドに依存しないことが分かります。

## 実行方法

```bash
# 运行本文件
go test ./pkg/persist -run 'TestLifecycleWatcher'

# 查看详细结果
go test ./pkg/persist -run 'TestLifecycleWatcher' -v

# 运行整包
go test ./pkg/persist/...
```

## 注意事項

- **`Stop` は同期点**：`Spout.Stop` はチャネルを閉じ、消費ループが空になるまで待ち（最大 1s）、その後でようやく `snapshot` がすべての記録を見られます。`OnEvent` だけ呼んで `Stop` しない場合、記録がまだ消費されていないためアサーションがランダムに失敗します。
- **`Start` は省略できない**：`OnEvent` 自体は `Inlet` へ送信するだけで、消費は `Start` が起動する goroutine に依存します。
- **書き込みロジックはカバーしない**：`captureLifecycleHandler` は SQL を書かず、`HandleRecord` の Kind ルーティングや `InsertLifecycleEvent` / `Promote*` の呼び出し順は [`sqlite_test.md`](./sqlite_test.md) とソースコードが保証します。本テストは「イベント → 記録」のフィールドマッピングのみを固定します。
- **エラー分岐はカバーしない**：`unsupported lifecycle operation`、`生命周期 sqlite 未初始化` などのエラーは本ファイルではアサートしません。`OnEvent` は未知のイベントに対して `nil` を返すため、テストできるエラー経路もありません。
- **時間のアサーションは `math.Abs` の許容誤差を使う**：`TS` は `float64(UnixMilli())/1000` であり、浮動小数点比較には必ず許容誤差を設けます（テストでは `1e-6`）。
