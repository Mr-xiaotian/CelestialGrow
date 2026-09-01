# pkg/farm/graph.go

> 最后更新日期: 2026/09/01

## 作用

`graph.go` 定义了 Farm 内部使用的最小有向图数据结构 `OrderGraph`，以及一组用于「拓扑排序 / 强连通分量 / 节点分层」的图算法。这些工具独立于 `Farm` 业务，可在其它需要稳定插入顺序与图分析的场景复用。

`OrderGraph` 的核心设计目标：

- **节点插入顺序稳定**：`Nodes()` 按调用 `AddNode` 的顺序返回，便于做稳定遍历和测试断言。
- **可承载非 DAG**：`AddEdge` 不做环检测，由算法层（`IsDAG` / `TopoSort`）按需判断。
- **可承载孤立节点**：`AddNode` 可独立于 `AddEdge` 存在。

## 核心对象

### `OrderGraph` 结构体

```go
type OrderGraph struct {
    nodes []string
    in    map[string][]string
    out   map[string][]string
}
```

| 字段 | 作用 |
| --- | --- |
| `nodes` | 按插入顺序保存的节点列表 |
| `in` | 入边邻接表：`in[v]` 是所有 `u → v` 的 `u` 列表 |
| `out` | 出边邻接表：`out[u]` 是所有 `u → v` 的 `v` 列表 |

> 邻接表是**按边插入顺序**累积的切片。`AddEdge` 会忽略重复边，因此顺序不会因重复添加而抖动。

## 公开符号

### 构造

| 符号 | 签名 | 用途 |
| --- | --- | --- |
| `NewOrderGraph` | `func NewOrderGraph() *OrderGraph` | 创建一个空图 |
| `NewOrderGraphFromEdges` | `func NewOrderGraphFromEdges(outEdges map[string][]string, stageNames []string) *OrderGraph` | 从邻接表与可选的「孤立节点列表」构建图 |

### 修改

| 符号 | 签名 | 用途 |
| --- | --- | --- |
| `AddNode` | `func (g *OrderGraph) AddNode(name string)` | 已存在则直接返回；否则按当前顺序追加 |
| `AddEdge` | `func (g *OrderGraph) AddEdge(from, to string)` | 自动补全端点；重复边被忽略 |

### 查询

| 符号 | 签名 | 用途 |
| --- | --- | --- |
| `Nodes` | `func (g *OrderGraph) Nodes() []string` | 返回节点名的深拷贝切片 |
| `OutEdges` | `func (g *OrderGraph) OutEdges() map[string][]string` | 返回出边邻接表的深拷贝 |
| `InEdges` | `func (g *OrderGraph) InEdges() map[string][]string` | 返回入边邻接表的深拷贝 |
| `HasNode` | `func (g *OrderGraph) HasNode(name string) bool` | 判断节点是否存在 |
| `Successors` | `func (g *OrderGraph) Successors(name string) []string` | 返回指定节点的后继深拷贝 |
| `Predecessors` | `func (g *OrderGraph) Predecessors(name string) []string` | 返回指定节点的前驱深拷贝 |
| `Connected` | `func (g *OrderGraph) Connected(from, to string) bool` | 判断一条有向边是否存在 |
| `String` | `func (g *OrderGraph) String() string` | 返回 `OrderGraph(nodes=N, edges=M)` 形式摘要 |

### 拓扑算法

| 符号 | 签名 | 用途 |
| --- | --- | --- |
| `InDegree` | `func InDegree(graph *OrderGraph) map[string]int` | 计算每个节点的入度 |
| `IsDAG` | `func IsDAG(graph *OrderGraph) bool` | Kahn 算法判断是否 DAG |
| `TopoSort` | `func TopoSort(graph *OrderGraph) []string` | DAG 时返回稳定拓扑序；存在环返回 `nil` |
| `ComputeNodeLevels` | `func ComputeNodeLevels(graph *OrderGraph) (map[string]int, error)` | 先做 SCC 凝聚，再在凝聚 DAG 上按 `max(前驱 level + 1)` 传播，返回每个节点的「最早执行层级」；凝聚图非 DAG 时返回 error |

### 强连通分量

| 符号 | 签名 | 用途 |
| --- | --- | --- |
| `TarjanSCC` | `func TarjanSCC(graph *OrderGraph) [][]string` | Tarjan 算法求 SCC；返回顺序为凝聚图的「逆拓扑序」 |
| `NodeToSCCIndex` | `func NodeToSCCIndex(sccs [][]string) map[string]int` | 构造节点 → SCC 索引的映射 |
| `GetCondensation` | `func GetCondensation(graph *OrderGraph) (*OrderGraph, [][]string)` | 构造凝聚图，节点命名为 `scc_0, scc_1, ...`，自动去重跨 SCC 边 |
| `SourceSCCs` | `func SourceSCCs(graph *OrderGraph) [][]string` | 返回凝聚图中入度为 0 的 SCC 列表 |
| `SourceNodes` | `func SourceNodes(graph *OrderGraph) []string` | 从每个 Source SCC 中取代表节点（`scc[0]`） |

## 关键流程

### Tarjan SCC + 凝聚图

```mermaid
flowchart LR
    subgraph G[原图]
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

`SourceNodes` 在 `Farm.Run` 中用于决定哪些 plot 需要 `Seal()`。每个 Source SCC 只取一个代表，避免在强连通环上重复 seal。

### 节点层级计算

`ComputeNodeLevels` 用于按「最早可执行层级」对节点排序：

1. `GetCondensation` 将原图压成 DAG。
2. 在 DAG 上做 `TopoSort`（非 DAG 时直接返回 error）。
3. 按 `level[succ] = max(level[succ], level[from] + 1)` 沿出边传播。
4. 把 SCC 的 level 展开回所有原节点。

> 同一 SCC 内所有节点共享同一 level。

## 重要细节

- **稳定顺序 = 切片顺序**：所有 `OutEdges` / `InEdges` / `Successors` / `Predecessors` 均按 `AddEdge` 的调用顺序累积，重复 `AddEdge` 不会改变顺序。这让 `TopoSort` / `SourceNodes` 的结果可被稳定断言。
- **重复边静默忽略**：`AddEdge` 通过线性扫描 `out[from]` 查重；环不会因此被消除，只是不会产生重复邻接项。
- **`TopoSort` 在非 DAG 时返回 `nil`**：调用方需自己检查；同时 `ComputeNodeLevels` 依赖 `TopoSort` 拿到 `nil` 顺序时返回 `condensation graph must be a DAG`。
- **`SourceNodes` 与 `AddNode` 顺序耦合**：`TarjanSCC` 的递归起点使用 `g.nodes` 切片顺序；因此从 `SourceSCCs` 取 `scc[0]` 的代表节点选取规则也间接受 `AddNode` 顺序影响。
- **`OrderGraph` 不做并发安全保护**：所有方法假定单线程使用；`Farm` 自身在 `Run` 前完成全部 `AddPlot` / `Connect`，`Run` 期间不再修改图。
- **图与 Farm 关系**：`Farm` 嵌入 `*OrderGraph`，因此 `Farm.Connected(from, to)` 直接代理到 `OrderGraph.Connected`；`Farm` 的图视图只通过 `AddPlot`（`AddNode`）和 `Connect`（`AddEdge`）演化。

## 使用示例

构造一个简单的菱形图并验证算法：

```go
g := farm.NewOrderGraphFromEdges(map[string][]string{
    "a": {"b", "c"},
    "b": {"d"},
    "c": {"d"},
}, nil)

fmt.Println(farm.IsDAG(g))         // true
fmt.Println(farm.TopoSort(g))      // [a b c d]（按 AddNode 顺序）
fmt.Println(farm.SourceNodes(g))  // [a]

levels, err := farm.ComputeNodeLevels(g)
// levels = {"a":0, "b":1, "c":1, "d":2}
```

带环的图：

```go
g := farm.NewOrderGraphFromEdges(map[string][]string{
    "a": {"b"},
    "b": {"a", "c"},
    "c": {"d"},
    "d": {"c"},
}, nil)

fmt.Println(farm.IsDAG(g))    // false
fmt.Println(farm.TarjanSCC(g)) // [[a b] [c d]]
fmt.Println(farm.SourceSCCs(g)) // [[a b]]
```

## 注意事项

- **仅做图分析，不做调度**：`OrderGraph` 不感知任何运行时状态（如 `Plot` 的 `GetState`），所有调度由 `Farm.Run` 主导。
- **测试覆盖**：`pkg/farm/graph_test.go` 覆盖了基础 CRUD、稳定插入顺序、TopoSort、ComputeNodeLevels、Tarjan SCC 与凝聚图命名。详见 `graph_test.md`。
- **不是线程安全**：如需在多协程中读取图，请在外部加锁或先 `OutEdges` / `InEdges` 拿到深拷贝后再并发使用。
