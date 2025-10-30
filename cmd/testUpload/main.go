package main

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"
)

const (
	sourceDir   = `C:\Users\83795\Downloads\compressed`
	defaultDest  = `D:\project\cx_project\china_mobile\gitProject\richinfo_tyjf_xhmqqthy\src\main\webapp\res\wap\images\xdrNormal\202505`
	maxRetries   = 3
	retryDelay   = 500 * time.Millisecond
)

// 前缀到目标目录的映射
var prefixDestMap = map[string]string{
	"invite": `D:\project\cx_project\china_mobile\gitProject\richinfo_tyjf_xhmqqthy\src\main\webapp\res\wap\components\xdrInvite\static\202510`,
	// 可以在这里添加更多前缀映射
	// "other": `D:\path\to\other\directory`,
}

// 支持的图片扩展名
var imageExtensions = []string{".jpg", ".jpeg", ".png", ".gif", ".bmp", ".webp"}

func main() {
	fmt.Println("开始移动图片...")
	fmt.Printf("源目录: %s\n", sourceDir)

	// 检查源目录是否存在
	if _, err := os.Stat(sourceDir); os.IsNotExist(err) {
		fmt.Printf("错误: 源目录不存在: %s\n", sourceDir)
		fmt.Println("按任意键退出...")
		fmt.Scanln()
		return
	}

	// 读取源目录中的所有文件
	files, err := os.ReadDir(sourceDir)
	if err != nil {
		fmt.Printf("错误: 无法读取源目录: %v\n", err)
		fmt.Println("按任意键退出...")
		fmt.Scanln()
		return
	}

	movedCount := 0
	skippedCount := 0
	failedFiles := []string{}

	for _, file := range files {
		if file.IsDir() {
			continue
		}

		fileName := file.Name()
		ext := strings.ToLower(filepath.Ext(fileName))

		// 检查是否为图片文件
		if !isImageFile(ext) {
			fmt.Printf("跳过非图片文件: %s\n", fileName)
			skippedCount++
			continue
		}

		// 根据文件名前缀确定目标目录
		destDir := getDestDirectory(fileName)

		// 确保目标目录存在
		if err := os.MkdirAll(destDir, 0755); err != nil {
			fmt.Printf("错误: 无法创建目标目录 %s: %v\n", destDir, err)
			failedFiles = append(failedFiles, fileName)
			continue
		}

		// 移动文件（带重试）
		sourcePath := filepath.Join(sourceDir, fileName)
		destPath := filepath.Join(destDir, fileName)

		if err := moveFileWithRetry(sourcePath, destPath); err != nil {
			fmt.Printf("✗ 失败: %s (原因: %v)\n", fileName, err)
			failedFiles = append(failedFiles, fileName)
			continue
		}

		fmt.Printf("✓ 已移动: %s -> %s\n", fileName, destDir)
		movedCount++
	}

	// 显示结果
	fmt.Println("\n==================")
	fmt.Printf("移动完成! 成功: %d, 跳过: %d, 失败: %d\n", movedCount, skippedCount, len(failedFiles))

	if len(failedFiles) > 0 {
		fmt.Println("\n失败的文件列表:")
		for _, f := range failedFiles {
			fmt.Printf("  - %s\n", f)
		}
		fmt.Println("\n提示: 请关闭可能占用这些文件的程序（如图片查看器、编辑器等），然后重新运行。")
	}

	fmt.Println("\n按任意键退出...")
	fmt.Scanln()
}

// 判断是否为图片文件
func isImageFile(ext string) bool {
	for _, imgExt := range imageExtensions {
		if ext == imgExt {
			return true
		}
	}
	return false
}

// 根据文件名前缀获取目标目录
func getDestDirectory(fileName string) string {
	for prefix, destDir := range prefixDestMap {
		if strings.HasPrefix(strings.ToLower(fileName), strings.ToLower(prefix)) {
			return destDir
		}
	}
	return defaultDest
}

// 带重试的移动文件
func moveFileWithRetry(sourcePath, destPath string) error {
	var lastErr error

	for i := 0; i < maxRetries; i++ {
		if i > 0 {
			fmt.Printf("  重试 %d/%d...\n", i, maxRetries-1)
			time.Sleep(retryDelay)
		}

		err := copyFile(sourcePath, destPath)
		if err == nil {
			// 复制成功，尝试删除源文件
			if err := os.Remove(sourcePath); err != nil {
				// 删除失败，但复制成功，记录警告
				fmt.Printf("  警告: 文件已复制但无法删除源文件: %v\n", err)
				return nil
			}
			return nil
		}
		lastErr = err
	}

	return lastErr
}

// 复制文件
func copyFile(sourcePath, destPath string) error {
	// 打开源文件
	sourceFile, err := os.Open(sourcePath)
	if err != nil {
		return err
	}
	defer sourceFile.Close()

	// 创建目标文件（如果存在则覆盖）
	destFile, err := os.Create(destPath)
	if err != nil {
		return err
	}
	defer destFile.Close()

	// 复制文件内容
	if _, err := io.Copy(destFile, sourceFile); err != nil {
		return err
	}

	// 确保写入完成
	if err := destFile.Sync(); err != nil {
		return err
	}

	return nil
}
