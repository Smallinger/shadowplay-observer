package main

import (
	"context"
	"encoding/binary"
	"fmt"
	"os"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
	"golang.org/x/sys/windows/registry"
)

// ---- Dienst-Seite: Helferprozess in der Benutzersitzung starten ----

// pressInUserSession startet "<exe> press" mit dem Token des angemeldeten Benutzers
// auf dessen Desktop. Ein Dienst (Session 0) kann selbst keine Tasten dorthin senden.
func pressInUserSession(ctx context.Context) error {
	exe, err := os.Executable()
	if err != nil {
		return err
	}
	sid := windows.WTSGetActiveConsoleSessionId()
	if sid == 0xFFFFFFFF {
		return fmt.Errorf("keine aktive Benutzersitzung")
	}
	dbg("Starte Helfer in Sitzung %d", sid)
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
		return fmt.Errorf("Helferprozess ohne Ergebnis (wait=%d, err=%v)", ev, err)
	}
	var code uint32
	windows.GetExitCodeProcess(pi.Process, &code)
	dbg("Helferprozess beendet, Exitcode %d", code)
	if code != 0 {
		return fmt.Errorf("Helferprozess beendet mit Code %d", code)
	}
	return nil
}

// ---- Helfer-Seite (läuft als Benutzer): Hotkey senden ----

const (
	vkAlt   = 0x12
	vkShift = 0x10
	vkF10   = 0x79
)

// irToggleHotkey liest den Videobeweis-Hotkey aus der Registry des Benutzers
// (Standard: Alt+Shift+F10).
func irToggleHotkey() []uint16 {
	def := []uint16{vkAlt, vkShift, vkF10}
	k, err := registry.OpenKey(registry.CURRENT_USER,
		`Software\NVIDIA Corporation\Global\ShadowPlay\NVSPCAPS`, registry.QUERY_VALUE)
	if err != nil {
		return def
	}
	defer k.Close()
	get := func(name string) (uint32, bool) {
		b, _, err := k.GetBinaryValue(name)
		if err != nil || len(b) < 4 {
			return 0, false
		}
		return binary.LittleEndian.Uint32(b), true
	}
	n, ok := get("IRToggleHKeyCount")
	if !ok || n == 0 || n > 6 {
		return def
	}
	var keys []uint16
	for i := range n {
		v, ok := get(fmt.Sprintf("IRToggleHKey%d", i))
		if !ok || v == 0 || v > 0xFF {
			return def
		}
		keys = append(keys, uint16(v))
	}
	return keys
}

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
	if up {
		in.flags |= keyeventKeyUp
	}
	n, _, err := procSendInput.Call(1, uintptr(unsafe.Pointer(&in)), unsafe.Sizeof(in))
	if n != 1 {
		return fmt.Errorf("SendInput: %v", err)
	}
	return nil
}

// pressHotkey drückt die Tastenkombination (Modifier zuerst) und lässt sie wieder los.
func pressHotkey() error {
	keys := irToggleHotkey()
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
