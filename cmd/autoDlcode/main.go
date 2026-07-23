package main

import (
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"
)

const (
	imagePrefix = ""
	htmlPrefix  = "code"
	maxWorkers  = 10
	defaultExt  = ".png"
	downloadDir = `C:\Users\83795\Downloads`
)

// ✅ 默认静态资源路径列表，后期直接在此处追加即可
var defaultBasePaths = []string{
	// "../images/xdrNormal/202505/new/",
	// ../../components/xdrsignNew/static/
	// "~@/assets/img/farm/",
	"~@/assets/img/nft/",
}

type task struct {
	cdnURL    string
	className string
	fileName  string
}

func findLatestHTML(dir string) (string, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return "", fmt.Errorf("读取目录失败: %w", err)
	}

	type fileInfo struct {
		name    string
		modTime int64
	}

	var candidates []fileInfo
	for _, e := range entries {
		if e.IsDir() || !strings.HasPrefix(e.Name(), htmlPrefix) {
			continue
		}
		info, err := e.Info()
		if err != nil {
			continue
		}
		candidates = append(candidates, fileInfo{name: e.Name(), modTime: info.ModTime().UnixNano()})
	}

	if len(candidates) == 0 {
		return "", fmt.Errorf("未找到以 %q 开头的HTML文件", htmlPrefix)
	}

	sort.Slice(candidates, func(i, j int) bool {
		return candidates[i].modTime > candidates[j].modTime
	})
	return filepath.Join(dir, candidates[0].name), nil
}

// resolveBasePaths 确定最终使用的路径列表
// 优先级: 命令行参数 > 默认配置
func resolveBasePaths(args []string) []string {
	if len(args) > 1 {
		// 命令行传入的路径全部作为目标（支持多路径）
		return args[1:]
	}
	return defaultBasePaths
}

func main() {
	basePaths := resolveBasePaths(os.Args)

	htmlFile, err := findLatestHTML(downloadDir)
	if err != nil {
		panic(fmt.Sprintf("❌ %v", err))
	}
	fmt.Printf("📄 检测到最新HTML: %s\n", htmlFile)

	content, err := os.ReadFile(htmlFile)
	if err != nil {
		panic(fmt.Sprintf("❌ 无法读取文件 %s: %v", htmlFile, err))
	}
	htmlStr := string(content)

	// 解析 CSS (支持 background 和 background-image 两种写法，兼容多行 CSS)
	reRule := regexp.MustCompile(`\.([a-zA-Z0-9_-]+)\s*\{[^}]*background(?:-image)?:\s*url$$\s*['"]?(.*?)['"]?\s*$$[^}]*\}`)
	matches := reRule.FindAllStringSubmatch(htmlStr, -1)

	tasks := []task{}
	urlToTask := make(map[string]task)
	// 收集本地路径替换: oldURL -> newURL
	localReplacements := make(map[string]string)
	primaryRef := strings.ReplaceAll(basePaths[0], `\`, `/`)
	expectedDir := normalizePathSep(ensureTrailingSlash(basePaths[0]))

	for _, m := range matches {
		className := m[1]
		rawURL := strings.TrimSpace(m[2])
		if rawURL == "" {
			continue
		}
		if strings.HasPrefix(rawURL, "http") {
			// CDN 图片 → 下载任务
			if _, exists := urlToTask[rawURL]; exists {
				continue
			}
			ext := filepath.Ext(rawURL)
			if ext == "" || len(ext) > 5 {
				ext = defaultExt
			}
			t := task{cdnURL: rawURL, className: className, fileName: imagePrefix + className + ext}
			tasks = append(tasks, t)
			urlToTask[rawURL] = t
		} else {
			// 本地路径 → 收集并检查是否需要替换为 basePaths
			if _, exists := localReplacements[rawURL]; exists {
				continue
			}
			actualDir := normalizePathSep(extractDir(rawURL))
			if actualDir != expectedDir {
				localReplacements[rawURL] = primaryRef + className + filepath.Ext(rawURL)
			}
		}
	}

	if len(tasks) == 0 {
		if len(localReplacements) == 0 {
			fmt.Println("✅ 未发现需要下载的 CDN 图片，本地路径已是最新")
			// 检查并移动 compressed 目录中的文件
			checkAndMoveCompressedFiles(basePaths[0], 0)
			return
		}
		// 无 CDN 图片需下载，但本地路径与 resolveBasePaths 不一致 → 替换
		newHtml := htmlStr
		for oldURL, newURL := range localReplacements {
			newHtml = strings.ReplaceAll(newHtml, oldURL, newURL)
			fmt.Printf("🔄 替换路径: %s -> %s\n", oldURL, newURL)
		}
		if err := os.WriteFile(htmlFile, []byte(newHtml), 0644); err != nil {
			panic(fmt.Sprintf("❌ 写入文件失败: %v", err))
		}
		fmt.Printf("\n🎉 完成！共替换 %d 处本地路径\n", len(localReplacements))
		// 检查并移动 compressed 目录中的文件
		checkAndMoveCompressedFiles(basePaths[0], 0)
		return
	}

	// ✅ 图片统一下载到固定目录
	fmt.Printf("\n📂 图片下载目录: %s\n", downloadDir)
	if err := os.MkdirAll(downloadDir, os.ModePerm); err != nil {
		panic(fmt.Sprintf("❌ 无法创建目录 %s: %v", downloadDir, err))
	}

	var wg sync.WaitGroup
	ch := make(chan task, len(tasks))
	for i := 0; i < maxWorkers; i++ {
		go func() {
			for t := range ch {
				destPath := filepath.Join(downloadDir, t.fileName)
				downloadImage(t.cdnURL, destPath, t.className)
				wg.Done()
			}
		}()
	}
	for _, t := range tasks {
		wg.Add(1)
		ch <- t
	}
	close(ch)
	wg.Wait()

	// ✅ HTML 中替换为第一个 basePath 的相对路径（主引用路径）
	newHtml := htmlStr
	for cdnURL, t := range urlToTask {
		ref := primaryRef + t.fileName
		newHtml = strings.ReplaceAll(newHtml, cdnURL, ref)
	}

	if err := os.WriteFile(htmlFile, []byte(newHtml), 0644); err != nil {
		panic(fmt.Sprintf("❌ 写入文件失败: %v", err))
	}

	fmt.Printf("\n🎉 完成！共处理 %d 张图片，输出到 %d 个目录\n", len(tasks), len(basePaths))
	fmt.Printf("🔗 HTML主引用路径: %s<className>.png\n", primaryRef)

	// 检查并移动 compressed 目录中的文件
	checkAndMoveCompressedFiles(basePaths[0], len(tasks))
}

// filterNonDirs 过滤出非目录文件
func filterNonDirs(entries []os.DirEntry) []os.DirEntry {
	var result []os.DirEntry
	for _, e := range entries {
		if !e.IsDir() {
			result = append(result, e)
		}
	}
	return result
}

// waitForAllReady 轮询 compressed 目录，等待文件数量达到预期后返回
// expectedCount > 0 时会轮训等待所有文件就绪
func waitForAllReady(compressedDir string, expectedCount int, initialFiles []os.DirEntry) []os.DirEntry {
	if expectedCount <= 0 || len(initialFiles) >= expectedCount {
		return initialFiles
	}

	fmt.Printf("⏳ compressed 目录中 %d/%d 个文件，等待剩余文件...\n", len(initialFiles), expectedCount)
	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()
	timeout := time.After(5 * time.Minute)
	files := initialFiles

	for {
		select {
		case <-timeout:
			fmt.Printf("⚠️  等待超时(5分钟)，compressed 目录中仅 %d/%d 个文件\n", len(files), expectedCount)
			return files
		case <-ticker.C:
			entries, err := os.ReadDir(compressedDir)
			if err != nil {
				continue
			}
			files = filterNonDirs(entries)
			fmt.Printf("⏳ compressed 目录中 %d/%d 个文件\n", len(files), expectedCount)
			if len(files) >= expectedCount {
				fmt.Printf("✅ 所有 %d 个文件已就绪\n", expectedCount)
				return files
			}
		}
	}
}

// checkAndMoveCompressedFiles 检查并移动压缩文件到源目录
// targetDir 为 HTML 中替换后的目标路径（如 ../../components/xdrsignNew/static/）
// expectedCount 为预期的压缩文件数量，>0 时会轮训等待所有文件就绪
func checkAndMoveCompressedFiles(targetDir string, expectedCount int) {
	compressedDir := `C:\Users\83795\Downloads\compressed`

	// 检查compressed目录是否存在
	if _, err := os.Stat(compressedDir); os.IsNotExist(err) {
		return
	}

	// 读取compressed目录中的文件
	entries, err := os.ReadDir(compressedDir)
	if err != nil {
		fmt.Printf("⚠️  读取compressed目录失败: %v\n", err)
		return
	}

	// 过滤出非目录文件
	fileList := filterNonDirs(entries)

	// 如果需要等待压缩文件，轮训直到所有文件就绪
	fileList = waitForAllReady(compressedDir, expectedCount, fileList)
	if len(fileList) == 0 {
		return
	}

	// 根据环境变量确定源目录
	isHome := os.Getenv("IS_HOME") == "1"
	var sourcePath string
	if isHome {
		sourcePath = `D:\job_project\china_mobile\gitProject\richinfo_tyjf_xhmqqthy\src\main\webapp\res\wap`
	} else {
		sourcePath = `D:\project\cx_project\china_mobile\gitProject\richinfo_tyjf_xhmqqthy\src\main\webapp\res\wap`
	}

	// 列出所有文件
	fmt.Println("\n📦 发现以下文件在 compressed 目录中:")
	for i, file := range fileList {
		fmt.Printf("  %d. %s\n", i+1, file.Name())
	}

	// 询问用户是否移动
	fmt.Print("\n是否将这些文件移动到源目录? (Enter/y确认, n跳过): ")
	var response string
	fmt.Scanln(&response)

	response = strings.ToLower(strings.TrimSpace(response))
	if response != "y" && response != "" {
		fmt.Println("⏭️  跳过移动文件")
		return
	}

	// 解析 targetDir 为绝对路径（相对于 sourcePath）
	dstDir := filepath.Clean(filepath.Join(sourcePath, filepath.FromSlash(targetDir)))

	// 移动文件到目标目录
	movedCount := 0
	for _, file := range fileList {
		srcPath := filepath.Join(compressedDir, file.Name())
		dstPath := filepath.Join(dstDir, file.Name())

		// 确保目标目录存在
		if err := os.MkdirAll(filepath.Dir(dstPath), os.ModePerm); err != nil {
			fmt.Printf("⚠️  创建目录失败: %s - %v\n", filepath.Dir(dstPath), err)
			continue
		}

		if err := moveFile(srcPath, dstPath); err != nil {
			fmt.Printf("⚠️  移动失败: %s - %v\n", file.Name(), err)
			continue
		}

		fmt.Printf("✅ 已移动: %s -> %s\n", file.Name(), dstPath)
		movedCount++
	}

	fmt.Printf("\n🎉 共移动 %d 个文件\n", movedCount)
}

func downloadImage(url, dest, className string) {
	if _, err := os.Stat(dest); err == nil {
		fmt.Printf("⏭️  跳过 (已存在): %s\n", className)
		return
	}
	resp, err := http.Get(url)
	if err != nil {
		fmt.Printf("❌ 请求失败 [%s]: %v\n", className, err)
		return
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		fmt.Printf("❌ HTTP错误 [%s]: %d\n", className, resp.StatusCode)
		return
	}
	file, err := os.Create(dest)
	if err != nil {
		fmt.Printf("❌ 创建文件失败 [%s]: %v\n", className, err)
		return
	}
	defer file.Close()
	if _, err := io.Copy(file, resp.Body); err != nil {
		fmt.Printf("❌ 写入失败 [%s]: %v\n", className, err)
		return
	}
	fmt.Printf("✅ %s -> %s\n", className, filepath.Base(dest))
}

// moveFile 移动文件，支持跨盘符操作（Windows 下 os.Rename 不支持跨盘）
func moveFile(src, dst string) error {
	if err := os.Rename(src, dst); err == nil {
		return nil
	}
	// 跨盘符：复制 + 删除
	srcFile, err := os.Open(src)
	if err != nil {
		return err
	}
	dstFile, err := os.Create(dst)
	if err != nil {
		srcFile.Close()
		return err
	}
	if _, err := io.Copy(dstFile, srcFile); err != nil {
		srcFile.Close()
		dstFile.Close()
		return err
	}
	srcFile.Close()
	dstFile.Close()
	return os.Remove(src)
}

// normalizePathSep 将路径中的反斜杠统一为正斜杠
func normalizePathSep(path string) string {
	return strings.ReplaceAll(path, `\`, `/`)
}

// ensureTrailingSlash 确保路径以 / 结尾
func ensureTrailingSlash(path string) string {
	path = normalizePathSep(path)
	if !strings.HasSuffix(path, "/") {
		return path + "/"
	}
	return path
}

// extractDir 提取路径中的目录部分（保留末尾分隔符）
func extractDir(path string) string {
	if idx := strings.LastIndexAny(path, "/\\"); idx >= 0 {
		return path[:idx+1]
	}
	return ""
}