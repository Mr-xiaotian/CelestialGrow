package farm

import (
	"strings"
	"unicode/utf8"
)

// ==== Render ====

// renderFrame 是显式栈迭代 DFS 的栈帧。
// isRoot 表示根节点：根节点不画连接符，且其子节点前缀为空。
type renderFrame struct {
	name   string
	prefix string
	isLast bool
	isRoot bool
}

// RenderStructureList 从图结构（节点、邻接表、源节点）生成带边框的树形文本列表。
// 渲染规则：
//   - 以 sourceNodes 为根，按 edges 邻接表展开为树形文本；
//   - 环或共享子图节点只展开一次，再次出现时标记 [Ref]；
//   - 未从任意根渲染到的节点（孤立节点）追加在末尾；
//   - 根节点不画连接符，子节点使用 ╞--> / ╘--> 连接符。
//
// 使用显式栈迭代的 DFS 先序遍历，避免深链图触发递归深度限制。
// sourceNodes 为空时，自动从不出现在子节点集合中的节点推断；仍为空时取 nodes[0]。
// nodes 为空时返回占位提示 ["+ No stages defined +"]。
func RenderStructureList(nodes []string, edges map[string][]string, sourceNodes []string) []string {
	if len(nodes) == 0 {
		return []string{"+ No stages defined +"}
	}

	if len(sourceNodes) == 0 {
		childNames := make(map[string]struct{})
		for _, children := range edges {
			for _, child := range children {
				childNames[child] = struct{}{}
			}
		}
		sourceNodes = nil
		for _, name := range nodes {
			if _, ok := childNames[name]; !ok {
				sourceNodes = append(sourceNodes, name)
			}
		}
	}
	if len(sourceNodes) == 0 {
		sourceNodes = []string{nodes[0]}
	}

	lines := make([]string, 0)
	expandedNodes := make(map[string]struct{})
	var stack []renderFrame

	// visit 输出节点行，并将未展开子节点按逆序压入栈（保证弹栈顺序为 DFS 先序）。
	visit := func(nodeName, prefix string, isLast, isRoot bool) {
		_, expanded := expandedNodes[nodeName]
		label := nodeName
		if expanded {
			label += " [Ref]"
		}

		if isRoot {
			lines = append(lines, label)
		} else {
			connector := "╞-->"
			if isLast {
				connector = "╘-->"
			}
			lines = append(lines, prefix+connector+label)
		}
		if expanded {
			return
		}

		expandedNodes[nodeName] = struct{}{}

		// 子节点缩进取决于当前节点是否为最后一个：最后一个留空，否则延续竖线
		childPrefix := ""
		if !isRoot {
			if isLast {
				childPrefix = prefix + "    "
			} else {
				childPrefix = prefix + "│   "
			}
		}
		nextStages := edges[nodeName]
		for i := len(nextStages) - 1; i >= 0; i-- {
			stack = append(stack, renderFrame{
				name:   nextStages[i],
				prefix: childPrefix,
				isLast: i == len(nextStages)-1,
			})
		}
	}

	renderedRoots := make(map[string]struct{})
	for _, rootName := range sourceNodes {
		if len(lines) > 0 {
			lines = append(lines, "") // 根之间留空行
		}
		stack = append(stack, renderFrame{name: rootName, isRoot: true})
		for len(stack) > 0 {
			frame := stack[len(stack)-1]
			stack = stack[:len(stack)-1]
			visit(frame.name, frame.prefix, frame.isLast, frame.isRoot)
		}
		renderedRoots[rootName] = struct{}{}
	}

	for _, nodeName := range nodes {
		if _, ok := renderedRoots[nodeName]; ok {
			continue
		}
		if _, ok := expandedNodes[nodeName]; ok {
			continue
		}
		if len(lines) > 0 {
			lines = append(lines, "")
		}
		stack = append(stack, renderFrame{name: nodeName, isRoot: true})
		for len(stack) > 0 {
			frame := stack[len(stack)-1]
			stack = stack[:len(stack)-1]
			visit(frame.name, frame.prefix, frame.isLast, frame.isRoot)
		}
	}

	// 用 rune 数计算宽度并对齐，保证多字节连接符（╞/╘/│）不破坏边框对齐。
	maxLength := 0
	for _, line := range lines {
		if n := utf8.RuneCountInString(line); n > maxLength {
			maxLength = n
		}
	}

	content := make([]string, 0, len(lines)+2)
	content = append(content, "+"+strings.Repeat("-", maxLength+2)+"+")
	for _, line := range lines {
		pad := maxLength - utf8.RuneCountInString(line)
		content = append(content, "| "+line+strings.Repeat(" ", pad)+" |")
	}
	content = append(content, "+"+strings.Repeat("-", maxLength+2)+"+")
	return content
}
