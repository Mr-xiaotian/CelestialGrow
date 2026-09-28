# pkg/farm/farm_connect_test.go

> 📅 最終更新日: 2026/09/24

## 役割

`farm_connect_test.go` は `pkg/farm/farm.go` における**登録（`AddPlot`）**と**接続（`Connect`）**経路の意味論的な正しさをカバーする。テストファイルは `package farm_test` の形で `pkg/farm` の公開シンボルを通じて振る舞いを検証しており、`Farm` 公開 API の「ブラックボックス契約」となっている。

## テストケース

| テスト関数 | 検証点 |
| --- | --- |
| `TestFarmAddPlot` | 複数の plot を一度に登録する。`PlotCount` / `HasPlot` / `GetPlot` がすべて一致する。登録後にエッジは**確立されていない** |
| `TestFarmAddPlotDuplicateName` | 名称が重複する plot の 2 回目の `AddPlot` が error を返す |
| `TestFarmConnectHyperEdge` | `Connect` は重複するターゲット（例：`targetA` が 2 回現れる）を自動的に重複排除する。`source` はすべての target へ正しく接続されるが、逆向きのエッジは存在しない |
| `TestFarmConnectTypeMismatch` | 上流の `F` と下流の `S` の型が一致しない場合に `Connect` が error を返す（`ConnectTo` のアサーション失敗をそのまま伝播する） |

## 重要なアサーション

- `TestFarmAddPlot` は `GetPlot` で得たポインタが元の `plot` と一致することも検証する——`Farm.plots` はポインタで保持され、値のコピーは行われない。
- `TestFarmConnectHyperEdge` で重複して現れる `targetA` によって `source → targetA` が 2 回張られることはない。内部の `uniquePlots` が `Connect` を呼び出す前に重複排除を完了する。
- `TestFarmConnectTypeMismatch` では上流と下流の `Plot[S, F]` の `F` と `S` が異なるため、`ConnectTo` 内の `next.GetSeedChanAny()` に対する `.(chan runtime.Payload[Y])` 型アサーションが失敗する。したがって `Connect` 全体が error を返し、`OrderGraph` には `AddEdge` の記録が残らない。

## 関連ソース

- `AddPlot` のエラー返却：`pkg/farm/farm.go` の `AddPlot` / `requireRegistered`
- `Connect` のエラー返却：`pkg/farm/farm.go` の `Connect` / `uniquePlots` / `requireRegistered`
- 型アサーション：`pkg/plot/plot_base.go` の `basePlot.ConnectTo`

## 実行方法

```bash
go test ./pkg/farm/ -run 'TestFarmAdd|TestFarmConnect' -v
```
