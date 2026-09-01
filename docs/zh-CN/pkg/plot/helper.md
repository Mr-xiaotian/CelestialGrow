# pkg/plot/helper.go

> 最后更新日期: 2026/09/01

`helper.go` 是 `plot` 包内的私有工具函数集合，目前只提供一个字符串截断函数 `trunc`，专门用于在写日志前把 seed / fruit 的字符串表示压缩成较短的形式，避免日志行过长。

## 作用

- 提供「保留首尾各 1/3、中间用 `...` 替代」的字符串截断能力；
- 供 `Plot.bearFruit` / `Plot.bearWeed` / `Plot.tend` 在调用 `logInlet.*` 之前，把 `fmt.Sprintf("%+v", value)` 结果压短。

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
| `string` | 不超过 `maxLen` rune 的字符串；超出时形如 `<前 1/3>...<后 1/3>` |

## 行为细节

- **按 rune 计数**：使用 `[]rune(s)` 转换，避免中文等多字节字符被切到一半。
- **`max(1, maxLen/3)` 兜底**：当 `maxLen < 3` 时仍保证 `segmentLen >= 1`，避免出现「截断后变成空串」的情况。
- **不修改原字符串**：`s` 是按值传入，且只读取 `runes`。

## 在 `plot` 包内的使用点

| 位置 | 调用 | 效果 |
|------|------|------|
| `Plot.tend` | `trunc(fmt.Sprintf("%+v", seedPayload.Value), 50)` | 把 seed 字符串表示压到 ≤ 50 rune，用作 `SeedReplant` 日志 |
| `Plot.bearFruit` | `trunc(fmt.Sprintf("%+v", seed), 50)`、`trunc(fmt.Sprintf("%+v", fruit), 25)` | seed ≤ 50、fruit ≤ 25，用于 `SeedRipen` 日志 |
| `Plot.bearWeed` | `trunc(seedString, 50)` | seed ≤ 50，用于 `SeedWither` 日志 |

> 日志在 SQLite 中是文本字段；通过 `trunc` 控制每条日志长度既能保留关键首尾信息，又避免大对象（如 `[]byte`、长字符串）撑爆日志文件。

## 注意事项

- `trunc` 是未导出函数，外部包**无法**直接调用；如需截断字符串，请自行实现或复用 `pkg/persist` 内已封装的处理。
- `trunc` 不会在结果中保留「完整原长」信息；如果调试时需要完整内容，建议在测试或开发模式下绕过该函数（例如自定义 inlet / 日志 handler）。
- 当原始字符串 `len(runes) <= maxLen` 时函数**不会**返回 `s + "..."`，而是原样返回——这意味着当恰好等于 `maxLen` 时也不会带省略号。
