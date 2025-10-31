package main

import (
    "crypto/md5"
    "encoding/hex"
    "encoding/json"
    "flag"
    "fmt"
    "io"
    "os"
    "path/filepath"
    "regexp"
    "strings"
    "sync"
)

// Config 配置结构
type Config struct {
    RootDir     string   `json:"rootDir"`
    CDNDomain   string   `json:"cdnDomain"`
    HashLength  int      `json:"hashLength"`
    HTMLFiles   []string `json:"htmlFiles"`
    ExcludeDirs []string `json:"excludeDirs"`
}

// VersionManager 版本管理器
type VersionManager struct {
    config         Config
    versionMap     map[string]string
    processedFiles map[string]bool
    mu             sync.Mutex
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
func NewVersionManager(config Config) *VersionManager {
    return &VersionManager{
        config:         config,
        versionMap:     make(map[string]string),
        processedFiles: make(map[string]bool),
    }
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

// renameFileWithHash 重命名文件（如果hash改变）
func (vm *VersionManager) renameFileWithHash(filePath string) (*FileInfo, error) {
    dir := filepath.Dir(filePath)
    filename := filepath.Base(filePath)
    cleanFilename := vm.removeHashFromFilename(filename)
    
    // 计算hash
    hash, err := vm.calculateFileHash(filePath)
    if err != nil {
        return nil, err
    }
    
    newFilename := vm.addHashToFilename(cleanFilename, hash)
    newPath := filepath.Join(dir, newFilename)
    
    info := &FileInfo{
        OriginalPath: filePath,
        HashedPath:   newPath,
        Hash:         hash,
        Renamed:      filename != newFilename,
    }
    
    // 如果文件名没变化，直接返回
    if !info.Renamed {
        return info, nil
    }
    
    // 删除旧的带hash的文件
    if filename != cleanFilename && fileExists(filePath) {
        os.Remove(filePath)
    }
    
    // 检查是否存在无hash的原始文件
    cleanPath := filepath.Join(dir, cleanFilename)
    if fileExists(cleanPath) && cleanPath != newPath {
        // 复制文件到新路径
        if err := copyFile(cleanPath, newPath); err != nil {
            return nil, err
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

// updateCSSImageReferences 更新CSS文件中的图片引用 - 简化版本
func (vm *VersionManager) updateCSSImageReferences(cssPath string, imageMap map[string]string) error {
    content, err := os.ReadFile(cssPath)
    if err != nil {
        return err
    }
    
    contentStr := string(content)
    updated := false
    
    for originalPath, newFilename := range imageMap {
        oldFilename := filepath.Base(originalPath)
        cleanOldFilename := vm.removeHashFromFilename(oldFilename)
        
        // 更精确的正则表达式，处理各种引号情况
        pattern := fmt.Sprintf(`url\(\s*(['"]?)\s*([^'")\s]*[/\\])?%s\s*(['"]?)\s*\)`, regexp.QuoteMeta(cleanOldFilename))
        re := regexp.MustCompile(pattern)
        
        newContent := re.ReplaceAllStringFunc(contentStr, func(match string) string {
            submatches := re.FindStringSubmatch(match)
            if len(submatches) >= 4 {
                openingQuote := submatches[1]
                pathPrefix := submatches[2]
                closingQuote := submatches[3]
                
                // 确保引号一致
                if openingQuote != closingQuote {
                    // 如果只有一边有引号，两边都加上
                    if openingQuote != "" && closingQuote == "" {
                        closingQuote = openingQuote
                    } else if openingQuote == "" && closingQuote != "" {
                        openingQuote = closingQuote
                    }
                }
                
                result := fmt.Sprintf("url(%s%s%s%s)", openingQuote, pathPrefix, newFilename, closingQuote)
                
                if match != result {
                    updated = true
                    fmt.Printf("    🔄 %s -> %s\n", cleanOldFilename, newFilename)
                }
                return result
            }
            return match
        })
        
        contentStr = newContent
    }
    
    if updated {
        return os.WriteFile(cssPath, []byte(contentStr), 0644)
    }
    
    return nil
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
        for oldFilename, newFilename := range cssMap {
            if oldFilename == newFilename {
                continue
            }
            
            pattern := regexp.QuoteMeta(oldFilename)
            re := regexp.MustCompile(fmt.Sprintf(`(<link[^>]+href=['"])([^'"]*/)?\s*(%s)\s*(['"][^>]*>)`, pattern))
            
            newContent := re.ReplaceAllStringFunc(contentStr, func(match string) string {
                submatches := re.FindStringSubmatch(match)
                if len(submatches) >= 5 {
                    prefix := submatches[1]
                    pathPrefix := submatches[2]
                    suffix := submatches[4]
                    
                    cdnPrefix := ""
                    if vm.config.CDNDomain != "" {
                        cdnPrefix = vm.config.CDNDomain + "/"
                    }
                    
                    result := fmt.Sprintf("%s%s%s%s%s", prefix, cdnPrefix, pathPrefix, newFilename, suffix)
                    
                    if match != result {
                        updated = true
                        fmt.Printf("    🔄 CSS: %s -> %s\n", oldFilename, newFilename)
                    }
                    return result
                }
                return match
            })
            
            contentStr = newContent
        }
    }
    
    // 处理JS引用
    if jsMap, ok := resources["js"]; ok {
        for oldFilename, newFilename := range jsMap {
            if oldFilename == newFilename {
                continue
            }
            
            pattern := regexp.QuoteMeta(oldFilename)
            re := regexp.MustCompile(fmt.Sprintf(`(<script[^>]+src=['"])([^'"]*/)?\s*(%s)\s*(['"][^>]*>)`, pattern))
            
            newContent := re.ReplaceAllStringFunc(contentStr, func(match string) string {
                submatches := re.FindStringSubmatch(match)
                if len(submatches) >= 5 {
                    prefix := submatches[1]
                    pathPrefix := submatches[2]
                    suffix := submatches[4]
                    
                    cdnPrefix := ""
                    if vm.config.CDNDomain != "" {
                        cdnPrefix = vm.config.CDNDomain + "/"
                    }
                    
                    result := fmt.Sprintf("%s%s%s%s%s", prefix, cdnPrefix, pathPrefix, newFilename, suffix)
                    
                    if match != result {
                        updated = true
                        fmt.Printf("    🔄 JS: %s -> %s\n", oldFilename, newFilename)
                    }
                    return result
                }
                return match
            })
            
            contentStr = newContent
        }
    }
    
    if updated {
        return os.WriteFile(htmlPath, []byte(contentStr), 0644)
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
    
    resources := map[string]map[string]string{
        "css": make(map[string]string),
        "js":  make(map[string]string),
    }
    
    // 1. 处理对应的JS文件
    fmt.Println("\n📦 处理 JavaScript 文件...")
    jsPaths := []string{
        filepath.Join(htmlDir, htmlBasename+".js"),
        filepath.Join(htmlDir, "js", htmlBasename+".js"),
    }
    
    for _, jsPath := range jsPaths {
        actualJsPath := vm.findFile(jsPath)
        if actualJsPath == "" {
            continue
        }
        
        oldFilename := filepath.Base(actualJsPath)
        info, err := vm.renameFileWithHash(actualJsPath)
        if err != nil {
            fmt.Printf("  ❌ 处理JS失败: %v\n", err)
            continue
        }
        
        newFilename := filepath.Base(info.HashedPath)
        resources["js"][oldFilename] = newFilename
        resources["js"][vm.removeHashFromFilename(oldFilename)] = newFilename
        
        fmt.Printf("  ✅ %s -> %s\n", oldFilename, newFilename)
        
        relPath, _ := filepath.Rel(vm.config.RootDir, actualJsPath)
        vm.versionMap[relPath] = info.Hash
        break
    }
    
    // 2. 处理对应的CSS文件
    fmt.Println("\n🎨 处理 CSS 文件...")
    cssPaths := []string{
        filepath.Join(htmlDir, htmlBasename+".css"),
        filepath.Join(htmlDir, "css", htmlBasename+".css"),
    }
    
    for _, cssPath := range cssPaths {
        actualCssPath := vm.findFile(cssPath)
        if actualCssPath == "" {
            continue
        }
        
        oldCssFilename := filepath.Base(actualCssPath)
        
        // 2.1 收集CSS中的图片
        fmt.Println("  📸 收集CSS中引用的图片...")
        images, err := vm.collectImagesFromCSS(actualCssPath)
        if err != nil {
            fmt.Printf("  ⚠️  读取CSS失败: %v\n", err)
            continue
        }
        
        imageMap := make(map[string]string)
        
        if len(images) > 0 {
            fmt.Printf("  找到 %d 个图片引用\n", len(images))
            
            // 2.2 处理每个图片
            for _, image := range images {
                vm.mu.Lock()
                if vm.processedFiles[image.AbsolutePath] {
                    vm.mu.Unlock()
                    continue
                }
                vm.processedFiles[image.AbsolutePath] = true
                vm.mu.Unlock()
                
                oldImageFilename := filepath.Base(image.AbsolutePath)
                info, err := vm.renameFileWithHash(image.AbsolutePath)
                if err != nil {
                    fmt.Printf("    ⚠️  处理图片失败 %s: %v\n", oldImageFilename, err)
                    continue
                }
                
                newImageFilename := filepath.Base(info.HashedPath)
                imageMap[image.OriginalPath] = newImageFilename
                
                fmt.Printf("    ✅ %s -> %s\n", oldImageFilename, newImageFilename)
                
                relPath, _ := filepath.Rel(vm.config.RootDir, image.AbsolutePath)
                vm.versionMap[relPath] = info.Hash
            }
            
            // 2.3 更新CSS中的图片引用
            fmt.Println("  🔄 更新CSS中的图片引用...")
            if err := vm.updateCSSImageReferences(actualCssPath, imageMap); err != nil {
                fmt.Printf("  ⚠️  更新CSS引用失败: %v\n", err)
            }
        }
        
        // 2.4 重命名CSS文件（基于更新后的内容）
        info, err := vm.renameFileWithHash(actualCssPath)
        if err != nil {
            fmt.Printf("  ❌ 处理CSS失败: %v\n", err)
            continue
        }
        
        newCssFilename := filepath.Base(info.HashedPath)
        resources["css"][oldCssFilename] = newCssFilename
        resources["css"][vm.removeHashFromFilename(oldCssFilename)] = newCssFilename
        
        fmt.Printf("  ✅ %s -> %s\n", oldCssFilename, newCssFilename)
        
        relPath, _ := filepath.Rel(vm.config.RootDir, actualCssPath)
        vm.versionMap[relPath] = info.Hash
        break
    }
    
    // 3. 更新HTML中的引用
    fmt.Println("\n🔄 更新HTML中的资源引用...")
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
    
    vm.saveVersionMap()
    fmt.Println("\n" + strings.Repeat("=", 60))
    fmt.Println("🎉 全部处理完成！")
    fmt.Println(strings.Repeat("=", 60))
}

// saveVersionMap 保存版本映射
func (vm *VersionManager) saveVersionMap() {
    mapPath := filepath.Join(vm.config.RootDir, ".version-map.json")
    data, err := json.MarshalIndent(vm.versionMap, "", "  ")
    if err != nil {
        fmt.Printf("⚠️  保存版本映射失败: %v\n", err)
        return
    }
    
    if err := os.WriteFile(mapPath, data, 0644); err != nil {
        fmt.Printf("⚠️  写入版本映射失败: %v\n", err)
        return
    }
    
    fmt.Printf("💾 版本映射已保存到: .version-map.json\n")
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
    
    return &config, nil
}

func main() {
    configPath := flag.String("config", "version.config.json", "配置文件路径")
    htmlFile := flag.String("file", "D:\\project\\cx_project\\china_mobile\\gitProject\\richinfo_tyjf_xhmqqthy\\src\\main\\webapp\\res\\wap\\xdrNormal.html", "单个HTML文件路径")
    scanAll := flag.Bool("all", false, "扫描所有HTML文件")
    cdnDomain := flag.String("cdn", "https://qqt-res.cmicrwx.cn", "CDN域名")
    
    flag.Parse()
    
    // 加载配置
    config, err := loadConfig(*configPath)
    if err != nil {
        // 如果配置文件不存在，使用默认配置
        config = &Config{
            RootDir:     ".",
            HashLength:  8,
            ExcludeDirs: []string{"node_modules", ".git", "dist", "build"},
        }
    }
    
    // 命令行参数覆盖配置文件
    if *cdnDomain != "" {
        config.CDNDomain = *cdnDomain
    }
    
    vm := NewVersionManager(*config)
    
    // 处理单个文件
    if *htmlFile != "" {
        if err := vm.processHTMLFile(*htmlFile); err != nil {
            fmt.Printf("❌ 处理失败: %v\n", err)
            os.Exit(1)
        }
        vm.saveVersionMap()
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
        fmt.Println("请使用 -file 指定文件, -all 扫描所有文件, 或在配置文件中指定HTML文件列表")
        flag.Usage()
    }
}
