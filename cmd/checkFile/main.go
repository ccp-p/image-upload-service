package main

import (
	"encoding/json"
	"fmt"
	"image"
	_ "image/gif"
	_ "image/jpeg"
	_ "image/png"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

// Config 配置
const (
	SourceDir     = `C:\Users\83795\Downloads\compressed`
)
var CacheFileName = "image_path_map.json"

// TargetFileInfo 目标文件信息
type TargetFileInfo struct {
	Path   string `json:"path"`
	Width  int    `json:"width"`
	Height int    `json:"height"`
	Size   int64  `json:"size"`
}

// CacheData 缓存数据结构
type CacheData struct {
	TotalCount int                         `json:"totalCount"`
	LastUpdate time.Time                   `json:"lastUpdate"`
	Mapping    map[string][]TargetFileInfo `json:"mapping"`
}

func main() {
	// 1. 确定 BasePath
	isHome := os.Getenv("IS_HOME") == "1"
	var destBasePath string
	if isHome {
		destBasePath = `D:\job_project\china_mobile\gitProject\richinfo_tyjf_xhmqqthy\src\main\webapp\res\wap`
		CacheFileName = "image_path_map_home.json"
	} else {
		destBasePath = `D:\project\cx_project\china_mobile\gitProject\richinfo_tyjf_xhmqqthy\src\main\webapp\res\wap`
	}

	fmt.Printf("当前环境: %v\n目标根目录: %s\n", isHome, destBasePath)

	// 2. 检查并更新缓存
	cache, err := ensureCache(destBasePath)
	if err != nil {
		fmt.Printf("缓存处理失败: %v\n", err)
		return
	}

	// 3. 处理压缩图片
	processCompressedImages(SourceDir, cache)
}

// ensureCache 确保缓存是最新的
func ensureCache(basePath string) (*CacheData, error) {
	// 扫描当前目录获取图片总数
	staticList:= []string{
		"components/xdrsign/static",
		"images/xdrNormal/202505",
		"components/xdrInvite/static/202510",
	}
	currentCount := 0
	for _, subPath := range staticList {
		fullPath := filepath.Join(basePath, subPath)
		currentCount += countImages(fullPath)
	}
	fmt.Printf("当前目录图片总数: %d\n", currentCount)

	// 尝试读取缓存
	cache, err := loadCache()
	if err == nil {
		if cache.TotalCount == currentCount {
			fmt.Println("缓存有效，直接使用")
			return cache, nil
		}
		fmt.Printf("缓存过期 (缓存: %d, 实际: %d)，重新扫描...\n", cache.TotalCount, currentCount)
	} else {
		fmt.Println("缓存不存在或读取失败，开始扫描...")
	}
    newCache := &CacheData{
		Mapping: make(map[string][]TargetFileInfo),
		TotalCount: 0,
	}
	// 重新构建缓存
	for _, subPath := range staticList {
		fullPath:= filepath.Join(basePath, subPath)
		subCache := buildCache(fullPath)
		// 合并子缓存
		for k, v := range subCache.Mapping {
			newCache.Mapping[k] = append(newCache.Mapping[k], v...)
		}
	}
	newCache.TotalCount = currentCount // 确保计数一致
	if err := saveCache(newCache); err != nil {
		fmt.Printf("警告: 保存缓存失败: %v\n", err)
	}
	return newCache, nil
}
func isHashImageFileName(filename string) bool {
	// afterGetEquityPop.88ade0f6.png
	hashPattern := `\.[a-f0-9]{6,}\.(png|jpg|jpeg|gif)$`
	isHashImg, _ := regexp.MatchString(hashPattern, filename)
	return isHashImg
}
// countImages 快速计算图片数量
func countImages(root string) int {
	count := 0
	filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() {
			return nil
		}
		// is hash 1.2a351e.png
		
		if isImage(path) && !isHashImageFileName(info.Name()) {
			count++
		}
		return nil
	})
	return count
}

// buildCache 构建路径映射
func buildCache(root string) *CacheData {
	mapping := make(map[string][]TargetFileInfo)
	count := 0

	filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() {
			return nil
		}
		if !isImage(path) || isHashImageFileName(info.Name()) {
			return nil
		}

		count++
		width, height := getImageDimensions(path)
		
		fileInfo := TargetFileInfo{
			Path:   path,
			Width:  width,
			Height: height,
			Size:   info.Size(),
		}

		filename := info.Name()
		mapping[filename] = append(mapping[filename], fileInfo)
		
		if count%100 == 0 {
			fmt.Printf("\r已扫描: %d", count)
		}
		return nil
	})
	fmt.Println()

	return &CacheData{
		TotalCount: count,
		LastUpdate: time.Now(),
		Mapping:    mapping,
	}
}

// processCompressedImages 处理源目录下的图片
func processCompressedImages(sourceDir string, cache *CacheData) {
	files, err := os.ReadDir(sourceDir)
	if err != nil {
		fmt.Printf("读取源目录失败: %v\n", err)
		return
	}

	fmt.Println("开始处理图片移动...")
	for _, file := range files {
		if file.IsDir() || !isImage(file.Name()) {
			continue
		}

		sourcePath := filepath.Join(sourceDir, file.Name())
		width, height := getImageDimensions(sourcePath)
		sourceSize, _ := getFileSize(sourcePath)

		candidates, ok := cache.Mapping[file.Name()]
		if !ok {
			fmt.Printf("❌ 未找到目标: %s\n", file.Name())
			continue
		}

		// 筛选匹配的候选文件
		var matched []TargetFileInfo
		// 1. 优先匹配宽高
		for _, c := range candidates {
			if c.Width == width && c.Height == height {
				matched = append(matched, c)
			}
		}
		// matched
		fmt.Printf("🔍 处理: %s (Size: %d, %dx%d), 候选数: %d, 匹配数: %d\n", file.Name(), sourceSize, width, height, len(candidates), len(matched))

		targetPath := ""
		if len(matched) == 1 {
			targetPath = matched[0].Path
		} else if len(matched) > 1 {
			// 尝试通过大小进一步区分（仅当源文件和目标文件大小时）
			fmt.Printf("⚠️  存在歧义 (%d 个匹配): %s (Size: %d, %dx%d)\n", len(matched), file.Name(), sourceSize, width, height)
			for _, m := range matched {
				fmt.Printf("   - 候选: %s (Size: %d, %dx%d)\n", m.Path, m.Size, m.Width, m.Height)
			}
			// 简单的策略：如果无法区分，跳过
			continue
		} else {
			// 宽高都不匹配
			// 尝试回退到文件名匹配（如果有且仅有一个同名文件）
			if len(candidates) == 1 {
				fmt.Printf("⚠️  宽高不匹配但仅有一个同名文件，强制匹配: %s\n", file.Name())
				targetPath = candidates[0].Path
			} else {
				fmt.Printf("❌ 宽高不匹配且有多个同名文件: %s\n", file.Name())
				continue
			}
		}

		if targetPath != "" {
			err := moveFile(sourcePath, targetPath)
			if err != nil {
				fmt.Printf("❌ 移动失败 %s -> %s: %v\n", file.Name(), targetPath, err)
			} else {
				fmt.Printf("✅ 移动成功: %s -> %s\n", file.Name(), targetPath)
			}
		}
	}
}

// 辅助函数

func isImage(filename string) bool {
	ext := strings.ToLower(filepath.Ext(filename))
	return ext == ".png" || ext == ".jpg" || ext == ".jpeg" || ext == ".gif"
}

func getImageDimensions(path string) (int, int) {
	file, err := os.Open(path)
	if err != nil {
		return 0, 0
	}
	defer file.Close()

	config, _, err := image.DecodeConfig(file)
	if err != nil {
		return 0, 0
	}
	return config.Width, config.Height
}

func getFileSize(path string) (int64, error) {
	info, err := os.Stat(path)
	if err != nil {
		return 0, err
	}
	return info.Size(), nil
}

func loadCache() (*CacheData, error) {
	file, err := os.Open(CacheFileName)
	if err != nil {
		return nil, err
	}
	defer file.Close()

	var data CacheData
	if err := json.NewDecoder(file).Decode(&data); err != nil {
		return nil, err
	}
	return &data, nil
}

func saveCache(data *CacheData) error {
	file, err := os.Create(CacheFileName)
	if err != nil {
		return err
	}
	defer file.Close()

	encoder := json.NewEncoder(file)
	encoder.SetIndent("", "  ")
	return encoder.Encode(data)
}

func moveFile(src, dst string) error {
	// 跨盘符移动需要 Copy + Remove
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
	if err != nil {
		return err
	}

	// 关闭文件后删除源文件
	sourceFile.Close()
	destFile.Close()
	
	return os.Remove(src)
}
