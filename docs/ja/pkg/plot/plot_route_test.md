# pkg/plot/plot_route_test.go

> 📅 最終更新日: 2026/09/24

`plot_route_test.go` は `RoutePlot` の**結果スナップショット**テストであり、ケースは 1 つだけです。ルーティングノードが standalone でバッチ実行した後、各 seed の成功を**1 件の** `ripen` として記録し、ルーティングテーブル全体をそのまま `FruitJSON` に永続化することを検証します。

## ケース

### `TestRoutePlot_RunHarvest`

- **cultivator（ルーティング関数）**：
  ```go
  func(seed int) (map[string]string, error) {
      return map[string]string{
          "left":  fmt.Sprintf("L%d", seed),
          "right": fmt.Sprintf("R%d", seed),
      }, nil
  }
  ```
- **設定**：`plot.NewRoutePlot("route_harvest", cultivator, plot.WithTenders(2))`
- **seeds**：`[]int{1, 2}`

検証：

1. `mustHarvest(t, route)` が 2 件の状態レコードを返します。
2. `indexStatusesBySeed` を使い `SeedJSON` を key としてインデックスを作成した後：
   - `index["1"]`：`Status == "ripen"`、`FruitJSON == {"left":"L1","right":"R1"}` です。
   - `index["2"]`：`Status == "ripen"`、`FruitJSON == {"left":"L2","right":"R2"}` です。

> カバーのポイント：ルーティングテーブルの**全体**（個々の value ではなく）が 1 回の成功結果として記録されること。これは `RoutePlot` の結果型が `map[string]Y` であり、1 件の `ripen` にのみ対応することを示します。

## 共通の前提条件

- テストパッケージ：`package plot_test`（ブラックボックス）であり、`RoutePlot` の未エクスポートフィールドにはアクセスしません。
- 同パッケージ内の他のテストファイルが提供する 2 つの補助関数に依存します（いずれも `plot_harvest_test.go` で定義されています）：
  - `mustHarvest(t *testing.T, plot harvestable)`：`Harvest()` をラップし、エラー時に `t.Fatalf` を呼びます。`harvestable` はテスト内で定義されたローカルインターフェース `interface{ Harvest() ([]persist.LifecycleStatusRecord, error) }` であるため、`Plot` / `RoutePlot` / `SplitPlot` をそのまま渡せます。
  - `indexStatusesBySeed(records)`：`record.SeedJSON` を key としてインデックスを作成します。
- 本ケースはどの下流も**接続していません**：`route.yieldChans` は空であり、`ripenSeed` の配信ループは実行されません。`Run` は末尾の `Seal()` が入力のクローズを引き起こした後に正常に終了処理を行います。
- したがって本ケースは `AddDownstreamYieldNum` / `SeedInput` などの下流への転送挙動を**検証しません** —— その部分は `pkg/farm` の `farm_route_test.go` がカバーします。

## 実行方法

```bash
go test ./pkg/plot -run "TestRoutePlot" -v
```

## 注意事項

- `FruitJSON` は `persist` の JSON シリアライズによって得られ、`map[string]string` は `encoding/json` により **key 名でソート**されて `{"left":...,"right":...}` として出力されます。アサーションはこの順序を固定しているため、シリアライズの実装が挿入順を保持するように変わった場合、本テストは同期して調整する必要があります。
- `indexStatusesBySeed` の key は `SeedJSON`（`int` seed の JSON テキスト。例えば `"1"`、`"2"`）であり、フォーマット後の構造体文字列ではありません。
- このケースは単一 seed のテーブル全体の永続化のみを検証し、「異なる下流が異なる yield を受け取る」という配信効果は検証しません。配信を検証する必要がある場合は `pkg/farm/farm_route_test.go` を参照してください。
