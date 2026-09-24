# pkg/plot/plot_split_test.go

> 📅 最終更新日: 2026/09/24

`plot_split_test.go` は `SplitPlot` の**結果スナップショット**テストであり、ケースは 1 つだけです。分割ノードが standalone でバッチ実行した後、各 seed を**1 件の** `ripen` として記録し、分割結果のスライス全体を `FruitJSON` に永続化することを検証します —— 「0 個の要素に分割する」境界ケースも含みます。

## ケース

### `TestSplitPlot_RunHarvest`

- **splitter**：
  ```go
  func(seed int) ([]string, error) {
      fruits := make([]string, 0, seed)
      for i := 0; i < seed; i++ {
          fruits = append(fruits, fmt.Sprintf("%d-%d", seed, i))
      }
      return fruits, nil
  }
  ```
- **設定**：`plot.NewSplitPlot("split_harvest", splitter, plot.WithTenders(2))`
- **seeds**：`[]int{0, 2}`

検証：

1. `mustHarvest(t, split)` が 2 件の状態レコードを返します。
2. `indexStatusesBySeed` を使い `SeedJSON` を key としてインデックスを作成した後：
   - `index["0"]`（`seed = 0` であり、ループが実行されない → 空スライス）：`Status == "ripen"`、`FruitJSON == "[]"` です。
   - `index["2"]`（`seed = 2` → `["2-0","2-1"]`）：`Status == "ripen"`、`FruitJSON == "[\"2-0\",\"2-1\"]"` です。

> カバーのポイント：
> 1. 分割で生成される要素数は 0 でもよく、その場合でも `ripen` を 1 件記録します（`AddFruitNum(1)` は `len(fruits)` とは無関係です）。
> 2. スライス全体（個々の要素ではなく）が 1 回の成功結果として記録されます。

## 共通の前提条件

- テストパッケージ：`package plot_test`（ブラックボックス）であり、`SplitPlot` の未エクスポートフィールドにはアクセスしません。
- 同パッケージ内の他のテストファイルが提供する 2 つの補助関数に依存します（いずれも `plot_harvest_test.go` で定義されています）：
  - `mustHarvest(t *testing.T, plot harvestable)`：`Harvest()` をラップし、エラー時に `t.Fatalf` を呼びます。`harvestable` はテスト内で定義されたローカルインターフェース `interface{ Harvest() ([]persist.LifecycleStatusRecord, error) }` です。
  - `indexStatusesBySeed(records)`：`record.SeedJSON` を key としてインデックスを作成します。
- 本ケースはどの下流も**接続していません**：`split.yieldChans` は空であり、`ripenSeed` の下流への配信ループは実行されません。`Run` は末尾の `Seal()` が入力のクローズを引き起こした後に正常に終了処理を行います。
- したがって本ケースは「分割で生成された各要素が 1 つの下流 yield になる」ことや `AddDownstreamYieldNum(len(fruits))` のカウント効果を**検証しません** —— その部分は `pkg/farm` の `farm_split_test.go` がカバーします。

## 実行方法

```bash
go test ./pkg/plot -run "TestSplitPlot" -v
```

## 注意事項

- `fruits` は `make([]string, 0, seed)` で生成されるため、`seed = 0` のときは**非 nil の空スライス**となり、JSON シリアライズは `null` ではなく `[]` になります。splitter が `nil` を返すように変わった場合、本アサーションは相応に `null` へ変更する必要があります。
- `indexStatusesBySeed` の key は `SeedJSON`（`int` seed の JSON テキスト）であり、フォーマット後の構造体文字列ではありません。
- このケースは単一 seed のスライス全体の永続化のみを検証し、下流が実際に受け取る要素数と順序は検証しません。
