package main

// Pure control-flow extracted from Win32 callbacks so it can be unit-tested.
// The Win32 layer translates OS events into these semantic actions.

// action represents a semantic command from any input source
// (hotkey, tray menu, double-click).
type action int

const (
	actionNone action = iota
	actionToggle // switch intranet/internet
	actionQuit   // exit the tray process
	actionShowMenu
)

// mapMenuSelection converts a TrackPopupMenu selection ID to an action.
// With TPM_RETURNCMD, TrackPopupMenu returns the item ID directly (0 = dismissed).
func mapMenuSelection(sel uintptr) action {
	switch sel {
	case uintptr(wmAppToggle):
		return actionToggle
	case uintptr(wmAppQuit):
		return actionQuit
	default:
		return actionNone
	}
}

// mapCommand converts a WM_COMMAND wParam (menu item ID in low word) to an action.
func mapCommand(wParam uintptr) action {
	return mapMenuSelection(wParam & 0xffff)
}

// mapTrayMouseEvent converts a tray callback lParam (mouse message) to an action.
func mapTrayMouseEvent(lParam int32) action {
	switch lParam {
	case 0x0205, 0x0204: // WM_RBUTTONUP / WM_RBUTTONDOWN
		return actionShowMenu
	case 0x0203: // WM_LBUTTONDBLCLK
		return actionToggle
	default:
		return actionNone
	}
}

// dispatchAction routes an action to its handler. Returns true if the process
// should exit (quit requested).
func dispatchAction(a action) bool {
	switch a {
	case actionToggle:
		onToggleRequest()
		return false
	case actionQuit:
		removeTray()
		logLine(appDir, "quit requested")
		return true
	case actionShowMenu:
		// handled by the Win32 layer (needs hwnd); nothing here
		return false
	}
	return false
}

