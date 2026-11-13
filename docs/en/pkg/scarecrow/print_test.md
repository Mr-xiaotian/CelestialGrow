# pkg/scarecrow/print_test.go

> 📅 Last Updated: 2026/11/13

## Purpose

`pkg/scarecrow/print_test.go` pins down the **output format contract** and the **ignored scope** of `PrintWatcher`: the prefix, field order, and accumulated values of progress lines, and the fact that Farm / Seal / Replant events produce no output. The tests wire the output into a `bytes.Buffer` through the unexported `newPrintWatcher(out, name)`, avoiding pollution of standard output.

## Test Distribution

| Test | Coverage |
|------|----------|
| `TestPrintWatcherReportsProgress` | Feeds 8 events in order and asserts each of the 8 output lines |
| `TestPrintWatcherIgnoresFarmAndSealEvents` | Feeds 5 events unrelated to progress and asserts the buffer length is 0 |

## Key Assertions and Design Intent

### Output Format: `TestPrintWatcherReportsProgress`

The event sequence and the expected output (prefix `demo`):

| Event | Expected line |
|------|--------|
| `PlotStartEvent{}` | `[demo] start total=0` |
| `SeedInputEvent{}` ×3 | `[demo] total=1(+1)`, `total=2(+1)`, `total=3(+1)` |
| `SeedRipenEvent{}` | `[demo] succeeded=1(+1), total=3` |
| `SeedWitherEvent{}` | `[demo] failed=1(+1), total=3` |
| `SeedPruneEvent{}` | `[demo] skipped=1(+1), total=3` |
| `PlotEndEvent{}` | `[demo] finish total=3, skipped=1, succeeded=1, failed=1` |

Verification points:

- `PlotStartEvent` happens before any input, so the first line shows `total=0`—which also proves `total` is driven only by `SeedInputEvent`.
- Result lines keep `total` at 3, showing that result events do not change `total`.
- The closing line's field order is fixed as `total, skipped, succeeded, failed`, matching the argument order of `printf` in the source; changing the order is caught by the test immediately.
- Assertions match line by line with `strings.Contains` rather than requiring the whole text to be equal, so they do not constrain extra output between lines (the current implementation prints exactly one line per event).

### Ignored Scope: `TestPrintWatcherIgnoresFarmAndSealEvents`

Sends `FarmStartEvent`, `FarmEndEvent`, `SeedReplantEvent`, `SealInputEvent`, `SealMergeEvent` in order and asserts `out.Len() == 0`. This pins the "node and task progress only" behavior: Farm lifecycle, retries, and termination signals print nothing.

## Running

```bash
go test ./pkg/scarecrow -run 'TestPrintWatcher' -v
```

## Notes

- The tests verify only that "the output contains a line", not whether counts reset between runs; `PrintWatcher` itself has no reset interface (see [`print.md`](./print.md)).
- The tests do not cover concurrent calls: there is no concurrency assertion for the serialization of `PrintWatcher.mu`, so switching to a lock-free implementation would not fail this file.
- The path where writing to `out` fails is not covered (`printf` explicitly discards the `Fprintf` error), nor is the default `NewPrintWatcher` constructor that writes to `os.Stdout`.
- Changing any output line requires updating the expected strings in this file; this coupling is intentional, to prevent format drift.
