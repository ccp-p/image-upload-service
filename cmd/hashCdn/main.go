package main

import (
	"crypto/md5"
	"encoding/hex"
	"encoding/json"
	"encoding/xml"
	"flag"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

// 包级正则：编译一次，全局复用
var (
	reHashInFilename = regexp.MustCompile(`^(.+)\.([a-f0-9]{4,64})\.(css|js|jpg|jpeg|png|gif|svg|webp|ico)$`)
	reOldHashSuffix  = regexp.MustCompile(`\.[a-f0-9]{4,64}$`)
	reCSSUrlCollect  = regexp.MustCompile(`url\(['"]?([^'")\s]+)['"]?\)`)
	reCSSUrlReplace  = regexp.MustCompile(`url\(\s*(['"]?)([^'")\s]+)(['"]?)\s*\)`)
	reHTMLCSSLink    = regexp.MustCompile(`<link[^>]*href\s*=\s*['"]([^'"]+\.css(?:\?[^'"]*)?)['"]`)
	reHTMLJSScript   = regexp.MustCompile(`<script[^>]*src\s*=\s*['"]([^'"]+\.js(?:\?[^'"]*)?)['"]`)
	reHTMLComment    = regexp.MustCompile(`(?s)<!--.*?-->`)

	// CDN 全局替换用的正则（编译一次）
	reCDNCSS = regexp.MustCompile(`(<link[^>]*href\s*=\s*['"])([^'"]+)(['"][^>]*>)`)
	reCDNJS  = regexp.MustCompile(`(<script[^>]*src\s*=\s*['"])([^'"]+)(['"][^>]*>)`)

	// 动态正则缓存：避免同一 pattern 重复编译
	regexCache sync.Map
)

// getRegex 从缓存获取编译好的正则，未命中则编译并缓存
// 部署模式常量（消除 main() 中的魔法数字）
const (
	ModePreScriptCopy                = 1
	ModePreScriptCopyCommit          = 2
	ModePreScriptCopyCommitRollback  = 3
	ModeCopyNoCDN                    = 4
	ModeCopyCommitNoCDN              = 5
	ModeCopyCommitRollbackNoCDN      = 6
	ModeCopyExcludeCDN               = 7
	ModeCopyCommitExcludeCDN         = 8
	ModeCopyCommitRollbackExcludeCDN = 9
)

// isHomeEnv 判断当前是否为家庭环境（集中管理 IS_HOME 检查）
func isHomeEnv() bool {
	return os.Getenv("IS_HOME") == "1"
}

// vcsDestDelete 目标仓库删除旧 hash 文件的版本控制钩子。
// SVN 时代需要 svn delete 标记删除；Git 下普通 os.Remove 即可，
// 提交时 git add -A 会自动暂存删除，无需额外操作。
// 声明为 var 以便测试替换，验证删除旧 hash 文件时会经过此钩子。
var vcsDestDelete = func(filePath string, debugMode bool) {
	_, _ = filePath, debugMode
}

// initLogFile 初始化运行日志：本次运行的全部输出（stdout/stderr，含子进程）
// 同时写入 logs/ 下的时间戳日志文件，方便排查部署问题。
// 实现方式：os.Stdout/os.Stderr 重定向到管道，后台协程把管道内容
// 同步转发到控制台 + 日志文件（tee）。自动清理旧日志，仅保留最近 keep 个。
// 返回日志文件句柄（调用方 defer Close）
func initLogFile(keep int) (*os.File, error) {
	logDir := "logs"
	if err := os.MkdirAll(logDir, 0755); err != nil {
		return nil, err
	}
	name := filepath.Join(logDir, fmt.Sprintf("hashcdn_%s.log", time.Now().Format("20060102_150405")))
	f, err := os.OpenFile(name, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0644)
	if err != nil {
		return nil, err
	}
	cleanOldLogs(logDir, keep)

	console := os.Stdout
	r, w, err := os.Pipe()
	if err != nil {
		f.Close()
		return nil, err
	}
	os.Stdout = w
	os.Stderr = w
	go func() {
		buf := make([]byte, 8192)
		for {
			n, readErr := r.Read(buf)
			if n > 0 {
				_, _ = console.Write(buf[:n])
				_, _ = f.Write(buf[:n])
			}
			if readErr != nil {
				return
			}
		}
	}()
	fmt.Fprintf(f, "=== hashcdn 运行日志 %s ===\n", time.Now().Format("2006-01-02 15:04:05"))
	fmt.Fprintf(f, "命令行: %s\n\n", strings.Join(os.Args, " "))
	return f, nil
}

// cleanOldLogs 清理旧日志文件，仅保留最近 keep 个（文件名含时间戳，字典序即时间序）
func cleanOldLogs(logDir string, keep int) {
	entries, err := os.ReadDir(logDir)
	if err != nil {
		return
	}
	var logs []string
	for _, e := range entries {
		if !e.IsDir() && strings.HasPrefix(e.Name(), "hashcdn_") && strings.HasSuffix(e.Name(), ".log") {
			logs = append(logs, e.Name())
		}
	}
	if len(logs) <= keep {
		return
	}
	sort.Strings(logs)
	for _, name := range logs[:len(logs)-keep] {
		os.Remove(filepath.Join(logDir, name))
	}
}

// vcsGitAdd 执行 git add，消除 VersionManager 中的重复实现
func vcsGitAdd(filePath string, debugMode bool) {
	if _, err := exec.LookPath("git"); err != nil {
		return
	}
	cmd := exec.Command("git", "add", filepath.Base(filePath))
	cmd.Dir = filepath.Dir(filePath)
	if output, err := cmd.CombinedOutput(); err != nil {
		if debugMode {
			fmt.Printf("      ⚠️  Git add 失败: %s (%v)\n", filepath.Base(filePath), err)
			fmt.Printf("      Output: %s\n", string(output))
		}
	} else if debugMode {
		fmt.Printf("    ➕ Git add: %s\n", filepath.Base(filePath))
	}
}

func getRegex(pattern string) *regexp.Regexp {
	if v, ok := regexCache.Load(pattern); ok {
		return v.(*regexp.Regexp)
	}
	re := regexp.MustCompile(pattern)
	regexCache.Store(pattern, re)
	return re
}

// logf 统一的日志输出，非 debug 模式下静默
func (vm *VersionManager) logf(format string, args ...interface{}) {
	if vm.debugMode {
		fmt.Printf(format, args...)
	}
}

// isJSOrCSS 检查文件是否是JS或CSS
func isJSOrCSS(filename string) bool {
	lower := strings.ToLower(filename)
	return strings.HasSuffix(lower, ".js") || strings.HasSuffix(lower, ".css")
}

// logFile 仅JS/CSS文件的日志输出（非debug模式静默）
func (vm *VersionManager) logFile(filename, format string, args ...interface{}) {
	if vm.debugMode && isJSOrCSS(filename) {
		fmt.Printf(format, args...)
	}
}

// Config 配置结构
type Config struct {
	RootDir        string   `json:"rootDir"`
	CDNDomain      string   `json:"cdnDomain"`
	HashLength     int      `json:"hashLength"`
	SingleHTMLFile string   `json:"singleHTMLFile"` // 单个HTML文件路径
	HTMLFiles      []string `json:"htmlFiles"`
	ExcludeDirs    []string `json:"excludeDirs"`
	// 环境相关配置
	HomeHTMLFile    string `json:"homeHTMLFile"`    // 家里电脑的HTML文件路径
	CompanyHTMLFile string `json:"companyHTMLFile"` // 公司电脑的HTML文件路径
	// 新增：指定要处理的组件
	IncludeComponents []string `json:"includeComponents"` // 只处理指定的组件
	// 新增：指定哪些HTML文件需要处理主资源
	ProcessMainResources []string `json:"processMainResources"`
	ExtraHashResources   []string `json:"extraHashResources"` // 额外需要hash的资源（非components路径的共享脚本，如utils_index.js）
	ReplaceAllWithCDN    bool     `json:"replaceAllWithCDN"`  // 替换所有资源为CDN路径
	ObfuscateJS          bool     `json:"obfuscateJS"`        // 实验性：对JS文件进行混淆（minify+mangle）
	NoHash               bool     `json:"-"`                  // 运行时开关（mode 16/17）：CDN替换但跳过hash，部署只复制原始文件
	// 新增：部署相关配置
	RollbackAfterDeploy    bool         `json:"rollbackAfterDeploy"`    // 部署后回滚HTML
	GitCommitAfterRollback bool         `json:"gitCommitAfterRollback"` // 回滚后执行git commit和push
	CDNExcludeFiles        []string     `json:"cdnExcludeFiles"`        // CDN替换排除的文件列表
	Deploy                 DeployConfig `json:"deploy"`                 // 部署配置
}

// DeployConfig 部署配置
type DeployConfig struct {
	Enabled           bool     `json:"enabled"`
	Command           string   `json:"command"` // copy 或 copy-commit
	AutoCommit        bool     `json:"autoCommit"`
	HomeSourcePath    string   `json:"homeSourcePath"`
	HomeDestPath      string   `json:"homeDestPath"`
	CompanySourcePath string   `json:"companySourcePath"`
	CompanyDestPath   string   `json:"companyDestPath"`
	FilePaths         []string `json:"filePaths"`
	GitAuthors        []string `json:"gitAuthors"`
	CDNPathPrefix     string   `json:"cdnPathPrefix"` // 新增：CDN URL中需要裁掉的前缀映射，例如 /2016tyjf/xhmqqthy/res/wap/
	// 现网生产环境 HTML 地址：每次部署实时抓取，其引用的 hash 版本必须保留
	ProdHTMLURLs      []string `json:"prodHtmlUrls"`
	ForcePreScript    bool     `json:"-"`                 // 运行时覆盖：是否强制执行前置脚本
	HomeNodeScript    string   `json:"homeNodeScript"`    // 家里电脑的Node.js部署脚本路径
	CompanyNodeScript string   `json:"companyNodeScript"` // 公司电脑的Node.js部署脚本路径
}

// VersionManager 版本管理器
type VersionManager struct {
	config         Config
	processedFiles map[string]string // key=文件路径, value=hash值（缓存避免重复计算）
	mu             sync.Mutex
	debugMode      bool
	folderOpened   bool                // 记录文件夹是否已打开
	commitMessage  string              // 自定义提交信息
	excludeMap     map[string]struct{} // 预构建的 CDN 排除文件 map
}

// FileInfo 文件信息
type FileInfo struct {
	OriginalPath string
	HashedPath   string
	Hash         string
	Renamed      bool
}

// ImageReference 图片引用信息
type ImageReference struct {
	OriginalPath string
	AbsolutePath string
	RelativePath string
}

// NewVersionManager 创建版本管理器
func NewVersionManager(config Config, debugMode bool) *VersionManager {
	vm := &VersionManager{
		config:         config,
		processedFiles: make(map[string]string),
		debugMode:      debugMode,
		folderOpened:   false,
		excludeMap:     make(map[string]struct{}, len(config.CDNExcludeFiles)),
	}
	// 预构建 CDN 排除文件 map
	for _, f := range config.CDNExcludeFiles {
		vm.excludeMap[f] = struct{}{}
	}
	return vm
}

// shouldProcessComponent 检查是否应该处理指定组件
func (vm *VersionManager) shouldProcessComponent(componentPath string) bool {
	// 如果没有配置包含的组件列表，则处理所有组件
	if len(vm.config.IncludeComponents) == 0 {
		return true
	}

	// 检查组件路径是否匹配任何指定的组件
	for _, componentName := range vm.config.IncludeComponents {
		// 检查路径中是否包含该组件名
		if strings.Contains(componentPath, "/"+componentName+"/") ||
			strings.Contains(componentPath, "\\"+componentName+"\\") ||
			strings.HasSuffix(componentPath, "/"+componentName) ||
			strings.HasSuffix(componentPath, "\\"+componentName) ||
			strings.HasPrefix(filepath.Base(componentPath), componentName+".") {
			return true
		}
	}

	return false
}

// calculateFileHash 计算文件hash
func (vm *VersionManager) calculateFileHash(filePath string) (string, error) {
	hashString, err := getFileHash(filePath)
	if err != nil {
		return "", err
	}
	if vm.config.HashLength > 0 && vm.config.HashLength < len(hashString) {
		return hashString[:vm.config.HashLength], nil
	}

	return hashString, nil
}

// removeHashFromFilename 从文件名中移除hash
func (vm *VersionManager) removeHashFromFilename(filename string) string {
	matches := reHashInFilename.FindStringSubmatch(filename)
	if len(matches) == 4 {
		return matches[1] + "." + matches[3]
	}
	return filename
}

// addHashToFilename 给文件名添加hash
func (vm *VersionManager) addHashToFilename(filename, hash string) string {
	ext := filepath.Ext(filename)
	basename := strings.TrimSuffix(filename, ext)
	cleanBasename := reOldHashSuffix.ReplaceAllString(basename, "")
	return fmt.Sprintf("%s.%s%s", cleanBasename, hash, ext)
}

// findAndDeleteOldHashFiles 清理 src 中同 basename 的旧 hash 文件。
// src 只服务构建，不服务线上缓存；旧版本保护由 dest 的现网/基线名单负责。
func (vm *VersionManager) findAndDeleteOldHashFiles(dir, basename, ext, currentHash string) error {
	if isJSOrCSS(basename + ext) {
		vm.logf("  🔍 查找旧hash文件: %s%s (当前hash: %s)\n", basename, ext, currentHash)
	}

	hashPattern := fmt.Sprintf(`^%s\.([a-f0-9]{4,64})%s$`, regexp.QuoteMeta(basename), regexp.QuoteMeta(ext))
	re := getRegex(hashPattern)

	files, err := os.ReadDir(dir)
	if err != nil {
		return err
	}

	for _, file := range files {
		if file.IsDir() {
			continue
		}
		filename := file.Name()
		hashMatches := re.FindStringSubmatch(filename)
		if len(hashMatches) >= 2 && hashMatches[1] != currentHash {
			oldPath := filepath.Join(dir, filename)
			if err := os.Remove(oldPath); err != nil {
				if vm.debugMode {
					fmt.Printf("    ⚠️  清理src旧hash失败: %s: %v\n", filename, err)
				}
				continue
			}
			if isJSOrCSS(filename) {
				fmt.Printf("    🧹 已清理src旧hash: %s\n", filename)
			}
		}
	}

	return nil
}

// processHTMLFile 处理单个HTML文件及其关联资源
func (vm *VersionManager) processHTMLFile(htmlPath string) error {
	if vm.config.NoHash {
		return vm.processHTMLFileNoHash(htmlPath)
	}
	fmt.Println(strings.Repeat("=", 60))
	fmt.Printf("📄 处理: %s\n", htmlPath)
	fmt.Println(strings.Repeat("=", 60))

	if !fileExists(htmlPath) {
		return fmt.Errorf("文件不存在: %s", htmlPath)
	}

	htmlDir := filepath.Dir(htmlPath)
	htmlBasename := strings.TrimSuffix(filepath.Base(htmlPath), ".html")

	// 判断是否需要处理主资源
	shouldProcessMain := false
	if len(vm.config.ProcessMainResources) > 0 {
		for _, name := range vm.config.ProcessMainResources {
			if name == filepath.Base(htmlPath) || name == htmlBasename {
				shouldProcessMain = true
				break
			}
		}
	}

	if shouldProcessMain {
		fmt.Printf("🎯 策略: 处理主资源 (JS/CSS) 及组件\n")
	} else {
		fmt.Printf("🎯 策略: 仅处理组件资源 (跳过主JS/CSS)\n")
	}

	resources := map[string]map[string]string{
		"css": make(map[string]string),
		"js":  make(map[string]string),
	}

	// 1. 处理主JS文件
	if shouldProcessMain {
		fmt.Println("\n📦 处理主 JavaScript 文件...")

		jsPaths := []string{
			filepath.Join(htmlDir, htmlBasename+".js"),
			filepath.Join(htmlDir, "js", htmlBasename+".js"),
			filepath.Join(htmlDir, "scripts", "js", htmlBasename+".js"),
		}

		mainJsFound := false
		for _, jsPath := range jsPaths {
			actualJsPath := vm.findFile(jsPath)
			if actualJsPath != "" {
				info, err := vm.renameFileWithHash(actualJsPath)
				if err != nil {
					fmt.Printf("  ❌ 处理失败: %v\n", err)
					continue
				}

				relPath, _ := filepath.Rel(htmlDir, actualJsPath)
				relPath = filepath.ToSlash(relPath)

				hashedRelPath, _ := filepath.Rel(htmlDir, info.HashedPath)
				hashedRelPath = filepath.ToSlash(hashedRelPath)

				normalizedKey := strings.TrimPrefix(relPath, "./")
				if _, exists := resources["js"][normalizedKey]; !exists {
					resources["js"][normalizedKey] = hashedRelPath
				}

				mainJsFound = true
				break
			}
		}

		if !mainJsFound {
			fmt.Printf("  ℹ️  未找到主JS文件\n")
		}
	} else {
		fmt.Println("\n📦 跳过主 JavaScript 文件")
	}

	// 2. 处理主CSS文件
	if shouldProcessMain {
		fmt.Println("\n🎨 处理主 CSS 文件...")

		cssPaths := []string{
			filepath.Join(htmlDir, htmlBasename+".css"),
			filepath.Join(htmlDir, "css", htmlBasename+".css"),
		}

		mainCssFound := false
		for _, cssPath := range cssPaths {
			actualCssPath := vm.findFile(cssPath)
			if actualCssPath != "" {
				info, err := vm.processComponentCSS(actualCssPath)
				if err != nil {
					fmt.Printf("  ❌ 处理失败: %v\n", err)
					continue
				}

				relPath, _ := filepath.Rel(htmlDir, actualCssPath)
				relPath = filepath.ToSlash(relPath)

				hashedRelPath, _ := filepath.Rel(htmlDir, info.HashedPath)
				hashedRelPath = filepath.ToSlash(hashedRelPath)

				normalizedKey := strings.TrimPrefix(relPath, "./")
				if _, exists := resources["css"][normalizedKey]; !exists {
					resources["css"][normalizedKey] = hashedRelPath
				}

				mainCssFound = true
				break
			}
		}

		if !mainCssFound {
			fmt.Printf("  ℹ️  未找到主CSS文件\n")
		}
	} else {
		fmt.Println("\n🎨 跳过主 CSS 文件")
	}

	// 2.5 处理额外 hash 资源（非 components 路径的共享脚本，如 utils_index.js）
	if shouldProcessMain && len(vm.config.ExtraHashResources) > 0 {
		fmt.Println("\n📦 处理额外 hash 资源...")
		for _, relPath := range vm.config.ExtraHashResources {
			cleanRel := strings.ReplaceAll(relPath, "\\", "/")
			abs := filepath.Join(htmlDir, cleanRel)
			target := abs
			if actual := vm.findFile(abs); actual != "" {
				target = actual
			}
			if !fileExists(target) {
				fmt.Printf("  ⚠️ 未找到: %s\n", cleanRel)
				continue
			}
			rel, _ := filepath.Rel(htmlDir, target)
			rel = filepath.ToSlash(rel)
			normalizedKey := strings.TrimPrefix(rel, "./")
			if _, exists := resources["js"][normalizedKey]; exists {
				continue
			}
			info, err := vm.renameFileWithHash(target)
			if err != nil {
				fmt.Printf("  ⚠️ 处理失败 %s: %v\n", cleanRel, err)
				continue
			}
			fmt.Printf("  ✅ JS: %s -> %s\n", filepath.Base(target), filepath.Base(info.HashedPath))
			hashedRel, _ := filepath.Rel(htmlDir, info.HashedPath)
			hashedRel = filepath.ToSlash(hashedRel)
			resources["js"][normalizedKey] = hashedRel
		}
	}

	// 3. 收集并处理组件资源
	fmt.Println("\n🔍 扫描组件资源...")
	htmlResources, err := vm.collectResourcesFromHTML(htmlPath)
	if err != nil {
		return fmt.Errorf("扫描HTML失败: %v", err)
	}

	fmt.Printf("  找到 %d 个组件CSS, %d 个组件JS\n", len(htmlResources["css"]), len(htmlResources["js"]))

	// 4. 处理组件JS文件
	if len(htmlResources["js"]) > 0 {
		fmt.Println("\n🔧 处理组件 JavaScript 文件...")
		for _, jsRelPath := range htmlResources["js"] {
			normalizedKey := strings.TrimPrefix(strings.ReplaceAll(jsRelPath, "\\", "/"), "./")
			if _, exists := resources["js"][normalizedKey]; exists {
				continue
			}

			info, err := vm.processComponentResource(htmlDir, jsRelPath)
			if err != nil {
				fmt.Printf("  ❌ 失败: %s\n", jsRelPath)
				continue
			}

			hashedRelPath, _ := filepath.Rel(htmlDir, info.HashedPath)
			hashedRelPath = filepath.ToSlash(hashedRelPath)

			resources["js"][normalizedKey] = hashedRelPath
		}
	}

	// 5. 处理组件CSS文件
	if len(htmlResources["css"]) > 0 {
		fmt.Println("\n🔧 处理组件 CSS 文件...")
		for _, cssRelPath := range htmlResources["css"] {
			normalizedKey := strings.TrimPrefix(strings.ReplaceAll(cssRelPath, "\\", "/"), "./")
			if _, exists := resources["css"][normalizedKey]; exists {
				continue
			}

			info, err := vm.processComponentResource(htmlDir, cssRelPath)
			if err != nil {
				fmt.Printf("  ❌ 失败: %s\n", cssRelPath)
				continue
			}

			hashedRelPath, _ := filepath.Rel(htmlDir, info.HashedPath)
			hashedRelPath = filepath.ToSlash(hashedRelPath)

			resources["css"][normalizedKey] = hashedRelPath
		}
	}

	// 6. 更新HTML中的引用
	fmt.Println("\n🔄 更新HTML中的资源引用...")
	fmt.Printf("  📋 CSS: %d 项, JS: %d 项\n", len(resources["css"]), len(resources["js"]))

	if err := vm.updateHTMLReferences(htmlPath, resources); err != nil {
		return fmt.Errorf("更新HTML失败: %v", err)
	}

	fmt.Println("\n✨ 处理完成!")
	return nil
}

// processHTMLFileNoHash CDN替换但跳过hash的模式（mode 16/17）：
// 不生成、不清理 hash 文件，仅将 HTML 中的相对 css/js 引用替换为
// CDN 域名 + 原始文件名（复用 updateHTMLContent 的剩余资源 CDN 替换阶段，
// resources 传空即只触发该阶段）。部署侧由 copyBaseFileOnly 只复制原始文件。
func (vm *VersionManager) processHTMLFileNoHash(htmlPath string) error {
	fmt.Println(strings.Repeat("=", 60))
	fmt.Printf("📄 处理(CDN无hash): %s\n", htmlPath)
	fmt.Println(strings.Repeat("=", 60))

	if !fileExists(htmlPath) {
		return fmt.Errorf("文件不存在: %s", htmlPath)
	}
	if vm.config.CDNDomain == "" {
		fmt.Println("ℹ️  CDN域名为空，HTML无需修改")
		return nil
	}

	resources := map[string]map[string]string{
		"css": {},
		"js":  {},
	}
	if err := vm.updateHTMLContent(htmlPath, resources); err != nil {
		return fmt.Errorf("更新HTML失败: %v", err)
	}

	fmt.Println("\n✨ CDN替换完成（未生成hash文件）!")
	return nil
}

// processMultipleHTMLFiles 批量处理多个HTML文件
func (vm *VersionManager) processMultipleHTMLFiles(htmlPaths []string) {
	fmt.Println("🚀 开始批量处理HTML文件...")

	for _, htmlPath := range htmlPaths {
		absolutePath := filepath.Join(vm.config.RootDir, htmlPath)
		if err := vm.processHTMLFile(absolutePath); err != nil {
			fmt.Printf("❌ 处理失败 %s: %v\n", htmlPath, err)
		}
	}

	fmt.Println("\n" + strings.Repeat("=", 60))
	fmt.Println("🎉 全部处理完成！")
	fmt.Println(strings.Repeat("=", 60))
}

// findAllHTMLFiles 扫描目录查找所有HTML文件
func (vm *VersionManager) findAllHTMLFiles() []string {
	// 预构建排除目录 map
	excludeDirs := make(map[string]struct{}, len(vm.config.ExcludeDirs))
	for _, d := range vm.config.ExcludeDirs {
		excludeDirs[d] = struct{}{}
	}

	var htmlFiles []string

	err := filepath.WalkDir(vm.config.RootDir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}

		if d.IsDir() {
			if _, ok := excludeDirs[d.Name()]; ok {
				return filepath.SkipDir
			}
			return nil
		}

		if filepath.Ext(path) == ".html" {
			relPath, _ := filepath.Rel(vm.config.RootDir, path)
			htmlFiles = append(htmlFiles, relPath)
		}

		return nil
	})

	if err != nil {
		fmt.Printf("⚠️  扫描目录失败: %v\n", err)
	}

	return htmlFiles
}

// 辅助函数

func fileExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

func copyFile(src, dst string) error {
	srcFile, err := os.Open(src)
	if err != nil {
		return err
	}
	defer srcFile.Close()

	info, err := srcFile.Stat()
	if err != nil {
		return err
	}

	dstFile, err := os.OpenFile(dst, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, info.Mode())
	if err != nil {
		return err
	}
	defer dstFile.Close()

	_, err = io.Copy(dstFile, srcFile)
	return err
}

// fileCopyResult 文件复制结果
type fileCopyResult struct {
	copied  int
	skipped int
	failed  int
}

// runConcurrent 并发执行任务并收集结果
func runConcurrent[T any](items []T, maxWorkers int, handler func(T) fileCopyResult) []fileCopyResult {
	if len(items) == 0 {
		return nil
	}
	if maxWorkers > len(items) {
		maxWorkers = len(items)
	}

	jobs := make(chan T, len(items))
	results := make(chan fileCopyResult, len(items))

	var wg sync.WaitGroup
	for i := 0; i < maxWorkers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for item := range jobs {
				results <- handler(item)
			}
		}()
	}

	for _, item := range items {
		jobs <- item
	}
	close(jobs)

	go func() {
		wg.Wait()
		close(results)
	}()

	var all []fileCopyResult
	for r := range results {
		all = append(all, r)
	}
	return all
}

// renameFileWithHash 重命名文件（如果hash改变）
func (vm *VersionManager) renameFileWithHash(filePath string) (*FileInfo, error) {
	dir := filepath.Dir(filePath)
	filename := filepath.Base(filePath)
	cleanFilename := vm.removeHashFromFilename(filename)

	// 确定源文件路径（优先使用无hash的原始文件）
	cleanPath := filepath.Join(dir, cleanFilename)
	sourcePath := filePath
	if fileExists(cleanPath) {
		sourcePath = cleanPath
	}

	// 对JS文件进行实验性混淆（minify+mangle），hash基于混淆后内容
	var processedContent []byte // 非 nil 表示内容已被变换（混淆），需直接写入而非 copyFile
	isJS := strings.HasSuffix(strings.ToLower(cleanFilename), ".js")

	var hash string
	if vm.config.ObfuscateJS && isJS {
		raw, readErr := os.ReadFile(sourcePath)
		if readErr != nil {
			return nil, readErr
		}
		obf, obfErr := obfuscateJS(raw)
		if obfErr != nil {
			fmt.Printf("  ⚠️  混淆失败 %s: %v（使用原始内容）\n", cleanFilename, obfErr)
			processedContent = raw
		} else {
			processedContent = obf
			fmt.Printf("  🔒 混淆: %s (%d -> %d bytes)\n", cleanFilename, len(raw), len(obf))
		}
		hash = hashFromBytes(processedContent, vm.config.HashLength)
	} else {
		h, err := vm.calculateFileHash(sourcePath)
		if err != nil {
			return nil, err
		}
		hash = h
	}

	newFilename := vm.addHashToFilename(cleanFilename, hash)
	newPath := filepath.Join(dir, newFilename)

	info := &FileInfo{
		OriginalPath: sourcePath,
		HashedPath:   newPath,
		Hash:         hash,
		Renamed:      true,
	}

	// 检查目标文件是否已存在
	if fileExists(newPath) {
		// 目标文件已存在，直接跳过
		if vm.debugMode && isJSOrCSS(newFilename) {
			fmt.Printf("  ⏭️  跳过（已存在）: %s\n", newFilename)
		}

		// 删除旧的hash文件（排除当前hash）
		ext := filepath.Ext(cleanFilename)
		basename := strings.TrimSuffix(cleanFilename, ext)
		if err := vm.findAndDeleteOldHashFiles(dir, basename, ext, hash); err != nil {
			if vm.debugMode {
				fmt.Printf("  ⚠️  清理旧文件时出错: %v\n", err)
			}
		}

		return info, nil
	}

	// 写入新文件：混淆内容直接写入，否则复制源文件
	if processedContent != nil {
		if err := os.WriteFile(newPath, processedContent, 0644); err != nil {
			return nil, fmt.Errorf("写入混淆文件失败: %v", err)
		}
	} else {
		if err := copyFile(sourcePath, newPath); err != nil {
			return nil, fmt.Errorf("复制文件失败: %v", err)
		}
	}

	vcsGitAdd(newPath, vm.debugMode) // 自动添加到git

	if isJSOrCSS(newFilename) {
		fmt.Printf("  ✅ 已生成: %s\n", newFilename)
	}

	// 删除旧的hash文件
	ext := filepath.Ext(cleanFilename)
	basename := strings.TrimSuffix(cleanFilename, ext)
	if err := vm.findAndDeleteOldHashFiles(dir, basename, ext, hash); err != nil {
		if vm.debugMode {
			fmt.Printf("  ⚠️  清理旧文件时出错: %v\n", err)
		}
	}

	return info, nil
}

// collectImagesFromCSS 收集CSS中引用的所有图片
func (vm *VersionManager) collectImagesFromCSS(cssPath string) ([]ImageReference, error) {
	content, err := os.ReadFile(cssPath)
	if err != nil {
		return nil, err
	}

	cssDir := filepath.Dir(cssPath)
	var images []ImageReference

	matches := reCSSUrlCollect.FindAllStringSubmatch(string(content), -1)

	for _, match := range matches {
		if len(match) < 2 {
			continue
		}

		imagePath := match[1]

		if strings.HasPrefix(imagePath, "http") ||
			strings.HasPrefix(imagePath, "data:") ||
			strings.HasPrefix(imagePath, "//") {
			continue
		}

		imagePath = strings.Split(imagePath, "?")[0]
		imagePath = strings.Split(imagePath, "#")[0]

		absolutePath := filepath.Join(cssDir, filepath.FromSlash(imagePath))
		absolutePath = filepath.Clean(absolutePath)

		if fileExists(absolutePath) {
			relativePath, _ := filepath.Rel(cssDir, absolutePath)
			images = append(images, ImageReference{
				OriginalPath: imagePath,
				AbsolutePath: absolutePath,
				RelativePath: relativePath,
			})
		}
	}

	return images, nil
}

// updateCSSImageReferences 更新CSS文件中的图片引用 - 只更新指定的CSS文件
// imageMap 的 key 是原始CSS中的路径（如 ../images/pic.png），value 是新的带hash的文件名
func (vm *VersionManager) updateCSSImageReferences(cssPath string, imageMap map[string]string) error {
	content, err := os.ReadFile(cssPath)
	if err != nil {
		return err
	}

	contentStr := string(content)
	updated := false

	// 预归一化 imageMap 的 key（优化点3：避免循环内重复 ReplaceAll）
	normalizedMap := make(map[string]string, len(imageMap))
	for k, v := range imageMap {
		normalizedMap[strings.ReplaceAll(k, "\\", "/")] = v
	}

	newContent := reCSSUrlReplace.ReplaceAllStringFunc(contentStr, func(match string) string {
		submatches := reCSSUrlReplace.FindStringSubmatch(match)
		if len(submatches) < 4 {
			return match
		}

		openingQuote := submatches[1]
		originalPath := submatches[2]
		closingQuote := submatches[3]

		if strings.HasPrefix(originalPath, "http") ||
			strings.HasPrefix(originalPath, "data:") ||
			strings.HasPrefix(originalPath, "//") {
			return match
		}

		cleanPath := strings.Split(originalPath, "?")[0]
		cleanPath = strings.Split(cleanPath, "#")[0]
		normalizedPath := strings.ReplaceAll(cleanPath, "\\", "/")

		// O(1) 查找
		newFilename, found := normalizedMap[normalizedPath]
		if !found {
			return match
		}

		dir := filepath.Dir(originalPath)
		dir = strings.ReplaceAll(dir, "\\", "/")

		var newPath string
		if dir == "." {
			newPath = newFilename
		} else {
			newPath = dir + "/" + newFilename
		}

		if openingQuote != closingQuote {
			if openingQuote != "" && closingQuote == "" {
				closingQuote = openingQuote
			} else if openingQuote == "" && closingQuote != "" {
				openingQuote = closingQuote
			}
		}

		result := fmt.Sprintf("url(%s%s%s)", openingQuote, newPath, closingQuote)

		if match != result {
			updated = true
			if isJSOrCSS(filepath.Base(filepath.Base(originalPath))) {
				vm.logf("    🔄 图片 %s -> %s\n", filepath.Base(originalPath), newFilename)
			}
		}

		return result
	})

	contentStr = newContent

	if updated {
		return os.WriteFile(cssPath, []byte(contentStr), 0644)
	}

	return nil
}

// findFile 查找文件（支持带hash版本）
func (vm *VersionManager) findFile(basePath string) string {
	if fileExists(basePath) {
		return basePath
	}

	dir := filepath.Dir(basePath)
	name := filepath.Base(basePath)
	ext := filepath.Ext(name)
	nameWithoutExt := strings.TrimSuffix(name, ext)

	if !fileExists(dir) {
		return ""
	}

	files, err := os.ReadDir(dir)
	if err != nil {
		return ""
	}

	pattern := getRegex(fmt.Sprintf(`^%s\.[a-f0-9]{8}%s$`, regexp.QuoteMeta(nameWithoutExt), regexp.QuoteMeta(ext)))

	for _, file := range files {
		if pattern.MatchString(file.Name()) {
			return filepath.Join(dir, file.Name())
		}
	}

	return ""
}

// collectResourcesFromHTML 从HTML中收集所有资源引用（包括组件）
func (vm *VersionManager) collectResourcesFromHTML(htmlPath string) (map[string][]string, error) {
	content, err := os.ReadFile(htmlPath)
	if err != nil {
		return nil, err
	}

	htmlBasename := strings.TrimSuffix(filepath.Base(htmlPath), ".html")

	shouldProcessMain := false
	for _, name := range vm.config.ProcessMainResources {
		if name == filepath.Base(htmlPath) || name == htmlBasename {
			shouldProcessMain = true
			break
		}
	}

	resources := map[string][]string{
		"css": {},
		"js":  {},
	}

	contentStr := string(content)

	// 收集CSS文件
	cssMatches := reHTMLCSSLink.FindAllStringSubmatch(contentStr, -1)
	for _, match := range cssMatches {
		if len(match) >= 2 {
			cssPath := match[1]
			// 移除查询参数（如 ?v=202607107）
			if idx := strings.Index(cssPath, "?"); idx != -1 {
				cssPath = cssPath[:idx]
			}
			isExternal := strings.HasPrefix(cssPath, "http") || strings.HasPrefix(cssPath, "//")

			if isExternal {
				if shouldProcessMain || !strings.Contains(cssPath, "components") {
					continue
				}
			} else if !strings.Contains(cssPath, "components") {
				continue
			}

			if !vm.shouldProcessComponent(cssPath) {
				continue
			}

			resources["css"] = append(resources["css"], cssPath)
		}
	}

	// 收集JS文件
	jsMatches := reHTMLJSScript.FindAllStringSubmatch(contentStr, -1)
	for _, match := range jsMatches {
		if len(match) >= 2 {
			jsPath := match[1]
			// 移除查询参数（如 ?v=202607107）
			if idx := strings.Index(jsPath, "?"); idx != -1 {
				jsPath = jsPath[:idx]
			}
			isExternal := strings.HasPrefix(jsPath, "http") || strings.HasPrefix(jsPath, "//")

			if isExternal {
				if shouldProcessMain || !strings.Contains(jsPath, "components") {
					continue
				}
			} else if !strings.Contains(jsPath, "components") {
				continue
			}

			if !vm.shouldProcessComponent(jsPath) {
				continue
			}

			resources["js"] = append(resources["js"], jsPath)
		}
	}

	return resources, nil
}

// processComponentResource 处理组件资源（JS或CSS）
func (vm *VersionManager) processComponentResource(htmlDir, relativePath string) (*FileInfo, error) {
	targetPath := relativePath
	if strings.HasPrefix(relativePath, "http") || strings.HasPrefix(relativePath, "//") {
		idx := strings.Index(relativePath, "components/")
		if idx != -1 {
			targetPath = relativePath[idx:]
		}
	}

	// 移除查询参数（如 ?v=202607107）用于文件路径解析
	if idx := strings.Index(targetPath, "?"); idx != -1 {
		targetPath = targetPath[:idx]
	}

	absolutePath := filepath.Join(htmlDir, filepath.FromSlash(targetPath))
	absolutePath = filepath.Clean(absolutePath)

	actualPath := vm.findFile(absolutePath)
	if actualPath == "" {
		actualPath = absolutePath
	}

	if !fileExists(actualPath) {
		return nil, fmt.Errorf("文件不存在: %s", actualPath)
	}

	// 检查是否已经处理过（使用缓存的 hash，避免重复计算）
	vm.mu.Lock()
	if cachedHash, ok := vm.processedFiles[actualPath]; ok {
		vm.mu.Unlock()
		dir := filepath.Dir(actualPath)
		filename := filepath.Base(actualPath)
		cleanFilename := vm.removeHashFromFilename(filename)
		hashedFilename := vm.addHashToFilename(cleanFilename, cachedHash)
		hashedPath := filepath.Join(dir, hashedFilename)
		return &FileInfo{
			OriginalPath: actualPath,
			HashedPath:   hashedPath,
			Hash:         cachedHash,
			Renamed:      true,
		}, nil
	}
	vm.processedFiles[actualPath] = "" // 占位，防止递归处理
	vm.mu.Unlock()

	// 处理CSS文件时，先处理其中的图片引用
	if strings.HasSuffix(strings.ToLower(actualPath), ".css") {
		info, err := vm.processComponentCSS(actualPath)
		if err == nil {
			vm.mu.Lock()
			vm.processedFiles[actualPath] = info.Hash
			vm.mu.Unlock()
		}
		return info, err
	}

	// 处理JS文件
	info, err := vm.renameFileWithHash(actualPath)
	if err == nil {
		vm.mu.Lock()
		vm.processedFiles[actualPath] = info.Hash
		vm.mu.Unlock()
	}
	return info, err
}

// processComponentCSS 处理组件CSS文件（包括其中的图片）
func (vm *VersionManager) processComponentCSS(cssPath string) (*FileInfo, error) {
	cssDir := filepath.Dir(cssPath)
	filename := filepath.Base(cssPath)
	cleanFilename := vm.removeHashFromFilename(filename)

	// 确保使用原始CSS文件
	originalCssPath := filepath.Join(cssDir, cleanFilename)
	if !fileExists(originalCssPath) {
		originalCssPath = cssPath
	}

	if vm.debugMode {
		fmt.Printf("    📝 处理CSS: %s\n", cleanFilename)
	}

	// 收集并处理CSS中的图片
	images, err := vm.collectImagesFromCSS(originalCssPath)
	if err != nil {
		return nil, err
	}

	// imageMap 的 key 使用原始CSS中的相对路径，value 是新的带hash的文件名
	imageMap := make(map[string]string)

	if len(images) > 0 {
		fmt.Printf("    📸 处理 %d 个图片引用\n", len(images))

		for _, image := range images {
			originalPathKey := strings.ReplaceAll(image.OriginalPath, "\\", "/")

			vm.mu.Lock()
			if cachedHash, ok := vm.processedFiles[image.AbsolutePath]; ok {
				vm.mu.Unlock()
				// 使用缓存的 hash，避免重复读文件计算
				if cachedHash != "" {
					dir := filepath.Dir(image.AbsolutePath)
					cleanImageFilename := vm.removeHashFromFilename(filepath.Base(image.AbsolutePath))
					newImageFilename := vm.addHashToFilename(cleanImageFilename, cachedHash)
					hashedPath := filepath.Join(dir, newImageFilename)
					if fileExists(hashedPath) {
						imageMap[originalPathKey] = newImageFilename
					} else {
						actualHashedFile := vm.findFile(filepath.Join(dir, cleanImageFilename))
						if actualHashedFile != "" {
							imageMap[originalPathKey] = filepath.Base(actualHashedFile)
						}
					}
				} else {
					// hash 尚未计算完成（正在被另一个路径处理），回退到重算
					hash, err := vm.calculateFileHash(image.AbsolutePath)
					if err != nil {
						continue
					}
					dir := filepath.Dir(image.AbsolutePath)
					cleanImageFilename := vm.removeHashFromFilename(filepath.Base(image.AbsolutePath))
					newImageFilename := vm.addHashToFilename(cleanImageFilename, hash)
					if fileExists(filepath.Join(dir, newImageFilename)) {
						imageMap[originalPathKey] = newImageFilename
					}
				}
				continue
			}
			vm.processedFiles[image.AbsolutePath] = "" // 占位
			vm.mu.Unlock()

			info, err := vm.renameFileWithHash(image.AbsolutePath)
			if err != nil {
				fmt.Printf("      ⚠️  失败: %s (%v)\n", filepath.Base(image.AbsolutePath), err)
				continue
			}

			newImageFilename := filepath.Base(info.HashedPath)
			imageMap[originalPathKey] = newImageFilename

			// 缓存图片 hash
			vm.mu.Lock()
			vm.processedFiles[image.AbsolutePath] = info.Hash
			vm.mu.Unlock()

			if isJSOrCSS(newImageFilename) {
				vm.logf("      📎 映射: %s -> %s\n", originalPathKey, newImageFilename)
			}
			// 移除: relPath, _ := filepath.Rel(vm.config.RootDir, image.AbsolutePath)
			// 移除: vm.versionMap[relPath] = info.Hash
		}
	}

	// 计算原始CSS的hash
	originalHash, err := vm.calculateFileHash(originalCssPath)
	if err != nil {
		return nil, err
	}

	hashedCssFilename := vm.addHashToFilename(cleanFilename, originalHash)
	hashedCssPath := filepath.Join(cssDir, hashedCssFilename)

	// 复制并更新CSS文件
	if err := copyFile(originalCssPath, hashedCssPath); err != nil {
		return nil, err
	}

	// 更新hash版本CSS中的图片引用
	if len(imageMap) > 0 {
		if vm.debugMode {
			fmt.Printf("    📋 图片映射表 (%d 项):\n", len(imageMap))
			for k, v := range imageMap {
				if isJSOrCSS(k) {
					fmt.Printf("图片%s -> %s\n", k, v)
				}
			}
		}

		if err := vm.updateCSSImageReferences(hashedCssPath, imageMap); err != nil {
			fmt.Printf("      ⚠️  更新CSS图片引用失败: %v\n", err)
		}

		// 重新计算hash
		newHash, err := vm.calculateFileHash(hashedCssPath)
		if err == nil && newHash != originalHash {
			finalCssFilename := vm.addHashToFilename(cleanFilename, newHash)
			finalCssPath := filepath.Join(cssDir, finalCssFilename)

			if finalCssPath != hashedCssPath {
				if err := os.Rename(hashedCssPath, finalCssPath); err != nil {
					os.Remove(hashedCssPath)
					return nil, fmt.Errorf("重命名CSS哈希文件失败: %v", err)
				}
				hashedCssPath = finalCssPath
				hashedCssFilename = finalCssFilename
				originalHash = newHash
			}
		}
	}

	vcsGitAdd(hashedCssPath, vm.debugMode) // 自动添加到git

	// 删除旧的CSS hash文件
	cssExt := filepath.Ext(cleanFilename)
	cssBasename := strings.TrimSuffix(cleanFilename, cssExt)
	if err := vm.findAndDeleteOldHashFiles(cssDir, cssBasename, cssExt, originalHash); err != nil {
		if vm.debugMode {
			fmt.Printf("      ⚠️  清理CSS旧文件时出错: %v\n", err)
		}
	}

	// 移除: relPath, _ := filepath.Rel(vm.config.RootDir, originalCssPath)
	// 移除: vm.versionMap[relPath] = originalHash

	return &FileInfo{
		OriginalPath: originalCssPath,
		HashedPath:   hashedCssPath,
		Hash:         originalHash,
		Renamed:      true,
	}, nil
}

// updateHTMLContent updates HTML file content with hashed resource references (no deployment side effects)
func (vm *VersionManager) updateHTMLContent(htmlPath string, resources map[string]map[string]string) error {
	content, err := os.ReadFile(htmlPath)
	if err != nil {
		return err
	}

	contentStr := string(content)
	updated := false

	// 提取通用的资源替换逻辑（CSS和JS共用）
	// tagAttr 示例: `<link[^>]*href\s*=\s*` 或 `<script[^>]*src\s*=\s*`
	replaceResource := func(resMap map[string]string, tagAttr string, resType string) {
		for originalRelPath, newHashedPath := range resMap {
			escapedPath := regexp.QuoteMeta(originalRelPath)
			escapedPath = strings.ReplaceAll(escapedPath, "/", `[/\\]`)

			patterns := []string{
				fmt.Sprintf(`(%s['"])(%s)(\?[^'"]*)?(['"][^>]*>)`, tagAttr, escapedPath),
				fmt.Sprintf(`(%s['"])(\.{1,2}[/\\]%s)(\?[^'"]*)?(['"][^>]*>)`, tagAttr, escapedPath),
			}

			matched := false
			for _, pattern := range patterns {
				re := getRegex(pattern)
				if re.MatchString(contentStr) {
					newContent := re.ReplaceAllStringFunc(contentStr, func(match string) string {
						submatches := re.FindStringSubmatch(match)
						if len(submatches) >= 5 {
							prefix := submatches[1]
							oldPath := submatches[2]
							// submatches[3] is the optional query string, ignored
							suffix := submatches[4]

							var oldDir string
							isUrl := strings.HasPrefix(originalRelPath, "http") || strings.HasPrefix(originalRelPath, "//")
							if isUrl {
								lastSlash := strings.LastIndex(originalRelPath, "/")
								if lastSlash != -1 {
									oldDir = originalRelPath[:lastSlash]
								}
							} else {
								oldDir = filepath.Dir(originalRelPath)
							}

							newFilename := filepath.Base(newHashedPath)

							var newPath string
							if isUrl {
								if oldDir != "" {
									newPath = oldDir + "/" + newFilename
								} else {
									newPath = newFilename
								}
							} else if oldDir != "." && oldDir != "/" {
								newPath = filepath.Join(oldDir, newFilename)
								newPath = strings.ReplaceAll(newPath, `\`, "/")
							} else {
								newPath = newFilename
							}

							if strings.HasPrefix(oldPath, "../") || strings.HasPrefix(oldPath, "..\\") {
								if !strings.HasPrefix(newPath, "../") && !strings.HasPrefix(newPath, "..\\") {
									newPath = "../" + newPath
								}
							} else if strings.HasPrefix(oldPath, "./") || strings.HasPrefix(oldPath, ".\\") {
								if !strings.HasPrefix(newPath, "./") && !strings.HasPrefix(newPath, ".\\") {
									newPath = "./" + newPath
								}
							}

							if vm.config.CDNDomain != "" && !strings.HasPrefix(newPath, "http") && !vm.shouldExcludeFromCDN(newPath) {
								cleanNewPath := strings.TrimPrefix(newPath, "./")
								cleanNewPath = strings.TrimPrefix(cleanNewPath, "../")
								newPath = vm.config.CDNDomain + "/" + cleanNewPath
							}

							result := fmt.Sprintf("%s%s%s", prefix, newPath, suffix)

							if match != result {
								updated = true
								matched = true
								fmt.Printf("  ✅ %s: %s -> %s\n", resType, filepath.Base(oldPath), filepath.Base(newPath))
							}
							return result
						}
						return match
					})

					contentStr = newContent
					if matched {
						break
					}
				}
			}

			if !matched {
				vm.logf("  ⚠️  未匹配%s: %s\n", resType, originalRelPath)
			}
		}
	}

	// 处理CSS引用
	if cssMap, ok := resources["css"]; ok {
		replaceResource(cssMap, `<link[^>]*href\s*=\s*`, "CSS")
	}

	// 处理JS引用
	if jsMap, ok := resources["js"]; ok {
		replaceResource(jsMap, `<script[^>]*src\s*=\s*`, "JS")
	}

	// 处理剩余的普通资源（非hash），替换为CDN路径
	if vm.config.CDNDomain != "" {
		// 处理CSS
		contentStr = reCDNCSS.ReplaceAllStringFunc(contentStr, func(match string) string {
			submatches := reCDNCSS.FindStringSubmatch(match)
			if len(submatches) >= 4 {
				prefix := submatches[1]
				path := submatches[2]
				suffix := submatches[3]

				if !strings.Contains(path, ".css") {
					return match
				}
				if strings.HasPrefix(path, "http") || strings.HasPrefix(path, "//") || strings.HasPrefix(path, "data:") {
					return match
				}
				if vm.shouldExcludeFromCDN(path) {
					return match
				}

				cleanPath := path
				for strings.HasPrefix(cleanPath, "./") || strings.HasPrefix(cleanPath, "../") {
					cleanPath = strings.TrimPrefix(cleanPath, "./")
					cleanPath = strings.TrimPrefix(cleanPath, "../")
				}
				cleanPath = strings.TrimPrefix(cleanPath, "/")

				newPath := vm.config.CDNDomain + "/" + cleanPath

				if newPath != path {
					updated = true
					fmt.Printf("  🌍 CDN(CSS): %s -> %s\n", filepath.Base(path), newPath)
					return prefix + newPath + suffix
				}
			}
			return match
		})

		// 处理JS
		contentStr = reCDNJS.ReplaceAllStringFunc(contentStr, func(match string) string {
			submatches := reCDNJS.FindStringSubmatch(match)
			if len(submatches) >= 4 {
				prefix := submatches[1]
				path := submatches[2]
				suffix := submatches[3]

				if !strings.Contains(path, ".js") {
					return match
				}
				if strings.HasPrefix(path, "http") || strings.HasPrefix(path, "//") || strings.HasPrefix(path, "data:") {
					return match
				}
				if vm.shouldExcludeFromCDN(path) {
					return match
				}

				cleanPath := path
				for strings.HasPrefix(cleanPath, "./") || strings.HasPrefix(cleanPath, "../") {
					cleanPath = strings.TrimPrefix(cleanPath, "./")
					cleanPath = strings.TrimPrefix(cleanPath, "../")
				}
				cleanPath = strings.TrimPrefix(cleanPath, "/")

				newPath := vm.config.CDNDomain + "/" + cleanPath

				if newPath != path {
					updated = true
					fmt.Printf("  🌍 CDN(JS): %s -> %s\n", filepath.Base(path), newPath)
					return prefix + newPath + suffix
				}
			}
			return match
		})
	}

	// 更新顶部注释中的创建和修改日期
	now := time.Now().Format("2006-01-02 15:04:05")
	datePattern := `(@(?:create|modify)\s+date\s+)\d{4}-\d{2}-\d{2}\s+\d{2}:\d{2}:\d{2}`
	reDate := regexp.MustCompile(datePattern)
	if reDate.MatchString(contentStr) {
		newContentStr := reDate.ReplaceAllString(contentStr, "${1}"+now)
		if newContentStr != contentStr {
			contentStr = newContentStr
			updated = true
			fmt.Printf("  🕐 注释日期已更新: %s\n", now)
		}
	}

	if updated {
		if err := os.WriteFile(htmlPath, []byte(contentStr), 0644); err != nil {
			return err
		}
		fmt.Printf("\n✅ HTML文件已更新\n")
	} else {
		fmt.Printf("\n⚠️  没有内容需要更新\n")
	}

	return nil
}

// updateHTMLReferences updates HTML references and then triggers deployment
func (vm *VersionManager) updateHTMLReferences(htmlPath string, resources map[string]map[string]string) error {
	if err := vm.updateHTMLContent(htmlPath, resources); err != nil {
		return err
	}

	// 执行部署（如果启用）
	if vm.config.Deploy.Enabled {
		vm.runDeploy()
	}

	return nil
}

// ==================== 部署相关功能 ====================

// DeployManager 部署管理器
type DeployManager struct {
	config          DeployConfig
	sourcePath      string
	destPath        string
	debugMode       bool
	folderOpened    bool
	cache           *DeployCache    // 持久化文件hash缓存
	currentLive     map[string]bool // 本次抓取的现网引用名单
	skipCleanup     bool            // 现网抓取失败/首次记录时跳过清理
	protectedHashes map[string]bool // dest HTML 历史版本引用过的 hash 资源名单
	noHash          bool            // CDN无hash模式（mode 16/17）：只复制原始文件
}

// NewDeployManager 创建部署管理器
func NewDeployManager(config DeployConfig, debugMode bool) *DeployManager {
	isHome := isHomeEnv()

	var sourcePath, destPath string
	if isHome {
		sourcePath = config.HomeSourcePath
		destPath = config.HomeDestPath
	} else {
		sourcePath = config.CompanySourcePath
		destPath = config.CompanyDestPath
	}

	return &DeployManager{
		config:       config,
		sourcePath:   sourcePath,
		destPath:     destPath,
		debugMode:    debugMode,
		folderOpened: false,
		cache:        loadDeployCache(".deploy-cache.json"),
	}
}

// getFileHash 计算文件hash
func getFileHash(filePath string) (string, error) {
	file, err := os.Open(filePath)
	if err != nil {
		return "", err
	}
	defer file.Close()

	hash := md5.New()
	if _, err := io.Copy(hash, file); err != nil {
		return "", err
	}

	return hex.EncodeToString(hash.Sum(nil)), nil
}

// FileCacheEntry 文件hash缓存条目
type FileCacheEntry struct {
	Hash    string `json:"hash"`
	Size    int64  `json:"size"`
	ModTime int64  `json:"modTime"`
}

// DeployCache 部署文件hash缓存（持久化到磁盘，避免每次重复计算MD5）
type DeployCacheFile struct {
	Files          map[string]FileCacheEntry `json:"files"`
	SessionTime    int64                     `json:"sessionTime,omitempty"`
	ReleasedHashes map[string]bool           `json:"releasedHashes,omitempty"`
}

type DeployCache struct {
	Files          map[string]FileCacheEntry
	SessionTime    int64
	ReleasedHashes map[string]bool
	cachePath      string
	mu             sync.RWMutex
	dirty          bool
}

// deploySessionGap 超过该间隔的两次部署视为新会话：重新抓现网快照作为
// 会话基线；会话内多次部署沿用会话开始时的基线。
const deploySessionGap = 6 * time.Hour

// prodProtectWindow 会话基线（会话开始时的现网版本）的保护时长：
// 覆盖“发布后老用户浏览器仍缓存旧 HTML”的窗口期。
const prodProtectWindow = 12 * time.Hour

// loadDeployCache 从磁盘加载缓存，文件不存在或解析失败时返回空缓存
func loadDeployCache(cachePath string) *DeployCache {
	dc := &DeployCache{Files: make(map[string]FileCacheEntry), cachePath: cachePath}
	if data, err := os.ReadFile(cachePath); err == nil {
		// 标准包装格式（Go 写出，Rust 已兼容）：{"files":{...},"sessionTime":N,"releasedHashes":{...}}
		var wrapper DeployCacheFile
		if err := json.Unmarshal(data, &wrapper); err == nil && wrapper.Files != nil {
			dc.Files = wrapper.Files
			dc.SessionTime = wrapper.SessionTime
			dc.ReleasedHashes = wrapper.ReleasedHashes
			return dc
		}

		// Rust 版扁平格式：文件条目与 __sessionTime / __releasedHashes 元数据键平级
		var raw map[string]json.RawMessage
		if err := json.Unmarshal(data, &raw); err == nil && len(raw) > 0 {
			parsed := 0
			for key, val := range raw {
				switch key {
				case "__sessionTime", "sessionTime":
					var t int64
					if json.Unmarshal(val, &t) == nil {
						dc.SessionTime = t
					}
				case "__releasedHashes", "releasedHashes":
					var arr []string
					if json.Unmarshal(val, &arr) == nil && arr != nil {
						dc.ReleasedHashes = make(map[string]bool, len(arr))
						for _, name := range arr {
							dc.ReleasedHashes[name] = true
						}
					} else {
						var obj map[string]bool
						if json.Unmarshal(val, &obj) == nil && obj != nil {
							dc.ReleasedHashes = obj
						}
					}
				default:
					var entry FileCacheEntry
					if json.Unmarshal(val, &entry) == nil && entry.Hash != "" {
						dc.Files[key] = entry
						parsed++
					}
				}
			}
			if parsed > 0 {
				return dc
			}
		}

		// 旧版扁平格式（无元数据键）
		var legacy map[string]FileCacheEntry
		if err := json.Unmarshal(data, &legacy); err == nil && legacy != nil {
			dc.Files = legacy
			return dc
		}

		if len(dc.Files) == 0 {
			fmt.Printf("⚠️  部署缓存解析失败，将重建缓存: 无法识别的缓存格式\n")
			dc.Files = make(map[string]FileCacheEntry)
		}
	}
	return dc
}

// Save 将缓存写入磁盘（仅在有变更时写入）
func (dc *DeployCache) Save() error {
	dc.mu.RLock()
	defer dc.mu.RUnlock()
	if !dc.dirty {
		return nil
	}
	data, err := json.MarshalIndent(DeployCacheFile{
		Files:          dc.Files,
		SessionTime:    dc.SessionTime,
		ReleasedHashes: dc.ReleasedHashes,
	}, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(dc.cachePath, data, 0644)
}

// getCachedHash 获取文件hash：size+modTime 未变则复用缓存的hash，否则重新计算
func (dc *DeployCache) getCachedHash(filePath string) (string, error) {
	key := filepath.Clean(filePath)
	info, err := os.Stat(filePath)
	if err != nil {
		return "", err
	}

	dc.mu.RLock()
	entry, ok := dc.Files[key]
	dc.mu.RUnlock()

	if ok && entry.Size == info.Size() && entry.ModTime == info.ModTime().UnixNano() {
		return entry.Hash, nil
	}

	hash, err := getFileHash(filePath)
	if err != nil {
		return "", err
	}

	dc.mu.Lock()
	dc.Files[key] = FileCacheEntry{
		Hash:    hash,
		Size:    info.Size(),
		ModTime: info.ModTime().UnixNano(),
	}
	dc.dirty = true
	dc.mu.Unlock()

	return hash, nil
}

// updateCache 直接写入缓存条目（复制文件后用源文件hash同步目标缓存）
func (dc *DeployCache) updateCache(filePath, hash string, size, modTime int64) {
	key := filepath.Clean(filePath)
	dc.mu.Lock()
	dc.Files[key] = FileCacheEntry{Hash: hash, Size: size, ModTime: modTime}
	dc.dirty = true
	dc.mu.Unlock()
}

// FileVersion 文件版本信息
type FileVersion struct {
	Path    string
	Name    string
	HasHash bool
	ModTime time.Time
	Hash    string
}

// findAllFileVersions 查找文件的所有版本（包含hash值）
func (dm *DeployManager) findAllFileVersions(configPath string) []FileVersion {
	fullPath := filepath.Join(dm.sourcePath, configPath)
	dir := filepath.Dir(fullPath)
	fileName := filepath.Base(fullPath)
	ext := filepath.Ext(fileName)
	basename := strings.TrimSuffix(fileName, ext)

	if dm.debugMode && isJSOrCSS(fileName) {
		fmt.Printf("  🔎 findAllFileVersions: configPath=%s\n", configPath)
		fmt.Printf("     fullPath=%s\n", fullPath)
		fmt.Printf("     dir=%s, fileName=%s\n", dir, fileName)
	}

	var versions []FileVersion

	if !fileExists(dir) {
		if dm.debugMode && isJSOrCSS(fileName) {
			fmt.Printf("     ❌ 目录不存在: %s\n", dir)
		}
		return versions
	}

	// 检查无hash版本
	if fileExists(fullPath) {
		hash, _ := dm.cache.getCachedHash(fullPath)
		info, _ := os.Stat(fullPath)
		versions = append(versions, FileVersion{
			Path:    fullPath,
			Name:    fileName,
			HasHash: false,
			ModTime: info.ModTime(),
			Hash:    hash,
		})
	}

	// 查找所有hash版本
	files, err := os.ReadDir(dir)
	if err != nil {
		return versions
	}

	hashPattern := getRegex(fmt.Sprintf(`^%s\.[a-zA-Z0-9]+%s$`, regexp.QuoteMeta(basename), regexp.QuoteMeta(ext)))

	if dm.debugMode && isJSOrCSS(fileName) {
		fmt.Printf("     hashPattern: ^%s\\.[a-zA-Z0-9]+%s$\n", regexp.QuoteMeta(basename), regexp.QuoteMeta(ext))
	}

	for _, file := range files {
		if file.Name() == fileName {
			continue
		}

		if hashPattern.MatchString(file.Name()) {
			filePath := filepath.Join(dir, file.Name())
			hash, _ := dm.cache.getCachedHash(filePath)
			info, _ := file.Info()
			versions = append(versions, FileVersion{
				Path:    filePath,
				Name:    file.Name(),
				HasHash: true,
				ModTime: info.ModTime(),
				Hash:    hash,
			})
			if dm.debugMode && isJSOrCSS(fileName) {
				fmt.Printf("     ✅ 匹配hash文件: %s\n", file.Name())
			}
		}
	}

	if dm.debugMode && isJSOrCSS(fileName) {
		fmt.Printf("     共找到 %d 个版本\n", len(versions))
	}

	// 按修改时间排序（最新的在前）
	for i := 0; i < len(versions)-1; i++ {
		for j := i + 1; j < len(versions); j++ {
			if versions[j].ModTime.After(versions[i].ModTime) {
				versions[i], versions[j] = versions[j], versions[i]
			}
		}
	}

	return versions
}

// liveProdFetch 抓取现网 HTML 内容（测试时可替换）。
var liveProdFetch = func(url string) ([]byte, error) {
	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Get(url)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("HTTP %d", resp.StatusCode)
	}
	return io.ReadAll(resp.Body)
}

// fetchLiveHashes 抓取现网页面并解析其引用的 hash 资源文件名。
// 至少一个 URL 抓取成功才算 ok；未配置或全部失败返回 ok=false。
func (dm *DeployManager) fetchLiveHashes() (map[string]bool, bool) {
	if len(dm.config.ProdHTMLURLs) == 0 {
		return nil, false
	}
	fmt.Println("🌐 正在抓取现网快照...")
	hashes := make(map[string]bool)
	hashPattern := getRegex(`[A-Za-z0-9_-]+\.[a-f0-9]{4,64}\.[A-Za-z0-9]+`)
	ok := false
	for _, url := range dm.config.ProdHTMLURLs {
		fmt.Printf("  📄 %s\n", url)
		started := time.Now()
		body, err := liveProdFetch(url)
		if err != nil {
			fmt.Printf("⚠️  抓取现网页面失败 %s: %v\n", url, err)
			continue
		}
		ok = true
		for _, m := range hashPattern.FindAllString(string(body), -1) {
			hashes[filepath.Base(m)] = true
		}
		fmt.Printf("  ✅ 抓取完成 %v，解析到 %d 个hash\n", time.Since(started), len(hashes))
	}
	if ok {
		return hashes, true
	}
	return nil, false
}

// prepareDeploySession 部署前的现网快照准备：
//   - 每次部署实时抓现网，其引用的版本本次必须保留（currentLive）
//   - 距上次部署超过 deploySessionGap 视为新会话，把当时的现网快照存为
//     会话基线（保护 prodProtectWindow），覆盖“12点上线后老用户缓存”场景
//   - 会话内现网的后续变化不改变基线，也不作为清理依据
//   - 抓取失败或首次记录快照时跳过本次清理，宁可多留不可误删
func (dm *DeployManager) prepareDeploySession() {
	live, ok := dm.fetchLiveHashes()
	now := time.Now().UnixNano()
	if !ok {
		dm.skipCleanup = true
		fmt.Println("⚠️  无法获取现网版本，本次部署跳过旧hash清理")
		return
	}
	dm.currentLive = live

	if dm.cache.SessionTime == 0 || now-dm.cache.SessionTime >= int64(deploySessionGap) {
		firstRun := dm.cache.SessionTime == 0
		dm.cache.ReleasedHashes = live
		dm.cache.SessionTime = now
		dm.cache.dirty = true
		if err := dm.cache.Save(); err != nil && dm.debugMode {
			fmt.Printf("  ⚠️  保存现网快照失败: %v\n", err)
		}
		if firstRun {
			dm.skipCleanup = true
			fmt.Println("ℹ️  首次记录现网快照，本次部署不清理旧hash")
		}
	}
}

// ==================== HTML 历史引用保护 ====================

// htmlProtectWindow 线上 HTML 历史保护窗口：窗口内所有已提交 HTML 版本
// 引用过的 hash 资源一律保留。用户 HTML 缓存约 12 小时，窗口取 24 小时
// （12h × 2 保险）：任何在窗口内上线过的 HTML 版本，其引用的资源都可能
// 被缓存用户请求，删除即 404。HTML 可能被频繁提交，因此不固定取
// "最近 N 版"，而是按时间窗扫描全部版本，避免频繁提交导致覆盖时间过短
const htmlProtectWindow = 24 * time.Hour

// htmlProtectMaxRevisions 单个 HTML 在窗口内允许扫描的最大版本数，超过则跳过本次清理
const htmlProtectMaxRevisions = 50

var reHashedBasename = regexp.MustCompile(`[A-Za-z0-9_-]+\.[a-f0-9]{4,64}\.[A-Za-z0-9]+`)
var reHashedBasenameParts = regexp.MustCompile(`^([A-Za-z0-9_-]+)\.[a-f0-9]{4,64}\.([A-Za-z0-9]+)$`)
var reHashedCSSPath = regexp.MustCompile(`[/\w.-]+\.[a-f0-9]{4,64}\.css`)

// historyLogTrace dest 历史查询追踪（dry-run 时打开，打印实际执行的命令与结果），
// 默认空实现；诊断走的是与生产完全相同的查询代码路径
var historyLogTrace = func(format string, args ...interface{}) {}

// destRepoKind 目标目录所属的版本控制系统类型
type destRepoKind int

const (
	destRepoUnknown destRepoKind = iota
	destRepoGit
	destRepoSvn
)

func (k destRepoKind) String() string {
	switch k {
	case destRepoGit:
		return "Git"
	case destRepoSvn:
		return "SVN"
	}
	return "未知"
}

// detectDestRepoKind 探测目录所属版本控制系统：优先 Git，其次 SVN。
// 部署目标在 Git 仓库子目录（如 res/wap）下时 git 命令依然有效
func detectDestRepoKind(dir string) destRepoKind {
	if dir == "" {
		return destRepoUnknown
	}
	if isGitRepo(dir) {
		return destRepoGit
	}
	if isSvnRepo(dir) {
		return destRepoSvn
	}
	return destRepoUnknown
}

// detectRepoKindForPath 按文件所在目录探测仓库类型
func detectRepoKindForPath(wcPath string) destRepoKind {
	return detectDestRepoKind(filepath.Dir(wcPath))
}

// destHTMLHistoryRevisions 按目标仓库类型分发历史版本查询：
// 查询文件在 since 之后的所有提交（Git 完整 hash / SVN 版本号），
// 外加窗口边界前最后一次提交（该版本在窗口开始时仍在线上）。
// 返回按时间从新到旧排列；声明为 var 以便测试注入
var destHTMLHistoryRevisions = func(wcPath string, since time.Time, maxRevs int) ([]string, error) {
	switch detectRepoKindForPath(wcPath) {
	case destRepoGit:
		return gitHTMLHistoryRevisions(wcPath, since, maxRevs)
	case destRepoSvn:
		return svnHTMLHistoryRevisions(wcPath, since, maxRevs)
	}
	return nil, fmt.Errorf("目标目录既不是 Git 也不是 SVN 仓库: %s", filepath.Dir(wcPath))
}

// gitHTMLHistoryRevisions Git 实现：git log 窗口内提交 + git rev-list 边界提交
func gitHTMLHistoryRevisions(wcPath string, since time.Time, maxRevs int) ([]string, error) {
	if _, err := exec.LookPath("git"); err != nil {
		return nil, fmt.Errorf("未找到git命令")
	}
	dir := filepath.Dir(wcPath)
	name := filepath.Base(wcPath)
	sinceStr := since.Format("2006-01-02T15:04:05")

	cmd := exec.Command("git", "log", "--format=%H", "--since="+sinceStr, "--", name)
	cmd.Dir = dir
	output, err := cmd.Output()
	if err != nil {
		return nil, err
	}

	commits := parseGitHashLines(string(output))
	historyLogTrace("  [git log] 窗口内版本查询: git log --format=%%H --since=%s -- %s", sinceStr, name)
	for _, c := range commits {
		historyLogTrace("      %s", c)
	}
	if len(commits) == 0 {
		historyLogTrace("      (窗口内无提交)")
	}

	// 窗口边界前最后一次提交：该版本在窗口开始时仍在线上，同样需保护。
	// 长寿命版本（如 9/8 上线、9/24 才被替换）只出现在这里，查询失败必须
	// 中止本次清理，漏掉边界版本会误删仍在被缓存用户请求的上一生产版本
	boundaryCmd := exec.Command("git", "rev-list", "-1", "--before="+sinceStr, "HEAD", "--", name)
	boundaryCmd.Dir = dir
	boundaryOut, boundaryErr := boundaryCmd.Output()
	if boundaryErr != nil {
		return nil, fmt.Errorf("查询窗口边界版本失败: %w", boundaryErr)
	}

	boundaryCommit := strings.TrimSpace(string(boundaryOut))
	historyLogTrace("  [git rev-list] 窗口边界版本查询: git rev-list -1 --before=%s HEAD -- %s", sinceStr, name)
	if boundaryCommit != "" {
		dup := false
		for _, c := range commits {
			if c == boundaryCommit {
				dup = true
				break
			}
		}
		if !dup {
			commits = append(commits, boundaryCommit)
			historyLogTrace("      → 边界版本 %s（窗口开始时仍在线上，须保护）", boundaryCommit)
		} else {
			historyLogTrace("      → 边界版本 %s 与窗口内版本重复，无需重复加入", boundaryCommit)
		}
	} else {
		historyLogTrace("      (窗口开始前该文件无提交，无边界版本)")
	}

	if len(commits) == 0 {
		return nil, fmt.Errorf("窗口内无提交历史")
	}
	if len(commits) > maxRevs {
		return nil, fmt.Errorf("窗口内版本数 %d 超过上限 %d", len(commits), maxRevs)
	}
	return commits, nil
}

// svnXMLLog svn log --xml 输出结构（仅解析所需字段）
type svnXMLLog struct {
	Entries []struct {
		Revision string `xml:"revision,attr"`
		Author   string `xml:"author"`
		Date     string `xml:"date"`
	} `xml:"logentry"`
}

// svnHTMLHistoryRevisions SVN 实现：svn log 窗口内版本 + 窗口边界前最后版本
func svnHTMLHistoryRevisions(wcPath string, since time.Time, maxRevs int) ([]string, error) {
	if _, err := exec.LookPath("svn"); err != nil {
		return nil, fmt.Errorf("未找到svn命令")
	}
	sinceStr := "{" + since.Format("2006-01-02") + "}"

	cmd := exec.Command("svn", "log", "--xml", "-r", sinceStr+":HEAD", "-l", strconv.Itoa(maxRevs), wcPath)
	output, err := cmd.Output()
	if err != nil {
		return nil, err
	}
	var log svnXMLLog
	if err := xml.Unmarshal(output, &log); err != nil {
		return nil, err
	}

	seen := make(map[string]bool)
	revs := make([]string, 0, len(log.Entries)+1)
	historyLogTrace("  [svn log] 窗口内版本查询: svn log -r %s:HEAD -l %d %s", sinceStr, maxRevs, filepath.Base(wcPath))
	for _, e := range log.Entries {
		rev := strings.TrimSpace(e.Revision)
		if rev == "" || seen[rev] {
			continue
		}
		seen[rev] = true
		revs = append(revs, rev)
		historyLogTrace("      r%s  %s  %s", rev, e.Date, e.Author)
	}
	if len(revs) == 0 {
		historyLogTrace("      (窗口内无提交)")
	}

	// 窗口边界前最后一次提交：该版本在窗口开始时仍在线上，同样需保护。
	// 长寿命版本（如 9/8 上线、9/24 才被替换）只出现在这里，查询失败必须
	// 中止本次清理，漏掉边界版本会误删仍在被缓存用户请求的上一生产版本
	boundaryCmd := exec.Command("svn", "log", "--xml", "-r", sinceStr+":1", "-l", "1", wcPath)
	boundaryOut, boundaryErr := boundaryCmd.Output()
	if boundaryErr != nil {
		return nil, fmt.Errorf("查询窗口边界版本失败: %w", boundaryErr)
	}
	var boundaryLog svnXMLLog
	if err := xml.Unmarshal(boundaryOut, &boundaryLog); err != nil {
		return nil, fmt.Errorf("解析窗口边界版本失败: %w", err)
	}
	historyLogTrace("  [svn log] 窗口边界版本查询: svn log -r %s:1 -l 1 %s", sinceStr, filepath.Base(wcPath))
	if len(boundaryLog.Entries) > 0 {
		rev := strings.TrimSpace(boundaryLog.Entries[0].Revision)
		if rev != "" && !seen[rev] {
			revs = append(revs, rev)
			historyLogTrace("      → 边界版本 r%s  %s  %s（窗口开始时仍在线上，须保护）",
				rev, boundaryLog.Entries[0].Date, boundaryLog.Entries[0].Author)
		} else if rev != "" {
			historyLogTrace("      → 边界版本 r%s 与窗口内版本重复，无需重复加入", rev)
		}
	} else {
		historyLogTrace("      (窗口开始前该文件无提交，无边界版本)")
	}

	if len(revs) == 0 {
		return nil, fmt.Errorf("窗口内无提交历史")
	}
	if len(revs) > maxRevs {
		return nil, fmt.Errorf("窗口内版本数 %d 超过上限 %d", len(revs), maxRevs)
	}
	return revs, nil
}

// parseGitHashLines 从 git log 输出解析提交 hash（每行一个）
func parseGitHashLines(output string) []string {
	var out []string
	for _, line := range strings.Split(output, "\n") {
		line = strings.TrimSpace(line)
		if line != "" {
			out = append(out, line)
		}
	}
	return out
}

// showFileRevision 按目标仓库类型分发：读取文件在指定提交/版本的内容；
// 声明为 var 以便测试注入
var showFileRevision = func(wcPath string, commit string) ([]byte, error) {
	switch detectRepoKindForPath(wcPath) {
	case destRepoGit:
		return gitShowRevision(wcPath, commit)
	case destRepoSvn:
		return svnCatRevision(wcPath, commit)
	}
	return nil, fmt.Errorf("目标目录既不是 Git 也不是 SVN 仓库: %s", filepath.Dir(wcPath))
}

// gitShowRevision Git 实现（:./ 相对当前目录解析路径）
func gitShowRevision(wcPath string, commit string) ([]byte, error) {
	if _, err := exec.LookPath("git"); err != nil {
		return nil, fmt.Errorf("未找到git命令")
	}
	cmd := exec.Command("git", "show", commit+":./"+filepath.Base(wcPath))
	cmd.Dir = filepath.Dir(wcPath)
	return cmd.Output()
}

// svnCatRevision SVN 实现：svn cat -r <版本号>
func svnCatRevision(wcPath string, rev string) ([]byte, error) {
	if _, err := exec.LookPath("svn"); err != nil {
		return nil, fmt.Errorf("未找到svn命令")
	}
	cmd := exec.Command("svn", "cat", "-r", rev, wcPath)
	return cmd.Output()
}

// destFileCommitTime 按目标仓库类型分发：查询文件最后一次提交时间
// （本地元数据，不联网）；用于"近期部署过的 hash 先保留一阵"的判定
var destFileCommitTime = func(wcPath string) (time.Time, bool) {
	switch detectRepoKindForPath(wcPath) {
	case destRepoGit:
		return gitFileCommitTime(wcPath)
	case destRepoSvn:
		return svnLastChangedTime(wcPath)
	}
	return time.Time{}, false
}

// gitFileCommitTime Git 实现：git log -1 --format=%ct
func gitFileCommitTime(wcPath string) (time.Time, bool) {
	if _, err := exec.LookPath("git"); err != nil {
		return time.Time{}, false
	}
	cmd := exec.Command("git", "log", "-1", "--format=%ct", "--", filepath.Base(wcPath))
	cmd.Dir = filepath.Dir(wcPath)
	output, err := cmd.Output()
	if err != nil {
		return time.Time{}, false
	}
	secs, err := strconv.ParseInt(strings.TrimSpace(string(output)), 10, 64)
	if err != nil {
		return time.Time{}, false
	}
	return time.Unix(secs, 0), true
}

// svnLastChangedTime SVN 实现：解析 svn info --xml 的最后提交时间
func svnLastChangedTime(wcPath string) (time.Time, bool) {
	if _, err := exec.LookPath("svn"); err != nil {
		return time.Time{}, false
	}
	cmd := exec.Command("svn", "info", "--xml", wcPath)
	output, err := cmd.Output()
	if err != nil {
		return time.Time{}, false
	}
	out := string(output)
	start := strings.Index(out, "<date>")
	if start < 0 {
		return time.Time{}, false
	}
	rest := out[start+len("<date>"):]
	end := strings.Index(rest, "</date>")
	if end < 0 {
		return time.Time{}, false
	}
	t, err := time.Parse("2006-01-02T15:04:05.999999999Z07:00", strings.TrimSpace(rest[:end]))
	if err != nil {
		return time.Time{}, false
	}
	return t, true
}

// findDestHTMLFiles 扫描 dest 根目录（不递归）下的 HTML 文件
func findDestHTMLFiles(destPath string) ([]string, error) {
	var htmls []string
	entries, err := os.ReadDir(destPath)
	if err != nil {
		return nil, err
	}
	for _, e := range entries {
		if !e.IsDir() && strings.EqualFold(filepath.Ext(e.Name()), ".html") {
			htmls = append(htmls, filepath.Join(destPath, e.Name()))
		}
	}
	return htmls, nil
}

// deployedHTMLFiles 返回本次部署涉及的 HTML 文件（dest 根目录下）：
// 取 config.FilePaths 里的 .html 项；没有则回退扫描全部 .html。
// 只保护本工具部署的页面，避免把无关页面的历史引用也保护进来
func (dm *DeployManager) deployedHTMLFiles() []string {
	all, err := findDestHTMLFiles(dm.destPath)
	if err != nil {
		return nil
	}
	want := make(map[string]bool)
	for _, fp := range dm.config.FilePaths {
		name := filepath.Base(strings.ReplaceAll(fp, "\\", "/"))
		if strings.EqualFold(filepath.Ext(name), ".html") {
			want[name] = true
		}
	}
	if len(want) == 0 {
		return all
	}
	var out []string
	for _, p := range all {
		if want[filepath.Base(p)] {
			out = append(out, p)
		}
	}
	if len(out) == 0 {
		return all
	}
	return out
}

// diagnoseHashRetention 只读诊断：对本次部署涉及的目录做 hash 文件去留分类，
// 打印"引用保护 / 近期部署保留 / 可清理"三类，供 dry-run 核对
func (dm *DeployManager) diagnoseHashRetention(protected map[string]bool) {
	type target struct {
		dir      string
		wildcard bool
		base     string
	}
	var targets []target
	for _, fp := range dm.config.FilePaths {
		rel := strings.ReplaceAll(strings.TrimPrefix(fp, "/"), "\\", "/")
		if strings.HasSuffix(rel, "/*") {
			// 通配符项：去掉 /* 后本身就是目录
			targets = append(targets, target{
				dir:      filepath.Join(dm.destPath, filepath.FromSlash(strings.TrimSuffix(rel, "/*"))),
				wildcard: true,
			})
		} else {
			base := filepath.Base(rel)
			if filepath.Ext(base) == "" {
				continue
			}
			targets = append(targets, target{
				dir:  filepath.Join(dm.destPath, filepath.FromSlash(filepath.Dir(rel))),
				base: base,
			})
		}
	}
	var refProtected, recentKept, deletable []string
	seen := make(map[string]bool)
	for _, t := range targets {
		basePrefix := strings.TrimSuffix(t.base, filepath.Ext(t.base)) + "."
		filepath.WalkDir(t.dir, func(path string, d fs.DirEntry, err error) error {
			if err != nil {
				return nil
			}
			if d.IsDir() {
				if d.Name() == ".svn" || d.Name() == ".git" {
					return filepath.SkipDir
				}
				return nil
			}
			name := d.Name()
			if reHashedBasename.FindString(name) != name {
				return nil
			}
			// 非通配符项只关心同 basename 的 hash 变体（与 cleanHashFiles 一致）
			if !t.wildcard && !strings.HasPrefix(name, basePrefix) {
				return nil
			}
			if seen[path] {
				return nil
			}
			seen[path] = true
			switch {
			case protected[name]:
				refProtected = append(refProtected, name)
			default:
				if commitTime, ok := destFileCommitTime(path); ok &&
					time.Since(commitTime) < htmlProtectWindow {
					recentKept = append(recentKept, name+" (提交于 "+commitTime.Local().Format("01-02 15:04")+")")
				} else {
					deletable = append(deletable, name)
				}
			}
			return nil
		})
	}
	fmt.Printf("\n  📊 去留分类: HTML引用保护 %d 个，近期部署保留 %d 个，可清理 %d 个\n",
		len(refProtected), len(recentKept), len(deletable))
	if len(recentKept) > 0 {
		fmt.Printf("     —— 近期部署保留（仓库里查不到引用，靠提交时间兜底）:\n")
		sort.Strings(recentKept)
		for _, n := range recentKept {
			fmt.Printf("        🛡️ %s\n", n)
		}
	}
	if len(deletable) > 0 {
		fmt.Printf("     —— 可清理（不在任何保护名单、且提交超 %v）:\n", htmlProtectWindow)
		sort.Strings(deletable)
		for _, n := range deletable {
			fmt.Printf("        🗑️ %s\n", n)
		}
	}
}

// indexDestFiles 建立 dest 下文件 basename → 完整路径 索引（跳过 .svn/.git 目录）
func indexDestFiles(destPath string) (map[string]string, error) {
	index := make(map[string]string)
	err := filepath.WalkDir(destPath, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if d.Name() == ".svn" || d.Name() == ".git" {
				return filepath.SkipDir
			}
			return nil
		}
		index[d.Name()] = path
		return nil
	})
	return index, err
}

// deriveHistoryRelPath 从 HTML 中匹配到的资源路径推导相对 dest 的路径：
// 优先剥离 CDNPathPrefix（形如 https://cdn域/前缀/wap），剩余即相对路径；
// 本身就是相对路径（css/xxx.css）则原样返回
func deriveHistoryRelPath(match string, cdnPrefix string) string {
	p := strings.ReplaceAll(match, "\\", "/")
	p = strings.TrimPrefix(p, "//")
	prefix := strings.ReplaceAll(cdnPrefix, "\\", "/")
	prefix = strings.TrimPrefix(prefix, "https://")
	prefix = strings.TrimPrefix(prefix, "http://")
	prefix = strings.TrimSuffix(prefix, "/")
	if prefix != "" && strings.HasPrefix(p, prefix) {
		p = p[len(prefix):]
	}
	return strings.TrimPrefix(p, "/")
}

// buildProtectedHashes 以 dest 里线上 HTML 的提交历史为准构建保护名单：
//   - dest 最新已提交 HTML = 当前上线版本，其引用的全部 hash 必须保留
//   - 再回溯 htmlProtectWindow 内提交过的每个 HTML 版本，引用同样保留
//     （覆盖缓存里还拿着旧版 HTML 的用户）
//   - 其中引用的带 hash CSS，其内容里的 url(...) 带 hash 图片同样保护
//   - 任何 git 查询失败返回 ok=false（调用方跳过本次清理，宁可多留不可误删）
func (dm *DeployManager) buildProtectedHashes() (map[string]bool, bool) {
	htmlFiles := dm.deployedHTMLFiles()
	if len(htmlFiles) == 0 {
		return nil, false
	}

	destIndex, err := indexDestFiles(dm.destPath)
	if err != nil {
		return nil, false
	}

	protected := make(map[string]bool)
	since := time.Now().Add(-htmlProtectWindow)
	for _, htmlPath := range htmlFiles {
		commits, err := destHTMLHistoryRevisions(htmlPath, since, htmlProtectMaxRevisions)
		if err != nil {
			fmt.Printf("⚠️  获取HTML历史版本失败 %s: %v，本次跳过旧hash清理\n", filepath.Base(htmlPath), err)
			return nil, false
		}
		for _, commit := range commits {
			content, err := showFileRevision(htmlPath, commit)
			if err != nil {
				fmt.Printf("⚠️  读取HTML历史内容失败 %s@%s: %v，本次跳过旧hash清理\n", filepath.Base(htmlPath), commit, err)
				return nil, false
			}
			contentStr := string(content)

			// 1. HTML 内容中引用的所有带 hash 资源
			for _, m := range reHashedBasename.FindAllString(contentStr, -1) {
				protected[m] = true
			}

			// 2. 引用的带 hash CSS：其内容里的 url(...) 带 hash 图片同样保护
			for _, cssMatch := range reHashedCSSPath.FindAllString(contentStr, -1) {
				cssBase := filepath.Base(cssMatch)
				images, ok := dm.fetchCSSImageRefs(cssMatch, cssBase, commit, destIndex)
				if !ok {
					fmt.Printf("⚠️  获取CSS历史内容失败 %s@%s，本次跳过旧hash清理\n", cssBase, commit)
					return nil, false
				}
				for _, img := range images {
					protected[img] = true
				}
			}
		}
	}
	return protected, true
}

// fetchCSSImageRefs 获取历史版本 CSS 内容中引用的带 hash 图片名。
// hash 文件名即内容指纹（同名同内容），CSS 优先读 dest 本地副本；
// 本地不存在时按 HTML 中的路径线索从 git 历史读取
func (dm *DeployManager) fetchCSSImageRefs(cssMatch, cssBase string, commit string, destIndex map[string]string) ([]string, bool) {
	var content []byte
	if cssPath, found := destIndex[cssBase]; found {
		data, err := os.ReadFile(cssPath)
		if err != nil {
			return nil, false
		}
		content = data
	} else {
		relPath := deriveHistoryRelPath(cssMatch, dm.config.CDNPathPrefix)
		if relPath == "" || relPath == cssBase {
			return nil, false
		}
		data, err := showFileRevision(filepath.Join(dm.destPath, filepath.FromSlash(relPath)), commit)
		if err != nil {
			return nil, false
		}
		content = data
	}
	return reHashedBasename.FindAllString(string(content), -1), true
}

// cleanHashFiles 清理旧 hash：保留本次新版本、当前现网版、
// 以及 12 小时保护期内的会话基线版本；其余（灰度中间产物、
// 超过保护期的旧现网版）删除。
func (dm *DeployManager) cleanHashFiles(destPath, keepFileName string) int {
	destDir := filepath.Dir(destPath)
	destFileName := filepath.Base(destPath)
	ext := filepath.Ext(destFileName)
	basename := strings.TrimSuffix(destFileName, ext)

	if !fileExists(destDir) {
		return 0
	}

	files, err := os.ReadDir(destDir)
	if err != nil {
		return 0
	}

	hashPattern := getRegex(fmt.Sprintf(`^%s\.[a-zA-Z0-9]+%s$`, regexp.QuoteMeta(basename), regexp.QuoteMeta(ext)))

	if dm.skipCleanup {
		if dm.debugMode {
			fmt.Println("    ⏭️  skipCleanup 生效，本次不清理")
		}
		return 0
	}

	now := time.Now().UnixNano()
	baseActive := dm.cache.SessionTime != 0 &&
		now-dm.cache.SessionTime < int64(prodProtectWindow)

	deletedCount := 0
	for _, file := range files {
		isBase := baseActive && dm.cache.ReleasedHashes[file.Name()]
		if file.Name() == destFileName || file.Name() == keepFileName ||
			dm.currentLive[file.Name()] || isBase ||
			dm.protectedHashes[file.Name()] {
			if dm.debugMode && isJSOrCSS(file.Name()) {
				fmt.Printf("    🛡️  保留(最新/当前现网/基线/HTML历史引用): %s\n", file.Name())
			}
			continue
		}

		if hashPattern.MatchString(file.Name()) {
			filePath := filepath.Join(destDir, file.Name())
			// 第二条保护规则：近期部署产生的 hash 先保留一阵。
			// 部分旧 hash 在仓库里已无任何引用（引用它的缓存页面/仓库外页面无法
			// 从 git 历史证明），只能按提交时间保留最近若干轮部署的产物
			if commitTime, ok := destFileCommitTime(filePath); ok &&
				time.Since(commitTime) < htmlProtectWindow {
				if isJSOrCSS(file.Name()) {
					fmt.Printf("    🛡️  保留(近期部署/提交于 %s): %s\n",
						commitTime.Local().Format("01-02 15:04"), file.Name())
				}
				continue
			}
			// 不在任何保护名单内的旧 hash（灰度中间产物、过期现网版）直接清理
			vcsDestDelete(filePath, dm.debugMode)
			if err := os.Remove(filePath); err == nil {
				deletedCount++
				if isJSOrCSS(file.Name()) {
					fmt.Printf("    🗑️  已清理旧hash: %s\n", file.Name())
				}
			}
		}
	}

	return deletedCount
}

// cleanHashOnly 独立清理模式：不部署复制，仅清理 dest 中配置目标范围内的旧
// hash 文件。保护语义与部署时完全一致：当前现网版本、HTML 历史引用（24h 窗口）、
// 会话基线、近期部署兜底；任何保护机制不可用则本次不清理（宁可多留不可误删）。
func (dm *DeployManager) cleanHashOnly() error {
	fmt.Println("🧹 开始清理 dest 旧 hash 文件（仅清理，不部署）")
	destKind := detectDestRepoKind(dm.destPath)
	fmt.Printf("📂 目标路径: %s（仓库类型: %s）\n\n", dm.destPath, destKind)

	// 先更新 dest 仓库，保证历史保护查询基于最新提交
	switch destKind {
	case destRepoGit:
		if err := dm.updateDestGit(); err != nil {
			fmt.Printf("⚠️  dest Git 拉取失败: %v，继续清理...\n", err)
		}
	case destRepoSvn:
		if err := dm.updateSvnRepo(); err != nil {
			fmt.Printf("⚠️  SVN更新失败: %v，继续清理...\n", err)
		}
	default:
		fmt.Printf("⚠️  目标目录未识别为 Git/SVN 仓库: %s\n", dm.destPath)
	}

	// 现网快照 + 会话基线（失败/首次记录时 skipCleanup 置位）
	dm.prepareDeploySession()
	if dm.skipCleanup {
		fmt.Println("ℹ️  现网快照不可用或首次记录，本次清理取消（宁可多留不可误删）")
		return nil
	}

	// HTML 历史保护名单
	if protected, ok := dm.buildProtectedHashes(); ok {
		dm.protectedHashes = protected
		if dm.debugMode {
			fmt.Printf("🛡️  HTML历史保护名单: %d 个资源\n", len(protected))
		}
	} else {
		fmt.Println("⚠️  无法构建HTML历史保护名单，本次清理取消（宁可多留不可误删）")
		return nil
	}

	fmt.Println("📦 开始清理...")
	totalDeleted := 0
	for _, fp := range dm.config.FilePaths {
		if strings.Contains(fp, "*") {
			deleted, err := dm.cleanWildcardPath(fp)
			if err != nil {
				fmt.Printf("⚠️  处理失败: %s - %v\n", fp, err)
				continue
			}
			totalDeleted += deleted
		} else {
			rel := strings.TrimPrefix(fp, "/")
			destFile := filepath.Join(dm.destPath, filepath.FromSlash(rel))
			totalDeleted += dm.cleanHashFiles(destFile, "")
		}
	}

	fmt.Printf("\n%s\n", strings.Repeat("=", 50))
	fmt.Printf("🧹 清理完成: 共清理 %d 个旧 hash 文件\n", totalDeleted)
	fmt.Printf("%s\n\n", strings.Repeat("=", 50))
	dm.openFolder()
	return nil
}

// cleanWildcardPath 清理通配符目标目录下的旧 hash 文件（不部署复制）。
// 以 dest 目录为准遍历（源目录可能已删除部分文件，dest 中遗留的也要清），
// 对每个带 hash 的文件还原其 base 文件路径，调用 cleanHashFiles 清理
// 同 base 的所有未受保护 hash 变体
func (dm *DeployManager) cleanWildcardPath(wildcardPath string) (int, error) {
	dirPath := strings.TrimSuffix(wildcardPath, "/*")
	destDirPath := filepath.Join(dm.destPath, dirPath)
	if !fileExists(destDirPath) {
		return 0, fmt.Errorf("目标目录不存在: %s", destDirPath)
	}

	total := 0
	seen := make(map[string]bool)
	err := filepath.WalkDir(destDirPath, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if d.Name() == ".git" || d.Name() == ".svn" {
				return filepath.SkipDir
			}
			return nil
		}
		m := reHashedBasenameParts.FindStringSubmatch(d.Name())
		if m == nil {
			return nil
		}
		base := m[1] + "." + m[2]
		if seen[base] {
			return nil
		}
		seen[base] = true
		destFile := filepath.Join(filepath.Dir(path), base)
		total += dm.cleanHashFiles(destFile, "")
		return nil
	})
	return total, err
}

// copyFileWithVersions 复制文件（包括hash版本）
func (dm *DeployManager) copyFileWithVersions(sourcePath, destPath string) (int, int, error) {
	// CDN无hash模式（mode 16/17）：只复制原始文件，不查找/复制hash版本，不清理
	if dm.noHash {
		return dm.copyBaseFileOnly(sourcePath, destPath)
	}
	versions := dm.findAllFileVersions(sourcePath)

	if len(versions) == 0 {
		return 0, 0, fmt.Errorf("源文件不存在: %s", sourcePath)
	}

	// 调试：打印发现的所有版本（仅JS/CSS）
	if dm.debugMode && isJSOrCSS(sourcePath) {
		fmt.Printf("  🔍 发现 %d 个版本: %s\n", len(versions), sourcePath)
		for _, v := range versions {
			fmt.Printf("    - %s (hash=%s, hasHash=%v)\n", v.Name, v.Hash, v.HasHash)
		}
	}

	// 筛选：只保留基础文件和最新的hash文件
	var versionsToProcess []FileVersion
	var baseVersion *FileVersion
	var latestHashVersion *FileVersion

	for i := range versions {
		if !versions[i].HasHash {
			baseVersion = &versions[i]
		} else if latestHashVersion == nil {
			latestHashVersion = &versions[i]
		}
	}

	if baseVersion != nil {
		versionsToProcess = append(versionsToProcess, *baseVersion)
	}
	if latestHashVersion != nil {
		versionsToProcess = append(versionsToProcess, *latestHashVersion)
	}

	destDir := filepath.Dir(destPath)
	if err := os.MkdirAll(destDir, 0755); err != nil {
		return 0, 0, err
	}

	// 清理旧的hash文件
	if latestHashVersion != nil {
		if dm.debugMode && isJSOrCSS(sourcePath) {
			fmt.Printf("  🧹 清理旧hash文件，保留: %s\n", latestHashVersion.Name)
		}
		dm.cleanHashFiles(destPath, latestHashVersion.Name)
	}

	copiedCount := 0
	skippedCount := 0

	for _, version := range versionsToProcess {
		var versionDestPath string
		if version.HasHash {
			versionDestPath = filepath.Join(destDir, version.Name)
		} else {
			versionDestPath = destPath
		}

		// 检查目标文件是否存在且内容相同
		if fileExists(versionDestPath) {
			// 快速检查：文件大小不同则内容一定不同，跳过 MD5 计算
			srcInfo, srcErr := os.Stat(version.Path)
			dstInfo, dstErr := os.Stat(versionDestPath)
			sameContent := false
			if srcErr == nil && dstErr == nil && srcInfo.Size() == dstInfo.Size() {
				// 大小相同，再比较 hash
				destHash, err := dm.cache.getCachedHash(versionDestPath)
				if err == nil && destHash == version.Hash {
					sameContent = true
				}
			}
			if sameContent {
				if dm.debugMode && isJSOrCSS(sourcePath) {
					fmt.Printf("  ⏭️  跳过（内容相同）: %s\n", version.Name)
				}
				skippedCount++
				continue
			}
		}

		// 复制文件
		if err := copyFile(version.Path, versionDestPath); err != nil {
			if isJSOrCSS(sourcePath) {
				fmt.Printf("  ❌ 复制失败: %s -> %s (%v)\n", version.Path, versionDestPath, err)
			}
			return copiedCount, skippedCount, err
		}
		copiedCount++
		// 复制后用源文件hash同步目标缓存，避免下次重复计算MD5
		if dstInfo, err := os.Stat(versionDestPath); err == nil {
			dm.cache.updateCache(versionDestPath, version.Hash, dstInfo.Size(), dstInfo.ModTime().UnixNano())
		}
		if isJSOrCSS(sourcePath) {
			fmt.Printf("  ✅ 已复制: %s -> %s\n", version.Name, destDir)
		}
	}

	return copiedCount, skippedCount, nil
}

// handleWildcardPath 处理通配符路径
func (dm *DeployManager) handleWildcardPath(wildcardPath string) (int, int, error) {
	dirPath := strings.TrimSuffix(wildcardPath, "/*")
	sourceDirPath := filepath.Join(dm.sourcePath, dirPath)
	destDirPath := filepath.Join(dm.destPath, dirPath)

	if !fileExists(sourceDirPath) {
		return 0, 0, fmt.Errorf("源目录不存在: %s", sourceDirPath)
	}

	// 先收集所有文件路径
	type fileTask struct {
		relToSource string
		destPath    string
	}
	var tasks []fileTask

	err := filepath.WalkDir(sourceDirPath, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}

		relPath, _ := filepath.Rel(sourceDirPath, path)
		destPath := filepath.Join(destDirPath, relPath)
		relToSource, _ := filepath.Rel(dm.sourcePath, path)
		tasks = append(tasks, fileTask{relToSource, destPath})
		return nil
	})
	if err != nil {
		return 0, 0, err
	}

	// 并发处理文件
	workerCount := runtime.NumCPU()
	if workerCount > 4 {
		workerCount = 4
	}
	results := runConcurrent(tasks, workerCount, func(t fileTask) fileCopyResult {
		copied, skipped, err := dm.copyFileWithVersions(t.relToSource, t.destPath)
		failed := 0
		if err != nil {
			fmt.Printf("⚠️  处理失败: %s - %v\n", t.destPath, err)
			failed = 1
		}
		return fileCopyResult{copied: copied, skipped: skipped, failed: failed}
	})

	var totalCopied, totalSkipped int
	for _, r := range results {
		totalCopied += r.copied
		totalSkipped += r.skipped
	}
	return totalCopied, totalSkipped, nil
}

// copyBaseFileOnly 无hash模式部署：仅复制原始文件到 dest
// （跳过hash版本查找与旧hash清理，缓存照常更新）
func (dm *DeployManager) copyBaseFileOnly(sourcePath, destPath string) (int, int, error) {
	sourceFullPath := filepath.Join(dm.sourcePath, sourcePath)
	if !fileExists(sourceFullPath) {
		return 0, 0, fmt.Errorf("源文件不存在: %s", sourcePath)
	}
	if err := copyFile(sourceFullPath, destPath); err != nil {
		return 0, 0, err
	}
	if info, err := os.Stat(sourceFullPath); err == nil {
		if hash, hashErr := getFileHash(sourceFullPath); hashErr == nil {
			dm.cache.updateCache(destPath, hash, info.Size(), info.ModTime().UnixNano())
		}
	}
	if dm.debugMode {
		fmt.Printf("  📄 无hash复制: %s\n", filepath.Base(destPath))
	}
	return 1, 0, nil
}

// isSvnRepo 检查是否是SVN仓库
func isSvnRepo(dir string) bool {
	cmd := exec.Command("svn", "info")
	cmd.Dir = dir
	return cmd.Run() == nil
}

// isGitRepo 检查是否是Git仓库
func isGitRepo(dir string) bool {
	cmd := exec.Command("git", "status")
	cmd.Dir = dir
	return cmd.Run() == nil
}

// updateSvnRepo 更新SVN仓库
func (dm *DeployManager) updateSvnRepo() error {
	fmt.Printf("🔄 正在更新SVN仓库: %s\n", dm.destPath)

	cmd := exec.Command("svn", "update")
	cmd.Dir = dm.destPath

	output, err := cmd.CombinedOutput()
	if err != nil {
		// 尝试清理
		if strings.Contains(string(output), "locked") || strings.Contains(string(output), "cleanup") {
			fmt.Println("🔧 检测到SVN锁定，尝试清理...")
			cleanCmd := exec.Command("svn", "cleanup")
			cleanCmd.Dir = dm.destPath
			if cleanErr := cleanCmd.Run(); cleanErr == nil {
				// 重试更新
				return dm.updateSvnRepo()
			}
		}
		return err
	}

	fmt.Printf("✅ SVN更新成功\n%s\n", string(output))
	return nil
}

// svnAddAll 添加所有新文件到SVN
func (dm *DeployManager) svnAddAll() error {
	fmt.Println("📁 正在添加新文件到SVN...")

	cmd := exec.Command("svn", "status")
	cmd.Dir = dm.destPath

	output, err := cmd.Output()
	if err != nil {
		return err
	}

	lines := strings.Split(string(output), "\n")
	addedCount := 0

	for _, line := range lines {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "?") {
			file := strings.TrimSpace(line[1:])
			if file == "" {
				continue
			}

			addCmd := exec.Command("svn", "add", file)
			addCmd.Dir = dm.destPath
			if addErr := addCmd.Run(); addErr == nil {
				addedCount++
			}
		}
	}

	if addedCount > 0 {
		fmt.Printf("✅ 已添加 %d 个新文件\n", addedCount)
	}

	return nil
}

// queryGitLatestCommit 查询指定目录的Git最新提交信息（可按author过滤）
func queryGitLatestCommit(dir string, authors []string) (string, string, error) {
	if !isGitRepo(dir) {
		return "", "", fmt.Errorf("目录不是Git仓库: %s", dir)
	}

	args := []string{"log", "-1", "--pretty=format:%h|%s"}
	for _, author := range authors {
		args = append(args, "--author="+author)
	}

	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	output, err := cmd.Output()
	if err != nil {
		return "", "", err
	}

	parts := strings.SplitN(string(output), "|", 2)
	if len(parts) != 2 {
		return "", "", fmt.Errorf("无法解析Git提交信息")
	}
	return strings.TrimSpace(parts[0]), strings.TrimSpace(parts[1]), nil
}

// getLatestGitCommit 获取Git最新提交信息
func (dm *DeployManager) getLatestGitCommit() (string, string, error) {
	authors := dm.config.GitAuthors
	if len(authors) == 0 {
		authors = []string{"chenchengpeng", "ccp"}
	}
	return queryGitLatestCommit(dm.sourcePath, authors)
}

// svnCommit 提交SVN更改
func (dm *DeployManager) svnCommit(message string) error {
	fmt.Printf("📤 正在提交SVN更改...\n")
	fmt.Printf("   提交信息: %s\n", message)

	// 先添加所有新文件
	dm.svnAddAll()

	// 创建临时文件存储提交信息
	tempFile := filepath.Join(dm.destPath, ".svn_commit_msg.tmp")
	defer os.Remove(tempFile)

	// 写入带BOM的UTF-8内容
	content := "\xEF\xBB\xBF" + message
	if err := os.WriteFile(tempFile, []byte(content), 0644); err != nil {
		return err
	}

	cmd := exec.Command("svn", "commit", "--file", tempFile, "--encoding", "UTF-8")
	cmd.Dir = dm.destPath

	output, err := cmd.CombinedOutput()
	if err != nil {
		if strings.Contains(string(output), "no changes") || strings.Contains(string(output), "没有修改") {
			fmt.Println("ℹ️  没有需要提交的更改")
			return nil
		}
		return fmt.Errorf("SVN提交失败: %s", string(output))
	}

	fmt.Printf("✅ SVN提交成功\n%s\n", string(output))
	return nil
}

// updateDestGit 拉取 dest git 仓库远程 master 分支
func (dm *DeployManager) updateDestGit() error {
	fmt.Printf("🔄 正在拉取 dest Git 仓库 (origin master): %s\n", dm.destPath)

	cmd := exec.Command("git", "pull", "origin", "master")
	cmd.Dir = dm.destPath

	output, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("git pull 失败: %s", strings.TrimSpace(string(output)))
	}

	fmt.Printf("✅ dest Git 拉取成功\n%s\n", string(output))
	return nil
}

// commitAndPushDest 提交 dest git 仓库的所有变更并推送远程 master。
// git add -A 自动暂存新增/修改/删除；push 失败时按约定尝试本机 7890 代理重试
func (dm *DeployManager) commitAndPushDest(message string) error {
	fmt.Printf("📤 正在提交 dest Git 更改...\n")
	fmt.Printf("   提交信息: %s\n", message)

	addCmd := exec.Command("git", "add", "-A")
	addCmd.Dir = dm.destPath
	if output, err := addCmd.CombinedOutput(); err != nil {
		return fmt.Errorf("git add 失败: %s", string(output))
	}

	statusCmd := exec.Command("git", "status", "--porcelain")
	statusCmd.Dir = dm.destPath
	statusOut, err := statusCmd.Output()
	if err != nil {
		return fmt.Errorf("获取git状态失败: %v", err)
	}
	if strings.TrimSpace(string(statusOut)) == "" {
		fmt.Println("ℹ️  没有需要提交的更改")
		return nil
	}

	commitCmd := exec.Command("git", "commit", "-m", message)
	commitCmd.Dir = dm.destPath
	if output, err := commitCmd.CombinedOutput(); err != nil {
		combined := string(output)
		if strings.Contains(combined, "nothing to commit") {
			fmt.Println("ℹ️  没有需要提交的更改")
			return nil
		}
		return fmt.Errorf("git commit 失败: %s", combined)
	}
	fmt.Printf("✅ git commit 成功\n")

	pushCmd := exec.Command("git", "push", "origin", "master")
	pushCmd.Dir = dm.destPath
	pushCmd.Stdout = os.Stdout
	pushCmd.Stderr = os.Stderr
	if err := pushCmd.Run(); err != nil {
		// 按约定：push 失败时尝试走本机 7890 代理重试
		fmt.Println("⚠️  push 失败，尝试使用本机代理 http://127.0.0.1:7890 重试...")
		retryCmd := exec.Command("git", "push", "origin", "master")
		retryCmd.Dir = dm.destPath
		retryCmd.Env = append(os.Environ(),
			"HTTPS_PROXY=http://127.0.0.1:7890",
			"HTTP_PROXY=http://127.0.0.1:7890")
		retryCmd.Stdout = os.Stdout
		retryCmd.Stderr = os.Stderr
		if retryErr := retryCmd.Run(); retryErr != nil {
			return fmt.Errorf("git push 失败（含代理重试）: %v", retryErr)
		}
	}

	fmt.Printf("✅ git push 成功\n")
	return nil
}

// revertAllSvn 回退dest SVN工作副本的所有本地变更（递归 svn revert）
// 恢复已修改的文件、撤销已标记的 svn add/delete，将工作副本还原到上次提交状态
func (dm *DeployManager) revertAllSvn() error {
	fmt.Println("\n" + strings.Repeat("=", 60))
	fmt.Println("🔄 回退dest SVN的所有本地变更")
	fmt.Println(strings.Repeat("=", 60))
	fmt.Printf("📂 目标路径: %s\n", dm.destPath)

	if dm.destPath == "" {
		return fmt.Errorf("未配置dest路径（请检查 version.config.json 中的 deploy.homeDestPath / deploy.companyDestPath）")
	}

	if !isSvnRepo(dm.destPath) {
		return fmt.Errorf("目标路径不是SVN仓库: %s", dm.destPath)
	}

	// 先尝试 cleanup，处理可能的中断/锁定状态
	cleanCmd := exec.Command("svn", "cleanup")
	cleanCmd.Dir = dm.destPath
	if cleanOut, cleanErr := cleanCmd.CombinedOutput(); cleanErr != nil {
		fmt.Printf("⚠️  SVN cleanup 失败（可忽略）: %v\n%s\n", cleanErr, string(cleanOut))
	} else {
		fmt.Println("✅ SVN cleanup 完成")
	}

	// 先查看当前待提交的状态，便于确认回退内容
	statusCmd := exec.Command("svn", "status")
	statusCmd.Dir = dm.destPath
	if statusOut, statusErr := statusCmd.Output(); statusErr == nil {
		statusStr := strings.TrimSpace(string(statusOut))
		if statusStr == "" {
			fmt.Println("ℹ️  当前没有待提交的本地变更，无需回退")
			fmt.Println(strings.Repeat("=", 60))
			return nil
		}
		fmt.Println("📋 待回退的本地变更:")
		fmt.Println(statusStr)
	}

	// 递归回退所有本地变更：恢复修改、撤销 add/delete 标记
	fmt.Println("\n⏳ 正在执行 svn revert -R . ...")
	revertCmd := exec.Command("svn", "revert", "-R", ".")
	revertCmd.Dir = dm.destPath
	revertCmd.Stdout = os.Stdout
	revertCmd.Stderr = os.Stderr
	if err := revertCmd.Run(); err != nil {
		return fmt.Errorf("SVN回退失败: %v", err)
	}

	fmt.Println("✅ dest SVN的所有本地变更已回退")
	fmt.Println(strings.Repeat("=", 60))
	return nil
}

// revertSrcGit 回退src Git工作区的所有本地改动
// git reset --hard HEAD 丢弃所有已跟踪的修改并撤销暂存的 add/delete
// git clean -fd 移除未跟踪的文件和目录（保留 .gitignore 排除的 node_modules/dist 等）
func (dm *DeployManager) revertSrcGit() error {
	fmt.Println("\n" + strings.Repeat("=", 60))
	fmt.Println("🔄 回退src Git工作区的所有本地改动")
	fmt.Println(strings.Repeat("=", 60))
	fmt.Printf("📂 源路径: %s\n", dm.sourcePath)

	if dm.sourcePath == "" {
		return fmt.Errorf("未设置source路径（请检查 version.config.json 中的 deploy.homeSourcePath / deploy.companySourcePath）")
	}

	if _, err := exec.LookPath("git"); err != nil {
		return fmt.Errorf("未找到git命令")
	}

	if !isGitRepo(dm.sourcePath) {
		return fmt.Errorf("源路径不是Git仓库: %s", dm.sourcePath)
	}

	// 1. git reset --hard HEAD: 丢弃所有已跟踪的修改和暂存区
	fmt.Println("⏳ 正在执行 git reset --hard HEAD ...")
	resetCmd := exec.Command("git", "reset", "--hard", "HEAD")
	resetCmd.Dir = dm.sourcePath
	resetCmd.Stdout = os.Stdout
	resetCmd.Stderr = os.Stderr
	if err := resetCmd.Run(); err != nil {
		return fmt.Errorf("git reset --hard HEAD 失败: %v", err)
	}

	// 2. git clean -fd: 移除未跟踪的文件和目录（不加 -x 以保留 .gitignore 排除项）
	fmt.Println("⏳ 正在执行 git clean -fd ...")
	cleanCmd := exec.Command("git", "clean", "-fd")
	cleanCmd.Dir = dm.sourcePath
	cleanCmd.Stdout = os.Stdout
	cleanCmd.Stderr = os.Stderr
	if err := cleanCmd.Run(); err != nil {
		return fmt.Errorf("git clean -fd 失败: %v", err)
	}

	// 3. 显示 git status 确认工作区已干净
	statusCmd := exec.Command("git", "status", "--porcelain")
	statusCmd.Dir = dm.sourcePath
	if statusOut, statusErr := statusCmd.Output(); statusErr == nil {
		statusStr := strings.TrimSpace(string(statusOut))
		if statusStr == "" {
			fmt.Println("✅ src Git工作区已干净")
		} else {
			fmt.Printf("⚠️  Git状态仍有未清理的变更:\n%s\n", statusStr)
		}
	}

	fmt.Println(strings.Repeat("=", 60))
	return nil
}

// revertDestGit 回退 dest git 仓库的所有本地变更。
// git reset --hard HEAD 恢复已跟踪文件的修改并清空暂存区（含删除标记），
// git clean -fd 移除未跟踪的新增文件/目录，整体还原到上次提交状态
func (dm *DeployManager) revertDestGit() error {
	fmt.Println("\n" + strings.Repeat("=", 60))
	fmt.Println("🔄 回退 dest Git 的所有本地变更")
	fmt.Println(strings.Repeat("=", 60))
	fmt.Printf("📂 目标路径: %s\n", dm.destPath)

	if dm.destPath == "" {
		return fmt.Errorf("未配置dest路径（请检查 version.config.json 中的 deploy.homeDestPath / deploy.companyDestPath）")
	}

	if !isGitRepo(dm.destPath) {
		return fmt.Errorf("目标路径不是Git仓库: %s", dm.destPath)
	}

	// 先查看当前待提交的状态，便于确认回退内容
	statusCmd := exec.Command("git", "status", "--porcelain")
	statusCmd.Dir = dm.destPath
	if statusOut, statusErr := statusCmd.Output(); statusErr == nil {
		statusStr := strings.TrimSpace(string(statusOut))
		if statusStr == "" {
			fmt.Println("ℹ️  当前没有待提交的本地变更，无需回退")
			fmt.Println(strings.Repeat("=", 60))
			return nil
		}
		fmt.Println("📋 待回退的本地变更:")
		fmt.Println(statusStr)
	}

	fmt.Println("\n⏳ 正在执行 git reset --hard HEAD ...")
	resetCmd := exec.Command("git", "reset", "--hard", "HEAD")
	resetCmd.Dir = dm.destPath
	resetCmd.Stdout = os.Stdout
	resetCmd.Stderr = os.Stderr
	if err := resetCmd.Run(); err != nil {
		return fmt.Errorf("git reset --hard 失败: %v", err)
	}

	fmt.Println("⏳ 正在执行 git clean -fd ...")
	cleanCmd := exec.Command("git", "clean", "-fd")
	cleanCmd.Dir = dm.destPath
	cleanCmd.Stdout = os.Stdout
	cleanCmd.Stderr = os.Stderr
	if err := cleanCmd.Run(); err != nil {
		return fmt.Errorf("git clean 失败: %v", err)
	}

	fmt.Println("✅ dest Git 的所有本地变更已回退")
	fmt.Println(strings.Repeat("=", 60))
	return nil
}

// revertDestVcs 按目标仓库类型分发回退：Git → reset --hard + clean，
// SVN → 递归 svn revert + 清理未版本化文件
func (dm *DeployManager) revertDestVcs() error {
	switch detectDestRepoKind(dm.destPath) {
	case destRepoSvn:
		return dm.revertAllSvn()
	case destRepoGit:
		return dm.revertDestGit()
	}
	return fmt.Errorf("目标路径不是 Git/SVN 仓库: %s", dm.destPath)
}

// openFolderInOS 跨平台打开文件夹
func openFolderInOS(path string) error {
	var cmd *exec.Cmd
	switch {
	case isWindows():
		cmd = exec.Command("explorer", path)
	case isDarwin():
		cmd = exec.Command("open", path)
	default:
		cmd = exec.Command("xdg-open", path)
	}
	return cmd.Start()
}

// openFolder 打开文件夹（避免重复打开）
func (dm *DeployManager) openFolder() {
	if dm.folderOpened {
		return
	}

	if !fileExists(dm.destPath) {
		fmt.Printf("⚠️  目标目录不存在: %s\n", dm.destPath)
		return
	}

	if err := openFolderInOS(dm.destPath); err != nil {
		fmt.Printf("❌ 打开文件夹失败: %v\n", err)
		fmt.Printf("📁 请手动打开: %s\n", dm.destPath)
	} else {
		fmt.Printf("✅ 已打开目标文件夹: %s\n", dm.destPath)
		dm.folderOpened = true
	}
}

// Run 执行部署
func (dm *DeployManager) Run(autoCommit bool, commitMessage string, htmlPath string, cdnDomain string) error {
	fmt.Println("🚀 开始部署操作...")
	fmt.Printf("📂 源路径: %s\n", dm.sourcePath)
	destKind := detectDestRepoKind(dm.destPath)
	fmt.Printf("📂 目标路径: %s（仓库类型: %s）\n\n", dm.destPath, destKind)

	// 文件hash缓存
	fmt.Printf("💾 已加载文件缓存: %d 个条目\n\n", len(dm.cache.Files))

	// 是否执行前置脚本
	if dm.config.ForcePreScript {
		scriptPath := filepath.Join(dm.sourcePath, filepath.FromSlash("scripts/bussiness/cdn.js"))
		if fileExists(scriptPath) {
			fmt.Printf("🔧 执行CDN前置脚本: %s\n", scriptPath)
			cmd := exec.Command("node", scriptPath)
			cmd.Dir = dm.sourcePath
			cmd.Stdout = os.Stdout
			cmd.Stderr = os.Stderr
			if err := cmd.Run(); err != nil {
				fmt.Printf("⚠️ 执行前置脚本失败: %v\n", err)
			} else {
				fmt.Println("✅ 前置脚本执行完成")
			}
			fmt.Println()
		}
	}

	// 按目标仓库类型更新（Git pull / SVN update）
	switch destKind {
	case destRepoGit:
		if err := dm.updateDestGit(); err != nil {
			fmt.Printf("⚠️  dest Git 拉取失败: %v，继续部署...\n", err)
		}
	case destRepoSvn:
		if err := dm.updateSvnRepo(); err != nil {
			fmt.Printf("⚠️  SVN更新失败: %v，继续部署...\n", err)
		}
	default:
		fmt.Printf("⚠️  目标目录未识别为 Git/SVN 仓库，跳过远程更新: %s\n", dm.destPath)
	}

	// 部署前抓现网快照，确定本次保留名单
	dm.prepareDeploySession()

	// 以 dest 里线上 HTML 的提交历史构建保护名单：当前上线版本 +
	// 保护窗口内所有版本的引用（含 CSS 里的 hash 图片）都必须保留。
	// 任何查询失败则本次不清理，宁可多留不可误删
	if protected, ok := dm.buildProtectedHashes(); ok {
		dm.protectedHashes = protected
		if dm.debugMode {
			fmt.Printf("🛡️  HTML历史保护名单: %d 个资源\n", len(protected))
		}
	} else if !dm.skipCleanup {
		dm.skipCleanup = true
		fmt.Println("⚠️  无法构建HTML历史保护名单，本次部署跳过旧hash清理")
	}

	fmt.Println("📦 开始复制文件...")

	// 并发处理文件，提高复制效率
	workerCount := runtime.NumCPU()
	if workerCount > 8 {
		workerCount = 8
	}
	filePaths := dm.config.FilePaths
	results := runConcurrent(filePaths, workerCount, func(filePath string) fileCopyResult {
		var copied, skipped int
		var err error
		if strings.Contains(filePath, "*") {
			copied, skipped, err = dm.handleWildcardPath(filePath)
		} else {
			sourcePath := strings.TrimPrefix(filePath, "/")
			destPath := filepath.Join(dm.destPath, sourcePath)
			copied, skipped, err = dm.copyFileWithVersions(sourcePath, destPath)
		}
		failed := 0
		if err != nil {
			fmt.Printf("⚠️  处理失败: %s - %v\n", filePath, err)
			failed = 1
		}
		return fileCopyResult{copied: copied, skipped: skipped, failed: failed}
	})

	var totalCopied, totalSkipped, totalFailed int
	for _, r := range results {
		totalCopied += r.copied
		totalSkipped += r.skipped
		totalFailed += r.failed
	}
	// 保存hash缓存到磁盘
	if err := dm.cache.Save(); err != nil {
		fmt.Printf("⚠️  缓存保存失败: %v\n", err)
	}

	// 打印汇总
	fmt.Printf("\n%s\n", strings.Repeat("=", 50))
	fmt.Printf("📊 复制完成: 复制 %d, 跳过 %d, 失败 %d\n", totalCopied, totalSkipped, totalFailed)

	// 验证 CDN 资源存在性
	if htmlPath != "" && cdnDomain != "" {
		fmt.Println("🔍 正在校验 HTML 中的 CDN 资源 (已忽略注释内容)...")
		if err := dm.validateCDNResources(htmlPath, cdnDomain); err != nil {
			return fmt.Errorf("❌ CDN 资源校验失败: %v", err)
		}
		fmt.Println("✅ 所有非注释 CDN 资源均已在目标目录就绪")
	}

	if totalFailed == 0 {
		fmt.Println("✅ 全部成功！")
	}
	fmt.Printf("%s\n\n", strings.Repeat("=", 50))

	// 自动提交（按目标仓库类型选择提交方式）
	if autoCommit {
		var commitFn func(string) error
		var repoLabel string
		switch destKind {
		case destRepoGit:
			commitFn = dm.commitAndPushDest
			repoLabel = "Git"
		case destRepoSvn:
			commitFn = dm.svnCommit
			repoLabel = "SVN"
		}

		if commitFn == nil {
			fmt.Printf("⚠️  目标目录未识别为 Git/SVN 仓库，跳过自动提交: %s\n", dm.destPath)
		} else {
			destMessage := ""
			if commitMessage != "" {
				// 使用自定义提交信息
				destMessage = commitMessage
				fmt.Printf("\n📝 使用自定义提交信息: %s\n", destMessage)
			} else {
				// 使用Git最新提交信息
				hash, message, err := dm.getLatestGitCommit()
				if err != nil {
					fmt.Printf("⚠️  获取Git提交信息失败: %v\n", err)
					fmt.Println("💡 请手动提交更改")
					return nil
				}
				destMessage = message
				fmt.Printf("\n📝 Git提交: %s - %s\n", hash, message)
			}

			fmt.Println("⏳ 2秒后开始提交...")
			time.Sleep(2 * time.Second)

			if err := commitFn(destMessage); err != nil {
				fmt.Printf("❌ 自动提交失败: %v\n", err)
			} else {
				fmt.Printf("🎉 自动提交完成！（%s）\n", repoLabel)
			}
		}
	}

	// 打开文件夹
	dm.openFolder()

	return nil
}

// validateCDNResources 校验 HTML 中的 CDN 资源是否在 destPath 中存在
func (dm *DeployManager) validateCDNResources(htmlPath string, cdnDomain string) error {
	content, err := os.ReadFile(htmlPath)
	if err != nil {
		return err
	}

	cleanContent := reHTMLComment.ReplaceAllString(string(content), "")

	pattern := regexp.QuoteMeta(cdnDomain) + "(/[^\\s'\"\\x60]+)"
	re := getRegex(pattern)
	matches := re.FindAllStringSubmatch(cleanContent, -1)

	for _, match := range matches {
		urlPath := match[1]

		// 移除查询参数
		if idx := strings.Index(urlPath, "?"); idx != -1 {
			urlPath = urlPath[:idx]
		}

		// 如果配置了前缀，则移除它以获取相对于 destPath 的路径
		relPath := urlPath
		if dm.config.CDNPathPrefix != "" && strings.HasPrefix(urlPath, dm.config.CDNPathPrefix) {
			relPath = strings.TrimPrefix(urlPath, dm.config.CDNPathPrefix)
		}

		// 拼接到目标目录进行检查
		checkPath := filepath.Join(dm.destPath, filepath.FromSlash(relPath))
		if !fileExists(checkPath) {
			return fmt.Errorf("文件缺失: %s -> (预检路径: %s)", urlPath, checkPath)
		}

		if dm.debugMode {
			fmt.Printf("  ✓ 校验通过: %s\n", urlPath)
		}
	}
	return nil
}

// runDeploy 执行部署流程
func (vm *VersionManager) runDeploy() {
	if !vm.config.Deploy.Enabled {
		return
	}

	fmt.Println("\n" + strings.Repeat("=", 60))
	fmt.Println("🚀 开始部署流程")
	fmt.Println(strings.Repeat("=", 60))

	dm := NewDeployManager(vm.config.Deploy, vm.debugMode)
	dm.noHash = vm.config.NoHash

	autoCommit := vm.config.Deploy.AutoCommit
	if vm.config.Deploy.Command == "copy-commit" {
		autoCommit = true
	}

	// 使用自定义提交信息（如果有）
	commitMsg := vm.commitMessage
	if commitMsg != "" {
		fmt.Printf("📝 使用自定义提交信息: %s\n", commitMsg)
	}

	if err := dm.Run(autoCommit, commitMsg, vm.config.SingleHTMLFile, vm.config.CDNDomain); err != nil {
		fmt.Printf("❌ 部署失败: %v\n", err)
		return
	}

	// 回滚HTML文件
	if vm.config.RollbackAfterDeploy && vm.config.SingleHTMLFile != "" {
		vm.rollbackHTMLFile(vm.config.SingleHTMLFile)

		// 如果设置了回滚后git commit和push
		if vm.config.GitCommitAfterRollback {
			vm.gitCommitAndPushAfterRollback(vm.config.SingleHTMLFile)
		}
	}

	// 更新folderOpened状态
	vm.folderOpened = dm.folderOpened
}

// shouldExcludeFromCDN 检查文件是否应该排除CDN替换
func (vm *VersionManager) shouldExcludeFromCDN(filePath string) bool {
	if len(vm.excludeMap) == 0 {
		return false
	}

	filename := filepath.Base(filePath)
	if idx := strings.Index(filename, "?"); idx != -1 {
		filename = filename[:idx]
	}

	// O(1) 查找
	if _, ok := vm.excludeMap[filename]; ok {
		vm.logf("    🚫 排除CDN替换: %s\n", filename)
		return true
	}
	// 退化为路径包含检查（无法用 map 优化，但频率低）
	for excludeFile := range vm.excludeMap {
		if strings.Contains(filePath, excludeFile) {
			vm.logf("    🚫 排除CDN替换: %s\n", filename)
			return true
		}
	}
	return false
}

// rollbackHTMLFile 使用git回滚HTML文件
func (vm *VersionManager) rollbackHTMLFile(htmlPath string) error {
	if !vm.config.RollbackAfterDeploy {
		return nil
	}

	absPath, _ := filepath.Abs(htmlPath)
	dir := filepath.Dir(absPath)
	filename := filepath.Base(absPath)

	fmt.Printf("\n🔄 正在回滚HTML文件: %s\n", filename)
	fmt.Printf("  📂 工作目录: %s\n", dir)

	// 检查git是否存在
	if _, err := exec.LookPath("git"); err != nil {
		fmt.Printf("⚠️  未找到git命令，跳过回滚\n")
		return nil
	}

	// 1. 使用 HEAD 强制从上次提交恢复，忽略已经 git add 的内容
	cmd := exec.Command("git", "checkout", "HEAD", "--", filename)
	cmd.Dir = dir

	if _, err := cmd.CombinedOutput(); err != nil {
		// 如果 HEAD 恢复失败，尝试普通的 checkout
		cmdRetry := exec.Command("git", "checkout", "--", filename)
		cmdRetry.Dir = dir
		if outRetry, errRetry := cmdRetry.CombinedOutput(); errRetry != nil {
			fmt.Printf("❌ Git回滚失败: %v\n", errRetry)
			fmt.Printf("   Output: %s\n", string(outRetry))
			return errRetry
		}
	}

	// 2. 打印回滚后的 Git 状态以便确认
	statusCmd := exec.Command("git", "status", "-s", filename)
	statusCmd.Dir = dir
	if statusOut, err := statusCmd.Output(); err == nil {
		statusStr := strings.TrimSpace(string(statusOut))
		if statusStr == "" {
			fmt.Printf("  ✓ 文件状态: 已恢复至 Commit 状态 (Clean)\n")
		} else {
			fmt.Printf("  📊 Git状态: %s\n", statusStr)
		}
	}

	fmt.Printf("\n✅ HTML文件已回滚到CDN替换前的状态\n")
	return nil
}

// gitCommitAndPushAfterRollback 在回滚HTML后执行全量git commit和push
func (vm *VersionManager) gitCommitAndPushAfterRollback(htmlPath string) error {
	absPath, _ := filepath.Abs(htmlPath)
	dir := filepath.Dir(absPath)

	fmt.Printf("\n🔄 正在执行Git提交和推送...\n")
	fmt.Printf("  📂 工作目录: %s\n", dir)

	// 检查git是否存在
	if _, err := exec.LookPath("git"); err != nil {
		fmt.Printf("⚠️  未找到git命令，跳过提交\n")
		return nil
	}

	// 1. 获取最新的git commit hash作为提交信息
	hash, _, err := vm.getLatestGitCommitForRollback(dir)
	if err != nil {
		fmt.Printf("⚠️  获取Git提交信息失败: %v\n", err)
		// 使用时间戳作为备选提交信息
		hash = time.Now().Format("20060102150405")
	}
	commitMsg := fmt.Sprintf("切hash（%s)", hash)

	// 2. 执行 git add -A (全量添加)
	fmt.Printf("  📁 执行 git add -A...\n")
	addCmd := exec.Command("git", "add", "-A")
	addCmd.Dir = dir
	if output, err := addCmd.CombinedOutput(); err != nil {
		fmt.Printf("❌ Git add 失败: %v\n", err)
		fmt.Printf("   Output: %s\n", string(output))
		return err
	}

	// 3. 检查是否有变更需要提交
	statusCmd := exec.Command("git", "status", "--porcelain")
	statusCmd.Dir = dir
	statusOutput, err := statusCmd.Output()
	if err != nil {
		fmt.Printf("❌ 获取Git状态失败: %v\n", err)
		return err
	}

	if strings.TrimSpace(string(statusOutput)) == "" {
		fmt.Printf("  ℹ️  没有变更需要提交\n")
		return nil
	}

	// 4. 执行 git commit
	fmt.Printf("  📝 执行 git commit -m \"%s\"...\n", commitMsg)
	commitCmd := exec.Command("git", "commit", "-m", commitMsg)
	commitCmd.Dir = dir
	if output, err := commitCmd.CombinedOutput(); err != nil {
		fmt.Printf("❌ Git commit 失败: %v\n", err)
		fmt.Printf("   Output: %s\n", string(output))
		return err
	}
	fmt.Printf("  ✅ Git commit 成功\n")

	// 5. 执行 git pull --rebase 同步远程更改
	fmt.Printf("  🔄 执行 git pull --rebase 同步远程更改...\n")
	pullCmd := exec.Command("git", "pull", "--rebase")
	pullCmd.Dir = dir
	pullCmd.Stdout = os.Stdout
	pullCmd.Stderr = os.Stderr
	if err := pullCmd.Run(); err != nil {
		fmt.Printf("❌ Git pull --rebase 失败: %v\n", err)
		fmt.Printf("   已中止推送，请手动解决冲突后重试\n")
		return err
	}

	// 6. 执行 git push
	fmt.Printf("  🚀 执行 git push...\n")
	pushCmd := exec.Command("git", "push")
	pushCmd.Dir = dir
	pushCmd.Stdout = os.Stdout
	pushCmd.Stderr = os.Stderr
	if err := pushCmd.Run(); err != nil {
		fmt.Printf("❌ Git push 失败: %v\n", err)
		return err
	}
	fmt.Printf("  ✅ Git push 成功\n")

	fmt.Printf("\n🎉 Git提交和推送完成！\n")
	return nil
}

// getLatestGitCommitForRollback 获取指定目录的最新git提交hash
func (vm *VersionManager) getLatestGitCommitForRollback(dir string) (string, string, error) {
	return queryGitLatestCommit(dir, nil)
}

// openFolder 打开文件夹（避免重复打开）
func (vm *VersionManager) openFolder(folderPath string) {
	if vm.folderOpened {
		if vm.debugMode {
			fmt.Printf("📁 文件夹已打开，跳过: %s\n", folderPath)
		}
		return
	}

	if !fileExists(folderPath) {
		fmt.Printf("⚠️  目标目录不存在: %s\n", folderPath)
		return
	}

	if err := openFolderInOS(folderPath); err != nil {
		fmt.Printf("❌ 打开文件夹失败: %v\n", err)
		fmt.Printf("📁 请手动打开: %s\n", folderPath)
	} else {
		fmt.Printf("✅ 已打开目标文件夹: %s\n", folderPath)
		vm.folderOpened = true
	}
}

// isWindows 检查是否Windows系统
func isWindows() bool {
	return runtime.GOOS == "windows"
}

// isDarwin 检查是否macOS系统
func isDarwin() bool {
	return runtime.GOOS == "darwin"
}

// loadConfig 加载配置文件
func loadConfig(configPath string) (*Config, error) {
	data, err := os.ReadFile(configPath)
	if err != nil {
		return nil, err
	}

	var config Config
	if err := json.Unmarshal(data, &config); err != nil {
		return nil, err
	}

	// 设置默认值
	if config.RootDir == "" {
		config.RootDir = "."
	}
	if config.HashLength == 0 {
		config.HashLength = 8
	}
	if len(config.ExcludeDirs) == 0 {
		config.ExcludeDirs = []string{"node_modules", ".git", "dist", "build"}
	}

	// 根据环境变量 IS_HOME 选择路径
	isHome := isHomeEnv()
	envLabel := "Office"
	if isHome {
		envLabel = "Home"
	}
	fmt.Printf("📍 当前环境: %s\n", envLabel)

	if config.HomeHTMLFile != "" || config.CompanyHTMLFile != "" {
		if isHome {
			if config.HomeHTMLFile != "" {
				config.SingleHTMLFile = config.HomeHTMLFile
				fmt.Printf("🏠 使用家里电脑路径: %s\n", config.SingleHTMLFile)
			}
		} else {
			if config.CompanyHTMLFile != "" {
				config.SingleHTMLFile = config.CompanyHTMLFile
				fmt.Printf("🏢 使用公司电脑路径: %s\n", config.SingleHTMLFile)
			}
		}
	}

	return &config, nil
}

func main() {
	logFile, logErr := initLogFile(20)
	if logErr == nil {
		defer logFile.Close()
		fmt.Printf("📝 日志文件: %s\n\n", logFile.Name())
	} else {
		fmt.Printf("⚠️  日志文件创建失败（仅控制台输出）: %v\n\n", logErr)
	}

	configPath := flag.String("config", "version.config.json", "配置文件路径")
	htmlFile := flag.String("file", "", "单个HTML文件路径（命令行指定，优先级高于配置文件）")
	scanAll := flag.Bool("all", false, "扫描所有HTML文件")
	cdnDomain := flag.String("cdn", "", "CDN域名")
	debugMode := flag.Bool("debug", false, "调试模式（显示详细日志）")
	deployOnly := flag.Bool("deploy", false, "仅执行部署（不处理hash）")
	deployCommit := flag.Bool("deploy-commit", false, "部署并自动提交")
	deployMode := flag.Int("mode", 6, "部署模式：1=pre-script+copy, 2=pre-script+copy-commit, 3=pre-script+copy-commit+回滚HTML+git commit&push, 4=不替换CDN+copy, 5=不替换CDN+copy-commit, 6=不替换CDN+copy-commit+回滚HTML+git commit&push, 7=copy(排除cdnExcludeFiles), 8=copy-commit(排除cdnExcludeFiles), 9=copy-commit+回滚HTML+git commit&push(排除cdnExcludeFiles), 16=CDN替换+跳过hash+copy, 17=CDN替换+跳过hash+copy-commit")
	commitMessage := flag.String("message", "", "自定义SVN提交信息（不指定则使用Git最新提交信息）")
	revertSvn := flag.Bool("revert-svn", false, "回退dest仓库的所有本地变更（自动识别 Git/SVN；Git 用 reset --hard + clean，SVN 用递归 revert）")
	cleanHash := flag.Bool("clean-hash", false, "仅清理dest旧hash文件（不部署不复制；保护现网/HTML历史/近期部署）")
	revertGit := flag.Bool("revert-git", false, "回退src Git工作区的所有本地改动（git reset --hard HEAD + git clean -fd）")
	dryRun := flag.Bool("dry-run", false, "预览模式（只显示将要执行的操作，不实际修改文件）")

	flag.Parse()

	config, err := loadConfig(*configPath)
	fmt.Printf("📂 加载配置文件: %s\n", *configPath)
	// configlog
	startTime := time.Now()

	if err != nil {
		config = &Config{
			RootDir:     ".",
			HashLength:  8,
			ExcludeDirs: []string{"node_modules", ".git", "dist", "build"},
		}
	}

	if *cdnDomain != "" {
		config.CDNDomain = *cdnDomain
	}

	// 应用部署模式
	if *deployMode > 0 {
		config.Deploy.Enabled = true // 强制开启部署
		switch *deployMode {
		case ModePreScriptCopy:
			config.Deploy.AutoCommit = false
			config.Deploy.ForcePreScript = false
			config.Deploy.Command = "copy"
		case ModePreScriptCopyCommit:
			config.Deploy.AutoCommit = true
			config.Deploy.ForcePreScript = false
			config.Deploy.Command = "copy-commit"
		case ModePreScriptCopyCommitRollback:
			config.Deploy.AutoCommit = true
			config.Deploy.ForcePreScript = false
			config.Deploy.Command = "copy-commit"
			config.RollbackAfterDeploy = true    // 回滚HTML
			config.GitCommitAfterRollback = true // 回滚后执行git commit和push
		case ModeCopyNoCDN:
			config.Deploy.AutoCommit = false
			config.Deploy.ForcePreScript = false
			config.Deploy.Command = "copy"
			config.CDNDomain = "" // 不替换CDN
		case ModeCopyCommitNoCDN:
			config.Deploy.AutoCommit = true
			config.Deploy.ForcePreScript = false
			config.Deploy.Command = "copy-commit"
			config.CDNDomain = "" // 不替换CDN
		case ModeCopyCommitRollbackNoCDN:
			config.Deploy.AutoCommit = true
			config.Deploy.ForcePreScript = false
			config.Deploy.Command = "copy-commit"
			config.CDNDomain = ""                // 不替换CDN
			config.RollbackAfterDeploy = true    // 回滚HTML
			config.GitCommitAfterRollback = true // 回滚后执行git commit和push
		case ModeCopyExcludeCDN:
			config.Deploy.AutoCommit = false
			config.Deploy.ForcePreScript = false
			config.Deploy.Command = "copy"
			config.CDNExcludeFiles = []string{} // 清空CDN排除文件
		case ModeCopyCommitExcludeCDN:
			config.Deploy.AutoCommit = true
			config.Deploy.ForcePreScript = false
			config.Deploy.Command = "copy-commit"
			config.CDNExcludeFiles = []string{} // 清空CDN排除文件
		case ModeCopyCommitRollbackExcludeCDN:
			config.Deploy.AutoCommit = true
			config.Deploy.ForcePreScript = false
			config.Deploy.Command = "copy-commit"
			config.RollbackAfterDeploy = true    // 回滚HTML
			config.GitCommitAfterRollback = true // 回滚后执行git commit和push
			config.CDNExcludeFiles = []string{}  // 清空CDN排除文件
		case 16:
			// CDN替换 + 跳过hash：保留CDN域名，部署只复制原始文件
			config.Deploy.AutoCommit = false
			config.Deploy.ForcePreScript = false
			config.Deploy.Command = "copy"
			config.NoHash = true
		case 17:
			// CDN替换 + 跳过hash + 自动提交
			config.Deploy.AutoCommit = true
			config.Deploy.ForcePreScript = false
			config.Deploy.Command = "copy-commit"
			config.NoHash = true
		}
	}
	// deployMode
	fmt.Printf("📋 部署模式: %d\n", *deployMode)

	// 预览模式：显示配置后直接退出
	if *dryRun {
		fmt.Println("\n🔍 === 预览模式（dry-run）===")
		fmt.Printf("  HTML文件: %s\n", config.SingleHTMLFile)
		fmt.Printf("  CDN域名: %s\n", config.CDNDomain)
		fmt.Printf("  HashLength: %d\n", config.HashLength)
		fmt.Printf("  部署: enabled=%v, command=%s, autoCommit=%v\n",
			config.Deploy.Enabled, config.Deploy.Command, config.Deploy.AutoCommit)
		fmt.Printf("  回滚HTML: %v, Git提交: %v\n", config.RollbackAfterDeploy, config.GitCommitAfterRollback)
		fmt.Printf("  源路径(Home): %s\n", config.Deploy.HomeSourcePath)
		fmt.Printf("  目标路径(Home): %s\n", config.Deploy.HomeDestPath)
		fmt.Printf("  源路径(Office): %s\n", config.Deploy.CompanySourcePath)
		fmt.Printf("  目标路径(Office): %s\n", config.Deploy.CompanyDestPath)
		fmt.Printf("  现网快照URLs(%d项):\n", len(config.Deploy.ProdHTMLURLs))
		for _, url := range config.Deploy.ProdHTMLURLs {
			fmt.Printf("    - %s\n", url)
		}
		fmt.Printf("  部署文件列表(%d项):\n", len(config.Deploy.FilePaths))
		for _, fp := range config.Deploy.FilePaths {
			fmt.Printf("    - %s\n", fp)
		}
		if len(config.ExtraHashResources) > 0 {
			fmt.Printf("  额外hash资源(%d项):\n", len(config.ExtraHashResources))
			for _, r := range config.ExtraHashResources {
				fmt.Printf("    - %s\n", r)
			}
		}

		// 旧 hash 保留诊断：打印窗口边界怎么取、最终保护名单，只读不修改
		fmt.Printf("\n🛡️  === 旧hash保留诊断（只读）===\n")
		fmt.Printf("  保护窗口: %v（用户 HTML 缓存约 12h，取 2 倍保险）\n", htmlProtectWindow)
		dm := NewDeployManager(config.Deploy, true)
		historyLogTrace = func(format string, args ...interface{}) { fmt.Printf(format+"\n", args...) }
		if protected, ok := dm.buildProtectedHashes(); ok {
			fmt.Printf("\n  ✅ 保护名单构建成功: %d 个资源\n", len(protected))
			names := make([]string, 0, len(protected))
			for name := range protected {
				names = append(names, name)
			}
			sort.Strings(names)
			for _, name := range names {
				fmt.Printf("      🛡️ %s\n", name)
			}
			dm.diagnoseHashRetention(protected)
		} else {
			fmt.Printf("\n  ⚠️ 保护名单构建失败 —— 生产环境本次部署将跳过旧hash清理（不删任何文件）\n")
		}
		historyLogTrace = func(format string, args ...interface{}) {}

		fmt.Println("  （预览模式，不执行实际操作）")
		return
	}

	vm := NewVersionManager(*config, *debugMode)

	// 设置自定义提交信息
	if *commitMessage != "" {
		vm.commitMessage = *commitMessage
	}

	// 回退本地变更：-revert-svn 回退dest仓库（自动识别 Git/SVN），-revert-git 回退src Git，可同时使用
	if *revertSvn || *revertGit {
		dm := NewDeployManager(config.Deploy, *debugMode)
		if *revertSvn {
			if err := dm.revertDestVcs(); err != nil {
				fmt.Printf("❌ SVN回退失败: %v\n", err)
				os.Exit(1)
			}
		}
		if *revertGit {
			if err := dm.revertSrcGit(); err != nil {
				fmt.Printf("❌ Git回退失败: %v\n", err)
				os.Exit(1)
			}
		}
		duration := time.Since(startTime)
		fmt.Printf("\n⏱️  总运行时间: %v\n", duration)
		return
	}

	// 仅清理dest旧hash文件：-clean-hash
	if *cleanHash {
		dm := NewDeployManager(config.Deploy, *debugMode)
		if err := dm.cleanHashOnly(); err != nil {
			fmt.Printf("❌ 清理失败: %v\n", err)
			os.Exit(1)
		}
		duration := time.Since(startTime)
		fmt.Printf("\n⏱️  总运行时间: %v\n", duration)
		return
	}

	// 仅部署模式
	if *deployOnly || *deployCommit {
		if !config.Deploy.Enabled {
			fmt.Println("❌ 部署功能未启用，请在配置文件中设置 deploy.enabled = true")
			os.Exit(1)
		}

		dm := NewDeployManager(config.Deploy, *debugMode)
		autoCommit := *deployCommit || config.Deploy.AutoCommit || config.Deploy.Command == "copy-commit"

		if err := dm.Run(autoCommit, *commitMessage, vm.config.SingleHTMLFile, vm.config.CDNDomain); err != nil {
			fmt.Printf("❌ 部署失败: %v\n", err)
			os.Exit(1)
		}

		duration := time.Since(startTime)
		fmt.Printf("\n⏱️  总运行时间: %v\n", duration)
		return
	}

	// 显示处理的组件配置
	if len(config.IncludeComponents) > 0 {
		fmt.Printf("📋 指定处理组件: %v\n", config.IncludeComponents)
	} else {
		fmt.Printf("📋 处理所有组件\n")
	}

	// 显示CDN排除文件
	if len(config.CDNExcludeFiles) > 0 {
		fmt.Printf("🚫 CDN排除文件: %v\n", config.CDNExcludeFiles)
	}

	// 确定要处理的单个HTML文件（优先级：命令行 > 配置文件）
	targetHTMLFile := *htmlFile
	if targetHTMLFile == "" && config.SingleHTMLFile != "" {
		targetHTMLFile = config.SingleHTMLFile
		fmt.Printf("📋 使用配置文件中的HTML文件\n")
	}

	// 处理单个文件
	if targetHTMLFile != "" {
		if err := vm.processHTMLFile(targetHTMLFile); err != nil {
			fmt.Printf("❌ 处理失败: %v\n", err)
			os.Exit(1)
		}
		duration := time.Since(startTime)
		fmt.Printf("\n⏱️  总运行时间: %v\n", duration)
		return
	}

	// 扫描所有文件
	if *scanAll {
		htmlFiles := vm.findAllHTMLFiles()
		fmt.Printf("📋 找到 %d 个HTML文件\n\n", len(htmlFiles))
		if len(htmlFiles) > 0 {
			vm.processMultipleHTMLFiles(htmlFiles)
		} else {
			fmt.Println("❌ 未找到HTML文件")
		}
		return
	}

	// 使用配置文件中的HTML列表
	if len(config.HTMLFiles) > 0 {
		vm.processMultipleHTMLFiles(config.HTMLFiles)
	} else {
		fmt.Println("⚠️  未指定要处理的HTML文件")
		fmt.Println("使用 -file 指定文件, -all 扫描所有, 或在配置文件中指定")
		fmt.Println("\n🚀 部署命令:")
		fmt.Println("  -deploy        仅执行部署（不处理hash）")
		fmt.Println("  -deploy-commit 部署并自动提交SVN")
		flag.Usage()
	}
}
