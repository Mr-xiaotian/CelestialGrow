---
name: "docs-i18n-sync"
description: "CelestialGrow 项目专属配置。补充 docs/zh-CN → docs/en、docs/ja 同步时本项目的路径/H1 规则、不用 --root-file 的原因，以及全局校验脚本的盲区。Invoke when zh-CN docs have been updated and English/Japanese translations need to follow."
---

# Docs I18n Sync（CelestialGrow 项目特化）

> 通用流程、动作分类、分批策略、子代理委派：`~/.agents/skills/docs-i18n-sync/SKILL.md`
> 通用翻译规则与输出格式：`~/.agents/skills/docs-i18n-sync/_subagent-base.md`
> 本文件只写**与全局不同**的地方，其余一律照全局执行。

## 1. 参数

| 角色 | 路径 | 日期行 |
|------|------|--------|
| 源（只读） | `docs/zh-CN` | `> 📅 最后更新日期:` |
| 目标 | `docs/en` | `> 📅 Last Updated:` |
| 目标 | `docs/ja` | `> 📅 最終更新日:` |

日期值**复制源文件的值**，不用"今天"。

## 2. 与全局的三处差异

### 2.1 不要传 `--root-file README.md`

全局示例都带 `--root-file README.md`，本项目**不能带**：`docs/zh-CN/README.md` 已存在，
README 的镜像路径本来就是 `docs/{lang}/README.md`，再传会为同一目标路径产出两条 `NEW`。

```bash
uv run python ~/.agents/skills/docs-i18n-sync/scan_i18n_diff.py \
    --project-root . --source docs/zh-CN --targets en:docs/en ja:docs/ja \
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
| `docs/zh-CN/README.md` | `# CelestialGrow` |

### 2.3 全局校验脚本查不到本项目的 H1 和截断

`validate_i18n_sync.py` 的 H1 正则只匹配 `src|tests|bench|demo` 下的 `*.py`，
本项目 `# pkg/**/*.go` 的 H1 **实际未被检查**（报 OK 是假象）；它也不查译文是否被截断或段落漂移。
所以阶段 4 要**多跑一次**（Go 版，不依赖 Python）：

```bash
go run .agents/skills/docs-i18n-sync/check_mirror.go \
    --project-root . --source docs/zh-CN --targets "en:docs/en ja:docs/ja"
```

## 3. 收尾

汇总格式照全局；删除临时产物（`tmp_i18n_*.md`、临时指令文件）；只写 `docs/{en,ja}/**`，不碰 git 区。
