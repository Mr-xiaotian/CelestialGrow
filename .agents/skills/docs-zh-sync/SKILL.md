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
| `README.md`（项目根） | `docs/zh-CN/README.md` |
| `AGENTS.md`（项目根） | （**不纳入**文档同步范围，仅为开发规范） |

### 后缀映射

| 代码后缀 | 文档后缀 | 备注 |
|:-------:|:-------:|------|
| `.go` | `.md` | 所有非测试 Go 文件 |
| `_test.go` | `_test.md` | 测试文件单独建一份说明文档 |

> 说明：Go 测试文件常承载了大量设计意图（覆盖行为、并发模型、边界条件），值得单独建一份精简的「测试重点」文档；但**不强制**对每个测试文件建文档，判定准则见 `_subagent-base.md` 的「测试文档判定」。

### 同步源与同步目标

- `docs/zh-CN/**`（含顶层 `docs/zh-CN/README.md`）是**同步目标**。
- 根 `README.md` 是 `docs/zh-CN/README.md` 的**同步源**，它自身也可能过期。
- `pkg/**`、`demo/**` 源码是唯一事实来源。若根 README 与源码不一致，**只修改 `docs/zh-CN/README.md`**，并在最终汇总中列出根 README 的漂移点，不直接改根 README。

### 排除项

- `docs/en/`、`docs/ja/`（如有）
- `*.sqlite3`、`*.log`、`logs/`、`lifecycles/` 等运行时产物
- `go.sum`、`*.exe` 等构建/运行产物

## 3. 阶段 2：扫描脚本调用（Go 适配）

本项目使用 `.agents/skills/docs-zh-sync/scan_manifest.go`（通用框架 `scan_manifest.py` 的 Go 版，CLI 一致，无 Python 依赖）。子任务涉及的具体文件清单**不在本配置里枚举**——以扫描结果为准，代码重构后无需维护本文件。

在项目根执行：

```bash
go run .agents/skills/docs-zh-sync/scan_manifest.go \
    --project-root . \
    --pairs "pkg/api:docs/zh-CN/pkg/api" \
            "pkg/farm:docs/zh-CN/pkg/farm" \
            "pkg/plot:docs/zh-CN/pkg/plot" \
            "pkg/observer:docs/zh-CN/pkg/observer" \
            "pkg/persist:docs/zh-CN/pkg/persist" \
            "pkg/funnel:docs/zh-CN/pkg/funnel" \
            "pkg/runtime:docs/zh-CN/pkg/runtime" \
            "demo:docs/zh-CN/demo" \
    --top-level docs/zh-CN \
    --output tmp_manifest.md
```

- `--pairs` 用 `:` 分隔 code_dir 与 doc_dir，因此**必须用相对路径**（Windows 绝对路径的 `D:` 会与分隔符冲突）。
- 输出四表：`exists`（审计内容一致性）/ `missing`（需新建）/ `orphans`（需移动或删除）/ `overviews`（无 1:1 源码的总览文档：保留、勿删、不套用 H1 规范）。镜像目录内的 `README.md` 也会被保护，不会误判为孤立文档。
- `--format json` 输出 JSON；`--output <file>` 写入文件（按 UTF-8 写出，避免 PowerShell 重定向产生 UTF-16/BOM），留空则打印到 stdout。
- 出现目录 `WARNING` 时说明配置中的目录已改名，先核对实际代码目录再继续。
- `orphans` 表的「处理建议」列会给出「疑似重命名：X -> Y（相似度 ..）」提示；命中重命名时直接指引子代理把旧文档改写为新文档，不要新建。
- 用完的 `tmp_manifest.md` 等临时产物必须删除，不得留在仓库里。

## 4. 子任务划分

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
| A9 | 顶层 README | （无源码） | `docs/zh-CN/README.md` | 由根 `README.md` 同步并对齐代码现状 |

并行度建议：1 个批次并行 A1–A7（共 7 个），第二批 A8–A9。

> **退化策略**：若环境不支持并行 subagent，则按 A1→A2→…→A9 顺序串行执行。

## 5. 文档写作规范（Go 适配）

通用写作规范遵循 `~/.agents/skills/docs-zh-sync/_subagent-writing.md`。Go 项目的特别之处：

- **公开符号**：`type`、`func`、`const`、`var` 中**首字母大写**的即为公开。文档聚焦这些符号。
- **泛型**：`Plot[S any, F any]` 等泛型参数需明确说明 S/F 的语义。
- **接口契约**：Go 的隐式接口需在文档中显式列出接口方法、典型实现。
- **并发模型**：重点说明 `chan`、`sync.WaitGroup`、`context.Context` 等的传递与关闭点。
- **错误处理**：列出函数可能返回的 error 场景，而非仅说「返回 error」。
- **示例代码块**：使用 ` ```go ` 语言标注，并保持可直接 `go run` / `go test`。
- **不引入** Markdown frontmatter / 复杂模板，遵循通用骨架（作用 / 核心对象 / 关键流程 / 重要细节 / 使用示例 / 注意事项）。

## 6. 主 Agent 收尾自检（Go 版命令）

所有子代理完成后，在项目根依次执行：

1. **重跑扫描**：执行 §3 的命令，确认 `missing`、`orphans` 均为空，`overviews` 仅剩预期条目（如 `docs/zh-CN/README.md`）。
2. **H1 全量校验**：确认每个镜像文档的首个一级标题等于「源码相对项目根路径」（`docs/zh-CN/README.md` 例外，固定为 `# CelestialGrow`）：

   ```bash
   grep -rn --include="*.md" -m 1 "^# " docs/zh-CN
   ```

3. **旧名残留扫描**：把各子代理汇总的旧名集合拼成正则，在**整个** `docs/zh-CN/`（含顶层文档）上搜索，修复命中项：

   ```bash
   grep -rnE "Old1|Old2|Old3" docs/zh-CN
   ```

   历史记录类文件（`change_log.md` 等）中的旧名属正常历史，跳过。
4. **构建校验与清场**：`go build ./...`；确认子代理为验证示例做过的编译未留下临时目录、`*.exe`、`logs/`、`lifecycles/`。

## 7. 当前日期

以会话系统信息中的 `Today's Date` 为准，格式 `YYYY/MM/DD`；无法获取时应主动向用户确认。

同一批次的所有文档使用同一日期；**仅更新本次实际修改过的文档的日期**，未修改的文档保持原日期。
