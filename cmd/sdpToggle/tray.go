//go:build windows

package main

// System tray via Shell_NotifyIcon + popup menu (pure Win32, no deps).
// Green = intranet, gray = internet, red = client unreachable, yellow = switching.

import (
	"bytes"
	"fmt"
	"image"
	"image/color"
	"image/draw"
	pngstd "image/png"
	"os"
	"path/filepath"
	"sync"
	"syscall"
	"time"
	"unsafe"
)

var (
	shell32   = syscall.NewLazyDLL("shell32.dll")
	user32DLL = syscall.NewLazyDLL("user32.dll")
	kernel32  = syscall.NewLazyDLL("kernel32.dll")

	procShellNotifyIcon = shell32.NewProc("Shell_NotifyIconW")
	procCreateWindowEx  = user32DLL.NewProc("CreateWindowExW")
	procDefWindowProc   = user32DLL.NewProc("DefWindowProcW")
	procRegisterClass   = user32DLL.NewProc("RegisterClassExW")
	procGetMessage      = user32DLL.NewProc("GetMessageW")
	procDispatchMsg     = user32DLL.NewProc("DispatchMessageW")
	procGetModuleHandle = kernel32.NewProc("GetModuleHandleW")
	procDestroyIcon     = user32DLL.NewProc("DestroyIcon")
	procLoadImage       = user32DLL.NewProc("LoadImageW")
	procCreatePopupMenu = user32DLL.NewProc("CreatePopupMenu")
	procAppendMenu      = user32DLL.NewProc("AppendMenuW")
	procTrackPopupMenu  = user32DLL.NewProc("TrackPopupMenuEx")
	procDestroyMenu     = user32DLL.NewProc("DestroyMenu")
	procPostMessage     = user32DLL.NewProc("PostMessageW")
	procSetForeground   = user32DLL.NewProc("SetForegroundWindow")
	procGetCursorPos    = user32DLL.NewProc("GetCursorPos")
)

const (
	nimAdd    = 0
	nimModify = 1
	nimDelete = 2
	nifMsg    = 1
	nifIcon   = 2
	nifTip    = 4

	wmTrayCallback = 0x0400 + 8000
	wmAppToggle    = 0x0400 + 8001
	wmAppQuit      = 0x0400 + 8002

	tpmRightAlign  = 0x0008
	tpmBottomAlign = 0x0020
)

type notifyIconData struct {
	cbSize           uint32
	hwnd             uintptr
	uID              uint32
	uFlags           uint32
	uCallbackMessage uint32
	hIcon            uintptr
	szTip            [128]uint16
	dwState          uint32
	dwStateMask      uint32
	szInfo           [256]uint16
	uVersion         uint32
	szInfoTitle      [64]uint16
	dwInfoFlags      uint32
	guidItem         [16]byte
	hBalloonIcon     uintptr
}

type trayColor struct{ r, g, b uint8 }

var (
	trayColorGreen  = trayColor{34, 139, 34}
	trayColorGray   = trayColor{150, 150, 150}
	trayColorRed    = trayColor{200, 60, 60}
	trayColorYellow = trayColor{230, 180, 30}
)

var (
	trayMu      sync.Mutex
	trayHWND    uintptr
	curColor    = trayColorGray
	curTip      = "SDP Toggle"
	currentIcon uintptr
)

func utf16Fixed(s string, n int) []uint16 {
	out := make([]uint16, n)
	i := 0
	for _, r := range s {
		if i >= n-1 {
			break
		}
		out[i] = uint16(r)
		i++
	}
	return out
}

// ---- state ----

func setTrayState(intranet *bool, unreachable bool, tip string) {
	trayMu.Lock()
	defer trayMu.Unlock()
	switch {
	case unreachable:
		curColor = trayColorRed
	case intranet != nil && *intranet:
		curColor = trayColorGreen
	default:
		curColor = trayColorGray
	}
	curTip = tip
	if trayHWND != 0 {
		refreshTray()
	}
}

func setTraySwitching(tip string) {
	trayMu.Lock()
	defer trayMu.Unlock()
	curColor = trayColorYellow
	curTip = tip
	if trayHWND != 0 {
		refreshTray()
	}
}

// ---- icon ----

func buildIconPNG(c trayColor) []byte {
	const size = 64
	img := image.NewNRGBA(image.Rect(0, 0, size, size))
	bg := color.RGBA{c.r, c.g, c.b, 255}
	draw.Draw(img, img.Bounds(), &image.Uniform{bg}, image.Point{}, draw.Src)
	white := color.RGBA{255, 255, 255, 255}
	for x := 10; x < 54; x++ {
		for y := 10; y < 54; y++ {
			dx, dy := x - 32, y - 32
			d := dx*dx + dy*dy
			if d >= 484 && d <= 676 {
				img.Set(x, y, white)
			}
		}
	}
	drawThick := func(x0, y0, x1, y1 int) {
		steps := 32
		for i := 0; i <= steps; i++ {
			x := x0 + (x1-x0)*i/steps
			y := y0 + (y1-y0)*i/steps
			for dx := -2; dx <= 2; dx++ {
				for dy := -2; dy <= 2; dy++ {
					img.Set(x+dx, y+dy, white)
				}
			}
		}
	}
	switch c {
	case trayColorGreen:
		drawThick(18, 34, 28, 44)
		drawThick(28, 44, 46, 22)
	case trayColorYellow:
		drawThick(16, 22, 48, 42)
	case trayColorRed:
		drawThick(20, 20, 44, 44)
		drawThick(44, 20, 20, 44)
	default:
		drawThick(20, 20, 44, 44)
		drawThick(44, 20, 20, 44)
	}
	var pngBuf bytes.Buffer
	if err := pngstd.Encode(&pngBuf, img); err != nil {
		return nil
	}
	n := len(pngBuf.Bytes())
	out := []byte{0, 0, 1, 0, 1, 0, byte(size), byte(size), 0, 0, 32, 0, 0, 0,
		byte(n), byte(n >> 8), byte(n >> 16), byte(n >> 24), 22, 0, 0, 0}
	out = append(out, pngBuf.Bytes()...)
	return out
}

func makeHIcon(c trayColor) uintptr {
	buf := buildIconPNG(c)
	if buf == nil {
		return 0
	}
	tmp := filepath.Join(os.TempDir(), fmt.Sprintf("sdp_toggle_%d_%d.ico", c.r, time.Now().UnixNano()))
	if err := os.WriteFile(tmp, buf, 0644); err != nil {
		logLine(appDir, "write ico failed: "+err.Error())
		return 0
	}
	defer os.Remove(tmp)
	p16, _ := syscall.UTF16PtrFromString(tmp)
	h, _, _ := procLoadImage.Call(0, uintptr(unsafe.Pointer(p16)), 1, 0, 0, 0x0010)
	return h
}

// ---- posting ----

func postTrayRet(hIcon uintptr, tip string, op uintptr) uintptr {
	ni := notifyIconData{
		cbSize:           uint32(unsafe.Sizeof(notifyIconData{})),
		hwnd:             trayHWND,
		uID:              1,
		uFlags:           nifMsg | nifIcon | nifTip,
		uCallbackMessage: wmTrayCallback,
		hIcon:            hIcon,
	}
	copy(ni.szTip[:], utf16Fixed(tip, 128))
	r, _, _ := procShellNotifyIcon.Call(op, uintptr(unsafe.Pointer(&ni)))
	return r
}

func refreshTray() {
	if currentIcon != 0 {
		procDestroyIcon.Call(currentIcon)
		currentIcon = 0
	}
	h := makeHIcon(curColor)
	currentIcon = h
	if r := postTrayRet(h, curTip, nimModify); r == 0 {
		logLine(appDir, "NIM_MODIFY failed")
	}
}

// ---- window & message loop ----

func trayWndProc(hwnd, msg, wParam, lParam uintptr) uintptr {
	const wmCommand = 0x0111
	if msg == wmCommand {
		dispatchAction(mapCommand(wParam))
		return 0
	}
	if msg == wmTrayCallback {
		switch a := mapTrayMouseEvent(int32(lParam)); a {
		case actionShowMenu:
			showTrayMenu(hwnd)
		case actionToggle:
			dispatchAction(a)
		}
		return 0
	}
	if msg == wmAppToggle {
		dispatchAction(actionToggle)
		return 0
	}
	if msg == wmAppQuit {
		dispatchAction(actionQuit)
		return 0
	}
	r, _, _ := procDefWindowProc.Call(hwnd, msg, wParam, lParam)
	return r
}

func showTrayMenu(hwnd uintptr) {
	logLine(appDir, "showTrayMenu called")
	menu, _, _ := procCreatePopupMenu.Call()
	if menu == 0 {
		return
	}
	statusText := "状态：未知"
	trayMu.Lock()
	switch curColor {
	case trayColorGreen:
		statusText = "状态：内网 Active"
	case trayColorYellow:
		statusText = "状态：切换中…"
	case trayColorRed:
		statusText = "状态：客户端未运行"
	default:
		statusText = "状态：外网"
	}
	trayMu.Unlock()

	mfString := uintptr(0x00000000)
	mfDisabled := uintptr(0x00000003)
	sep := uintptr(0x00000800) // MF_SEPARATOR
	st1, _ := syscall.UTF16PtrFromString(statusText)
	st2, _ := syscall.UTF16PtrFromString("切换 内网/外网 (Shift+W)")
	st3, _ := syscall.UTF16PtrFromString("退出")
	procAppendMenu.Call(menu, mfString|mfDisabled, 0, uintptr(unsafe.Pointer(st1)))
	procAppendMenu.Call(menu, sep, 0, 0)
	procAppendMenu.Call(menu, mfString, uintptr(wmAppToggle), uintptr(unsafe.Pointer(st2)))
	procAppendMenu.Call(menu, mfString, uintptr(wmAppQuit), uintptr(unsafe.Pointer(st3)))

	var pt struct{ x, y int32 }
	procGetCursorPos.Call(uintptr(unsafe.Pointer(&pt)))
	procSetForeground.Call(hwnd)
	tr, _, _ := procTrackPopupMenu.Call(menu, uintptr(tpmRightAlign|tpmBottomAlign|0x0100), uintptr(pt.x), uintptr(pt.y), hwnd, 0)
	if tr != 0 {
		switch tr {
		case uintptr(wmAppToggle):
			onToggleRequest()
		case uintptr(wmAppQuit):
			removeTray()
			logLine(appDir, "quit requested")
			os.Exit(0)
		}
	}
	procDestroyMenu.Call(menu)
}

func trayLoop() {
	defer func() {
		if r := recover(); r != nil {
			logLine(appDir, fmt.Sprintf("trayLoop panic: %v", r))
		}
	}()
	logLine(appDir, "trayLoop entered")
	hInstance, _, _ := procGetModuleHandle.Call(0)
	clsName, _ := syscall.UTF16PtrFromString("SDPToggleTray")
	type wndclass struct {
		cbSize        uint32
		style         uint32
		lpfnWndProc   uintptr
		cbClsExtra    int32
		cbWndExtra    int32
		hInstance     uintptr
		hIcon         uintptr
		hCursor       uintptr
		hbrBackground uintptr
		lpszMenuName  uintptr
		lpszClassName uintptr
		hIconSm       uintptr
	}
	wc := wndclass{cbSize: uint32(unsafe.Sizeof(wndclass{})), lpfnWndProc: syscall.NewCallback(trayWndProc), hInstance: hInstance, lpszClassName: uintptr(unsafe.Pointer(clsName))}
	if ret, _, _ := procRegisterClass.Call(uintptr(unsafe.Pointer(&wc))); ret == 0 {
		logLine(appDir, "RegisterClass failed")
		return
	}
	winName, _ := syscall.UTF16PtrFromString("SDPToggleTrayWindow")
	hwnd, _, _ := procCreateWindowEx.Call(0, uintptr(unsafe.Pointer(clsName)), uintptr(unsafe.Pointer(winName)), 0, 0, 0, 0, 0, 0, 0, hInstance, 0)
	if hwnd == 0 {
		logLine(appDir, "CreateWindowEx failed")
		return
	}
	trayMu.Lock()
	trayHWND = hwnd
	trayMu.Unlock()

	h := makeHIcon(curColor)
	currentIcon = h
	if r := postTrayRet(h, curTip, nimAdd); r == 0 {
		logLine(appDir, "NIM_ADD failed")
	} else {
		logLine(appDir, "NIM_ADD ok")
	}

	type msg struct {
		hwnd    uintptr
		message uint32
		wParam  uintptr
		lParam  int32
		time    uint32
		ptX     int32
		ptY     int32
	}
	m := &msg{}
	for {
		ret, _, _ := procGetMessage.Call(uintptr(unsafe.Pointer(m)), 0, 0, 0)
		if int32(ret) <= 0 {
			break
		}
		procDispatchMsg.Call(uintptr(unsafe.Pointer(m)))
	}
	postTrayRet(0, "", nimDelete)
}

func removeTray() {
	trayMu.Lock()
	defer trayMu.Unlock()
	if trayHWND == 0 {
		return
	}
	postTrayRet(0, "", nimDelete)
	trayHWND = 0
}
