// sdpToggle — SDP 内网/外网切换托盘程序（Go 版）
//
// 功能：
//   - Shift+W 全局热键切换内网/外网（走 SDP Electron 客户端自身 IPC）
//   - 右下角托盘图标实时显示状态：绿=内网 Active，灰=外网，红=客户端未运行
//   - 每次切换弹 Windows toast 通知
//   - 日志写同目录 sdp_toggle.log
//
// 前提：SDP 客户端以调试模式启动（sdp-start.bat）并已登录。
package main

import (
	"context"
	"sync/atomic"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

var appDir string

// switching guards concurrent toggles and drives the yellow "busy" tray state.
var switching int32

// onToggleRequest is invoked by the tray menu / double-click / Shift+W.
var onToggleRequest func()

func main() {
	if len(os.Args) <= 1 || (len(os.Args) > 1 && os.Args[1] != "-no-tray") {
		// tray mode: single instance via named mutex
		if !acquireSingleInstance() {
			os.Exit(0) // already running
		}
		defer releaseSingleInstance()
	}
	noTray := flag.Bool("no-tray", false, "run without tray icon (CLI mode)")
	flag.Parse()

	exe, err := os.Executable()
	if err == nil {
		appDir = filepath.Dir(exe)
	} else {
		appDir, _ = os.Getwd()
	}

	ctx := context.Background()

	if *noTray {
		runCLI(ctx)
		return
	}

	logLine(appDir, "tray started")
	go trayLoop()
	time.Sleep(300 * time.Millisecond) // let tray window come up

	// status monitor: refresh icon every 10s
	go func() {
		for {
			cur, err := isConnected(ctx)
			if err != nil {
				setTrayState(nil, true, "SDP 切换 — 客户端未运行\nShift+W 切换")
			} else if cur {
				setTrayState(&cur, false, "SDP 切换 — 内网 Active\nShift+W 切换")
			} else {
				setTrayState(&cur, false, "SDP 切换 — 外网\nShift+W 切换")
			}
			time.Sleep(10 * time.Second)
		}
	}()

	onToggleRequest = func() {
		if !atomic.CompareAndSwapInt32(&switching, 0, 1) {
			notify("SDP 正在切换中", "请等待当前切换完成（图标为黄色）")
			return
		}
		go func() {
			defer atomic.StoreInt32(&switching, 0)
			setTraySwitching("SDP 切换中… 请稍候")
			msg, _, err := toggle(ctx)
			if err != nil {
				notify("SDP 切换失败", err.Error())
				logLine(appDir, "ERR "+err.Error())
			} else {
				notify("SDP 已切换", msg)
				logLine(appDir, "toggle -> "+msg)
			}
			cur, err2 := isConnected(ctx)
			if err2 != nil {
				setTrayState(nil, true, "SDP 切换 — 客户端未运行\nShift+W 切换")
			} else {
				setTrayState(&cur, false, trayTip(cur))
			}
		}()
	}

	// immediate icon refresh
	if cur, err := isConnected(ctx); err == nil {
		setTrayState(&cur, false, trayTip(cur))
	} else {
		setTrayState(nil, true, "SDP 切换 — 客户端未运行\nShift+W 切换")
	}

	if err := hotkeyLoop(func() { onToggleRequest() }); err != nil {
		notify("SDP 切换", err.Error())
		logLine(appDir, "hotkey err: "+err.Error())
	}
	removeTray()
}

func trayTip(intranet bool) string {
	if intranet {
		return "SDP 切换 — 内网 Active\nShift+W 切换"
	}
	return "SDP 切换 — 外网\nShift+W 切换"
}

// runCLI handles one command from argv (status/intranet/internet/toggle) and exits.
func runCLI(ctx context.Context) {
	cmd := "toggle"
	if flag.NArg() > 0 {
		cmd = flag.Arg(0)
	}
	switch cmd {
	case "status":
		cur, err := isConnected(ctx)
		if err != nil {
			fmt.Println("ERR", err)
			os.Exit(1)
		}
		if cur {
			fmt.Println("内网已连接")
		} else {
			fmt.Println("外网模式")
		}
	case "intranet":
		res, err := connectIntranet(ctx)
		if err != nil {
			fmt.Println("ERR", err)
			os.Exit(1)
		}
		fmt.Println(res)
	case "internet":
		res, err := disconnectInternet(ctx)
		if err != nil {
			fmt.Println("ERR", err)
			os.Exit(1)
		}
		fmt.Println(res)
	case "toggle":
		msg, _, err := toggle(ctx)
		if err != nil {
			fmt.Println("ERR", err)
			os.Exit(1)
		}
		fmt.Println(msg)
		notify("SDP 已切换", msg)
	default:
		fmt.Println("用法: sdp-toggle [status|intranet|internet|toggle] 或不带参数启动托盘")
	}
}
