// CelestialGrow 项目 docs-i18n-sync 结构镜像校验脚本（Go 版本）。
//
// 与通用框架 validate_i18n_sync.py 的分工：
//   - validate_i18n_sync.py 负责镜像完整性、代码围栏配对、非代码区 CJK 残留；
//     但它的 H1 正则只匹配 src|tests|bench|demo 下的 *.py，本项目 # pkg/**/*.go
//     的 H1 实际从未被检查（报 OK 是假象）。
//   - 本脚本补充逐文件比对：文件存在、行数、H1、日期行，可暴露「译文被截断 /
//     段落漂移」（子代理中断或 token 上限时会发生）。
//
// 行尾按通用换行归一化（\r\n / \r → \n）后再比较，理由见 readLines。
//
// 仅使用 Go 标准库，运行无需 Python 解释器。
//
// 用法：
//
//	go run .agents/skills/docs-i18n-sync/check_mirror.go \
//	    --project-root . --source docs/zh-CN --targets "en:docs/en ja:docs/ja"
//
// 退出码：有任一问题返回 1，否则 0。
package main

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

// datePatterns 是各语言的日期行正则，需与 ~/.agents/skills/docs-i18n-sync/scan_i18n_diff.py 保持一致。
var datePatterns = map[string]*regexp.Regexp{
	"zh-CN": regexp.MustCompile(`^>\s*📅\s*最后更新日期:\s*(\d{4}/\d{2}/\d{2})\s*$`),
	"en":    regexp.MustCompile(`^>\s*📅\s*Last Updated:\s*(\d{4}/\d{2}/\d{2})\s*$`),
	"ja":    regexp.MustCompile(`^>\s*📅\s*最終更新日:\s*(\d{4}/\d{2}/\d{2})\s*$`),
}

// skipDirNames 是递归扫描时跳过的目录名。
var skipDirNames = map[string]bool{
	"__pycache__":  true,
	".git":         true,
	".venv":        true,
	"node_modules": true,
}

// target 是一个 lang:dir 目标语言配对。
type target struct {
	lang string
	dir  string
}

// targetList 收集 --targets 的取值，支持空格/逗号分隔或重复传入。
type targetList []string

// String 实现 flag.Value。
func (t *targetList) String() string { return strings.Join(*t, " ") }

// Set 实现 flag.Value，按空格/逗号/制表符切分后追加。
func (t *targetList) Set(v string) error {
	*t = append(*t, strings.FieldsFunc(v, func(r rune) bool {
		return r == ' ' || r == ',' || r == '\t'
	})...)
	return nil
}

// readLines 按 UTF-8 读取文件并按换行切分。
//
// 与 Python 的 read_text 一致做通用换行归一化（\r\n / \r → \n）：仓库 core.autocrlf=true，
// 工作区里同一文件可能表现为 CRLF 或 LF，但 git 索引里统一为 LF，故行尾差异不算镜像不一致。
// 文件末尾的换行体现为最后一个空元素。
func readLines(path string) ([]string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	text := strings.ReplaceAll(string(data), "\r\n", "\n")
	text = strings.ReplaceAll(text, "\r", "\n")
	return strings.Split(text, "\n"), nil
}

// dateOf 在 line 匹配 lang 的日期行时返回其中的日期值，否则返回 ""。
func dateOf(line, lang string) string {
	re, ok := datePatterns[lang]
	if !ok {
		return ""
	}
	if m := re.FindStringSubmatch(line); m != nil {
		return m[1]
	}
	return ""
}

// head 返回首个元素；空切片返回 ""。
func head(lines []string) string {
	if len(lines) == 0 {
		return ""
	}
	return lines[0]
}

// findMarkdown 递归收集 root 下所有 .md 文件，返回相对 root 的斜杠路径（已排序）。
func findMarkdown(root string) ([]string, error) {
	var out []string
	err := filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return nil // 忽略无法访问的条目
		}
		if d.IsDir() {
			if skipDirNames[d.Name()] {
				return filepath.SkipDir
			}
			return nil
		}
		if filepath.Ext(path) != ".md" {
			return nil
		}
		rel, relErr := filepath.Rel(root, path)
		if relErr != nil {
			return nil
		}
		out = append(out, filepath.ToSlash(rel))
		return nil
	})
	return out, err
}

// checkPair 比对单个译文文件，返回问题描述列表。
//
// src 是源文件按行切分的内容，srcDate 是源文件日期值（"" 表示源无日期行）。
func checkPair(tgtPath, rel string, tgt target, src []string, srcDate string) []string {
	name := tgt.lang + "/" + rel
	lines, err := readLines(tgtPath)
	if err != nil {
		return []string{"[缺文件] " + name}
	}

	var problems []string
	if len(lines) != len(src) {
		problems = append(problems, fmt.Sprintf("[行数] %s: 源 %d / 译文 %d", name, len(src), len(lines)))
	}
	if head(lines) != head(src) {
		problems = append(problems, fmt.Sprintf("[H1] %s: %q != %q", name, head(lines), head(src)))
	}

	tgtDate := ""
	if len(lines) > 2 {
		tgtDate = dateOf(lines[2], tgt.lang)
	}
	switch {
	case srcDate == "" && tgtDate != "":
		problems = append(problems, fmt.Sprintf("[日期行] %s: 源无日期行但译文有（%s）", name, tgtDate))
	case srcDate != "" && tgtDate != srcDate:
		actual := ""
		if len(lines) > 2 {
			actual = lines[2]
		}
		problems = append(problems, fmt.Sprintf("[日期行] %s: 期望 %s，实际 %q", name, srcDate, actual))
	}
	return problems
}

func main() {
	fs := flag.NewFlagSet("check-mirror", flag.ExitOnError)
	projectRoot := fs.String("project-root", ".", "项目根目录（用于将所有路径输出为相对路径）")
	source := fs.String("source", "docs/zh-CN", "源语言文档目录")
	var rawTargets targetList
	fs.Var(&rawTargets, "targets", "目标语言配对 lang:dir，空格/逗号分隔或重复传入")
	fs.Usage = func() {
		fmt.Fprintf(os.Stderr, `用法：check_mirror --project-root <root> --source <dir> --targets "<lang:dir> [<lang:dir> ...]"

示例：
  check_mirror --project-root . --source docs/zh-CN --targets "en:docs/en ja:docs/ja"
`)
	}
	if err := fs.Parse(os.Args[1:]); err != nil {
		os.Exit(1)
	}
	if len(rawTargets) == 0 {
		rawTargets = targetList{"en:docs/en", "ja:docs/ja"}
	}

	targets := make([]target, 0, len(rawTargets))
	for _, item := range rawTargets {
		lang, dir, ok := strings.Cut(item, ":")
		if !ok || datePatterns[lang] == nil {
			fmt.Fprintf(os.Stderr, "--targets %q 格式错误（应为 lang:dir，lang 仅支持 en / ja）\n", item)
			os.Exit(1)
		}
		targets = append(targets, target{lang: lang, dir: dir})
	}

	srcRoot := filepath.Join(*projectRoot, filepath.FromSlash(*source))
	srcFiles, err := findMarkdown(srcRoot)
	if err != nil || len(srcFiles) == 0 {
		fmt.Fprintf(os.Stderr, "[错误] 源目录无 .md 文件：%s\n", srcRoot)
		os.Exit(1)
	}

	var problems []string
	for _, rel := range srcFiles {
		src, err := readLines(filepath.Join(srcRoot, filepath.FromSlash(rel)))
		if err != nil {
			problems = append(problems, "[读取失败] "+(*source)+"/"+rel+": "+err.Error())
			continue
		}
		srcDate := ""
		if len(src) > 2 {
			srcDate = dateOf(src[2], "zh-CN")
		}
		if srcDate == "" {
			problems = append(problems, fmt.Sprintf("[源日期行] %s/%s: 缺少或格式不符", *source, rel))
		}
		for _, tgt := range targets {
			tgtPath := filepath.Join(*projectRoot, filepath.FromSlash(tgt.dir), filepath.FromSlash(rel))
			problems = append(problems, checkPair(tgtPath, rel, tgt, src, srcDate)...)
		}
	}

	fmt.Printf("检查 %d 个源文件 × %d 个目标语言\n", len(srcFiles), len(targets))
	if len(problems) == 0 {
		fmt.Println("逐文件镜像（行数 / H1 / 日期行）：全部一致 ✅")
		return
	}
	fmt.Printf("\n发现 %d 处问题 ❌\n", len(problems))
	for _, p := range problems {
		fmt.Println(" -", p)
	}
	os.Exit(1)
}
