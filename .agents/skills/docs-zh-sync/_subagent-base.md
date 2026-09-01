# Subagent Base Rules（CelestialGrow 项目特化）

> 通用规则见 `~/.agents/skills/docs-zh-sync/_subagent-base.md`。
> 本文件仅覆盖本项目的路径映射、文件类型差异与具体子任务清单。

---

## 1. 必读文件清单

子代理开始工作前，请按顺序阅读：

1. `~/.agents/skills/docs-zh-sync/_subagent-base.md`——通用规则、输出格式
2. `~/.agents/skills/docs-zh-sync/_subagent-audit.md`——通用审计清单
3. `~/.agents/skills/docs-zh-sync/_subagent-writing.md`——通用写作规范
4. 项目内的 `.agents/skills/docs-zh-sync/SKILL.md`——项目配置与子任务划分
5. 项目内的 `.agents/skills/docs-zh-sync/_subagent-base.md`（本文件）——项目路径映射

---

## 2. 路径映射规则（Go 适配）

| 代码路径 | 文档路径 |
|---------|---------|
| `pkg/<name>/<file>.go` | `docs/zh-CN/pkg/<name>/<file>.md` |
| `pkg/<name>/<file>_test.go` | `docs/zh-CN/pkg/<name>/<file>_test.md` |
| `demo/<file>.go` | `docs/zh-CN/demo/<file>.md` |

> - 没有 `__init__.py`，故不需要 `__init__.md`。
> - 测试文件单独建文档，子代理在子任务中根据「是否存在非平凡验证逻辑」自行决定是否新建测试说明文档；若仅为重复简单行为验证，可以**不**建文档，但在最终报告「未修改文档」一节注明。

### 镜像文档 H1 规范（本项目强制）

> 通用规则见 `~/.agents/skills/docs-zh-sync/_subagent-writing.md` 的「标题（H1）规范」。本项目强制要求：

| 文档 | 强制 H1 |
|------|---------|
| `docs/zh-CN/pkg/<name>/<file>.md` | `# pkg/<name>/<file>.go` |
| `docs/zh-CN/pkg/<name>/<file>_test.md` | `# pkg/<name>/<file>_test.go` |
| `docs/zh-CN/demo/<file>.md` | `# demo/<file>.go` |
| `docs/zh-CN/README.md` | `# CelestialGrow`（顶层 README 例外） |

禁止：包级别短名（`# pkg/api`）、符号名（`# funnel.Inlet`）、追加后缀（`# pkg/plot/option.go — 配置函数`）、中文别名。审计时若发现不合规，按 🔴 极高优先级修复。

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

---

## 5. 子任务清单（按编号）

| 编号 | 名称 | 代码目录 | 文档目录 | 备注 |
|:----:|------|---------|---------|------|
| A1 | `pkg/api` | `pkg/api` | `docs/zh-CN/pkg/api` | 仅 1 个文件 `api.go` |
| A2 | `pkg/farm` | `pkg/farm` | `docs/zh-CN/pkg/farm` | 含 `farm.go`、`graph.go` 与 3 个测试 |
| A3 | `pkg/plot` | `pkg/plot` | `docs/zh-CN/pkg/plot` | 含 5 个核心文件与 2 个测试 |
| A4 | `pkg/observer` | `pkg/observer` | `docs/zh-CN/pkg/observer` | 含 `observer.go`、`progress.go` |
| A5 | `pkg/persist` | `pkg/persist` | `docs/zh-CN/pkg/persist` | 含 3 个核心文件与 1 个测试 |
| A6 | `pkg/funnel` | `pkg/funnel` | `docs/zh-CN/pkg/funnel` | 含 `inlet.go`、`spout.go` |
| A7 | `pkg/runtime` | `pkg/runtime` | `docs/zh-CN/pkg/runtime` | 含 `event.go`、`type.go` |
| A8 | `demo` | `demo` | `docs/zh-CN/demo` | 含 `demo_farm.go` |
| A9 | 顶层 README | （无源码） | `docs/zh-CN/README.md` | 从根 `README.md` 同步并保持与代码现状一致 |
