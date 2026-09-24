# pkg/plot/plot_retry_test.go

> 📅 最后更新日期: 2026/09/24

`plot_retry_test.go` 聚焦 `Plot` 的**重试语义**：验证 `WithMaxRetries` / `WithRetryDelay` / `WithRetryIf` 三个 Option 如何影响 `basePlot.tend` 的重试循环，并最终通过 `Plot.Harvest` 把结果固化为生命周期快照。

## 用例

### `TestPlot_RetrySuccess` —— 重试后成功

- **cultivator**：
  ```go
  func(seed int) (int, error) {
      n := attempts.Add(1)
      if n <= 2 {
          return 0, errors.New("transient error")
      }
      return seed * 10, nil
  }
  ```
- **配置**：`plot.NewPlot("test_retry_success", cultivator, plot.WithTenders(1), plot.WithMaxRetries(3))`
- **seeds**：`[]int{1}`

验证：

1. `mustHarvest` 返回 1 条记录；
2. `Status == "ripen"`、`FruitJSON == "10"`；
3. `attempts.Load() == 3`（前 2 次失败 + 第 3 次成功）。

> 覆盖要点：默认 `retryDelay = 0` 时，瞬时错误经过若干次重试后能成功，且 `SeedRipen` 生命周期被正确写入。

### `TestPlot_RetryExhausted` —— 重试耗尽仍失败

- **cultivator**：`func(seed int) (int, error) { attempts.Add(1); return 0, errors.New("permanent error") }`（永远失败）
- **配置**：`plot.NewPlot("test_retry_exhausted", cultivator, plot.WithTenders(1), plot.WithMaxRetries(2))`
- **seeds**：`[]int{1}`

验证：

1. 1 条记录：`Status == "wither"`、`WitherMessage == "permanent error"`；
2. `attempts.Load() == 3`（1 次原始 + 2 次重试）。

> 覆盖要点：达到 `maxRetries+1` 次后退出循环并进入 `witherSeed`，最后一次失败**不**再写 `SeedReplant` 日志（该日志只在 `attempt <= maxRetries` 时写）。

### `TestPlot_RetryIf` —— 错误过滤器阻止重试

- **cultivator**：永远返回 `errors.New("permanent")`。
- **配置**：`plot.NewPlot("test_retry_if", cultivator, plot.WithTenders(1), plot.WithMaxRetries(3), plot.WithRetryIf(func(err error) bool { return !errors.Is(err, permanent) }))`
- **seeds**：`[]int{1}`

验证：

1. 1 条记录：`Status == "wither"`、`WitherMessage == "permanent"`；
2. `attempts.Load() == 1`（首次即被 `retryIf` 拦下，不再重试）。

> 覆盖要点：自定义 `retryIf` 能精确过滤「不可重试」错误，业务上常用于把 `ErrPermanent` 这类错误排除在重试循环之外。

### `TestPlot_RetryDelay` —— 自定义重试间隔

- **cultivator**：
  ```go
  func(seed int) (int, error) {
      n := attempts.Add(1)
      if n <= 1 {
          return 0, errors.New("transient")
      }
      return seed, nil
  }
  ```
- **配置**：`plot.NewPlot("test_retry_delay", cultivator, plot.WithTenders(1), plot.WithMaxRetries(2), plot.WithRetryDelay(func(attempt int) time.Duration { return 100 * time.Millisecond }))`
- **seeds**：`[]int{1}`
- **额外**：`start := time.Now()`，`Run` 结束后 `elapsed := time.Since(start)`。

验证：

1. 1 条记录：`Status == "ripen"`、`FruitJSON == "1"`；
2. `elapsed >= 100*time.Millisecond`，证明重试前确实等待了 100ms。

> 覆盖要点：`retryDelay(attempt)` 在 `attempt <= maxRetries` 时被 `time.Sleep` 调用，常用于实现指数退避或固定间隔节流。

## 共同前置条件

- 测试包：`package plot_test`（黑盒），不直接访问 `Plot` 未导出字段；
- `attempts` 使用 `sync/atomic.Int32` 跨协程安全累加，确保重试次数断言不受并发影响；
- 复用 `plot_harvest_test.go` 中定义的 `mustHarvest(t, plot harvestable)`（`harvestable` 为测试内局部接口 `interface{ Harvest() ([]persist.LifecycleStatusRecord, error) }`）拉取快照，因此隐式验证了 `Harvest` + `lifecycleSpout` 的串联；
- 四个用例都用 `WithTenders(1)`：并发 tender 不会改变总调用次数，但会让 `attempts.Add(1)` 的时序难以断言，串行化后重试次数才是确定值。

## 运行方式

```bash
go test ./pkg/plot -run "TestPlot_Retry" -v
```

或一次性跑全部 plot 包测试：

```bash
go test ./pkg/plot/... -v
```

## 注意事项

- 状态字符串以源码为准：成功为 `"ripen"`、失败为 `"wither"`；结果字段为 `FruitJSON`，错误字段为 `WitherMessage`。
- `TestPlot_RetryDelay` 用 `elapsed >= 100*time.Millisecond` 作为下界，**不**做严格等于；CI 抖动或调度延迟都会让实际耗时略大，属预期行为。
- `TestPlot_RetryIf` 依赖 `errors.Is` 语义；如果未来 `retryIf` 内部改用 `==` 比较，本测试需同步调整。
- 这些用例没有显式断言「`SeedReplant` 日志被调用了几次」，只通过 `attempts` 总数与最终状态推断；若需要断言日志次数，需配合 `pkg/persist` 的测试工具。
- `TestPlot_RetryExhausted` 断言的是 `attempts.Load() == 3`，即 `1 + maxRetries`；修改默认 `maxRetries` 不会影响该用例（用例显式传入 `WithMaxRetries(2)`）。
