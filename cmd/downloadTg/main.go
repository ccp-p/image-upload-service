package main

import (
	"bufio"
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync/atomic"
	"time"

	"github.com/gotd/td/telegram"
	"github.com/gotd/td/telegram/auth"
	"github.com/gotd/td/telegram/dcs"
	"github.com/gotd/td/telegram/downloader"
	"github.com/gotd/td/tg"
	"golang.org/x/net/proxy"
	"golang.org/x/term"
)

// 变量配置：可以通过命令行参数传入，也可以直接修改这里
var (
	// 使用 Telegram Desktop 的公开 AppID 和 AppHash
	// 这样你就不需要自己去申请了，直接运行脚本即可
	AppID   int    = 2040
	AppHash string = "b18441a1ff607e10a989891a5462e627"
	Phone   string = "" // 选填: 你的手机号
)

func main() {
	// 1. 解析参数
	link := flag.String("link", "https://t.me/TwitterSex_cn/104493", "Telegram 消息链接 (例如 https://t.me/channelname/123)")
	outDir := flag.String("out", ".", "保存目录")
	flag.IntVar(&AppID, "id", AppID, "App ID")
	flag.StringVar(&AppHash, "hash", AppHash, "App Hash")
	flag.StringVar(&Phone, "phone", Phone, "手机号")
	flag.Parse()

	// 2. 交互式获取链接
	if *link == "" {
		fmt.Print("🔗 请输入消息链接: ")
		scanner := bufio.NewScanner(os.Stdin)
		if scanner.Scan() {
			*link = strings.TrimSpace(scanner.Text())
		}
	}

	// 3. 初始化客户端
	// 使用本地 Session 目录缓存登录状态，避免每次都输入验证码
	sessionDir := filepath.Join(".", "td_session")
	if err := os.MkdirAll(sessionDir, 0700); err != nil {
		panic(err)
	}

	// 设置 SOCKS5 代理 (127.0.0.1:7890)
	socks5Dialer, err := proxy.SOCKS5("tcp", "127.0.0.1:7890", nil, proxy.Direct)
	if err != nil {
		panic(fmt.Errorf("创建代理失败: %w", err))
	}

	// 包装 Dial 函数以适配 dcs.PlainOptions
	proxyDialContext := func(ctx context.Context, network, addr string) (net.Conn, error) {
		return socks5Dialer.Dial(network, addr)
	}
	
	client := telegram.NewClient(AppID, AppHash, telegram.Options{
		SessionStorage: &telegram.FileSessionStorage{
			Path: filepath.Join(sessionDir, "session.json"),
		},
		// 配置代理
		Resolver: dcs.Plain(dcs.PlainOptions{
			Dial: proxyDialContext,
		}),
		// 伪装成 Telegram Desktop 客户端，降低风控概率，且无需自己申请ID
		Device: telegram.DeviceConfig{
			DeviceModel:    "Desktop",
			SystemVersion:  "Windows 10",
			AppVersion:     "4.8.3",
			SystemLangCode: "en",
			LangCode:       "en",
		},
	})

	err = client.Run(context.Background(), func(ctx context.Context) error {
		// 4. 登录认证
		flow := auth.NewFlow(termAuth{phone: Phone}, auth.SendCodeOptions{})
		if err := client.Auth().IfNecessary(ctx, flow); err != nil {
			return fmt.Errorf("认证失败: %w", err)
		}
		fmt.Println("✅ 登录成功")

		// 5. 解析链接并获取消息
		api := client.API()
		message, err := resolveMessage(ctx, api, *link)
		if err != nil {
			return fmt.Errorf("解析链接失败: %w", err)
		}

		// 6. 提取媒体信息
		media, ok := getMediaLocation(message)
		if !ok {
			return fmt.Errorf("该消息中没有可下载的媒体文件")
		}

		// 7. 准备下载
		fileName := getFileName(message)
		outPath := filepath.Join(*outDir, fileName)
		
		// 检查文件是否存在
		if _, err := os.Stat(outPath); err == nil {
			fmt.Printf("⚠️ 文件已存在: %s，跳过下载\n", outPath)
			return nil
		}

		fmt.Printf("🚀 开始下载: %s\n", fileName)
		startTime := time.Now()

		// 使用 gotd 的并发下载器
		d := downloader.NewDownloader()
		
		// 简单的进度条实现
		var downloaded int64
		// 获取文件大小（如果可用）
		totalSize := getMediaSize(message)
		
		// 启动进度显示协程
		done := make(chan struct{})
		go func() {
			ticker := time.NewTicker(1 * time.Second)
			defer ticker.Stop()
			for {
				select {
				case <-done:
					return
				case <-ticker.C:
					current := atomic.LoadInt64(&downloaded)
					speed := float64(current) / time.Since(startTime).Seconds() / 1024 / 1024
					if totalSize > 0 {
						percent := float64(current) / float64(totalSize) * 100
						fmt.Printf("\r⬇️  进度: %.1f%% (%.2f MB/s)   ", percent, speed)
					} else {
						fmt.Printf("\r⬇️  已下载: %.2f MB (%.2f MB/s)   ", float64(current)/1024/1024, speed)
					}
				}
			}
		}()

		// 创建文件
		f, err := os.Create(outPath)
		if err != nil {
			close(done)
			return fmt.Errorf("创建文件失败: %w", err)
		}
		defer f.Close()

		// 使用 Stream 方法并包装 Writer 来实现进度监控
		writer := &progressWriter{
			w: f,
			onWrite: func(n int) {
				atomic.AddInt64(&downloaded, int64(n))
			},
		}

		// 设置 8 线程并发下载
		if _, err := d.Download(api, media).WithThreads(8).Stream(ctx, writer); err != nil {
			close(done)
			return fmt.Errorf("下载出错: %w", err)
		}

		close(done)
		fmt.Println() // 换行

		fmt.Printf("✨ 下载完成! 耗时: %v\n📂 文件位置: %s\n", time.Since(startTime), outPath)
		return nil
	})

	if err != nil {
		fmt.Printf("❌ 程序执行失败: %v\n", err)
		os.Exit(1)
	}
}

// --- 辅助函数 ---

// 进度 Writer
type progressWriter struct {
	w       io.Writer
	onWrite func(int)
}

func (pw *progressWriter) Write(p []byte) (int, error) {
	n, err := pw.w.Write(p)
	if n > 0 && pw.onWrite != nil {
		pw.onWrite(n)
	}
	return n, err
}

// 解析消息链接
func resolveMessage(ctx context.Context, api *tg.Client, link string) (*tg.Message, error) {
	// 格式1: https://t.me/username/123
	// 格式2: https://t.me/c/1234567890/123 (私有群组)
	
	parts := strings.Split(link, "/")
	if len(parts) < 2 {
		return nil, fmt.Errorf("无效链接格式")
	}

	msgIDStr := parts[len(parts)-1]
	msgID, err := strconv.Atoi(msgIDStr)
	if err != nil {
		return nil, fmt.Errorf("无法解析消息ID: %s", msgIDStr)
	}

	identifier := parts[len(parts)-2]
	
	var inputChannel tg.InputChannelClass

	if identifier == "c" && len(parts) >= 3 {
		// 私有群组/频道 ID
		channelIDStr := parts[len(parts)-2] // c/123456/123 -> 123456
		channelID, err := strconv.ParseInt(channelIDStr, 10, 64)
		if err != nil {
			return nil, fmt.Errorf("无法解析频道ID")
		}

		// 对于私有频道，我们需要 AccessHash。
		fmt.Println("🔍 正在查找私有频道信息...")
		chats, err := api.MessagesGetChats(ctx, []int64{channelID})
		if err != nil {
			return nil, fmt.Errorf("无法获取频道信息，请确保你已加入该频道: %w", err)
		}
		
		var targetChat *tg.Channel
		
		switch c := chats.(type) {
		case *tg.MessagesChats:
			if len(c.Chats) > 0 {
				if ch, ok := c.Chats[0].(*tg.Channel); ok {
					targetChat = ch
				}
			}
		case *tg.MessagesChatsSlice:
			if len(c.Chats) > 0 {
				if ch, ok := c.Chats[0].(*tg.Channel); ok {
					targetChat = ch
				}
			}
		}

		if targetChat == nil {
			return nil, fmt.Errorf("未找到频道，请确认你已加入")
		}
		
		inputChannel = &tg.InputChannel{
			ChannelID:  targetChat.ID,
			AccessHash: targetChat.AccessHash,
		}

	} else {
		// 公开用户名
		fmt.Printf("🔍 解析用户名: %s\n", identifier)
		resolved, err := api.ContactsResolveUsername(ctx, &tg.ContactsResolveUsernameRequest{Username: identifier})
		if err != nil {
			return nil, fmt.Errorf("解析用户名失败: %w", err)
		}
		if len(resolved.Chats) > 0 {
			channel := resolved.Chats[0].(*tg.Channel)
			inputChannel = &tg.InputChannel{
				ChannelID:  channel.ID,
				AccessHash: channel.AccessHash,
			}
		} else {
			return nil, fmt.Errorf("未找到对应的频道")
		}
	}

	// 获取消息
	fmt.Printf("📩 获取消息 ID: %d\n", msgID)
	msgs, err := api.ChannelsGetMessages(ctx, &tg.ChannelsGetMessagesRequest{
		Channel: inputChannel,
		ID:      []tg.InputMessageClass{&tg.InputMessageID{ID: msgID}},
	})
	if err != nil {
		return nil, fmt.Errorf("获取消息失败: %w", err)
	}

	messages := msgs.(*tg.MessagesChannelMessages).Messages
	if len(messages) == 0 {
		return nil, fmt.Errorf("消息不存在")
	}

	msg, ok := messages[0].(*tg.Message)
	if !ok {
		return nil, fmt.Errorf("获取到的不是有效消息")
	}

	return msg, nil
}

// 提取媒体位置
func getMediaLocation(msg *tg.Message) (tg.InputFileLocationClass, bool) {
	if msg.Media == nil {
		return nil, false
	}

	switch m := msg.Media.(type) {
	case *tg.MessageMediaDocument:
		doc, ok := m.Document.AsNotEmpty()
		if !ok {
			return nil, false
		}
		return &tg.InputDocumentFileLocation{
			ID:            doc.ID,
			AccessHash:    doc.AccessHash,
			FileReference: doc.FileReference,
			ThumbSize:     "",
		}, true
	case *tg.MessageMediaPhoto:
		photo, ok := m.Photo.AsNotEmpty()
		if !ok {
			return nil, false
		}
		// 获取最大的图片尺寸
		var maxSize string
		for _, size := range photo.Sizes {
			if s, ok := size.(interface{ GetType() string }); ok {
				maxSize = s.GetType()
			}
		}
		return &tg.InputPhotoFileLocation{
			ID:            photo.ID,
			AccessHash:    photo.AccessHash,
			FileReference: photo.FileReference,
			ThumbSize:     maxSize,
		}, true
	}
	return nil, false
}

// 获取文件名
func getFileName(msg *tg.Message) string {
	if msg.Media == nil {
		return fmt.Sprintf("file_%d", msg.ID)
	}

	switch m := msg.Media.(type) {
	case *tg.MessageMediaDocument:
		doc, ok := m.Document.AsNotEmpty()
		if !ok {
			break
		}
		// 尝试从属性中查找文件名
		for _, attr := range doc.Attributes {
			if filenameAttr, ok := attr.(*tg.DocumentAttributeFilename); ok {
				return filenameAttr.FileName
			}
		}
		// 如果是视频但没有文件名
		for _, attr := range doc.Attributes {
			if _, ok := attr.(*tg.DocumentAttributeVideo); ok {
				return fmt.Sprintf("video_%d.mp4", msg.ID)
			}
		}
		return fmt.Sprintf("doc_%d", msg.ID)
	}
	return fmt.Sprintf("media_%d.jpg", msg.ID)
}

// 获取文件大小
func getMediaSize(msg *tg.Message) int64 {
	if msg.Media == nil {
		return 0
	}
	switch m := msg.Media.(type) {
	case *tg.MessageMediaDocument:
		doc, ok := m.Document.AsNotEmpty()
		if ok {
			return doc.Size
		}
	}
	return 0
}

// 终端认证交互
type termAuth struct {
	phone string
}

func (a termAuth) Phone(_ context.Context) (string, error) {
	if a.phone != "" {
		return a.phone, nil
	}
	fmt.Print("📱 请输入手机号 (例如 +8613800000000): ")
	var phone string
	fmt.Scanln(&phone)
	return phone, nil
}

func (a termAuth) Password(_ context.Context) (string, error) {
	fmt.Print("🔐 请输入两步验证密码: ")
	bytePw, err := term.ReadPassword(int(os.Stdin.Fd()))
	if err != nil {
		return "", err
	}
	fmt.Println()
	return string(bytePw), nil
}

func (a termAuth) Code(_ context.Context, _ *tg.AuthSentCode) (string, error) {
	fmt.Print("📩 请输入 Telegram 收到的验证码: ")
	var code string
	fmt.Scanln(&code)
	return code, nil
}

func (a termAuth) AcceptTermsOfService(_ context.Context, tos tg.HelpTermsOfService) error {
	return nil
}

func (a termAuth) SignUp(_ context.Context) (auth.UserInfo, error) {
	return auth.UserInfo{}, errors.New("不支持注册，请先在手机上注册")
}

// 进度回调适配器
type progressFunc func(ctx context.Context, chunk int64) error

func (p progressFunc) Chunk(ctx context.Context, chunk int64) error {
	return p(ctx, chunk)
}
