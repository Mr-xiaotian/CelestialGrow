# pkg/farm/farm_route_test.go

> 📅 最終更新日: 2026/09/24

## 役割

`farm_route_test.go` は `package farm_test` として、`RoutePlot` の `Farm` におけるエンドツーエンドの振る舞いをカバーする。`Farm.Connect` が `route → {left, right}` の多重下流接続を確立した後、`Run` がルートテーブルに従って yield を対応する下流へ転送できるかを検証する。

## テストの重点

- **有向転送**：同一の上流 seed を `map[string]Y` ルートテーブルの key に従って異なる下流へ振り分ける。
- **下流の seed カウント**：`left` / `right` の `GetSeedNum` は上流の yield カウンタに由来する（本ケースでは両者ともローカルで種子を播かない）。`ConnectTo` のエッジをまたぐ yield カウントの配線を検証する。
- **未接続のターゲットは静かにスキップされる**：未接続の下流へのルーティングは他のルート項目に影響せず、上流もブロックしない。
- **順序に依存しないアサーション**：並行転送下では下流の受信順序を仮定しない。

## テストケース

| テスト関数 | 検証点 |
| --- | --- |
| `TestFarmRunRoutePlot` | `route`（`int → map[string]string`）が偶奇で振り分ける：偶数 → `left`、奇数 → `right`。4 つの seed の後、`route.GetFruitNum() == 4`、`left` / `right` はそれぞれ `GetSeedNum() == 2` であり、実際に受信した seed 集合はそれぞれ `{even-2, even-4}` と `{odd-1, odd-3}` である |
| `TestFarmRunRoutePlotSkipsUnconnectedTarget` | ルートテーブルが接続済みの `left` と未登録の `ghost` を同時に含む。`left` のみが `kept-1` を受信し、`left.GetSeedNum() == 1` となり、`ghost` は静かにスキップされる |

## 重要な詳細

- **並行収集のロック**：両方のケースは `sync.Mutex` で下流の `received` map / スライスを保護する。`RoutePlot.ripenSeed` は複数の tender コルーチン上で並行に実行されるためである。
- **`assertSeeds` ヘルパー関数**：まず実収集の集合をソートしてから項目ごとに比較し、下流の並行受信順序に依存することを避ける。
- **上流の yield カウントに依存**：`left` / `right` にはローカルの `Seed` がなく、その `GetSeedNum` は完全に上流が `upstreamYieldCounter` に登録したカウンタから寄与される。これは「per-edge の下流 yield カウント」へのリファクタリングに対する直接の回帰テストである。
- **`WithTenders(2)`**：ルートノードと下流はいずれも明示的に 2 つの tender を設定し、並行カバレッジを高める。

## 関連ソース

- `pkg/farm/farm.go` の `Connect` / `Run`
- `pkg/farm/graph.go` の `SourceNodes` / `AddEdge`
- `pkg/plot/plot_route.go` の `RoutePlot.ripenSeed`（未接続の下流をスキップする）
- `pkg/plot/plot_base.go` の `ConnectTo`（yield カウンタの双方向配線）
- `pkg/plot/counter.go` の `GetSeedNum` / `GetFruitNum`

## 実行方法

```bash
go test ./pkg/farm/ -run 'TestFarmRunRoutePlot' -v
```
