# pkg/plot/helper.go

> 📅 Last Updated: 2026/11/13

`helper.go` is the collection of private utility functions in the `plot` package, and currently provides only one string truncation function, `trunc`, used specifically to compress the string representation of seed / fruit / yield into a shorter form before emitting events, so that neither the events nor the log lines generated from them grow too long.

## Purpose

- Provide the string truncation capability of "keeping the first and last 1/3 each and replacing the middle with `...`";
- Used by `basePlot` and the three kinds of nodes when constructing the `SeedRepr` / `FruitRepr` fields of `SeedInputEvent` / `SeedRipenEvent` / `SeedWitherEvent` / `SeedPruneEvent` / `SeedReplantEvent`, to shorten the result of `fmt.Sprintf("%+v", value)`.

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
| `string` | When over the limit it looks like `<first 1/3>...<last 1/3>`; see "Behavioral Details" below for the actual length |

## Behavioral Details

- **Counted in runes**: it converts with `[]rune(s)`, avoiding multi-byte characters such as Chinese being cut in half.
- **`max(1, maxLen/3)` as a fallback**: when `maxLen < 3` it still guarantees `segmentLen >= 1`, avoiding the situation where "truncation produces an empty string".
- **It does not modify the original string**: `s` is passed by value, and only `runes` is read.
- **Length after truncation**: the result is `2*segmentLen + 3` runes (`segmentLen = max(1, maxLen/3)`). For the 25 / 50 values used within the package, the result is far smaller than `maxLen`; only when `maxLen` is very small (such as 3, 4, 6) may it exceed the limit, so do not treat it as a strict length cap.

## Usage Sites within the `plot` Package

The current call sites of `trunc` are spread across the `basePlot` skeleton and the success/failure paths of the three kinds of nodes:

| Location | Call | Event Field Written |
|------|------|----------------|
| `basePlot.Seed` | `trunc(fmt.Sprintf("%+v", seed), 50)` | `SeedInputEvent.SeedRepr` |
| `basePlot.witherSeed` | `trunc(fmt.Sprintf("%+v", seed), 50)` | `SeedWitherEvent.SeedRepr` |
| `basePlot.pruneSeed` | `trunc(fmt.Sprintf("%+v", seed), 50)` | `SeedPruneEvent.SeedRepr` |
| `basePlot.tend` | `trunc(fmt.Sprintf("%+v", seedPayload.Value), 50)` | `SeedReplantEvent.SeedRepr` |
| `SinglePlot.ripenSeed` | seed ≤ 50, fruit ≤ 25 | `SeedRipenEvent.SeedRepr/FruitRepr`; the downstream `SeedInputEvent` reuses the 25-rune fruit representation |
| `SplitPlot.ripenSeed` | seed ≤ 50, `[]F` ≤ 25; each element ≤ 50 | The slice is used for `SeedRipenEvent.FruitRepr`, and a single element is used for the downstream `SeedInputEvent.SeedRepr` |
| `RoutePlot.ripenSeed` | seed ≤ 50, routing table ≤ 25; a single yield ≤ 50 | The routing table is used for `SeedRipenEvent.FruitRepr`, and a single yield is used for the downstream `SeedInputEvent.SeedRepr` |

> "Results" uniformly use 25 runes (they may be very long, and only identification is needed), while "seed / a single yield" uniformly use 50 runes (recognizability matters more); this is the division of labor established by convention within the package.

> `persist.LogWatcher` renders logs directly from the `SeedRepr` / `FruitRepr` in events, so the effect of `trunc` ultimately shows up in the log text; the logs also keep the event ID, so the full data can still be traced back.

## Notes

- `trunc` is an unexported function, so external packages **cannot** call it directly; if string truncation is needed, implement it yourself or reuse the handling already wrapped in `pkg/persist`.
- `trunc` does not preserve the "full original length" information in its result; if the complete content is needed for debugging, it is advisable to use a custom `Watcher` in test or development mode to read the raw `Seed` / `Fruit` fields of the event.
- When the original string has `len(runes) <= maxLen`, the function does **not** return `s + "..."` but returns it unchanged — which means that when it is exactly equal to `maxLen` there is no ellipsis either.
- Lifecycle records (`LifecycleRecord.SeedJSON` / `FruitJSON`) **do not go through** `trunc`: `persist.LifecycleWatcher` serializes the raw `Seed` / `Fruit` in the event and always stores the complete JSON; truncation only affects the readable representation.
