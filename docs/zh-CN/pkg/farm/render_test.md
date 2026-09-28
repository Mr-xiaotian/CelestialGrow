# pkg/farm/render_test.go

> 📅 最后更新日期: 2026/09/24

## 作用

`render_test.go` 以 `package farm_test` 黑盒方式覆盖 `pkg/farm/render.go` 的公开函数 `RenderStructureList`，验证渲染形状、精确输出、边界与深层图。

## 测试重点

- **精确输出格式**：边框宽度、对齐填充与连接符必须与实现一致。
- **共享 / 环节点去重**：同一节点只展开一次，重复出现追加 `[Ref]`。
- **空图与源节点推断**：`nodes` 为空时的占位符；`sourceNodes` 为空时的推断逻辑与孤立节点补充。
- **深链回归**：用 5000 节点深链保证「显式栈迭代」实现不会因递归深度而栈溢出。

## 测试用例

| 测试函数 | 验证点 |
| --- | --- |
| `TestRenderStructureList_Basic` | `s1 → {s2, s3} → s4` 菱形；返回非空、首行内容含 `s1`、共享节点 `s4` 出现 `[Ref]` |
| `TestRenderStructureList_SimpleExact` | `a → b` 的渲染结果与期望逐行 `DeepEqual`：`+-------+` / `\| a     \|` / `\| ╘-->b \|` / `+-------+` |
| `TestRenderStructureList_NoNodes` | `(nil, nil, nil)` 直接返回 `["+ No stages defined +"]` |
| `TestRenderStructureList_Cycle` | `c1 → c2 → c3 → c1` 环；`c1` 恰好出现 2 次（展开 + `[Ref]`）且包含 `[Ref]` |
| `TestRenderStructureList_DeepChain` | 5000 节点链式图；返回行数为 `deepChain + 2`，首行含 `n0`、末内容行含 `n4999` |
| `TestRenderStructureList_OrphansAndInferredSources` | `sourceNodes` 为空时自动推断根，孤立节点 `a` 被渲染，`b` 子树渲染出 `╘-->c` |

## 关键细节

- **`deepChain = 5000` 常量**：注释明确其目的是「超过常见的递归深度限制」，用于回归显式栈迭代版渲染逻辑，防止退回递归实现。
- **精确断言 vs 包含断言**：只有 `SimpleExact` 与 `NoNodes` 使用 `reflect.DeepEqual` 做全量比对，其余用例用 `strings.Contains` / `strings.Count` 做宽松断言，避免绑定到不稳定的节点顺序。
- **推断根的分隔**：`OrphansAndInferredSources` 中推断出的两个根之间会插入空行，测试只断言关键行存在，不约束空行位置。

## 关联源码

- `pkg/farm/render.go` 的 `RenderStructureList`
- `pkg/farm/farm.go` 的 `Farm.getStructureList`（生产侧调用点）

## 运行方式

```bash
go test ./pkg/farm/ -run 'TestRenderStructureList' -v
```
