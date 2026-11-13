# pkg/persist/log_test.go

> 📅 最終更新日: 2026/11/13

## 役割

`pkg/persist/log_test.go` は `LogWatcher` の 2 つの中核挙動を検証します：

1. **イベント → テキストのマッピング**：各 `scarecrow` イベントが生成するログレベルと文言が正しいか；
2. **レベルフィルタリング**：`minLevel` 未満のイベントが黙って破棄されるか。

テストはパッケージ内コンストラクタ `newLogWatcher(handler, metrics, level)` でキャプチャ用 handler を注入し、実際のログファイルには書き込みません。

## テストフィクスチャ

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

- `funnel.RecordHandler[LogRecord]` を実装しますが、記録するだけでディスクへは書きません。
- `sync.Mutex` がスライスを保護します：`HandleRecord` は `Spout` のバックグラウンド goroutine で呼ばれ、`snapshot` はテスト goroutine で読み取ります。

## テストのカバレッジ

| テスト | カバーする能力 | 主要な不変条件 |
|------|----------|-----------|
| `TestLogWatcherTranslatesEvents` | 9 個のイベント → 10 件のログ。Farm / Plot / Seed のすべてのマッピングをカバー | 記録順、レベル、文言、指標の件数が期待どおりに完全一致 |
| `TestLogWatcherFiltersLevels` | `minLevel = ERROR` のときに 1 件の DEBUG + 1 件の ERROR を送信 | ERROR の記録のみが残る |

## `TestLogWatcherTranslatesEvents`

### 駆動方法

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

- 最低レベルを `DEBUG` に設定します。そうしないと `SeedInputEvent` がフィルタリングされ、完全なマッピングを検証できません。
- **同じイベントを先に `metrics` へ、次に `obs` へ送ります**：`PlotEndEvent` の件数は `MetricsWatcher` から読むため、順序を逆にすると古いスナップショットを読んでしまいます。これは `pkg/assembly` における「指標オブザーバを先に登録する」実際の順序にも対応しています。

### 期待される記録

`events` は順に：`FarmStart` → `PlotStart` → `SeedInput` → `SeedReplant` → `SeedRipen` → `SeedWither` → `SeedPrune` → `PlotEnd` → `FarmEnd` で、合計 10 件の記録を生成します（`FarmStart` の `Structure` が 1 行だけなので、さらに 1 件を追加で生み出します）：

| # | レベル | 文言のアサーション（`strings.Contains`） |
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

- アサーションは「件数 + 各件のレベル + 部分文字列の包含」の 3 段構えです：まず `len(records) != len(want)` で失敗させ、次に `Level` と `Message` を順に比較します。
- インデックス 8 は `MetricsWatcher` との統合も検証します：`SeedRipen` / `SeedWither` / `SeedPrune` がそれぞれ 1 回なので、件数は `1 ripened, 1 withered, 1 pruned` になります。
- インデックス 3〜7 の `[seedID*]` / `[seedID->resultID*]` サフィックスは、それぞれ `SeedID`、`FruitID`、`WeedID`、`PruneID` フィールドに対応します。
- `SeedReplant` の文言は `attempt 1` を含み、サフィックスは `[1*]` のみです（再試行イベントは新しいイベント ID を生成しません）。

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

- `minLevel = levelOrder["ERROR"] = 5` であり、`DEBUG`（1）と `INFO`（3）はどちらもフィルタリングされます。
- `SeedWitherEvent` は `Err` を設定していないため、文言は `<nil>` とレンダリングされますが、本テストはレベルと件数のみをアサートし、このテキストには依存しません。

## 実行方法

```bash
# 运行本文件
go test ./pkg/persist -run 'TestLogWatcher'

# 查看详细结果
go test ./pkg/persist -run 'TestLogWatcher' -v

# 运行整包
go test ./pkg/persist/...
```

## 注意事項

- **`Stop` は同期点**：`Spout.Stop` はチャネルを閉じ、キューの排出を待って（最大 1s）から `snapshot` を取ります。`Stop` を呼ばないと記録数が不安定になります。
- **`Start` は省略できない**：注入した handler の `BeforeStart` は空実装ですが、消費 goroutine は依然として `Start` が起動します。
- **指標の送信順に依存**：`metrics.OnEvent` を `obs.OnEvent` の後に置くと、`PlotEnd` の件数が少なくなり、インデックス 8 のアサーションが失敗します。
- **カバーするのは変換とフィルタリングのみ**：`LogRecordHandler` のディレクトリ作成、ファイルオープン、追記、クローズはソースコードと [`log.md`](./log.md) が説明しており、本テストはファイルシステムに触れません。
- **`FormatTime` はアサートしない**：テストはイベント時刻に `time.Now()` を使い、整形結果をハードコードしてアサートしません。形式を検証する必要がある場合は、固定時刻に切り替えて `2006-01-02 15:04:05` の形をアサートすべきです。
