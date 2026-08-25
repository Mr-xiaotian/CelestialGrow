# CelestialGrow

`CelestialGrow` 是从 `CelestialForge` 中拆分出的独立 Go 项目，提供两层能力：

- `pkg/funnel`：通用异步记录生产/消费基础设施
- `pkg/grow`：基于 `Plot` / `Farm` 的并发任务编排与生命周期追踪

## 开发

```bash
go mod tidy
go test ./pkg/...
```

## 目录

```text
pkg/
  funnel/  # 异步消费基础设施
  grow/    # 任务编排、事件、生命周期与持久化
```
