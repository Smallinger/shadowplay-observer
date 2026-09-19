package main

import (
	"os"
	"path/filepath"
	"strings"
	"time"
)

// Pausing works through a small file in the user's local app data:
//
//	%LOCALAPPDATA%\ShadowplayObserver\paused
//
// It contains an RFC 3339 expiry time, or "indefinite". The service runs as SYSTEM, so it
// looks for the file in every user profile. The CLI and the tray icon write it.

const pauseFileName = "paused"

func pauseFileWritePath() string {
	return filepath.Join(userDataDir(), pauseFileName)
}

// pauseFileCandidates lists every place a pause file may be (own profile + all profiles).
func pauseFileCandidates() []string {
	cands := []string{pauseFileWritePath()}
	drive := os.Getenv("SystemDrive")
	if drive == "" {
		drive = "C:"
	}
	glob, _ := filepath.Glob(filepath.Join(drive+`\`, "Users", "*", "AppData", "Local", "ShadowplayObserver", pauseFileName))
	return append(cands, glob...)
}

// pauseState reports whether monitoring is paused and until when (zero = until resumed).
func pauseState() (paused bool, until time.Time) {
	for _, p := range pauseFileCandidates() {
		b, err := os.ReadFile(p)
		if err != nil {
			continue
		}
		var exp time.Time
		if t, err := time.Parse(time.RFC3339, strings.TrimSpace(string(b))); err == nil {
			exp = t
			if time.Now().After(exp) { // expired: clean up and ignore
				os.Remove(p)
				continue
			}
		}
		return true, exp
	}
	return false, time.Time{}
}

func isPaused() bool {
	p, _ := pauseState()
	return p
}

// setPause pauses monitoring for d, or until resume if d is 0.
func setPause(d time.Duration) error {
	if err := os.MkdirAll(userDataDir(), 0o755); err != nil {
		return err
	}
	content := "indefinite"
	if d > 0 {
		content = time.Now().Add(d).Format(time.RFC3339)
	}
	return os.WriteFile(pauseFileWritePath(), []byte(content), 0o644)
}

// clearPause resumes monitoring (removes the pause file wherever it is).
func clearPause() error {
	var firstErr error
	for _, p := range pauseFileCandidates() {
		if err := os.Remove(p); err != nil && !os.IsNotExist(err) && firstErr == nil {
			firstErr = err
		}
	}
	return firstErr
}

func pauseDescription() string {
	p, until := pauseState()
	switch {
	case !p:
		return "monitoring active"
	case until.IsZero():
		return "PAUSED until resumed"
	default:
		return "PAUSED until " + until.Format("15:04:05")
	}
}
