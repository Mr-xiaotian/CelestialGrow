package grow_test

import (
	"fmt"
	"reflect"
	"sort"
	"strings"
	"testing"

	"github.com/Mr-xiaotian/CelestialGrow/pkg/grow"
)

func canonicalizeSCCs(sccs [][]string) [][]string {
	canonical := make([][]string, len(sccs))
	for i, scc := range sccs {
		canonical[i] = append([]string(nil), scc...)
		sort.Strings(canonical[i])
	}
	sort.Slice(canonical, func(i, j int) bool {
		return strings.Join(canonical[i], ",") < strings.Join(canonical[j], ",")
	})
	return canonical
}

func TestOrderGraph_BasicOperations(t *testing.T) {
	graph := grow.NewOrderGraph()
	graph.AddNode("isolated")
	graph.AddEdge("a", "b")
	graph.AddEdge("a", "c")
	graph.AddEdge("a", "b")

	if !graph.HasNode("isolated") {
		t.Fatalf("expected isolated node to exist")
	}

	wantNodes := []string{"isolated", "a", "b", "c"}
	if !reflect.DeepEqual(graph.Nodes(), wantNodes) {
		t.Fatalf("unexpected node order: got %v want %v", graph.Nodes(), wantNodes)
	}

	wantSuccessors := []string{"b", "c"}
	if !reflect.DeepEqual(graph.Successors("a"), wantSuccessors) {
		t.Fatalf("unexpected successors: got %v want %v", graph.Successors("a"), wantSuccessors)
	}

	wantPredecessors := []string{"a"}
	if !reflect.DeepEqual(graph.Predecessors("b"), wantPredecessors) {
		t.Fatalf("unexpected predecessors: got %v want %v", graph.Predecessors("b"), wantPredecessors)
	}
}

func TestGraphAlgorithms_TopoSortAndLevels(t *testing.T) {
	graph := grow.NewOrderGraphFromEdges(map[string][]string{
		"a": {"b", "c"},
		"b": {"d"},
		"c": {"d"},
	}, nil)

	if !grow.IsDAG(graph) {
		t.Fatalf("expected graph to be a DAG")
	}

	wantTopo := []string{"a", "b", "c", "d"}
	if !reflect.DeepEqual(grow.TopoSort(graph), wantTopo) {
		t.Fatalf("unexpected topo order: got %v want %v", grow.TopoSort(graph), wantTopo)
	}

	wantSources := []string{"a"}
	if !reflect.DeepEqual(grow.SourceNodes(graph), wantSources) {
		t.Fatalf("unexpected source nodes: got %v want %v", grow.SourceNodes(graph), wantSources)
	}

	levels, err := grow.ComputeNodeLevels(graph)
	if err != nil {
		t.Fatalf("compute node levels failed: %v", err)
	}

	wantLevels := map[string]int{
		"a": 0,
		"b": 1,
		"c": 1,
		"d": 2,
	}
	if !reflect.DeepEqual(levels, wantLevels) {
		t.Fatalf("unexpected node levels: got %v want %v", levels, wantLevels)
	}
}

func TestGraphAlgorithms_SCCAndCondensation(t *testing.T) {
	graph := grow.NewOrderGraphFromEdges(map[string][]string{
		"a": {"b"},
		"b": {"a", "c"},
		"c": {"d"},
		"d": {"c"},
	}, nil)

	if grow.IsDAG(graph) {
		t.Fatalf("expected graph to contain cycles")
	}

	sccs := grow.TarjanSCC(graph)
	if len(sccs) != 2 {
		t.Fatalf("unexpected SCC count: got %d want 2", len(sccs))
	}

	wantSCCs := [][]string{{"a", "b"}, {"c", "d"}}
	if !reflect.DeepEqual(canonicalizeSCCs(sccs), canonicalizeSCCs(wantSCCs)) {
		t.Fatalf("unexpected SCCs: got %v want %v", sccs, wantSCCs)
	}

	sourceSCCs := grow.SourceSCCs(graph)
	wantSourceSCCs := [][]string{{"a", "b"}}
	if !reflect.DeepEqual(canonicalizeSCCs(sourceSCCs), canonicalizeSCCs(wantSourceSCCs)) {
		t.Fatalf("unexpected source SCCs: got %v want %v", sourceSCCs, wantSourceSCCs)
	}

	condensation, condensationSCCs := grow.GetCondensation(graph)
	if len(condensationSCCs) != 2 {
		t.Fatalf("unexpected condensation SCC count: got %d want 2", len(condensationSCCs))
	}

	mapping := grow.NodeToSCCIndex(condensationSCCs)
	if mapping["a"] != mapping["b"] || mapping["c"] != mapping["d"] || mapping["a"] == mapping["c"] {
		t.Fatalf("unexpected condensation SCC mapping: %v", mapping)
	}

	from := fmt.Sprintf("scc_%d", mapping["a"])
	to := fmt.Sprintf("scc_%d", mapping["c"])
	wantCondensationEdges := map[string][]string{from: {to}, to: {}}
	if !reflect.DeepEqual(condensation.OutEdges(), wantCondensationEdges) {
		t.Fatalf("unexpected condensation edges: got %v want %v", condensation.OutEdges(), wantCondensationEdges)
	}
}
