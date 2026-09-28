# pkg/farm/farm_structure_test.go

> 📅 最終更新日: 2026/09/24

## 役割

`farm_structure_test.go` は `pkg/farm` で最も大きなテストファイルであり、`Farm` の**非自明なトポロジー**におけるエンドツーエンドの振る舞いをカバーし、以下を検証する：

- 多ソース多シンクの「1→2→1」菱形構造（`TestFarmStructure121`）
- ノードが部分的に失敗したときの `fruit` / `weed` のカウント（`TestFarmStructure121PartialFailure`）
- 複数の**相互に非連結な**サブグラフの並行実行（`TestFarmStructureDisconnectedComponents`）
- 複数の source の速度差がある場合の fan-in の合流（`TestFarmStructure21FaninDifferentSpeed`）

## テストケース

### `TestFarmStructure121`

- トポロジー：`root`（source）→ `{midA, midB}` → `head`（sink）
- 50 個の種子（`0..49`）を `root` へ入力する。`midA` は `seed*10+1` を出力し、`midB` は `seed*10+2` を出力する。
- 期待：`head` は合計 100 個の fruit を受信し、各 `i ∈ [0,50)` について `i*10+1` と `i*10+2` がそれぞれちょうど 1 回ずつ現れる。
- すべての plot の最終状態が `state == 2` である。

### `TestFarmStructure121PartialFailure`

- 同一の菱形トポロジーで、20 個の種子（`0..19`）。
- 失敗の注入：
  - `root`：偶数の seed が error を返す → fruit/weed がそれぞれ 10
  - `midB`：`seed > 10` で失敗 → 5 fruit / 5 weed
  - `midA`：すべて成功
- 期待：
  - `root.GetFruitNum() == 10` かつ `GetWeedNum() == 10`
  - `midA.GetFruitNum() == 10`（`root` の実際の fruit 数と整合する）
  - `midB.GetFruitNum() == 5` / `GetWeedNum() == 5`
  - `head.GetFruitNum() == 15`（`midA` からの 10 + `midB` からの 5）
- すべての plot の最終状態が `state == 2` である。

### `TestFarmStructureDisconnectedComponents`

- 同一の `Farm` 内に相互に連結していない 2 つのサブグラフ：
  - サブグラフ A：`rootA` → `{midA1, midA2}`
  - サブグラフ B：`{rootB1, rootB2}` → `headB`
- 両方のサブグラフに 50 個の seed を入力する。
- 期待：
  - `resultsA`（`midA1` / `midA2` が共同で収集）は 50 個の異なる値を含み、各値のカウントが 2 である（`midA1` と `midA2` からそれぞれ 1 回ずつ）。
  - `resultsB`（`headB` が収集）は 100 個の異なる値を含み、各元の seed が生む `seed*10+3` と `seed*10+4` がそれぞれ 1 回ずつ現れる。
- `Farm.SourceNodes` が**2 つ**の source SCC（`{rootA}` と `{rootB1, rootB2}`）を正しく識別し、各 source に対して `Seal()` をトリガーすることを検証する。

### `TestFarmStructure21FaninDifferentSpeed`

- トポロジー：`{rootFast, rootSlow}` → `head`。
- `rootSlow` は 1 回の処理につき 10ms sleep し、`rootFast` は sleep しない。両者とも `WithChanSize(50)` を使用する。
- それぞれ 50 個の seed。
- 期待：`head` は合計 100 個を受信し、各々がちょうど 1 回だけカウントされ、すべての plot の最終状態が `state == 2` である。
- 複数の source の速度差がある状況でも fan-in が正しく完了することを検証する（`Plot` の `chan` バッファと `sprout` の `select` スケジューリングに依存する）。

## 重要な詳細

- **複数の連結成分**：`TestFarmStructureDisconnectedComponents` は `SourceNodes` が**各々の** Source SCC から 1 つの代表ノードを取ることを間接的にカバーする——これは `Farm.Run` が `rootA`、`rootB1`、`rootB2` のすべてに `Seal()` を送信することを意味する。
- **fan-out / fan-in のカウント**：`counts[seed]` は `head` の `cultivator` に由来し、並行に呼び出されるため、テストでは `sync.Mutex` で保護する。
- **失敗のルーティング**：`basePlot.witherSeed` は失敗を記録するだけで、下流へは転送しない。`head` が受信する fruit 数と `midA.GetFruitNum() + midB.GetFruitNum()` は厳密に等しく、このアサーションは `TestFarmStructure121PartialFailure` で検証される。
- **チャネル設定**：`WithChanSize` は `TestFarmStructure21FaninDifferentSpeed` で明示的に大きくし、遅い source が速い source の書き込みをブロックすることを避ける。

## 関連ソース

- `pkg/farm/farm.go`：`Run`、`SourceNodes`、`AddPlot`、`Connect`
- `pkg/farm/graph.go`：`SourceNodes`、`TarjanSCC`
- `pkg/plot/plot_base.go`：`GetState` / `sprout` / `tend` / `witherSeed`
- `pkg/plot/plot.go`：`Plot.ripenSeed`
- `pkg/plot/counter.go`：`GetSeedNum` / `GetFruitNum` / `GetWeedNum`

## 実行方法

```bash
go test ./pkg/farm/ -run 'TestFarmStructure' -v
```
