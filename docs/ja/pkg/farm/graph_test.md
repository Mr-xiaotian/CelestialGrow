# pkg/farm/graph_test.go

> 📅 最終更新日: 2026/09/24

## 役割

`graph_test.go` は `pkg/farm/graph.go` における `OrderGraph` の基本的な CRUD とグラフアルゴリズム（TopoSort、ComputeNodeLevels、Tarjan SCC、SourceSCCs、GetCondensation、NodeToSCCIndex）をカバーする。これらは `Farm` のトポロジー分析能力の基盤である。

## テストケース

### `TestOrderGraph_BasicOperations`

- `OrderGraph` を作成する。まず `AddNode("isolated")` し、次に `AddEdge("a", "b")`、`AddEdge("a", "c")` を行い、`AddEdge("a", "b")` をもう一度繰り返す。
- 期待：
  - `HasNode("isolated") == true`
  - `Nodes()` はソートすると `["a", "b", "c", "isolated"]` になる（ノード集合の順序は保証されないため、テストでは先に `sort.Strings` する）
  - `Successors("a") == ["b", "c"]`（重複エッジは無視され、順序は挿入順である）
  - `Predecessors("b") == ["a"]`

### `TestGraphAlgorithms_TopoSortAndLevels`

- 菱形グラフ `a → {b, c} → d`。
- 期待：
  - `IsDAG == true`
  - `TopoSort == ["a", "b", "c", "d"]`
  - `SourceNodes == ["a"]`
  - `ComputeNodeLevels == {"a":0, "b":1, "c":1, "d":2}`

### `TestGraphAlgorithms_SCCAndCondensation`

- グラフ：`a → b → a`（サイクル）と `c → d → c`（サイクル）があり、`b → c` によって 2 つのサイクルが接続されている。
- 期待：
  - `IsDAG == false`
  - `TarjanSCC` は `[["a","b"], ["c","d"]]` を与える（順序は `canonicalizeSCCs` で正規化してから比較する）
  - `SourceSCCs == [["a","b"]]`
  - `GetCondensation` が返す凝縮グラフには `scc_0 → scc_1` の 1 本のエッジのみがあり、ノード名は `scc_0` / `scc_1` である。
  - `NodeToSCCIndex` は `a`、`b` を同一の索引へ、`c`、`d` を別の索引へマッピングし、2 つの索引は異なる。

## 重要な詳細

- **canonicalizeSCCs ヘルパー関数**：テストではこの関数によって `SCC` スライスを「内部のソート + 全体のソート」し、`TarjanSCC` の内部実装の詳細に依存して脆弱なアサーションになるのを避ける。
- **凝縮グラフのノード命名**：`GetCondensation` は SCC を `scc_0, scc_1, ...` と命名する。テストでは `fmt.Sprintf("scc_%d", mapping["a"])` によって対応する名前を組み立て、`mapping` への間接的なアサーションを保証する。
- **順序のアサーション**：`TestOrderGraph_BasicOperations` は `Nodes()` を先にソートしてから比較し、`Successors` / `Predecessors` の**隣接リストの挿入順**という中核の保証のみに依存する。`TestGraphAlgorithms_TopoSortAndLevels` の菱形グラフは入次数ゼロのソースノードが 1 つだけであるため、`TopoSort` の結果は確定する（多ソースのグラフでは同層の候補順序は保証されない）。

## 関連ソース

- `pkg/farm/graph.go` のすべての公開シンボル

## 実行方法

```bash
go test ./pkg/farm/ -run 'TestOrderGraph|TestGraphAlgorithms' -v
```
