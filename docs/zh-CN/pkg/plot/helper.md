# pkg/plot/helper.go

> 📅 最后更新日期: 2026/11/13

`helper.go` 是 `plot` 包内的私有工具函数集合，目前只提供一个字符串截断函数 `trunc`，专门用于在发送事件前把 seed / fruit / yield 的字符串表示压缩成较短的形式，避免事件与随之生成的日志行过长。

## 作用

- 提供「保留首尾各 1/3、中间用 `...` 替代」的字符串截断能力；
- 供 `basePlot` 与三种节点在构造 `SeedInputEvent` / `SeedRipenEvent` / `SeedWitherEvent` / `SeedPruneEvent` / `SeedReplantEvent` 的 `SeedRepr` / `FruitRepr` 字段时，把 `fmt.Sprintf("%+v", value)` 的结果压短。

## 公开符号

该文件**只包含一个未导出函数**：

### `trunc(s string, maxLen int) string`

把字符串 `s` 截断到最多 `maxLen` 个 rune：

- 若 `len([]rune(s)) <= maxLen`：原样返回；
- 否则取首 `segmentLen = max(1, maxLen/3)` 个 rune 与尾 `segmentLen` 个 rune，中间用 `"..."` 拼接返回。

```go
func trunc(s string, maxLen int) string {
	runes := []rune(s)
	if len(runes) <= maxLen {
		return s
	}

	segmentLen := max(1, maxLen/3)
	headStr := string(runes[:segmentLen])
	tailStr := string(runes[len(runes)-segmentLen:])
	return headStr + "..." + tailStr
}
```

| 入参 | 含义 |
|------|------|
| `s` | 原始字符串 |
| `maxLen` | 截断上限（按 rune 计数，不是字节） |

| 返回 | 含义 |
|------|------|
| `string` | 超出上限时形如 `<前 1/3>...<后 1/3>`；实际长度见下方「行为细节」 |

## 行为细节

- **按 rune 计数**：使用 `[]rune(s)` 转换，避免中文等多字节字符被切到一半。
- **`max(1, maxLen/3)` 兜底**：当 `maxLen < 3` 时仍保证 `segmentLen >= 1`，避免出现「截断后变成空串」的情况。
- **不修改原字符串**：`s` 是按值传入，且只读取 `runes`。
- **截断后长度**：结果为 `2*segmentLen + 3` 个 rune（`segmentLen = max(1, maxLen/3)`）。对包内使用的 25 / 50 而言结果远小于 `maxLen`；只有 `maxLen` 很小时（如 3、4、6）才可能超过上限，因此不要把它当作严格的长度上限。

## 在 `plot` 包内的使用点

当前 `trunc` 的调用点分布在 `basePlot` 骨架与三种节点的成功/失败路径上：

| 位置 | 调用 | 写入的事件字段 |
|------|------|----------------|
| `basePlot.Seed` | `trunc(fmt.Sprintf("%+v", seed), 50)` | `SeedInputEvent.SeedRepr` |
| `basePlot.witherSeed` | `trunc(fmt.Sprintf("%+v", seed), 50)` | `SeedWitherEvent.SeedRepr` |
| `basePlot.pruneSeed` | `trunc(fmt.Sprintf("%+v", seed), 50)` | `SeedPruneEvent.SeedRepr` |
| `basePlot.tend` | `trunc(fmt.Sprintf("%+v", seedPayload.Value), 50)` | `SeedReplantEvent.SeedRepr` |
| `SinglePlot.ripenSeed` | seed ≤ 50、fruit ≤ 25 | `SeedRipenEvent.SeedRepr/FruitRepr`；下游 `SeedInputEvent` 复用 fruit 的 25 rune 表示 |
| `SplitPlot.ripenSeed` | seed ≤ 50、`[]F` ≤ 25；每个元素 ≤ 50 | 切片用于 `SeedRipenEvent.FruitRepr`，单个元素用于下游 `SeedInputEvent.SeedRepr` |
| `RoutePlot.ripenSeed` | seed ≤ 50、路由表 ≤ 25；单个 yield ≤ 50 | 路由表用于 `SeedRipenEvent.FruitRepr`，单个 yield 用于下游 `SeedInputEvent.SeedRepr` |

> 「结果」统一用 25 rune（可能很长，只需辨识），「seed / 单个 yield」统一用 50 rune（辨识度更重要），这是包内约定俗成的分工。

> `persist.LogWatcher` 直接使用事件里的 `SeedRepr` / `FruitRepr` 渲染日志，因此 `trunc` 的效果最终体现在日志文本中；日志同时保留事件 ID，仍可回溯完整数据。

## 注意事项

- `trunc` 是未导出函数，外部包**无法**直接调用；如需截断字符串，请自行实现或复用 `pkg/persist` 内已封装的处理。
- `trunc` 不会在结果中保留「完整原长」信息；如果调试时需要完整内容，建议在测试或开发模式下改用自定义 `Watcher` 读取事件的 `Seed` / `Fruit` 原始字段。
- 当原始字符串 `len(runes) <= maxLen` 时函数**不会**返回 `s + "..."`，而是原样返回——这意味着当恰好等于 `maxLen` 时也不会带省略号。
- 生命周期记录（`LifecycleRecord.SeedJSON` / `FruitJSON`）**不经过** `trunc`：`persist.LifecycleWatcher` 序列化的是事件中的原始 `Seed` / `Fruit`，始终保存完整 JSON；截断只影响可读表示。
