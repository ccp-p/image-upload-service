package main

import (
	"archive/zip"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"unicode"
)

const (
	OutputDir = `D:\download\archive`
)

var ProjectPaths = []string{
	`D:\project\cx_project\china_mobile\chartityProject\gitSourceCode\charity-open-fronted\pc\dev`,
	`D:\project\cx_project\china_mobile\chartityProject\gitSourceCode\charity-open-fronted\wap\dev`,
	`D:\project\cx_project\china_mobile\chartityProject\gitSourceCode\charity-open-fronted\wap\AnnualReport`,
	`D:\project\cx_project\china_mobile\chartityProject\gitSourceCode\charity-manage-fronted\dev`,
	`D:\project\cx_project\china_mobile\chartityProject\gitSourceCode\charity-open-fronted\pc\yiqijuan`,
}

// 忽略的目录和文件
var IgnoreList = []string{
	"node_modules",
	".git",
	".svn",
	".idea",
	".vscode",
	"dist",
	"build",
	".DS_Store",
	"Thumbs.db",
	"*.log",
}

func main() {
	// 确保输出目录存在
	if err := os.MkdirAll(OutputDir, 0755); err != nil {
		fmt.Printf("创建输出目录失败: %v\n", err)
		return
	}

	var wg sync.WaitGroup

	for _, projectPath := range ProjectPaths {
		wg.Add(1)
		go func(path string) {
			defer wg.Done()
			processProject(path)
		}(projectPath)
	}

	wg.Wait()
	fmt.Println("\n所有项目处理完成!")
}

func processProject(projectPath string) {
	// 检查目录是否存在
	info, err := os.Stat(projectPath)
	if err != nil || !info.IsDir() {
		fmt.Printf("❌ 目录不存在: %s\n", projectPath)
		return
	}

	// 查找有 package.json 的项目根目录
	projectRoot := findProjectRoot(projectPath)
	if projectRoot == "" {
		fmt.Printf("⚠️  不是前端项目 (无package.json): %s\n", projectPath)
		return
	}

	// 生成 zip 文件名
	zipName := generateZipName(projectRoot, projectPath)
	zipPath := filepath.Join(OutputDir, zipName+".zip")

	fmt.Printf("📦 开始打包: %s\n", projectPath)
	fmt.Printf("   输出: %s\n", zipPath)

	fmt.Printf("   项目根目录: %s\n", projectRoot)

	// 创建 zip 文件
	zipFile, err := os.Create(zipPath)
	if err != nil {
		fmt.Printf("❌ 创建zip文件失败: %v\n", err)
		return
	}
	defer zipFile.Close()

	writer := zip.NewWriter(zipFile)
	defer writer.Close()

	// 统计
	fileCount := 0

	// 遍历项目目录
	err = filepath.Walk(projectRoot, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return nil
		}

		// 获取相对路径
		relPath, _ := filepath.Rel(projectRoot, path)
		if relPath == "." {
			return nil
		}

		// 检查是否应该忽略
		if shouldIgnore(path, info) {
			if info.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}

		// 如果是目录，跳过（zip 会自动创建目录结构）
		if info.IsDir() {
			return nil
		}

		// 添加文件到 zip
		if err := addFileToZip(writer, path, relPath); err != nil {
			fmt.Printf("   ⚠️  跳过文件 %s: %v\n", relPath, err)
			return nil
		}

		fileCount++
		if fileCount%100 == 0 {
			fmt.Printf("   已处理 %d 个文件...\n", fileCount)
		}

		return nil
	})

	if err != nil {
		fmt.Printf("❌ 打包失败: %v\n", err)
		return
	}

	// 获取 zip 文件大小
	zipInfo, _ := os.Stat(zipPath)
	sizeMB := float64(zipInfo.Size()) / 1024 / 1024

	fmt.Printf("✅ 打包完成: %s -> %s\n", projectPath, zipName)
	fmt.Printf("   文件数: %d, 大小: %.2f MB\n", fileCount, sizeMB)
}

func findProjectRoot(dir string) string {
	// 先检查当前目录
	if _, err := os.Stat(filepath.Join(dir, "package.json")); err == nil {
		return dir
	}

	// 向上查找父目录
	parent := filepath.Dir(dir)
	if parent == dir {
		return "" // 已到根目录
	}

	// 检查父目录
	if _, err := os.Stat(filepath.Join(parent, "package.json")); err == nil {
		return parent
	}

	return ""
}

func generateZipName(projectRoot, projectPath string) string {
	// 获取 projectPath 相对于 projectRoot 的子路径
	relPath, err := filepath.Rel(projectRoot, projectPath)
	if err != nil || relPath == "." {
		// projectPath 就是 projectRoot，使用项目目录名
		relPath = filepath.Base(projectRoot)
	}

	// 将子路径按分隔符分割
	parts := strings.Split(relPath, string(filepath.Separator))

	// 如果子路径只有一级，需要加上父目录名来区分
	// 例如: charity-manage-fronted\dev -> manageDev
	if len(parts) <= 1 {
		// 获取 projectRoot 的父目录名
		parentDir := filepath.Base(filepath.Dir(projectRoot))
		parentParts := strings.FieldsFunc(parentDir, func(r rune) bool {
			return r == '-' || r == '_' || r == ' '
		})

		// 取最后1-2个有意义的词
		if len(parentParts) > 2 {
			parentParts = parentParts[len(parentParts)-2:]
		}

		// 转换为驼峰并加入
		for _, p := range parentParts {
			p = strings.ToLower(p)
			if p != "charity" && p != "fronted" && p != "open" && p != "manage" {
				parts = append([]string{p}, parts...)
			}
		}

		// 如果还是只有原始目录名，加上 "manage" 或 "open" 前缀
		if len(parts) == 1 {
			if strings.Contains(projectRoot, "manage") {
				parts = append([]string{"manage"}, parts...)
			} else if strings.Contains(projectRoot, "open") {
				// 不加前缀，保持原样
			}
		}
	}

	// 转换为驼峰命名
	var camelParts []string
	for _, part := range parts {
		if part != "." && part != "" {
			camelParts = append(camelParts, toCamelCase(part))
		}
	}

	// 拼接并首字母小写
	result := strings.Join(camelParts, "")
	if len(result) > 0 {
		runes := []rune(result)
		runes[0] = unicode.ToLower(runes[0])
		result = string(runes)
	}

	return result
}

func toCamelCase(s string) string {
	// 按连字符、下划线、空格分割
	parts := strings.FieldsFunc(s, func(r rune) bool {
		return r == '-' || r == '_' || r == ' '
	})

	var result strings.Builder
	for _, part := range parts {
		if len(part) == 0 {
			continue
		}
		// 首字母大写
		runes := []rune(part)
		runes[0] = unicode.ToUpper(runes[0])
		result.WriteString(string(runes))
	}

	return result.String()
}

func shouldIgnore(path string, info os.FileInfo) bool {
	name := info.Name()

	for _, ignore := range IgnoreList {
		// 检查通配符
		if strings.HasPrefix(ignore, "*") {
			suffix := ignore[1:]
			if strings.HasSuffix(name, suffix) {
				return true
			}
			continue
		}

		// 精确匹配目录名或文件名
		if name == ignore {
			return true
		}
	}

	return false
}

func addFileToZip(zipWriter *zip.Writer, filePath, relPath string) error {
	// 打开文件
	file, err := os.Open(filePath)
	if err != nil {
		return err
	}
	defer file.Close()

	// 获取文件信息
	info, err := file.Stat()
	if err != nil {
		return err
	}

	// 创建 zip 文件头
	header, err := zip.FileInfoHeader(info)
	if err != nil {
		return err
	}

	// 使用相对路径
	header.Name = filepath.ToSlash(relPath)
	header.Method = zip.Deflate

	// 创建 writer
	writer, err := zipWriter.CreateHeader(header)
	if err != nil {
		return err
	}

	// 复制文件内容
	buf := make([]byte, 32*1024) // 32KB buffer
	for {
		n, readErr := file.Read(buf)
		if n > 0 {
			if _, werr := writer.Write(buf[:n]); werr != nil {
				return werr
			}
		}
		if readErr != nil {
			if readErr.Error() == "EOF" {
				return nil
			}
			return readErr
		}
	}
}
