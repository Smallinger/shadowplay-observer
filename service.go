package main

import (
	"context"
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"
	"sync"
	"time"

	"golang.org/x/sys/windows"
	"golang.org/x/sys/windows/svc"
	"golang.org/x/sys/windows/svc/mgr"
)

const (
	svcName = "ShadowplayObserver"
	svcDesc = "Keeps NVIDIA Instant Replay switched on permanently."
)

// ---- Logging (file with simple rotation) ----

type rotatingFile struct {
	mu   sync.Mutex
	path string
	f    *os.File
	size int64
}

func (r *rotatingFile) Write(p []byte) (int, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.f == nil || r.size > 1<<20 {
		if r.f != nil {
			r.f.Close()
			os.Rename(r.path, r.path+".old")
		}
		f, err := os.OpenFile(r.path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
		if err != nil {
			return 0, err
		}
		r.f = f
		r.size = 0
		if fi, err := f.Stat(); err == nil {
			r.size = fi.Size()
		}
	}
	n, err := r.f.Write(p)
	r.size += int64(n)
	return n, err
}

var asService bool // running as the Windows service (SYSTEM)

func programDataDir() string {
	pd := os.Getenv("ProgramData")
	if pd == "" {
		pd = `C:\ProgramData`
	}
	return filepath.Join(pd, "ShadowplayObserver")
}

// dataDir is where the regular log lives: ProgramData for the service, the user's
// local app data for autostart/foreground runs (the user may not write to ProgramData).
func dataDir() string {
	if !asService {
		if la := os.Getenv("LOCALAPPDATA"); la != "" {
			return filepath.Join(la, "ShadowplayObserver")
		}
	}
	return programDataDir()
}

func newLogger(console bool) *log.Logger {
	os.MkdirAll(dataDir(), 0o755)
	var w io.Writer = &rotatingFile{path: filepath.Join(dataDir(), "observer.log")}
	if debugOn {
		w = io.MultiWriter(w, &rotatingFile{path: debugLogPath()})
	}
	if console {
		w = io.MultiWriter(w, os.Stdout)
	}
	l := log.New(w, "", log.Ldate|log.Ltime|log.Lmicroseconds)
	if debugOn {
		debugOut = l
	}
	return l
}

// ---- Service ----

type handler struct{ log *log.Logger }

func (h handler) Execute(_ []string, req <-chan svc.ChangeRequest, status chan<- svc.Status) (bool, uint32) {
	status <- svc.Status{State: svc.StartPending}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan struct{})
	m := &Monitor{log: h.log, path: captureLogPath(), press: pressInUserSession}
	go func() {
		defer close(done)
		if !waitForInstance(ctx, h.log.Printf) {
			return
		}
		defer releaseInstance()
		m.Run(ctx)
	}()
	status <- svc.Status{State: svc.Running, Accepts: svc.AcceptStop | svc.AcceptShutdown}
	for c := range req {
		switch c.Cmd {
		case svc.Interrogate:
			status <- c.CurrentStatus
		case svc.Stop, svc.Shutdown:
			status <- svc.Status{State: svc.StopPending}
			cancel()
			select {
			case <-done:
			case <-time.After(10 * time.Second):
			}
			h.log.Print("Service stopped")
			return false, 0
		}
	}
	return false, 0
}

func runService() {
	asService = true
	l := newLogger(false)
	l.Printf("Service starting (debug=%v)", debugOn)
	logEnvironment()
	if err := svc.Run(svcName, handler{l}); err != nil {
		l.Printf("svc.Run: %v", err)
	}
}

// runConsole: monitoring in the foreground (for testing), Ctrl+C exits.
func runConsole() {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() {
		c := make(chan os.Signal, 1)
		notifyInterrupt(c)
		<-c
		cancel()
	}()
	l := newLogger(!backgroundMode)
	if !acquireInstance() {
		l.Print("Another instance (service or autostart) is already running, exiting")
		return
	}
	defer releaseInstance()
	logEnvironment()
	m := &Monitor{log: l, path: captureLogPath(), press: func(ctx context.Context) error { return pressHotkey() }}
	m.Run(ctx)
}

// ---- Installation ----

func installService(debug bool) error {
	exe, err := os.Executable()
	if err != nil {
		return err
	}
	m, err := mgr.Connect()
	if err != nil {
		return fmt.Errorf("service manager: %w", err)
	}
	defer m.Disconnect()

	if s, err := m.OpenService(svcName); err == nil {
		s.Close()
		return fmt.Errorf("service %q is already installed (run 'uninstall' first)", svcName)
	}
	s, err := m.CreateService(svcName, exe, mgr.Config{
		DisplayName:      "Shadowplay Observer",
		Description:      svcDesc,
		StartType:        mgr.StartAutomatic,
		DelayedAutoStart: true,
	}, svcArgs(debug)...)
	if err != nil {
		return err
	}
	defer s.Close()
	s.SetRecoveryActions([]mgr.RecoveryAction{
		{Type: mgr.ServiceRestart, Delay: 5 * time.Second},
		{Type: mgr.ServiceRestart, Delay: 30 * time.Second},
		{Type: mgr.ServiceRestart, Delay: time.Minute},
	}, 86400)
	if err := s.Start(); err != nil {
		return fmt.Errorf("installed, but start failed: %w", err)
	}
	return nil
}

func uninstallService() error {
	m, err := mgr.Connect()
	if err != nil {
		return err
	}
	defer m.Disconnect()
	s, err := m.OpenService(svcName)
	if err != nil {
		return fmt.Errorf("service %q not found", svcName)
	}
	defer s.Close()
	if st, err := s.Query(); err == nil && st.State != svc.Stopped {
		s.Control(svc.Stop)
		for i := 0; i < 20; i++ {
			time.Sleep(500 * time.Millisecond)
			if st, err := s.Query(); err != nil || st.State == svc.Stopped {
				break
			}
		}
	}
	return s.Delete()
}

func serviceStatus() string {
	scm, err := windows.OpenSCManager(nil, nil, windows.SC_MANAGER_CONNECT)
	if err != nil {
		return "unknown (" + err.Error() + ")"
	}
	defer windows.CloseServiceHandle(scm)
	name, _ := windows.UTF16PtrFromString(svcName)
	h, err := windows.OpenService(scm, name, windows.SERVICE_QUERY_STATUS)
	if err != nil {
		return "not installed"
	}
	defer windows.CloseServiceHandle(h)
	var st windows.SERVICE_STATUS
	if err := windows.QueryServiceStatus(h, &st); err != nil {
		return "unknown"
	}
	names := map[uint32]string{windows.SERVICE_STOPPED: "stopped", windows.SERVICE_START_PENDING: "starting", windows.SERVICE_STOP_PENDING: "stopping", windows.SERVICE_RUNNING: "running"}
	if n, ok := names[st.CurrentState]; ok {
		return n
	}
	return fmt.Sprint(st.CurrentState)
}

func isElevated() bool { return windows.GetCurrentProcessToken().IsElevated() }

// relaunchElevated restarts the exe with the same arguments via UAC.
func relaunchElevated(args []string) error {
	exe, err := os.Executable()
	if err != nil {
		return err
	}
	var cmdline string
	for _, a := range args {
		cmdline += a + " "
	}
	cmdline += "--elevated"
	verb, _ := windows.UTF16PtrFromString("runas")
	file, _ := windows.UTF16PtrFromString(exe)
	params, _ := windows.UTF16PtrFromString(cmdline)
	return windows.ShellExecute(0, verb, file, params, nil, windows.SW_SHOWNORMAL)
}

func svcArgs(debug bool) []string {
	if debug {
		return []string{"-debug"}
	}
	return nil
}
