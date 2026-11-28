# pkg/reporter/inject_test.go

> 📅 Last Updated: 2026/11/26

## Purpose

`pkg/reporter/inject_test.go` covers three main paths of `InjectionTicker`: a single successful pull and dispatch, the prohibition of use before `Start`, and the constructor's rejection of invalid arguments. It does not depend on a real server; everything is done through `net/http/httptest` and local probes.

## Test distribution

| Test | Coverage |
|------|----------|
| `TestInjectionTickerPullsTasksAndTerminations` | `session_id` query parameter, 2xx response decoding, `tasks`/`terminations` dispatch, `TickPeriod` return value |
| `TestInjectionTickerRejectsUseBeforeStart` | `OnTick` must report an error when `Start` has not been called, and must not make a request |
| `TestNewInjectionTickerRejectsInvalidArguments` | Empty URL, empty SessionID, nil target, missing scheme, non-http(s) scheme |

Test helper type:

```go
type injectionTargetProbe struct {
    tasks        map[string][]any
    terminations []string
}
```

`injectionTargetProbe` implements `InjectionTarget`: `InjectTasks` stores the map directly (no copy), while `InjectTerminations` copies the slice with `append([]string(nil), nodes...)`. It never returns an error itself, so the tests cannot cover the "target injection failure" branch.

## Key assertions and design intent

### Successful pull: `TestInjectionTickerPullsTasksAndTerminations`

The server uses `httptest.NewServer` to receive the request, and the handler first asserts:

```go
if got := r.URL.Query().Get("session_id"); got != "demo@1" {
    t.Fatalf("session_id = %q, want demo@1", got)
}
```

This verifies the equivalence between `url.QueryEscape` encoding (the request line contains `demo%401`) and server-side decoding. It then returns:

```json
{"tasks": {"root": [1.0, "x"]}, "terminations": ["leaf"]}
```

Client-side assertions:

| Assertion | Intent |
|------|------|
| `NewInjectionTicker(...)` has no error | `baseURL` is the `httptest` value `http://127.0.0.1:<port>`, which passes scheme/host validation |
| `TickPeriod() == 5` | An explicitly passed period is not overridden by the default |
| `Start()` / `Stop()` return `nil` | Lifecycle methods produce no errors |
| `reflect.DeepEqual(target.tasks, map[string][]any{"root": {1.0, "x"}})` | JSON numbers decode to `float64`; the map structure and values match exactly |
| `reflect.DeepEqual(target.terminations, []string{"leaf"})` | The termination list is delivered in original order and content |

`Stop` is placed in a `defer` and asserted to return `nil` again, thereby verifying that "`Stop` can be called safely after `OnTick`". The test does not assert the **call order** of `InjectTasks` and `InjectTerminations` (the probe only records results); the ordering guarantee is provided by the two serial calls in the implementation.

### Use before start: `TestInjectionTickerRejectsUseBeforeStart`

```go
ticker, err := NewInjectionTicker("http://example.com", "demo@1", &injectionTargetProbe{}, 1, time.Second)
// ...
if err := ticker.OnTick(metronome.TickEvent{Seq: 1}); err == nil {
    t.Fatal("OnTick() before Start must fail")
}
```

- Using the unreachable `example.com` is deliberate: if the implementation wrongly created the client lazily inside `OnTick`, the test would attempt a real network request instead of passing silently.
- Only "there is an error" is asserted; the error text is not checked; the current implementation returns `reporter: injection client is not initialized`.
- `Stop` is not called, because `client` was never created.

### Constructor argument validation: `TestNewInjectionTickerRejectsInvalidArguments`

| Input | Expected rejection reason |
|------|-------------|
| `("", "demo@1", probe, 1, 1s)` | Empty base URL |
| `("http://example.com", "", probe, 1, 1s)` | Empty session ID |
| `("http://example.com", "demo@1", nil, 1, 1s)` | nil target |
| `("127.0.0.1:5005", "demo@1", probe, 1, 1s)` | Missing scheme (`url.Parse` leaves host empty) |
| `("ftp://example.com", "demo@1", probe, 1, 1s)` | Non-`http`/`https` scheme |

The test only checks `err == nil` and does not compare specific error text. All five cases use `period = 1` and `timeout = 1s`, so they do not interfere with the period/timeout validation.

## How to run

```bash
# 仅本文件的测试
go test ./pkg/reporter -run 'TestInjectionTicker|TestNewInjectionTicker' -v

# 整个包
go test ./pkg/reporter -v
```

The tests start a local `httptest` server (loopback address) and need no external reporting service; no shared state is involved, so they can run with `-race`:

```bash
go test ./pkg/reporter -run 'TestInjectionTicker' -race
```

## Notes

- **Uncovered validation branches**: `period < 0` (`injection period must be positive`) and `timeout < 0` (`pull timeout must be non-negative`) have no tests; default replacement (`period == 0 → 5`, `timeout == 0 → 3s`) also cannot be observed through the public API and can only be verified indirectly.
- **Uncovered error paths**: non-2xx statuses, invalid JSON, `InjectTasks` failure (in which case `InjectTerminations` should not be called), and `InjectTerminations` failure.
- **Uncovered empty-collection normalization**: when the server returns `{"tasks": null, "terminations": null}`, the target should receive an empty map / empty slice; the current tests only cover non-empty payloads.
- **Uncovered URL normalization**: there is no case for a `baseURL` with a trailing `/`; the implementation removes all trailing slashes to avoid concatenating `//api/pull_injection`.
- **Tests do not verify the request method**: the server handler does not check `r.Method`, so a wrongful use of POST would not be caught (the current implementation uses `GET`).
