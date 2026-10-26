package main

import (
	"sync/atomic"
	"testing"
)

func TestMapMenuSelection(t *testing.T) {
	cases := []struct {
		name string
		sel  uintptr
		want action
	}{
		{"toggle item", uintptr(wmAppToggle), actionToggle},
		{"quit item", uintptr(wmAppQuit), actionQuit},
		{"dismissed menu", 0, actionNone},
		{"unknown id", 99999, actionNone},
	}
	for _, c := range cases {
		if got := mapMenuSelection(c.sel); got != c.want {
			t.Errorf("%s: mapMenuSelection(%d)=%v want %v", c.name, c.sel, got, c.want)
		}
	}
}

func TestMapCommand(t *testing.T) {
	// WM_COMMAND packs menu ID in low word; high word holds notifications.
	if got := mapCommand(uintptr(wmAppQuit) | (0 << 16)); got != actionQuit {
		t.Errorf("mapCommand quit = %v want actionQuit", got)
	}
	if got := mapCommand(uintptr(wmAppToggle) | (0 << 16)); got != actionToggle {
		t.Errorf("mapCommand toggle = %v want actionToggle", got)
	}
	// high word set (e.g. accelerator flags) must be masked off
	if got := mapCommand(uintptr(wmAppQuit) | (1 << 16)); got != actionQuit {
		t.Errorf("mapCommand should mask high word, got %v", got)
	}
}

func TestMapTrayMouseEvent(t *testing.T) {
	cases := []struct {
		name string
		lp   int32
		want action
	}{
		{"right up", 0x0205, actionShowMenu},
		{"right down", 0x0204, actionShowMenu},
		{"left double click", 0x0203, actionToggle},
		{"left up", 0x0201, actionNone},
		{"mouse move", 0x0200, actionNone},
	}
	for _, c := range cases {
		if got := mapTrayMouseEvent(c.lp); got != c.want {
			t.Errorf("%s: mapTrayMouseEvent(%#x)=%v want %v", c.name, c.lp, got, c.want)
		}
	}
}

func TestDispatchQuitSignalsExit(t *testing.T) {
	// Quit must signal process exit; verify via dispatchAction return value.
	// removeTray with trayHWND=0 is a safe no-op in tests.
	if !dispatchAction(actionQuit) {
		t.Fatal("actionQuit must return true (process should exit)")
	}
}

func TestDispatchToggleCallsRequest(t *testing.T) {
	var called int32
	orig := onToggleRequest
	onToggleRequest = func() { atomic.AddInt32(&called, 1) }
	defer func() { onToggleRequest = orig }()

	if dispatchAction(actionToggle) {
		t.Fatal("actionToggle must not exit the process")
	}
	if atomic.LoadInt32(&called) != 1 {
		t.Fatalf("onToggleRequest called %d times, want 1", called)
	}
}

func TestDispatchShowMenuAndNone(t *testing.T) {
	if dispatchAction(actionShowMenu) {
		t.Fatal("actionShowMenu must not exit")
	}
	if dispatchAction(actionNone) {
		t.Fatal("actionNone must not exit")
	}
}
