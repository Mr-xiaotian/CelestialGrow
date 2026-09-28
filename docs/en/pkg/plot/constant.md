# pkg/plot/constant.go

> 📅 Last Updated: 2026/09/24

`constant.go` is the collection of private constants in the `plot` package, and currently defines only a single sentinel string, used to distinguish "external caller" from "some named upstream plot" in the `runtime.Payload.Source` field.

## Purpose

- Give `Payload.Source` a stable sentinel value, avoiding the literal `"__input__"` scattered around the implementation;
- Work together with `basePlot.Seal` and `basePlot.markSealed` to implement the semantics that "**an external `Seal()` is a hard termination**".

## Public Symbols

This file **contains only one unexported constant**:

```go
const sourceInput = "__input__"
```

| Constant | Type | Value | Purpose |
|------|------|----|------|
| `sourceInput` | `string` | `"__input__"` | Serves as the sentinel value of `runtime.Payload.Source`, meaning "this seal comes from an external caller rather than from some upstream plot" |

> Since it is an unexported constant, external packages **cannot** reference it directly; to determine "whether a Payload was injected externally", reason from the behavior of `Seal` / `Seed` rather than reading this value.

## Usage Sites

`sourceInput` is used in 2 places within `pkg/plot`, both in `pkg/plot/plot_base.go`:

1. `basePlot.Seal()`:

   ```go
   func (p *basePlot[S, F, Y]) Seal() {
       sealID := p.eventClient.Emit("seal", []int{})
       p.seedChan <- runtime.Payload[S]{Signal: runtime.SignalSeal, Source: sourceInput, EventID: sealID}
   }
   ```

   It marks that this seal comes from an external `Seal()` call.

2. `basePlot.markSealed`:

   ```go
   if source == sourceInput {
       sealedFrom[sourceInput] = sealID
       return true
   }
   ```

   When `markSealed` sees `Source == sourceInput`, it **returns `true` directly** (the input is already closed) and **no longer waits for other upstream seals** — this is the "hard termination" semantics of an external `Seal()`.

On the normal path, however, seal propagation comes from the finishing logic of `sprout` itself:

```go
sealPayload := runtime.Payload[Y]{Signal: runtime.SignalSeal, Source: p.name, EventID: sealID}
```

Note that `Source` is the `name` of the current plot (not `sourceInput`), so downstream can correctly identify the origin.

## Notes

- Do not redefine or hardcode `"__input__"` in external packages; that string is an internal implementation detail of `pkg/plot` and may change in the future.
- Only `Seal()` produces a seal with `Source == sourceInput`; the seal emitted when an upstream plot finishes always has its own `name` as `Source`.
- If in Farm mode you see a downstream plot finishing early (no longer waiting for other upstreams), it is most likely because one of the upstreams called its own `Seal()`, triggering this hard-termination path.
