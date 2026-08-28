package main

import (
	"bytes"
	"encoding/json"
	"encoding/xml"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

// ============================================================
//  常量
// ============================================================

const (
	svnLogCount = 3

	// CDN 域名前缀，HTML 中以此开头的引用去掉前缀后在 dest 目录中校验
	cdnDomain = `https://qqt-res.cmicrwx.cn/2016tyjf/xhmqqthy/res/wap`

	pushPlusURL = "https://www.pushplus.plus/send"
)

// ============================================================
//  结构体
// ============================================================

type EnvConfig struct {
	TargetDir string
	SvnRoot   string
	DestPath  string
	Label     string
}

type SvnLog struct {
	XMLName   xml.Name   `xml:"log"`
	LogEntrys []LogEntry `xml:"logentry"`
}

type LogEntry struct {
	Revision string `xml:"revision,attr"`
	Author   string `xml:"author"`
	Date     string `xml:"date"`
	Msg      string `xml:"msg"`
}

type PushPlusRequest struct {
	Token    string `json:"token"`
	Title    string `json:"title"`
	Content  string `json:"content"`
	Template string `json:"template"`
}

type CheckResult struct {
	Found       bool
	TotalRefs    int
	MissingFiles []string
	ElapsedTime time.Duration
}

// ============================================================
//  环境路径
// ============================================================

func getEnvConfig() EnvConfig {
	isHome := os.Getenv("IS_HOME")
	logf("环境变量 IS_HOME=%s\n", isHome)

	if isHome == "1" {
		cfg := EnvConfig{
			TargetDir: `D:\self_project\go_project\image-upload-service\cmd\hashCdn`,
			SvnRoot:   `D:\job_project\china_mobile\huidu\xhmqqthy-res`,
			DestPath:  `D:\job_project\china_mobile\huidu\xhmqqthy-res`,
			Label:     "家里",
		}
		logf("🏠 使用家里配置\n")
		logf("  targetDir : %s\n", cfg.TargetDir)
		logf("  svnRoot   : %s\n", cfg.SvnRoot)
		logf("  destPath  : %s\n", cfg.DestPath)
		return cfg
	}

	cfg := EnvConfig{
		TargetDir: `D:\project\my_go_project\image-upload-service\cmd\hashCdn`,
		SvnRoot:   `D:\project\cx_project\china_mobile\huidu\xhmqqthy-res`,
		DestPath:  `D:\project\cx_project\china_mobile\huidu\xhmqqthy-res`,
		Label:     "公司",
	}
	logf("🏢 使用公司配置\n")
	logf("  targetDir : %s\n", cfg.TargetDir)
	logf("  svnRoot   : %s\n", cfg.SvnRoot)
	logf("  destPath  : %s\n", cfg.DestPath)
	return cfg
}

// ============================================================
//  工具函数
// ============================================================

func nowStr() string {
	return time.Now().Format("2006-01-02 15:04:05")
}

func logf(format string, args ...interface{}) {
	fmt.Printf("[%s] %s", nowStr(), fmt.Sprintf(format, args...))
}

// newCmd 仅创建命令并设置工作目录
// ✅ 修复：不再在此处绑定 Stdout/Stderr，避免与 CombinedOutput/Output 冲突
func newCmd(dir, name string, args ...string) *exec.Cmd {
	cmd := exec.Command(name, args...)
	cmd.Dir = dir
	return cmd
}

// ============================================================
//  功能一：部署执行
// ============================================================

func executeDeploy(cfg EnvConfig) (string, error) {
	absDir, err := filepath.Abs(cfg.TargetDir)
	if err != nil {
		return "", fmt.Errorf("解析目录路径失败: %w", err)
	}
	if _, err := os.Stat(absDir); os.IsNotExist(err) {
		return "", fmt.Errorf("目标目录不存在: %s", absDir)
	}

	logf("开始执行部署命令...\n")
	logf("  工作目录: %s\n", absDir)

	// Rust 版二进制：<repo>\target\release\hash-cdn.exe，速度比 Go 版快
	repoRoot := filepath.Dir(filepath.Dir(absDir))
	exePath := filepath.Join(repoRoot, "target", "release", "hash-cdn.exe")

	needBuild := true
	if info, e := os.Stat(exePath); e == nil {
		needBuild = false
		// .rs 源码或 Cargo.toml 比 exe 新时重新编译
		entries, _ := os.ReadDir(repoRoot)
		for _, f := range entries {
			if !f.IsDir() && (strings.HasSuffix(f.Name(), ".rs") || f.Name() == "Cargo.toml") {
				if fi, e := f.Info(); e == nil {
					if fi.ModTime().After(info.ModTime()) {
						needBuild = true
						break
					}
				}
			}
		}
	}
	if needBuild {
		logf("正在编译 hash-cdn.exe (Rust) ...\n")
		buildCmd := newCmd(repoRoot, "cargo", "build", "--release")
		var buildOut bytes.Buffer
		buildCmd.Stdout = &buildOut
		buildCmd.Stderr = &buildOut
		if e := buildCmd.Run(); e != nil {
			return "", fmt.Errorf("编译 hash-cdn (Rust) 失败: %w\n输出: %s", e, buildOut.String())
		}
		logf("编译完成\n")
	} else {
		logf("hash-cdn.exe (Rust) 已是最新，跳过编译\n")
	}

	cmd := newCmd(absDir, exePath,
		"-config=version.config.json", "-mode=9", "-message", "切hash")

	// ✅ 使用 MultiWriter 同时输出到控制台和 buffer
	var buf bytes.Buffer
	tee := io.MultiWriter(os.Stdout, &buf)
	cmd.Stdout = tee
	cmd.Stderr = tee

	err = cmd.Run()
	outputStr := buf.String()

	if err != nil {
		return outputStr, fmt.Errorf("部署命令执行失败: %w\n输出: %s", err, outputStr)
	}

	logf("部署命令执行完成\n")
	return outputStr, nil
}

// ============================================================
//  功能二：SVN 日志
// ============================================================

func getRecentSvnLogs(cfg EnvConfig) ([]LogEntry, error) {
	absRoot, err := filepath.Abs(cfg.SvnRoot)
	if err != nil {
		return nil, fmt.Errorf("解析 SVN 路径失败: %w", err)
	}

	logf("获取最近 %d 条 SVN 记录（目录: %s）...\n", svnLogCount, absRoot)

	cmd := newCmd(absRoot, "svn", "log",
		"-l", fmt.Sprintf("%d", svnLogCount), "--xml")
	output, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("svn log 执行失败: %w", err)
	}

	var svnLog SvnLog
	if err := xml.Unmarshal(output, &svnLog); err != nil {
		return nil, fmt.Errorf("解析 SVN XML 失败: %w", err)
	}
	if len(svnLog.LogEntrys) == 0 {
		return nil, fmt.Errorf("未获取到 SVN 记录")
	}
	return svnLog.LogEntrys, nil
}

// ============================================================
//  功能三：CDN 更新检测
// ============================================================

// reHTMLComment 匹配 HTML 注释，提取引用前先移除注释避免误检
var reHTMLComment = regexp.MustCompile(`(?s)<!--.*?-->`)

// reHTMLRef 提取 HTML 中所有 src 和 href 属性值
var reHTMLRef = regexp.MustCompile(`(?:src|href)\s*=\s*["']([^"']+)["']`)

// extractHTMLReferences 从 HTML 内容中提取所有 src/href 引用路径（已移除注释）
func extractHTMLReferences(htmlContent string) []string {
	clean := reHTMLComment.ReplaceAllString(htmlContent, "")
	matches := reHTMLRef.FindAllStringSubmatch(clean, -1)
	var refs []string
	for _, m := range matches {
		ref := strings.TrimSpace(m[1])
		if ref == "" || ref == "#" {
			continue
		}
		// 跳过 javascript:、mailto:、tel: 等非资源引用
		if strings.HasPrefix(ref, "javascript:") ||
			strings.HasPrefix(ref, "mailto:") ||
			strings.HasPrefix(ref, "tel:") ||
			strings.HasPrefix(ref, "data:") {
			continue
		}
		// 跳过 JS 模板占位符（如 ${var}.png）
		if strings.Contains(ref, "${") {
			continue
		}
		refs = append(refs, ref)
	}
	return refs
}

// resolveRefToDest 将一个引用解析为 dest 目录中的实际文件路径。
// 返回 (absPath, shouldCheck)：shouldCheck=false 表示是外部资源，无需校验。
func resolveRefToDest(ref, htmlDir, destPath, cdnPrefix string) (string, bool) {
	// 去掉查询参数和锚点
	ref = strings.SplitN(ref, "?", 2)[0]
	ref = strings.SplitN(ref, "#", 2)[0]
	if ref == "" {
		return "", false
	}

	var localPath string

	switch {
	case strings.HasPrefix(ref, cdnPrefix):
		// CDN 引用：去掉域名前缀得到相对于 dest 根目录的路径
		relPath := strings.TrimPrefix(ref, cdnPrefix)
		relPath = strings.TrimPrefix(relPath, "/")
		localPath = filepath.Join(destPath, filepath.FromSlash(relPath))

	case strings.HasPrefix(ref, "http://"), strings.HasPrefix(ref, "https://"):
		// 其他域名的绝对 URL，属于外部资源
		return "", false

	case strings.HasPrefix(ref, "//"):
		// 协议相对 URL，属于外部资源
		return "", false

	case strings.HasPrefix(ref, "/"):
		// 根路径引用：相对于 dest 根目录
		localPath = filepath.Join(destPath, filepath.FromSlash(ref))

	default:
		// 相对路径：相对于 HTML 文件所在目录解析
		relPath := strings.TrimPrefix(ref, "./")
		localPath = filepath.Join(htmlDir, filepath.FromSlash(relPath))
	}

	absPath, err := filepath.Abs(localPath)
	if err != nil {
		return localPath, true
	}
	return absPath, true
}

// checkTargetHTML 是固定校验的目标 HTML 文件名
const checkTargetHTML = "xdrNormal.html"

// runCheck 校验 dest 根目录下 xdrNormal.html 中所有引用的资源是否本地存在
func runCheck(cfg EnvConfig) CheckResult {
	startTime := time.Now()

	htmlFile := filepath.Join(cfg.DestPath, checkTargetHTML)
	content, err := os.ReadFile(htmlFile)
	if err != nil {
		logf("读取 %s 失败: %v\n", checkTargetHTML, err)
		return CheckResult{
			Found:        false,
			MissingFiles: []string{htmlFile},
			ElapsedTime:  time.Since(startTime),
		}
	}

	logf("开始本地引用校验：%s（dest: %s）\n", checkTargetHTML, cfg.DestPath)

	var missingFiles []string
	totalRefs := 0

	htmlDir := filepath.Dir(htmlFile)
	refs := extractHTMLReferences(string(content))

	for _, ref := range refs {
		absPath, shouldCheck := resolveRefToDest(ref, htmlDir, cfg.DestPath, cdnDomain)
		if !shouldCheck {
			continue
		}
		totalRefs++
		if _, err := os.Stat(absPath); os.IsNotExist(err) {
			missingFiles = append(missingFiles, absPath)
		}
	}

	elapsed := time.Since(startTime)
	found := len(missingFiles) == 0

	if found {
		logf("✅ 引用校验通过：%s 中 %d 个引用全部存在\n", checkTargetHTML, totalRefs)
	} else {
		logf("❌ 引用校验失败：%s 中 %d 个引用，%d 个缺失\n",
			checkTargetHTML, totalRefs, len(missingFiles))
		for i, f := range missingFiles {
			if i >= 20 {
				logf("  ... 还有 %d 个缺失文件未显示\n", len(missingFiles)-20)
				break
			}
			logf("  缺失: %s\n", f)
		}
	}

	return CheckResult{
		Found:        found,
		TotalRefs:    totalRefs,
		MissingFiles: missingFiles,
		ElapsedTime:  elapsed,
	}
}

// ============================================================
//  通知
// ============================================================

func sendNotification(title, content string) error {
	token := os.Getenv("PUSH_PLUS")
	if token == "" {
		return fmt.Errorf("环境变量 PUSH_PLUS 未设置，跳过通知")
	}

	logf("发送 PushPlus 通知: %s\n", title)

	payload := PushPlusRequest{
		Token:    token,
		Title:    title,
		Content:  content,
		Template: "markdown",
	}

	jsonData, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("序列化请求失败: %w", err)
	}

	for i := 1; i <= 3; i++ {
		client := &http.Client{Timeout: 15 * time.Second}
		resp, err := client.Post(pushPlusURL, "application/json", bytes.NewBuffer(jsonData))
		if err != nil {
			logf("第 %d 次通知请求失败: %v\n", i, err)
			time.Sleep(time.Duration(i) * 2 * time.Second)
			continue
		}
		body, _ := io.ReadAll(resp.Body)
		resp.Body.Close()

		if resp.StatusCode == http.StatusOK {
			logf("通知发送成功\n")
			return nil
		}
		logf("第 %d 次通知返回 %d: %s\n", i, resp.StatusCode, string(body))
		time.Sleep(time.Duration(i) * 2 * time.Second)
	}
	return fmt.Errorf("通知发送失败（已重试3次）")
}

// ============================================================
//  内容构建
// ============================================================

func formatSVNTable(logs []LogEntry) string {
	var sb strings.Builder
	sb.WriteString("| 版本号 | 作者 | 日期 | 提交说明 |\n")
	sb.WriteString("|--------|------|------|----------|\n")
	for _, entry := range logs {
		date := entry.Date
		if t, err := time.Parse(time.RFC3339, entry.Date); err == nil {
			date = t.Format("2006-01-02 15:04")
		}
		msg := strings.ReplaceAll(entry.Msg, "\n", " ")
		if len(msg) > 60 {
			msg = msg[:57] + "..."
		}
		sb.WriteString(fmt.Sprintf("| r%s | %s | %s | %s |\n",
			entry.Revision, entry.Author, date, msg))
	}
	return sb.String()
}

func buildDeployNotifyContent(deployOutput string, logs []LogEntry) (string, string) {
	now := nowStr()
	title := fmt.Sprintf("CDN 切换通知 - %s", now)

	var sb strings.Builder
	sb.WriteString("## CDN 切换执行结果\n\n")
	sb.WriteString(fmt.Sprintf("**执行时间**: %s\n\n", now))
	sb.WriteString("---\n\n")

	sb.WriteString("### 部署输出\n\n")
	trimmed := strings.TrimSpace(deployOutput)
	if trimmed == "" {
		sb.WriteString("执行完成，无额外输出。\n\n")
	} else {
		sb.WriteString("```\n")
		sb.WriteString(trimmed)
		sb.WriteString("\n```\n\n")
	}

	if len(logs) > 0 {
		sb.WriteString("### 最近 SVN 提交记录\n\n")
		sb.WriteString(formatSVNTable(logs))
		sb.WriteString("\n")
	}
	return title, sb.String()
}

func buildCheckNotifyContent(result CheckResult, locationLabel string) (string, string) {
	now := nowStr()

	if result.Found {
		title := fmt.Sprintf("✅ 引用校验通过 - %s", now)
		var sb strings.Builder
		sb.WriteString("## 本地引用校验结果\n\n")
		sb.WriteString(fmt.Sprintf("- **状态**: ✅ 全部引用存在\n"))
		sb.WriteString(fmt.Sprintf("- **时间**: %s\n", now))
		sb.WriteString(fmt.Sprintf("- **引用总数**: %d\n", result.TotalRefs))
		sb.WriteString(fmt.Sprintf("- **检测位置**: %s\n", locationLabel))
		sb.WriteString(fmt.Sprintf("- **耗时**: %s\n", result.ElapsedTime.Round(time.Second)))
		return title, sb.String()
	}

	title := fmt.Sprintf("❌ 引用校验失败 - %s", now)
	var sb strings.Builder
	sb.WriteString("## 本地引用校验结果\n\n")
	sb.WriteString(fmt.Sprintf("- **状态**: ❌ 存在缺失引用\n"))
	sb.WriteString(fmt.Sprintf("- **时间**: %s\n", now))
	sb.WriteString(fmt.Sprintf("- **引用总数**: %d\n", result.TotalRefs))
	sb.WriteString(fmt.Sprintf("- **缺失数量**: %d\n", len(result.MissingFiles)))
	sb.WriteString(fmt.Sprintf("- **检测位置**: %s\n", locationLabel))
	sb.WriteString(fmt.Sprintf("- **耗时**: %s\n", result.ElapsedTime.Round(time.Second)))
	sb.WriteString("\n### 缺失文件列表\n\n")
	for i, f := range result.MissingFiles {
		if i >= 50 {
			sb.WriteString(fmt.Sprintf("\n... 还有 %d 个缺失文件未列出\n", len(result.MissingFiles)-50))
			break
		}
		sb.WriteString(fmt.Sprintf("- `%s`\n", f))
	}
	return title, sb.String()
}

func buildFullNotifyContent(deployOutput string, logs []LogEntry, checkResult CheckResult, locationLabel string) (string, string) {
	now := nowStr()

	statusIcon := "✅"
	if !checkResult.Found {
	statusIcon = "❌"
	}
	title := fmt.Sprintf("%s CDN 切换 + 引用校验 - %s", statusIcon, now)

	var sb strings.Builder
	sb.WriteString("## CDN 切换完整报告\n\n")
	sb.WriteString(fmt.Sprintf("**执行时间**: %s | **检测位置**: %s\n\n", now, locationLabel))

	sb.WriteString("---\n\n")
	sb.WriteString("### 1. 部署执行\n\n")
	trimmed := strings.TrimSpace(deployOutput)
	if trimmed == "" {
		sb.WriteString("执行完成。\n\n")
	} else {
		sb.WriteString("```\n")
		sb.WriteString(trimmed)
		sb.WriteString("\n```\n\n")
	}

	if len(logs) > 0 {
		sb.WriteString("---\n\n")
		sb.WriteString("### 2. 最近 SVN 提交\n\n")
		sb.WriteString(formatSVNTable(logs))
		sb.WriteString("\n")
	}

	sb.WriteString("---\n\n")
	sb.WriteString("### 3. CDN 更新检测\n\n")
	if checkResult.Found {
		sb.WriteString(fmt.Sprintf("- **结果**: ✅ 全部引用存在\n"))
	} else {
		sb.WriteString(fmt.Sprintf("- **结果**: ❌ 存在缺失引用\n"))
		sb.WriteString(fmt.Sprintf("- **缺失数量**: %d\n", len(checkResult.MissingFiles)))
	}
	sb.WriteString(fmt.Sprintf("- **引用总数**: %d\n", checkResult.TotalRefs))
	if checkResult.ElapsedTime > 0 {
		sb.WriteString(fmt.Sprintf("- **校验耗时**: %s\n", checkResult.ElapsedTime.Round(time.Second)))
	}
	if !checkResult.Found && len(checkResult.MissingFiles) > 0 {
		sb.WriteString("\n**缺失文件**:\n\n")
		for i, f := range checkResult.MissingFiles {
			if i >= 30 {
				sb.WriteString(fmt.Sprintf("\n... 还有 %d 个未列出\n", len(checkResult.MissingFiles)-30))
				break
			}
			sb.WriteString(fmt.Sprintf("- `%s`\n", f))
		}
		sb.WriteString("\n")
	}

	return title, sb.String()
}

// ============================================================
//  三种运行模式（返回 bool 表示任务是否已完成）
// ============================================================

func modeDeploy() bool {
	logf("===== 模式: 仅部署 =====\n")
	cfg := getEnvConfig()

	var (
		deployOutput string
		svnLogs      []LogEntry
		taskErrors   []string
	)

	output, err := executeDeploy(cfg)
	deployOutput = output
	if err != nil {
		taskErrors = append(taskErrors, fmt.Sprintf("部署出错: %v", err))
		logf("部署出错: %v\n", err)
	}

	logs, err := getRecentSvnLogs(cfg)
	if err != nil {
		taskErrors = append(taskErrors, fmt.Sprintf("SVN 出错: %v", err))
		logf("SVN 获取出错: %v\n", err)
	} else {
		svnLogs = logs
	}

	title, content := buildDeployNotifyContent(deployOutput, svnLogs)
	if len(taskErrors) > 0 {
		title = "⚠️ " + title
		content += "\n\n---\n\n### ⚠️ 异常信息\n\n"
		for _, e := range taskErrors {
			content += fmt.Sprintf("- %s\n", e)
		}
	}

	if err := sendNotification(title, content); err != nil {
		logf("通知失败: %v\n", err)
	}
	logf("===== 部署流程结束 =====\n")
	return true
}

func modeCheck() bool {
	logf("===== 模式: 仅检测更新 =====\n")
	cfg := getEnvConfig()
	result := runCheck(cfg)
	title, content := buildCheckNotifyContent(result, cfg.Label)

	if err := sendNotification(title, content); err != nil {
		logf("通知失败: %v\n", err)
	}
	logf("===== 检测流程结束 =====\n")

	// 引用校验仅为通知，始终视为完成
	return true
}

func modeFull() bool {
	logf("===== 模式: 部署 + 检测 =====\n")
	cfg := getEnvConfig()

	var (
		deployOutput string
		svnLogs      []LogEntry
	)

	output, err := executeDeploy(cfg)
	deployOutput = output
	if err != nil {
		logf("部署出错: %v\n", err)
		title := fmt.Sprintf("⚠️ 部署异常 - %s", nowStr())
		content := fmt.Sprintf("## 部署执行失败\n\n```\n%s\n```\n\n错误: %v", deployOutput, err)
		_ = sendNotification(title, content)
		return false
	}

	logs, err := getRecentSvnLogs(cfg)
	if err != nil {
		logf("SVN 出错: %v\n", err)
	} else {
		svnLogs = logs
	}

	logf("部署完成，开始本地引用校验...\n")

	result := runCheck(cfg)

	title, content := buildFullNotifyContent(deployOutput, svnLogs, result, cfg.Label)
	if err := sendNotification(title, content); err != nil {
		logf("通知失败: %v\n", err)
	}
	logf("===== 完整流程结束 =====\n")

	// 部署已完成，引用校验仅为通知，不影响部署结果
	return true
}

// ============================================================
//  main
// ============================================================

func main() {
	mode := flag.String("mode", "full", "运行模式: deploy=仅部署, check=仅检测, full=部署+检测")
	nowFlag := flag.Bool("now", false, "立即执行一次，不等待定时")
	timeStr := flag.String("time", "2130", "定时执行时间(HHMM格式)，如 2130=21:30, 905=09:05")
	flag.Parse()

	scheduleHour, scheduleMinute, err := parseScheduleTime(*timeStr)
	if err != nil {
		logf("❌ 时间参数解析失败: %v\n", err)
		logf("用法示例: -time=2130 (表示每天 21:30 执行)\n")
		os.Exit(1)
	}

	logf("定时脚本启动\n")
	logf("  模式: %s\n", *mode)
	logf("  定时: 每天 %02d:%02d\n", scheduleHour, scheduleMinute)

	if os.Getenv("PUSH_PLUS") == "" {
		logf("[警告] 环境变量 PUSH_PLUS 未设置，通知功能不可用\n")
	}

	var runFunc func() bool
	switch *mode {
	case "deploy":
		runFunc = modeDeploy
	case "check":
		runFunc = modeCheck
	case "full":
		runFunc = modeFull
	default:
		logf("未知模式: %s，使用 full\n", *mode)
		runFunc = modeFull
	}

	if *nowFlag {
		logf("检测到 --now 参数，立即执行\n")
		done := runFunc()
		if done {
			logf("✅ 任务已完成，程序退出\n")
			return
		}
		logf("⏳ 本次未完成，进入定时循环...\n")
	}

	for {
		today := time.Now()
		target := time.Date(today.Year(), today.Month(), today.Day(),
			scheduleHour, scheduleMinute, 0, 0, today.Location())

		if today.After(target) {
			target = target.Add(24 * time.Hour)
		}

		wait := target.Sub(today)
		logf("下次执行: %s（等待 %s）\n", target.Format("2006-01-02 15:04:05"), wait.Round(time.Second))

		timer := time.NewTimer(wait)
		<-timer.C

		done := runFunc()
		if done {
			logf("✅ 任务已完成，程序退出\n")
			return
		}
		logf("⏳ 本次未完成，将继续等待下一次定时执行...\n")
	}
}

// parseScheduleTime 解析 HHMM 格式的时间字符串
func parseScheduleTime(timeStr string) (int, int, error) {
	timeStr = strings.TrimSpace(timeStr)
	if timeStr == "" {
		return 0, 0, fmt.Errorf("时间参数不能为空")
	}

	var hour, minute int
	switch len(timeStr) {
	case 3:
		hour = int(timeStr[0] - '0')
		minute = int(timeStr[1]-'0')*10 + int(timeStr[2]-'0')
	case 4:
		hour = int(timeStr[0]-'0')*10 + int(timeStr[1]-'0')
		minute = int(timeStr[2]-'0')*10 + int(timeStr[3]-'0')
	default:
		return 0, 0, fmt.Errorf("时间格式错误: %q，请使用3~4位数字(如 905 或 2130)", timeStr)
	}

	if hour < 0 || hour > 23 {
		return 0, 0, fmt.Errorf("小时数无效: %d (应为 0-23)", hour)
	}
	if minute < 0 || minute > 59 {
		return 0, 0, fmt.Errorf("分钟数无效: %d (应为 0-59)", minute)
	}

	return hour, minute, nil
}
