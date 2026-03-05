package main

import (
	"fmt"
	"image"
	_ "image/png"
	"io"
	"os"
	"path/filepath"
	"strings"
)

const (
	homeSourcePath    = "D:\\job_project\\china_mobile\\gitProject\\richinfo_tyjf_xhmqqthy\\src\\main\\webapp\\res\\wap"
	companySourcePath = "D:\\project\\cx_project\\china_mobile\\gitProject\\richinfo_tyjf_xhmqqthy\\src\\main\\webapp\\res\\wap"
	compressedPath    = "C:\\Users\\83795\\Downloads\\compressed"
	dateDir           = "202505"
)

func main() {
	isHome := os.Getenv("IS_HOME") == "1"
	basePath := companySourcePath
	envName := "公司电脑"
	if isHome {
		basePath = homeSourcePath
		envName = "家里电脑"
	}

	fmt.Printf("[开始处理] 当前环境: %s\n", envName)
	fmt.Printf("[基础路径] %s\n", basePath)

	files, err := os.ReadDir(compressedPath)
	if err != nil {
		fmt.Printf("[错误] 读取压缩目录失败: %v\n", err)
		return
	}

	count := 0
	for _, f := range files {
		if f.IsDir() {
			continue
		}
		processFile(basePath, f.Name())
		count++
	}
	fmt.Printf("[完成] 共处理 %d 个文件。\n", count)
}

func processFile(basePath, fileName string) {
	fullPath := filepath.Join(compressedPath, fileName)
	width, height := getImageDimension(fullPath)
	ext := filepath.Ext(fileName)
	nameOnly := strings.TrimSuffix(fileName, ext)

	var targetDir, cssPath, cssContent string

	// 规则判定
	if width == 220 && height == 220 {
		targetDir = filepath.Join(basePath, "components/xdrsign/static/popQy")
		cssPath = filepath.Join(basePath, "components/xdrsign/index.css")
		cssContent = fmt.Sprintf(".level-sign-popup .level-sign-popup-prize.%s {\n    background-image: url('../../components/xdrsign/static/popQy/%s');\n}\n", nameOnly, fileName)
	} else if width == 200 && height == 208 {
		targetDir = filepath.Join(basePath, "components/xdrsign/static")
		cssPath = filepath.Join(basePath, "components/xdrsign/index.css")
		cssContent = fmt.Sprintf(".level-sign-prize-wrapper #level-sign-prize-swiper .swiper-slide.%s {\n    background-image: url('../../components/xdrsign/static/%s');\n}\n", nameOnly, fileName)
	} else {
		targetDir = filepath.Join(basePath, "images/xdrNormal", dateDir)
		cssPath = filepath.Join(basePath, "css/xdrNormal.css")
		cssContent = generateNormalCSS(nameOnly, fileName)
	}

	// 1. 移动文件
	err := os.MkdirAll(targetDir, 0755)
	if err != nil {
		fmt.Printf("[错误] 创建目录失败 %s: %v\n", targetDir, err)
		return
	}

	err = moveFile(fullPath, filepath.Join(targetDir, fileName))
	if err != nil {
		fmt.Printf("[错误] 移动文件 %s 失败: %v\n", fileName, err)
		return
	}
	fmt.Printf("[移动] %s -> %s\n", fileName, targetDir)

	// 2. 追加 CSS
	err = appendCSS(cssPath, cssContent, nameOnly)
	if err != nil {
		fmt.Printf("[错误] 更新 CSS %s 失败: %v\n", cssPath, err)
	} else {
		fmt.Printf("[CSS] 成功更新样式至: %s\n", filepath.Base(cssPath))
	}
}

func generateNormalCSS(name, file string) string {
	if before, ok :=strings.CutSuffix(name, "_not_start"); ok  {
		cleanName := before
		return fmt.Sprintf(".level-award-center #XdrNotStartList #not-start-swiper .swiper-slide.%s {\n  background-image: url('../images/xdrNormal/%s/%s');\n}\n", cleanName, dateDir, file)
	}
	// ... 类似处理 _xdr, _xdr_r 等逻辑
	return fmt.Sprintf("/* %s */\n.level-award-prize .item.%s {\n    background-image: url('../images/xdrNormal/%s/%s');\n}\n", name, name, dateDir, file)
}

func appendCSS(path, content, keyword string) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	contentStr := string(data)

	// 1. 检查是否已存在 (防重复)
	if strings.Contains(contentStr, keyword) {
		fmt.Printf("[跳过] 样式已存在: %s\n", keyword)
		return nil
	}

	// 2. 寻找插入位置
	// 定义特征关键字，用于寻找同类样式的聚居区
	var searchKey string
	if strings.Contains(content, "#XdrNotStartList") {
		searchKey = "_not_start"
	} else if strings.Contains(content, "popQy") {
		searchKey = "popQy"
	} else if strings.Contains(content, "xdrsign") {
		searchKey = "xdrsign"
	} else {
		searchKey = "level-award-prize" // 默认普通奖品区
	}

	lastIdx := strings.LastIndex(contentStr, searchKey)
	insertIdx := len(contentStr)

	if lastIdx != -1 {
		// 找到该类样式最后一次出现后的闭合大括号位置
		afterLast := contentStr[lastIdx:]
		endBraceIdx := strings.Index(afterLast, "}")
		if endBraceIdx != -1 {
			insertIdx = lastIdx + endBraceIdx + 1
		}
	}

	// 3. 构建新内容并重写
	newContent := contentStr[:insertIdx] + "\n" + content + contentStr[insertIdx:]
	return os.WriteFile(path, []byte(newContent), 0644)
}

func getImageDimension(path string) (int, int) {
	file, _ := os.Open(path)
	defer file.Close()
	img, _, _ := image.DecodeConfig(file)
	return img.Width, img.Height
}

func moveFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.Create(dst)
	if err != nil {
		return err
	}
	defer out.Close()
	_, err = io.Copy(out, in)
	return err
}
