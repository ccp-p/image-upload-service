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

type FileInfo struct {
	fullPath   string
	fileName   string
	nameOnly   string
	category   string
	targetDir  string
	cssPath    string
	jsPath     string
	cssContent string
}

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

	// 按 CSS 路径分组收集处理信息
	cssGroups := make(map[string][]*FileInfo)
	count := 0

	for _, f := range files {
		if f.IsDir() || !strings.HasPrefix(f.Name(), "xdr") {
			continue
		}
		info := prepareFileInfo(basePath, f.Name())
		if info != nil {
			cssGroups[info.cssPath] = append(cssGroups[info.cssPath], info)
			count++
		}
	}

	// 统一处理文件移动和 CSS 更新
	for cssPath, infos := range cssGroups {
		updateBatch(cssPath, infos)
	}

	fmt.Printf("[完成] 共处理 %d 个文件。\n", count)
}

func prepareFileInfo(basePath, fileName string) *FileInfo {
	fullPath := filepath.Join(compressedPath, fileName)
	width, height := getImageDimension(fullPath)
	ext := filepath.Ext(fileName)
	nameOnly := strings.TrimSuffix(fileName, ext)

	info := &FileInfo{
		fullPath: fullPath,
		fileName: fileName,
		nameOnly: nameOnly,
	}

	if width == 220 && height == 220 {
		info.category = "popQy"
		info.targetDir = filepath.Join(basePath, "components/xdrsign/static/popQy")
		info.cssPath = filepath.Join(basePath, "components/xdrsign/index.css")
		info.cssContent = fmt.Sprintf(".level-sign-popup .level-sign-popup-prize.%s {\n    background-image: url('../../components/xdrsign/static/popQy/%s');\n}\n", nameOnly, fileName)
	} else if width == 200 && height == 208 {
		info.category = "signPrize"
		info.targetDir = filepath.Join(basePath, "components/xdrsign/static")
		info.cssPath = filepath.Join(basePath, "components/xdrsign/index.css")
		info.cssContent = fmt.Sprintf(".level-sign-prize-wrapper #level-sign-prize-swiper .swiper-slide.%s {\n    background-image: url('../../components/xdrsign/static/%s');\n}\n", nameOnly, fileName)
	} else {
		info.targetDir = filepath.Join(basePath, "images/xdrNormal", dateDir)
		info.cssPath = filepath.Join(basePath, "css/xdrNormal.css")
		info.jsPath = filepath.Join(basePath, "scripts/js/xdrNormal.js")
		if strings.HasSuffix(nameOnly, "_not_start") {
			info.category = "notStart"
		} else if strings.Contains(nameOnly, "_xdr") {
			info.category = "xdrPrize"
		} else {
			info.category = "normalPrize"
		}
		info.cssContent = generateNormalCSS(nameOnly, fileName)
	}
	return info
}

func updateBatch(cssPath string, infos []*FileInfo) {
	data, err := os.ReadFile(cssPath)
	if err != nil {
		fmt.Printf("[错误] 读取 CSS 失败 %s: %v\n", cssPath, err)
		return
	}
	contentStr := string(data)

	searchKeys := map[string]string{
		"popQy":       ".level-sign-popup .level-sign-popup-prize",
		"signPrize":   ".level-sign-prize-wrapper #level-sign-prize-swiper .swiper-slide",
		"notStart":    "#XdrNotStartList #not-start-swiper .swiper-slide",
		"xdrPrize":    "#XdrPrizeList .level-award-prize .item",
		"normalPrize": ".level-award-prize .item",
	}

	for _, info := range infos {
		// 1. 移动文件
		os.MkdirAll(info.targetDir, 0755)
		moveFile(info.fullPath, filepath.Join(info.targetDir, info.fileName))
		fmt.Printf("[移动] %s -> %s\n", info.fileName, info.targetDir)

		// 2. 内存中构造新 CSS
		if strings.Contains(contentStr, info.nameOnly) {
			continue
		}

		searchKey := searchKeys[info.category]
		lastIdx := strings.LastIndex(contentStr, searchKey)
		insertIdx := len(contentStr)
		if lastIdx != -1 {
			if endBraceIdx := strings.Index(contentStr[lastIdx:], "}"); endBraceIdx != -1 {
				insertIdx = lastIdx + endBraceIdx + 1
			}
		}
		contentStr = contentStr[:insertIdx] + "\n" + info.cssContent + contentStr[insertIdx:]
	}

	os.WriteFile(cssPath, []byte(contentStr), 0644)
	fmt.Printf("[CSS] 已批量更新: %s\n", filepath.Base(cssPath))

	// 3. 处理 JS 更新
	updateJS(infos)
}

func updateJS(infos []*FileInfo) {
	// 找到第一个有 jsPath 的 info
	var jsPath string
	var filteredInfos []*FileInfo
	for _, info := range infos {
		if info.jsPath != "" {
			jsPath = info.jsPath
			filteredInfos = append(filteredInfos, info)
		}
	}

	if jsPath == "" || len(filteredInfos) == 0 {
		return
	}

	data, err := os.ReadFile(jsPath)
	if err != nil {
		fmt.Printf("[错误] 读取 JS 失败 %s: %v\n", jsPath, err)
		return
	}
	contentStr := string(data)

	const jsArrayKey = "const PRODUCTS_MAP = ["
	idx := strings.Index(contentStr, jsArrayKey)
	if idx == -1 {
		fmt.Printf("[错误] 未找到 PRODUCTS_MAP: %s\n", jsPath)
		return
	}

	insertIdx := idx + len(jsArrayKey)
	newEntries := ""
	
	// 用于在本次处理中去重
	processedCodes := make(map[string]bool)

	for _, info := range filteredInfos {
		// 根据后缀清理 code
		cleanCode := info.nameOnly
		cleanCode = strings.TrimSuffix(cleanCode, "_not_start")
		cleanCode = strings.TrimSuffix(cleanCode, "_xdr_r")
		cleanCode = strings.TrimSuffix(cleanCode, "_r")
		cleanCode = strings.TrimSuffix(cleanCode, "_xdr")

		// 1. 检查是否在本次循环中已处理过该 code
		// 2. 检查 JS 文件中是否已存在该 code
		if processedCodes[cleanCode] || strings.Contains(contentStr, fmt.Sprintf("code: '%s'", cleanCode)) {
			continue
		}

		processedCodes[cleanCode] = true
		newEntries += fmt.Sprintf("\n    {id: 9999, code: '%s', comment: '新权益', isOwn: false},", cleanCode)
	}

	if newEntries != "" {
		contentStr = contentStr[:insertIdx] + newEntries + contentStr[insertIdx:]
		os.WriteFile(jsPath, []byte(contentStr), 0644)
		fmt.Printf("[JS] 已更新: %s\n", filepath.Base(jsPath))
	}
}

func generateNormalCSS(name, file string) string {
	if before, ok := strings.CutSuffix(name, "_not_start"); ok {
		cleanName := before
		return fmt.Sprintf(".level-award-center #XdrNotStartList #not-start-swiper .swiper-slide.%s {\n  background-image: url('../images/xdrNormal/%s/%s');\n}\n", cleanName, dateDir, file)
	}

	// 处理 _xdr_r 后缀 (映射为 .received)
	if before, ok := strings.CutSuffix(name, "_xdr_r"); ok {
		cleanName := before
		return fmt.Sprintf(".level-award-center #XdrPrizeList .level-award-prize .item.%s.received {\n  background-image: url('../images/xdrNormal/%s/%s');\n}\n", cleanName, dateDir, file)
	}

	// 处理 _r 后缀 (映射为 .received)
	if before, ok := strings.CutSuffix(name, "_r"); ok {
		cleanName := before
		return fmt.Sprintf(".level-award-prize .item.%s.received {\n    background-image: url('../images/xdrNormal/%s/%s');\n}\n", cleanName, dateDir, file)
	}

	// 处理 _xdr 后缀
	if before, ok := strings.CutSuffix(name, "_xdr"); ok {
		cleanName := before
		return fmt.Sprintf(".level-award-center #XdrPrizeList .level-award-prize .item.%s {\n  background-image: url('../images/xdrNormal/%s/%s');\n}\n", cleanName, dateDir, file)
	}

	return fmt.Sprintf("/* %s */\n.level-award-prize .item.%s {\n    background-image: url('../images/xdrNormal/%s/%s');\n}\n", name, name, dateDir, file)
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
