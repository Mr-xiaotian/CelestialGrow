# pkg/reporter/utils.go

> 📅 Last Updated: 2026/11/26

`pkg/reporter/utils.go` gathers the projection, deep-copy, URL validation, HTTP client, and serialization helper functions internal to the reporter package. They are all **unexported**, but they determine the JSON shape pushed to the server (empty collections are `[]`/`{}` rather than `null`), the timestamp precision, and the failure fallback behavior.

## Purpose

| Function | One-line responsibility | Main callers |
|------|-----------|-----------|
| `toStatusSnapshot` | Metrics → `NodeStatus` projection | `SnapshotTicker.OnTick`, `PushWatcher.OnEvent(FarmEnd)` |
| `copyEdges` | Deep-copies the adjacency list; empty outgoing edges are encoded as `[]` | `PushWatcher.OnEvent(FarmStart)` |
| `copySnapshot` | Deep-copies a status snapshot and its counter dictionaries | `PushWatcher.pushSnapshotAt` |
| `copyIntMap` | Deep-copies a `map[string]int`; an empty map is encoded as `{}` | `copySnapshot`, test probes |
| `copyNodeMeta` | Copies the node metadata table | `PushWatcher.OnEvent(FarmStart)` |
| `newHTTPClient` | Creates a client that owns an independent Transport | `pushHandler.BeforeStart`, `InjectionTicker.Start` |
| `normalizeBaseURL` | Validates and normalizes the reporting base address | `NewPushWatcher`, `NewInjectionTicker` |
| `unixSeconds` | `time.Time` → Unix float seconds | Timestamps of snapshot/graph metadata/error records |
| `toJSONValue` | Ensures any task payload can be JSON-encoded | `PushWatcher.OnEvent(SeedWither)` |

## Core functions

### `toStatusSnapshot`

```go
func toStatusSnapshot(metrics map[string]scarecrow.PlotMetrics) map[string]NodeStatus
```

- The output map capacity is pre-allocated as `len(metrics)`, **keeping every node key**, including metrics with zero values.
- `Status` is converted via `int(PlotStatus)` (`0/1/2`); no string is output.
- `StartTime` / `EndTime`: a zero-value `time.Time` outputs `0.0`, otherwise `unixSeconds(...)`. This guarantees that "not started/not ended" has an explicit sentinel value on the web side instead of a negative pre-1970 value.
- Counters are mapped directly: `InputTotal / Processed / Pending / Succeeded / Failed / Skipped`.
- `UpstreamCounts` / `DownstreamCounts` are passed **by reference**, not copied.
- Derived elapsed times (such as `elapsed`) are deliberately not output: they change on every tick and would break the server's content-based deduplication.

> Safety of reference semantics: `scarecrow.MetricsWatcher.GetPlots` returns a new dictionary each time, so referencing it directly is safe; `PushWatcher.pushSnapshotAt` additionally goes through `copySnapshot`, so the enqueued data is fully isolated from the caller. A custom `MetricsView` that reuses dictionaries must guarantee immutability itself, or copy on the sink side.

### `copyEdges`

```go
func copyEdges(edges map[string][]string) map[string][]string
```

```go
result[from] = append([]string{}, targets...)
```

- `append([]string{}, nil...)` produces a **non-nil empty slice**, so it is JSON-encoded as `[]`; if `targets` were assigned directly, Go would encode a nil slice as `null` and the web side would need extra compatibility handling.
- Each slice's underlying array is also copied, so the caller mutating the original adjacency list after enqueueing does not affect the enqueued payload.
- When `edges == nil`, a **non-nil** empty map is returned (`make(map[string][]string, len(edges))` with capacity 0), encoded as `{}`.

### `copySnapshot`

```go
func copySnapshot(snapshot map[string]NodeStatus) map[string]NodeStatus
```

- It copies each `NodeStatus` value (the struct itself is a value type) and calls `copyIntMap` on its `UpstreamCounts` and `DownstreamCounts`.
- Purpose: after `Inlet.Send` returns, the caller may still hold and modify the original map/dictionaries; the enqueued payload must be decoupled from the caller.
- An empty map input returns a non-nil empty map, i.e. JSON `{}`.

### `copyIntMap`

```go
func copyIntMap(source map[string]int) map[string]int
```

- It creates a non-nil result with `make` and then copies item by item, so a `nil` input yields a non-nil empty map encoded as `{}` rather than `null`.
- This is the fundamental guarantee that the two counter dictionaries in a snapshot never appear as `null`.

### `copyNodeMeta`

```go
func copyNodeMeta(meta map[string]scarecrow.NodeMeta) map[string]scarecrow.NodeMeta
```

- It copies only the map itself (shallow copy); all three fields of `scarecrow.NodeMeta` (`ClassName`, `ExecutionMode`, `MaxWorkers`) are value types with no slices/maps/pointers, so a shallow copy is full isolation.
- A nil input returns a non-nil empty map, i.e. JSON `{}`.

### `newHTTPClient`

```go
func newHTTPClient(timeout time.Duration) *http.Client
```

```go
transport := http.DefaultTransport
if base, ok := transport.(*http.Transport); ok {
    transport = base.Clone()
}
return &http.Client{Timeout: timeout, Transport: transport}
```

- Cloning `http.DefaultTransport` gives each reporter component its **own connection pool**: `CloseIdleConnections()` in `Stop` only closes its own idle connections and does not affect other HTTP clients in the process.
- The clone happens at creation time, so later field modifications to `http.DefaultTransport` (e.g. `MaxIdleConns`) do not retroactively affect already-created clients.
- If the process replaced `http.DefaultTransport` with a non-`*http.Transport` implementation, that instance is shared directly and the isolation of `CloseIdleConnections` no longer holds (`*http.Client.CloseIdleConnections` calls the Transport's optional interface).
- `timeout` serves directly as the upper bound for the whole request (connect + redirects + reading the body); `0` means no timeout, but production callers never pass 0: `pushHandler` uses `DefaultPushTimeout`, and `InjectionTicker` uses the constructor argument (0 is already replaced with `DefaultPullTimeout`).

### `normalizeBaseURL`

```go
func normalizeBaseURL(raw string) (string, error)
```

Processing steps and errors:

| Step | Behavior | Failure error |
|:----:|------|---------|
| 1 | `strings.TrimSpace(raw)`, then strip **all** trailing `/` | Empty result → `reporter: base url is empty` |
| 2 | `url.Parse`, requires `Scheme != ""` and `Host != ""` | `reporter: invalid base url %q` (the quoted value is the **original** `raw`) |
| 3 | The scheme must be `http` or `https` | `reporter: base url must use http or https, got %q` |
| 4 | Returns the normalized string | — |

Key points:

- `TrimRight(..., "/")` removes consecutive slashes: `http://host///` → `http://host`.
- The return value is the **trimmed original string**, without `url.String()` reordering, so case, path, and query parameters are all preserved as-is.
- It only validates "has scheme + has host + protocol is http(s)" and **does not validate the path**. Endpoints are built by string concatenation `baseURL + "/api/..."`, so a base address with a path (`http://host/api`) produces the double prefix `/api/api/...`; an address with a query string inserts the query before the path, with unpredictable results. The base address should only go up to host[:port].
- It does not check reachability, does not check port validity, and does not forbid plaintext HTTP; TLS validation is entirely delegated to `http.Transport`.
- User info (`http://user:pass@host`) is preserved — the `Host` field does not include userinfo, but the return value is still the original string, so credentials are sent with requests.

### `unixSeconds`

```go
func unixSeconds(at time.Time) float64
```

```go
float64(at.Unix()) + float64(at.Nanosecond())/1e9
```

- It outputs Unix seconds **with sub-second precision** (e.g. `time.Unix(10, 500_000_000)` → `10.5`), consistent with celestialflow-web's `number` timestamp contract.
- `Unix()` floors negative times (before 1970) and `Nanosecond()` is always non-negative, so the formula also holds for negative times; however, all reporter call sites exclude zero-value times first.
- Calling it directly on a zero-value `time.Time` yields roughly `-6.2e10`, not `0` — `toStatusSnapshot` guards with `IsZero()`, and the timestamps in `PushWatcher` all come from real events/`time.Now()`.

### `toJSONValue`

```go
func toJSONValue(value any) any
```

- It first uses `json.Marshal(value)` as a **serializability probe**: on success the value is returned as-is (the original type is kept; numbers/structs are not turned into strings).
- On failure it returns the readable string from `fmt.Sprintf("%+v", value)`.
- Design intent: `SeedWitherEvent.Seed` may contain unencodable values such as `chan`, `func`, `complex`, `NaN`/`Inf`, or cyclic references; if placed into `taskFailRecord` directly, `json.Marshal` would make the **entire error report** fail. After degrading, at least the diagnosability of the error is retained.
- The degraded text is unstable (channels/functions print addresses), so it is only suitable for display, not as replay input.

## Key flow

```mermaid
flowchart TD
    PM[map string PlotMetrics] --> P[toStatusSnapshot]
    P -->|NodeStatus| CS[copySnapshot]
    CS -->|copyIntMap ×2| Q[enqueued payload]
    FE[FarmStartEvent.Edges] --> CE[copyEdges]
    CE -->|empty slice, non-nil| Q2[graphMetaRecord]
    NM[FarmStartEvent.NodeMeta] --> CN[copyNodeMeta]
    CN --> Q2
    RAW[raw base url] --> N[normalizeBaseURL]
    N -->|scheme/host validation| C[newHTTPClient]
    C -->|cloned Transport| HTTP[POST / GET]
    T[time.Time] --> U[unixSeconds]
    U --> Q
    U --> Q2
    ANY[Any Seed] --> J[toJSONValue]
    J --> Q3[taskFailRecord]
```

## Usage example

The following program indirectly verifies three things through the public API: a trailing `/` is normalized, a `nil` counter dictionary is encoded as `{}`, and a `nil` adjacency list is encoded as `[]`.

```go
package main

import (
    "encoding/json"
    "fmt"
    "io"
    "net/http"
    "net/http/httptest"
    "time"

    "github.com/Mr-xiaotian/CelestialGrow/pkg/reporter"
    "github.com/Mr-xiaotian/CelestialGrow/pkg/scarecrow"
)

// demoMetrics 返回一个零值节点，counts 故意留 nil。
type demoMetrics struct{}

func (demoMetrics) GetPlots() map[string]scarecrow.PlotMetrics {
    return map[string]scarecrow.PlotMetrics{"root": {}}
}

func main() {
    var bodies []string
    server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
        body, _ := io.ReadAll(r.Body)
        bodies = append(bodies, string(body))
        w.WriteHeader(http.StatusOK)
    }))
    defer server.Close()

    // 故意带尾部斜杠：normalizeBaseURL 会去掉它。
    watcher, err := reporter.NewPushWatcher("demo@1", server.URL+"/", demoMetrics{})
    if err != nil {
        panic(err)
    }
    if err := watcher.Start(); err != nil {
        panic(err)
    }

    // Edges["leaf"] 为 nil：copyEdges 会编码成 []。
    if err := watcher.OnEvent(scarecrow.FarmStartEvent{
        Farm:  "demo",
        Plots: []string{"root", "leaf"},
        Edges: map[string][]string{"root": {"leaf"}, "leaf": nil},
        StartTime: time.Unix(10, 0),
    }); err != nil {
        panic(err)
    }

    // NodeStatus 的 counts 留 nil：copyIntMap 会编码成 {}。
    if err := watcher.PushSnapshot(map[string]reporter.NodeStatus{"root": {}}); err != nil {
        panic(err)
    }

    if err := watcher.Stop(); err != nil {
        panic(err)
    }

    for _, body := range bodies { // bodies[0] 图元，bodies[1] 快照
        var decoded map[string]any
        if err := json.Unmarshal([]byte(body), &decoded); err != nil {
            panic(err)
        }
        fmt.Println(decoded["edges"], decoded["snapshot"])
    }
}
```

In the output, `edges` should contain `"leaf":[]`, and `snapshot.root` should contain `"upstream_counts":{}` and `"downstream_counts":{}` — that is, the empty-collection guarantee of the utils layer.

## Notes

- **The empty-collection convention is a hard contract**: `copyEdges` / `copyIntMap` / `copySnapshot` all deliberately use `make` to return non-nil empty collections. If they returned `nil` directly, JSON would contain `null` and the web-side `Record<string, number>` type would fail to parse. Follow the same pattern when adding new fields.
- **Deep copy only covers known reference fields**: `copySnapshot` handles only the two counter dictionaries; if `NodeStatus` gains slice/map fields in the future, it must be extended accordingly, otherwise the enqueued data would still be modified by the caller.
- **The timestamp unit is seconds, not milliseconds**: all `*_time` / `timestamp` / `ts` values are float seconds (possibly fractional), consistent with the web side's `number` + `Date` second-level convention; do not change them to `UnixMilli`.
- **URL validation happens at construction time**: `normalizeBaseURL` only performs static validation, so DNS, port, TLS, and server health cannot be detected in advance; these errors only appear as network errors on the first `OnTick`/`HandleRecord`.
- **The isolation of `newHTTPClient` depends on the type of `http.DefaultTransport`**: if it is replaced with a custom RoundTripper, multiple components share the same Transport and `CloseIdleConnections` may affect other components.
- **The `toJSONValue` probe has a cost**: every wither event performs a full `json.Marshal` trial on the seed, and large objects incur extra allocations; the trade-off is that a single error report never fails entirely due to payload problems.
