package main

import (
	"bufio"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

var defaultExts = "png,jpg,jpeg,gif,webp,svg,bmp,ico"

// stringList is a repeatable string flag (--img-dir a --img-dir b).
type stringList []string

func (s *stringList) String() string { return strings.Join(*s, ",") }
func (s *stringList) Set(v string) error {
	*s = append(*s, v)
	return nil
}

var (
	flagImgDirs stringList
	flagDel     bool
	flagMove    string
	flagClean   bool
	flagYes     bool
	flagExts    string
)

func main() {
	flag.Var(&flagImgDirs, "img-dir", "额外的图片扫描目录（可多次指定）；未指定时自动取 CSS 引用图片的父目录")
	flag.BoolVar(&flagDel, "delete", false, "删除未被引用的图片")
	flag.StringVar(&flagMove, "move", "", "将未被引用的图片移动到指定目录（与 --delete 互斥）")
	flag.BoolVar(&flagClean, "clean-css", false, "重写 CSS，移除引用了缺失图片的规则（生成 .bak 备份）")
	flag.BoolVar(&flagYes, "yes", false, "跳过删除/重写前的确认")
	flag.StringVar(&flagExts, "ext", defaultExts, "图片扩展名白名单（逗号分隔）")
	flag.Usage = printUsage
	flag.Parse()

	cssFiles := flag.Args()
	if len(cssFiles) == 0 {
		flag.Usage()
		os.Exit(2)
	}
	if flagDel && flagMove != "" {
		fmt.Fprintln(os.Stderr, "[错误] --delete 与 --move 不能同时使用")
		os.Exit(2)
	}

	exts := parseExts(flagExts)

	var cssAbs []string
	for _, c := range cssFiles {
		abs, err := filepath.Abs(c)
		if err != nil {
			fmt.Fprintf(os.Stderr, "[错误] 无法解析路径 %s: %v\n", c, err)
			os.Exit(1)
		}
		if !fileExists(abs) {
			fmt.Fprintf(os.Stderr, "[错误] CSS 文件不存在: %s\n", abs)
			os.Exit(1)
		}
		cssAbs = append(cssAbs, abs)
	}

	var allRefs []urlRef
	referenced := make(map[string]bool)
	for _, css := range cssAbs {
		data, err := os.ReadFile(css)
		if err != nil {
			fmt.Fprintf(os.Stderr, "[错误] 读取 CSS 失败 %s: %v\n", css, err)
			os.Exit(1)
		}
		refs := collectRefs(css, string(data))
		allRefs = append(allRefs, refs...)
		for _, r := range refs {
			referenced[norm(r.resolved)] = true
		}
	}

	var missing []urlRef
	seen := make(map[string]bool)
	for _, r := range allRefs {
		if r.exists {
			continue
		}
		k := norm(r.resolved)
		if seen[k] {
			continue
		}
		seen[k] = true
		missing = append(missing, r)
	}

	roots := scanRoots(allRefs, flagImgDirs)
	orphans, hashSkipped := findOrphans(roots, referenced, exts)

	printReport(cssAbs, allRefs, missing, roots, orphans, hashSkipped)

	destructive := flagDel || flagMove != "" || flagClean
	if !destructive {
		fmt.Println()
		fmt.Println("[提示] 本次为预览模式，未修改任何文件。加 --delete / --move / --clean-css 执行清理。")
		return
	}
	fmt.Println()
	if !flagYes {
		parts := []string{}
		if flagDel {
			parts = append(parts, fmt.Sprintf("删除 %d 张未引用图片", len(orphans)))
		}
		if flagMove != "" {
			parts = append(parts, fmt.Sprintf("移动 %d 张未引用图片到 %s", len(orphans), flagMove))
		}
		if flagClean {
			parts = append(parts, fmt.Sprintf("重写 %d 个 CSS（移除断链规则）", len(cssAbs)))
		}
		if !confirm("确认执行：" + strings.Join(parts, "；") + " ？") {
			fmt.Println("已取消，未做任何修改。")
			return
		}
	}

	if flagDel || flagMove != "" {
		handleOrphans(orphans)
	}
	if flagClean {
		handleCleanCSS(cssAbs)
	}
}

func printUsage() {
	out := os.Stderr
	fmt.Fprintln(out, "cleanUnused - 清理未被 CSS 引用的图片与引用了缺失图片的 CSS 规则")
	fmt.Fprintln(out)
	fmt.Fprintln(out, "用法:")
	fmt.Fprintln(out, "  cleanUnused [选项] <css文件> [<css文件>...]")
	fmt.Fprintln(out)
	fmt.Fprintln(out, "说明:")
	fmt.Fprintln(out, "  - 根据 CSS 中 url() 的相对路径定位图片，判断是否过时。")
	fmt.Fprintln(out, "  - 过时图片: 磁盘上存在但未被任何传入 CSS 引用的图片。")
	fmt.Fprintln(out, "  - 过时 CSS : CSS 规则引用了不存在的图片（断链规则）。")
	fmt.Fprintln(out, "  - 默认仅报告（dry-run），需显式加 --delete / --move / --clean-css 才会改动。")
	fmt.Fprintln(out, "  - 若多个 CSS 共用同一图片目录，请一并传入，避免误判共用图片为孤儿。")
	fmt.Fprintln(out)
	fmt.Fprintln(out, "选项:")
	flag.PrintDefaults()
	fmt.Fprintln(out)
	fmt.Fprintln(out, "示例:")
	fmt.Fprintln(out, "  cleanUnused D:\\proj\\res\\wap\\css\\xdrNormal.css")
	fmt.Fprintln(out, "  cleanUnused --clean-css a.css b.css")
	fmt.Fprintln(out, "  cleanUnused --move .\\_unused xdrNormal.css")
}

func parseExts(s string) []string {
	parts := strings.Split(s, ",")
	var exts []string
	for _, p := range parts {
		p = strings.ToLower(strings.TrimSpace(p))
		if p == "" {
			continue
		}
		if !strings.HasPrefix(p, ".") {
			p = "." + p
		}
		exts = append(exts, p)
	}
	return exts
}

func norm(p string) string {
	return strings.ToLower(filepath.Clean(p))
}

func isImageFile(name string, exts []string) bool {
	ext := strings.ToLower(filepath.Ext(name))
	for _, e := range exts {
		if ext == e {
			return true
		}
	}
	return false
}

// scanRoots builds a minimal set of directories to scan for orphan images:
// the parent dirs of every referenced image (existing or not), plus any
// explicit --img-dir, with descendant dirs removed so each file is visited once.
func scanRoots(refs []urlRef, extra []string) []string {
	dirSet := make(map[string]bool)
	for _, r := range refs {
		d := filepath.Dir(r.resolved)
		if dirExists(d) {
			dirSet[d] = true
		}
	}
	for _, e := range extra {
		abs, err := filepath.Abs(e)
		if err != nil {
			continue
		}
		if dirExists(abs) {
			dirSet[abs] = true
		}
	}
	dirs := make([]string, 0, len(dirSet))
	for d := range dirSet {
		dirs = append(dirs, d)
	}
	sort.Strings(dirs)

	var roots []string
	for i, d := range dirs {
		descendant := false
		for j, o := range dirs {
			if i == j {
				continue
			}
			if isStrictSubdir(d, o) {
				descendant = true
				break
			}
		}
		if !descendant {
			roots = append(roots, d)
		}
	}
	return roots
}

// isStrictSubdir reports whether d is a strict descendant of parent.
func isStrictSubdir(d, parent string) bool {
	rel, err := filepath.Rel(parent, d)
	if err != nil {
		return false
	}
	rel = filepath.ToSlash(rel)
	return rel != "." && !strings.HasPrefix(rel, "../") && rel != ".."
}

func findOrphans(roots []string, referenced map[string]bool, exts []string) (orphans []string, hashSkipped int) {
	visited := make(map[string]bool)
	for _, root := range roots {
		filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
			if err != nil || info.IsDir() {
				return nil
			}
			name := info.Name()
			if !isImageFile(name, exts) {
				return nil
			}
			// hash 版本是 hashCdn 的构建产物，重建时会自动清理；始终排除避免误删
			if isHashImageFileName(name) {
				hashSkipped++
				return nil
			}
			k := norm(path)
			if referenced[k] || visited[k] {
				return nil
			}
			visited[k] = true
			orphans = append(orphans, path)
			return nil
		})
	}
	sort.Strings(orphans)
	return orphans, hashSkipped
}

func printReport(cssAbs []string, refs []urlRef, missing []urlRef, roots []string, orphans []string, hashSkipped int) {
	fmt.Println("==================== cleanUnused 报告 ====================")
	fmt.Printf("扫描 CSS 文件: %d 个\n", len(cssAbs))
	for _, c := range cssAbs {
		fmt.Printf("  - %s\n", c)
	}
	fmt.Printf("CSS 中本地 url() 引用: %d 处\n", len(refs))
	fmt.Printf("图片扫描目录: %d 个\n", len(roots))
	for _, r := range roots {
		fmt.Printf("  - %s\n", r)
	}
	fmt.Printf("已保护 hash 版本图片: %d 个（默认排除，由 hashCdn 管理）\n", hashSkipped)
	fmt.Println()

	fmt.Printf("【过时 CSS】引用了缺失图片的引用: %d 处\n", len(missing))
	if len(missing) > 0 {
		for _, r := range missing {
			sel := r.selector
			if len(sel) > 80 {
				sel = sel[:80] + "..."
			}
			fmt.Printf("  [缺失] %s\n", filepath.Base(r.raw))
			fmt.Printf("    CSS : %s:%d\n", filepath.Base(r.cssFile), r.line)
			fmt.Printf("    路径: %s\n", r.raw)
			fmt.Printf("    解析: %s\n", r.resolved)
			if sel != "" {
				fmt.Printf("    选择器: %s\n", sel)
			}
		}
	} else {
		fmt.Println("  （无）")
	}
	fmt.Println()

	fmt.Printf("【过时图片】未被任何 CSS 引用的图片: %d 个\n", len(orphans))
	if len(orphans) > 0 {
		for _, p := range orphans {
			fmt.Printf("  [孤儿] %s\n", p)
		}
	} else {
		fmt.Println("  （无）")
	}
}

func handleOrphans(orphans []string) {
	if len(orphans) == 0 {
		fmt.Println("[图片] 没有未引用图片需要处理。")
		return
	}
	if flagMove != "" {
		abs, err := filepath.Abs(flagMove)
		if err != nil {
			fmt.Fprintf(os.Stderr, "[错误] 无法解析 --move 路径: %v\n", err)
			os.Exit(1)
		}
		if err := os.MkdirAll(abs, 0755); err != nil {
			fmt.Fprintf(os.Stderr, "[错误] 创建目标目录失败: %v\n", err)
			os.Exit(1)
		}
		moved, failed := 0, 0
		for _, p := range orphans {
			dst := uniqueDest(abs, filepath.Base(p))
			if err := moveFile(p, dst); err != nil {
				fmt.Fprintf(os.Stderr, "  [失败] 移动 %s: %v\n", p, err)
				failed++
			} else {
				moved++
			}
		}
		fmt.Printf("[图片] 已移动 %d 个，失败 %d 个 -> %s\n", moved, failed, abs)
		return
	}
	deleted, failed := 0, 0
	for _, p := range orphans {
		if err := os.Remove(p); err != nil {
			fmt.Fprintf(os.Stderr, "  [失败] 删除 %s: %v\n", p, err)
			failed++
		} else {
			deleted++
		}
	}
	fmt.Printf("[图片] 已删除 %d 个，失败 %d 个\n", deleted, failed)
}

func handleCleanCSS(cssAbs []string) {
	totalRemoved := 0
	for _, css := range cssAbs {
		data, err := os.ReadFile(css)
		if err != nil {
			fmt.Fprintf(os.Stderr, "[CSS] 读取失败 %s: %v\n", css, err)
			continue
		}
		original := string(data)
		newText, removed, selectors := cleanCSSText(css, original)
		if removed == 0 {
			fmt.Printf("[CSS] %s: 无断链规则\n", filepath.Base(css))
			continue
		}
		if err := os.WriteFile(css+".bak", data, 0644); err != nil {
			fmt.Fprintf(os.Stderr, "[CSS] 备份失败 %s: %v，跳过重写\n", css, err)
			continue
		}
		if err := os.WriteFile(css, []byte(newText), 0644); err != nil {
			fmt.Fprintf(os.Stderr, "[CSS] 写入失败 %s: %v（备份在 %s.bak）\n", css, err, css)
			continue
		}
		totalRemoved += removed
		fmt.Printf("[CSS] %s: 已移除 %d 条断链规则（备份 %s.bak）\n", filepath.Base(css), removed, filepath.Base(css))
		for _, s := range selectors {
			if len(s) > 100 {
				s = s[:100] + "..."
			}
			fmt.Printf("    - %s\n", s)
		}
	}
	fmt.Printf("[CSS] 共移除 %d 条断链规则\n", totalRemoved)
}

// uniqueDest ensures a non-conflicting destination filename inside dir.
func uniqueDest(dir, name string) string {
	dst := filepath.Join(dir, name)
	if !fileExists(dst) {
		return dst
	}
	ext := filepath.Ext(name)
	base := strings.TrimSuffix(name, ext)
	for i := 1; ; i++ {
		cand := filepath.Join(dir, fmt.Sprintf("%s_%d%s", base, i, ext))
		if !fileExists(cand) {
			return cand
		}
	}
}

// moveFile renames src to dst, falling back to copy+remove for cross-device moves.
func moveFile(src, dst string) error {
	if err := os.Rename(src, dst); err == nil {
		return nil
	}
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.Create(dst)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		return err
	}
	if err := out.Close(); err != nil {
		return err
	}
	return os.Remove(src)
}

func confirm(prompt string) bool {
	fmt.Print(prompt + " [y/N] ")
	scanner := bufio.NewScanner(os.Stdin)
	if !scanner.Scan() {
		return false
	}
	s := strings.ToLower(strings.TrimSpace(scanner.Text()))
	return s == "y" || s == "yes"
}
