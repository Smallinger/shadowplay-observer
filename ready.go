package main

import (
	"context"
	"os"
	"strings"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

// Readiness of NVIDIA ShadowPlay. Instant Replay is made of three parts, and the hotkey only
// works when all of them are up:
//
//  1. service  NvContainerLocalSystem, starts the containers
//  2. capture  a nvcontainer.exe in the user's session whose command line contains "SPUser"
//     (the ShadowPlay user container, it hosts the capture and encode threads)
//  3. overlay  NVIDIA Overlay.exe, which registers and receives the hotkey
//
// Sending the hotkey before that goes nowhere, or lands in whatever window has focus.

const (
	nvServiceName   = "NvContainerLocalSystem"
	nvContainerExe  = "nvcontainer.exe"
	nvCaptureMarker = "SPUser"
)

// nvMissing lists what is not running yet; empty means ShadowPlay is ready.
func nvMissing() []string {
	var missing []string
	if !nvServiceRunning() {
		missing = append(missing, "service "+nvServiceName)
	}
	if !nvCaptureContainerRunning() {
		missing = append(missing, "ShadowPlay capture container (nvcontainer SPUser)")
	}
	if _, ok := overlayStart(); !ok {
		missing = append(missing, overlayExe)
	}
	return missing
}

// nvReadyDescription is a one-line summary for `status`.
func nvReadyDescription() string {
	if m := nvMissing(); len(m) > 0 {
		return "NOT ready, missing: " + strings.Join(m, ", ")
	}
	return "ready"
}

func nvServiceRunning() bool {
	scm, err := windows.OpenSCManager(nil, nil, windows.SC_MANAGER_CONNECT)
	if err != nil {
		return true // cannot tell, do not block on it
	}
	defer windows.CloseServiceHandle(scm)
	name, _ := windows.UTF16PtrFromString(nvServiceName)
	h, err := windows.OpenService(scm, name, windows.SERVICE_QUERY_STATUS)
	if err != nil {
		return false
	}
	defer windows.CloseServiceHandle(h)
	var st windows.SERVICE_STATUS
	if windows.QueryServiceStatus(h, &st) != nil {
		return true
	}
	return st.CurrentState == windows.SERVICE_RUNNING
}

// nvCaptureContainerRunning looks for the ShadowPlay user container. The service runs in
// session 0 and starts one nvcontainer per role in the user's session; only the SPUser one
// captures. If command lines cannot be read it accepts any nvcontainer outside session 0.
func nvCaptureContainerRunning() bool {
	snap, err := windows.CreateToolhelp32Snapshot(windows.TH32CS_SNAPPROCESS, 0)
	if err != nil {
		return true
	}
	defer windows.CloseHandle(snap)
	pe := windows.ProcessEntry32{Size: uint32(unsafe.Sizeof(windows.ProcessEntry32{}))}
	fallback := false
	for err = windows.Process32First(snap, &pe); err == nil; err = windows.Process32Next(snap, &pe) {
		if !strings.EqualFold(windows.UTF16ToString(pe.ExeFile[:]), nvContainerExe) {
			continue
		}
		var sess uint32
		if windows.ProcessIdToSessionId(pe.ProcessID, &sess) != nil || sess == 0 {
			continue
		}
		cmd, err := processCommandLine(pe.ProcessID)
		if err != nil {
			fallback = true
			continue
		}
		if strings.Contains(cmd, nvCaptureMarkerVar) {
			return true
		}
	}
	return fallback
}

// processCommandLine reads a process' command line (NtQueryInformationProcess, class 60).
func processCommandLine(pid uint32) (string, error) {
	h, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION, false, pid)
	if err != nil {
		return "", err
	}
	defer windows.CloseHandle(h)
	const processCommandLineInformation = 60
	var n uint32
	windows.NtQueryInformationProcess(h, processCommandLineInformation, nil, 0, &n) // asks for the size
	if n == 0 {
		return "", os.ErrInvalid
	}
	buf := make([]byte, n)
	if err := windows.NtQueryInformationProcess(h, processCommandLineInformation, unsafe.Pointer(&buf[0]), n, &n); err != nil {
		return "", err
	}
	return (*windows.NTUnicodeString)(unsafe.Pointer(&buf[0])).String(), nil
}

// waitReady blocks until ShadowPlay is ready (true), the timeout passes or ctx ends (false).
// It logs what is missing once and again when it is ready.
func (m *Monitor) waitReady(ctx context.Context) bool {
	deadline := time.Now().Add(readyTimeout)
	logged := false
	for {
		missing := nvMissing()
		if len(missing) == 0 {
			if logged {
				m.log.Print("NVIDIA ShadowPlay is ready")
			}
			return true
		}
		if !logged {
			m.log.Printf("NVIDIA ShadowPlay not ready, waiting. Missing: %s", strings.Join(missing, ", "))
			logged = true
		}
		if time.Now().After(deadline) {
			m.log.Printf("NVIDIA ShadowPlay still not ready after %s (missing: %s), will retry when it is",
				readyTimeout, strings.Join(missing, ", "))
			return false
		}
		if !sleepCtx(ctx, 3*time.Second) {
			return false
		}
	}
}

// Variables (not constants) so tests can prove the marker is really matched and shorten the wait.
var (
	nvCaptureMarkerVar = nvCaptureMarker
	readyTimeout       = 3 * time.Minute
)
