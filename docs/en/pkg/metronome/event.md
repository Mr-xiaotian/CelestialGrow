# pkg/metronome/event.go

> 📅 Last Updated: 2026/11/26

`pkg/metronome/event.go` defines a single value object, `TickEvent` — the only payload the tick system passes to every `Ticker` on each firing. It bundles "which beat" together with "when it actually fired", so receivers can make throttling/sampling decisions and also time-sensitive measurements.

## Purpose

- Serves as the only argument of `Ticker.OnTick(event TickEvent)`, running through the whole dispatch chain `Clock → Hub → child Hub → leaf Ticker`.
- Carries a **monotonically increasing sequence number** `Seq`, used by `Periodic` filtering (`(Seq-1)%period == 0`) and by business-side "do this every N beats" logic.
- Carries the **actual firing instant** `FiredAt`, used by tickers that compute latency, sampling periods or aligned log timestamps.

## Core Objects

### `type TickEvent struct`

```go
type TickEvent struct {
    Seq     int       // 节拍序号，从 1 开始
    FiredAt time.Time // 实际触发时间
}
```

| Field | Type | Semantics |
|------|------|------|
| `Seq` | `int` | Tick sequence number, monotonically increasing **starting at 1**. It counts "how many times the clock really fired", not "how many times a ticker received something". |
| `FiredAt` | `time.Time` | The actual firing instant obtained by the clock from `time.Now()`, carrying a **monotonic clock component**. |

The struct has no methods and holds no locks, so it can be copied freely and passed across goroutines.

## Important Details

### The semantic boundary of `Seq`

- **It starts at 1**: `Clock.run` has `seq := 0` and does `seq++` each time it really lands on a grid firing point, so the first event is `Seq: 1`. The test `TestClockTicksRoot` asserts receiving `[]int{1, 2, 3}` directly.
- **It is only generated at the Clock layer**: `Hub.OnTick` passes the upstream event's `Seq` through unchanged and never renumbers it. Therefore, in a nested Hub tree every ticker sees `Seq` values from the same clock sequence.
- **Period-filtered beats still consume sequence numbers**: a ticker with `TickPeriod() = 2` only receives `Seq = 1, 3, 5, ...`, but the numbers `2, 4` are indeed consumed by the clock. This matches the expectations of `TestClockRespectsRootTickPeriod` / `TestHubFiltersByTickerPeriod`.
- **Restarting resets it to zero**: after a `Clock` is `Stop`ped and `Start`ed again, `run` restarts from `seq = 0` and the first event of the new sequence is still `Seq: 1`. Do not treat it as a globally unique ID across restarts.
- **Its type is `int`**: an extremely long run (about 290,000 years at one beat per millisecond) would be needed to reach overflow, so in practice there is no need to worry, but keep this in mind if you persist `Seq` as a "globally unique sequence number".

### `FiredAt` and the monotonic clock

`FiredAt` comes from `time.Now()`, and the Go runtime attaches a monotonic clock reading to it. `Clock` internally uses `time.Until` / `Before` / `Sub` for scheduling arithmetic, and these prefer the monotonic component, therefore:

- a wall-clock change (NTP correction, manual time change, time zone switch) does **not** make the ticks speed up, go backwards or stall;
- but when you format `FiredAt` as a string you see wall-clock time, which may differ from the monotonic reading used for internal scheduling.

`FiredAt` is the **actual firing time** and not the scheduled time: if the previous beat took long to process, the current event still records the real `time.Now()` and will be noticeably later than the grid point (see the anchoring and beat-skipping section of [`clock.md`](clock.md)).

## Usage Example

### Throttling by sequence number inside a ticker

```go
package main

import (
    "fmt"
    "time"

    "github.com/Mr-xiaotian/CelestialGrow/pkg/metronome"
)

// EveryFiveTicks 只在第 1、6、11… 拍执行。
type EveryFiveTicks struct{}

func (EveryFiveTicks) OnTick(event metronome.TickEvent) error {
    if event.Seq%5 != 1 {
        return nil
    }
    fmt.Printf("第 %d 拍，于 %s\n", event.Seq, event.FiredAt.Format(time.RFC3339))
    return nil
}
```

> A better approach is to implement `metronome.Periodic` (returning `TickPeriod() = 5`) so that `Hub` filters centrally before dispatching, avoiding duplicated modulo logic in every ticker; the two are semantically equivalent (`Periodic` uses `(Seq-1)%period == 0`).

### Observing scheduling latency with `FiredAt`

```go
type LagProbe struct{ last time.Time }

func (p *LagProbe) OnTick(event metronome.TickEvent) error {
    if !p.last.IsZero() {
        fmt.Println("相邻两拍实际间隔:", event.FiredAt.Sub(p.last))
    }
    p.last = event.FiredAt
    return nil
}
```

Because `Clock` uses the "skip missed beats, never catch up" policy, after slow processing this interval will be larger than the nominal period, which is the expected behavior.

## Notes

- **Do not rewrite the event**: `TickEvent` is passed by value, so a ticker's modification of the argument does not affect other receivers, and no locking is needed.
- **Do not use `Seq` as a business primary key**: it resets to zero when the `Clock` restarts, and skipped beats are never renumbered.
- **`FiredAt` may be later than expected**: it records the real firing time and never fakes a scheduled time when scheduling lags.
- **The zero value of `FiredAt` is meaningless**: when constructing `TickEvent{Seq: 1}` by hand (for example calling `Hub.OnTick` directly in a unit test), `FiredAt` is the zero value, and logic that only relies on `Seq` is unaffected.
