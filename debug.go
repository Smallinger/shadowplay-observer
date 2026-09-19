package main

import (
	"log"
	"os"
	"path/filepath"
	"time"

	"golang.org/x/sys/windows"
)

// -debug: ausführlichere Ausgaben, zusätzlich in eine Logdatei neben der exe.
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

// logEnvironment schreibt beim Start die Rahmenbedingungen ins Debug-Log.
func logEnvironment() {
	exe, _ := os.Executable()
	var sess uint32
	windows.ProcessIdToSessionId(uint32(os.Getpid()), &sess)
	user := os.Getenv("USERNAME")
	dbg("exe=%s args=%q pid=%d", exe, os.Args[1:], os.Getpid())
	dbg("user=%s session=%d aktive Konsolensitzung=%d", user, sess, int32(windows.WTSGetActiveConsoleSessionId()))
	dbg("System-Uptime=%s", uptime().Round(time.Second))
	dbg("Capture-Log=%s Overlay-Log=%q", captureLogPath(), overlayLogPath())
	if t, ok := overlayStart(); ok {
		dbg("NVIDIA Overlay läuft seit %s (vor %s)", t.Format("15:04:05"), time.Since(t).Round(time.Second))
	} else {
		dbg("NVIDIA Overlay läuft nicht")
	}
	s, ts, ok := readStartupStatus()
	dbg("letzter App-Status: %s (ok=%v, Zeile von %s)", s, ok, ts.Format("2006-01-02 15:04:05"))
	fs, fok := readIREnabled()
	dbg("ShareSettings.json irEnabled: %s (ok=%v, Datei=%q)", fs, fok, overlayFile("ShareSettings.json"))
	dbg("Capture-Log letzter Stand: %s, .old: %s", lastStateInFile(captureLogPath()), lastStateInFile(captureLogPath()[:len(captureLogPath())-4]+".old"))
}
