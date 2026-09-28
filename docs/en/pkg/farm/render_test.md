# pkg/farm/render_test.go

> 📅 Last Updated: 2026/09/24

## Purpose

`render_test.go`, as `package farm_test`, covers the public function `RenderStructureList` in `pkg/farm/render.go` in a black-box manner, verifying rendering shapes, exact output, boundaries, and deep graphs.

## Test Focus

- **Exact output format**: border width, alignment padding and connectors must match the implementation.
- **Shared / cycle node deduplication**: the same node is expanded only once, with `[Ref]` appended on repeated appearances.
- **Empty graph and source node inference**: the placeholder when `nodes` is empty; the inference logic and orphan node supplementation when `sourceNodes` is empty.
- **Deep chain regression**: a 5000-node deep chain ensures the "explicit-stack iterative" implementation does not overflow the stack due to recursion depth.

## Test Cases

| Test function | What it verifies |
| --- | --- |
| `TestRenderStructureList_Basic` | `s1 → {s2, s3} → s4` diamond; the return is non-empty, the first line contains `s1`, and the shared node `s4` shows `[Ref]` |
| `TestRenderStructureList_SimpleExact` | The rendering result of `a → b` is `DeepEqual` to the expectation line by line: `+-------+` / `\| a     \|` / `\| ╘-->b \|` / `+-------+` |
| `TestRenderStructureList_NoNodes` | `(nil, nil, nil)` returns `["+ No stages defined +"]` directly |
| `TestRenderStructureList_Cycle` | `c1 → c2 → c3 → c1` cycle; `c1` appears exactly 2 times (expansion + `[Ref]`) and contains `[Ref]` |
| `TestRenderStructureList_DeepChain` | A 5000-node chain graph; the number of returned lines is `deepChain + 2`, the first line contains `n0`, and the last content line contains `n4999` |
| `TestRenderStructureList_OrphansAndInferredSources` | Roots are inferred automatically when `sourceNodes` is empty, the orphan node `a` is rendered, and the `b` subtree renders `╘-->c` |

## Key Details

- **The `deepChain = 5000` constant**: the comment explicitly states its purpose as "exceeding the common recursion depth limit", used to regress the explicit-stack iterative rendering logic and prevent falling back to a recursive implementation.
- **Exact assertions vs containment assertions**: only `SimpleExact` and `NoNodes` use `reflect.DeepEqual` for full comparison; the other cases use `strings.Contains` / `strings.Count` for loose assertions, avoiding binding to unstable node order.
- **Separation of inferred roots**: in `OrphansAndInferredSources`, a blank line is inserted between the two inferred roots; the test only asserts that the key lines exist and does not constrain the blank line's position.

## Related Source

- `RenderStructureList` in `pkg/farm/render.go`
- `Farm.getStructureList` in `pkg/farm/farm.go` (the production-side call site)

## How to Run

```bash
go test ./pkg/farm/ -run 'TestRenderStructureList' -v
```
