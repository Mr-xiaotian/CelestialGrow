# pkg/plot/helper.go

> 📅 Last Updated: 2026/09/24

`helper.go` is the collection of private utility functions in the `plot` package, and currently provides only one string truncation function, `trunc`, dedicated to compressing the string representation of seed / fruit / yield into a shorter form before writing logs, so that log lines do not become too long.

## Purpose

- Provide the string truncation capability of "keeping the first and last 1/3 each and replacing the middle with `...`";
- Used by `basePlot` and the three kinds of nodes to shorten the result of `fmt.Sprintf("%+v", value)` before calling `logInlet.*`.

## Public Symbols

This file **contains only one unexported function**:

### `trunc(s string, maxLen int) string`

Truncates the string `s` to at most `maxLen` runes:

- If `len([]rune(s)) <= maxLen`: return it unchanged;
- Otherwise take the first `segmentLen = max(1, maxLen/3)` runes and the last `segmentLen` runes, and join them with `"..."` in between.

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

| Parameter | Meaning |
|------|------|
| `s` | The original string |
| `maxLen` | Truncation limit (counted in runes, not bytes) |

| Return | Meaning |
|------|------|
| `string` | A string of no more than `maxLen` runes; when truncated it looks like `<first 1/3>...<last 1/3>` |

## Behavioral Details

- **Counted in runes**: it converts with `[]rune(s)`, avoiding multi-byte characters such as Chinese being cut in half.
- **`max(1, maxLen/3)` as a fallback**: when `maxLen < 3` it still guarantees `segmentLen >= 1`, avoiding the situation where "truncation produces an empty string".
- **It does not modify the original string**: `s` is passed by value, and only `runes` is read.

## Usage Sites within the `plot` Package

The current call sites of `trunc` are spread across the `basePlot` skeleton and the success/failure paths of the three kinds of nodes; grouped by the log semantics they serve, they are as follows:

| Location | Call | Effect |
|------|------|------|
| `basePlot.tend` | `trunc(fmt.Sprintf("%+v", seedPayload.Value), 50)` | seed ≤ 50, used for the `SeedReplant` log |
| `basePlot.witherSeed` | `trunc(seedString, 50)` | seed ≤ 50, used for the `SeedWither` log |
| `basePlot.Seed` | `trunc(fmt.Sprintf("%+v", seed), 50)` | locally sown seed ≤ 50, used for the `SeedInput` log |
| `Plot.ripenSeed` | seed ≤ 50, fruit ≤ 25 | Used for the `SeedRipen` log; fruit is a **single** `F` |
| `SplitPlot.ripenSeed` | seed ≤ 50, `[]F` ≤ 25; each element ≤ 50 | The result slice is used for `SeedRipen`, and a single element is used for the downstream `SeedInput` |
| `RoutePlot.ripenSeed` | seed ≤ 50, routing table ≤ 25; a single yield ≤ 50 | The routing table is used for `SeedRipen`, and a single yield is used for the downstream `SeedInput` |

> "Results" uniformly use 25 runes (they may be very long, and only identification is needed), while "seed / a single yield" uniformly use 50 runes (recognizability matters more); this is the division of labor established by convention within the package.

> Logs are text fields in SQLite; controlling the length of each log through `trunc` both keeps the key head and tail information and prevents large objects (such as `[]byte` or long strings) from blowing up the log file.

## Notes

- `trunc` is an unexported function, so external packages **cannot** call it directly; if string truncation is needed, implement it yourself or reuse the handling already wrapped in `pkg/persist`.
- `trunc` does not preserve information about "the full original length" in its result; if the complete content is needed for debugging, it is advisable to bypass the function in test or development mode (for example with a custom inlet / log handler).
- When the original string has `len(runes) <= maxLen`, the function does **not** return `s + "..."` but returns it unchanged — which means that when it is exactly equal to `maxLen` there is no ellipsis either.
- Lifecycle records (`LifecycleRecord.SeedJSON` / `FruitJSON`) **do not go through** `trunc` and always store the complete JSON; truncation only happens on the log path.
