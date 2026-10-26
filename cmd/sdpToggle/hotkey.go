//go:build windows

package main

import (
	"fmt"
	"syscall"
	"unsafe"
)

const (
	wmHotkey = 0x0312
	modShift = 0x0004
	vkW      = 0x57
	hotkeyID = 1
)

var (
	user32           = syscall.NewLazyDLL("user32.dll")
	procRegisterHK   = user32.NewProc("RegisterHotKey")
	procUnregisterHK = user32.NewProc("UnregisterHotKey")
	procGetMessageW  = user32.NewProc("GetMessageW")
)

type msg struct {
	hwnd    uintptr
	message uint32
	wParam  uintptr
	lParam  int32
	time    uint32
	ptX     int32
	ptY     int32
}

// hotkeyLoop registers Shift+W and dispatches fn on each press.
func hotkeyLoop(fn func()) error {
	r, _, err := procRegisterHK.Call(0, hotkeyID, modShift, vkW)
	if r == 0 {
		return fmt.Errorf("Shift+W register failed: %v", err)
	}
	m := &msg{}
	for {
		ret, _, _ := procGetMessageW.Call(uintptr(unsafe.Pointer(m)), 0, 0, 0)
		if int32(ret) <= 0 {
			break
		}
		if m.message == wmHotkey && m.wParam == hotkeyID {
			fn()
		}
	}
	procUnregisterHK.Call(0, hotkeyID)
	return nil
}
