# pkg/farm/graph.go

> 📅 Last Updated: 2026/09/24

## Purpose

`graph.go` defines `OrderGraph`, the minimal directed graph data structure used internally by Farm, along with a set of graph algorithms for "topological sorting / strongly connected components / node layering". These tools are independent of the `Farm` business logic and can be reused in other scenarios that need graph analysis.

The core design goals of `OrderGraph`:

- **Stable adjacency list order**: the successor / predecessor list of each node accumulates in the order its edges are added by `AddEdge`.
- **Can hold non-DAGs**: `AddEdge` performs no cycle detection; the algorithm layer (`IsDAG` / `TopoSort`) decides as needed.
- **Can hold isolated nodes**: `AddNode` can exist independently of `AddEdge`.

> The name comes from the orderability of the graph: topological levels, reachability and SCC structures are all deterministic; but **the traversal order of the nodes themselves is not guaranteed** (the node set is stored in a map).

## Core Objects

### The `OrderGraph` struct

```go
type OrderGraph struct {
    in  map[string][]string
    out map[string][]string
}
```

| Field | Purpose |
| --- | --- |
| `in` | In-edge adjacency list: `in[v]` is the list of all `u` such that `u → v` |
| `out` | Out-edge adjacency list: `out[u]` is the list of all `v` such that `u → v` |

> The adjacency lists are slices accumulated **in edge insertion order**. `AddEdge` ignores duplicate edges, so the order does not jitter due to repeated additions. The key sets of the two maps are always in sync — `AddNode` guarantees that every node has an entry in both `in` and `out`.

## Public Symbols

### Construction

| Symbol | Signature | Purpose |
| --- | --- | --- |
| `NewOrderGraph` | `func NewOrderGraph() *OrderGraph` | Creates an empty graph |

### Mutation

| Symbol | Signature | Purpose |
| --- | --- | --- |
| `AddNode` | `func (g *OrderGraph) AddNode(name string)` | Returns directly if it already exists; otherwise registers an empty adjacency list in `in` / `out` |
| `AddEdge` | `func (g *OrderGraph) AddEdge(from, to string)` | Auto-completes endpoints; duplicate edges are ignored |

### Queries

| Symbol | Signature | Purpose |
| --- | --- | --- |
| `Nodes` | `func (g *OrderGraph) Nodes() []string` | Returns all node names (order not guaranteed) |
| `OutEdges` | `func (g *OrderGraph) OutEdges() map[string][]string` | Returns a deep copy of the out-edge adjacency list |
| `InEdges` | `func (g *OrderGraph) InEdges() map[string][]string` | Returns a deep copy of the in-edge adjacency list |
| `HasNode` | `func (g *OrderGraph) HasNode(name string) bool` | Reports whether a node exists |
| `Successors` | `func (g *OrderGraph) Successors(name string) []string` | Returns a deep copy of the named node's successors in insertion order |
| `Predecessors` | `func (g *OrderGraph) Predecessors(name string) []string` | Returns a deep copy of the named node's predecessors in insertion order |
| `Connected` | `func (g *OrderGraph) Connected(from, to string) bool` | Reports whether a directed edge exists |
| `String` | `func (g *OrderGraph) String() string` | Returns a summary of the form `OrderGraph(nodes=N, edges=M)` |

### Topological Algorithms

| Symbol | Signature | Purpose |
| --- | --- | --- |
| `InDegree` | `func InDegree(g *OrderGraph) map[string]int` | Computes the in-degree of each node |
| `IsDAG` | `func IsDAG(g *OrderGraph) bool` | Uses Kahn's algorithm to determine whether it is a DAG |
| `TopoSort` | `func TopoSort(g *OrderGraph) []string` | Returns a topological order for a DAG; returns `nil` if a cycle exists |
| `ComputeNodeLevels` | `func ComputeNodeLevels(graph *OrderGraph) (map[string]int, error)` | First performs SCC contraction, then propagates `max(predecessor level + 1)` on the condensation DAG, returning the "earliest execution level" of each node; returns an error if the condensation graph is not a DAG |

### Strongly Connected Components

| Symbol | Signature | Purpose |
| --- | --- | --- |
| `TarjanSCC` | `func TarjanSCC(graph *OrderGraph) [][]string` | Uses Tarjan's algorithm to find SCCs; the returned order is the "reverse topological order" of the condensation graph |
| `NodeToSCCIndex` | `func NodeToSCCIndex(sccs [][]string) map[string]int` | Builds a node → SCC index mapping |
| `GetCondensation` | `func GetCondensation(graph *OrderGraph) (*OrderGraph, [][]string)` | Builds the condensation graph with nodes named `scc_0, scc_1, ...`, automatically deduplicating cross-SCC edges |
| `SourceSCCs` | `func SourceSCCs(graph *OrderGraph) [][]string` | Returns the list of SCCs with in-degree 0 in the condensation graph |
| `SourceNodes` | `func SourceNodes(graph *OrderGraph) []string` | Takes a representative node (`scc[0]`) from each Source SCC |

## Key Flows

### Tarjan SCC + condensation graph

```mermaid
flowchart LR
    subgraph G[Original graph]
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

`SourceNodes` is used in `Farm.Run` to decide which plots need `Seal()`. Only one representative is taken per Source SCC, avoiding repeated seals on a strongly connected cycle.

### Node level computation

`ComputeNodeLevels` sorts nodes by "earliest executable level":

1. `GetCondensation` compresses the original graph into a DAG.
2. `TopoSort` is performed on the DAG (returns an error directly if not a DAG).
3. Propagate along out-edges via `level[succ] = max(level[succ], level[from] + 1)`.
4. Expand each SCC's level back to all original nodes.

> All nodes within the same SCC share the same level.

## Important Details

- **Adjacency list order = edge insertion order**: each entry of `OutEdges` / `InEdges` / `Successors` / `Predecessors` accumulates in `AddEdge` call order, and repeated `AddEdge` does not change the order.
- **The node set itself is unordered**: `Nodes()` iterates the internal map directly, so order is not guaranteed; the initial zero-in-degree queue of `IsDAG` / `TopoSort` likewise comes from map iteration. Therefore on graphs with multiple source nodes, `TopoSort` is only deterministic in terms of "topological validity"; the relative order of candidates at the same level is not guaranteed.
- **Duplicate edges silently ignored**: `AddEdge` checks for duplicates by a linear scan of `out[from]`; cycles are not thereby eliminated, only duplicate adjacency entries are avoided.
- **`TopoSort` returns `nil` for non-DAGs**: the caller must check this itself; `ComputeNodeLevels` relies on `TopoSort` and returns `condensation graph must be a DAG` when it gets a `nil` order.
- **`TarjanSCC`'s starting point order is nondeterministic**: it iterates the `g.out` map to decide the recursion starting point; `SourceSCCs` / `SourceNodes` take `scc[0]` as the representative, so the choice of representative node is affected by map iteration and stack order and is not stable across runs.
- **`OrderGraph` provides no concurrency safety**: all methods assume single-threaded use; `Farm` itself completes all `AddPlot` / `Connect` before `Run` and does not modify the graph during `Run`.
- **Relationship between the graph and Farm**: `Farm` embeds `*OrderGraph`, so `Farm.Connected(from, to)` delegates directly to `OrderGraph.Connected`; Farm's graph view evolves only through `AddPlot` (`AddNode`) and `Connect` (`AddEdge`).

## Usage Example

Construct a simple diamond graph and verify the algorithms:

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

A graph with cycles:

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

## Notes

- **Graph analysis only, no scheduling**: `OrderGraph` is unaware of any runtime state (such as `Plot`'s `GetState`); all scheduling is driven by `Farm.Run`.
- **Test coverage**: `pkg/farm/graph_test.go` covers basic CRUD, adjacency order stability, TopoSort, ComputeNodeLevels, Tarjan SCC and condensation graph naming. See `graph_test.md` for details.
- **Not thread-safe**: to read the graph from multiple goroutines, lock externally or obtain deep copies via `OutEdges` / `InEdges` first and then use them concurrently.
