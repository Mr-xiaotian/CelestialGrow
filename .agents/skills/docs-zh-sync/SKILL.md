---
name: "docs-zh-sync"
description: "CelestialGrow 项目专属配置。Defines project-specific path mapping and subtask division for syncing code to docs/zh-CN, building on the generic docs-zh-sync framework. Invoke when code changes require Chinese docs sync."
---

# Docs Zh Sync（CelestialGrow 项目特化）

> 本项目 `docs-zh-sync` 在 `~/.agents/skills/docs-zh-sync/SKILL.md`（通用框架）的基础上，定义本项目专属的代码↔文档映射规则与子任务划分方案。

## 1. 项目信息

- 项目根：`d:/Project/CelestialGrow`
- 模块：`github.com/Mr-xiaotian/CelestialGrow`
- 语言：Go 1.25.5（toolchain go1.26.2）
- 文档目标根：`docs/zh-CN`

## 2. Go 适配的路径映射

| 代码路径 | 文档路径 |
|---------|---------|
| `pkg/...` | `docs/zh-CN/pkg/...` |
| `demo/...` | `docs/zh-CN/demo/...` |
| `README.md`（项目根） | `docs/zh-CN/README.md`（需作为顶层文档处理） |
| `AGENTS.md`（项目根） | （**不纳入**文档同步范围，仅为开发规范） |

### 后缀映射

| 代码后缀 | 文档后缀 | 备注 |
|:-------:|:-------:|------|
| `.go` | `.md` | 所有非测试 Go 文件 |
| `_test.go` | `_test.md` | 测试文件单独建一份说明文档 |

> 说明：Go 测试文件常承载了大量设计意图（覆盖行为、并发模型、边界条件），值得单独建一份精简的「测试重点」文档；但**不强制**对每个测试文件建文档，仅对存在非平凡验证逻辑的包建立一份总览。

### 排除项

- `docs/en/`、`docs/ja/`（如有）
- `*.sqlite3`、`*.log`、`logs/`、`lifecycles/` 等运行时产物
- `go.sum`、`*.exe` 等构建/运行产物

## 3. 子任务划分

按 Go 包（`pkg/*`、`demo/*`）划分，每个子任务对应一个目录：

| 编号 | 名称 | 代码目录 | 文档目录 | 范围 |
|:----:|------|---------|---------|------|
| A1 | `pkg/api` | `pkg/api` | `docs/zh-CN/pkg/api` | 对外统一入口 |
| A2 | `pkg/farm` | `pkg/farm` | `docs/zh-CN/pkg/farm` | 图结构与调度 |
| A3 | `pkg/plot` | `pkg/plot` | `docs/zh-CN/pkg/plot` | 泛型并发节点 |
| A4 | `pkg/observer` | `pkg/observer` | `docs/zh-CN/pkg/observer` | 进度观察器 |
| A5 | `pkg/persist` | `pkg/persist` | `docs/zh-CN/pkg/persist` | 日志与生命周期持久化 |
| A6 | `pkg/funnel` | `pkg/funnel` | `docs/zh-CN/pkg/funnel` | 异步消费基础设施 |
| A7 | `pkg/runtime` | `pkg/runtime` | `docs/zh-CN/pkg/runtime` | 运行时基础类型 |
| A8 | `demo` | `demo` | `docs/zh-CN/demo` | 示例程序 |
| A9 | 顶层 README | （无源码） | `docs/zh-CN/README.md` | 由 A1 同步从根 `README.md` 复制并对齐 |

并行度建议：1 个批次并行 A1–A7（共 7 个），第二批 A8–A9。

> **退化策略**：若环境不支持并行 subagent，则按 A1→A2→…→A9 顺序串行执行。

## 4. 文档写作规范（Go 适配）

通用写作规范遵循 `~/.agents/skills/docs-zh-sync/_subagent-writing.md`。Go 项目的特别之处：

- **公开符号**：`type`、`func`、`const`、`var` 中**首字母大写**的即为公开。文档聚焦这些符号。
- **泛型**：`Plot[S any, F any]` 等泛型参数需明确说明 S/F 的语义。
- **接口契约**：Go 的隐式接口需在文档中显式列出接口方法、典型实现。
- **并发模型**：重点说明 `chan`、`sync.WaitGroup`、`context.Context` 等的传递与关闭点。
- **错误处理**：列出函数可能返回的 error 场景，而非仅说「返回 error」。
- **示例代码块**：使用 ` ```go ` 语言标注，并保持可直接 `go run` / `go test`。
- **不引入** Markdown frontmatter / 复杂模板，遵循通用骨架（作用 / 核心对象 / 关键流程 / 重要细节 / 使用示例 / 注意事项）。

## 5. 当前日期

`2026/09/01`

所有新建/修改文档的 `最后更新日期` 字段使用此日期，未修改的文档保持原日期。
