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

// Videobeweis == "Instant Replay" (IR) in der NVIDIA App. Der Zustand wird in
// CaptureCore.log protokolliert, z.B.
//
//	CCaptureSession::EvaluateDVRStop: IR(0) HL(0) DVR(0)              -> aus
//	CCaptureSession::EvaluateAutoDVRStart: Starting DVR capture. IR(1) -> an
//	DVR Active = true                                                  -> an
type irState int

const (
	stateUnknown irState = iota
	stateOn
	stateOff
)

func (s irState) String() string {
	switch s {
	case stateOn:
		return "an"
	case stateOff:
		return "aus"
	}
	return "unbekannt"
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

// lastStateInFile liefert den letzten erkennbaren Zustand aus den letzten 1 MB der Datei.
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

// tailer liest neue Zeilen aus dem Log und erkennt Rotation (Datei ersetzt/verkleinert).
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

// poll liefert alle seit dem letzten Aufruf neu erkannten Zustände.
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
		t.pos = 0 // rotiert: neue Datei von vorne lesen
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
		return nil, nil // unvollständige Zeile, später weiterlesen
	}
	t.pos += int64(end + 1)
	var out []irState
	for _, line := range bytes.Split(buf[:end], []byte("\n")) {
		if s := parseState(line); s != stateUnknown {
			dbg("Capture-Log: %s <- %s", s, bytes.TrimSpace(line))
			out = append(out, s)
		}
	}
	return out, nil
}

// Monitor hält den Videobeweis dauerhaft eingeschaltet.
type Monitor struct {
	log   *log.Logger
	path  string
	press func(ctx context.Context) error // sendet den Videobeweis-Hotkey in die Benutzersitzung

	lastFile irState // zuletzt gelesener Wert von irEnabled aus ShareSettings.json
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
	m.log.Printf("Überwachung gestartet (%s), Videobeweis laut Log: %s", m.path, state)
	// ShareSettings.json (irEnabled) ist die genaueste Quelle und hat Vorrang vor dem Log.
	if fs, ok := readIREnabled(); ok {
		m.lastFile = fs
		state = fs
		m.log.Printf("Videobeweis laut ShareSettings.json: %s", fs)
	}

	// Ist die NVIDIA App schon länger aktiv, ist das Log aktuell genug. Sonst (Neustart des
	// Rechners/der App) wird der Status neu von der App abgefragt, siehe syncWithApp.
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
		// Neue App-Instanz (Neustart, Treiber-Update, ...): Status neu abfragen.
		c, ok := overlayStart()
		if ok != !seen.IsZero() {
			dbg("NVIDIA Overlay läuft: %v", ok)
			if !ok {
				seen = time.Time{}
			}
		}
		if ok && !c.Equal(seen) {
			seen = c
			m.log.Print("NVIDIA App neu gestartet")
			state = m.syncWithApp(ctx, c, state)
			if state == stateOff && m.ensureOn(ctx, t) {
				state = stateOn
			}
			continue
		}
		evs, err := m.pollAll(t)
		if err != nil {
			if !warned {
				m.log.Printf("Log nicht lesbar: %v", err)
				warned = true
			}
			continue
		}
		warned = false
		if s := lastOf(evs); s != stateUnknown && s != state {
			m.log.Printf("Videobeweis: %s -> %s", state, s)
			state = s
			if state == stateOff && m.ensureOn(ctx, t) {
				state = stateOn
			}
		}
	}
}

// freshAppWindow: so jung darf die App-Instanz sein, damit ihr Start-Status als maßgeblich gilt.
const freshAppWindow = 15 * time.Minute

// syncWithApp wartet (bis 3 min) auf den Status, den die frisch gestartete NVIDIA App beim
// Start selbst meldet, und liefert ihn. Das Capture-Log kann nach einem Neustart veraltet sein.
func (m *Monitor) syncWithApp(ctx context.Context, appStart time.Time, fallback irState) irState {
	deadline := time.Now().Add(3 * time.Minute)
	for {
		s, ts, ok := readStartupStatus()
		dbg("Warte auf App-Status: gefunden=%v Stand=%s Zeit=%s App-Start=%s", ok, s, ts.Format("15:04:05.000"), appStart.Format("15:04:05.000"))
		if ok && !ts.Before(appStart) {
			m.log.Printf("Videobeweis laut NVIDIA App: %s", s)
			return s
		}
		if time.Now().After(deadline) || !sleepCtx(ctx, 3*time.Second) {
			if fs, ok := readIREnabled(); ok {
				m.log.Printf("Kein Start-Status der NVIDIA App gefunden, nehme ShareSettings.json: %s", fs)
				return fs
			}
			m.log.Printf("Kein Start-Status der NVIDIA App gefunden, greife nicht ein (Log-Stand war: %s)", fallback)
			return stateUnknown
		}
	}
}

// ensureOn schaltet den Videobeweis wieder ein und prüft die Bestätigung.
func (m *Monitor) ensureOn(ctx context.Context, t *tailer) bool {
	// Entprellen: beim Spielstart wechselt der Zustand kurz mehrfach.
	if !sleepCtx(ctx, 2*time.Second) {
		return false
	}
	if evs, _ := m.pollAll(t); m.confirmedOn(evs) {
		m.log.Print("Videobeweis ist von selbst wieder an")
		return true
	}
	for attempt := 1; attempt <= 3; attempt++ {
		if err := m.press(ctx); err != nil {
			m.log.Printf("Versuch %d: Hotkey senden fehlgeschlagen: %v", attempt, err)
			if !sleepCtx(ctx, 20*time.Second) {
				return false
			}
			continue
		}
		m.log.Printf("Versuch %d: Hotkey gesendet", attempt)
		for range 30 { // bis zu 15 s auf Bestätigung warten
			if !sleepCtx(ctx, 500*time.Millisecond) {
				return false
			}
			evs, _ := m.pollAll(t)
			if m.confirmedOn(evs) {
				m.log.Print("Videobeweis wieder eingeschaltet")
				return true
			}
		}
		m.log.Printf("Versuch %d: keine Bestätigung", attempt)
		if !sleepCtx(ctx, 5*time.Second) {
			return false
		}
	}
	m.log.Print("Aufgegeben, warte auf das nächste Ereignis")
	return false
}

// pollAll liefert neue Ereignisse aus dem Capture-Log und Änderungen von irEnabled in
// ShareSettings.json (die Datei zuletzt, sie gewinnt bei Widerspruch).
func (m *Monitor) pollAll(t *tailer) ([]irState, error) {
	evs, err := t.poll()
	if fs, ok := readIREnabled(); ok && fs != m.lastFile {
		m.log.Printf("ShareSettings.json: irEnabled %s -> %s", m.lastFile, fs)
		m.lastFile = fs
		evs = append(evs, fs)
	}
	return evs, err
}

// confirmedOn: laut neuen Ereignissen oder laut gespeichertem Schalter ist der Videobeweis an.
func (m *Monitor) confirmedOn(evs []irState) bool {
	if lastOf(evs) == stateOn {
		return true
	}
	fs, ok := readIREnabled()
	return ok && fs == stateOn
}
