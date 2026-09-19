package main

import (
	"log"
	"os"
	"path/filepath"
	"time"

	"golang.org/x/sys/windows"
)

// -debug: verbose output, additionally written to a log file next to the exe.
var (
	debugOn  bool
	debugOut *log.Logger
)

func dbg(format string, a ...any) {
	if debugOn && debugOut != nil {
		debugOut.Printf("DEBUG "+format, a...)
	}
}

func exeDir() string {
	exe, err := os.Executable()
	if err != nil {
		return "."
	}
	return filepath.Dir(exe)
}

func debugLogPath() string { return filepath.Join(exeDir(), "shadowplay-observer.log") }

var procGetTickCount64 = windows.NewLazySystemDLL("kernel32.dll").NewProc("GetTickCount64")

func uptime() time.Duration {
	ms, _, _ := procGetTickCount64.Call()
	return time.Duration(ms) * time.Millisecond
}

// logEnvironment writes the runtime environment to the debug log at startup.
func logEnvironment() {
	exe, _ := os.Executable()
	var sess uint32
	windows.ProcessIdToSessionId(uint32(os.Getpid()), &sess)
	user := os.Getenv("USERNAME")
	dbg("exe=%s args=%q pid=%d", exe, os.Args[1:], os.Getpid())
	dbg("user=%s session=%d active console session=%d", user, sess, int32(windows.WTSGetActiveConsoleSessionId()))
	dbg("System uptime=%s", uptime().Round(time.Second))
	dbg("Capture log=%s overlay log=%q", captureLogPath(), overlayLogPath())
	if t, ok := overlayStart(); ok {
		dbg("NVIDIA Overlay running since %s (%s ago)", t.Format("15:04:05"), time.Since(t).Round(time.Second))
	} else {
		dbg("NVIDIA Overlay not running")
	}
	s, ts, ok := readStartupStatus()
	dbg("last app status: %s (ok=%v, line from %s)", s, ok, ts.Format("2006-01-02 15:04:05"))
	fs, fok := readIREnabled()
	dbg("ShareSettings.json irEnabled: %s (ok=%v, file=%q)", fs, fok, overlayFile("ShareSettings.json"))
	dbg("NVIDIA ShadowPlay: %s", nvReadyDescription())
	if keys, src, err := resolveHotkey(); err != nil {
		dbg("hotkey: %v", err)
	} else {
		dbg("hotkey: %s (from %s)", formatHotkey(keys), src)
	}
	dbg("Capture log last state: %s, .old: %s", lastStateInFile(captureLogPath()), lastStateInFile(captureLogPath()[:len(captureLogPath())-4]+".old"))
}
