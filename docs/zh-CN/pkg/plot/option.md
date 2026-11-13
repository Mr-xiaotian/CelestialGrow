# pkg/plot/option.go

> 📅 最后更新日期: 2026/11/13

`option.go` 定义所有节点的「函数选项」配置层。所有 `Option` 都是把 `*plotOptions` 修改一遍的小闭包；`newBasePlot` 会先取 `defaultOptions()`，再依次应用用户传入的 `Option`，因此 `NewSinglePlot` / `NewSplitPlot` / `NewRoutePlot` 共享同一套配置。

## 作用

- 把 `numTenders` / `chanSize` / `maxRetries` / `retryDelay` / `retryIf` / `pruneIf` 等可调参数封装为「函数选项」；
- 让各节点的构造签名保持简洁（`name, cultivator, opts...`），同时支持未来追加新配置而不破坏 API；
- `plotOptions` 被 `basePlot` 直接内嵌，因此选项对三种节点（`SinglePlot` / `SplitPlot` / `RoutePlot`）同样生效。

## 核心类型

```go
type Option func(*plotOptions)

type plotOptions struct {
    numTenders int
    chanSize   int
    maxRetries int
    retryDelay func(attempt int) time.Duration
    retryIf    func(error) bool
    pruneIf    func(any) bool
}
```

`Option` 即「接收 `*plotOptions`、就地修改、什么都不返回」的函数，是 Go 中常见的可选参数模式。

> 日志级别**不在** `plotOptions` 中：它由 `Run(seeds, logLevel)` 在运行时传给 `assembly.StartPlotResources`，因此同一个 plot 的不同次运行可以使用不同的日志级别。

## 默认值

`defaultOptions()` 的实现：

| 字段 | 默认值 | 含义 |
|------|--------|------|
| `numTenders` | `runtime.NumCPU()` | tender（tend 协程）并发数 |
| `chanSize` | `runtime.NumCPU()` | `seedChan` 缓冲大小 |
| `maxRetries` | `1` | 最大重试次数（不含首次），即「默认重试 1 次」 |
| `retryDelay` | `func(attempt int) time.Duration { return 0 }` | 重试前不等待 |
| `retryIf` | `func(error) bool { return true }` | 任何错误都重试 |
| `pruneIf` | `nil` | 不修剪 |

## `WithXxx` 函数一览

下表覆盖 `option.go` 中所有公开的 `WithXxx` 函数，参数、默认值、效果均以源码为唯一事实来源。

| 函数 | 参数 | 默认值 | 作用 |
|------|------|--------|------|
| `WithTenders(n int) Option` | `n`：tender 协程数 | `runtime.NumCPU()` | 设置 `numTenders`，控制 `sprout` 中 `sem` 信号量大小，从而决定最大并发培育数 |
| `WithChanSize(n int) Option` | `n`：通道缓冲大小 | `runtime.NumCPU()` | 设置 `chanSize`，用于分配本节点 `seedChan` 的缓冲；下游的 `yieldChans` 不额外分配缓冲（直接复用下游的 `seedChan`） |
| `WithMaxRetries(n int) Option` | `n`：最大重试次数（不含首次） | `1` | 设置 `maxRetries`。`WithMaxRetries(2)` 表示最多执行 3 次（1 次原始 + 2 次重试） |
| `WithRetryDelay(fn func(attempt int) time.Duration) Option` | `fn`：重试间隔策略 | `func(int) time.Duration { return 0 }` | 设置 `retryDelay`。`attempt` 从 1 开始递增，常用于实现「指数退避」 |
| `WithRetryIf(fn func(error) bool) Option` | `fn`：错误过滤器 | `func(error) bool { return true }` | 设置 `retryIf`。返回 `true` 的错误才参与下一次重试，返回 `false` 则立即终止重试循环并走 `witherSeed` |
| `WithPruneIf[S any](fn func(S) bool) Option` | `fn`：修剪谓词 | 未设置（`nil`，即不修剪） | 设置 `pruneIf`。谓词在 `cultivator` **之前**判断；返回 `true` 的种子直接终结为 `prune`，不触发重试、不调用 `cultivator`、也不向任何下游产出 |

> `WithChanSize` 的源码注释仍写作「seedChan/fruitChan」，但当前骨架只有 `seedChan` 使用该容量，`fruitChan` 已不存在。

## 与重试的协作示例

下例组合三个 Option 实现「最多 3 次重试、按 attempt 线性退避、永久错误不重试」：

```go
p := plot.NewSinglePlot("flaky",
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

> 重试循环的具体行为见 `plot.md` 的「重试循环 `tend`」一节：`maxRetries=3` 时最多跑 4 次（1 原始 + 3 重试），最后一次失败时不发 `SeedReplant` 事件，但仍会执行一次 `retryDelay(4)`。

## 与修剪的协作示例

以下通过 `WithPruneIf` 实现「重复种子直接修剪跳过，不再重复培育」：

```go
var seenSeeds sync.Map // 注意：需 import "sync"

p := plot.NewSinglePlot("dedup",
    func(seed int) (int, error) { return seed * 10, nil },
    plot.WithTenders(2),
    plot.WithPruneIf(func(seed int) bool {
        _, loaded := seenSeeds.LoadOrStore(seed, struct{}{})
        return loaded // 已见即修剪
    }),
)
```

> 修剪语义见 `plot.md` 的「修剪路径 `pruneSeed`」一节：被修剪的种子在 `cultivator` 之前就被截断，计入 `Skipped` 且 `Processed` 守恒。

## 注意事项

- `WithMaxRetries(n)` 中的 `n` **不包含**首次执行；`n=0` 表示「不重试」。
- `WithRetryDelay` 的 `attempt` 是 1-based。源码在每次失败后都会 `Sleep(retryDelay(attempt))`，**包括最后一次**（此时不会再重试），因此退避函数会在「可能出现空等」的位置被多调用一次。
- `WithPruneIf` 的谓词在 `cultivator` 之前判断，且**优先于重试**：一旦谓词返回 `true`，该种子既不调用 `cultivator`，也不进入重试循环，直接走 `pruneSeed`。谓词默认 `nil`（不修剪）。
- `WithPruneIf` 内部用类型断言把 `any` 还原为 `S`；断言失败时按「不修剪」处理。泛型参数 `S` 由传入谓词的参数类型推断，无需显式指定。
- 日志级别由 `Run` 的 `logLevel` 参数控制，不再存在 `WithLogLevel` Option。
- 因为 `Option` 是简单的闭包叠加，**后传入的同名 Option 会覆盖先传入的**；如果业务需要更复杂的合并策略，可在外层自行处理后只传一次。
- `numTenders` / `chanSize` 的默认值均为 `runtime.NumCPU()`，在容器中该值反映的是宿主可见 CPU 数；I/O 密集型业务建议结合 `WithTenders` / `WithChanSize` 显式调大。
