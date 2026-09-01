# pkg/plot/plot_retry_test.go

> 最后更新日期: 2026/09/01

`plot_retry_test.go` 聚焦于 `Plot` 的**重试语义**：验证 `WithMaxRetries` / `WithRetryDelay` / `WithRetryIf` 三个 Option 能否正确影响 `tend` 协程的重试循环，并最终通过 `Plot.Harvest` 把结果固化为生命周期快照。

## 用例

### `TestPlot_RetrySuccess` —— 重试后成功

- **cultivator**：
  ```go
  func(int) (int, error) {
      n := attempts.Add(1)
      if n <= 2 { return 0, errors.New("transient error") }
      return seed * 10, nil
  }
  ```
- **配置**：`WithTends(1)`、`WithMaxRetries(3)`。
- **seeds**：`[1]`。

验证：

1. `mustHarvest` 返回 1 条记录；
2. `Status == "success"`、`ResultJSON == "10"`；
3. `attempts.Load() == 3`（前 2 次失败 + 第 3 次成功）。

> 覆盖要点：默认 `retryDelay=0` 下，瞬时错误经过 N 次重试后能成功，且 `SeedSuccess` 生命周期被正确写入。

### `TestPlot_RetryExhausted` —— 重试耗尽仍失败

- **cultivator**：`func(int) (int, error) { attempts.Add(1); return 0, errors.New("permanent error") }`。
- **配置**：`WithTends(1)`、`WithMaxRetries(2)`。
- **seeds**：`[1]`。

验证：

1. 1 条记录：`Status == "failed"`、`ErrorMessage == "permanent error"`；
2. `attempts.Load() == 3`（1 原始 + 2 重试）。

> 覆盖要点：达到 `maxRetries+1` 次后退出循环，状态记为 `failed`，最后一次失败**不**再触发 `SeedReplant` 日志（重试循环外才进入 `bearWeed`）。

### `TestPlot_RetryIf` —— 错误过滤器阻止重试

- **cultivator**：永远返回 `errors.New("permanent")`。
- **配置**：`WithTends(1)`、`WithMaxRetries(3)`、`WithRetryIf(func(err error) bool { return !errors.Is(err, permanent) })`。
- **seeds**：`[1]`。

验证：

1. 1 条记录：`Status == "failed"`、`ErrorMessage == "permanent"`；
2. `attempts.Load() == 1`（首次即被 `retryIf` 拦下，不再重试）。

> 覆盖要点：自定义 `retryIf` 能精确过滤「不可重试」错误，业务上常用于把 `ErrPermanent` 这类错误排除在重试循环之外。

### `TestPlot_RetryDelay` —— 自定义重试间隔

- **cultivator**：
  ```go
  func(int) (int, error) {
      n := attempts.Add(1)
      if n <= 1 { return 0, errors.New("transient") }
      return seed, nil
  }
  ```
- **配置**：`WithTends(1)`、`WithMaxRetries(2)`、`WithRetryDelay(func(int) time.Duration { return 100 * time.Millisecond })`。
- **seeds**：`[1]`。
- **额外**：`start := time.Now()`，`plot.Run` 完成后 `elapsed := time.Since(start)`。

验证：

1. 1 条记录：`Status == "success"`、`ResultJSON == "1"`；
2. `elapsed >= 100*time.Millisecond`，证明重试前确实等了 100ms。

> 覆盖要点：`retryDelay(attempt)` 在 `attempt <= maxRetries` 时被 `Sleep`，常用于实现指数退避或固定间隔节流。

## 共同前置条件

- 测试包：`package plot_test`，不直接访问 `Plot` 未导出字段；
- `attempts` 计数器用 `sync/atomic.Int32` 跨协程安全累加，确保重试次数断言不受并发影响；
- 所有用例都通过 `mustHarvest` 拉取快照并断言最终状态，因此隐式验证了 `Plot.Harvest` + `lifecycleSpout` 的串联工作。

## 运行方式

```bash
go test ./pkg/plot -run "TestPlot_Retry" -v
```

或一次性跑全部 plot 包测试：

```bash
go test ./pkg/plot/... -v
```

## 注意事项

- `TestPlot_RetryDelay` 用 `elapsed >= 100*time.Millisecond` 作为下界，**不**用严格等于；CI 抖动或调度延迟都可能导致略大于 100ms，是预期行为。
- `TestPlot_RetryIf` 显式依赖 `errors.Is` 语义；如果未来 `retryIf` 内部改用 `==` 比较，本测试需要同步调整。
- 四个用例都使用 `WithTends(1)`，以便让 `attempts` 计数严格按「时间顺序」增长；并发 tend 不会改变总调用次数，但会让 `attempts.Add(1)` 的时序难以断言。
- 用例没有显式断言「`SeedReplant` 日志次数」，仅通过 `attempts` 总数与最终状态推断；如果未来需要把「`SeedReplant` 被调用的次数」也纳入断言，需要配合 `pkg/persist` 的测试工具。
