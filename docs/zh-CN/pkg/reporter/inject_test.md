# pkg/reporter/inject_test.go

> 📅 最后更新日期: 2026/11/26

## 作用

`pkg/reporter/inject_test.go` 覆盖 `InjectionTicker` 的三条主路径：一次成功的拉取与分发、`Start` 之前禁止使用、构造函数对非法参数的拒绝。它不依赖真实服务端，全部通过 `net/http/httptest` 与本地探针完成。

## 测试分布

| 测试 | 覆盖内容 |
|------|----------|
| `TestInjectionTickerPullsTasksAndTerminations` | `session_id` 查询参数、2xx 响应解码、`tasks`/`terminations` 分发、`TickPeriod` 返回值 |
| `TestInjectionTickerRejectsUseBeforeStart` | 未调用 `Start` 时 `OnTick` 必须报错，且不发起请求 |
| `TestNewInjectionTickerRejectsInvalidArguments` | 空 URL、空 SessionID、nil target、缺 scheme、非 http(s) scheme |

测试辅助类型：

```go
type injectionTargetProbe struct {
    tasks        map[string][]any
    terminations []string
}
```

`injectionTargetProbe` 实现 `InjectionTarget`：`InjectTasks` 直接保存 map（不复制），`InjectTerminations` 用 `append([]string(nil), nodes...)` 复制切片。它本身不返回错误，因此测试无法覆盖"目标注入失败"分支。

## 关键断言与设计意图

### 成功拉取：`TestInjectionTickerPullsTasksAndTerminations`

服务端用 `httptest.NewServer` 接收请求，处理函数先断言：

```go
if got := r.URL.Query().Get("session_id"); got != "demo@1" {
    t.Fatalf("session_id = %q, want demo@1", got)
}
```

这验证了 `url.QueryEscape` 编码（请求行里是 `demo%401`）与服务端解码后的等价性。随后返回：

```json
{"tasks": {"root": [1.0, "x"]}, "terminations": ["leaf"]}
```

客户端侧断言：

| 断言 | 意图 |
|------|------|
| `NewInjectionTicker(...)` 无错误 | `baseURL` 为 `httptest` 的 `http://127.0.0.1:<port>`，可通过 scheme/host 校验 |
| `TickPeriod() == 5` | 显式传入的周期不被默认值覆盖 |
| `Start()` / `Stop()` 返回 `nil` | 生命周期方法不产生错误 |
| `reflect.DeepEqual(target.tasks, map[string][]any{"root": {1.0, "x"}})` | JSON 数字解码为 `float64`；map 结构与值完全一致 |
| `reflect.DeepEqual(target.terminations, []string{"leaf"})` | 终止名单按原顺序、原内容交付 |

`Stop` 放在 `defer` 中，并再次断言返回 `nil`，从而验证「`Stop` 可在 `OnTick` 之后安全调用」。测试没有断言 `InjectTasks` 与 `InjectTerminations` 的**调用顺序**（探针只记录结果）；顺序保证由实现中的两次串行调用提供。

### 未启动即使用：`TestInjectionTickerRejectsUseBeforeStart`

```go
ticker, err := NewInjectionTicker("http://example.com", "demo@1", &injectionTargetProbe{}, 1, time.Second)
// ...
if err := ticker.OnTick(metronome.TickEvent{Seq: 1}); err == nil {
    t.Fatal("OnTick() before Start must fail")
}
```

- 使用不可达的 `example.com` 是刻意的：一旦实现错误地在 `OnTick` 内懒创建客户端，测试会尝试真实网络请求而不是静默通过。
- 只断言「有错误」，不校验错误文本；当前实现返回 `reporter: injection client is not initialized`。
- 未调用 `Stop`，因为 `client` 从未创建。

### 构造参数校验：`TestNewInjectionTickerRejectsInvalidArguments`

| 输入 | 预期拒绝原因 |
|------|-------------|
| `("", "demo@1", probe, 1, 1s)` | 空 base URL |
| `("http://example.com", "", probe, 1, 1s)` | 空 session ID |
| `("http://example.com", "demo@1", nil, 1, 1s)` | nil target |
| `("127.0.0.1:5005", "demo@1", probe, 1, 1s)` | 缺 scheme（`url.Parse` 后 host 为空） |
| `("ftp://example.com", "demo@1", probe, 1, 1s)` | 非 `http`/`https` scheme |

测试只判断 `err == nil`，不比对具体错误文本。五个用例全部使用 `period = 1`、`timeout = 1s`，因此不会与周期/超时校验相互干扰。

## 运行方式

```bash
# 仅本文件的测试
go test ./pkg/reporter -run 'TestInjectionTicker|TestNewInjectionTicker' -v

# 整个包
go test ./pkg/reporter -v
```

测试会启动本地 `httptest` 服务器（回环地址），不需要外部上报服务；不涉及共享状态，可用 `-race` 运行：

```bash
go test ./pkg/reporter -run 'TestInjectionTicker' -race
```

## 注意事项

- **未覆盖的校验分支**：`period < 0`（`injection period must be positive`）与 `timeout < 0`（`pull timeout must be non-negative`）没有测试；默认值替换（`period == 0 → 5`、`timeout == 0 → 3s`）也无法通过公开 API 观测，只能间接验证。
- **未覆盖的错误路径**：非 2xx 状态、非法 JSON、`InjectTasks` 失败（此时不应调用 `InjectTerminations`）、`InjectTerminations` 失败。
- **未覆盖空集合归一化**：服务端返回 `{"tasks": null, "terminations": null}` 时目标应收到空 map / 空切片，当前测试只覆盖非空载荷。
- **未覆盖 URL 规范化**：`baseURL` 末尾带 `/` 的情况没有用例；实现会去掉全部尾部斜杠，避免拼出 `//api/pull_injection`。
- **测试不校验请求方法**：服务端处理函数未检查 `r.Method`，因此实现若误用 POST 也不会被发现（当前实现是 `GET`）。
