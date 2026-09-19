package main

import (
	"context"
	"fmt"
	"os"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

// ---- Service side: start the helper process in the user session ----

// pressInUserSession starts "<exe> press" with the logged-in user's token
// on their desktop. A service (session 0) cannot send keystrokes there itself.
func pressInUserSession(ctx context.Context) error {
	exe, err := os.Executable()
	if err != nil {
		return err
	}
	// Resolve here as well so a removed shortcut is reported clearly instead of as a helper failure.
	if _, _, err := resolveHotkey(); err != nil {
		return err
	}
	sid := windows.WTSGetActiveConsoleSessionId()
	if sid == 0xFFFFFFFF {
		return fmt.Errorf("no active user session")
	}
	dbg("Starting helper in session %d", sid)
	var userTok windows.Token
	if err := windows.WTSQueryUserToken(sid, &userTok); err != nil {
		return fmt.Errorf("WTSQueryUserToken: %w", err)
	}
	defer userTok.Close()
	var tok windows.Token
	if err := windows.DuplicateTokenEx(userTok, windows.MAXIMUM_ALLOWED, nil,
		windows.SecurityIdentification, windows.TokenPrimary, &tok); err != nil {
		return fmt.Errorf("DuplicateTokenEx: %w", err)
	}
	defer tok.Close()
	var env *uint16
	if err := windows.CreateEnvironmentBlock(&env, tok, false); err != nil {
		return fmt.Errorf("CreateEnvironmentBlock: %w", err)
	}
	defer windows.DestroyEnvironmentBlock(env)

	cmd, _ := windows.UTF16PtrFromString(`"` + exe + `" press`)
	desktop, _ := windows.UTF16PtrFromString(`winsta0\default`)
	si := windows.StartupInfo{Cb: uint32(unsafe.Sizeof(windows.StartupInfo{})), Desktop: desktop}
	var pi windows.ProcessInformation
	if err := windows.CreateProcessAsUser(tok, nil, cmd, nil, nil, false,
		windows.CREATE_UNICODE_ENVIRONMENT|windows.CREATE_NO_WINDOW,
		env, nil, &si, &pi); err != nil {
		return fmt.Errorf("CreateProcessAsUser: %w", err)
	}
	defer windows.CloseHandle(pi.Process)
	defer windows.CloseHandle(pi.Thread)

	ev, err := windows.WaitForSingleObject(pi.Process, uint32((15 * time.Second).Milliseconds()))
	if err != nil || ev != windows.WAIT_OBJECT_0 {
		windows.TerminateProcess(pi.Process, 1)
		return fmt.Errorf("helper process gave no result (wait=%d, err=%v)", ev, err)
	}
	var code uint32
	windows.GetExitCodeProcess(pi.Process, &code)
	dbg("Helper process finished, exit code %d", code)
	if code != 0 {
		return fmt.Errorf("helper process exited with code %d", code)
	}
	return nil
}

// ---- Helper side (runs as the user): send the hotkey ----

type keyInput struct {
	typ   uint32
	_     uint32
	vk    uint16
	scan  uint16
	flags uint32
	time  uint32
	extra uintptr
	_     [8]byte
}

const (
	inputKeyboard    = 1
	keyeventExtended = 0x1
	keyeventKeyUp    = 0x2
	keyeventScancode = 0x8
)

var (
	user32        = windows.NewLazySystemDLL("user32.dll")
	procSendInput = user32.NewProc("SendInput")
	procMapVK     = user32.NewProc("MapVirtualKeyW")
)

func sendKey(vk uint16, up bool) error {
	scan, _, _ := procMapVK.Call(uintptr(vk), 0) // MAPVK_VK_TO_VSC
	in := keyInput{typ: inputKeyboard, vk: vk, scan: uint16(scan)}
	if isExtendedKey(vk) {
		in.flags |= keyeventExtended
	}
	if up {
		in.flags |= keyeventKeyUp
	}
	n, _, err := procSendInput.Call(1, uintptr(unsafe.Pointer(&in)), unsafe.Sizeof(in))
	if n != 1 {
		return fmt.Errorf("SendInput: %v", err)
	}
	return nil
}

// pressHotkey presses the key combination (modifiers first) and releases it again.
func pressHotkey() error {
	keys, source, err := resolveHotkey()
	if err != nil {
		return err
	}
	dbg("Pressing %s (from %s)", formatHotkey(keys), source)
	for _, k := range keys {
		if err := sendKey(k, false); err != nil {
			return err
		}
		time.Sleep(30 * time.Millisecond)
	}
	time.Sleep(80 * time.Millisecond)
	for i := len(keys) - 1; i >= 0; i-- {
		if err := sendKey(keys[i], true); err != nil {
			return err
		}
		time.Sleep(30 * time.Millisecond)
	}
	return nil
}
