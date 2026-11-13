# pkg/scarecrow/metrics_test.go

> 📅 最終更新日: 2026/11/13

## 役割

`pkg/scarecrow/metrics_test.go` は `MetricsWatcher` のカウント基準とスナップショットインターフェースをカバーします：入力元の分類、結果カウントと未完了数、状態とタイムスタンプ、一括ビュー、ゼロ値の使用可否です。これは [`metrics.md`](./metrics.md) のフィールド計算表の実行可能な版です。

## テストの分布

| テスト | 対象内容 |
|------|----------|
| `TestMetricsWatcherCountsInputs` | 外部入力 vs 上流入力。受信側の `UpstreamCounts` と送信側の `DownstreamCounts` も同時に検証する |
| `TestMetricsWatcherCountsResults` | 4 件の入力 + それぞれ 1 件の成熟／枯死／剪定で、`Processed` と `Pending` を検証する |
| `TestMetricsWatcherTracksStatus` | `PlotStartEvent` → `running` かつ `EndTime` はゼロ値。`PlotEndEvent` → `stopped` かつ `EndTime` が更新される |
| `TestMetricsWatcherFarmView` | `GetPlots` が 2 つの Plot のスナップショットを返す。未登録の `ghost` は `false` を返す |
| `TestMetricsWatcherZeroValue` | `var metrics MetricsWatcher` でも直接 `OnEvent` が使える（`plots` を遅延初期化する） |

## 主要なアサーションと設計意図

### 入力元：`TestMetricsWatcherCountsInputs`

```text
SeedInputEvent{Plot: "a", SeedID: 1}              // 外部注入
SeedInputEvent{Plot: "b", From: "a", SeedID: 2}   // a 投递给 b
```

| アサーション | 意図 |
|------|------|
| `a.ExternalInput == 1`、`a.UpstreamInput == 0`、`a.InputTotal == 1` | `From == ""` のときだけ外部入力として数える |
| `a.DownstreamCounts["b"] == 1` | 配送行為は**送信側**で記帳する |
| `b.ExternalInput == 0`、`b.UpstreamInput == 1`、`b.InputTotal == 1` | `UpstreamInput` はマップの合計から求める |
| `b.UpstreamCounts["a"] == 1` | 受信側は送信元ごとに個別に数える |

このテストは「双方向の記帳」も同時に検証します：同じ `SeedInputEvent` が 2 つの Plot のセル（受信側の `upstream` と送信側の `downstream`）に書き込まれるため、送信側が一度も起動していなくても `GetPlots()` に現れます。

### 結果と未完了：`TestMetricsWatcherCountsResults`

- 4 件の外部入力を連続で送った後、成熟・枯死・剪定をそれぞれ 1 件ずつ送る。
- `(Succeeded, Failed, Skipped) == (1, 1, 1)`、`Processed == 3`、`InputTotal == 4`、`Pending == 1` をアサートする——`Processed` が 3 者の和、`Pending` が入力から処理済みを引いた値という基準を直接固定する。
- このテストではすべての `OnEvent` のエラーを無視する（`_ =`）。この経路はエラーを生成しないため。

### 状態と時刻：`TestMetricsWatcherTracksStatus`

- 起動直後にスナップショットを読む：`Status == PlotRunning`、`StartTime` はイベント時刻に等しく、`EndTime.IsZero()` が真——「未終了なら終了時刻を設定しない」ことを検証する。
- 終了後：`Status == PlotStopped`、`EndTime` はイベント時刻に等しく、`StartTime` は上書きされない（テストは状態と `EndTime` しか読んでいないが、実装では両者は独立したフィールド）。
- `==` ではなく `time.Time.Equal` で比較し、単調クロックのフィールド差を回避する。

### 一括ビュー：`TestMetricsWatcherFarmView`

- 2 つの Plot がそれぞれ 1 件の起動イベントを送り、`GetPlots()` の長さが 2 で、両方の状態が `running` でなければならない。
- `"ghost"` に対して `GetPlot` を呼ぶと `false` を返し、「未登録ならデータを捏造しない」ことを検証する。

### ゼロ値：`TestMetricsWatcherZeroValue`

- `var metrics MetricsWatcher` で `NewMetricsWatcher` を呼ばず、直接 `OnEvent` した後に `GetPlot("a")` が `ExternalInput == 1` を返すべき。これは `plot()` 内の `m.plots == nil` の遅延初期化分岐を検証する。

## 実行方法

```bash
go test ./pkg/scarecrow -run 'TestMetricsWatcher' -v
```

本ファイルは並行処理も I/O も扱わないため `-race` は不要です。並行保護は実装の `RWMutex` が担いますが、現在のテストは並行呼び出しを行っていません。

## 注意事項

- **`Pending` の 0 下限クランプはカバーされていない**：すべてのテストが `Processed <= InputTotal` を満たしており、イベント列に結果だけがあり入力がない場合の `pending < 0 → 0` 分岐はテストされていません。このロジックを変更する際は自分でテストを追加する必要があります。
- **未知の `PlotStatus` に対する `String()` のデフォルト分岐**（`not_started` を返す）も、`PlotStartEvent` が重複到着したときの上書きセマンティクスもカバーされていません。
- テストはイベントを直接構築して `Hub` を経由しないため、並行ロックの振る舞いは検証しません。`GetPlot` が返すスナップショットを安全に長期保持できるかも実装の保証に委ねられ、テストではアサートされていません。
- アサーションは構造体フィールドの完全な組み合わせ（例えば 3 つ組 `1/0/1`）を使うため、フィールド名の変更は即座にコンパイルエラーになります。これは想定された連鎖影響です。
