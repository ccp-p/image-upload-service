package main

import (
	"crypto/md5"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io/ioutil"
	"os"
	"path/filepath"
	"strings"
)

func main2() {
	// 需求背景 json 文件路径
	targetJson := `D:\project\cx_project\china_mobile\chartityProject\gitSourceCode\charity-open-fronted\wap\dev\src\assets\lottie\farm\pet\birthDress\data.json`

	// 1. 确定路径
	baseDir := filepath.Dir(targetJson)
	imgDir := filepath.Join(baseDir, "images")
	outputJson := filepath.Join(baseDir, "data_processed.json")

	fmt.Printf("正在处理文件: %s\n", targetJson)

	// 2. 读取文件
	content, err := ioutil.ReadFile(targetJson)
	if err != nil {
		fmt.Printf("无法读取文件: %v\n", err)
		return
	}

	// 3. 解析 JSON (使用 map[string]interface{} 保持原始结构)
	var lottieData map[string]interface{}
	if err := json.Unmarshal(content, &lottieData); err != nil {
		fmt.Printf("JSON 解析失败: %v\n", err)
		return
	}

	// 4. 创建 images 目录
	if _, err := os.Stat(imgDir); os.IsNotExist(err) {
		if err := os.MkdirAll(imgDir, 0755); err != nil {
			fmt.Printf("无法创建目录 %s: %v\n", imgDir, err)
			return
		}
	}

	// 5. 遍历 assets 提取图片
	if assets, ok := lottieData["assets"].([]interface{}); ok {
		count := 0
		for _, assetItem := range assets {
			asset, ok := assetItem.(map[string]interface{})
			if !ok {
				continue
			}

			// 获取 p 字段 (图片内容或路径)
			pVal, _ := asset["p"].(string)

			// 检查是否为 Base64 格式
			if strings.HasPrefix(pVal, "data:image/") {
				// 解析 Base64 字符串
				// 格式通常为: data:image/png;base64,iVBORw0KGgo...
				parts := strings.Split(pVal, ",")
				if len(parts) != 2 {
					continue
				}

				meta := parts[0]
				b64Data := parts[1]

				// 确定扩展名
				ext := "png"
				if strings.Contains(meta, "jpeg") || strings.Contains(meta, "jpg") {
					ext = "jpg"
				} else if strings.Contains(meta, "gif") {
					ext = "gif"
				}

				// 解码
				imgBytes, err := base64.StdEncoding.DecodeString(b64Data)
				if err != nil {
					fmt.Printf("Base64 解码错误: %v\n", err)
					continue
				}

				// 计算 Hash 作为文件名
				hash := md5.Sum(imgBytes)
				hashName := hex.EncodeToString(hash[:])
				fileName := fmt.Sprintf("%s.%s", hashName, ext)
				savePath := filepath.Join(imgDir, fileName)

				// 保存图片文件
				if err := ioutil.WriteFile(savePath, imgBytes, 0644); err != nil {
					fmt.Printf("保存图片失败: %v\n", err)
					continue
				}

				// 修改 JSON 字段
				// u: 路径前缀 "images/"
				// p: 文件名
				asset["u"] = "images/"
				asset["p"] = fileName
				
				// 确保 e 字段为 0 (外部文件)
				asset["e"] = 0

				count++
				fmt.Printf("提取图片: %s\n", fileName)
			}
		}
		fmt.Printf("共提取并保存了 %d 张图片。\n", count)
	} else {
		fmt.Println("JSON 中未找到 assets 数组。")
	}

	// 6. 保存处理后的 JSON
	// 使用 Marshal 而不是 MarshalIndent 以最小化文件体积
	newContent, err := json.Marshal(lottieData)
	if err != nil {
		fmt.Printf("JSON 序列化失败: %v\n", err)
		return
	}

	if err := ioutil.WriteFile(outputJson, newContent, 0644); err != nil {
		fmt.Printf("写入新 JSON 失败: %v\n", err)
		return
	}

	fmt.Printf("处理完成！新文件已保存为: %s\n", outputJson)
}
