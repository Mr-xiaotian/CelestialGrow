# pkg/farm/farm_route_test.go

> 📅 Last Updated: 2026/09/24

## Purpose

`farm_route_test.go`, as `package farm_test`, covers the end-to-end behavior of `RoutePlot` in a `Farm`: after `Farm.Connect` establishes the multiple-downstream connection `route → {left, right}`, whether `Run` can forward yields to the corresponding downstream according to the routing table.

## Test Focus

- **Directed forwarding**: the same upstream seed is dispatched to different downstreams according to the keys of the `map[string]Y` routing table.
- **Downstream seed counting**: the `GetSeedNum` of `left` / `right` comes from upstream yield counters (neither sows seeds locally in this case), verifying the cross-edge yield counter wiring of `ConnectTo`.
- **Unconnected targets silently skipped**: routing to an unconnected downstream does not affect the other routing entries and does not block the upstream.
- **Order-independent assertions**: downstream receive order is not assumed under concurrent forwarding.

## Test Cases

| Test function | What it verifies |
| --- | --- |
| `TestFarmRunRoutePlot` | `route` (`int → map[string]string`) dispatches by parity: even → `left`, odd → `right`. After 4 seeds, `route.GetFruitNum() == 4`, `left` / `right` each have `GetSeedNum() == 2`, and the actually received seed sets are `{even-2, even-4}` and `{odd-1, odd-3}` respectively |
| `TestFarmRunRoutePlotSkipsUnconnectedTarget` | The routing table contains both the connected `left` and the unregistered `ghost`; only `left` receives `kept-1`, `left.GetSeedNum() == 1`, and `ghost` is silently skipped |

## Key Details

- **Locking around concurrent collection**: both cases protect the downstream `received` map / slice with a `sync.Mutex`, because `RoutePlot.ripenSeed` runs concurrently across multiple tender goroutines.
- **The `assertSeeds` helper**: sorts the actually received set first and then compares item by item, avoiding dependence on the downstream's concurrent receive order.
- **Depends on upstream yield counting**: `left` / `right` have no local `Seed`, and their `GetSeedNum` is contributed entirely by the counters registered upstream in `upstreamYieldCounter`; this is a direct regression for the "per-edge downstream yield counting" refactor.
- **`WithTenders(2)`**: both the routing node and the downstreams explicitly configure 2 tenders, increasing concurrency coverage.

## Related Source

- `Connect` / `Run` in `pkg/farm/farm.go`
- `SourceNodes` / `AddEdge` in `pkg/farm/graph.go`
- `RoutePlot.ripenSeed` in `pkg/plot/plot_route.go` (skips unconnected downstreams)
- `ConnectTo` in `pkg/plot/plot_base.go` (bidirectional yield counter wiring)
- `GetSeedNum` / `GetFruitNum` in `pkg/plot/counter.go`

## How to Run

```bash
go test ./pkg/farm/ -run 'TestFarmRunRoutePlot' -v
```
