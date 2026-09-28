// CelestialGrow 项目 docs-zh-sync 扫描脚本（Go 版本）。
//
// 与通用框架 scan_manifest.py 的差异：
//   - 仅使用 Go 标准库，编译/运行无需 Python 解释器
//   - 代码后缀固定为 .go（含 *_test.go），后缀映射 .go -> .md
//   - 排除 .git、logs、lifecycles 等目录
//   - CLI 保持一致：--project-root / --pairs / --top-level / --format / --output / --rename-threshold
//
// 输出四表：exists（审计内容一致性）/ missing（需新建）/ orphans（需移动或删除）/
// overviews（无 1:1 源码的总览文档，保留、勿删、不套用 H1 规范）。
//
// 用法：
//
//	go run .agents/skills/docs-zh-sync/scan_manifest.go \
//	    --project-root . \
//	    --pairs "pkg/api:docs/zh-CN/pkg/api pkg/farm:docs/zh-CN/pkg/farm" \
//	    --top-level docs/zh-CN --output tmp_manifest.md
//
//	go run .agents/skills/docs-zh-sync/scan_manifest.go \
//	    --project-root . --top-level docs/zh-CN --format json
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// ---- 项目特定配置 ----------------------------------------------------

var codeExts = map[string]bool{".go": true}
var docExts = map[string]bool{".md": true}

var excludeDirNames = map[string]bool{
	".git":         true,
	".venv":        true,
	"node_modules": true,
	"logs":         true,
	"lifecycles":   true,
}

var suffixMap = map[string]string{".go": ".md"}

// overviewDocNames 是无 1:1 源码的总览文档：保留、勿删，也不套用 H1 规范。
// 镜像目录内出现同名文件时同样受保护，不会被当作孤立文档。
var overviewDocNames = map[string]bool{"readme.md": true}

const defaultRenameThreshold = 0.5

// ---- 数据结构 --------------------------------------------------------

type RenameCandidate struct {
	MissingDoc  string  `json:"missing_doc"`
	MissingCode string  `json:"missing_code"`
	OrphanDoc   string  `json:"orphan_doc"`
	Similarity  float64 `json:"similarity"`
	Suggestion  string  `json:"suggestion"`
}

type AreaResult struct {
	CodeDir   string              `json:"code_dir"`
	DocDir    string              `json:"doc_dir"`
	Exists    []map[string]string `json:"exists"`
	Missing   []map[string]string `json:"missing"`
	Orphans   []map[string]string `json:"orphans"`
	Overviews []map[string]string `json:"overviews"`
	Renames   []RenameCandidate   `json:"renames"`
}

// ---- 路径推导 --------------------------------------------------------

// codeToDoc 按后缀映射推导源码文件对应的文档路径；无法映射时返回 ""。
func codeToDoc(code, codeRoot, docRoot string) string {
	rel, err := filepath.Rel(codeRoot, code)
	if err != nil {
		return ""
	}
	ext := filepath.Ext(rel)
	newExt, ok := suffixMap[ext]
	if !ok {
		return ""
	}
	return filepath.Join(docRoot, strings.TrimSuffix(rel, ext)+newExt)
}

// docToCode 按后缀映射反向推导文档对应的源码路径；无法映射时返回 ""。
func docToCode(doc, docRoot, codeRoot string) string {
	rel, err := filepath.Rel(docRoot, doc)
	if err != nil {
		return ""
	}
	ext := filepath.Ext(rel)
	for codeExt, docExt := range suffixMap {
		if ext == docExt {
			return filepath.Join(codeRoot, strings.TrimSuffix(rel, ext)+codeExt)
		}
	}
	return ""
}

// ---- 扫描 ------------------------------------------------------------

func scanFiles(root string, exts map[string]bool) []string {
	var out []string
	_ = filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return nil
		}
		if info.IsDir() {
			return nil
		}
		if !exts[filepath.Ext(path)] {
			return nil
		}
		for _, part := range strings.Split(path, string(os.PathSeparator)) {
			if excludeDirNames[part] {
				return nil
			}
		}
		out = append(out, path)
		return nil
	})
	sort.Strings(out)
	return out
}

func fileExists(p string) bool {
	_, err := os.Stat(p)
	return err == nil
}

func relPath(p, base string) string {
	r, err := filepath.Rel(base, p)
	if err != nil {
		return p
	}
	return filepath.ToSlash(r)
}

// ---- 重命名相似度（基于最长公共子串）---------------------------------

func stemSimilarity(a, b string) float64 {
	la := strings.ToLower(a)
	lb := strings.ToLower(b)
	if la == lb {
		return 1.0
	}
	if la == "" || lb == "" {
		return 0.0
	}
	aRunes := []rune(la)
	bRunes := []rune(lb)
	n, m := len(aRunes), len(bRunes)
	longest := 0
	for i := 0; i < n; i++ {
		for j := 0; j < m; j++ {
			k := 0
			for i+k < n && j+k < m && aRunes[i+k] == bRunes[j+k] {
				k++
			}
			if k > longest {
				longest = k
			}
		}
	}
	return 2.0 * float64(longest) / float64(n+m)
}

func round3(f float64) float64 {
	return float64(int64(f*1000+0.5)) / 1000.0
}

func detectRenames(missing, orphans []map[string]string, threshold float64) []RenameCandidate {
	bestForOrphan := make(map[string]RenameCandidate)
	for _, m := range missing {
		mStem := stemOf(m["doc"])
		for _, o := range orphans {
			oDoc := o["doc"]
			sim := stemSimilarity(mStem, stemOf(oDoc))
			if sim < threshold {
				continue
			}
			cand := RenameCandidate{
				MissingDoc:  m["doc"],
				MissingCode: m["code"],
				OrphanDoc:   oDoc,
				Similarity:  round3(sim),
				Suggestion:  fmt.Sprintf("疑似重命名：%s -> %s（相似度 %.2f）", oDoc, m["doc"], sim),
			}
			if existing, ok := bestForOrphan[oDoc]; !ok || cand.Similarity > existing.Similarity {
				bestForOrphan[oDoc] = cand
			}
		}
	}
	renames := make([]RenameCandidate, 0, len(bestForOrphan))
	for _, r := range bestForOrphan {
		renames = append(renames, r)
	}
	sort.Slice(renames, func(i, j int) bool { return renames[i].Similarity > renames[j].Similarity })
	return renames
}

func stemOf(p string) string {
	base := filepath.Base(p)
	ext := filepath.Ext(base)
	return strings.TrimSuffix(base, ext)
}

// ---- Manifest 构建 ----------------------------------------------------

func buildArea(codeDir, docDir, projectRoot string, threshold float64) AreaResult {
	result := AreaResult{
		CodeDir: relPath(codeDir, projectRoot),
		DocDir:  relPath(docDir, projectRoot),
	}
	for _, cf := range scanFiles(codeDir, codeExts) {
		target := codeToDoc(cf, codeDir, docDir)
		if target == "" {
			continue
		}
		relCode := relPath(cf, projectRoot)
		relTarget := relPath(target, projectRoot)
		if fileExists(target) {
			result.Exists = append(result.Exists, map[string]string{"code": relCode, "doc": relTarget})
		} else {
			result.Missing = append(result.Missing, map[string]string{"code": relCode, "doc": relTarget})
		}
	}
	for _, df := range scanFiles(docDir, docExts) {
		relDoc := relPath(df, projectRoot)
		if overviewDocNames[strings.ToLower(filepath.Base(df))] {
			result.Overviews = append(result.Overviews, map[string]string{"doc": relDoc})
			continue
		}
		target := docToCode(df, docDir, codeDir)
		if target != "" && fileExists(target) {
			continue
		}
		candidate := "（无直接源码对应）"
		if target != "" {
			candidate = relPath(target, projectRoot)
		}
		result.Orphans = append(result.Orphans, map[string]string{
			"doc":            relDoc,
			"code_candidate": candidate,
		})
	}
	result.Renames = detectRenames(result.Missing, result.Orphans, threshold)
	return result
}

func buildTopLevel(docDir, projectRoot string) AreaResult {
	result := AreaResult{
		CodeDir: "(top-level)",
		DocDir:  relPath(docDir, projectRoot),
	}
	entries, err := os.ReadDir(docDir)
	if err != nil {
		return result
	}
	for _, e := range entries {
		if e.IsDir() || !docExts[filepath.Ext(e.Name())] {
			continue
		}
		result.Overviews = append(result.Overviews, map[string]string{
			"doc": relPath(filepath.Join(docDir, e.Name()), projectRoot),
		})
	}
	return result
}

// ---- 渲染 ------------------------------------------------------------

func renderMarkdown(areas []AreaResult) string {
	var sb strings.Builder
	for _, area := range areas {
		if area.CodeDir == "(top-level)" {
			sb.WriteString(fmt.Sprintf("\n## 顶层文档目录: %s\n", area.DocDir))
			sb.WriteString("无 1:1 源码对应，均为总览文档（保留，勿删，不套用 H1 规范）。\n")
			writeOverviewTable(&sb, area.Overviews)
			continue
		}

		sb.WriteString(fmt.Sprintf("\n## 区域: %s -> %s\n", area.CodeDir, area.DocDir))

		sb.WriteString("\n### 有代码且有文档（审计内容一致性）\n")
		sb.WriteString("| # | 代码文件 | 文档文件 |\n|---|---------|---------|\n")
		writeRows(&sb, area.Exists, "code", "doc")

		sb.WriteString("\n### 有代码但无文档（需新建）\n")
		sb.WriteString("| # | 代码文件 | 目标文档 |\n|---|---------|---------|\n")
		writeRows(&sb, area.Missing, "code", "doc")

		sb.WriteString("\n### 孤立文档（需移动/删除）\n")
		sb.WriteString("| # | 当前文档 | 源码实际位置 | 处理建议 |\n|---|---------|-------------|---------|\n")
		if len(area.Orphans) == 0 {
			sb.WriteString("| - | - | - | 无 |\n")
		} else {
			renameByOrphan := make(map[string]RenameCandidate, len(area.Renames))
			for _, r := range area.Renames {
				renameByOrphan[r.OrphanDoc] = r
			}
			for i, item := range area.Orphans {
				suggestion := "删除（无对应源码）"
				if r, ok := renameByOrphan[item["doc"]]; ok {
					suggestion = r.Suggestion
				}
				sb.WriteString(fmt.Sprintf("| %d | `%s` | `%s` | %s |\n",
					i+1, item["doc"], item["code_candidate"], suggestion))
			}
		}

		sb.WriteString("\n### 总览文档（保留，勿删）\n")
		writeOverviewTable(&sb, area.Overviews)
	}
	return sb.String()
}

// writeRows 渲染「代码文件 | 文档文件」两列三行式表格，空列表输出一行「无」。
func writeRows(sb *strings.Builder, items []map[string]string, k1, k2 string) {
	if len(items) == 0 {
		sb.WriteString("| - | - | 无 |\n")
		return
	}
	for i, item := range items {
		sb.WriteString(fmt.Sprintf("| %d | `%s` | `%s` |\n", i+1, item[k1], item[k2]))
	}
}

// writeOverviewTable 渲染总览文档列表，空列表输出一行「无」。
func writeOverviewTable(sb *strings.Builder, items []map[string]string) {
	sb.WriteString("\n| # | 文档文件 |\n|---|---------|\n")
	if len(items) == 0 {
		sb.WriteString("| - | 无 |\n")
		return
	}
	for i, item := range items {
		sb.WriteString(fmt.Sprintf("| %d | `%s` |\n", i+1, item["doc"]))
	}
}

// ---- 解析 --pairs（支持空格分隔的多值，等价 Python nargs="+"）--------

// flagMatch 检查 arg 是否为指定 flag 的两种形式（-name / --name），可带或不带 =value。
// 返回 (matched, value, hasInlineValue)。
func flagMatch(arg, name string) (bool, string, bool) {
	for _, prefix := range []string{"--" + name, "-" + name} {
		if arg == prefix {
			return true, "", false
		}
		if strings.HasPrefix(arg, prefix+"=") {
			return true, strings.TrimPrefix(arg, prefix+"="), true
		}
	}
	return false, "", false
}

// collectFlagValues 提取指定 flag 后所有连续的非 flag 值。
// 支持 -flag value / --flag value / -flag=value / --flag=value 四种形式。
// 等价于 Python argparse 的 nargs="+"。
func collectFlagValues(args []string, name string) []string {
	var out []string
	i := 0
	for i < len(args) {
		matched, inline, hasInline := flagMatch(args[i], name)
		if !matched {
			i++
			continue
		}
		if hasInline {
			out = append(out, inline)
			i++
			continue
		}
		// 形式 -flag value / --flag value：取后续非 flag 参数
		i++
		for i < len(args) {
			if isFlagStart(args[i]) {
				break
			}
			out = append(out, args[i])
			i++
		}
	}
	return out
}

// filterArgs 从 args 中移除指定 flags 及其所有后续值。
// flags 列表传入不带前导 -- 的 flag 名，内部同时匹配 --flag/-flag 与 =value 形式。
// 与 collectFlagValues 保持一致：一个 flag 后面所有连续的非 flag 参数都算它的值（等价 argparse 的 nargs="+"），
// 否则 --pairs 之后出现的其他 flag 会被 flag.Parse 当作位置参数而静默忽略。
func filterArgs(args []string, flags []string) []string {
	out := make([]string, 0, len(args))
	i := 0
	for i < len(args) {
		matched := false
		for _, f := range flags {
			if ok, _, _ := flagMatch(args[i], f); ok {
				matched = true
				break
			}
		}
		if !matched {
			out = append(out, args[i])
			i++
			continue
		}
		i++
		for i < len(args) && !isFlagStart(args[i]) {
			i++
		}
	}
	return out
}

// isFlagStart 判定一个 token 是否为新 flag 的起点（仅 -flag / --flag，不含 =value）。
func isFlagStart(s string) bool {
	if len(s) < 2 || s[0] != '-' {
		return false
	}
	return s[1] == '-'
}

// ---- main ------------------------------------------------------------

func main() {
	fs := flag.NewFlagSet("scan-manifest", flag.ExitOnError)
	projectRoot := fs.String("project-root", ".", "项目根目录（用于将所有路径输出为相对路径）")
	format := fs.String("format", "markdown", "输出格式：markdown 或 json")
	output := fs.String("output", "", "输出文件路径；留空则打印到 stdout（写文件时按 UTF-8 编码）")
	renameThreshold := fs.Float64("rename-threshold", defaultRenameThreshold, "重命名相似度阈值（0-1）")
	fs.Usage = func() {
		fmt.Fprintf(os.Stderr, `用法：scan_manifest --project-root <root> (--pairs <c1:d1 [c2:d2 ...]> | --top-level <dir>) [--format markdown|json] [--output <file>]

示例：
  scan_manifest --project-root . \
      --pairs "pkg/api:docs/zh-CN/pkg/api pkg/farm:docs/zh-CN/pkg/farm" \
      --top-level docs/zh-CN --output tmp_manifest.md
`)
		fs.PrintDefaults()
	}

	// 兼容 Python 版本的 nargs="+"：从 os.Args 手动提取 --pairs / --top-level 的多值。
	pairArgs := collectFlagValues(os.Args[1:], "pairs")
	topLevelArgs := collectFlagValues(os.Args[1:], "top-level")

	// 构造过滤后的 args，把 --pairs/--top-level 及其值剔除后再交给 flag.Parse。
	// 同时支持 --flag 与 -flag 两种形式。
	filtered := filterArgs(os.Args[1:], []string{"pairs", "top-level"})
	if err := fs.Parse(filtered); err != nil {
		os.Exit(2)
	}

	if len(pairArgs) == 0 && len(topLevelArgs) == 0 {
		fs.Usage()
		fmt.Fprintln(os.Stderr, "错误：至少需要 --pairs 或 --top-level 之一")
		os.Exit(2)
	}

	abs, err := filepath.Abs(*projectRoot)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}

	var pairs [][2]string
	for _, p := range pairArgs {
		parts := strings.SplitN(p, ":", 2)
		if len(parts) != 2 {
			fmt.Fprintf(os.Stderr, "--pairs 参数格式错误：%q 应为 code_dir:doc_dir\n", p)
			os.Exit(2)
		}
		pairs = append(pairs, [2]string{
			filepath.Join(abs, parts[0]),
			filepath.Join(abs, parts[1]),
		})
	}

	var areas []AreaResult
	for _, p := range pairs {
		areas = append(areas, buildArea(p[0], p[1], abs, *renameThreshold))
	}
	for _, tl := range topLevelArgs {
		areas = append(areas, buildTopLevel(filepath.Join(abs, tl), abs))
	}

	if *format == "json" {
		out, err := json.MarshalIndent(map[string]interface{}{"areas": areas}, "", "  ")
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		if err := emit(string(out)+"\n", *output); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		return
	}
	if err := emit(renderMarkdown(areas), *output); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

// emit 把结果写入文件（UTF-8）或 stdout。
func emit(s, output string) error {
	if output == "" {
		fmt.Print(s)
		return nil
	}
	return os.WriteFile(output, []byte(s), 0o644)
}
