# pkg/plot/plot_prune_test.go

> 📅 最終更新日: 2026/11/13

`plot_prune_test.go` は `SinglePlot` の**剪定（prune）セマンティクス**に焦点を当てます。`WithPruneIf` の述語が 1 つの seed を「そのままスキップ」にするか「通常どおり育成」にするかをどう決めるか、そして剪定数 `GetPruneNum` と完了数 `GetCompleted` が混在シナリオで保存されるかを検証します。

## ケース

### `TestPlot_PruneSkipsCultivator` —— 全件ヒットで剪定

- **cultivator**：
  ```go
  func(seed int) (int, error) {
      cultivations.Add(1)
      return 0, errors.New("cultivator should never be called")
  }
  ```
- **設定**：`plot.NewSinglePlot("test_prune_all", cultivator, plot.WithTenders(1), plot.WithPruneIf(func(seed int) bool { return true }))`
- **seeds**：`[]int{1, 2, 3}`

検証：

1. `cultivations.Load() == 0` —— 述語が常に `true` なので、`cultivator` は **一度も呼ばれません**。
2. `mustHarvest` が 3 件のレコードを返し、いずれも `Status == "prune"` です。
3. `plot.GetPruneNum() == 3` かつ `plot.GetCompleted() == 3` です。

> カバーのポイント：剪定は `cultivator` の前に行われるため、全件ヒット時は育成ロジックが完全に迂回され、しかも剪定された seed は通常どおり完了数に計上され、`GetCompleted` と seed の総数が保存されます。

### `TestPlot_PruneMiss` —— 全件ミス

- **cultivator**：`func(seed int) (int, error) { return seed * 10, nil }`
- **設定**：`plot.NewSinglePlot("test_prune_miss", cultivator, plot.WithTenders(2), plot.WithPruneIf(func(seed int) bool { return false }))`
- **seeds**：`[]int{1, 2, 3}`

検証：

1. 各 seed の `Status == "ripen"`、`FruitJSON == strconv.Itoa(seed*10)` です。
2. `plot.GetPruneNum() == 0` です。
3. `plot.GetFruitNum() == 3` です。

> カバーのポイント：述語が常に `false` のときの挙動は **`WithPruneIf` を未設定にした場合と完全に同じ** です —— つまり剪定は「ゼロ侵襲」の任意機能であり、既存コードは影響を受けません。

### `TestPlot_PruneMixed` —— 混在シナリオ

- **cultivator**：`seed % 2 == 0` のときエラー（wither）を返し、それ以外は `seed * 10`（ripen）を返します。
- **設定**：`plot.NewSinglePlot("test_prune_mixed", cultivator, plot.WithTenders(2), plot.WithPruneIf(func(seed int) bool { return seed > 3 }))`
- **seeds**：`[]int{1, 2, 3, 4, 5}`

検証：

1. 5 件のレコードで、`Status` の分布は `prune`（`seed > 3`：4、5）が 2 件、`wither`（偶数 2）が 1 件、`ripen`（奇数 1、3）が 2 件です。
2. `successCount == 2`、`failedCount == 1`、`pruneCount == 2` です。
3. `plot.GetFruitNum() == 2`、`plot.GetWeedNum() == 1`、`plot.GetPruneNum() == 2` です。
4. `plot.GetCompleted() == 5` —— 3 種類の結果の合計が seed の総数に等しく、厳密に保存されます。

> カバーのポイント：prune / ripen / wither の三態が同じ plot 内で共存でき、`GetCompleted`（成熟 + 枯死 + 剪定）が常に入力 seed 数と等しくなるため、進捗表示と `IsFinish` が剪定シナリオでも歪まないことが保証されます。

## 共通の前提条件

- テストパッケージ：`package plot_test`（ブラックボックス）であり、`SinglePlot` の未エクスポートフィールドには直接アクセスしません。
- `plot_harvest_test.go` で定義された `mustHarvest(t, plot harvestable)` を再利用してライフサイクルスナップショットを取得します。
- `indexStatusesBySeed(records)` でレコードを `SeedJSON` によりインデックス化し、seed ごとにアサートできるようにします。`SeedJSON` は `strconv.Itoa(seed)` であり、たとえば seed `1` の key は `"1"` です。
- カウントのアサーションは `GetPruneNum()` / `GetFruitNum()` / `GetWeedNum()` / `GetCompleted()` に依存します。これらのメソッドは `pkg/plot/plot.go` の `basePlot` に定義され、`scarecrow.MetricsWatcher` がイベントストリームから再構築した `PlotMetrics` スナップショット（`Skipped` / `Succeeded` / `Failed` / `Processed`）を読みます。独立した `Counter` はもうありません。

## 実行方法

```bash
go test ./pkg/plot -run "TestPlot_Prune" -v
```

または plot パッケージのテストをまとめて実行します。

```bash
go test ./pkg/plot/... -v
```

## 注意事項

- 状態文字列はソースコードを基準とします。剪定は `"prune"`、成功は `"ripen"`、失敗は `"wither"` です。
- 剪定と `wither` の重要な違い：`wither` は「育成の失敗」、`prune` は「育成の前にスキップされた」ものです。どちらも完了として数えられますが、`WitherMessage` を書き込むのは **`wither` だけ** です（`prune` レコードには WitherMessage がありません）。
- `TestPlot_PruneMixed` は `seed > 3` を述語に、`seed % 2` を wither の判定に使っており、両者は直交するため三態の共存を確定的にカバーできます。
- これらのケースは一律で `WithTenders` による並行実行を使用します。指標は `MetricsWatcher` がイベント配信時にロックして加算するため、アサーションは厳密な直列実行に依存しません。
