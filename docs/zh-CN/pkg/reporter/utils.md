# pkg/reporter/utils.go

> 📅 最后更新日期: 2026/11/26

`pkg/reporter/utils.go` 汇集 reporter 包内部的投影、深拷贝、URL 校验、HTTP 客户端与序列化辅助函数。它们全部**不导出**，但决定了推送到服务端后的 JSON 形状（空集合是 `[]`/`{}` 而不是 `null`）、时间戳精度与失败降级行为。

## 作用

| 函数 | 一句话职责 | 主要调用方 |
|------|-----------|-----------|
| `toStatusSnapshot` | 指标 → `NodeStatus` 投影 | `SnapshotTicker.OnTick`、`PushWatcher.OnEvent(FarmEnd)` |
| `copyEdges` | 深拷贝邻接表，空出边编码为 `[]` | `PushWatcher.OnEvent(FarmStart)` |
| `copySnapshot` | 深拷贝状态快照及其计数字典 | `PushWatcher.pushSnapshotAt` |
| `copyIntMap` | 深拷贝 `map[string]int`，空映射编码为 `{}` | `copySnapshot`、测试探针 |
| `copyNodeMeta` | 复制节点元信息表 | `PushWatcher.OnEvent(FarmStart)` |
| `newHTTPClient` | 创建持有独立 Transport 的客户端 | `pushHandler.BeforeStart`、`InjectionTicker.Start` |
| `normalizeBaseURL` | 校验并规范化上报基础地址 | `NewPushWatcher`、`NewInjectionTicker` |
| `unixSeconds` | `time.Time` → Unix 浮点秒 | 快照/图元/错误记录的时间戳 |
| `toJSONValue` | 保证任意任务载荷可被 JSON 编码 | `PushWatcher.OnEvent(SeedWither)` |

## 核心函数

### `toStatusSnapshot`

```go
func toStatusSnapshot(metrics map[string]scarecrow.PlotMetrics) map[string]NodeStatus
```

- 输出 map 容量预分配为 `len(metrics)`，**保留全部节点键**，包括零值指标。
- `Status` 做 `int(PlotStatus)` 转换（`0/1/2`），不输出字符串。
- `StartTime` / `EndTime`：`time.Time` 零值输出 `0.0`，否则 `unixSeconds(...)`。这保证「未启动/未结束」在 Web 端有明确哨兵值，而不是 1970 年前的负数。
- 计数直接映射：`InputTotal / Processed / Pending / Succeeded / Failed / Skipped`。
- `UpstreamCounts` / `DownstreamCounts` **按引用传递**，不复制。
- 刻意不输出派生耗时（如 `elapsed`）：它每拍都变，会破坏服务端的内容判重。

> 引用语义的安全性：`scarecrow.MetricsWatcher.GetPlots` 每次返回新字典，因此可以直接引用；`PushWatcher.pushSnapshotAt` 还会再走一次 `copySnapshot`，所以入队后的数据与调用方完全隔离。自定义 `MetricsView` 若复用字典，必须自行保证不可变，或在 sink 侧复制。

### `copyEdges`

```go
func copyEdges(edges map[string][]string) map[string][]string
```

```go
result[from] = append([]string{}, targets...)
```

- `append([]string{}, nil...)` 产生**非 nil 的空切片**，因此 JSON 编码为 `[]`；若直接赋值 `targets`，Go 会把 nil 切片编码成 `null`，Web 端需额外兼容。
- 同时复制每个切片的底层数组，调用方在入队后修改原邻接表不影响已入队载荷。
- `edges == nil` 时返回**非 nil** 的空 map（`make(map[string][]string, len(edges))`，容量为 0），编码为 `{}`。

### `copySnapshot`

```go
func copySnapshot(snapshot map[string]NodeStatus) map[string]NodeStatus
```

- 复制每个 `NodeStatus` 值（结构体本身是值类型），并对其中的 `UpstreamCounts`、`DownstreamCounts` 调 `copyIntMap`。
- 目的：`Inlet.Send` 返回后调用方仍可能持有并修改原 map/字典；入队载荷必须与调用方解耦。
- 空 map 输入返回非 nil 空 map，JSON 为 `{}`。

### `copyIntMap`

```go
func copyIntMap(source map[string]int) map[string]int
```

- 用 `make` 创建非 nil 结果再逐项复制，因此 `nil` 输入得到非 nil 空 map，编码为 `{}` 而非 `null`。
- 这是快照中两个计数字典永远不会出现 `null` 的根本保证。

### `copyNodeMeta`

```go
func copyNodeMeta(meta map[string]scarecrow.NodeMeta) map[string]scarecrow.NodeMeta
```

- 只复制 map 本身（浅拷贝）；`scarecrow.NodeMeta` 三个字段（`ClassName`、`ExecutionMode`、`MaxWorkers`）都是值类型，不含切片/映射/指针，因此浅拷贝即完全隔离。
- nil 输入返回非 nil 空 map，编码为 `{}`。

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

- 克隆 `http.DefaultTransport` 让每个 reporter 组件拥有**独立连接池**：`Stop` 里的 `CloseIdleConnections()` 只关闭自己的空闲连接，不会影响进程内其他 HTTP 客户端。
- 克隆发生在创建时，之后对 `http.DefaultTransport` 的字段修改（如 `MaxIdleConns`）不会反向影响已创建的客户端。
- 若进程把 `http.DefaultTransport` 替换成了非 `*http.Transport` 实现，则直接共享该实例，`CloseIdleConnections` 的隔离性不再成立（`*http.Client.CloseIdleConnections` 会调用 Transport 的可选接口）。
- `timeout` 直接作为整次请求（连接 + 重定向 + 读 body）的上限；`0` 表示不超时，但生产调用方不会传 0：`pushHandler` 用 `DefaultPushTimeout`，`InjectionTicker` 用构造参数（0 已被替换为 `DefaultPullTimeout`）。

### `normalizeBaseURL`

```go
func normalizeBaseURL(raw string) (string, error)
```

处理步骤与错误：

| 步骤 | 行为 | 失败错误 |
|:----:|------|---------|
| 1 | `strings.TrimSpace(raw)`，再去掉**所有**尾部 `/` | 结果为空 → `reporter: base url is empty` |
| 2 | `url.Parse`，要求 `Scheme != ""` 且 `Host != ""` | `reporter: invalid base url %q`（引号内是**原始** `raw`） |
| 3 | scheme 必须是 `http` 或 `https` | `reporter: base url must use http or https, got %q` |
| 4 | 返回规范化后的字符串 | — |

要点：

- `TrimRight(..., "/")` 会去掉连续多个斜杠：`http://host///` → `http://host`。
- 返回值是**修剪后的原始字符串**，不做 `url.String()` 重排，因此大小写、路径、查询参数都原样保留。
- 只校验「有 scheme + 有 host + 协议为 http(s)」，**不校验路径**。端点由 `baseURL + "/api/..."` 字符串拼接而来，所以带路径的基础地址（`http://host/api`）会产生 `/api/api/...` 的双前缀；带查询串的地址会把查询串插在路径之前，结果不可预期。基础地址应只写到 host[:port]。
- 不检查可达性、不检查端口合法性、不限制明文 HTTP；TLS 校验完全交给 `http.Transport`。
- 用户信息（`http://user:pass@host`）会被保留——`Host` 字段不含 userinfo，但返回值仍是原串，因此凭证会随请求发送。

### `unixSeconds`

```go
func unixSeconds(at time.Time) float64
```

```go
float64(at.Unix()) + float64(at.Nanosecond())/1e9
```

- 输出**带亚秒精度**的 Unix 秒（如 `time.Unix(10, 500_000_000)` → `10.5`），与 celestialflow-web 的 `number` 时间戳契约一致。
- `Unix()` 对负时间（1970 前）向下取整、`Nanosecond()` 始终非负，因此公式对负时间也成立；但 reporter 的调用点都会先排除零值时间。
- 直接对零值 `time.Time` 调用会得到约 `-6.2e10`，不是 `0`——`toStatusSnapshot` 用 `IsZero()` 守卫，`PushWatcher` 的时间戳都来自真实事件/`time.Now()`。

### `toJSONValue`

```go
func toJSONValue(value any) any
```

- 先用 `json.Marshal(value)` 做**可序列化探测**：成功则原样返回（保留原始类型，数字/结构不会被转成字符串）。
- 失败则返回 `fmt.Sprintf("%+v", value)` 的可读字符串。
- 设计意图：`SeedWitherEvent.Seed` 可能包含 `chan`、`func`、`complex`、`NaN`/`Inf`、循环引用等不可编码值；若直接放入 `taskFailRecord`，`json.Marshal` 会让**整条错误上报**失败。降级后至少保留错误的可诊断性。
- 降级文本不稳定（通道/函数会打印地址），只适合展示，不适合作为重放输入。

## 关键流程

```mermaid
flowchart TD
    PM[map string PlotMetrics] --> P[toStatusSnapshot]
    P -->|NodeStatus| CS[copySnapshot]
    CS -->|copyIntMap ×2| Q[入队载荷]
    FE[FarmStartEvent.Edges] --> CE[copyEdges]
    CE -->|空切片非 nil| Q2[graphMetaRecord]
    NM[FarmStartEvent.NodeMeta] --> CN[copyNodeMeta]
    CN --> Q2
    RAW[raw base url] --> N[normalizeBaseURL]
    N -->|scheme/host 校验| C[newHTTPClient]
    C -->|克隆 Transport| HTTP[POST / GET]
    T[time.Time] --> U[unixSeconds]
    U --> Q
    U --> Q2
    ANY[任意 Seed] --> J[toJSONValue]
    J --> Q3[taskFailRecord]
```

## 使用示例

下面这段程序通过公开 API 间接验证三件事：尾部 `/` 被规范化、`nil` 计数字典编码为 `{}`、`nil` 邻接表编码为 `[]`。

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

输出中 `edges` 应包含 `"leaf":[]`，`snapshot.root` 应包含 `"upstream_counts":{}` 与 `"downstream_counts":{}`——即 utils 层的空集合保证。

## 注意事项

- **空集合约定是硬契约**：`copyEdges` / `copyIntMap` / `copySnapshot` 都刻意用 `make` 返回非 nil 空集合。若改成直接返回 `nil`，JSON 会出现 `null`，Web 端的 `Record<string, number>` 类型会解析失败。新增字段时请沿用同一模式。
- **深拷贝只覆盖已知引用字段**：`copySnapshot` 只处理两个计数字典；若未来 `NodeStatus` 增加切片/映射字段，必须同步扩展，否则入队后仍会被调用方修改。
- **时间戳单位是秒而非毫秒**：所有 `*_time` / `timestamp` / `ts` 都是浮点秒（可带小数），与 Web 端 `number` + `Date` 秒级约定一致；不要改写成 `UnixMilli`。
- **URL 校验发生在构造期**：`normalizeBaseURL` 只做静态校验，DNS、端口、TLS、服务端健康都无法提前发现；这些错误在首次 `OnTick`/`HandleRecord` 时才以网络错误形式出现。
- **`newHTTPClient` 的隔离性依赖 `http.DefaultTransport` 的类型**：若被替换为自定义 RoundTripper，多个组件会共享同一 Transport，`CloseIdleConnections` 可能影响其他组件。
- **`toJSONValue` 的探测有成本**：每个枯萎事件都会对种子做一次完整 `json.Marshal` 试探，大对象会带来额外分配；换来的是单条错误上报不会因载荷问题整体失败。
