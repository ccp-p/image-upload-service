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
    
    // 检查目标文件是否已存在且内容相同
    if fileExists(newPath) {
        existingHash, err := vm.calculateFileHash(newPath)
        if err == nil && existingHash == hash {
            fmt.Printf("  ⏭️  Hash文件已存在且内容相同: %s\n", newFilename)
            return info, nil
        }
        // 如果hash不同，删除旧文件
        fmt.Printf("  🗑️  删除旧的hash文件: %s\n", newFilename)
        os.Remove(newPath)
    }
    
    // 总是复制源文件到新路径（保留原始文件）
    if err := copyFile(sourcePath, newPath); err != nil {
        return nil, fmt.Errorf("复制文件失败: %v", err)
    }
    
    fmt.Printf("  ✅ 已生成带hash文件: %s (保留原文件: %s)\n", newFilename, cleanFilename)
    
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
            
            // 清理旧文件名（移除可能存在的hash）
            cleanOldFilename := vm.removeHashFromFilename(oldFilename)
            
            // 匹配CSS引用，支持各种路径形式
            pattern := regexp.QuoteMeta(cleanOldFilename)
            re := regexp.MustCompile(fmt.Sprintf(`(<link[^>]*href\s*=\s*['"])([^'"]*/)?\s*(%s)\s*(['"][^>]*>)`, pattern))
            
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
                        fmt.Printf("    🔄 CSS: %s -> %s\n", cleanOldFilename, newFilename)
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
            
            // 清理旧文件名（移除可能存在的hash）
            cleanOldFilename := vm.removeHashFromFilename(oldFilename)
            
            fmt.Printf("    🔍 尝试替换JS: %s -> %s\n", cleanOldFilename, newFilename)
            
            // 匹配JS引用，修改为更宽松的模式，支持没有type属性的script标签
            pattern := regexp.QuoteMeta(cleanOldFilename)
            re := regexp.MustCompile(fmt.Sprintf(`(<script[^>]*\ssrc\s*=\s*['"])([^'"]*/)?\s*(%s)\s*(['"][^>]*>)`, pattern))
            
            // 也匹配src在最前面的情况（没有其他属性）
            re2 := regexp.MustCompile(fmt.Sprintf(`(<script\s+src\s*=\s*['"])([^'"]*/)?\s*(%s)\s*(['"][^>]*>)`, pattern))
            
            beforeReplace := contentStr
            
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
                        fmt.Printf("    🔄 JS (模式1): %s -> %s\n", cleanOldFilename, newFilename)
                        fmt.Printf("      原始: %s\n", match)
                        fmt.Printf("      替换: %s\n", result)
                    }
                    return result
                }
                return match
            })
            
            // 如果第一个正则没匹配到，尝试第二个
            if newContent == beforeReplace {
                newContent = re2.ReplaceAllStringFunc(contentStr, func(match string) string {
                    submatches := re2.FindStringSubmatch(match)
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
                            fmt.Printf("    🔄 JS (模式2): %s -> %s\n", cleanOldFilename, newFilename)
                            fmt.Printf("      原始: %s\n", match)
                            fmt.Printf("      替换: %s\n", result)
                        }
                        return result
                    }
                    return match
                })
            }
            
            contentStr = newContent
        }
    }
    
    if updated {
        if err := os.WriteFile(htmlPath, []byte(contentStr), 0644); err != nil {
            return err
        }
        fmt.Printf("    ✅ HTML文件已更新\n")
    } else {
        fmt.Printf("    ⚠️  没有内容需要更新\n")
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
    
    fmt.Printf("📂 HTML目录: %s\n", htmlDir)
    fmt.Printf("📝 HTML基础名: %s\n", htmlBasename)
    
    resources := map[string]map[string]string{
        "css": make(map[string]string),
        "js":  make(map[string]string),
    }
    
    // 1. 处理对应的JS文件
    fmt.Println("\n📦 处理 JavaScript 文件...")
    jsPaths := []string{
        filepath.Join(htmlDir, htmlBasename+".js"),
        filepath.Join(htmlDir, "js", htmlBasename+".js"),
        filepath.Join(htmlDir, "scripts", "js", htmlBasename+".js"),
    }
    
    jsFound := false
    for _, jsPath := range jsPaths {
        fmt.Printf("  🔍 查找: %s\n", jsPath)
        actualJsPath := vm.findFile(jsPath)
        if actualJsPath == "" {
            fmt.Printf("  ⚠️  未找到JS文件: %s\n", jsPath)
            continue
        }
        
        jsFound = true
        fmt.Printf("  📂 找到JS文件: %s\n", actualJsPath)
        
        oldFilename := filepath.Base(actualJsPath)
        cleanFilename := vm.removeHashFromFilename(oldFilename)
        
        info, err := vm.renameFileWithHash(actualJsPath)
        if err != nil {
            fmt.Printf("  ❌ 处理JS失败: %v\n", err)
            continue
        }
        
        newFilename := filepath.Base(info.HashedPath)
        
        // 同时记录原始文件名和清理后文件名的映射
        resources["js"][oldFilename] = newFilename
        resources["js"][cleanFilename] = newFilename
        
        fmt.Printf("  ✅ JS处理完成: %s -> %s (hash: %s)\n", cleanFilename, newFilename, info.Hash[:8])
        
        relPath, _ := filepath.Rel(vm.config.RootDir, info.OriginalPath)
        vm.versionMap[relPath] = info.Hash
        break
    }
    
    if !jsFound {
        fmt.Println("  ⚠️  未找到任何JS文件")
    }
    
    // 2. 处理对应的CSS文件
    fmt.Println("\n🎨 处理 CSS 文件...")
    cssPaths := []string{
        filepath.Join(htmlDir, htmlBasename+".css"),
        filepath.Join(htmlDir, "css", htmlBasename+".css"),
    }
    
    cssFound := false
    for _, cssPath := range cssPaths {
        fmt.Printf("  🔍 查找: %s\n", cssPath)
        actualCssPath := vm.findFile(cssPath)
        if actualCssPath == "" {
            fmt.Printf("  ⚠️  未找到CSS文件: %s\n", cssPath)
            continue
        }
        
        cssFound = true
        fmt.Printf("  📂 找到CSS文件: %s\n", actualCssPath)
        
        oldCssFilename := filepath.Base(actualCssPath)
        cleanCssFilename := vm.removeHashFromFilename(oldCssFilename)
        
        // 确保使用原始CSS文件（无hash版本）
        cssDir := filepath.Dir(actualCssPath)
        originalCssPath := filepath.Join(cssDir, cleanCssFilename)
        if !fileExists(originalCssPath) {
            originalCssPath = actualCssPath
        }
        
        fmt.Printf("  📝 原始CSS文件: %s\n", cleanCssFilename)
        
        // 2.1 收集CSS中的图片
        fmt.Println("  📸 收集CSS中引用的图片...")
        images, err := vm.collectImagesFromCSS(originalCssPath)
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
                
                fmt.Printf("    ✅ 图片: %s -> %s\n", oldImageFilename, newImageFilename)
                
                relPath, _ := filepath.Rel(vm.config.RootDir, image.AbsolutePath)
                vm.versionMap[relPath] = info.Hash
            }
        }
        
        // 2.3 先复制原始CSS文件生成hash版本
        fmt.Println("  🔄 生成带hash的CSS文件...")
        
        // 计算原始CSS的hash
        originalHash, err := vm.calculateFileHash(originalCssPath)
        if err != nil {
            fmt.Printf("  ❌ 计算CSS hash失败: %v\n", err)
            continue
        }
        
        hashedCssFilename := vm.addHashToFilename(cleanCssFilename, originalHash)
        hashedCssPath := filepath.Join(cssDir, hashedCssFilename)
        
        // 先复制原始CSS文件
        if err := copyFile(originalCssPath, hashedCssPath); err != nil {
            fmt.Printf("  ❌ 复制CSS文件失败: %v\n", err)
            continue
        }
        
        fmt.Printf("  ✅ 已复制CSS到: %s\n", hashedCssFilename)
        
        // 2.4 只更新hash版本CSS中的图片引用（不修改原始CSS）
        if len(imageMap) > 0 {
            fmt.Println("  🔄 更新hash版本CSS中的图片引用...")
            if err := vm.updateCSSImageReferences(hashedCssPath, imageMap); err != nil {
                fmt.Printf("  ⚠️  更新CSS引用失败: %v\n", err)
            } else {
                fmt.Printf("  ✅ Hash版本CSS已更新图片引用\n")
                fmt.Printf("  📝 原始CSS保持不变: %s\n", cleanCssFilename)
            }
            
            // 重新计算更新后的CSS文件的hash
            newHash, err := vm.calculateFileHash(hashedCssPath)
            if err == nil && newHash != originalHash {
                // 如果hash改变了，需要重命名
                finalCssFilename := vm.addHashToFilename(cleanCssFilename, newHash)
                finalCssPath := filepath.Join(cssDir, finalCssFilename)
                
                if finalCssPath != hashedCssPath {
                    fmt.Printf("  🔄 CSS内容变化，重新计算hash: %s -> %s\n", originalHash[:8], newHash[:8])
                    
                    // 删除旧的hash文件，重命名为新hash
                    if err := os.Rename(hashedCssPath, finalCssPath); err != nil {
                        // 如果重命名失败，尝试复制后删除
                        copyFile(hashedCssPath, finalCssPath)
                        os.Remove(hashedCssPath)
                    }
                    
                    hashedCssPath = finalCssPath
                    hashedCssFilename = finalCssFilename
                    originalHash = newHash
                    
                    fmt.Printf("  ✅ CSS已重命名为: %s\n", finalCssFilename)
                }
            }
        }
        
        // 同时记录原始文件名和清理后文件名的映射
        resources["css"][oldCssFilename] = hashedCssFilename
        resources["css"][cleanCssFilename] = hashedCssFilename
        
        fmt.Printf("  ✅ CSS处理完成: %s -> %s (hash: %s)\n", cleanCssFilename, hashedCssFilename, originalHash[:8])
        fmt.Printf("  📋 CSS映射: [%s] -> %s\n", cleanCssFilename, hashedCssFilename)
        
        relPath, _ := filepath.Rel(vm.config.RootDir, originalCssPath)
        vm.versionMap[relPath] = originalHash
        break
    }
    
    if !cssFound {
        fmt.Println("  ⚠️  未找到任何CSS文件")
    }
    
    // 3. 更新HTML中的引用
    fmt.Println("\n🔄 更新HTML中的资源引用...")
    fmt.Printf("  📋 CSS映射 (%d 项): %v\n", len(resources["css"]), resources["css"])
    fmt.Printf("  📋 JS映射 (%d 项): %v\n", len(resources["js"]), resources["js"])
    
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
    data, err := json.MarshalIndent(vm.versionMap, "", "  ")
    if err != nil {
        fmt.Printf("⚠️  保存版本映射失败: %v\n", err)
        return
    }
    mapPath := ".version-map.json"
    if err := os.WriteFile(mapPath, data, 0644); err != nil {
        fmt.Printf("⚠️  写入版本映射失败: %v\n", err)
        return
    }
    
    fmt.Printf("💾 版本映射已保存\n")
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
    cdnDomain := flag.String("cdn", "", "CDN域名")
    
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
