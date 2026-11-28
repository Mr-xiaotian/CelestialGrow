# pkg/scarecrow/hub_test.go

> 📅 Last Updated: 2026/11/26

## Purpose

`pkg/scarecrow/hub_test.go` is the behavioral contract test for `Hub`: it covers **dispatch order, registration rules, error and panic isolation, lifecycle start/stop, nested topologies, `Find` search priority, and concurrent registration/dispatch**. These are the executable evidence for each semantic in [`hub.md`](./hub.md).

## Test Distribution

| Group | Test | Behavior verified |
|------|------|-----------|
| Dispatch | `TestHubFansOutInRegistrationOrder` | Three watchers are called as `a, b, c` in registration order (a mutex guards the collected slice) |
| Dispatch | `TestHubAllowsDuplicateRegistration` | The same instance registered twice is called twice for a single event |
| Dispatch | `TestHubWithoutWatchers` | Dispatch on an empty Hub returns `nil` and does not panic |
| Dispatch | `TestHubIgnoresNilEvent` | `OnEvent(nil)` returns `nil` and calls **no** watcher |
| Registration | `TestHubRejectsNilWatcher` | `AddWatcher(nil)` returns an error |
| Registration | `TestHubNestsChildHub` | After a parent Hub registers a child Hub, parent events reach the child Hub's watchers, exactly once |
| Registration | `TestHubRejectsCyclicRegistration` | Self-registration is rejected; after `a→b`, `b→a` is rejected |
| Error isolation | `TestHubIsolatesWatcherError` | A failing watcher does not affect later watchers; the error is reported exactly once, `errors.Is` sees through the wrapping, and the text carries `scarecrow` context |
| Error isolation | `TestHubRecoversWatcherPanic` | A watcher's `panic("kaboom")` is recovered, the report text contains `kaboom`, and later watchers still run |
| Error isolation | `TestHubSurvivesErrorHandlerPanic` | A panicking `ErrorHandler` does not interrupt dispatch (`log` output is redirected to `io.Discard`) |
| Error isolation | `TestZeroValueHubIsUsable` | `&Hub{}` can register and dispatch without calling `NewHub`, falling back to the default error handler |
| Lifecycle | `TestHubStartsAndStopsLifecycleInOrder` | Three `Lifecycle`s start as `a, b, c` and stop as `c, b, a`; repeated `Start` / `Stop` has no side effect |
| Lifecycle | `TestHubSkipsNonLifecycleWatcher` | A plain `Watcher` does not take part in start/stop, but still receives a dispatched `PlotStartEvent` after `Stop` |
| Lifecycle | `TestHubRollsBackOnStartFailure` | When the second child fails to start, the started `a` is rolled back (recording `stop:a`), the error wraps `boom` and carries `start` context; after the failure `Stop` has no side effect and another `Start` returns an error |
| Lifecycle | `TestHubStopBeforeStartIsNoop` | Calling `Stop` before startup returns `nil` and calls no child |
| Lifecycle | `TestHubRejectsStartAfterStop` | After `Stop`, another `Start` returns an error and no lifecycle method is called again |
| Lifecycle | `TestHubStartsNestedHub` | The parent Hub starts a nested child Hub: `start:child → start:parent → stop:parent → stop:child` |
| Lifecycle | `TestHubJoinsStopErrors` | When both children fail to `Stop`, `errors.Is` matches both errors (`errors.Join`) |
| Search | `TestFindFindsDirectWatcher` | `Find[*markerWatcher]` hits a direct instance |
| Search | `TestFindSearchesNestedHub` | Hits an instance inside a nested child Hub |
| Search | `TestFindPrefersDirectWatcher` | A direct watcher takes priority over an instance of the same type inside a nested Hub |
| Search | `TestFindMissingWatcher` | Both an empty Hub and a `nil` Hub return `false` |
| Concurrency | `TestHubConcurrentAddAndEmit` | After 64 concurrent registrations + 128 concurrent dispatches no registration is lost; one more dispatch calls all 64 watchers |

## Key Flows

```mermaid
flowchart TD
    subgraph CONCURRENT[Concurrent Scenario]
        A1[64 goroutines<br/>AddWatcher] --> L[Hub write-lock append]
        A2[128 goroutines<br/>OnEvent] --> S[Hub read-lock snapshot]
    end
    L --> R[listeners length = 64]
    S --> R
    R --> F[send one more event]
    F --> C[each watcher called once<br/>64 in total]
```

`TestHubConcurrentAddAndEmit` counts calls with `atomic.Int64` and waits for all goroutines with `sync.WaitGroup`; afterwards it reads `hub.listeners` directly (same-package test) to confirm the registration count, then clears the counter with `calls.Store(0)` and dispatches one more event serially, asserting the call count equals the watcher count. This step is the key check of **snapshot integrity**: dispatch during concurrent registration may see different subsets of watchers, but the final state must contain every registered entry.

### Lifecycle: the `Lifecycle` contract

`lifecycleRecorder` records call strings under a mutex, and `lifecycleWatcherStub` implements `OnEvent` / `Start` / `Stop` at once, with injectable `startErr` and `stopErr`. The related cases cover:

```mermaid
flowchart TD
    A[Start: in registration order] --> B{does the child implement Lifecycle?}
    B -->|no| C[skip, only takes part in dispatch]
    B -->|yes| D[call Start]
    D --> E{error returned?}
    E -->|yes| F[Stop started children in reverse<br/>state = hubStopped]
    E -->|no| G[record into the lifecycle slice]
    G --> H[state = hubRunning]
    H --> I[Stop: call Stop in reverse]
    I --> J[errors.Join aggregates errors<br/>state = hubStopped]
```

Key assertions:

| Case | Assertion |
|------|------|
| `TestHubStartsAndStopsLifecycleInOrder` | The call sequence is exactly `start:a, start:b, start:c, stop:c, stop:b, stop:a`; a second `Start` / `Stop` produces no new records |
| `TestHubSkipsNonLifecycleWatcher` | A plain `WatcherFunc` is not started or stopped; `OnEvent` can still call it after `Stop` |
| `TestHubRollsBackOnStartFailure` | The failing case leaves only `start:a, stop:a`; `c` is never started; `errors.Is(err, boom)` holds and the text contains `start`; after the failure `Start` returns an error |
| `TestHubStopBeforeStartIsNoop` | Before startup `Stop` returns `nil` and nothing is recorded |
| `TestHubRejectsStartAfterStop` | After stopping, `Start` reports an error and the call log still holds only one `start:a` / `stop:a` pair |
| `TestHubStartsNestedHub` | The nested Hub is started and stopped recursively, in the order `start:child, start:parent, stop:parent, stop:child` |
| `TestHubJoinsStopErrors` | `Stop` returns both `a boom` and `b boom`, and `errors.Is` is true for both |

## Important Details

- **Error assertions are two-layered**: they require both `errors.Is(reported[0], boom)` (showing the `%w` wrapping preserved the error chain) and `strings.Contains(..., "scarecrow")` (showing errors carry context instead of being passed through verbatim).
- **Panic assertions check a substring only**: `Hub.deliver` formats the panic value with `%v`, so the test asserts that the report text contains `kaboom` rather than comparing the full format.
- **Log redirection**: `TestHubSurvivesErrorHandlerPanic` and `TestZeroValueHubIsUsable` temporarily call `log.SetOutput(io.Discard)` and restore it in `defer`, so the default handler does not write noise into test output.
- **Lifecycle recorder**: `lifecycleRecorder` locks itself, and `lifecycleWatcherStub.Start` **records nothing** when returning an injected error, so the rollback assertions only see the genuinely successful `start:a`; `Stop` records regardless of failure.
- **Failure is final**: `TestHubRollsBackOnStartFailure` and `TestHubRejectsStartAfterStop` together lock down the "`hubStopped` cannot be started again" semantic; `Stop` in the post-failure state is a no-op.
- **Recursive start/stop of nested Hubs**: `TestHubStartsNestedHub` verifies that `Hub`, being a `Lifecycle`, is started/stopped by its parent Hub; the case asserts only start/stop order, while event dispatch is covered by `TestHubNestsChildHub`.
- **`markerWatcher` is an in-package test type**: it implements only `OnEvent`, and exists to verify that the generic `Find` can search for a "concrete pointer type" (not merely an interface type).
- **Order assertions use `slices.Equal`**: `TestHubFansOutInRegistrationOrder` collects the call order into `[]string` and compares it whole, which exposes out-of-order calls better than per-item assertions.
- **The `seen` deduplication branch is not directly covered**: `rejectCycle` hits the `seen` dedup on diamond topologies (the same Hub reachable through several paths), and the `seen` in `Find` guards against abnormal topologies; the current tests cover only self-loops and chain nesting, not diamond topologies.

## Running

```bash
# 全量（推荐带竞态检测）
go test ./pkg/scarecrow -race -v

# 只跑 Hub 与 Find
go test ./pkg/scarecrow -race -run 'TestHub|TestFind|TestZeroValueHub' -v
```

## Notes

- The concurrency test asserts **eventual consistency**, not intermediate timing: it does not guarantee that one event reaches every watcher, only that no registration is lost and the final dispatch is complete.
- The test accesses the private fields `hub.listeners` and `hub.mu` directly, so it must run inside the same package (`package scarecrow`); moving it to an external test package would lose that ability.
- If error wording changes, the `strings.Contains` assertions in `hub_test.go` must follow (they currently depend only on the `scarecrow` prefix and the panic value itself).
- The cross-Hub concurrent race in cycle detection is not covered by tests (the source comment already declares that usage unsupported); before adding such a test, first decide whether the implementation should be tightened.
