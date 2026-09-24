# pkg/farm/farm_split_test.go

> 📅 最終更新日: 2026/09/24

## 役割

`farm_split_test.go` は `package farm_test` として、`SplitPlot` の `Farm` におけるエンドツーエンドの振る舞いをカバーする。上流の seed が複数の下流 yield へ分割された後、`Farm.Run` がすべての要素を 1 つずつ下流へ転送するかを検証する。

## テストの重点

- **1 対多の分割転送**：1 回の `SplitPlot` が複数の下流 yield を正常に産出した場合、下流の seed 数は分割要素の総和となるべきである。
- **カウント意味論の区別**：`SplitPlot.GetFruitNum` は「入力 seed 数」でカウントする（1 回の分割につき 1 つの fruit を数える）のであり、分割要素数でカウントするのではない。下流の `head.GetSeedNum` のほうが要素の総数を反映する。
- **空の分割結果**：分割関数が `nil` スライスを返した場合、下流へは何も転送しない。

## テストケース

| テスト関数 | 検証点 |
| --- | --- |
| `TestFarmRunSplitPlot` | `split`（`string → []int`、カンマで分割）と `head`（`int → int`、`seed*10`）。入力 `{"1,2,3", "4,5"}` の後：`split.GetFruitNum() == 2`（2 回の分割成功）、`head.GetSeedNum() == 5`、`head.GetFruitNum() == 5` |

## 重要な詳細

- **分割関数が空入力をカバー**：`split` は空文字列に対して `nil, nil` を返し、「分割結果が空」の分岐（yield を下流へ送らない）をカバーする。
- **数値パースのエラー**：分割の過程で `strconv.Atoi` が失敗すると error を返し、`SplitPlot` の withered 経路を通る。本ケースの入力はすべて正当な整数であり、純粋な成功経路を検証する。
- **`head` の fan-in**：`head` は `WithTenders(2)` を使用し、`split` から来る複数の yield を並行に処理するため、`head.GetSeedNum` は上流 yield カウンタの正しい登録に依存する。

## 関連ソース

- `pkg/farm/farm.go` の `Connect` / `Run`
- `pkg/plot/plot_split.go` の `SplitPlot.ripenSeed`（要素ごとに下流 yield を送信する）
- `pkg/plot/counter.go` の `GetSeedNum` / `GetFruitNum` / `AddDownstreamYieldNum`

## 実行方法

```bash
go test ./pkg/farm/ -run 'TestFarmRunSplitPlot' -v
```
