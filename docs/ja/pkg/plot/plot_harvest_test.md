# pkg/plot/plot_harvest_test.go

> 📅 最終更新日: 2026/11/13

`plot_harvest_test.go` は `plot` パッケージの「**結果スナップショット**」テストセットです。`SinglePlot.Run` でバッチ実行した後に `Harvest` を呼び出し、各 seed の最終的なライフサイクル状態（`ripen` / `wither` + `FruitJSON` + `WitherMessage`）を取得して、次を検証します。

- 全失敗、一部失敗、全成功の 3 種類の結果分布。
- イベントストリームから再構築された指標（`GetCompleted`）と `GetState` がバッチ実行の終了時に正しく落ち着いているか。
- 失敗時のエラーメッセージがそのまま `LifecycleStatusRecord.WitherMessage` に反映されるか。

本ファイルはさらに、同パッケージの他のテストファイル（`plot_prune_test.go`、`plot_retry_test.go`、`plot_route_test.go`、`plot_split_test.go`）へ 2 つの補助関数を提供します。

## テスト補助関数

```go
type harvestable interface {
	Harvest() ([]persist.LifecycleStatusRecord, error)
}

func mustHarvest(t *testing.T, plot harvestable) []persist.LifecycleStatusRecord
```

`harvestable` はテスト内で定義されたローカルインターフェースであり、`Harvest()` の実装のみを要求します。そのため `SinglePlot`、`RoutePlot`、`SplitPlot` をそのまま `mustHarvest` に渡せます —— いずれも埋め込んだ `basePlot` を通じて同じ `Harvest` メソッドを得ています。`mustHarvest` はエラー時にそのまま `t.Fatalf` を呼び、そうでなければ状態レコードのスライスを返します。

```go
func indexStatusesBySeed(records []persist.LifecycleStatusRecord) map[string]persist.LifecycleStatusRecord
```

`record.SeedJSON` を key としてレコードのスライスを map に変換し、seed 値ごとの単点アサーションを容易にします（`int` seed の JSON テキストは `"1"`、`"2"` などです）。

## ケース

### `TestPlot_AllError` —— 全失敗

- **cultivator**：`func(seed int) (string, error) { return "", errors.New("always fail") }`
- **設定**：`plot.NewSinglePlot("test_all_error", cultivator, plot.WithTenders(2))`
- **seeds**：`[]int{1, 2, 3, 4, 5}`

検証：

1. `mustHarvest` が 5 件のレコードを返します。
2. 各 `record.Status == "wither"` かつ `record.WitherMessage == "always fail"` です。
3. `plot.GetCompleted() == 5` です。
4. `int(plot.GetState()) == 2`（`done`）です。

> 用途：`basePlot.witherSeed` の経路上で `SeedWitherEvent` が正しく発行され、ライフサイクルレコードが正しく書き込まれ、エラーメッセージが握りつぶされないことを保証します。

### `TestPlot_PartialError` —— 一部失敗

- **cultivator**：`seed % 2 == 0` の場合 `(0, errors.New("even number error"))` を返します。それ以外は `(seed*10, nil)` を返します。
- **設定**：`plot.NewSinglePlot("test_partial_error", cultivator, plot.WithTenders(2))`
- **seeds**：`[]int{1, 2, 3, 4, 5}`

検証：

1. `mustHarvest` が 5 件のレコードを返します。
2. `indexStatusesBySeed` を使い `strconv.Itoa(seed)` で各レコードを参照します：
   - 偶数の seed：`Status == "wither"` かつ `WitherMessage == "even number error"` です。
   - 奇数の seed：`Status == "ripen"` かつ `FruitJSON == strconv.Itoa(seed*10)` です。
3. 成功カウント = 3、失敗カウント = 2、`GetCompleted() == 5` です。

> 用途：`ripenSeed` と `witherSeed` の分岐条件が排他であり、`SeedJSON` が入力 seed と厳密に対応することを保証します。

### `TestPlot_AllSuccess` —— 全成功

- **cultivator**：`func(seed int) (int, error) { return seed * 2, nil }`
- **設定**：`plot.NewSinglePlot("test_all_success", cultivator, plot.WithTenders(3))`
- **seeds**：`[]int{1, 2, 3, 4, 5}`

検証：

1. `mustHarvest` が 5 件のレコードを返します。
2. 各 `Status == "ripen"` かつ `FruitJSON == strconv.Itoa(seed*2)` です。
3. `int(plot.GetState()) == 2`（`done`）です。

> 用途：失敗ゼロの純粋な成功経路をカバーし、`FruitJSON` のシリアライズと状態機械の終了処理を検証します。

## 共通の前提条件

- テストパッケージ：`package plot_test`（ブラックボックステスト）であり、`SinglePlot` の未エクスポートフィールドには直接アクセスしません。
- `StartAsync` / `Seal` を明示的に呼び出さず、すべて `Run(seeds, "INFO")` の内部で完結します。
- `Run` は `assembly.StartPlotResources` を通じて `MetricsWatcher` と `LifecycleWatcher` を登録するため、`GetCompleted` はイベントストリームから再構築された指標を読め、`Harvest` は `LifecycleWatcher.DBPath()` からすでに永続化されたデータベースを見つけられます。
- `Run` は戻る前に `resources.Stop()` でオブザーバを停止してデータベースを閉じ、その後 `Harvest` が `persist.LoadLifecycleStatusesFromPath` でデータベースを開き直してオフライン照会します。
- 3 つのケースの cultivator はいずれも「一時的なエラーの後に成功する」という挙動を返さないため、`maxRetries` のデフォルト値（`1`）の影響を受けません —— 失敗ケースは**確定的な失敗**、成功ケースは**1 回で成功**です。

## 実行方法

```bash
go test ./pkg/plot -run "TestPlot_AllError|TestPlot_PartialError|TestPlot_AllSuccess" -v
```

または本ファイルの `TestPlot_` プレフィックスのケースをまとめて実行します。

```bash
go test ./pkg/plot -run "TestPlot_" -v
```

## 注意事項

- `mustHarvest` はそのまま `t.Fatalf` で失敗します。`plot %q lifecycle watcher is not registered` や `plot %q lifecycle database is not ready` のいかなるエラーもテスト全体を失敗させます。
- 3 つのケースはいずれもライフサイクルの SQLite スナップショットのみを検証し、ログファイルの内容は検証しません。ログの挙動が必要な場合は `pkg/persist` の関連テストを参照してください。
- 状態文字列はソースコードを基準とします。成功は `"ripen"`、失敗は `"wither"` です（`pkg/persist` のライフサイクルテーブルの基準によって決まります）。この基準が変更された場合、本ファイルと `plot_retry_test.md` に記載されたアサーションを同期して更新する必要があります。
- `indexStatusesBySeed` の key は `SeedJSON`（seed の JSON テキスト）であり、フォーマット後の構造体文字列ではありません。そのためアサーションでは `fmt.Sprintf("%+v", seed)` ではなく `strconv.Itoa(seed)` を使用します。
