# pkg/observer/progress.go

> 最后更新日期: 2026/09/01

## 作用

`pkg/observer/progress.go` 提供了 `Observer` 接口的默认终端可视化实现 `ProgressBar`。它基于第三方库 [`github.com/schollz/progressbar/v3`](https://github.com/schollz/progressbar) 将 Plot 的培育进度渲染为标准错误输出上的实时进度条，便于在 CLI 场景下观测大批量种子的处理进度。

## 核心对象

### `ProgressBar` 类型

```go
type ProgressBar struct {
    description string
    bar         *progressbar.ProgressBar
    mu          sync.Mutex
}
```

| 字段 | 作用 |
|------|------|
| `description` | 进度条前缀描述文本，由 `NewProgressBar` 设定后只读 |
| `bar` | 延迟创建的 `progressbar.ProgressBar` 实例；首次 `OnStart` / `OnProgress` / `OnFinish` 拿到非零 `total` 后才真正构造 |
| `mu` | 保护 `bar` 字段与 `bar.Set` / `bar.Finish` 调用的互斥锁，确保并发安全 |

`ProgressBar` 自身并未导出，因此只能通过 `NewProgressBar` 构造，通过 `Observer` 接口使用。

### `NewProgressBar` 构造

```go
func NewProgressBar(description string) *ProgressBar
```

- `description`：进度条前缀文本，会显示在 `OptionSetDescription` 设定的前缀位置。
- 返回值：仅设置 `description` 的零状态 `*ProgressBar`，此时底层 `bar` 仍为 `nil`，直到首次回调携带非零 `total` 时才真正初始化。

## 进度条实现原理

`ProgressBar` 持有 `schollz/progressbar/v3` 的 `*progressbar.ProgressBar` 实例。`OnStart` / `OnProgress` / `OnFinish` 三个方法都先加锁 `p.mu`，再调用 `ensureBar` 触发按需懒加载，最后在持锁状态下操作 `bar`：

```go
func (p *ProgressBar) ensureBar(total int) {
    if total == 0 || p.bar != nil {
        return
    }
    p.bar = progressbar.NewOptions64(...)
}
```

- 懒加载：`bar` 仅在 `total > 0` 且尚未创建时构造，因此 `OnStart(0)` 不会立即绘制。
- 互斥锁：所有 `OnStart` / `OnProgress` / `OnFinish` 入口都执行 `p.mu.Lock(); defer p.mu.Unlock();`，防止并发场景下 `bar` 被重复创建或被并发地 `Set` / `Finish`。
- 完成回调：注册了 `OptionOnCompletion`，在进度条满格时向 `os.Stderr` 额外写一个换行，避免与后续日志粘连。

## 描述符 / 选项

`ensureBar` 通过 `progressbar.NewOptions64` 设置以下选项：

| 选项 | 作用 |
|------|------|
| `OptionSetDescription(p.description)` | 设置进度条前缀描述，使用 `NewProgressBar` 传入的字符串 |
| `OptionSetWriter(os.Stderr)` | 输出目标为 `os.Stderr`，避免与 stdout 业务输出冲突 |
| `OptionSetWidth(10)` | 进度条字符宽度 10 |
| `OptionShowTotalBytes(true)` | 同时按字节风格显示总量（与 `OptionSetWidth(10)` 组合成总长度） |
| `OptionThrottle(time.Millisecond)` | 渲染节流为 1ms，避免高并发 `Set` 时刷屏 |
| `OptionShowCount()` | 显示当前计数 |
| `OptionShowIts()` | 显示迭代次数（每秒刷新率） |
| `OptionOnCompletion(...)` | 完成时输出一个换行符 |
| `OptionSpinnerType(14)` | 使用第 14 号 spinner 动画 |
| `OptionFullWidth()` | 让进度条铺满终端宽度（与固定 width 配合时由库决定） |
| `OptionSetRenderBlankState(true)` | 允许在 `total == 0` 状态下渲染空白占位 |

颜色方案由 `schollz/progressbar/v3` 的默认主题提供，未通过 `OptionSetTheme` 自定义；如需更换颜色，需要在 `ensureBar` 中追加 `progressbar.OptionSetTheme(...)` 调用。

## 三个回调的具体行为

```go
func (p *ProgressBar) OnStart(total int) {
    p.mu.Lock()
    defer p.mu.Unlock()
    p.ensureBar(total)
}

func (p *ProgressBar) OnProgress(completed, total int) {
    p.mu.Lock()
    defer p.mu.Unlock()
    p.ensureBar(total)
    _ = p.bar.Set(completed)
}

func (p *ProgressBar) OnFinish(completed, total int) {
    p.mu.Lock()
    defer p.mu.Unlock()
    p.ensureBar(total)
    _ = p.bar.Set(total)
    _ = p.bar.Finish()
}
```

- `OnStart`：仅确保 `bar` 已被创建；`completed` 信息此时还没意义。
- `OnProgress`：把 `completed` 写入 `bar`，由 `progressbar/v3` 内部重绘。
- `OnFinish`：强制把 `bar` 推满到 `total`（即使最新 `completed < total`），再调用 `Finish()` 标记收尾，触发 `OptionOnCompletion` 写出换行。
- 所有 `bar.Set` / `bar.Finish` 返回的错误都被显式 `_ =` 忽略，遵循 `progressbar/v3` 在标准流关闭时返回 io 错误的常见模式，避免噪音日志。

## 使用示例

与 `pkg/plot` 配合的标准写法：

```go
package main

import (
    "github.com/Mr-xiaotian/CelestialGrow/pkg/observer"
    "github.com/Mr-xiaotian/CelestialGrow/pkg/plot"
)

type Seed struct{ ID int }
type Fruit struct{ ID int }

func cultivate(s Seed) (Fruit, error) {
    return Fruit{ID: s.ID}, nil
}

func main() {
    p := plot.NewPlot[Seed, Fruit]("harvester", cultivate)
    p.AddObserver(observer.NewProgressBar("harvesting"))

    seeds := make([]Seed, 1000)
    for i := range seeds {
        seeds[i] = Seed{ID: i}
    }
    p.Run(seeds) // 内部会调用 StartAsync + Seed + Seal + WaitAsync
}
```

运行后 `os.Stderr` 会出现类似：

```
harvesting |█████████-| 1,000/1,000 [100%] 2.5s
```

## 注意事项

- **输出介质固定为 `os.Stderr`**：通过 `OptionSetWriter` 显式设定，调用方若把 stderr 重定向到 `/dev/null`，进度条将不可见但不会影响 Plot 正常运行。
- **宽度由 `OptionFullWidth` 决定**：实际显示宽度受 `OptionSetWidth(10)` 与终端列数共同影响；如需在窄终端显示，建议替换为固定 `OptionSetWidth` 而非 `OptionFullWidth`。
- **重入安全**：`bar` 一旦创建就只被替换为非空指针；并发触发 `OnStart` / `OnProgress` / `OnFinish` 由 `mu` 串行化，不会出现 race。
- **错误忽略**：`bar.Set` / `bar.Finish` 的 `error` 被显式丢弃，符合终端 UI 场景下「绘制失败不应影响主流程」的惯例。
- **不可重用**：`ProgressBar` 与单个 Plot 生命周期绑定，没有 `Reset` / `ResetTotal` 方法；如需在另一批任务中复用，请重新 `NewProgressBar`。
