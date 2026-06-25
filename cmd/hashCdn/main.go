package main

import (
	"crypto/md5"
	"encoding/hex"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"sync"
	"time"
)

// 包级正则：编译一次，全局复用
var (
	reHashInFilename = regexp.MustCompile(`^(.+)\.([a-f0-9]{8})\.(css|js|jpg|jpeg|png|gif|svg|webp|ico)$`)
	reOldHashSuffix  = regexp.MustCompile(`\.[a-f0-9]{8}$`)
	reCSSUrlCollect  = regexp.MustCompile(`url\(['"]?([^'")\s]+)['"]?\)`)
	reCSSUrlReplace  = regexp.MustCompile(`url\(\s*(['"]?)([^'")\s]+)(['"]?)\s*\)`)
	reHTMLCSSLink    = regexp.MustCompile(`<link[^>]*href\s*=\s*['"]([^'"]+\.css)['"]`)
	reHTMLJSScript   = regexp.MustCompile(`<script[^>]*src\s*=\s*['"]([^'"]+\.js)['"]`)
	reHTMLComment    = regexp.MustCompile(`(?s)<!--.*?-->`)

	// CDN 全局替换用的正则（编译一次）
	reCDNCSS = regexp.MustCompile(`(<link[^>]*href\s*=\s*['"])([^'"]+)(['"][^>]*>)`)
	reCDNJS  = regexp.MustCompile(`(<script[^>]*src\s*=\s*['"])([^'"]+)(['"][^>]*>)`)

	// 动态正则缓存：避免同一 pattern 重复编译
	regexCache sync.Map
)

// getRegex 从缓存获取编译好的正则，未命中则编译并缓存
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
	ReplaceAllWithCDN    bool     `json:"replaceAllWithCDN"` // 替换所有资源为CDN路径
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
	ForcePreScript    bool     `json:"-"`             // 运行时覆盖：是否强制执行前置脚本
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

// gitAddFile 执行 git add 命令
func (vm *VersionManager) gitAddFile(filePath string) {
	// 简单检查git是否存在
	if _, err := exec.LookPath("git"); err != nil {
		return
	}

	cmd := exec.Command("git", "add", filepath.Base(filePath))
	cmd.Dir = filepath.Dir(filePath)

	if output, err := cmd.CombinedOutput(); err != nil {
		if vm.debugMode {
			fmt.Printf("      ⚠️  Git add 失败: %s (%v)\n", filepath.Base(filePath), err)
			fmt.Printf("      Output: %s\n", string(output))
		}
	} else {
		if vm.debugMode {
			fmt.Printf("    ➕ Git add: %s\n", filepath.Base(filePath))
		}
	}
}

// runNodeCopyScript 执行Node.js复制脚本
func (vm *VersionManager) runNodeCopyScript() {
	isHome := os.Getenv("IS_HOME")
	var scriptPath string

	if isHome == "1" {
		fmt.Println("🏠 当前环境: Home")
		scriptPath = `D:\self_project\js_project\miaowei\test\auto\normal.js`
	} else {
		fmt.Println("🏢 当前环境: Office")
		scriptPath = `d:\project\my_web_project\web\train\miaov-disk\Cloud_disk\test\auto\normal.js`
	}

	fmt.Printf("🚀 执行部署脚本: node %s copy\n", scriptPath)

	// 检查 node 是否存在
	if _, err := exec.LookPath("node"); err != nil {
		fmt.Printf("⚠️  未找到 node 命令，跳过脚本执行\n")
		return
	}

	cmd := exec.Command("node", scriptPath, "copy")
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	if err := cmd.Run(); err != nil {
		fmt.Printf("❌ 脚本执行失败: %v\n", err)
	} else {
		fmt.Println("✅ 脚本执行成功")
	}
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
	file, err := os.Open(filePath)
	if err != nil {
		return "", err
	}
	defer file.Close()

	hash := md5.New()
	if _, err := io.Copy(hash, file); err != nil {
		return "", err
	}

	hashBytes := hash.Sum(nil)
	hashString := hex.EncodeToString(hashBytes)

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

// findAndDeleteOldHashFiles 查找并删除旧的hash文件
func (vm *VersionManager) findAndDeleteOldHashFiles(dir, basename, ext, currentHash string) error {
	if isJSOrCSS(basename + ext) {
		vm.logf("  🔍 查找旧hash文件: %s%s (当前hash: %s)\n", basename, ext, currentHash)
	}

	hashPattern := fmt.Sprintf(`^%s\.([a-f0-9]{8})%s$`, regexp.QuoteMeta(basename), regexp.QuoteMeta(ext))
	re := getRegex(hashPattern)

	files, err := os.ReadDir(dir)
	if err != nil {
		return err
	}

	var deletedCount int
	for _, file := range files {
		if file.IsDir() {
			continue
		}
		filename := file.Name()
		hashMatches := re.FindStringSubmatch(filename)
		if len(hashMatches) >= 2 && hashMatches[1] != currentHash {
			oldFilePath := filepath.Join(dir, filename)
			vm.svnDeleteFile(oldFilePath)
			if err := os.Remove(oldFilePath); err != nil {
				if isJSOrCSS(filename) {
					fmt.Printf("    ⚠️  删除失败: %s\n", filename)
				}
			} else {
				if isJSOrCSS(filename) {
					fmt.Printf("    🗑️  已删除: %s\n", filename)
				}
				deletedCount++
			}
		}
	}

	if deletedCount > 0 {
		vm.logf("  ✅ 共删除 %d 个旧文件\n", deletedCount)
	}

	return nil
}

// svnDeleteFile 通知SVN删除文件（如果在SVN仓库中）
func (vm *VersionManager) svnDeleteFile(filePath string) {
	if _, err := exec.LookPath("svn"); err != nil {
		return
	}

	dir := filepath.Dir(filePath)
	filename := filepath.Base(filePath)

	cmd := exec.Command("svn", "delete", "--keep-local", filename)
	cmd.Dir = dir

	if output, err := cmd.CombinedOutput(); err != nil {
		if vm.debugMode {
			fmt.Printf("      ⚠️  SVN delete 失败: %s (%v)\n", filename, err)
			fmt.Printf("      Output: %s\n", string(output))
		}
	} else {
		if vm.debugMode {
			fmt.Printf("    📝 SVN delete: %s\n", filename)
		}
	}
}

// processHTMLFile 处理单个HTML文件及其关联资源
func (vm *VersionManager) processHTMLFile(htmlPath string) error {
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

// processMultipleHTMLFiles 批量处理多个HTML文件
func (vm *VersionManager) processMultipleHTMLFiles(htmlPaths []string) {
	fmt.Println("🚀 开始批量处理HTML文件...\n")

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
	data, err := os.ReadFile(src)
	if err != nil {
		return err
	}
	return os.WriteFile(dst, data, 0644)
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

	// 计算hash（基于源文件）
	hash, err := vm.calculateFileHash(sourcePath)
	if err != nil {
		return nil, err
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

	// 复制源文件到新路径
	if err := copyFile(sourcePath, newPath); err != nil {
		return nil, fmt.Errorf("复制文件失败: %v", err)
	}

	vm.gitAddFile(newPath) // 自动添加到git

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

	pattern := getRegex(fmt.Sprintf(`^%s\.[a-f0-9]{8}\%s$`, regexp.QuoteMeta(nameWithoutExt), regexp.QuoteMeta(ext)))

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
				os.Rename(hashedCssPath, finalCssPath)
				hashedCssPath = finalCssPath
				hashedCssFilename = finalCssFilename
				originalHash = newHash
			}
		}
	}

	vm.gitAddFile(hashedCssPath) // 自动添加到git

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

func (vm *VersionManager) updateHTMLReferences(htmlPath string, resources map[string]map[string]string) error {
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
				fmt.Sprintf(`(%s['"])(%s)(['"][^>]*>)`, tagAttr, escapedPath),
				fmt.Sprintf(`(%s['"])(\.{1,2}[/\\]%s)(['"][^>]*>)`, tagAttr, escapedPath),
			}

			matched := false
			for _, pattern := range patterns {
				re := getRegex(pattern)
				if re.MatchString(contentStr) {
					newContent := re.ReplaceAllStringFunc(contentStr, func(match string) string {
						submatches := re.FindStringSubmatch(match)
						if len(submatches) >= 4 {
							prefix := submatches[1]
							oldPath := submatches[2]
							suffix := submatches[3]

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

	if updated {
		if err := os.WriteFile(htmlPath, []byte(contentStr), 0644); err != nil {
			return err
		}
		fmt.Printf("\n✅ HTML文件已更新\n")
	} else {
		fmt.Printf("\n⚠️  没有内容需要更新\n")
	}

	// 执行部署脚本（如果启用）
	if vm.config.Deploy.Enabled {
		vm.runDeploy()
	} else {
		vm.runNodeCopyScript()
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
	sourceHashCache sync.Map // filePath -> hash string
}

// NewDeployManager 创建部署管理器
func NewDeployManager(config DeployConfig, debugMode bool) *DeployManager {
	isHome := os.Getenv("IS_HOME") == "1"

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
		hash, _ := getFileHash(fullPath)
		if hash != "" {
			dm.sourceHashCache.Store(fullPath, hash)
		}
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
			hash, _ := getFileHash(filePath)
			if hash != "" {
				dm.sourceHashCache.Store(filePath, hash)
			}
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

// FileVersion 文件版本信息
type FileVersion struct {
	Path    string
	Name    string
	HasHash bool
	ModTime time.Time
	Hash    string
}

// cleanHashFiles 清理旧的hash文件
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

	deletedCount := 0
	for _, file := range files {
		if file.Name() == destFileName || file.Name() == keepFileName {
			if dm.debugMode && isJSOrCSS(file.Name()) {
				fmt.Printf("    🛡️  保留: %s\n", file.Name())
			}
			continue
		}

		if hashPattern.MatchString(file.Name()) {
			filePath := filepath.Join(destDir, file.Name())
			// 先通知SVN删除，再删除本地文件
			dm.svnDeleteFile(filePath)
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

// svnDeleteFile 通知SVN删除文件（如果在SVN仓库中）
func (dm *DeployManager) svnDeleteFile(filePath string) {
	if _, err := exec.LookPath("svn"); err != nil {
		return
	}

	dir := filepath.Dir(filePath)
	filename := filepath.Base(filePath)

	cmd := exec.Command("svn", "delete", "--keep-local", filename)
	cmd.Dir = dir

	if output, err := cmd.CombinedOutput(); err != nil {
		if dm.debugMode {
			fmt.Printf("      ⚠️  SVN delete 失败: %s (%v)\n", filename, err)
			fmt.Printf("      Output: %s\n", string(output))
		}
	} else {
		if dm.debugMode {
			fmt.Printf("    📝 SVN delete: %s\n", filename)
		}
	}
}

// copyFileWithVersions 复制文件（包括hash版本）
func (dm *DeployManager) copyFileWithVersions(sourcePath, destPath string) (int, int, error) {
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
				destHash, err := getFileHash(versionDestPath)
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

	err := filepath.Walk(sourceDirPath, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.IsDir() {
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
	if workerCount > len(tasks) {
		workerCount = len(tasks)
	}
	if workerCount == 0 {
		return 0, 0, nil
	}

	jobCh := make(chan fileTask, len(tasks))
	resultCh := make(chan struct {
		copied  int
		skipped int
		failed  bool
	}, len(tasks))

	var wg sync.WaitGroup
	for i := 0; i < workerCount; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for t := range jobCh {
				copied, skipped, err := dm.copyFileWithVersions(t.relToSource, t.destPath)
				failed := false
				if err != nil {
					fmt.Printf("⚠️  处理失败: %s - %v\n", t.destPath, err)
					failed = true
				}
				resultCh <- struct {
					copied  int
					skipped int
					failed  bool
				}{copied, skipped, failed}
			}
		}()
	}

	for _, t := range tasks {
		jobCh <- t
	}
	close(jobCh)

	go func() {
		wg.Wait()
		close(resultCh)
	}()

	totalCopied := 0
	totalSkipped := 0
	for r := range resultCh {
		totalCopied += r.copied
		totalSkipped += r.skipped
	}

	return totalCopied, totalSkipped, nil
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

// getLatestGitCommit 获取Git最新提交信息
func (dm *DeployManager) getLatestGitCommit() (string, string, error) {
	if !isGitRepo(dm.sourcePath) {
		return "", "", fmt.Errorf("源路径不是Git仓库")
	}

	authors := dm.config.GitAuthors
	if len(authors) == 0 {
		authors = []string{"chenchengpeng", "ccp"}
	}

	// 构建author过滤参数
	args := []string{"log", "-1", "--pretty=format:%h|%s"}
	for _, author := range authors {
		args = append(args, "--author="+author)
	}

	cmd := exec.Command("git", args...)
	cmd.Dir = dm.sourcePath

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

// openFolder 打开文件夹（避免重复打开）
func (dm *DeployManager) openFolder() {
	if dm.folderOpened {
		return
	}

	if !fileExists(dm.destPath) {
		fmt.Printf("⚠️  目标目录不存在: %s\n", dm.destPath)
		return
	}

	var cmd *exec.Cmd
	switch {
	case isWindows():
		cmd = exec.Command("explorer", dm.destPath)
	case isDarwin():
		cmd = exec.Command("open", dm.destPath)
	default:
		cmd = exec.Command("xdg-open", dm.destPath)
	}

	if err := cmd.Start(); err != nil {
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
	fmt.Printf("📂 目标路径: %s\n\n", dm.destPath)

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

	// 先更新SVN仓库
	if isSvnRepo(dm.destPath) {
		if err := dm.updateSvnRepo(); err != nil {
			fmt.Printf("⚠️  SVN更新失败: %v，继续部署...\n", err)
		}
	}

	fmt.Println("📦 开始复制文件...\n")

	totalCopied := 0
	totalSkipped := 0
	totalFailed := 0

	// 并发处理文件，提高复制效率
	workerCount := runtime.NumCPU()
	if workerCount > 8 {
		workerCount = 8
	}
	filePaths := dm.config.FilePaths
	jobs := make(chan string, len(filePaths))
	results := make(chan struct {
		copied  int
		skipped int
		failed  int
	}, len(filePaths))

	var wg sync.WaitGroup
	for i := 0; i < workerCount; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for filePath := range jobs {
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
				results <- struct {
					copied  int
					skipped int
					failed  int
				}{copied, skipped, failed}
			}
		}()
	}

	for _, fp := range filePaths {
		jobs <- fp
	}
	close(jobs)

	go func() {
		wg.Wait()
		close(results)
	}()

	for r := range results {
		totalCopied += r.copied
		totalSkipped += r.skipped
		totalFailed += r.failed
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

	// 自动提交
	if autoCommit && isSvnRepo(dm.destPath) {
		svnMessage := ""
		if commitMessage != "" {
			// 使用自定义提交信息
			svnMessage = commitMessage
			fmt.Printf("\n📝 使用自定义提交信息: %s\n", svnMessage)
		} else {
			// 使用Git最新提交信息
			hash, message, err := dm.getLatestGitCommit()
			if err != nil {
				fmt.Printf("⚠️  获取Git提交信息失败: %v\n", err)
				fmt.Println("💡 请手动提交SVN更改")
				return nil
			}
			svnMessage = message
			fmt.Printf("\n📝 Git提交: %s - %s\n", hash, message)
		}

		fmt.Println("⏳ 2秒后开始提交...")
		time.Sleep(2 * time.Second)

		if err := dm.svnCommit(svnMessage); err != nil {
			fmt.Printf("❌ 自动提交失败: %v\n", err)
		} else {
			fmt.Println("🎉 自动提交完成！")
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
	hash = "hash化"

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
	fmt.Printf("  📝 执行 git commit -m \"%s\"...\n", hash)
	commitCmd := exec.Command("git", "commit", "-m", hash)
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
		fmt.Printf("⚠️  Git pull 失败: %v\n", err)
		fmt.Printf("   继续尝试推送...\n")
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
	if !isGitRepo(dir) {
		return "", "", fmt.Errorf("目录不是Git仓库: %s", dir)
	}

	cmd := exec.Command("git", "log", "-1", "--pretty=format:%h|%s")
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

	var cmd *exec.Cmd
	switch {
	case isWindows():
		cmd = exec.Command("explorer", folderPath)
	case isDarwin():
		cmd = exec.Command("open", folderPath)
	default:
		cmd = exec.Command("xdg-open", folderPath)
	}

	if err := cmd.Start(); err != nil {
		fmt.Printf("❌ 打开文件夹失败: %v\n", err)
		fmt.Printf("📁 请手动打开: %s\n", folderPath)
	} else {
		fmt.Printf("✅ 已打开目标文件夹: %s\n", folderPath)
		vm.folderOpened = true
	}
}

// isWindows 检查是否Windows系统
func isWindows() bool {
	return os.PathSeparator == '\\' && os.PathListSeparator == ';'
}

// isDarwin 检查是否macOS系统
func isDarwin() bool {
	// 简单检查，实际可以用runtime.GOOS
	return false
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
	isHome := os.Getenv("IS_HOME")
	fmt.Printf("📍 环境变量 IS_HOME=%s\n", isHome)

	if config.HomeHTMLFile != "" || config.CompanyHTMLFile != "" {
		if isHome == "1" {
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
	configPath := flag.String("config", "version.config.json", "配置文件路径")
	htmlFile := flag.String("file", "", "单个HTML文件路径（命令行指定，优先级高于配置文件）")
	scanAll := flag.Bool("all", false, "扫描所有HTML文件")
	cdnDomain := flag.String("cdn", "", "CDN域名")
	debugMode := flag.Bool("debug", false, "调试模式（显示详细日志）")
	deployOnly := flag.Bool("deploy", false, "仅执行部署（不处理hash）")
	deployCommit := flag.Bool("deploy-commit", false, "部署并自动提交")
	deployMode := flag.Int("mode", 7, "部署模式：1=pre-script+copy, 2=pre-script+copy-commit, 3=pre-script+copy-commit+回滚HTML+git commit&push, 4=不替换CDN+copy, 5=不替换CDN+copy-commit, 6=不替换CDN+copy-commit+回滚HTML+git commit&push, 7=copy(排除cdnExcludeFiles), 8=copy-commit(排除cdnExcludeFiles), 9=copy-commit+回滚HTML+git commit&push(排除cdnExcludeFiles)")
	commitMessage := flag.String("message", "", "自定义SVN提交信息（不指定则使用Git最新提交信息）")

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
		case 1:
			config.Deploy.AutoCommit = false
			config.Deploy.ForcePreScript = false
			config.Deploy.Command = "copy"
		case 2:
			config.Deploy.AutoCommit = true
			config.Deploy.ForcePreScript = false
			config.Deploy.Command = "copy-commit"
		case 3:
			config.Deploy.AutoCommit = true
			config.Deploy.ForcePreScript = false
			config.Deploy.Command = "copy-commit"
			config.RollbackAfterDeploy = true    // 回滚HTML
			config.GitCommitAfterRollback = true // 回滚后执行git commit和push
		case 4:
			config.Deploy.AutoCommit = false
			config.Deploy.ForcePreScript = false
			config.Deploy.Command = "copy"
			config.CDNDomain = "" // 不替换CDN
		case 5:
			config.Deploy.AutoCommit = true
			config.Deploy.ForcePreScript = false
			config.Deploy.Command = "copy-commit"
			config.CDNDomain = "" // 不替换CDN
		case 6:
			config.Deploy.AutoCommit = true
			config.Deploy.ForcePreScript = false
			config.Deploy.Command = "copy-commit"
			config.CDNDomain = ""                // 不替换CDN
			config.RollbackAfterDeploy = true    // 回滚HTML
			config.GitCommitAfterRollback = true // 回滚后执行git commit和push
		case 7:
			config.Deploy.AutoCommit = false
			config.Deploy.ForcePreScript = false
			config.Deploy.Command = "copy"
			config.CDNExcludeFiles = []string{} // 清空CDN排除文件
		case 8:
			config.Deploy.AutoCommit = true
			config.Deploy.ForcePreScript = false
			config.Deploy.Command = "copy-commit"
			config.CDNExcludeFiles = []string{} // 清空CDN排除文件
		case 9:
			config.Deploy.AutoCommit = true
			config.Deploy.ForcePreScript = false
			config.Deploy.Command = "copy-commit"
			config.RollbackAfterDeploy = true    // 回滚HTML
			config.GitCommitAfterRollback = true // 回滚后执行git commit和push
			config.CDNExcludeFiles = []string{}  // 清空CDN排除文件
		}
	}

	vm := NewVersionManager(*config, *debugMode)

	// 设置自定义提交信息
	if *commitMessage != "" {
		vm.commitMessage = *commitMessage
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
