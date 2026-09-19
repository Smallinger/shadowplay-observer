package main

import (
	"bytes"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

// On startup the NVIDIA app (NVIDIA Overlay.exe) queries the real Instant Replay status
// and writes it to its console.log:
//
//	2026-09-19 02:37:56.775 INFO  ShadowPlayService  GetInstantReplayEnableStatus returns:  {
//		"status": true,

const overlayExe = "NVIDIA Overlay.exe"

// overlayStart returns the start time of the oldest running NVIDIA Overlay instance.
func overlayStart() (time.Time, bool) {
	snap, err := windows.CreateToolhelp32Snapshot(windows.TH32CS_SNAPPROCESS, 0)
	if err != nil {
		return time.Time{}, false
	}
	defer windows.CloseHandle(snap)
	pe := windows.ProcessEntry32{Size: uint32(unsafe.Sizeof(windows.ProcessEntry32{}))}
	var oldest time.Time
	found := false
	for err = windows.Process32First(snap, &pe); err == nil; err = windows.Process32Next(snap, &pe) {
		if !strings.EqualFold(windows.UTF16ToString(pe.ExeFile[:]), overlayExe) {
			continue
		}
		h, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION, false, pe.ProcessID)
		if err != nil {
			continue
		}
		var c, e, k, u windows.Filetime
		if windows.GetProcessTimes(h, &c, &e, &k, &u) == nil {
			t := time.Unix(0, c.Nanoseconds())
			if !found || t.Before(oldest) {
				oldest, found = t, true
			}
		}
		windows.CloseHandle(h)
	}
	return oldest, found
}

// overlayLogPath finds the NVIDIA Overlay console.log in the user's profile
// (the service runs as SYSTEM, so search all profiles; the most recent wins).
func overlayLogPath() string { return overlayFile("console.log") }

// overlayFile finds a file in the user's NVIDIA Overlay folder.
func overlayFile(name string) string {
	var pats []string
	if la := os.Getenv("LOCALAPPDATA"); la != "" {
		pats = append(pats, filepath.Join(la, "NVIDIA Corporation", "NVIDIA Overlay", name))
	}
	drive := os.Getenv("SystemDrive")
	if drive == "" {
		drive = "C:"
	}
	glob, _ := filepath.Glob(filepath.Join(drive+`\`, "Users", "*", "AppData", "Local", "NVIDIA Corporation", "NVIDIA Overlay", name))
	pats = append(pats, glob...)
	best, bestT := "", time.Time{}
	for _, p := range pats {
		if fi, err := os.Stat(p); err == nil && fi.ModTime().After(bestT) {
			best, bestT = p, fi.ModTime()
		}
	}
	return best
}

var statusRe = regexp.MustCompile(`"status":\s*(true|false)`)

// readStartupStatus returns the last Instant Replay status reported by the NVIDIA app
// together with the line's timestamp.
func readStartupStatus() (irState, time.Time, bool) {
	path := overlayLogPath()
	if path == "" {
		return stateUnknown, time.Time{}, false
	}
	f, err := os.Open(path)
	if err != nil {
		return stateUnknown, time.Time{}, false
	}
	defer f.Close()
	fi, err := f.Stat()
	if err != nil {
		return stateUnknown, time.Time{}, false
	}
	const window = 4 << 20
	off := max(fi.Size()-window, 0)
	buf := make([]byte, fi.Size()-off)
	if _, err := f.ReadAt(buf, off); err != nil && err != io.EOF {
		return stateUnknown, time.Time{}, false
	}
	lines := bytes.Split(buf, []byte("\n"))
	res, ts, ok := stateUnknown, time.Time{}, false
	for i, l := range lines {
		if !bytes.Contains(l, []byte("GetInstantReplayEnableStatus returns")) || len(l) < 23 || i+1 >= len(lines) {
			continue
		}
		m := statusRe.FindSubmatch(lines[i+1])
		if m == nil {
			continue
		}
		t, err := time.ParseInLocation("2006-01-02 15:04:05.000", string(l[:23]), time.Local)
		if err != nil {
			continue
		}
		if string(m[1]) == "true" {
			res = stateOn
		} else {
			res = stateOff
		}
		ts, ok = t, true
	}
	return res, ts, ok
}

var irEnabledRe = regexp.MustCompile(`"irEnabled"\s*:\s*(true|false)`)

// readIREnabled reads the persisted Instant Replay switch from ShareSettings.json
// (video.irEnabled). NVIDIA writes the file immediately on every toggle.
func readIREnabled() (irState, bool) {
	path := overlayFile("ShareSettings.json")
	if path == "" {
		return stateUnknown, false
	}
	b, err := os.ReadFile(path)
	if err != nil {
		return stateUnknown, false
	}
	m := irEnabledRe.FindSubmatch(b)
	if m == nil {
		return stateUnknown, false
	}
	if string(m[1]) == "true" {
		return stateOn, true
	}
	return stateOff, true
}
