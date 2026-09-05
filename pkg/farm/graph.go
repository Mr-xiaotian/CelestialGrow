package farm

import (
	"fmt"
	"slices"
)

// ==== Struct ====

// OrderGraph 是供图分析辅助函数使用的最小有向图。
// 其名称取自图的可排序性：拓扑层级、可达性与 SCC 等依赖结构均确定；
// 节点本身的遍历顺序不保证，每个节点的后继/前驱列表按添加顺序保存。
type OrderGraph struct {
	in  map[string][]string
	out map[string][]string
}

// ==== Construction ====

// NewOrderGraph 创建一个空的有向图。
func NewOrderGraph() *OrderGraph {
	return &OrderGraph{
		in:  make(map[string][]string),
		out: make(map[string][]string),
	}
}

// NewOrderGraphFromEdges 从邻接数据构建图。
// stageNames 用于保留未出现在出边邻接表中的孤立节点。
func NewOrderGraphFromEdges(outEdges map[string][]string, stageNames []string) *OrderGraph {
	graph := NewOrderGraph()

	for _, name := range stageNames {
		graph.AddNode(name)
	}
	for from, targets := range outEdges {
		if len(targets) == 0 {
			graph.AddNode(from)
			continue
		}
		for _, to := range targets {
			graph.AddEdge(from, to)
		}
	}

	return graph
}

// ==== Mutation ====

// AddNode 在节点不存在时添加节点。
func (g *OrderGraph) AddNode(name string) {
	if _, ok := g.out[name]; ok {
		return
	}

	g.out[name] = make([]string, 0)
	g.in[name] = make([]string, 0)
}

// AddEdge 添加一条有向边，并自动补全缺失端点节点。
// 重复边会被忽略，以保持邻接表稳定。
func (g *OrderGraph) AddEdge(from, to string) {
	g.AddNode(from)
	g.AddNode(to)

	if slices.Contains(g.out[from], to) {
		return
	}

	g.out[from] = append(g.out[from], to)
	g.in[to] = append(g.in[to], from)
}

// ==== Queries ====

// Nodes 返回全部节点名称，顺序不保证。
func (g *OrderGraph) Nodes() []string {
	nodes := make([]string, 0, len(g.out))
	for node := range g.out {
		nodes = append(nodes, node)
	}
	return nodes
}

// OutEdges 返回出边邻接表的深拷贝视图。
func (g *OrderGraph) OutEdges() map[string][]string {
	out := make(map[string][]string, len(g.out))
	for node, targets := range g.out {
		out[node] = append([]string{}, targets...)
	}
	return out
}

// InEdges 返回入边邻接表的深拷贝视图。
func (g *OrderGraph) InEdges() map[string][]string {
	in := make(map[string][]string, len(g.in))
	for node, sources := range g.in {
		in[node] = append([]string{}, sources...)
	}
	return in
}

// HasNode 判断节点是否存在。
func (g *OrderGraph) HasNode(name string) bool {
	_, ok := g.out[name]
	return ok
}

// Successors 按插入顺序返回后继节点。
func (g *OrderGraph) Successors(name string) []string {
	return append([]string{}, g.out[name]...)
}

// Predecessors 按插入顺序返回前驱节点。
func (g *OrderGraph) Predecessors(name string) []string {
	return append([]string{}, g.in[name]...)
}

// Connected 返回 from → to 是否已建立连接。
func (g *OrderGraph) Connected(from, to string) bool {
	if !g.HasNode(from) || !g.HasNode(to) {
		return false
	}
	return slices.Contains(g.out[from], to)
}

// String 返回图的简要描述。
func (g *OrderGraph) String() string {
	edgeCount := 0
	for _, targets := range g.out {
		edgeCount += len(targets)
	}
	return fmt.Sprintf("OrderGraph(nodes=%d, edges=%d)", len(g.out), edgeCount)
}

// ==== Topological Algorithms ====

// InDegree 计算每个节点的入度。
func InDegree(g *OrderGraph) map[string]int {
	degree := make(map[string]int, len(g.out))
	for name := range g.out {
		degree[name] = len(g.in[name])
	}
	return degree
}

// IsDAG 使用 Kahn 算法判断图是否为 DAG。
func IsDAG(g *OrderGraph) bool {
	degree := InDegree(g)
	queue := make([]string, 0, len(g.out))
	for name := range g.out {
		if degree[name] == 0 {
			queue = append(queue, name)
		}
	}

	visited := 0
	for len(queue) > 0 {
		node := queue[0]
		queue = queue[1:]
		visited++

		for _, succ := range g.out[node] {
			degree[succ]--
			if degree[succ] == 0 {
				queue = append(queue, succ)
			}
		}
	}

	return visited == len(g.out)
}

// TopoSort 在图是 DAG 时返回一个拓扑序；若存在环则返回 nil。
func TopoSort(g *OrderGraph) []string {
	if !IsDAG(g) {
		return nil
	}

	degree := InDegree(g)
	queue := make([]string, 0, len(g.out))
	for name := range g.out {
		if degree[name] == 0 {
			queue = append(queue, name)
		}
	}

	order := make([]string, 0, len(g.out))
	for len(queue) > 0 {
		node := queue[0]
		queue = queue[1:]
		order = append(order, node)

		for _, succ := range g.out[node] {
			degree[succ]--
			if degree[succ] == 0 {
				queue = append(queue, succ)
			}
		}
	}

	return order
}

// ==== Strongly Connected Components ====

// TarjanSCC 使用 Tarjan 算法计算强连通分量。
// 返回的 SCC 顺序为凝聚图的逆拓扑序。
func TarjanSCC(g *OrderGraph) [][]string {
	index := 0
	stack := make([]string, 0, len(g.out))
	onStack := make(map[string]struct{}, len(g.out))
	indices := make(map[string]int, len(g.out))
	lowlink := make(map[string]int, len(g.out))
	sccs := make([][]string, 0)

	var strongConnect func(node string)
	strongConnect = func(node string) {
		indices[node] = index
		lowlink[node] = index
		index++

		stack = append(stack, node)
		onStack[node] = struct{}{}

		for _, succ := range g.out[node] {
			if _, visited := indices[succ]; !visited {
				strongConnect(succ)
				if lowlink[succ] < lowlink[node] {
					lowlink[node] = lowlink[succ]
				}
				continue
			}

			if _, stacked := onStack[succ]; stacked && indices[succ] < lowlink[node] {
				lowlink[node] = indices[succ]
			}
		}

		if lowlink[node] != indices[node] {
			return
		}

		scc := make([]string, 0)
		for {
			last := stack[len(stack)-1]
			stack = stack[:len(stack)-1]
			delete(onStack, last)
			scc = append(scc, last)
			if last == node {
				break
			}
		}
		sccs = append(sccs, scc)
	}

	for node := range g.out {
		if _, visited := indices[node]; !visited {
			strongConnect(node)
		}
	}

	return sccs
}

// NodeToSCCIndex 构建节点到 SCC 索引的映射。
func NodeToSCCIndex(sccs [][]string) map[string]int {
	mapping := make(map[string]int)
	for idx, scc := range sccs {
		for _, node := range scc {
			mapping[node] = idx
		}
	}
	return mapping
}

// GetCondensation 构建 SCC 凝聚图。
// 凝聚图节点命名为 scc_0, scc_1 ...，并自动去重跨 SCC 边。
func GetCondensation(graph *OrderGraph) (*OrderGraph, [][]string) {
	sccs := TarjanSCC(graph)
	mapping := NodeToSCCIndex(sccs)
	cond := NewOrderGraph()

	for idx := range sccs {
		cond.AddNode(fmt.Sprintf("scc_%d", idx))
	}

	seen := make(map[[2]int]struct{})
	for from, targets := range graph.out {
		for _, to := range targets {
			fromSCC := mapping[from]
			toSCC := mapping[to]
			if fromSCC == toSCC {
				continue
			}

			key := [2]int{fromSCC, toSCC}
			if _, ok := seen[key]; ok {
				continue
			}

			seen[key] = struct{}{}
			cond.AddEdge(fmt.Sprintf("scc_%d", fromSCC), fmt.Sprintf("scc_%d", toSCC))
		}
	}

	return cond, sccs
}

// SourceSCCs 返回凝聚图中入度为 0 的 SCC。
func SourceSCCs(graph *OrderGraph) [][]string {
	sccs := TarjanSCC(graph)
	if len(sccs) == 0 {
		return nil
	}

	mapping := NodeToSCCIndex(sccs)
	sccInDegree := make([]int, len(sccs))

	for from, targets := range graph.out {
		for _, to := range targets {
			fromSCC := mapping[from]
			toSCC := mapping[to]
			if fromSCC != toSCC {
				sccInDegree[toSCC]++
			}
		}
	}

	sources := make([][]string, 0)
	for idx, degree := range sccInDegree {
		if degree == 0 {
			sources = append(sources, append([]string(nil), sccs[idx]...))
		}
	}

	return sources
}

// SourceNodes 从每个 Source SCC 中返回一个代表节点。
func SourceNodes(graph *OrderGraph) []string {
	sourceSCCs := SourceSCCs(graph)
	nodes := make([]string, 0, len(sourceSCCs))
	for _, scc := range sourceSCCs {
		if len(scc) > 0 {
			nodes = append(nodes, scc[0])
		}
	}
	return nodes
}

// ==== Node Levels ====

// ComputeNodeLevels 计算每个节点的最早执行层级。
// 它会先构建 SCC 凝聚图，再在该 DAG 上逐层传播 level。
func ComputeNodeLevels(graph *OrderGraph) (map[string]int, error) {
	condensation, sccs := GetCondensation(graph)
	order := TopoSort(condensation)
	if order == nil {
		return nil, fmt.Errorf("condensation graph must be a DAG")
	}

	sccLevels := make(map[string]int, len(condensation.out))
	for node := range condensation.out {
		sccLevels[node] = 0
	}

	for _, node := range order {
		for _, succ := range condensation.out[node] {
			nextLevel := sccLevels[node] + 1
			if nextLevel > sccLevels[succ] {
				sccLevels[succ] = nextLevel
			}
		}
	}

	levels := make(map[string]int, len(graph.out))
	for idx, scc := range sccs {
		level := sccLevels[fmt.Sprintf("scc_%d", idx)]
		for _, node := range scc {
			levels[node] = level
		}
	}

	return levels, nil
}
