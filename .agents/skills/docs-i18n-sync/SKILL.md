---
name: "docs-i18n-sync"
description: "CelestialGrow 项目专属配置。说明中文 README 仅保留项目根 README.md 并翻译到 docs/en/README.md、docs/ja/README.md，补充 docs/zh-CN → docs/en、docs/ja 同步时的路径/H1 规则，以及全局校验脚本的盲区。Invoke when Chinese docs or the root README have been updated and English/Japanese translations need to follow."
---

# Docs I18n Sync（CelestialGrow 项目特化）

> 通用流程、动作分类、分批策略、子代理委派：`~/.agents/skills/docs-i18n-sync/SKILL.md`
> 通用翻译规则与输出格式：`~/.agents/skills/docs-i18n-sync/_subagent-base.md`
> 本文件只写**与全局不同**的地方，其余一律照全局执行。

## 1. 参数

| 角色 | 路径 | 日期行 |
|------|------|--------|
| 源（只读） | `README.md`（项目根） | `> 📅 最后更新日期:` |
| 源（只读） | `docs/zh-CN/**` | `> 📅 最后更新日期:` |
| 目标 | `docs/en/README.md` + `docs/en/**` | `> 📅 Last Updated:` |
| 目标 | `docs/ja/README.md` + `docs/ja/**` | `> 📅 最終更新日:` |

日期值**复制源文件的值**，不用"今天"。

> **中文 README 的唯一位置是项目根 `README.md`。** `docs/zh-CN/README.md` 已删除，
> 不得重建；英文/日文 README 从根 `README.md` 翻译，仍落在 `docs/en/README.md`
> 与 `docs/ja/README.md`（即现有的各语言顶层 README 位置）。

## 2. 与全局的差异

### 2.1 必须传 `--root-file README.md`

中文 README 只存在于项目根，常规镜像只扫描 `docs/zh-CN`，因此必须显式把根 README
纳入扫描：

- `--root-file README.md` 会把 `README.md` 映射到 `docs/{lang}/README.md`
  （即 `docs/en/README.md`、`docs/ja/README.md`）。
- 不传该参数时，根 README 不会被翻译；同时目标侧已有的 `docs/{en,ja}/README.md`
  会被误判为 `DELETE`。

```bash
uv run python ~/.agents/skills/docs-i18n-sync/scan_i18n_diff.py \
    --project-root . --source docs/zh-CN --targets en:docs/en ja:docs/ja \
    --root-file README.md \
    --batch-size 12 --output tmp_i18n_manifest.md
```

> 本机 `python` 是 Microsoft Store 占位程序，必须用 `uv run python`；落盘用 `--output`，
> 不要用 PowerShell 的 `>`（会写成 UTF-16/BOM）。

### 2.2 H1 逐字镜像

zh-CN 的 H1 是源码相对路径，译文原样复制，不翻译：

| 源 | H1 |
|----|----|
| `docs/zh-CN/pkg/<name>/<file>.md` | `# pkg/<name>/<file>.go` |
| `docs/zh-CN/demo/<file>.md` | `# demo/<file>.go` |
| `README.md`（项目根） | `# CelestialGrow` |

### 2.3 全局校验脚本查不到本项目的 H1 和截断

`validate_i18n_sync.py` 的 H1 正则只匹配 `src|tests|bench|demo` 下的 `*.py`，
本项目 `# pkg/**/*.go` 的 H1 **实际未被检查**（报 OK 是假象）；它也不查译文是否被截断或段落漂移。
所以阶段 4 要**多跑一次**（Go 版，不依赖 Python）：

```bash
uv run python ~/.agents/skills/docs-i18n-sync/validate_i18n_sync.py \
    --project-root . --source docs/zh-CN --targets en:docs/en ja:docs/ja \
    --root-file README.md

go run .agents/skills/docs-i18n-sync/check_mirror.go \
    --project-root . --source docs/zh-CN --targets "en:docs/en ja:docs/ja" \
    --root-file README.md
```

`check_mirror.go` 会额外比对根 `README.md` 与 `docs/{en,ja}/README.md` 的行数、H1 与日期行，
规则与其余镜像文档一致。

## 3. 收尾

汇总格式照全局；删除临时产物（`tmp_i18n_*.md`、临时指令文件）；只写 `docs/{en,ja}/**` 与根 `README.md`（仅当用户要求修改中文源），不碰 git 区。**不要重建 `docs/zh-CN/README.md`。**
