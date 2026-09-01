# pkg/plot/option.go

> 最后更新日期: 2026/09/01

`option.go` 定义 `Plot` 的「函数选项」配置层。所有 `Option` 都是把 `*plotOptions` 修改一遍的小闭包；`NewPlot` 会用 `defaultOptions()` 初始化默认值，再依次应用用户传入的 `Option`。

## 作用

- 把 `numTends` / `chanSize` / `maxRetries` / `retryDelay` / `retryIf` / `logLevel` 等可调参数封装为「函数选项」；
- 让 `NewPlot` 的签名保持简洁（`name, cultivator, opts...`），同时支持未来追加新配置而不破坏 API。

## 核心类型

```go
type Option func(*plotOptions)

type plotOptions struct {
    numTends   int
    chanSize   int
    maxRetries int
    retryDelay func(attempt int) time.Duration
    retryIf    func(error) bool
    logLevel   string
}
```

`Option` 即「接收 `*plotOptions`、就地修改、什么都不返回」的函数，是 Go 中常见的可选参数模式。

## 默认值

`defaultOptions()` 的实现：

| 字段 | 默认值 | 含义 |
|------|--------|------|
| `numTends` | `runtime.NumCPU()` | tend 协程并发数 |
| `chanSize` | `runtime.NumCPU()` | `seedChan` 缓冲大小 |
| `maxRetries` | `1` | 最大重试次数（不含首次），即「默认重试 1 次」 |
| `retryDelay` | `func(attempt int) time.Duration { return 0 }` | 重试前不等待 |
| `retryIf` | `func(error) bool { return true }` | 任何错误都重试 |
| `logLevel` | `"INFO"` | 日志最低级别 |

## `WithXxx` 函数一览

下表覆盖 `option.go` 中所有公开的 `WithXxx` 函数，参数、默认值、效果均以源码为唯一事实来源。

| 函数 | 参数 | 默认值 | 作用 |
|------|------|--------|------|
| `WithTends(n int) Option` | `n`：tend 协程数 | `runtime.NumCPU()` | 设置 `numTends`，控制 `sprout` 信号量大小，从而决定最大并发培育数 |
| `WithChanSize(n int) Option` | `n`：通道缓冲大小 | `runtime.NumCPU()` | 设置 `chanSize`，用于分配 `seedChan` 的缓冲；不影响下游的 `fruitChans`（它们直接复用下游的 `seedChan`） |
| `WithMaxRetries(n int) Option` | `n`：最大重试次数（不含首次） | `1` | 设置 `maxRetries`。`WithMaxRetries(2)` 表示最多执行 3 次（1 次原始 + 2 次重试） |
| `WithRetryDelay(fn func(attempt int) time.Duration) Option` | `fn`：重试间隔策略 | `func(int) time.Duration { return 0 }` | 设置 `retryDelay`。`attempt` 从 1 开始递增，常用于实现「指数退避」 |
| `WithRetryIf(fn func(error) bool) Option` | `fn`：错误过滤器 | `func(error) bool { return true }` | 设置 `retryIf`。返回 `true` 的错误才参与下一次重试，返回 `false` 则立即终止重试循环并走 `bearWeed` |
| `WithLogLevel(level string) Option` | `level`：日志级别字符串 | `"INFO"` | 设置 `logLevel`，会传给 `persist.NewLogInlet` 用于过滤写入的日志 |

## 与重试的协作示例

下例组合三个 Option 实现「最多 3 次重试、按 attempt 线性退避、永久错误不重试」：

```go
p := plot.NewPlot("flaky",
    func(seed int) (int, error) { return doWork(seed) },
    plot.WithMaxRetries(3),
    plot.WithRetryDelay(func(attempt int) time.Duration {
        return time.Duration(attempt) * 100 * time.Millisecond
    }),
    plot.WithRetryIf(func(err error) bool {
        return !errors.Is(err, ErrPermanent)
    }),
)
```

> 重试循环的具体行为见 `plot.md` 的「失败与重试」一节：`maxRetries=3` 时最多跑 4 次（1 原始 + 3 重试），最后一次失败时不写 `SeedReplant` 日志。

## 注意事项

- `WithMaxRetries(n)` 中的 `n` **不包含**首次执行；`n=0` 表示「不重试」。
- `WithRetryDelay` 的 `attempt` 是 1-based，且只有在 `attempt <= maxRetries` 时才会真正被 `Sleep`，即参数表达「重试前的等待」。
- `WithLogLevel` 只控制「被 `logInlet` 写入的日志最低级别」，不会改变 `eventClient` 的事件 ID 分配。
- 因为 `Option` 是简单的闭包叠加，**后传入的同名 Option 会覆盖先传入的**；如果业务需要更复杂的合并策略，可在外层自行处理后只传一次。
