# pkg/scarecrow/event_test.go

> 📅 Last Updated: 2026/11/13

## Purpose

`pkg/scarecrow/event_test.go` verifies whether **the event fields are sufficient to rebuild runtime metrics**, and whether events keep their full payload and lineage IDs during dispatch. It does not test `MetricsWatcher` itself; instead it replicates the metric definitions with an independent event probe (`metricsProbe`), so "event definitions" and "metric implementation" are verified separately.

## Test Focus

| Test | Coverage |
|------|----------|
| `TestSyntheticLifecycleFeedsMetrics` | Feeds 17 synthetic events covering all 11 event kinds through a `Hub` in order, asserting Farm / Plot lifecycle, input source classification, result counts, upstream/downstream traffic, and pending counts |
| `TestEventsCarryPayloadAndIDs` | Asserts that the payloads, errors, timestamps, `Attempt`, `Parents`, and ID distinctness of `SeedRipenEvent` / `SeedWitherEvent` / `SeedReplantEvent` / `SeedInputEvent` stay unchanged after dispatch |

## Test Helper Types

| Type | Role |
|------|------|
| `nodeProbe` | Probe state of a single Plot: start/end flags and times, `external`, `upstream`, `downstream`, `succeeded` / `failed` / `skipped` |
| `metricsProbe` | A set of probes implementing `Watcher`; `OnEvent` accumulates through a `type switch` following `MetricsWatcher`'s definitions |

`nodeProbe.pending()` returns `external + Σupstream - (succeeded + failed + skipped)`, the same algorithm as `PlotMetrics.Pending` (but without clamping at a lower bound of 0, because the test flow is complete).

## Key Assertions and Design Intent

### Synthetic Lifecycle: `TestSyntheticLifecycleFeedsMetrics`

Event order (`a` and `b` are two Plots, with one edge `a → b`):

```text
FarmStart
→ PlotStart(a) → PlotStart(b)
→ SeedInput(a, 外部 ×2) → SeedReplant(a) → SeedRipen(a) → SeedPrune(a)
→ SeedInput(b, From=a) → SeedRipen(b)
→ SeedInput(b, 外部 ×1) → SeedWither(b)
→ SealInput(a) → SealMerge(b)
→ PlotEnd(a) → PlotEnd(b) → FarmEnd
```

Verification points:

| Assertion | Description |
|------|------|
| `graphStarted && graphEnded` | Both the opening and closing Farm events are observed, and dispatch loses no event |
| `a.started && a.ended`, likewise for `b` | Plot lifecycle events are complete |
| `a.external == 2` | Only the two `SeedInputEvent`s with `From == ""` count as external input |
| Result counts of `a` `(succeeded, failed, skipped) == (1, 0, 1)` | `SeedReplantEvent` counts toward no result |
| `a.downstream["b"] == 1` | Upstream delivery is recorded as downstream traffic on the **source** side |
| `b.external == 1`, `b.upstream["a"] == 1` | The receiver separates external input from upstream input by `From` |
| Result counts of `b` `(1, 1, 0)` | Ripened and withered are counted separately |
| `pending() == 0` for both nodes | Inputs and results correspond one-to-one, no event is missing |

The Hub uses `WithErrorHandler(func(err error) { t.Errorf(...) })`, so any observer error fails the test directly—this also verifies that "the synthetic event flow does not trigger Hub error paths".

### Payload and IDs: `TestEventsCarryPayloadAndIDs`

- Uses `WatcherFunc` to collect events into `[]Event`, then asserts **value types** one by one (`got[0].(SeedRipenEvent)`)—verifying events are dispatched as struct values, not pointers.
- Compares `success.Seed` with `seed` (an anonymous struct value) and checks `success.Fruit == "fruit"`: verifying the `any` payload is passed through as-is.
- `SeedID != FruitID`, `WeedID != SeedID`: verifying the producer must allocate new IDs for downstream events.
- `errors.Is(fail.Err, boom)`, `errors.Is(retry.Err, boom)`: verifying the `error` is neither wrapped nor lost.
- `!success.Time.Equal(...)` compares with `time.Equal` instead of `==`, avoiding false results from the monotonic clock field inside `time.Time`.
- `input.Parents == [2]` and `input.From == "a"`: verifying both lineage and source are retained for a delivered seed.

## Running

```bash
go test ./pkg/scarecrow -run 'TestSyntheticLifecycleFeedsMetrics|TestEventsCarryPayloadAndIDs' -v
```

It also passes without `-race`; this file involves no concurrency, which is covered by [`hub_test.md`](./hub_test.md).

## Notes

- The test builds event structs directly, bypassing the producer logic in `pkg/plot`; what it verifies is therefore **whether the event contract can support metrics**, not whether the producer's values are reasonable (such as `Elapsed` precision or `SeedRepr` truncation).
- When adding an event type, the `type switch` in `metricsProbe.OnEvent` raises no compile error (`Event` is a closed interface, but a `switch` may omit branches), so it must be confirmed manually whether the new event should affect probe state.
- Renaming event fields breaks this file and the tests in `pkg/persist` at the same time; that chain reaction is expected.
