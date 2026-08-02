package main

import (
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"
)

const (
	fxtwitterBase   = "https://api.fxtwitter.com"
	pushPlusURL     = "https://www.pushplus.plus/send"
	defaultProxy    = "http://127.0.0.1:7890"
	defaultInterval = 10
	maxRetries      = 3
)

type Config struct {
	Username      string
	Interval      int
	ProxyURL      string
	PushPlusToken string
	StateFile     string
}

type FxTwitterResponse struct {
	Code    int            `json:"code"`
	Message string         `json:"message"`
	User    *FxTwitterUser `json:"user"`
}

type FxTwitterUser struct {
	ScreenName  string `json:"screen_name"`
	Name        string `json:"name"`
	Description string `json:"description"`
	Tweets      int    `json:"tweets"`
	Followers   int    `json:"followers"`
	Following   int    `json:"following"`
	MediaCount  int    `json:"media_count"`
	Likes       int    `json:"likes"`
	AvatarURL   string `json:"avatar_url"`
	BannerURL   string `json:"banner_url"`
	Location    string `json:"location"`
	URL         string `json:"url"`
	ID          string `json:"id"`
}

type MonitorState struct {
	Username      string `json:"username"`
	Tweets        int    `json:"tweets"`
	Followers     int    `json:"followers"`
	Following     int    `json:"following"`
	Name          string `json:"name"`
	Description   string `json:"description"`
	AvatarURL     string `json:"avatar_url"`
	BannerURL     string `json:"banner_url"`
	LastCheckTime string `json:"last_check_time"`
	Initialized   bool   `json:"initialized"`
}

type PushPlusRequest struct {
	Token    string `json:"token"`
	Title    string `json:"title"`
	Content  string `json:"content"`
	Template string `json:"template"`
}

func nowStr() string {
	return time.Now().Format("2006-01-02 15:04:05")
}

func logf(format string, args ...interface{}) {
	fmt.Printf("[%s] %s", nowStr(), fmt.Sprintf(format, args...))
}

func newHTTPClient(proxyURL string) *http.Client {
	transport := &http.Transport{}
	if proxyURL != "" {
		if u, err := url.Parse(proxyURL); err == nil {
			transport.Proxy = http.ProxyURL(u)
			logf("使用代理: %s\n", proxyURL)
		}
	}
	return &http.Client{Transport: transport, Timeout: 30 * time.Second}
}

func fetchUserInfo(client *http.Client, username string) (*FxTwitterUser, error) {
	apiURL := fmt.Sprintf("%s/%s", fxtwitterBase, username)
	var lastErr error
	for i := 1; i <= maxRetries; i++ {
		req, err := http.NewRequest("GET", apiURL, nil)
		if err != nil {
			return nil, fmt.Errorf("创建请求失败: %w", err)
		}
		req.Header.Set("User-Agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36")
		resp, err := client.Do(req)
		if err != nil {
			lastErr = fmt.Errorf("请求失败(第%d次): %w", i, err)
			logf("%v\n", lastErr)
			if i < maxRetries {
				time.Sleep(time.Duration(i) * 3 * time.Second)
			}
			continue
		}
		body, err := io.ReadAll(resp.Body)
		resp.Body.Close()
		if err != nil {
			lastErr = fmt.Errorf("读取响应失败: %w", err)
			continue
		}
		if resp.StatusCode != http.StatusOK {
			lastErr = fmt.Errorf("API 返回状态码 %d: %s", resp.StatusCode, string(body))
			logf("%v\n", lastErr)
			if i < maxRetries {
				time.Sleep(time.Duration(i) * 3 * time.Second)
			}
			continue
		}
		var fxtResp FxTwitterResponse
		if err := json.Unmarshal(body, &fxtResp); err != nil {
			lastErr = fmt.Errorf("解析 JSON 失败: %w", err)
			continue
		}
		if fxtResp.Code != 200 || fxtResp.User == nil {
			lastErr = fmt.Errorf("API 返回错误: code=%d, message=%s", fxtResp.Code, fxtResp.Message)
			continue
		}
		return fxtResp.User, nil
	}
	return nil, lastErr
}

func loadState(path string) (*MonitorState, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return &MonitorState{Initialized: false}, nil
		}
		return nil, err
	}
	var state MonitorState
	if err := json.Unmarshal(data, &state); err != nil {
		return nil, fmt.Errorf("解析状态文件失败: %w", err)
	}
	return &state, nil
}

func saveState(path string, state *MonitorState) error {
	data, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		return fmt.Errorf("序列化状态失败: %w", err)
	}
	return os.WriteFile(path, data, 0644)
}

type ChangeType string

const (
	ChangeNewTweet    ChangeType = "new_tweet"
	ChangeDeleteTweet ChangeType = "delete_tweet"
	ChangeName        ChangeType = "name"
	ChangeBio         ChangeType = "bio"
	ChangeAvatar      ChangeType = "avatar"
	ChangeBanner      ChangeType = "banner"
)

type Change struct {
	Type ChangeType
	Old  string
	New  string
}

func detectChanges(state *MonitorState, user *FxTwitterUser) []Change {
	var changes []Change
	if state.Tweets != user.Tweets {
		ct := ChangeNewTweet
		if user.Tweets < state.Tweets {
			ct = ChangeDeleteTweet
		}
		changes = append(changes, Change{
			Type: ct,
			Old:  fmt.Sprintf("%d", state.Tweets),
			New:  fmt.Sprintf("%d", user.Tweets),
		})
	}
	if state.Name != user.Name {
		changes = append(changes, Change{Type: ChangeName, Old: state.Name, New: user.Name})
	}
	if state.Description != user.Description {
		changes = append(changes, Change{Type: ChangeBio, Old: state.Description, New: user.Description})
	}
	if state.AvatarURL != user.AvatarURL {
		changes = append(changes, Change{Type: ChangeAvatar, Old: state.AvatarURL, New: user.AvatarURL})
	}
	if state.BannerURL != user.BannerURL {
		changes = append(changes, Change{Type: ChangeBanner, Old: state.BannerURL, New: user.BannerURL})
	}
	return changes
}

func sendNotification(token, title, content string) error {
	if token == "" {
		return fmt.Errorf("PUSH_PLUS 未设置，跳过通知")
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
	return fmt.Errorf("通知发送失败（已重试 3 次）")
}

func buildNotifyContent(changes []Change, user *FxTwitterUser) (string, string) {
	now := nowStr()
	profileURL := fmt.Sprintf("https://x.com/%s", user.ScreenName)
	var title string
	var sb strings.Builder
	for _, c := range changes {
		if c.Type == ChangeNewTweet {
			title = fmt.Sprintf("🐦 @%s 发布新推文！（%d 条）", user.ScreenName, user.Tweets)
			break
		}
	}
	if title == "" {
		for _, c := range changes {
			if c.Type == ChangeDeleteTweet {
				title = fmt.Sprintf("🗑️ @%s 删除了推文（%s -> %s）", user.ScreenName, c.Old, c.New)
				break
			}
		}
	}
	if title == "" {
		title = fmt.Sprintf("📝 @%s 更新了个人资料", user.ScreenName)
	}
	sb.WriteString(fmt.Sprintf("## %s (@%s)\n\n", user.Name, user.ScreenName))
	sb.WriteString(fmt.Sprintf("- **检测时间**: %s\n", now))
	sb.WriteString(fmt.Sprintf("- **推文数**: %d\n", user.Tweets))
	sb.WriteString(fmt.Sprintf("- **粉丝数**: %d\n", user.Followers))
	sb.WriteString(fmt.Sprintf("- **关注数**: %d\n", user.Following))
	if user.Location != "" {
		sb.WriteString(fmt.Sprintf("- **所在地**: %s\n", user.Location))
	}
	sb.WriteString(fmt.Sprintf("- **查看主页**: [点击前往](%s)\n\n", profileURL))
	markdownHr := strings.Repeat("-", 3)
	sb.WriteString(markdownHr + "\n\n### 变更详情\n\n")
	for _, c := range changes {
		switch c.Type {
		case ChangeNewTweet:
			diff := atoiSafe(c.New) - atoiSafe(c.Old)
			sb.WriteString(fmt.Sprintf("- **新推文**: 新增 %d 条（%s -> %s）\n", diff, c.Old, c.New))
		case ChangeDeleteTweet:
			diff := atoiSafe(c.Old) - atoiSafe(c.New)
			sb.WriteString(fmt.Sprintf("- **删除推文**: 减少 %d 条（%s -> %s）\n", diff, c.Old, c.New))
		case ChangeName:
			sb.WriteString(fmt.Sprintf("- **昵称**: %s -> %s\n", c.Old, c.New))
		case ChangeBio:
			sb.WriteString(fmt.Sprintf("- **简介**: %s -> %s\n", c.Old, c.New))
		case ChangeAvatar:
			sb.WriteString("- **头像**: 已更新\n")
		case ChangeBanner:
			sb.WriteString("- **横幅**: 已更新\n")
		}
	}
	sb.WriteString("\n")
	if user.AvatarURL != "" {
		avatar := strings.Replace(user.AvatarURL, "_normal", "_200x200", 1)
		sb.WriteString(fmt.Sprintf("![头像](%s)\n", avatar))
	}
	return title, sb.String()
}

func atoiSafe(s string) int {
	n := 0
	fmt.Sscanf(s, "%d", &n)
	return n
}

func updateUserState(state *MonitorState, user *FxTwitterUser) {
	state.Username = user.ScreenName
	state.Tweets = user.Tweets
	state.Followers = user.Followers
	state.Following = user.Following
	state.Name = user.Name
	state.Description = user.Description
	state.AvatarURL = user.AvatarURL
	state.BannerURL = user.BannerURL
	state.LastCheckTime = nowStr()
	state.Initialized = true
}

func runCheck(cfg Config, state *MonitorState, client *http.Client) bool {
	logf("开始检查 @%s ...\n", cfg.Username)
	user, err := fetchUserInfo(client, cfg.Username)
	if err != nil {
		logf("获取用户信息失败: %v\n", err)
		return false
	}
	logf("获取成功: %s (@%s) | 推文:%d 粉丝:%d 关注:%d\n",
		user.Name, user.ScreenName, user.Tweets, user.Followers, user.Following)
	if !state.Initialized {
		logf("首次运行，初始化状态（不发送通知）\n")
		updateUserState(state, user)
		if err := saveState(cfg.StateFile, state); err != nil {
			logf("保存状态失败: %v\n", err)
		}
		return true
	}
	changes := detectChanges(state, user)
	if len(changes) == 0 {
		logf("无变化\n")
	} else {
		logf("检测到 %d 项变更\n", len(changes))
		title, content := buildNotifyContent(changes, user)
		if err := sendNotification(cfg.PushPlusToken, title, content); err != nil {
			logf("通知失败: %v\n", err)
		}
	}
	updateUserState(state, user)
	if err := saveState(cfg.StateFile, state); err != nil {
		logf("保存状态失败: %v\n", err)
	}
	return true
}

func main() {
	username := flag.String("user", "1Ylik", "监控的 Twitter/X 用户名（不带 @）")
	interval := flag.Int("interval", defaultInterval, "检查间隔（分钟）")
	proxyURL := flag.String("proxy", "", "HTTP 代理地址（留空则使用环境变量 HTTP_PROXY 或默认值）")
	stateFile := flag.String("state", "", "状态文件路径（默认为同目录下 state.json）")
	once := flag.Bool("once", false, "只检查一次，不进入循环")
	testNotify := flag.Bool("test", false, "发送测试通知并退出")
	flag.Parse()
	// 状态文件路径：优先 exe 所在目录，兜底当前目录
	if *stateFile == "" {
		exe, err := os.Executable()
		if err == nil {
			exeDir := filepath.Dir(exe)
			// go run 时 exe 在临时目录，回退到当前工作目录
			if strings.Contains(exeDir, os.TempDir()) {
				*stateFile = "state.json"
			} else {
				*stateFile = filepath.Join(exeDir, "state.json")
			}
		} else {
			*stateFile = "state.json"
		}
	}
	cfg := Config{
		Username:      *username,
		Interval:      *interval,
		ProxyURL:      *proxyURL,
		PushPlusToken: os.Getenv("PUSH_PLUS"),
		StateFile:     *stateFile,
	}
	if cfg.ProxyURL == "" {
		cfg.ProxyURL = os.Getenv("HTTP_PROXY")
	}
	if cfg.ProxyURL == "" {
		cfg.ProxyURL = os.Getenv("HTTPS_PROXY")
	}
	if cfg.ProxyURL == "" {
		cfg.ProxyURL = defaultProxy
	}
	logf("Twitter/X 用户监控服务启动\n")
	logf("  监控用户: @%s\n", cfg.Username)
	logf("  检查间隔: %d 分钟\n", cfg.Interval)
	logf("  代理地址: %s\n", cfg.ProxyURL)
	logf("  状态文件: %s\n", cfg.StateFile)
	ppStatus := "未配置"
	if cfg.PushPlusToken != "" {
		ppStatus = "已配置"
	}
	logf("  PushPlus: %s\n", ppStatus)
	if cfg.PushPlusToken == "" {
		logf("[警告] 环境变量 PUSH_PLUS 未设置，通知功能不可用\n")
	}
	if *testNotify {
		logf("发送测试通知...\n")
		title := "Twitter 监控测试通知"
		content := fmt.Sprintf("## 测试通知\n\n- **时间**: %s\n- **监控用户**: @%s\n- **服务状态**: 正常运行\n", nowStr(), cfg.Username)
		if err := sendNotification(cfg.PushPlusToken, title, content); err != nil {
			logf("测试通知失败: %v\n", err)
			os.Exit(1)
		}
		return
	}
	client := newHTTPClient(cfg.ProxyURL)
	state, err := loadState(cfg.StateFile)
	if err != nil {
		logf("加载状态失败: %v，使用空状态\n", err)
		state = &MonitorState{Initialized: false}
	}
	if *once {
		runCheck(cfg, state, client)
		return
	}
	logf("进入循环监控模式\n")
	runCheck(cfg, state, client)
	for {
		logf("等待 %d 分钟后再次检查...\n", cfg.Interval)
		time.Sleep(time.Duration(cfg.Interval) * time.Minute)
		runCheck(cfg, state, client)
	}
}