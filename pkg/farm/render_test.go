package farm_test

import (
	"fmt"
	"reflect"
	"strings"
	"testing"

	"github.com/Mr-xiaotian/CelestialGrow/pkg/farm"
)

// deepChain 超过常见的递归深度限制，用于回归显式栈迭代版渲染逻辑。
const deepChain = 5000

func TestRenderStructureList_Basic(t *testing.T) {
	nodes := []string{"s1", "s2", "s3", "s4"}
	edges := map[string][]string{
		"s1": {"s2", "s3"},
		"s2": {"s4"},
		"s3": {"s4"},
		"s4": {},
	}

	rendered := farm.RenderStructureList(nodes, edges, []string{"s1"})

	if len(rendered) == 0 {
		t.Fatalf("expected non-empty render result")
	}
	if !strings.Contains(rendered[1], "s1") {
		t.Fatalf("expected first content line to contain s1, got %q", rendered[1])
	}
	foundRef := false
	for _, line := range rendered {
		if strings.Contains(line, "[Ref]") {
			foundRef = true
			break
		}
	}
	if !foundRef {
		t.Fatalf("expected shared node to be marked as [Ref]: %v", rendered)
	}
}

func TestRenderStructureList_SimpleExact(t *testing.T) {
	rendered := farm.RenderStructureList([]string{"a", "b"}, map[string][]string{"a": {"b"}}, []string{"a"})

	want := []string{
		"+-------+",
		"| a     |",
		"| ╘-->b |",
		"+-------+",
	}
	if !reflect.DeepEqual(rendered, want) {
		t.Fatalf("unexpected render: got %q want %q", rendered, want)
	}
}

func TestRenderStructureList_NoNodes(t *testing.T) {
	rendered := farm.RenderStructureList(nil, nil, nil)

	want := []string{"+ No stages defined +"}
	if !reflect.DeepEqual(rendered, want) {
		t.Fatalf("unexpected render: got %q want %q", rendered, want)
	}
}

func TestRenderStructureList_Cycle(t *testing.T) {
	nodes := []string{"c1", "c2", "c3"}
	edges := map[string][]string{
		"c1": {"c2"},
		"c2": {"c3"},
		"c3": {"c1"},
	}

	rendered := farm.RenderStructureList(nodes, edges, []string{"c1"})

	joined := strings.Join(rendered, "\n")
	if got := strings.Count(joined, "c1"); got != 2 {
		t.Fatalf("expected c1 to appear exactly twice (expand + [Ref]), got %d", got)
	}
	if !strings.Contains(joined, "[Ref]") {
		t.Fatalf("expected [Ref] marker in cycle render")
	}
}

func TestRenderStructureList_DeepChain(t *testing.T) {
	nodes := make([]string, deepChain)
	edges := make(map[string][]string, deepChain)
	for i := 0; i < deepChain; i++ {
		nodes[i] = fmt.Sprintf("n%d", i)
	}
	for i := 0; i < deepChain-1; i++ {
		edges[nodes[i]] = []string{nodes[i+1]}
	}
	edges[nodes[deepChain-1]] = []string{}

	rendered := farm.RenderStructureList(nodes, edges, []string{"n0"})

	if len(rendered) != deepChain+2 {
		t.Fatalf("expected %d lines, got %d", deepChain+2, len(rendered))
	}
	if !strings.Contains(rendered[1], "n0") {
		t.Fatalf("expected first content line to contain n0")
	}
	if !strings.Contains(rendered[len(rendered)-2], "n4999") {
		t.Fatalf("expected last content line to contain n4999")
	}
}

func TestRenderStructureList_OrphansAndInferredSources(t *testing.T) {
	nodes := []string{"a", "b", "c"}
	edges := map[string][]string{"b": {"c"}}

	// sourceNodes 为空时，从 nodes 中推断不出现在子节点集合中的节点作为根。
	rendered := farm.RenderStructureList(nodes, edges, nil)

	joined := strings.Join(rendered, "\n")
	if !strings.Contains(joined, "a") {
		t.Fatalf("expected orphan node a to be rendered, got:\n%s", joined)
	}
	if !strings.Contains(joined, "╘-->c") {
		t.Fatalf("expected c to be rendered under b, got:\n%s", joined)
	}
}
