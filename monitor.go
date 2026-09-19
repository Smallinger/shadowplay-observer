package main

import (
	"bytes"
	"context"
	"io"
	"log"
	"os"
	"path/filepath"
	"regexp"
	"time"
)

// Instant Replay (IR) in the NVIDIA app. Its state is written to
// CaptureCore.log, e.g.
//
//	CCaptureSession::EvaluateDVRStop: IR(0) HL(0) DVR(0)              -> off
//	CCaptureSession::EvaluateAutoDVRStart: Starting DVR capture. IR(1) -> on
//	DVR Active = true                                                  -> on
type irState int

const (
	stateUnknown irState = iota
	stateOn
	stateOff
)

func (s irState) String() string {
	switch s {
	case stateOn:
		return "on"
	case stateOff:
		return "off"
	}
	return "unknown"
}

var irRe = regexp.MustCompile(`(?:EvaluateDVRStop|EvaluateAutoDVRStart)\b.*\bIR\((\d)\)`)

func parseState(line []byte) irState {
	if m := irRe.FindSubmatch(line); m != nil {
		if m[1][0] == '0' {
			return stateOff
		}
		return stateOn
	}
	if bytes.Contains(line, []byte("DVR Active = true")) {
		return stateOn
	}
	return stateUnknown
}

func captureLogPath() string {
	pd := os.Getenv("ProgramData")
	if pd == "" {
		pd = `C:\ProgramData`
	}
	return filepath.Join(pd, "NVIDIA Corporation", "ShadowPlay", "CaptureCore.log")
}

// lastStateInFile returns the last recognizable state within the last 1 MB of the file.
func lastStateInFile(path string) irState {
	f, err := os.Open(path)
	if err != nil {
		return stateUnknown
	}
	defer f.Close()
	fi, err := f.Stat()
	if err != nil {
		return stateUnknown
	}
	const window = 1 << 20
	off := max(fi.Size()-window, 0)
	buf := make([]byte, fi.Size()-off)
	if _, err := f.ReadAt(buf, off); err != nil && err != io.EOF {
		return stateUnknown
	}
	res := stateUnknown
	for _, line := range bytes.Split(buf, []byte("\n")) {
		if s := parseState(line); s != stateUnknown {
			res = s
		}
	}
	return res
}

// tailer reads new lines from the log and detects rotation (file replaced/truncated).
type tailer struct {
	path string
	pos  int64
	id   os.FileInfo
}

func newTailer(path string) *tailer {
	t := &tailer{path: path}
	if f, err := os.Open(path); err == nil {
		if fi, err := f.Stat(); err == nil {
			t.pos, t.id = fi.Size(), fi
		}
		f.Close()
	}
	return t
}

// poll returns all states recognized since the last call.
func (t *tailer) poll() ([]irState, error) {
	f, err := os.Open(t.path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	fi, err := f.Stat()
	if err != nil {
		return nil, err
	}
	if t.id != nil && (!os.SameFile(t.id, fi) || fi.Size() < t.pos) {
		t.pos = 0 // rotated: read the new file from the start
	}
	t.id = fi
	if fi.Size() == t.pos {
		return nil, nil
	}
	n := min(fi.Size()-t.pos, 4<<20)
	buf := make([]byte, n)
	if _, err := f.ReadAt(buf, t.pos); err != nil && err != io.EOF {
		return nil, err
	}
	end := bytes.LastIndexByte(buf, '\n')
	if end < 0 {
		return nil, nil // incomplete line, keep reading later
	}
	t.pos += int64(end + 1)
	var out []irState
	for _, line := range bytes.Split(buf[:end], []byte("\n")) {
		if s := parseState(line); s != stateUnknown {
			dbg("Capture log: %s <- %s", s, bytes.TrimSpace(line))
			out = append(out, s)
		}
	}
	return out, nil
}

// Monitor keeps Instant Replay switched on permanently.
type Monitor struct {
	log   *log.Logger
	path  string
	press func(ctx context.Context) error // sends the Instant Replay hotkey into the user session

	lastFile irState // last irEnabled value read from ShareSettings.json
}

func sleepCtx(ctx context.Context, d time.Duration) bool {
	select {
	case <-ctx.Done():
		return false
	case <-time.After(d):
		return true
	}
}

func lastOf(evs []irState) irState {
	if len(evs) == 0 {
		return stateUnknown
	}
	return evs[len(evs)-1]
}

func (m *Monitor) Run(ctx context.Context) {
	state := lastStateInFile(m.path)
	if state == stateUnknown {
		state = lastStateInFile(m.path[:len(m.path)-len(filepath.Ext(m.path))] + ".old")
	}
	t := newTailer(m.path)
	m.log.Printf("Monitoring started (%s), Instant Replay per log: %s", m.path, state)
	// ShareSettings.json (irEnabled) is the most accurate source and takes precedence over the log.
	if fs, ok := readIREnabled(); ok {
		m.lastFile = fs
		state = fs
		m.log.Printf("Instant Replay per ShareSettings.json: %s", fs)
	}

	// If the NVIDIA app has been running for a while, the log is current enough. Otherwise (restart of the
	// PC/app) the status is queried from the app again, see syncWithApp.
	var seen time.Time
	if c, ok := overlayStart(); ok {
		seen = c
		if time.Since(c) < freshAppWindow {
			state = m.syncWithApp(ctx, c, state)
		}
	}
	if state == stateOff && m.ensureOn(ctx, t) {
		state = stateOn
	}

	tick := time.NewTicker(2 * time.Second)
	defer tick.Stop()
	warned := false
	for {
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
		}
		// New app instance (restart, driver update, ...): query the status again.
		c, ok := overlayStart()
		if ok != !seen.IsZero() {
			dbg("NVIDIA Overlay running: %v", ok)
			if !ok {
				seen = time.Time{}
			}
		}
		if ok && !c.Equal(seen) {
			seen = c
			m.log.Print("NVIDIA app restarted")
			state = m.syncWithApp(ctx, c, state)
			if state == stateOff && m.ensureOn(ctx, t) {
				state = stateOn
			}
			continue
		}
		evs, err := m.pollAll(t)
		if err != nil {
			if !warned {
				m.log.Printf("Log not readable: %v", err)
				warned = true
			}
			continue
		}
		warned = false
		if s := lastOf(evs); s != stateUnknown && s != state {
			m.log.Printf("Instant Replay: %s -> %s", state, s)
			state = s
			if state == stateOff && m.ensureOn(ctx, t) {
				state = stateOn
			}
		}
	}
}

// freshAppWindow: how young the app instance may be for its startup status to count as authoritative.
const freshAppWindow = 15 * time.Minute

// syncWithApp waits (up to 3 min) for the status the freshly started NVIDIA app reports on
// startup, and returns it. The capture log can be stale after a restart.
func (m *Monitor) syncWithApp(ctx context.Context, appStart time.Time, fallback irState) irState {
	deadline := time.Now().Add(3 * time.Minute)
	for {
		s, ts, ok := readStartupStatus()
		dbg("Waiting for app status: found=%v state=%s time=%s app-start=%s", ok, s, ts.Format("15:04:05.000"), appStart.Format("15:04:05.000"))
		if ok && !ts.Before(appStart) {
			m.log.Printf("Instant Replay per NVIDIA app: %s", s)
			return s
		}
		if time.Now().After(deadline) || !sleepCtx(ctx, 3*time.Second) {
			if fs, ok := readIREnabled(); ok {
				m.log.Printf("No startup status from the NVIDIA app, using ShareSettings.json: %s", fs)
				return fs
			}
			m.log.Printf("No startup status from the NVIDIA app, not intervening (log state was: %s)", fallback)
			return stateUnknown
		}
	}
}

// ensureOn switches Instant Replay back on and checks for confirmation.
func (m *Monitor) ensureOn(ctx context.Context, t *tailer) bool {
	// Debounce: the state briefly flips several times when a game starts.
	if !sleepCtx(ctx, 2*time.Second) {
		return false
	}
	if evs, _ := m.pollAll(t); m.confirmedOn(evs) {
		m.log.Print("Instant Replay came back on by itself")
		return true
	}
	for attempt := 1; attempt <= 3; attempt++ {
		if err := m.press(ctx); err != nil {
			m.log.Printf("Attempt %d: sending hotkey failed: %v", attempt, err)
			if !sleepCtx(ctx, 20*time.Second) {
				return false
			}
			continue
		}
		m.log.Printf("Attempt %d: hotkey sent", attempt)
		for range 30 { // wait up to 15 s for confirmation
			if !sleepCtx(ctx, 500*time.Millisecond) {
				return false
			}
			evs, _ := m.pollAll(t)
			if m.confirmedOn(evs) {
				m.log.Print("Instant Replay switched back on")
				return true
			}
		}
		m.log.Printf("Attempt %d: no confirmation", attempt)
		if !sleepCtx(ctx, 5*time.Second) {
			return false
		}
	}
	m.log.Print("Giving up, waiting for the next event")
	return false
}

// pollAll returns new events from the capture log and changes of irEnabled in
// ShareSettings.json (file last, it wins on conflict).
func (m *Monitor) pollAll(t *tailer) ([]irState, error) {
	evs, err := t.poll()
	if fs, ok := readIREnabled(); ok && fs != m.lastFile {
		m.log.Printf("ShareSettings.json: irEnabled %s -> %s", m.lastFile, fs)
		m.lastFile = fs
		evs = append(evs, fs)
	}
	return evs, err
}

// confirmedOn: new events or the stored switch say Instant Replay is on.
func (m *Monitor) confirmedOn(evs []irState) bool {
	if lastOf(evs) == stateOn {
		return true
	}
	fs, ok := readIREnabled()
	return ok && fs == stateOn
}
