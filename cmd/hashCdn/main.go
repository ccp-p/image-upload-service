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
    RootDir         string   `json:"rootDir"`
    CDNDomain       string   `json:"cdnDomain"`
    HashLength      int      `json:"hashLength"`
    SingleHTMLFile  string   `json:"singleHTMLFile"`  // 单个HTML文件路径
    HTMLFiles       []string `json:"htmlFiles"`
    ExcludeDirs     []string `json:"excludeDirs"`
    // 环境相关配置
    HomeHTMLFile    string   `json:"homeHTMLFile"`    // 家里电脑的HTML文件路径
    CompanyHTMLFile string   `json:"companyHTMLFile"` // 公司电脑的HTML文件路径
}

// VersionManager 版本管理器
type VersionManager struct {
    config         Config
    versionMap     map[string]string
    processedFiles map[string]bool
    mu             sync.Mutex
    debugMode      bool  // 调试模式
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
        versionMap:     make(map[string]string),
        processedFiles: make(map[string]bool),
        debugMode:      debugMode,
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
            if vm.debugMode {
                fmt.Printf("  ⏭️  跳过（已存在）: %s\n", newFilename)
            }
            return info, nil
        }
        os.Remove(newPath)
    }
    
    // 复制源文件到新路径
    if err := copyFile(sourcePath, newPath); err != nil {
        return nil, fmt.Errorf("复制文件失败: %v", err)
    }
    
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
    
    htmlDir := filepath.Dir(htmlPath)
    // 移除未使用的变量
    // htmlBasename := strings.TrimSuffix(filepath.Base(htmlPath), ".html")
    
    resources := map[string][]string{
        "css": {},
        "js":  {},
    }
    
    contentStr := string(content)
    
    // 收集CSS文件（只收集组件CSS，主CSS会单独处理）
    cssRe := regexp.MustCompile(`<link[^>]*href\s*=\s*['"]([^'"]+\.css)['"]`)
    cssMatches := cssRe.FindAllStringSubmatch(contentStr, -1)
    for _, match := range cssMatches {
        if len(match) >= 2 {
            cssPath := match[1]
            // 跳过外部URL
            if strings.HasPrefix(cssPath, "http") || strings.HasPrefix(cssPath, "//") {
                continue
            }
            
            // 只收集components目录下的CSS
            if !strings.Contains(cssPath, "components") {
                continue
            }
            
            // 转换为绝对路径（使用系统路径分隔符）
            absolutePath := filepath.Join(htmlDir, filepath.FromSlash(cssPath))
            absolutePath = filepath.Clean(absolutePath)
            
            if fileExists(absolutePath) || vm.findFile(absolutePath) != "" {
                // 保存时使用正斜杠（HTML标准）
                normalizedPath := filepath.ToSlash(cssPath)
                resources["css"] = append(resources["css"], normalizedPath)
                fmt.Printf("    📌 收集组件CSS: %s\n", normalizedPath)
            }
        }
    }
    
    // 收集JS文件（只收集组件目录下的JS，主JS会单独处理）
    jsRe := regexp.MustCompile(`<script[^>]*src\s*=\s*['"]([^'"]+\.js)['"]`)
    jsMatches := jsRe.FindAllStringSubmatch(contentStr, -1)
    for _, match := range jsMatches {
        if len(match) >= 2 {
            jsPath := match[1]
            // 跳过外部URL
            if strings.HasPrefix(jsPath, "http") || strings.HasPrefix(jsPath, "//") {
                continue
            }
            
            // 只收集components目录下的JS
            if !strings.Contains(jsPath, "components") {
                continue
            }
            
            // 转换为绝对路径（使用系统路径分隔符）
            absolutePath := filepath.Join(htmlDir, filepath.FromSlash(jsPath))
            absolutePath = filepath.Clean(absolutePath)
            
            if fileExists(absolutePath) || vm.findFile(absolutePath) != "" {
                // 保存时使用正斜杠（HTML标准）
                normalizedPath := filepath.ToSlash(jsPath)
                resources["js"] = append(resources["js"], normalizedPath)
                fmt.Printf("    📌 收集组件JS: %s\n", normalizedPath)
            }
        }
    }
    
    return resources, nil
}

// processComponentResource 处理组件资源（JS或CSS）
func (vm *VersionManager) processComponentResource(htmlDir, relativePath string) (*FileInfo, error) {
    absolutePath := filepath.Join(htmlDir, filepath.FromSlash(relativePath))
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
    
    imageMap := make(map[string]string)
    
    if len(images) > 0 {
        fmt.Printf("    📸 处理 %d 个图片引用\n", len(images))
        
        for _, image := range images {
            vm.mu.Lock()
            if vm.processedFiles[image.AbsolutePath] {
                vm.mu.Unlock()
                hash, err := vm.calculateFileHash(image.AbsolutePath)
                if err != nil {
                    continue
                }
                oldImageFilename := filepath.Base(image.AbsolutePath)
                cleanImageFilename := vm.removeHashFromFilename(oldImageFilename)
                newImageFilename := vm.addHashToFilename(cleanImageFilename, hash)
                imageMap[image.OriginalPath] = newImageFilename
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
            imageMap[image.OriginalPath] = newImageFilename
            
            relPath, _ := filepath.Rel(vm.config.RootDir, image.AbsolutePath)
            vm.versionMap[relPath] = info.Hash
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
    
    // 删除旧的CSS hash文件
    cssExt := filepath.Ext(cleanFilename)
    cssBasename := strings.TrimSuffix(cleanFilename, cssExt)
    if err := vm.findAndDeleteOldHashFiles(cssDir, cssBasename, cssExt, originalHash); err != nil {
        if vm.debugMode {
            fmt.Printf("      ⚠️  清理CSS旧文件时出错: %v\n", err)
        }
    }
    
    relPath, _ := filepath.Rel(vm.config.RootDir, originalCssPath)
    vm.versionMap[relPath] = originalHash
    
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
            cleanPath := vm.removeHashFromFilename(originalRelPath)
            
            escapedPath := regexp.QuoteMeta(cleanPath)
            escapedPath = strings.ReplaceAll(escapedPath, "/", `[/\\]`)
            
            patterns := []string{
                fmt.Sprintf(`(<link[^>]*href\s*=\s*['"])(%s)(['"][^>]*>)`, escapedPath),
                fmt.Sprintf(`(<link[^>]*href\s*=\s*['"])(\.[\\/]%s)(['"][^>]*>)`, escapedPath),
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
                            
                            var newPath string
                            if strings.HasPrefix(oldPath, "./") {
                                newPath = "./" + newHashedPath
                            } else {
                                newPath = newHashedPath
                            }
                            
                            if vm.config.CDNDomain != "" && !strings.HasPrefix(newPath, "http") {
                                cleanNewPath := strings.TrimPrefix(newPath, "./")
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
                fmt.Printf("  ⚠️  未匹配: %s\n", cleanPath)
            }
        }
    }
    
    // 处理JS引用
    if jsMap, ok := resources["js"]; ok {
        for originalRelPath, newHashedPath := range jsMap {
            cleanPath := vm.removeHashFromFilename(originalRelPath)
            
            escapedPath := regexp.QuoteMeta(cleanPath)
            escapedPath = strings.ReplaceAll(escapedPath, "/", `[/\\]`)
            
            patterns := []string{
                fmt.Sprintf(`(<script[^>]*src\s*=\s*['"])(%s)(['"][^>]*>)`, escapedPath),
                fmt.Sprintf(`(<script[^>]*src\s*=\s*['"])(\.[\\/]%s)(['"][^>]*>)`, escapedPath),
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
                            
                            var newPath string
                            if strings.HasPrefix(oldPath, "./") {
                                newPath = "./" + newHashedPath
                            } else {
                                newPath = newHashedPath
                            }
                            
                            if vm.config.CDNDomain != "" && !strings.HasPrefix(newPath, "http") {
                                cleanNewPath := strings.TrimPrefix(newPath, "./")
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
                fmt.Printf("  ⚠️  未匹配: %s\n", cleanPath)
            }
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
    
    // 1. 处理主JS文件
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
    
    // 2. 处理主CSS文件
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
    mapPath:= ".version-map.json"
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
    
    flag.Parse()
    
    // 加载配置
    config, err := loadConfig(*configPath)
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
    
    vm := NewVersionManager(*config, *debugMode)
    
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
        fmt.Println("⚠️  未指定要处理的HTML文件")
        fmt.Println("使用 -file 指定文件, -all 扫描所有, 或在配置文件中指定")
        flag.Usage()
    }
}