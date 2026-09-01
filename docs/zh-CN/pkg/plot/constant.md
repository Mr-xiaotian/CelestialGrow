# pkg/plot/constant.go

> 最后更新日期: 2026/09/01

`constant.go` 是 `plot` 包内的私有常量集合，目前只定义了一个 sentinel 字符串，用于在 `seedChan` / `fruitChan` 的 `runtime.Payload.Source` 字段中区分「外部调用者」与「某个具名上游 plot」。

## 作用

- 给 `Payload.Source` 一个稳定的哨兵值，避免外部直接用字面量 `"__input__"`；
- 配合 `Plot.Seal` 与 `Plot.sprout.markSealed` 实现「**外部 `Seal()` 是强终止**」的语义。

## 公开符号

该文件**只包含一个未导出常量**：

```go
const sourceInput = "__input__"
```

| 常量 | 类型 | 值 | 用途 |
|------|------|----|------|
| `sourceInput` | `string` | `"__input__"` | 作为 `runtime.Payload.Source` 的哨兵值，表示「这条 seal/seed 来自外部调用者，而非某个上游 plot」 |

> 由于它是未导出常量，外部包**不能**直接引用；此文档仅说明其语义。如需在外部代码中判断「某 Payload 是否由外部注入」，请通过 `Plot.Seal` / `Plot.Seed` 行为来推理，而不是去拿这个值。

## 使用位置

`sourceInput` 在 `pkg/plot` 内有 2 处使用：

1. `Plot.Seal()`：
   ```go
   p.seedChan <- runtime.Payload[S]{
       Signal:  runtime.SignalSeal,
       Source:  sourceInput,
       EventID: sealID,
   }
   ```
   标识本次 seal 来自外部 `Seal()` 调用。

2. `Plot.sprout.markSealed`：
   ```go
   if source == sourceInput {
       sealedFrom[sourceInput] = sealID
       return true
   }
   ```
   当 `markSealed` 看到 `Source == sourceInput` 时，**直接返回 `true`**（输入已关闭），**不再等待其他上游 seal**——这就是外部 `Seal()` 的「强终止」语义。

而其他正常路径下的 seal 传播来自 `sprout` 自身的收尾逻辑：

```go
sealPayload := runtime.Payload[F]{Signal: runtime.SignalSeal, Source: p.name, EventID: sealID}
```

注意 `Source` 是当前 plot 的 `name`（不是 `sourceInput`），从而下游可以正确识别来源。

## 注意事项

- 不要在外部包中重新定义或硬编码 `"__input__"`；该字符串是 `pkg/plot` 的内部实现细节，未来可能调整。
- 如果在 Farm 模式看到某个下游 plot 提前收尾（不再等待其他上游），大概率是上游之一调用了自身的 `Plot.Seal()` 或 `Farm.Seal()` 触发了这条强终止路径。
