# pkg/reporter/snapshot_test.go

> 📅 最終更新日: 2026/11/26

## 役割

`pkg/reporter/snapshot_test.go` は `SnapshotTicker` の投影と転送の挙動を検証し、あわせて `NodeStatus` の JSON フィールド契約を個別に固定します。ネットワークリクエストは発行しません：`MetricsView` と `SnapshotSink` はどちらもローカルのプローブで置き換えます。

## テスト分布

| テスト | カバー内容 |
|------|----------|
| `TestSnapshotTickerPushesProjectedSnapshot` | `PlotMetrics → NodeStatus` のフィールドマッピング、`TickPeriod` の戻り値、sink がスナップショットを受け取ること |
| `TestSnapshotTickerPropagatesSinkError` | sink のエラーがそのまま伝播する（`errors.Is` 可能） |
| `TestNewSnapshotTickerRejectsInvalidArguments` | nil MetricsView、nil sink、負の周期 |
| `TestNodeStatusJSONContract` | シリアライズ後に 11 個の JSON キーがすべて存在する |
| `TestToStatusSnapshotPreservesNodeSet` | 投影がすべてのノードキーを保持する（零値の指標を含む） |

テスト補助：

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

`stubMetricsView` は `GetPlots` 内で項目ごとに `copyIntMap` を行い、`scarecrow.MetricsWatcher` の「不変スナップショットを返す」セマンティクスを模倣します。`snapshotSinkProbe` は本番コードの `copySnapshot` でデータを保持し、アサーション時に後続の変更の影響を受けないようにします。

## 重要なアサーションと設計意図

### 投影マッピング：`TestSnapshotTickerPushesProjectedSnapshot`

入力指標：

```text
stage_a: Status=PlotStopped, StartTime=Unix(10,0), EndTime=Unix(20,0)
         InputTotal=7, Processed=6, Pending=1
         Succeeded=4, Failed=1, Skipped=1
         UpstreamCounts={"root":3}, DownstreamCounts={"leaf":2}
```

`NewSnapshotTicker(metricsView, sink, 2)` を構築した後：

| アサーション | 意図 |
|------|------|
| `TickPeriod() == 2` | 明示的な周期が既定値で上書きされない |
| `sink.snapshot["stage_a"]` が存在する | ノード名が map のキーとして完全に保持される |
| `Status == int(scarecrow.PlotStopped)` | `PlotStatus` が Web 側の整数コード（`2`）に変換される |
| `TasksInput == 7`、`TasksProcessed == 6`、`TasksPending == 1` | 3 つの主要カウントが正しくマッピングされる |
| `StartTime == 10.0`、`EndTime == 20.0` | `time.Time` が Unix 浮動小数点秒に変換される（整数秒なら小数なし） |

`time.Unix(10, 0)` の `Unix()` は 10、`Nanosecond()` は 0 なので、期待値は正確に `10.0`/`20.0` となり、`!=` で直接比較できます。`Succeeded`/`Failed`/`Skipped` と上流／下流カウントは本テストではアサートされていません（入力には値が与えられていますが）。

### エラーの伝播：`TestSnapshotTickerPropagatesSinkError`

```go
want := errors.New("boom")
ticker, _ := NewSnapshotTicker(stubMetricsView{plots: map[string]scarecrow.PlotMetrics{}}, &snapshotSinkProbe{err: want}, 1)
if err := ticker.OnTick(metronome.TickEvent{Seq: 1}); !errors.Is(err, want) {
    t.Fatalf("OnTick() error = %v, want wrap %v", err, want)
}
```

- 指標が空でも影響しません：`OnTick` はまず空の map を投影し、その後に sink を呼びます。
- `errors.Is` が命中するということは、`OnTick` が sink のエラーを**再ラップも置換もしていない**（現在は直接 `return`）ことを意味します。
- 失敗メッセージの "want wrap" という表現は実際の挙動とやや異なります：実装は `%w` でラップせず、そのまま返します。`errors.Is` でのアサーションはどちらでも成立します。

### 構築の検証：`TestNewSnapshotTickerRejectsInvalidArguments`

| 入力 | 想定される拒否理由 |
|------|-------------|
| `(nil, &snapshotSinkProbe{}, 1)` | nil MetricsView |
| `(stubMetricsView{}, nil, 1)` | nil sink |
| `(stubMetricsView{}, &snapshotSinkProbe{}, -1)` | 負の周期 |

`err == nil` だけを判定し、エラーテキストは比較しません。`period == 0 → 1` の既定値置換もカバーしていません（この分岐は `TickPeriod()` で間接的にしか観測できず、テストは行っていません）。

### JSON 契約：`TestNodeStatusJSONContract`

すべてのフィールドを埋めた `NodeStatus` を構築し、`json.Marshal` 後に `map[string]map[string]any` へデシリアライズして、キーの存在を 1 つずつ確認します：

```text
status, tasks_input, tasks_processed, tasks_pending,
tasks_succeeded, tasks_failed, tasks_skipped,
upstream_counts, downstream_counts, start_time, end_time
```

- これは**フィールド名の回帰テスト**です：JSON タグの改名やフィールド削除は Web 側のパースを失敗させるため、このテストが即座に警告します。
- 「キーが存在する」ことだけをアサートし、値の比較も、余分なフィールドの有無も確認しません。値のマッピングの正しさは前のテストがカバーします。
- `float64`/`map` を `any` にデコードするのは意図的です：Go の構造体に依存せず、Web 側で JSON をパースした後の形を模倣しています。

### ノード集合：`TestToStatusSnapshotPreservesNodeSet`

```go
snapshot := toStatusSnapshot(map[string]scarecrow.PlotMetrics{
    "a": {},
    "b": {},
})
// sortedKeys(snapshot) == []string{"a", "b"}
```

- 2 つの指標はどちらも零値（`Status=PlotNotStarted`、時刻は零、カウントは 0）ですが、それぞれに `NodeStatus` を生成しなければなりません——投影は「データがなさそう」という理由でノードを落としてはいけません。
- ソート済みのキーで比較し、map の走査順序の不確実性を回避します。
- 零値の時刻が `0` にマッピングされるかはアサートしていません（これは `toStatusSnapshot` の別の分岐です）。

## 実行方法

```bash
# 仅本文件的测试
go test ./pkg/reporter -run 'TestSnapshotTicker|TestNodeStatusJSONContract|TestToStatusSnapshot' -v

# 整个包
go test ./pkg/reporter -v
```

本ファイルはネットワークも並行処理もないため `-race` は不要です。ただし同一パッケージの `push_test.go` はローカル HTTP サーバーを起動するので、パッケージ全体を実行する際はループバックアドレスが利用可能であることを確認してください。

## 注意事項

- **未カバーの零値時刻分岐**：`StartTime.IsZero()` / `EndTime.IsZero()` のとき `0` を出力すべきですが、5 つのテストはいずれもこれをアサートしていません（`TestToStatusSnapshotPreservesNodeSet` はキーだけを確認します）。
- **未カバーの上流／下流カウント**：`UpstreamCounts` / `DownstreamCounts` のマッピングは本ファイルにアサーションが一切ありません。`push_test.go` も空の隣接リストのエンコードだけを確認しています。
- **未カバーの既定周期**：`period == 0 → 1` のケースがありません。
- **未カバーの空 map の JSON 形状**：`toStatusSnapshot` 自体は `upstream_counts` が `{}` にエンコードされることを保証しません。これは `copySnapshot`/`copyIntMap` に依存しており、スナップショットのレベルではアサートされていません（`push_test.go` は `edges` の空スライスだけをアサートしています）。
- **未カバーのインターフェースの typed-nil**：コンストラクタは `== nil` 判定だけを行うため、`(*stub)(nil)` を渡しても拒否されません。テストはこの挙動を固定していません。
