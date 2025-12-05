package main

import (
	"encoding/base64"
	"fmt"
	"io/ioutil"
	"os"
	"path/filepath"
	"regexp"
)

func main() {
	inputFile := `D:\project\cx_project\china_mobile\chartityProject\gitSourceCode\charity-open-fronted\wap\dev\dist\static\js_20251205\chunk-05a8245a.8d409448.chunk.js`
	outputFile := `a`
	imgDir := "D:\\project\\my_go_project\\image-upload-service\\cmd\\checkFile\\images"

	// 1. 读取文件
	content, err := ioutil.ReadFile(inputFile)
	if err != nil {
		fmt.Printf("无法读取文件 %s: %v\n", inputFile, err)
		return
	}
	originalSize := len(content)

	// 2. 创建图片目录
	if _, err := os.Stat(imgDir); os.IsNotExist(err) {
		os.Mkdir(imgDir, 0755)
	}

	// 3. 正则匹配 Base64 图片字符串
	// 匹配格式: data:image/png;base64,iVBORw0KGgo...
	// 这是一个简单的正则，可能需要根据实际 JS 内容调整
	re := regexp.MustCompile(`data:image\/([a-zA-Z]+);base64,([a-zA-Z0-9+/=]+)`)

	newContent := re.ReplaceAllStringFunc(string(content), func(match string) string {
		// 提取子匹配
		submatches := re.FindStringSubmatch(match)
		if len(submatches) < 3 {
			return match // 匹配失败，不替换
		}

		ext := submatches[1]      // 图片扩展名 (png, jpeg, etc.)
		b64Data := submatches[2]  // Base64 数据部分

		// 解码 Base64
		decoded, err := base64.StdEncoding.DecodeString(b64Data)
		if err != nil {
			fmt.Printf("Base64 解码失败: %v\n", err)
			return match
		}

		// 生成文件名 (使用简单的计数或哈希，这里用简单的临时文件名逻辑，实际可用 md5)
		// 为了简单起见，这里不维护全局计数器，直接用内容长度+部分数据做文件名，或者你可以引入全局变量
		// 这里简单使用纳秒时间戳或者随机数，但在 ReplaceAllStringFunc 中不好控制顺序计数
		// 更好的方式是先 FindAll 拿到所有，再 Replace。
		// 但为了保持流式处理，我们这里简单生成一个基于内容哈希的文件名
		filename := fmt.Sprintf("img_%d.%s", len(decoded), ext)
		savePath := filepath.Join(imgDir, filename)

		// 保存图片 (如果文件已存在可能会覆盖，实际场景建议用 md5 命名)
		err = ioutil.WriteFile(savePath, decoded, 0644)
		if err != nil {
			fmt.Printf("保存图片失败 %s: %v\n", savePath, err)
			return match
		}

		fmt.Printf("已保存图片: %s\n", savePath)

		// 返回替换后的字符串，按要求替换为空字符
		return "" 
	})

	// 4. 保存瘦身后的 JS
	err = ioutil.WriteFile(outputFile, []byte(newContent), 0644)
	if err != nil {
		fmt.Printf("无法写入文件 %s: %v\n", outputFile, err)
		return
	}
	newSize := len(newContent)

	// 5. 输出结果
	fmt.Printf("处理完成。\n")
	fmt.Printf("原始文件 (%s) 大小: %d bytes\n", inputFile, originalSize)
	fmt.Printf("瘦身文件 (%s) 大小: %d bytes\n", outputFile, newSize)
	fmt.Printf("减少大小: %d bytes\n", originalSize-newSize)
}
