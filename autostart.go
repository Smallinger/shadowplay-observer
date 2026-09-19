package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
	"golang.org/x/sys/windows/registry"
)

// Autostart mode: the exe runs in the user's session, started at logon through the
// HKCU "Run" key. No admin rights needed. It sends the hotkey itself, no helper process.

const runKeyPath = `Software\Microsoft\Windows\CurrentVersion\Run`

// backgroundArgs returns the arguments of a background instance.
func backgroundArgs(debug bool) []string {
	args := []string{"run", "--background"}
	if debug {
		args = append(args, "-debug")
	}
	return args
}

// autostartCommand is the Run key value. conhost --headless starts the console exe
// without showing any window.
func autostartCommand(debug bool) string {
	exe, _ := os.Executable()
	return `conhost.exe --headless "` + exe + `" ` + strings.Join(backgroundArgs(debug), " ")
}

func autostartRegistered() bool {
	k, err := registry.OpenKey(registry.CURRENT_USER, runKeyPath, registry.QUERY_VALUE)
	if err != nil {
		return false
	}
	defer k.Close()
	_, _, err = k.GetStringValue(svcName)
	return err == nil
}

func installAutostart(debug bool) error {
	exe, err := os.Executable()
	if err != nil {
		return err
	}
	k, _, err := registry.CreateKey(registry.CURRENT_USER, runKeyPath, registry.SET_VALUE)
	if err != nil {
		return err
	}
	defer k.Close()
	if err := k.SetStringValue(svcName, autostartCommand(debug)); err != nil {
		return err
	}
	// Start it right away so a logoff is not needed.
	cmd := exec.Command("conhost.exe", append([]string{"--headless", exe}, backgroundArgs(debug)...)...)
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true, CreationFlags: windows.CREATE_NO_WINDOW}
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("registered, but start failed: %w", err)
	}
	return cmd.Process.Release()
}

func uninstallAutostart() error {
	k, err := registry.OpenKey(registry.CURRENT_USER, runKeyPath, registry.SET_VALUE)
	if err != nil {
		return err
	}
	defer k.Close()
	err = k.DeleteValue(svcName)
	if errors.Is(err, registry.ErrNotExist) {
		err = errors.New("autostart is not registered")
	}
	stopBackgroundInstances()
	return err
}

// backgroundPIDs lists other instances of this exe in user sessions (not the service,
// which runs in session 0).
func backgroundPIDs() []uint32 {
	snap, err := windows.CreateToolhelp32Snapshot(windows.TH32CS_SNAPPROCESS, 0)
	if err != nil {
		return nil
	}
	defer windows.CloseHandle(snap)
	self, _ := os.Executable()
	name := filepath.Base(self)
	pe := windows.ProcessEntry32{Size: uint32(unsafe.Sizeof(windows.ProcessEntry32{}))}
	var pids []uint32
	for err = windows.Process32First(snap, &pe); err == nil; err = windows.Process32Next(snap, &pe) {
		if pe.ProcessID == uint32(os.Getpid()) || !strings.EqualFold(windows.UTF16ToString(pe.ExeFile[:]), name) {
			continue
		}
		var sess uint32
		if windows.ProcessIdToSessionId(pe.ProcessID, &sess) == nil && sess != 0 {
			pids = append(pids, pe.ProcessID)
		}
	}
	return pids
}

func stopBackgroundInstances() {
	for _, pid := range backgroundPIDs() {
		h, err := windows.OpenProcess(windows.PROCESS_TERMINATE, false, pid)
		if err != nil {
			continue
		}
		windows.TerminateProcess(h, 0)
		windows.CloseHandle(h)
	}
}

func autostartStatus() string {
	s := "not registered"
	if autostartRegistered() {
		s = "registered"
	}
	if len(backgroundPIDs()) > 0 {
		s += ", running"
	} else {
		s += ", not running"
	}
	return s
}

// ---- Single instance: service and autostart must never run at the same time,
// otherwise both would send the toggle hotkey and cancel each other out. ----

var instanceMutex windows.Handle

// acquireInstance returns false if another instance already holds the lock.
func acquireInstance() bool {
	name, _ := windows.UTF16PtrFromString(`Global\ShadowplayObserverInstance`)
	h, err := windows.CreateMutex(nil, false, name)
	switch {
	case err == nil:
		instanceMutex = h
		return true
	case errors.Is(err, windows.ERROR_ALREADY_EXISTS):
		windows.CloseHandle(h)
		return false
	case errors.Is(err, windows.ERROR_ACCESS_DENIED):
		return false // created by the service (SYSTEM), we may not open it
	}
	return true // cannot tell, better run than not
}

func releaseInstance() {
	if instanceMutex != 0 {
		windows.CloseHandle(instanceMutex)
		instanceMutex = 0
	}
}

// waitForInstance blocks until the lock is ours or ctx ends. Used by the service so it
// takes over when an autostart instance goes away.
func waitForInstance(ctx context.Context, logf func(string, ...any)) bool {
	warned := false
	for {
		if acquireInstance() {
			return true
		}
		if !warned {
			logf("Another instance is running (autostart?), service is idle until it ends")
			warned = true
		}
		select {
		case <-ctx.Done():
			return false
		case <-time.After(30 * time.Second):
		}
	}
}
