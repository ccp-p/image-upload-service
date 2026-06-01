package main

import (
	"crypto/md5"
	"encoding/hex"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"
)

// Config 配置结构
type Config struct {
    RootDir         string   `json:"rootDir"`
    CDNDomain       string   `json:"cdnDomain"`
    HashLength      int      `json:"hashLength"`
    SingleHTMLFile  string   `json:"singleHTMLFile"`  // 单个HTML文件路径
    HTMLFiles       []string `json:"htmlFiles"`
    ExcludeDirs     []string `json:"excludeDirs"`
    // 环境相关配置
    HomeHTMLFile    string   `json:"homeHTMLFile"`    // 家里电脑的HTML文件路径
    CompanyHTMLFile string   `json:"companyHTMLFile"` // 公司电脑的HTML文件路径
    // 新增：指定要处理的组件
    IncludeComponents []string `json:"includeComponents"` // 只处理指定的组件
    // 新增：指定哪些HTML文件需要处理主资源
    ProcessMainResources []string `json:"processMainResources"` 
    ReplaceAllWithCDN bool     `json:"replaceAllWithCDN"` // 替换所有资源为CDN路径
    // 新增：部署相关配置
    RollbackAfterDeploy bool   `json:"rollbackAfterDeploy"` // 部署后回滚HTML
    GitCommitAfterRollback bool `json:"gitCommitAfterRollback"` // 回滚后执行git commit和push
    CDNExcludeFiles []string   `json:"cdnExcludeFiles"`     // CDN替换排除的文件列表
    Deploy          DeployConfig `json:"deploy"`            // 部署配置
}

// DeployConfig 部署配置
type DeployConfig struct {
    Enabled           bool     `json:"enabled"`
    Command           string   `json:"command"`           // copy 或 copy-commit
    AutoCommit        bool     `json:"autoCommit"`
    HomeSourcePath    string   `json:"homeSourcePath"`
    HomeDestPath      string   `json:"homeDestPath"`
    CompanySourcePath string   `json:"companySourcePath"`
    CompanyDestPath   string   `json:"companyDestPath"`
    FilePaths         []string `json:"filePaths"`
    GitAuthors        []string `json:"gitAuthors"`
    CDNPathPrefix     string   `json:"cdnPathPrefix"`     // 新增：CDN URL中需要裁掉的前缀映射，例如 /2016tyjf/xhmqqthy/res/wap/
    ForcePreScript    bool     `json:"-"`                 // 运行时覆盖：是否强制执行前置脚本
}

// VersionManager 版本管理器
type VersionManager struct {
    config         Config
    processedFiles map[string]bool
    mu             sync.Mutex
    debugMode      bool
    folderOpened   bool // 记录文件夹是否已打开
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
    return &VersionManager{
        config:         config,
        processedFiles: make(map[string]bool),
        debugMode:      debugMode,
        folderOpened:   false,
    }
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
    // 匹配格式: filename.hash.ext
    re := regexp.MustCompile(`^(.+)\.([a-f0-9]{8})\.(css|js|jpg|jpeg|png|gif|svg|webp|ico)$`)
    matches := re.FindStringSubmatch(filename)
    
    if len(matches) == 4 {
        return matches[1] + "." + matches[3]
    }
    
    return filename
}

// addHashToFilename 给文件名添加hash
func (vm *VersionManager) addHashToFilename(filename, hash string) string {
    ext := filepath.Ext(filename)
    basename := strings.TrimSuffix(filename, ext)
    
    // 移除可能存在的旧hash
    re := regexp.MustCompile(`\.[a-f0-9]{8}$`)
    cleanBasename := re.ReplaceAllString(basename, "")
    
    return fmt.Sprintf("%s.%s%s", cleanBasename, hash, ext)
}

// findAndDeleteOldHashFiles 查找并删除旧的hash文件
func (vm *VersionManager) findAndDeleteOldHashFiles(dir, basename, ext, currentHash string) error {
    if vm.debugMode {
        fmt.Printf("  🔍 查找旧hash文件: %s%s (当前hash: %s)\n", basename, ext, currentHash)
    }
    
    pattern := fmt.Sprintf(`^%s\.[a-f0-9]{8}%s$`, regexp.QuoteMeta(basename), regexp.QuoteMeta(ext))
    re := regexp.MustCompile(pattern)
    
    files, err := os.ReadDir(dir)
    if err != nil {
        return err
    }
    
    var deletedCount int
    for _, file := range files {
        if !file.IsDir() {
            filename := file.Name()
            
            if re.MatchString(filename) {
                expectedPattern := fmt.Sprintf(`^%s\.([a-f0-9]{8})%s$`, regexp.QuoteMeta(basename), regexp.QuoteMeta(ext))
                hashRe := regexp.MustCompile(expectedPattern)
                hashMatches := hashRe.FindStringSubmatch(filename)
                
                if len(hashMatches) >= 2 {
                    extractedHash := hashMatches[1]
                    
                    if extractedHash != currentHash {
                        oldFilePath := filepath.Join(dir, filename)
                        if err := os.Remove(oldFilePath); err != nil {
                            fmt.Printf("    ⚠️  删除失败: %s\n", filename)
                        } else {
                            fmt.Printf("    🗑️  已删除: %s\n", filename)
                            deletedCount++
                        }
                    }
                }
            }
        }
    }
    
    if vm.debugMode && deletedCount > 0 {
        fmt.Printf("  ✅ 共删除 %d 个旧文件\n", deletedCount)
    }
    
    return nil
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
    var htmlFiles []string
    
    err := filepath.Walk(vm.config.RootDir, func(path string, info os.FileInfo, err error) error {
        if err != nil {
            return err
        }
        
        // 跳过排除的目录
        if info.IsDir() {
            for _, excludeDir := range vm.config.ExcludeDirs {
                if info.Name() == excludeDir {
                    return filepath.SkipDir
                }
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
    sourceFile, err := os.Open(src)
    if err != nil {
        return err
    }
    defer sourceFile.Close()
    
    destFile, err := os.Create(dst)
    if err != nil {
        return err
    }
    defer destFile.Close()
    
    _, err = io.Copy(destFile, sourceFile)
    return err
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
        if vm.debugMode {
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

    fmt.Printf("  ✅ 已生成: %s\n", newFilename)
    
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
    
    // 匹配 url() 中的路径
    re := regexp.MustCompile(`url\(['"]?([^'")\s]+)['"]?\)`)
    matches := re.FindAllStringSubmatch(string(content), -1)
    
    for _, match := range matches {
        if len(match) < 2 {
            continue
        }
        
        imagePath := match[1]
        
        // 跳过绝对URL和data URI
        if strings.HasPrefix(imagePath, "http") || 
           strings.HasPrefix(imagePath, "data:") || 
           strings.HasPrefix(imagePath, "//") {
            continue
        }
        
        // 移除查询字符串和hash
        imagePath = strings.Split(imagePath, "?")[0]
        imagePath = strings.Split(imagePath, "#")[0]
        
        // 计算绝对路径
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
    
    // 匹配 url() 中的路径
    re := regexp.MustCompile(`url\(\s*(['"]?)([^'")\s]+)(['"]?)\s*\)`)
    
    newContent := re.ReplaceAllStringFunc(contentStr, func(match string) string {
        submatches := re.FindStringSubmatch(match)
        if len(submatches) < 4 {
            return match
        }
        
        openingQuote := submatches[1]
        originalPath := submatches[2]
        closingQuote := submatches[3]
        
        // 跳过绝对URL和data URI
        if strings.HasPrefix(originalPath, "http") || 
           strings.HasPrefix(originalPath, "data:") || 
           strings.HasPrefix(originalPath, "//") {
            return match
        }
        
        // 移除查询字符串和hash用于匹配
        cleanPath := strings.Split(originalPath, "?")[0]
        cleanPath = strings.Split(cleanPath, "#")[0]
        
        // 标准化路径分隔符为正斜杠进行比较
        normalizedPath := strings.ReplaceAll(cleanPath, "\\", "/")
        
        // 在 imageMap 中查找匹配的路径
        var newFilename string
        var foundKey string
        
        for key, value := range imageMap {
            // 标准化 key 的路径分隔符
            normalizedKey := strings.ReplaceAll(key, "\\", "/")
            
            // 精确匹配完整路径
            if normalizedPath == normalizedKey {
                newFilename = value
                foundKey = key
                break
            }
        }
        
        if newFilename == "" {
            // 没有找到匹配项，保持原样
            return match
        }
        
        // 构建新路径：保持原有的目录结构，只替换文件名
        dir := filepath.Dir(originalPath)
        // 确保使用正斜杠
        dir = strings.ReplaceAll(dir, "\\", "/")
        
        var newPath string
        if dir == "." {
            newPath = newFilename
        } else {
            newPath = dir + "/" + newFilename
        }
        
        // 确保引号一致
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
            oldFilename := filepath.Base(foundKey)
            if(vm.debugMode){

            fmt.Printf("    🔄 %s -> %s\n", oldFilename, newFilename)
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
    // 先检查原始路径
    if fileExists(basePath) {
        return basePath
    }
    
    // 查找带hash的版本
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
    
    pattern := regexp.MustCompile(fmt.Sprintf(`^%s\.[a-f0-9]{8}\%s$`, regexp.QuoteMeta(nameWithoutExt), regexp.QuoteMeta(ext)))
    
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
    
    // 判断当前HTML是否为主资源文件
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
    cssRe := regexp.MustCompile(`<link[^>]*href\s*=\s*['"]([^'"]+\.css)['"]`)
    cssMatches := cssRe.FindAllStringSubmatch(contentStr, -1)
    for _, match := range cssMatches {
        if len(match) >= 2 {
            cssPath := match[1]
            isExternal := strings.HasPrefix(cssPath, "http") || strings.HasPrefix(cssPath, "//")
            
            // 如果是外部URL且当前是主资源页面，跳过
            // 如果是外部URL但不是主资源页面且不包含components，跳过
            if isExternal {
                if shouldProcessMain || !strings.Contains(cssPath, "components") {
                    continue
                }
            } else if !strings.Contains(cssPath, "components") {
                continue
            }
            
            // 检查是否应该处理此组件
            if !vm.shouldProcessComponent(cssPath) {
                continue
            }
            
            resources["css"] = append(resources["css"], cssPath)
        }
    }
    
    // 收集JS文件
    jsRe := regexp.MustCompile(`<script[^>]*src\s*=\s*['"]([^'"]+\.js)['"]`)
    jsMatches := jsRe.FindAllStringSubmatch(contentStr, -1)
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
    // 处理可能的外部URL：尝试从中提取相对路径部分（从components开始）
    targetPath := relativePath
    if strings.HasPrefix(relativePath, "http") || strings.HasPrefix(relativePath, "//") {
        idx := strings.Index(relativePath, "components/")
        if idx != -1 {
            targetPath = relativePath[idx:]
        }
    }

    absolutePath := filepath.Join(htmlDir, filepath.FromSlash(targetPath))
    absolutePath = filepath.Clean(absolutePath)
    
    // 查找实际文件（可能是带hash的版本）
    actualPath := vm.findFile(absolutePath)
    if actualPath == "" {
        actualPath = absolutePath
    }
    
    if !fileExists(actualPath) {
        return nil, fmt.Errorf("文件不存在: %s", actualPath)
    }
    
    // 检查是否已经处理过
    vm.mu.Lock()
    if vm.processedFiles[actualPath] {
        vm.mu.Unlock()
        hash, err := vm.calculateFileHash(actualPath)
        if err != nil {
            return nil, err
        }
        dir := filepath.Dir(actualPath)
        filename := filepath.Base(actualPath)
        cleanFilename := vm.removeHashFromFilename(filename)
        hashedFilename := vm.addHashToFilename(cleanFilename, hash)
        hashedPath := filepath.Join(dir, hashedFilename)
        
        return &FileInfo{
            OriginalPath: actualPath,
            HashedPath:   hashedPath,
            Hash:         hash,
            Renamed:      true,
        }, nil
    }
    vm.processedFiles[actualPath] = true
    vm.mu.Unlock()
    
    // 处理CSS文件时，先处理其中的图片引用
    if strings.HasSuffix(strings.ToLower(actualPath), ".css") {
        return vm.processComponentCSS(actualPath)
    }
    
    // 处理JS文件
    return vm.renameFileWithHash(actualPath)
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
            // 使用原始路径作为key（标准化为正斜杠）
            originalPathKey := strings.ReplaceAll(image.OriginalPath, "\\", "/")
            
            vm.mu.Lock()
            if vm.processedFiles[image.AbsolutePath] {
                vm.mu.Unlock()
                // 查找已存在的带hash文件
                hash, err := vm.calculateFileHash(image.AbsolutePath)
                if err != nil {
                    continue
                }
                // 找到实际的带hash文件
                dir := filepath.Dir(image.AbsolutePath)
                oldImageFilename := filepath.Base(image.AbsolutePath)
                cleanImageFilename := vm.removeHashFromFilename(oldImageFilename)
                newImageFilename := vm.addHashToFilename(cleanImageFilename, hash)
                
                // 验证带hash的文件是否存在
                hashedPath := filepath.Join(dir, newImageFilename)
                if fileExists(hashedPath) {
                    imageMap[originalPathKey] = newImageFilename
                } else {
                    // 尝试查找任意带hash的版本
                    actualHashedFile := vm.findFile(filepath.Join(dir, cleanImageFilename))
                    if actualHashedFile != "" {
                        imageMap[originalPathKey] = filepath.Base(actualHashedFile)
                    }
                }
                continue
            }
            vm.processedFiles[image.AbsolutePath] = true
            vm.mu.Unlock()
            
            info, err := vm.renameFileWithHash(image.AbsolutePath)
            if err != nil {
                fmt.Printf("      ⚠️  失败: %s (%v)\n", filepath.Base(image.AbsolutePath), err)
                continue
            }
            
            newImageFilename := filepath.Base(info.HashedPath)
            // 使用原始CSS中的路径作为key
            imageMap[originalPathKey] = newImageFilename
            
            if vm.debugMode {
                fmt.Printf("      📎 映射: %s -> %s\n", originalPathKey, newImageFilename)
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
            if(vm.debugMode){

            for k, v := range imageMap {
                fmt.Printf("      %s -> %s\n", k, v)
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

// updateHTMLReferences 更新HTML中的资源引用
func (vm *VersionManager) updateHTMLReferences(htmlPath string, resources map[string]map[string]string) error {
    content, err := os.ReadFile(htmlPath)
    if err != nil {
        return err
    }
    
    contentStr := string(content)
    updated := false
    
    // 处理CSS引用
    if cssMap, ok := resources["css"]; ok {
        for originalRelPath, newHashedPath := range cssMap {
             vm.removeHashFromFilename(filepath.Base(originalRelPath))
            
            escapedPath := regexp.QuoteMeta(originalRelPath)
            escapedPath = strings.ReplaceAll(escapedPath, "/", `[/\\]`)
            
            patterns := []string{
                fmt.Sprintf(`(<link[^>]*href\s*=\s*['"])(%s)(['"][^>]*>)`, escapedPath),
                fmt.Sprintf(`(<link[^>]*href\s*=\s*['"])(\.{1,2}[/\\]%s)(['"][^>]*>)`, escapedPath),
            }
            
            matched := false
            for _, pattern := range patterns {
                re := regexp.MustCompile(pattern)
                if re.MatchString(contentStr) {
                    newContent := re.ReplaceAllStringFunc(contentStr, func(match string) string {
                        submatches := re.FindStringSubmatch(match)
                        if len(submatches) >= 4 {
                            prefix := submatches[1]
                            oldPath := submatches[2]
                            suffix := submatches[3]
                            
                            // 提取原始路径的目录部分
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
                            
                            // 构建新路径，保持原有的目录结构
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
                            
                            // 如果原始路径是相对路径（以./或../开头），保持相对路径格式
                            if strings.HasPrefix(oldPath, "../") || strings.HasPrefix(oldPath, "..\\") {
                                // 保持../格式
                                if !strings.HasPrefix(newPath, "../") && !strings.HasPrefix(newPath, "..\\") {
                                    newPath = "../" + newPath
                                }
                            } else if strings.HasPrefix(oldPath, "./") || strings.HasPrefix(oldPath, ".\\") {
                                // 保持./格式
                                if !strings.HasPrefix(newPath, "./") && !strings.HasPrefix(newPath, ".\\") {
                                    newPath = "./" + newPath
                                }
                            }
                            
                            // 🔥 检查是否排除CDN替换
                            if vm.config.CDNDomain != "" && !strings.HasPrefix(newPath, "http") && !vm.shouldExcludeFromCDN(newPath) {
                                cleanNewPath := strings.TrimPrefix(newPath, "./")
                                cleanNewPath = strings.TrimPrefix(cleanNewPath, "../")
                                newPath = vm.config.CDNDomain + "/" + cleanNewPath
                            }
                            
                            result := fmt.Sprintf("%s%s%s", prefix, newPath, suffix)
                            
                            if match != result {
                                updated = true
                                matched = true
                                fmt.Printf("  ✅ CSS: %s -> %s\n", filepath.Base(oldPath), filepath.Base(newPath))
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
            
            if !matched && vm.debugMode {
                fmt.Printf("  ⚠️  未匹配CSS: %s\n", originalRelPath)
            }
        }
    }
    
    // 处理JS引用
    if jsMap, ok := resources["js"]; ok {
        for originalRelPath, newHashedPath := range jsMap {
            vm.removeHashFromFilename(filepath.Base(originalRelPath))
            
            escapedPath := regexp.QuoteMeta(originalRelPath)
            escapedPath = strings.ReplaceAll(escapedPath, "/", `[/\\]`)
            
            patterns := []string{
                fmt.Sprintf(`(<script[^>]*src\s*=\s*['"])(%s)(['"][^>]*>)`, escapedPath),
                fmt.Sprintf(`(<script[^>]*src\s*=\s*['"])(\.{1,2}[/\\]%s)(['"][^>]*>)`, escapedPath),
            }
            
            matched := false
            for _, pattern := range patterns {
                re := regexp.MustCompile(pattern)
                if re.MatchString(contentStr) {
                    newContent := re.ReplaceAllStringFunc(contentStr, func(match string) string {
                        submatches := re.FindStringSubmatch(match)
                        if len(submatches) >= 4 {
                            prefix := submatches[1]
                            oldPath := submatches[2]
                            suffix := submatches[3]
                            
                            // 提取原始路径的目录部分
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
                            
                            // 构建新路径，保持原有的目录结构
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
                            
                            // 如果原始路径是相对路径（以./或../开头），保持相对路径格式
                            if strings.HasPrefix(oldPath, "../") || strings.HasPrefix(oldPath, "..\\") {
                                // 保持../格式
                                if !strings.HasPrefix(newPath, "../") && !strings.HasPrefix(newPath, "..\\") {
                                    newPath = "../" + newPath
                                }
                            } else if strings.HasPrefix(oldPath, "./") || strings.HasPrefix(oldPath, ".\\") {
                                // 保持./格式
                                if !strings.HasPrefix(newPath, "./") && !strings.HasPrefix(newPath, ".\\") {
                                    newPath = "./" + newPath
                                }
                            }
                            
                            // 🔥 检查是否排除CDN替换
                            if vm.config.CDNDomain != "" && !strings.HasPrefix(newPath, "http") && !vm.shouldExcludeFromCDN(newPath) {
                                cleanNewPath := strings.TrimPrefix(newPath, "./")
                                cleanNewPath = strings.TrimPrefix(cleanNewPath, "../")
                                newPath = vm.config.CDNDomain + "/" + cleanNewPath
                            }
                            
                            result := fmt.Sprintf("%s%s%s", prefix, newPath, suffix)
                            
                            if match != result {
                                updated = true
                                matched = true
                                fmt.Printf("  ✅ JS: %s -> %s\n", filepath.Base(oldPath), filepath.Base(newPath))
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
            
            if !matched && vm.debugMode {
                fmt.Printf("  ⚠️  未匹配JS: %s\n", originalRelPath)
            }
        }
    }
    
    // 新增：处理剩余的普通资源（非hash），替换为CDN路径
    if vm.config.CDNDomain != ""  {
        // 处理CSS
        cssPattern := `(<link[^>]*href\s*=\s*['"])([^'"]+)(['"][^>]*>)`
        cssRe := regexp.MustCompile(cssPattern)
        contentStr = cssRe.ReplaceAllStringFunc(contentStr, func(match string) string {
            submatches := cssRe.FindStringSubmatch(match)
            if len(submatches) >= 4 {
                prefix := submatches[1]
                path := submatches[2]
                suffix := submatches[3]
                
                // 只处理 .css 文件
                if !strings.Contains(path, ".css") {
                    return match
                }

                // 跳过绝对路径
                if strings.HasPrefix(path, "http") || strings.HasPrefix(path, "//") || strings.HasPrefix(path, "data:") {
                    return match
                }

                // 🔥 检查是否排除CDN替换
                if vm.shouldExcludeFromCDN(path) {
                    return match
                }

                // 清理路径
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
        jsPattern := `(<script[^>]*src\s*=\s*['"])([^'"]+)(['"][^>]*>)`
        jsRe := regexp.MustCompile(jsPattern)
        contentStr = jsRe.ReplaceAllStringFunc(contentStr, func(match string) string {
            submatches := jsRe.FindStringSubmatch(match)
            if len(submatches) >= 4 {
                prefix := submatches[1]
                path := submatches[2]
                suffix := submatches[3]
                
                // 只处理 .js 文件
                if !strings.Contains(path, ".js") {
                    return match
                }

                // 跳过绝对路径
                if strings.HasPrefix(path, "http") || strings.HasPrefix(path, "//") || strings.HasPrefix(path, "data:") {
                    return match
                }

                // 🔥 检查是否排除CDN替换
                if vm.shouldExcludeFromCDN(path) {
                    return match
                }

                // 清理路径
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
    config       DeployConfig
    sourcePath   string
    destPath     string
    debugMode    bool
    folderOpened bool
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
    
    var versions []FileVersion
    
    if !fileExists(dir) {
        return versions
    }
    
    // 检查无hash版本
    if fileExists(fullPath) {
        hash, _ := getFileHash(fullPath)
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
    
    hashPattern := regexp.MustCompile(fmt.Sprintf(`^%s\.[a-zA-Z0-9]+%s$`, regexp.QuoteMeta(basename), regexp.QuoteMeta(ext)))
    
    for _, file := range files {
        if file.Name() == fileName {
            continue
        }
        
        if hashPattern.MatchString(file.Name()) {
            filePath := filepath.Join(dir, file.Name())
            hash, _ := getFileHash(filePath)
            info, _ := file.Info()
            versions = append(versions, FileVersion{
                Path:    filePath,
                Name:    file.Name(),
                HasHash: true,
                ModTime: info.ModTime(),
                Hash:    hash,
            })
        }
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
    
    hashPattern := regexp.MustCompile(fmt.Sprintf(`^%s\.[a-zA-Z0-9]+%s$`, regexp.QuoteMeta(basename), regexp.QuoteMeta(ext)))
    
    deletedCount := 0
    for _, file := range files {
        if file.Name() == destFileName || file.Name() == keepFileName {
            continue
        }
        
        if hashPattern.MatchString(file.Name()) {
            filePath := filepath.Join(destDir, file.Name())
            if err := os.Remove(filePath); err == nil {
                deletedCount++
            }
        }
    }
    
    return deletedCount
}

// copyFileWithVersions 复制文件（包括hash版本）
func (dm *DeployManager) copyFileWithVersions(sourcePath, destPath string) (int, int, error) {
    versions := dm.findAllFileVersions(sourcePath)
    
    if len(versions) == 0 {
        return 0, 0, fmt.Errorf("源文件不存在: %s", sourcePath)
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
            destHash, err := getFileHash(versionDestPath)
            if err == nil && destHash == version.Hash {
                skippedCount++
                continue
            }
        }
        
        // 复制文件
        if err := copyFile(version.Path, versionDestPath); err != nil {
            return copiedCount, skippedCount, err
        }
        copiedCount++
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
    
    totalCopied := 0
    totalSkipped := 0
    
    err := filepath.Walk(sourceDirPath, func(path string, info os.FileInfo, err error) error {
        if err != nil {
            return err
        }
        
        if info.IsDir() {
            return nil
        }
        
        relPath, _ := filepath.Rel(sourceDirPath, path)
        destPath := filepath.Join(destDirPath, relPath)
        
        // 获取相对于sourcePath的路径用于查找版本
        relToSource, _ := filepath.Rel(dm.sourcePath, path)
        
        copied, skipped, err := dm.copyFileWithVersions(relToSource, destPath)
        totalFailed := 0
        if err != nil {
            fmt.Printf("⚠️  处理失败: %s - %v\n", destPath, err)
            totalFailed++
            return nil
        }
        
        totalCopied += copied
        totalSkipped += skipped
        return nil
    })
    
    return totalCopied, totalSkipped, err
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
func (dm *DeployManager) Run(autoCommit bool, htmlPath string, cdnDomain string) error {
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
    
    for _, filePath := range dm.config.FilePaths {
        if strings.Contains(filePath, "*") {
            copied, skipped, err := dm.handleWildcardPath(filePath)
            if err != nil {
                fmt.Printf("⚠️  处理失败: %s - %v\n", filePath, err)
                totalFailed++
                continue
            }
            totalCopied += copied
            totalSkipped += skipped
        } else {
            sourcePath := strings.TrimPrefix(filePath, "/")
            destPath := filepath.Join(dm.destPath, sourcePath)
            
            copied, skipped, err := dm.copyFileWithVersions(sourcePath, destPath)
            if err != nil {
                fmt.Printf("⚠️  复制失败: %s - %v\n", filePath, err)
                totalFailed++
                continue
            }
            totalCopied += copied
            totalSkipped += skipped
        }
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
        hash, message, err := dm.getLatestGitCommit()
        if err != nil {
            fmt.Printf("⚠️  获取Git提交信息失败: %v\n", err)
            fmt.Println("💡 请手动提交SVN更改")
        } else {
            svnMessage := message
            fmt.Printf("\n📝 Git提交: %s - %s\n", hash, message)
            fmt.Println("⏳ 2秒后开始提交...")
            time.Sleep(2 * time.Second)
            
            if err := dm.svnCommit(svnMessage); err != nil {
                fmt.Printf("❌ 自动提交失败: %v\n", err)
            } else {
                fmt.Println("🎉 自动提交完成！")
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

    // 1. 移除 HTML 注释内容，避免处理被注释掉的标签
    reComments := regexp.MustCompile(`(?s)<!--.*?-->`)
    cleanContent := reComments.ReplaceAllString(string(content), "")

    // 2. 在清理后的内容中匹配 CDN 域名开头的资源路径
    // 修改：排除反引号 \x60，防止匹配到模板字符串内容
    pattern := regexp.QuoteMeta(cdnDomain) + "(/[^\\s'\"\\x60]+)"
    re := regexp.MustCompile(pattern)
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
    
    if err := dm.Run(autoCommit, vm.config.SingleHTMLFile, vm.config.CDNDomain); err != nil {
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

    if len(vm.config.CDNExcludeFiles) == 0 {
        return false
    }
    
    filename := filepath.Base(filePath)
    // 移除查询参数
    if idx := strings.Index(filename, "?"); idx != -1 {
        filename = filename[:idx]
    }
    
    for _, excludeFile := range vm.config.CDNExcludeFiles {
        if filename == excludeFile || strings.Contains(filePath, excludeFile) {
            if vm.debugMode {
                fmt.Printf("    🚫 排除CDN替换: %s\n", filename)
            }
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
    deployMode := flag.Int("mode", 0, "部署模式：1=pre-script+copy, 2=pre-script+copy-commit, 3=pre-script+copy-commit+回滚HTML+git commit&push, 4=不替换CDN+copy, 5=不替换CDN+copy-commit, 6=不替换CDN+copy-commit+回滚HTML+git commit&push")
    
    flag.Parse()
    
    config, err := loadConfig(*configPath)
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
            config.Deploy.ForcePreScript = true
            config.Deploy.Command = "copy"
        case 2:
            config.Deploy.AutoCommit = true
            config.Deploy.ForcePreScript = true
            config.Deploy.Command = "copy-commit"
        case 3:
            config.Deploy.AutoCommit = true
            config.Deploy.ForcePreScript = true
            config.Deploy.Command = "copy-commit"
            config.RollbackAfterDeploy = true // 回滚HTML
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
            config.CDNDomain = "" // 不替换CDN
            config.RollbackAfterDeploy = true // 回滚HTML
            config.GitCommitAfterRollback = true // 回滚后执行git commit和push
        }
    }
    
    vm := NewVersionManager(*config, *debugMode)
    
    
    // 仅部署模式
    if *deployOnly || *deployCommit {
        if !config.Deploy.Enabled {
            fmt.Println("❌ 部署功能未启用，请在配置文件中设置 deploy.enabled = true")
            os.Exit(1)
        }
        
        dm := NewDeployManager(config.Deploy, *debugMode)
        autoCommit := *deployCommit || config.Deploy.AutoCommit || config.Deploy.Command == "copy-commit"
        
        if err := dm.Run(autoCommit, vm.config.SingleHTMLFile, vm.config.CDNDomain); err != nil {
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