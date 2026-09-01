# pkg/farm/graph_test.go

> 最后更新日期: 2026/09/01

## 作用

`graph_test.go` 覆盖 `pkg/farm/graph.go` 中 `OrderGraph` 的基础 CRUD 与图算法（TopoSort、ComputeNodeLevels、Tarjan SCC、SourceSCCs、GetCondensation、NodeToSCCIndex）。这些是 `Farm` 拓扑分析能力的基础。

## 测试用例

### `TestOrderGraph_BasicOperations`

- 创建 `OrderGraph`；先 `AddNode("isolated")`，再 `AddEdge("a", "b")`、`AddEdge("a", "c")`，并重复一次 `AddEdge("a", "b")`。
- 期望：
  - `HasNode("isolated") == true`
  - `Nodes() == ["isolated", "a", "b", "c"]`（按插入顺序）
  - `Successors("a") == ["b", "c"]`（重复边被忽略）
  - `Predecessors("b") == ["a"]`

### `TestGraphAlgorithms_TopoSortAndLevels`

- 菱形图 `a → {b, c} → d`。
- 期望：
  - `IsDAG == true`
  - `TopoSort == ["a", "b", "c", "d"]`
  - `SourceNodes == ["a"]`
  - `ComputeNodeLevels == {"a":0, "b":1, "c":1, "d":2}`

### `TestGraphAlgorithms_SCCAndCondensation`

- 图：`a → b → a`（环）与 `c → d → c`（环），并通过 `b → c` 连接两个环。
- 期望：
  - `IsDAG == false`
  - `TarjanSCC` 给出 `[["a","b"], ["c","d"]]`（顺序经 `canonicalizeSCCs` 规范化后比较）
  - `SourceSCCs == [["a","b"]]`
  - `GetCondensation` 返回的凝聚图只有 `scc_0 → scc_1` 一条边，且节点名为 `scc_0` / `scc_1`。
  - `NodeToSCCIndex` 把 `a`、`b` 映射到同一索引，`c`、`d` 映射到另一索引，且两个索引不同。

## 关键细节

- **canonicalizeSCCs 辅助函数**：测试中通过该函数对 `SCC` 切片做「内部排序 + 整体排序」，避免依赖 `TarjanSCC` 内部实现细节导致脆弱断言。
- **凝聚图节点命名**：`GetCondensation` 将 SCC 命名为 `scc_0, scc_1, ...`；测试通过 `fmt.Sprintf("scc_%d", mapping["a"])` 拼出对应名字，保证对 `mapping` 的间接断言。
- **稳定顺序断言**：`TestOrderGraph_BasicOperations` 与 `TestGraphAlgorithms_TopoSortAndLevels` 都显式依赖 `AddNode` / `AddEdge` 顺序，这是 `OrderGraph` 的核心保证。

## 关联源码

- `pkg/farm/graph.go` 的全部公开符号

## 运行方式

```bash
go test ./pkg/farm/ -run 'TestOrderGraph|TestGraphAlgorithms' -v
```
