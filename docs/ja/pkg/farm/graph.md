# pkg/farm/graph.go

> 📅 最終更新日: 2026/09/24

## 役割

`graph.go` は Farm 内部で用いる最小限の有向グラフデータ構造 `OrderGraph` と、「トポロジカルソート / 強連結成分 / ノードの階層化」のための一連のグラフアルゴリズムを定義する。これらのツールは `Farm` の業務から独立しており、グラフ分析を必要とする他の場面でも再利用できる。

`OrderGraph` の中核となる設計目標：

- **隣接リストの順序が安定**：各ノードの後続 / 先行リストは、そのエッジが `AddEdge` で追加された順に累積される。
- **非 DAG を扱える**：`AddEdge` はサイクル検出を行わず、アルゴリズム層（`IsDAG` / `TopoSort`）が必要に応じて判定する。
- **孤立ノードを扱える**：`AddNode` は `AddEdge` とは独立に存在できる。

> 名称はグラフの整列可能性に由来する。トポロジカルな階層、到達可能性、SCC といった依存構造はいずれも確定するが、**ノード自体の走査順序は保証されない**（ノード集合は map に格納される）。

## 中核オブジェクト

### `OrderGraph` 構造体

```go
type OrderGraph struct {
    in  map[string][]string
    out map[string][]string
}
```

| フィールド | 役割 |
| --- | --- |
| `in` | 入エッジ隣接リスト：`in[v]` はすべての `u → v` の `u` のリスト |
| `out` | 出エッジ隣接リスト：`out[u]` はすべての `u → v` の `v` のリスト |

> 隣接リストは**エッジの挿入順**で累積されるスライスである。`AddEdge` は重複エッジを無視するため、重複追加によって順序が変動することはない。2 つの map のキー集合は常に同期する——`AddNode` は各ノードが `in` と `out` の両方にエントリを持つことを保証する。

## 公開シンボル

### 構築

| シンボル | シグネチャ | 用途 |
| --- | --- | --- |
| `NewOrderGraph` | `func NewOrderGraph() *OrderGraph` | 空のグラフを生成する |

### 変更

| シンボル | シグネチャ | 用途 |
| --- | --- | --- |
| `AddNode` | `func (g *OrderGraph) AddNode(name string)` | 既に存在すればそのまま返す。そうでなければ `in` / `out` に空の隣接リストを登録する |
| `AddEdge` | `func (g *OrderGraph) AddEdge(from, to string)` | 端点を自動補完する。重複エッジは無視される |

### 照会

| シンボル | シグネチャ | 用途 |
| --- | --- | --- |
| `Nodes` | `func (g *OrderGraph) Nodes() []string` | すべてのノード名を返す（順序は保証されない） |
| `OutEdges` | `func (g *OrderGraph) OutEdges() map[string][]string` | 出エッジ隣接リストのディープコピーを返す |
| `InEdges` | `func (g *OrderGraph) InEdges() map[string][]string` | 入エッジ隣接リストのディープコピーを返す |
| `HasNode` | `func (g *OrderGraph) HasNode(name string) bool` | ノードが存在するかを判定する |
| `Successors` | `func (g *OrderGraph) Successors(name string) []string` | 指定ノードの後続のディープコピーを挿入順で返す |
| `Predecessors` | `func (g *OrderGraph) Predecessors(name string) []string` | 指定ノードの先行のディープコピーを挿入順で返す |
| `Connected` | `func (g *OrderGraph) Connected(from, to string) bool` | 有向エッジが存在するかを判定する |
| `String` | `func (g *OrderGraph) String() string` | `OrderGraph(nodes=N, edges=M)` 形式のサマリを返す |

### トポロジーアルゴリズム

| シンボル | シグネチャ | 用途 |
| --- | --- | --- |
| `InDegree` | `func InDegree(g *OrderGraph) map[string]int` | 各ノードの入次数を計算する |
| `IsDAG` | `func IsDAG(g *OrderGraph) bool` | Kahn のアルゴリズムで DAG かどうかを判定する |
| `TopoSort` | `func TopoSort(g *OrderGraph) []string` | DAG ならトポロジカル順序を 1 つ返す。サイクルが存在すれば `nil` を返す |
| `ComputeNodeLevels` | `func ComputeNodeLevels(graph *OrderGraph) (map[string]int, error)` | まず SCC 凝縮を行い、凝縮 DAG 上で `max(先行の level + 1)` に従って伝播し、各ノードの「最早実行階層」を返す。凝縮グラフが非 DAG の場合は error を返す |

### 強連結成分

| シンボル | シグネチャ | 用途 |
| --- | --- | --- |
| `TarjanSCC` | `func TarjanSCC(graph *OrderGraph) [][]string` | Tarjan のアルゴリズムで SCC を求める。返される順序は凝縮グラフの「逆トポロジカル順」である |
| `NodeToSCCIndex` | `func NodeToSCCIndex(sccs [][]string) map[string]int` | ノード → SCC 索引のマッピングを構築する |
| `GetCondensation` | `func GetCondensation(graph *OrderGraph) (*OrderGraph, [][]string)` | 凝縮グラフを構築する。ノード名は `scc_0, scc_1, ...` とし、SCC をまたぐエッジを自動的に重複排除する |
| `SourceSCCs` | `func SourceSCCs(graph *OrderGraph) [][]string` | 凝縮グラフにおいて入次数がゼロの SCC のリストを返す |
| `SourceNodes` | `func SourceNodes(graph *OrderGraph) []string` | 各 Source SCC から代表ノード（`scc[0]`）を取る |

## 主要フロー

### Tarjan SCC + 凝縮グラフ

```mermaid
flowchart LR
    subgraph G[元のグラフ]
        a --> b
        b --> a
        b --> c
        c --> d
        d --> c
    end
    G -->|TarjanSCC| S[SCCs:<br/>{a,b} {c,d}]
    S -->|GetCondensation| C[Condensation<br/>scc_0 → scc_1]
    C -->|SourceSCCs| SS[SourceSCCs:<br/>{a,b}]
    C -->|SourceNodes| SN[SourceNodes:<br/>a]
```

`SourceNodes` は `Farm.Run` において、どの plot が `Seal()` を必要とするかを決めるために用いられる。各 Source SCC からは代表を 1 つだけ取ることで、強連結なサイクル上で seal が重複するのを避ける。

### ノード階層の計算

`ComputeNodeLevels` は「最早実行可能な階層」でノードを並べるために用いられる：

1. `GetCondensation` が元のグラフを DAG へ圧縮する。
2. DAG 上で `TopoSort` を行う（非 DAG の場合は直ちに error を返す）。
3. `level[succ] = max(level[succ], level[from] + 1)` に従って出エッジに沿って伝播する。
4. SCC の level をすべての元ノードへ展開し戻す。

> 同一 SCC 内のすべてのノードは同じ level を共有する。

## 重要な詳細

- **隣接リストの順序 = エッジの挿入順**：`OutEdges` / `InEdges` / `Successors` / `Predecessors` の各項目の内部はすべて `AddEdge` の呼び出し順で累積され、`AddEdge` を重複しても順序は変わらない。
- **ノード集合自体は無順序**：`Nodes()` は内部の map を直接走査するため、順序は保証されない。`IsDAG` / `TopoSort` の初期の入次数ゼロのキューも同様に map のイテレーションに由来する。したがって複数のソースノードを含むグラフでは、`TopoSort` は「トポロジーとしての正当性」においてのみ確定し、同層の候補の相対順序は保証されない。
- **重複エッジは静かに無視される**：`AddEdge` は `out[from]` を線形走査して重複を調べる。サイクルがこれによって消えることはなく、単に重複した隣接項目が生じないだけである。
- **`TopoSort` は非 DAG のとき `nil` を返す**：呼び出し側が自ら確認する必要がある。`ComputeNodeLevels` は `TopoSort` が `nil` の順序を返した場合に `condensation graph must be a DAG` を返すことに依存している。
- **`TarjanSCC` の起点順序は不定**：これは `g.out` map を走査して再帰の起点を決める。`SourceSCCs` / `SourceNodes` はそこから `scc[0]` を代表として取るため、代表ノードの選出は map のイテレーションとスタックの順序に影響され、実行をまたいだ安定性はない。
- **`OrderGraph` は並行安全の保護を行わない**：すべてのメソッドは単一スレッドでの使用を前提とする。`Farm` 自身は `Run` の前にすべての `AddPlot` / `Connect` を完了し、`Run` の間はグラフを変更しない。
- **グラフと Farm の関係**：`Farm` は `*OrderGraph` を埋め込むため、`Farm.Connected(from, to)` は `OrderGraph.Connected` に直接委譲される。`Farm` のグラフビューは `AddPlot`（`AddNode`）と `Connect`（`AddEdge`）を通じてのみ変化する。

## 使用例

簡単な菱形グラフを構築し、アルゴリズムを検証する：

```go
g := farm.NewOrderGraph()
g.AddEdge("a", "b")
g.AddEdge("a", "c")
g.AddEdge("b", "d")
g.AddEdge("c", "d")

fmt.Println(farm.IsDAG(g))       // true
fmt.Println(farm.TopoSort(g))    // [a b c d]（单一源时确定；多源时同层顺序不保证）
fmt.Println(farm.SourceNodes(g)) // [a]

levels, err := farm.ComputeNodeLevels(g)
// levels = {"a":0, "b":1, "c":1, "d":2}
```

サイクルを持つグラフ：

```go
g := farm.NewOrderGraph()
g.AddEdge("a", "b")
g.AddEdge("b", "a")
g.AddEdge("b", "c")
g.AddEdge("c", "d")
g.AddEdge("d", "c")

fmt.Println(farm.IsDAG(g))      // false
fmt.Println(farm.TarjanSCC(g))  // [[a b] [c d]]（SCC 内与整体顺序均不保证）
fmt.Println(farm.SourceSCCs(g)) // [[a b]]
```

## 注意事項

- **グラフ分析のみを行い、スケジューリングは行わない**：`OrderGraph` はランタイム状態（`Plot` の `GetState` など）を一切感知せず、すべてのスケジューリングは `Farm.Run` が主導する。
- **テストカバレッジ**：`pkg/farm/graph_test.go` は基本的な CRUD、隣接順序の安定性、TopoSort、ComputeNodeLevels、Tarjan SCC と凝縮グラフの命名をカバーする。詳しくは `graph_test.md` を参照。
- **スレッドセーフではない**：複数のコルーチンでグラフを読む必要がある場合は、外部でロックするか、先に `OutEdges` / `InEdges` でディープコピーを取得してから並行に使用すること。
