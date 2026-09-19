package main

import (
	"context"
	"runtime"
	"syscall"
	"unsafe"

	"golang.org/x/sys/windows"
)

// A minimal tray icon (plain Win32, no dependencies): left click toggles pause,
// right click opens a menu with the toggle and Exit. Only used in autostart mode.
// Green logo on dark grey = monitoring active, red struck-through logo = paused.

const (
	wmDestroy    = 0x0002
	wmClose      = 0x0010
	wmCommand    = 0x0111
	wmTimer      = 0x0113
	wmLButtonUp  = 0x0202
	wmRButtonUp  = 0x0205
	wmTrayNotify = 0x8000 + 1

	nimAdd, nimModify, nimDelete = 0, 1, 2
	nifMessage, nifIcon, nifTip  = 0x1, 0x2, 0x4

	mfString        = 0x0
	tpmRightButton  = 0x2
	tpmBottomAlign  = 0x20
	idToggle, idEnd = 1, 2
)

var (
	shell32 = windows.NewLazySystemDLL("shell32.dll")
	gdi32   = windows.NewLazySystemDLL("gdi32.dll")

	pRegisterClassEx  = user32.NewProc("RegisterClassExW")
	pCreateWindowEx   = user32.NewProc("CreateWindowExW")
	pDefWindowProc    = user32.NewProc("DefWindowProcW")
	pGetMessage       = user32.NewProc("GetMessageW")
	pTranslateMessage = user32.NewProc("TranslateMessage")
	pDispatchMessage  = user32.NewProc("DispatchMessageW")
	pPostQuitMessage  = user32.NewProc("PostQuitMessage")
	pPostMessage      = user32.NewProc("PostMessageW")
	pSetTimer         = user32.NewProc("SetTimer")
	pCreatePopupMenu  = user32.NewProc("CreatePopupMenu")
	pAppendMenu       = user32.NewProc("AppendMenuW")
	pTrackPopupMenu   = user32.NewProc("TrackPopupMenu")
	pDestroyMenu      = user32.NewProc("DestroyMenu")
	pGetCursorPos     = user32.NewProc("GetCursorPos")
	pSetForeground    = user32.NewProc("SetForegroundWindow")
	pRegisterWinMsg   = user32.NewProc("RegisterWindowMessageW")
	pCreateIconInd    = user32.NewProc("CreateIconIndirect")
	pDestroyIcon      = user32.NewProc("DestroyIcon")
	pShellNotifyIcon  = shell32.NewProc("Shell_NotifyIconW")
	pCreateBitmap     = gdi32.NewProc("CreateBitmap")
	pDeleteObject     = gdi32.NewProc("DeleteObject")
	pGetSystemMetrics = user32.NewProc("GetSystemMetrics")
)

type wndClassEx struct {
	size       uint32
	style      uint32
	wndProc    uintptr
	clsExtra   int32
	wndExtra   int32
	instance   uintptr
	icon       uintptr
	cursor     uintptr
	background uintptr
	menuName   *uint16
	className  *uint16
	iconSm     uintptr
}

type notifyIconData struct {
	size            uint32
	hwnd            uintptr
	id              uint32
	flags           uint32
	callbackMessage uint32
	icon            uintptr
	tip             [128]uint16
	state           uint32
	stateMask       uint32
	info            [256]uint16
	timeout         uint32
	infoTitle       [64]uint16
	infoFlags       uint32
	guid            [16]byte
	balloonIcon     uintptr
}

type winMsg struct {
	hwnd    uintptr
	message uint32
	wParam  uintptr
	lParam  uintptr
	time    uint32
	ptX     int32
	ptY     int32
	_       uint32
}

type iconInfo struct {
	isIcon   int32
	xHotspot uint32
	yHotspot uint32
	mask     uintptr
	color    uintptr
}

var tray struct {
	hwnd        uintptr
	iconActive  uintptr
	iconPaused  uintptr
	taskbarMsg  uint32
	lastPaused  bool
	lastIR      irState
	added       bool
	cancel      context.CancelFunc
	initialized bool
}

// iconSize is the small icon size of the current DPI (16 at 100%, 24 at 150%, ...).
func iconSize() int {
	r, _, _ := pGetSystemMetrics.Call(49) // SM_CXSMICON
	if r < 16 {
		return 16
	}
	return int(r)
}

// makeIcon renders the glyph in fg on the dark grey tile (see icon.go) and turns it into an HICON.
func makeIcon(g glyph, fg [3]byte) uintptr {
	n := iconSize()
	pix := renderIcon(n, g, fg, trayFill)
	color, _, _ := pCreateBitmap.Call(uintptr(n), uintptr(n), 1, 32, uintptr(unsafe.Pointer(&pix[0])))
	maskBits := make([]byte, ((n+15)/16*2)*n) // 1 bpp rows are 16-bit aligned; all zero = opaque
	mask, _, _ := pCreateBitmap.Call(uintptr(n), uintptr(n), 1, 1, uintptr(unsafe.Pointer(&maskBits[0])))
	ii := iconInfo{isIcon: 1, mask: mask, color: color}
	h, _, _ := pCreateIconInd.Call(uintptr(unsafe.Pointer(&ii)))
	pDeleteObject.Call(color)
	pDeleteObject.Call(mask)
	return h
}

func utf16Copy(dst []uint16, s string) {
	u, _ := windows.UTF16FromString(s)
	if len(u) > len(dst) {
		u = append(u[:len(dst)-1], 0)
	}
	copy(dst, u)
}

func trayTip(paused bool, ir irState) string {
	s := "Shadowplay Observer: active"
	if paused {
		s = "Shadowplay Observer: paused"
	}
	return s + " (Instant Replay " + ir.String() + ")"
}

// updateTray adds the icon or refreshes it when the pause / Instant Replay state changed.
func updateTray(force bool) {
	paused := isPaused()
	ir, _ := readIREnabled()
	if tray.added && !force && paused == tray.lastPaused && ir == tray.lastIR {
		return
	}
	nid := notifyIconData{hwnd: tray.hwnd, id: 1, flags: nifMessage | nifIcon | nifTip, callbackMessage: wmTrayNotify}
	nid.size = uint32(unsafe.Sizeof(nid))
	nid.icon = tray.iconActive
	if paused {
		nid.icon = tray.iconPaused
	}
	utf16Copy(nid.tip[:], trayTip(paused, ir))
	op := uintptr(nimModify)
	if !tray.added || force {
		op = nimAdd
	}
	r, _, e := pShellNotifyIcon.Call(op, uintptr(unsafe.Pointer(&nid)))
	dbg("tray: Shell_NotifyIcon op=%d result=%d err=%v icon=%d size=%d", op, r, e, nid.icon, nid.size)
	if r != 0 {
		tray.added = true
	}
	tray.lastPaused, tray.lastIR = paused, ir
}

func toggleTrayPause() {
	if isPaused() {
		clearPause()
	} else {
		setPause(0)
	}
	updateTray(false)
}

func showTrayMenu(hwnd uintptr) {
	menu, _, _ := pCreatePopupMenu.Call()
	defer pDestroyMenu.Call(menu)
	label := "Pause monitoring"
	if isPaused() {
		label = "Resume monitoring"
	}
	l1, _ := windows.UTF16PtrFromString(label)
	l2, _ := windows.UTF16PtrFromString("Exit")
	pAppendMenu.Call(menu, mfString, idToggle, uintptr(unsafe.Pointer(l1)))
	pAppendMenu.Call(menu, mfString, idEnd, uintptr(unsafe.Pointer(l2)))
	var pt struct{ x, y int32 }
	pGetCursorPos.Call(uintptr(unsafe.Pointer(&pt)))
	pSetForeground.Call(hwnd) // required so the menu closes when clicking elsewhere
	pTrackPopupMenu.Call(menu, tpmRightButton|tpmBottomAlign, uintptr(pt.x), uintptr(pt.y), 0, hwnd, 0)
	pPostMessage.Call(hwnd, 0, 0, 0)
}

func trayWndProc(hwnd, msg, wParam, lParam uintptr) uintptr {
	switch uint32(msg) {
	case wmTrayNotify:
		switch uint32(lParam) & 0xFFFF {
		case wmLButtonUp:
			toggleTrayPause()
		case wmRButtonUp:
			showTrayMenu(hwnd)
		}
		return 0
	case wmCommand:
		switch wParam & 0xFFFF {
		case idToggle:
			toggleTrayPause()
		case idEnd:
			pPostMessage.Call(hwnd, wmClose, 0, 0)
		}
		return 0
	case wmTimer:
		updateTray(false)
		return 0
	case wmDestroy:
		nid := notifyIconData{hwnd: hwnd, id: 1}
		nid.size = uint32(unsafe.Sizeof(nid))
		pShellNotifyIcon.Call(nimDelete, uintptr(unsafe.Pointer(&nid)))
		pPostQuitMessage.Call(0)
		return 0
	}
	if tray.taskbarMsg != 0 && uint32(msg) == tray.taskbarMsg { // explorer restarted
		updateTray(true)
		return 0
	}
	r, _, _ := pDefWindowProc.Call(hwnd, msg, wParam, lParam)
	return r
}

// runTray shows the tray icon and runs the message loop until Exit is chosen or ctx ends.
// It blocks; call it on the main goroutine. cancel is called when the user picks Exit.
func runTray(ctx context.Context, cancel context.CancelFunc) {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	tray.cancel = cancel

	inst, _, _ := windows.NewLazySystemDLL("kernel32.dll").NewProc("GetModuleHandleW").Call(0)
	className, _ := windows.UTF16PtrFromString("ShadowplayObserverTray")
	wc := wndClassEx{instance: uintptr(inst), className: className, wndProc: syscall.NewCallback(trayWndProc)}
	wc.size = uint32(unsafe.Sizeof(wc))
	atom, _, e1 := pRegisterClassEx.Call(uintptr(unsafe.Pointer(&wc)))
	dbg("tray: RegisterClassEx atom=%d err=%v", atom, e1)
	tm, _ := windows.UTF16PtrFromString("TaskbarCreated")
	r, _, _ := pRegisterWinMsg.Call(uintptr(unsafe.Pointer(tm)))
	tray.taskbarMsg = uint32(r)

	tray.iconActive = makeIcon(glyphActive, colorActive)
	tray.iconPaused = makeIcon(glyphPaused, colorPaused)
	defer pDestroyIcon.Call(tray.iconActive)
	defer pDestroyIcon.Call(tray.iconPaused)

	title, _ := windows.UTF16PtrFromString("Shadowplay Observer")
	var cwErr error
	tray.hwnd, _, cwErr = pCreateWindowEx.Call(0x80, uintptr(unsafe.Pointer(className)), uintptr(unsafe.Pointer(title)),
		0, 0, 0, 0, 0, 0, 0, uintptr(inst), 0)
	dbg("tray: hwnd=%d err=%v", tray.hwnd, cwErr)
	if tray.hwnd == 0 {
		<-ctx.Done() // no tray possible, keep monitoring anyway
		return
	}
	updateTray(true)
	pSetTimer.Call(tray.hwnd, 1, 1000, 0)

	go func() {
		<-ctx.Done()
		pPostMessage.Call(tray.hwnd, wmClose, 0, 0)
	}()

	var m winMsg
	for {
		r, _, _ := pGetMessage.Call(uintptr(unsafe.Pointer(&m)), 0, 0, 0)
		if int32(r) <= 0 {
			break
		}
		pTranslateMessage.Call(uintptr(unsafe.Pointer(&m)))
		pDispatchMessage.Call(uintptr(unsafe.Pointer(&m)))
	}
	cancel()
}
