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
	isHome := os.Getenv("IS_HOME") == "true"
	basePath := companySourcePath
	if isHome {
		basePath = homeSourcePath
	}

	files, _ := os.ReadDir(compressedPath)
	for _, f := range files {
		if f.IsDir() { continue }
		processFile(basePath, f.Name())
	}
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
	os.MkdirAll(targetDir, 0755)
	moveFile(fullPath, filepath.Join(targetDir, fileName))

	// 2. 追加 CSS
	appendCSS(cssPath, cssContent, nameOnly)
}

func generateNormalCSS(name, file string) string {
	if before, ok :=strings.CutSuffix(name, "_not_start"); ok  {
		cleanName := before
		return fmt.Sprintf(".level-award-center #XdrNotStartList #not-start-swiper .swiper-slide.%s {\n  background-image: url('../images/xdrNormal/%s/%s');\n}\n", cleanName, dateDir, file)
	}
	// ... 类似处理 _xdr, _xdr_r 等逻辑
	return fmt.Sprintf("/* %s */\n.level-award-prize .item.%s {\n    background-image: url('../images/xdrNormal/%s/%s');\n}\n", name, name, dateDir, file)
}

func appendCSS(path, content, keyword string) {
	f, _ := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0644)
	defer f.Close()
	f.WriteString("\n" + content)
}

func getImageDimension(path string) (int, int) {
	file, _ := os.Open(path)
	defer file.Close()
	img, _, _ := image.DecodeConfig(file)
	return img.Width, img.Height
}

func moveFile(src, dst string) {
	in, _ := os.Open(src)
	defer in.Close()
	out, _ := os.Create(dst)
	defer out.Close()
	io.Copy(out, in)
}
