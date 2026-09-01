# pkg/plot/counter.go

> 最后更新日期: 2026/09/01

`counter.go` 定义 `Counter`：一个并发安全的「种子 / 果实 / 杂草」三联计数器。`Plot` 通过内嵌 `*Counter` 直接获得 `AddSeedNum` / `GetCompleted` 等方法，用于在多个 tend 协程同时写、观察者同时读的场景下避免加锁。

## 作用

- 跟踪当前 plot 的「种子总数」「成功产出（果实）」「失败产出（杂草）」；
- 统计上游 plot 的产出量（用于在 `GetSeedNum` 中合并出真实总输入数）；
- 提供「是否完成」的谓词 `IsFinish` 与「已完成数」`GetCompleted` 供观察者/调度逻辑使用。

## 核心对象

### `Counter` 结构

```go
type Counter struct {
    seedNum  atomic.Int64
    fruitNum atomic.Int64
    weedNum  atomic.Int64

    upstreamYields map[string]*atomic.Int64
}
```

| 字段 | 类型 | 含义 |
|------|------|------|
| `seedNum` | `atomic.Int64` | 本地播入 + 上游转入的种子总数（仅 `AddSeedNum` 自增，`GetSeedNum` 会合并上游） |
| `fruitNum` | `atomic.Int64` | 成功产出的果实数（`bearFruit` 调用 `AddFruitNum(1)`） |
| `weedNum` | `atomic.Int64` | 失败产出的杂草数（`bearWeed` 调用 `AddWeedNum(1)`） |
| `upstreamYields` | `map[string]*atomic.Int64` | 上游 plot 名称 → 其 fruit 计数指针；`GetSeedNum` 会把它们累加进来 |

> 注意：`seedNum` 本身**只**记录「本地 `Seed` 调用次数」；上游转入的 fruit 不通过 `AddSeedNum` 自增，而是通过 `upstreamYields` 在 `GetSeedNum` 时合并统计。`Plot` 自身并不直接调用 `AddUpstream`（这是 Farm 的事），`Counter` 只是提供存储与合并能力。

## 公开方法

### 构造

#### `NewCounter() *Counter`

构造一个空计数器，`upstreamYields` 初始化为空 map（**注意**：`NewCounter` 没有 lazy-init，所以 `NewCounter` 返回的对象调用 `GetSeedNum` 不会 panic——只是 map 为空，`range` 0 次）。

### Adders（自增）

| 方法 | 签名 | 行为 |
|------|------|------|
| `AddSeedNum` | `func (c *Counter) AddSeedNum(addNNum int)` | 原子地把 `seedNum` 增加 `addNNum` |
| `AddFruitNum` | `func (c *Counter) AddFruitNum(addNNum int)` | 原子地把 `fruitNum` 增加 `addNNum` |
| `AddWeedNum` | `func (c *Counter) AddWeedNum(addNNum int)` | 原子地把 `weedNum` 增加 `addNNum` |

> 参数名 `addNNum` 取自源码（多次自增场景保留命名风格），调用方通常传 `1`。

### Getters（只读）

| 方法 | 签名 | 返回值 |
|------|------|--------|
| `GetSeedNum` | `func (c *Counter) GetSeedNum() int` | `seedNum` + `Σ upstreamYields[*].Load()`；即「本 plot 处理过的总种子数」 |
| `GetFruitNum` | `func (c *Counter) GetFruitNum() int` | 成功果实数 |
| `GetWeedNum` | `func (c *Counter) GetWeedNum() int` | 失败杂草数 |
| `GetCompleted` | `func (c *Counter) GetCompleted() int` | `GetFruitNum() + GetWeedNum()` |

### 谓词

| 方法 | 签名 | 含义 |
|------|------|------|
| `IsFinish` | `func (c *Counter) IsFinish() bool` | `GetCompleted() == GetSeedNum()`；二者**不**是同一时刻的快照，但在 tend 全部结束后会自然趋于稳定 |

## 与 Plot 的协作

`Plot` 通过内嵌 `*Counter` 获得上述所有方法：

- `Plot.Seed` 调用 `AddSeedNum(1)`；
- `bearFruit` 调用 `AddFruitNum(1)` + `reportProgress`；
- `bearWeed` 调用 `AddWeedNum(1)` + `reportProgress`；
- 观察者通过 `GetCompleted` / `GetSeedNum` 计算进度（`completed / seedNum`）。

`Counter` 本身**不感知**上游 plot，是被 `Plot` 通过「共享 `*atomic.Int64` 指针」的方式在 plot 之间传递产量的：

```go
// 上游 plot 提供
p.GetYieldCounter() // = &p.fruitNum

// 下游 plot 登记
next.AddUpstream(upstreamName, upstream.GetYieldCounter())
```

之后上游的每次 `AddFruitNum(1)` 都会让下游的 `GetSeedNum` 自然增长。

## 注意事项

- `seedNum` 与 `fruitNum` / `weedNum` **不是**同一时刻的原子快照，因此 `IsFinish` 可能在「最后一批 tend 还没全部走完 `AddFruitNum` / `AddWeedNum`」的瞬间返回 `true` 之前先返回 `false`，这是正常现象，最终会稳定。
- `upstreamYields` 仅在 `GetSeedNum` 中读取，**Counter 自身不负责写入**——上游 plot 的 fruit 计数更新由上游自己完成，本 Counter 只是「读指针」。
- `AddSeedNum` / `AddFruitNum` / `AddWeedNum` 接受 `int` 但内部强转为 `int64` 后 `Add`；传负数会让计数倒退，业务层应避免。
