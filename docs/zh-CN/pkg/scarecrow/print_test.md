# pkg/scarecrow/print_test.go

> 📅 最后更新日期: 2026/11/13

## 作用

`pkg/scarecrow/print_test.go` 锁定 `PrintWatcher` 的**输出格式契约**与**忽略范围**：进度行的前缀、字段顺序、累计值，以及 Farm / Seal / Replant 事件不产生任何输出。测试通过未导出的 `newPrintWatcher(out, name)` 把输出接到 `bytes.Buffer`，避免污染标准输出。

## 测试分布

| 测试 | 覆盖内容 |
|------|----------|
| `TestPrintWatcherReportsProgress` | 依次送入 8 条事件，逐行断言 8 条输出文本 |
| `TestPrintWatcherIgnoresFarmAndSealEvents` | 送入 5 条与进度无关的事件，断言缓冲区长度为 0 |

## 关键断言与设计意图

### 输出格式：`TestPrintWatcherReportsProgress`

事件序列与期望输出（前缀 `demo`）：

| 事件 | 期望行 |
|------|--------|
| `PlotStartEvent{}` | `[demo] start total=0` |
| `SeedInputEvent{}` ×3 | `[demo] total=1(+1)`、`total=2(+1)`、`total=3(+1)` |
| `SeedRipenEvent{}` | `[demo] succeeded=1(+1), total=3` |
| `SeedWitherEvent{}` | `[demo] failed=1(+1), total=3` |
| `SeedPruneEvent{}` | `[demo] skipped=1(+1), total=3` |
| `PlotEndEvent{}` | `[demo] finish total=3, skipped=1, succeeded=1, failed=1` |

验证要点：

- `PlotStartEvent` 发生在任何输入之前，因此首行 `total=0`——同时证明 `total` 只由 `SeedInputEvent` 驱动。
- 结果行的 `total` 保持 3，说明结果事件不改变 `total`。
- 收尾行的字段顺序固定为 `total, skipped, succeeded, failed`，与源码 `printf` 的参数顺序一致；顺序调整会立刻被测试捕获。
- 断言方式是 `strings.Contains` 逐行匹配，不要求整体文本完全相等，因此不约束行间是否存在额外输出（当前实现每条事件恰好一行）。

### 忽略范围：`TestPrintWatcherIgnoresFarmAndSealEvents`

依次送入 `FarmStartEvent`、`FarmEndEvent`、`SeedReplantEvent`、`SealInputEvent`、`SealMergeEvent`，断言 `out.Len() == 0`。这锁定了「只关注节点与任务进度」的行为：Farm 生命周期、重试与终止信号都不打印。

## 运行方式

```bash
go test ./pkg/scarecrow -run 'TestPrintWatcher' -v
```

## 注意事项

- 测试只验证「输出包含某行」，不验证多次运行之间计数是否重置；`PrintWatcher` 本身没有重置接口（见 [`print.md`](./print.md)）。
- 测试不覆盖并发调用：`PrintWatcher.mu` 的串行化没有并发断言，若改成无锁实现，本文件不会失败。
- 未覆盖 `out` 写入失败的路径（`printf` 显式丢弃 `Fprintf` 的错误），也未覆盖 `NewPrintWatcher` 写 `os.Stdout` 的默认构造。
- 修改任何一行输出文案都需要同步本文件的期望字符串；这是有意的耦合，用于防止格式漂移。
