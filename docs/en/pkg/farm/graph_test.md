# pkg/farm/graph_test.go

> 📅 Last Updated: 2026/09/24

## Purpose

`graph_test.go` covers the basic CRUD and graph algorithms of `OrderGraph` in `pkg/farm/graph.go` (TopoSort, ComputeNodeLevels, Tarjan SCC, SourceSCCs, GetCondensation, NodeToSCCIndex). These are the foundation of `Farm`'s topology analysis capabilities.

## Test Cases

### `TestOrderGraph_BasicOperations`

- Creates an `OrderGraph`; first `AddNode("isolated")`, then `AddEdge("a", "b")`, `AddEdge("a", "c")`, and a repeated `AddEdge("a", "b")`.
- Expectations:
  - `HasNode("isolated") == true`
  - `Nodes()` sorted is `["a", "b", "c", "isolated"]` (the node set order is not guaranteed, so the test calls `sort.Strings` first)
  - `Successors("a") == ["b", "c"]` (duplicate edges are ignored, order is insertion order)
  - `Predecessors("b") == ["a"]`

### `TestGraphAlgorithms_TopoSortAndLevels`

- Diamond graph `a → {b, c} → d`.
- Expectations:
  - `IsDAG == true`
  - `TopoSort == ["a", "b", "c", "d"]`
  - `SourceNodes == ["a"]`
  - `ComputeNodeLevels == {"a":0, "b":1, "c":1, "d":2}`

### `TestGraphAlgorithms_SCCAndCondensation`

- Graph: `a → b → a` (cycle) and `c → d → c` (cycle), with the two cycles connected through `b → c`.
- Expectations:
  - `IsDAG == false`
  - `TarjanSCC` yields `[["a","b"], ["c","d"]]` (compared after normalizing the order with `canonicalizeSCCs`)
  - `SourceSCCs == [["a","b"]]`
  - The condensation graph returned by `GetCondensation` has only the single edge `scc_0 → scc_1`, and the node names are `scc_0` / `scc_1`.
  - `NodeToSCCIndex` maps `a` and `b` to the same index and `c` and `d` to another index, and the two indices differ.

## Key Details

- **The canonicalizeSCCs helper**: the test uses this function to perform "internal sorting + overall sorting" on the `SCC` slices, avoiding fragile assertions that depend on the internal implementation details of `TarjanSCC`.
- **Condensation graph node naming**: `GetCondensation` names SCCs as `scc_0, scc_1, ...`; the test builds the corresponding name with `fmt.Sprintf("scc_%d", mapping["a"])`, providing an indirect assertion on `mapping`.
- **Order assertions**: `TestOrderGraph_BasicOperations` sorts `Nodes()` before comparing, relying only on the core guarantee of the **adjacency list insertion order** of `Successors` / `Predecessors`; the diamond graph of `TestGraphAlgorithms_TopoSortAndLevels` has only one zero-in-degree source node, so the `TopoSort` result is deterministic (on multi-source graphs, the order of same-level candidates is not guaranteed).

## Related Source

- All public symbols of `pkg/farm/graph.go`

## How to Run

```bash
go test ./pkg/farm/ -run 'TestOrderGraph|TestGraphAlgorithms' -v
```
