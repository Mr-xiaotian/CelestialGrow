# Subagent Base Rules（CelestialGrow 项目特化）

> 通用规则见 `~/.agents/skills/docs-zh-sync/_subagent-base.md`。
> 本文件仅覆盖本项目的路径映射、文件类型差异与补充要点。

---

## 1. 必读文件清单

子代理开始工作前，请按顺序阅读：

1. `~/.agents/skills/docs-zh-sync/_subagent-base.md`——通用规则、输出格式
2. `~/.agents/skills/docs-zh-sync/_subagent-audit.md`——通用审计清单
3. `~/.agents/skills/docs-zh-sync/_subagent-writing.md`——通用写作规范
4. 项目内的 `.agents/skills/docs-zh-sync/SKILL.md`——项目配置、子任务划分、扫描脚本调用
5. 项目内的 `.agents/skills/docs-zh-sync/_subagent-base.md`（本文件）——项目路径映射

---

## 2. 路径映射规则（Go 适配）

| 代码路径 | 文档路径 |
|---------|---------|
| `pkg/<name>/<file>.go` | `docs/zh-CN/pkg/<name>/<file>.md` |
| `pkg/<name>/<file>_test.go` | `docs/zh-CN/pkg/<name>/<file>_test.md` |
| `demo/<file>.go` | `docs/zh-CN/demo/<file>.md` |

### 镜像文档 H1 规范（本项目强制）

> 通用规则见 `~/.agents/skills/docs-zh-sync/_subagent-writing.md` 的「标题（H1）规范」。本项目强制要求：

| 文档 | 强制 H1 |
|------|---------|
| `docs/zh-CN/pkg/<name>/<file>.md` | `# pkg/<name>/<file>.go` |
| `docs/zh-CN/pkg/<name>/<file>_test.md` | `# pkg/<name>/<file>_test.go` |
| `docs/zh-CN/demo/<file>.md` | `# demo/<file>.go` |
| `docs/zh-CN/README.md` | `# CelestialGrow`（顶层 README 例外） |

禁止：包级别短名（`# pkg/api`）、符号名（`# funnel.Inlet`）、追加后缀（`# pkg/plot/option.go — 配置函数`）、中文别名。审计时若发现不合规，按 🔴 极高优先级修复。

### 总览文档

`docs/zh-CN/README.md` 以及镜像目录内可能存在的 `README.md` 均为**无 1:1 源码的总览文档**：保留、勿删，不套用上面的 H1 规范（顶层 README 固定为 `# CelestialGrow`）。扫描脚本会将其列入 `overviews` 表而不是 `orphans`。

---

## 3. Go 特定的审计清单补充

通用审计清单见 `~/.agents/skills/docs-zh-sync/_subagent-audit.md`。Go 项目额外关注：

| 频率 | 模式 | 排查方法 |
|:----:|------|---------|
| 🔴 极高 | **导出符号遗漏** | 对每个 `*.go` 文件执行 `grep -E '^(func|type|var|const)\s+[A-Z]'`，确保文档列出所有公开符号 |
| 🔴 极高 | **泛型参数未说明** | 对每个泛型声明 `type Foo[X any, Y any]`，文档需解释 X/Y 的语义 |
| 🟠 高 | **接口方法漂移** | 对每个 `interface{...}`，确认文档中列出的方法与源码完全一致 |
| 🟠 高 | **错误返回描述不准确** | 检查 `return ..., err` 处的实际错误含义，避免「返回 error」式偷懒 |
| 🟡 中 | **并发原语传递遗漏** | 关注 `chan`、`WaitGroup`、`context` 在函数签名中的出现位置与文档描述 |
| 🟡 中 | **Option/配置函数遗漏** | 对 `func WithXxx(...) Option` 模式，文档需提供配置表 |
| 🟢 低 | **测试文件名拼写** | 确认 `xxx_test.md` 对应 `xxx_test.go` |

---

## 4. 输出格式

严格遵循通用 `~/.agents/skills/docs-zh-sync/_subagent-base.md` 中定义的输出格式。每个子任务结束必须输出「区域报告」。

除通用格式外，还必须附上**本区域的旧名集合**——本次审计中发现的、已被重命名或删除的类型、函数、方法、字段、配置项、SQLite 表名/列名、状态常量、路径名，供主 agent 做跨分区残留扫描。

---

## 5. 测试文档判定

对镜像清单中每个 `*_test.go`，按以下准则决定是否新建 `*_test.md`：

- **建**：断言覆盖 ≥ 2 类行为分支，或涉及并发、边界值、持久化/序列化断言、性能回归（如深链递归）等非平凡验证逻辑。
- **不建**：仅重复验证单一简单行为（例如只断言一个 getter 的返回值）。
- 决定不建时，必须在区域报告的「未修改文档」一节列出该测试文件并说明理由。

---

## 6. 工作纪律

- **只改 `docs/zh-CN/**`**：不改动 `pkg/**`、`demo/**`、根 `README.md` 等源码或源文档。
- **不留临时产物**：示例需要编译验证时，使用项目外的临时目录（或用 `go vet`），验证后清理；不得在仓库留下 `*.exe`、`tmp_*`、`logs/`、`lifecycles/` 等产物。
- **不触碰 git 区**：不执行 commit / add / checkout / stash。
