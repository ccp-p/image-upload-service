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

	info := make(map[string]string)
	info["fileName"] = fileName
	info["nameOnly"] = nameOnly

	// 规则判定
	if width == 220 && height == 220 {
		info["category"] = "popQy"
		info["targetDir"] = filepath.Join(basePath, "components/xdrsign/static/popQy")
		info["cssPath"] = filepath.Join(basePath, "components/xdrsign/index.css")
		info["cssContent"] = fmt.Sprintf(".level-sign-popup .level-sign-popup-prize.%s {\n    background-image: url('../../components/xdrsign/static/popQy/%s');\n}\n", nameOnly, fileName)
	} else if width == 200 && height == 208 {
		info["category"] = "signPrize"
		info["targetDir"] = filepath.Join(basePath, "components/xdrsign/static")
		info["cssPath"] = filepath.Join(basePath, "components/xdrsign/index.css")
		info["cssContent"] = fmt.Sprintf(".level-sign-prize-wrapper #level-sign-prize-swiper .swiper-slide.%s {\n    background-image: url('../../components/xdrsign/static/%s');\n}\n", nameOnly, fileName)
	} else {
		info["targetDir"] = filepath.Join(basePath, "images/xdrNormal", dateDir)
		info["cssPath"] = filepath.Join(basePath, "css/xdrNormal.css")
		if strings.HasSuffix(nameOnly, "_not_start") {
			info["category"] = "notStart"
		} else {
			info["category"] = "normalPrize"
		}
		info["cssContent"] = generateNormalCSS(nameOnly, fileName)
	}

	// 1. 移动文件
	err := os.MkdirAll(info["targetDir"], 0755)
	if err != nil {
		fmt.Printf("[错误] 创建目录失败 %s: %v\n", info["targetDir"], err)
		return
	}

	err = moveFile(fullPath, filepath.Join(info["targetDir"], fileName))
	if err != nil {
		fmt.Printf("[错误] 移动文件 %s 失败: %v\n", fileName, err)
		return
	}
	fmt.Printf("[移动] %s -> %s\n", fileName, info["targetDir"])

	// 2. 追加 CSS
	err = appendCSS(info)
	if err != nil {
		fmt.Printf("[错误] 更新 CSS %s 失败: %v\n", info["cssPath"], err)
	} else {
		fmt.Printf("[CSS] 成功更新样式至: %s\n", filepath.Base(info["cssPath"]))
	}
}

func generateNormalCSS(name, file string) string {
	if before, ok := strings.CutSuffix(name, "_not_start"); ok {
		cleanName := before
		return fmt.Sprintf(".level-award-center #XdrNotStartList #not-start-swiper .swiper-slide.%s {\n  background-image: url('../images/xdrNormal/%s/%s');\n}\n", cleanName, dateDir, file)
	}

	if before, ok := strings.CutSuffix(name, "_r"); ok {
		cleanName := before
		return fmt.Sprintf(".level-award-prize .item.%s.received {\n    background-image: url('../images/xdrNormal/%s/%s');\n}\n", cleanName, dateDir, file)
	}

	return fmt.Sprintf("/* %s */\n.level-award-prize .item.%s {\n    background-image: url('../images/xdrNormal/%s/%s');\n}\n", name, name, dateDir, file)
}

func appendCSS(info map[string]string) error {
	path := info["cssPath"]
	content := info["cssContent"]
	keyword := info["nameOnly"]
	category := info["category"]

	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	contentStr := string(data)

	if strings.Contains(contentStr, keyword) {
		fmt.Printf("[跳过] 样式已存在: %s\n", keyword)
		return nil
	}

	// 定义各类样式的特征选择器 map
	searchKeys := map[string]string{
		"popQy":       ".level-sign-popup .level-sign-popup-prize",
		"signPrize":   ".level-sign-prize-wrapper #level-sign-prize-swiper .swiper-slide",
		"notStart":    "#XdrNotStartList #not-start-swiper .swiper-slide",
		"normalPrize": ".level-award-prize .item",
	}

	searchKey := searchKeys[category]
	lastIdx := strings.LastIndex(contentStr, searchKey)
	insertIdx := len(contentStr)

	if lastIdx != -1 {
		// 寻找该选择器块结束的 '}'
		afterMatch := contentStr[lastIdx:]
		endBraceIdx := strings.Index(afterMatch, "}")
		if endBraceIdx != -1 {
			insertIdx = lastIdx + endBraceIdx + 1
		}
	}

	// 插入内容
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
