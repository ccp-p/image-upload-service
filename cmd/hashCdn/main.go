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

// findAndDeleteOldHashFiles 查找并删除旧的hash文件
func (vm *VersionManager) findAndDeleteOldHashFiles(dir, basename, ext, currentHash string) error {
    fmt.Printf("  🔍 开始查找旧hash文件: dir=%s, basename=%s, ext=%s, currentHash=%s\n", dir, basename, ext, currentHash)
    
    // 构建更灵活的正则表达式
    pattern := fmt.Sprintf(`^%s\.[a-f0-9]{8}%s$`, regexp.QuoteMeta(basename), regexp.QuoteMeta(ext))
    re := regexp.MustCompile(pattern)
    
    fmt.Printf("  📋 正则模式: %s\n", pattern)
    
    files, err := os.ReadDir(dir)
    if err != nil {
        fmt.Printf("  ❌ 读取目录失败: %v\n", err)
        return err
    }
    
    fmt.Printf("  📂 目录中找到 %d 个文件\n", len(files))
    
    var oldFiles []os.FileInfo
    for _, file := range files {
        if !file.IsDir() {
            filename := file.Name()
            fmt.Printf("    检查文件: %s\n", filename)
            
            // 测试正则匹配
            matches := re.MatchString(filename)
            fmt.Printf("      正则匹配结果: %t\n", matches)
            
            if matches {
                fmt.Printf("      ✓ 匹配正则: %s\n", filename)
                
                // 提取hash部分 - 更精确的提取方法
                // 格式: basename.hash.ext
                expectedPattern := fmt.Sprintf(`^%s\.([a-f0-9]{8})%s$`, regexp.QuoteMeta(basename), regexp.QuoteMeta(ext))
                hashRe := regexp.MustCompile(expectedPattern)
                hashMatches := hashRe.FindStringSubmatch(filename)
                
                if len(hashMatches) >= 2 {
                    extractedHash := hashMatches[1]
                    fmt.Printf("      🔍 提取hash: %s, 当前hash: %s\n", extractedHash, currentHash)
                    
                    if extractedHash != currentHash {
                        fileInfo, err := file.Info()
                        if err != nil {
                            fmt.Printf("      ❌ 获取文件信息失败: %v\n", err)
                            continue
                        }
                        oldFiles = append(oldFiles, fileInfo)
                        fmt.Printf("      ✅ 标记为旧文件: %s (hash: %s)\n", filename, extractedHash)
                    } else {
                        fmt.Printf("      ℹ️  当前文件，跳过: %s\n", filename)
                    }
                } else {
                    fmt.Printf("      ⚠️  无法提取hash: %s (正则未匹配)\n", filename)
                }
            } else {
                fmt.Printf("      ✗ 不匹配正则: %s\n", filename)
                
                // 额外测试：检查是否包含basename
                if strings.Contains(filename, basename) {
                    fmt.Printf("        ℹ️  包含basename，但格式不匹配\n")
                    // 检查是否可能是其他格式
                    parts := strings.Split(filename, ".")
                    if len(parts) >= 3 {
                        fmt.Printf("        ℹ️  文件拆分: %v\n", parts)
                    }
                }
            }
        }
    }
    
    // 删除所有找到的旧文件
    fmt.Printf("  🗑️ 准备删除 %d 个旧文件\n", len(oldFiles))
    for _, oldFile := range oldFiles {
        oldFilePath := filepath.Join(dir, oldFile.Name())
        if err := os.Remove(oldFilePath); err != nil {
            fmt.Printf("    ❌ 删除旧文件失败 %s: %v\n", oldFile.Name(), err)
        } else {
            fmt.Printf("    ✅ 删除旧hash文件: %s\n", oldFile.Name())
        }
    }
    
    return nil
}

// renameFileWithHash 重命名文件（如果hash改变）
func (vm *VersionManager) renameFileWithHash(filePath string) (*FileInfo, error) {
    dir := filepath.Dir(filePath)
    filename := filepath.Base(filePath)
    cleanFilename := vm.removeHashFromFilename(filename)
    
    fmt.Printf("  📁 处理文件: %s, 目录: %s\n", filename, dir)
    fmt.Printf("  📁 清理后的文件名: %s\n", cleanFilename)
    
    // 确定源文件路径（优先使用无hash的原始文件）
    cleanPath := filepath.Join(dir, cleanFilename)
    sourcePath := filePath
    if fileExists(cleanPath) {
        sourcePath = cleanPath
    }
    
    fmt.Printf("  📄 源文件路径: %s\n", sourcePath)
    
    // 计算hash（基于源文件）
    hash, err := vm.calculateFileHash(sourcePath)
    if err != nil {
        return nil, err
    }
    
    fmt.Printf("  🔑 计算出的hash: %s\n", hash)
    
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
    
    // 删除旧的hash文件
    ext := filepath.Ext(cleanFilename)
    basename := strings.TrimSuffix(cleanFilename, ext)
    fmt.Printf("  🧹 准备删除旧文件: dir=%s, basename=%s, ext=%s, currentHash=%s\n", dir, basename, ext, hash)
    if err := vm.findAndDeleteOldHashFiles(dir, basename, ext, hash); err != nil {
        fmt.Printf("  ⚠️  查找旧文件时出错: %v\n", err)
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
    
    // 收集JS文件（只收集components目录下的JS，主JS会单独处理）
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
    
    fmt.Printf("    🔧 处理组件资源: %s -> %s\n", relativePath, actualPath)
    
    // 检查是否已经处理过
    vm.mu.Lock()
    if vm.processedFiles[actualPath] {
        vm.mu.Unlock()
        // 返回已处理的信息
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
    
    fmt.Printf("    📝 处理CSS文件: %s\n", cleanFilename)
    
    // 收集并处理CSS中的图片
    images, err := vm.collectImagesFromCSS(originalCssPath)
    if err != nil {
        return nil, err
    }
    
    imageMap := make(map[string]string)
    
    if len(images) > 0 {
        fmt.Printf("    📸 找到 %d 个图片引用\n", len(images))
        
        for _, image := range images {
            vm.mu.Lock()
            if vm.processedFiles[image.AbsolutePath] {
                vm.mu.Unlock()
                // 获取已处理的图片hash文件名
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
                fmt.Printf("      ⚠️  处理图片失败 %s: %v\n", filepath.Base(image.AbsolutePath), err)
                continue
            }
            
            newImageFilename := filepath.Base(info.HashedPath)
            imageMap[image.OriginalPath] = newImageFilename
            
            fmt.Printf("      ✅ 图片: %s -> %s\n", filepath.Base(image.AbsolutePath), newImageFilename)
            
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
    fmt.Printf("    🧹 删除旧CSS文件: dir=%s, basename=%s, ext=%s, currentHash=%s\n", cssDir, cssBasename, cssExt, originalHash)
    if err := vm.findAndDeleteOldHashFiles(cssDir, cssBasename, cssExt, originalHash); err != nil {
        fmt.Printf("      ⚠️  查找旧CSS文件时出错: %v\n", err)
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
    
    // 处理CSS引用（包括组件）
    if cssMap, ok := resources["css"]; ok {
        for originalRelPath, newFilename := range cssMap {
            // 规范化路径 - 统一使用正斜杠
            cleanPath := strings.TrimPrefix(originalRelPath, "./")
            cleanPath = strings.ReplaceAll(cleanPath, "\\", "/")
            
            // 移除可能的hash
            cleanPath = vm.removeHashFromFilename(cleanPath)
            
            fmt.Printf("  🔍 尝试匹配CSS: %s (原始: %s)\n", cleanPath, originalRelPath)
            
            // 转义特殊字符，同时匹配反斜杠和正斜杠
            escapedPath := regexp.QuoteMeta(cleanPath)
            escapedPath = strings.ReplaceAll(escapedPath, "/", `[/\\]`)
            
            // 构建多个匹配模式
            patterns := []string{
                // 精确匹配完整路径（支持反斜杠和正斜杠）
                fmt.Sprintf(`(<link[^>]*href\s*=\s*['"])(%s)(['"][^>]*>)`, escapedPath),
                // 匹配带 ./ 前缀的路径
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
                            
                            // 保留原路径的目录部分，只替换文件名（统一使用正斜杠）
                            oldPath = strings.ReplaceAll(oldPath, "\\", "/")
                            dir := filepath.ToSlash(filepath.Dir(oldPath))
                            var newPath string
                            if dir == "." || dir == "" {
                                newPath = newFilename
                            } else {
                                // 保留原始路径格式，直接拼接
                                newPath = dir + "/" + newFilename
                            }
                            
                            // 添加CDN域名（如果配置了）
                            if vm.config.CDNDomain != "" && !strings.HasPrefix(newPath, "http") {
                                // 移除开头的 ./，但保留 ../
                                cleanNewPath := newPath
                                if strings.HasPrefix(cleanNewPath, "./") {
                                    cleanNewPath = strings.TrimPrefix(cleanNewPath, "./")
                                }
                                newPath = vm.config.CDNDomain + "/" + cleanNewPath
                            }
                            
                            result := fmt.Sprintf("%s%s%s", prefix, newPath, suffix)
                            
                            if match != result {
                                updated = true
                                matched = true
                                fmt.Printf("    ✅ CSS: %s -> %s\n", oldPath, newPath)
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
                fmt.Printf("    ⚠️  未匹配到CSS: %s\n", cleanPath)
            }
        }
    }
    
    // 处理JS引用（包括组件）
    if jsMap, ok := resources["js"]; ok {
        for originalRelPath, newFilename := range jsMap {
            // 规范化路径 - 统一使用正斜杠
            cleanPath := strings.TrimPrefix(originalRelPath, "./")
            cleanPath = strings.ReplaceAll(cleanPath, "\\", "/")
            
            // 移除可能的hash
            cleanPath = vm.removeHashFromFilename(cleanPath)
            
            fmt.Printf("  🔍 尝试匹配JS: %s (原始: %s)\n", cleanPath, originalRelPath)
            
            // 转义特殊字符，同时匹配反斜杠和正斜杠
            escapedPath := regexp.QuoteMeta(cleanPath)
            escapedPath = strings.ReplaceAll(escapedPath, "/", `[/\\]`)
            
            // 构建多个匹配模式
            patterns := []string{
                // 精确匹配完整路径（支持反斜杠和正斜杠）
                fmt.Sprintf(`(<script[^>]*src\s*=\s*['"])(%s)(['"][^>]*>)`, escapedPath),
                // 匹配带 ./ 前缀的路径
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
                            
                            // 保留原路径的目录部分，只替换文件名（统一使用正斜杠）
                            oldPath = strings.ReplaceAll(oldPath, "\\", "/")
                            dir := filepath.ToSlash(filepath.Dir(oldPath))
                            var newPath string
                            if dir == "." || dir == "" {
                                newPath = newFilename
                            } else {
                                // 保留原始路径格式，直接拼接
                                newPath = dir + "/" + newFilename
                            }
                            
                            // 添加CDN域名（如果配置了）
                            if vm.config.CDNDomain != "" && !strings.HasPrefix(newPath, "http") {
                                // 移除开头的 ./，但保留 ../
                                cleanNewPath := newPath
                                if strings.HasPrefix(cleanNewPath, "./") {
                                    cleanNewPath = strings.TrimPrefix(cleanNewPath, "./")
                                }
                                newPath = vm.config.CDNDomain + "/" + cleanNewPath
                            }
                            
                            result := fmt.Sprintf("%s%s%s", prefix, newPath, suffix)
                            
                            if match != result {
                                updated = true
                                matched = true
                                fmt.Printf("    ✅ JS: %s -> %s\n", oldPath, newPath)
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
                fmt.Printf("    ⚠️  未匹配到JS: %s\n", cleanPath)
            }
        }
    }
    
    if updated {
        if err := os.WriteFile(htmlPath, []byte(contentStr), 0644); err != nil {
            return err
        }
        fmt.Printf("\n    ✅ HTML文件已更新\n")
    } else {
        fmt.Printf("\n    ⚠️  没有内容需要更新\n")
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
    
    fmt.Printf("📂 HTML目录: %s\n", htmlDir)
    fmt.Printf("📝 HTML基础名: %s\n", htmlBasename)
    
    resources := map[string]map[string]string{
        "css": make(map[string]string),
        "js":  make(map[string]string),
    }
    
    // 1. 处理主JS文件（与HTML同名的JS）
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
            fmt.Printf("  📁 找到主JS路径: %s\n", actualJsPath)
            info, err := vm.renameFileWithHash(actualJsPath)
            if err != nil {
                fmt.Printf("  ❌ 处理JS失败: %v\n", err)
                continue
            }
            
            // 计算相对于HTML目录的路径
            relPath, _ := filepath.Rel(htmlDir, actualJsPath)
            relPath = filepath.ToSlash(relPath)
            
            // 同时记录多种可能的路径格式
            resources["js"][relPath] = filepath.Base(info.HashedPath)
            resources["js"]["./"+relPath] = filepath.Base(info.HashedPath)
            
            fmt.Printf("  ✅ 主JS: %s -> %s\n", filepath.Base(actualJsPath), filepath.Base(info.HashedPath))
            mainJsFound = true
            break
        } else {
            fmt.Printf("  ❌ 未找到JS路径: %s\n", jsPath)
        }
    }
    
    if !mainJsFound {
        fmt.Printf("  ℹ️  未找到主JS文件 (%s.js)\n", htmlBasename)
    }
    
    // 2. 处理主CSS文件（与HTML同名的CSS）
    fmt.Println("\n🎨 处理主 CSS 文件...")
    
    cssPaths := []string{
        filepath.Join(htmlDir, htmlBasename+".css"),
        filepath.Join(htmlDir, "css", htmlBasename+".css"),
    }
    
    mainCssFound := false
    for _, cssPath := range cssPaths {
        actualCssPath := vm.findFile(cssPath)
        if actualCssPath != "" {
            fmt.Printf("  📁 找到主CSS路径: %s\n", actualCssPath)
            info, err := vm.processComponentCSS(actualCssPath)
            if err != nil {
                fmt.Printf("  ❌ 处理CSS失败: %v\n", err)
                continue
            }
            
            // 计算相对于HTML目录的路径
            relPath, _ := filepath.Rel(htmlDir, actualCssPath)
            relPath = filepath.ToSlash(relPath)
            
            // 同时记录多种可能的路径格式
            resources["css"][relPath] = filepath.Base(info.HashedPath)
            resources["css"]["./"+relPath] = filepath.Base(info.HashedPath)
            
            fmt.Printf("  ✅ 主CSS: %s -> %s\n", filepath.Base(actualCssPath), filepath.Base(info.HashedPath))
            mainCssFound = true
            break
        } else {
            fmt.Printf("  ❌ 未找到CSS路径: %s\n", cssPath)
        }
    }
    
    if !mainCssFound {
        fmt.Printf("  ℹ️  未找到主CSS文件 (%s.css)\n", htmlBasename)
    }
    
    // 3. 收集并处理组件资源
    fmt.Println("\n🔍 扫描组件资源...")
    htmlResources, err := vm.collectResourcesFromHTML(htmlPath)
    if err != nil {
        return fmt.Errorf("扫描HTML失败: %v", err)
    }
    
    fmt.Printf("  找到 %d 个组件CSS引用\n", len(htmlResources["css"]))
    fmt.Printf("  找到 %d 个组件JS引用\n", len(htmlResources["js"]))
    
    // 4. 处理组件JS文件
    if len(htmlResources["js"]) > 0 {
        fmt.Println("\n🔧 处理组件 JavaScript 文件...")
        for _, jsRelPath := range htmlResources["js"] {
            fmt.Printf("  🔧 处理组件JS: %s\n", jsRelPath)
            info, err := vm.processComponentResource(htmlDir, jsRelPath)
            if err != nil {
                fmt.Printf("    ❌ 失败: %v\n", err)
                continue
            }
            
            // 使用HTML中的原始路径作为key
            resources["js"][jsRelPath] = filepath.Base(info.HashedPath)
            
            fmt.Printf("    ✅ %s -> %s\n", filepath.Base(info.OriginalPath), filepath.Base(info.HashedPath))
        }
    }
    
    // 5. 处理组件CSS文件
    if len(htmlResources["css"]) > 0 {
        fmt.Println("\n🔧 处理组件 CSS 文件...")
        for _, cssRelPath := range htmlResources["css"] {
            fmt.Printf("  🔧 处理组件CSS: %s\n", cssRelPath)
            info, err := vm.processComponentResource(htmlDir, cssRelPath)
            if err != nil {
                fmt.Printf("    ❌ 失败: %v\n", err)
                continue
            }
            
            // 使用HTML中的原始路径作为key
            resources["css"][cssRelPath] = filepath.Base(info.HashedPath)
            
            fmt.Printf("    ✅ %s -> %s\n", filepath.Base(info.OriginalPath), filepath.Base(info.HashedPath))
        }
    }
    
    // 6. 更新HTML中的引用
    fmt.Println("\n🔄 更新HTML中的资源引用...")
    fmt.Printf("  📋 需要更新的CSS (%d 项):\n", len(resources["css"]))
    for k, v := range resources["css"] {
        fmt.Printf("    - %s -> %s\n", k, v)
    }
    fmt.Printf("  📋 需要更新的JS (%d 项):\n", len(resources["js"]))
    for k, v := range resources["js"] {
        fmt.Printf("    - %s -> %s\n", k, v)
    }
    
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
    htmlFile := flag.String("file", "D:\\self_project\\go_project\\image-upload-service\\test\\index.html", "单个HTML文件路径")
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