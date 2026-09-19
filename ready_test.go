package main

import (
	"bytes"
	"context"
	"log"
	"strings"
	"testing"
	"time"
)

// These tests need the NVIDIA app running on this machine, otherwise they skip.
func skipWithoutNvidia(t *testing.T) {
	t.Helper()
	if len(nvMissing()) > 0 {
		t.Skipf("NVIDIA ShadowPlay is not fully running here: %v", nvMissing())
	}
}

func TestNvReadyWhenRunning(t *testing.T) {
	skipWithoutNvidia(t)
	if d := nvReadyDescription(); d != "ready" {
		t.Errorf("description = %q", d)
	}
}

// With a marker that no container has, the capture container check must fail: proof that the
// command line really is read and matched, and not that a fallback says yes.
func TestCaptureContainerNeedsMarker(t *testing.T) {
	skipWithoutNvidia(t)
	defer func(old string) { nvCaptureMarkerVar = old }(nvCaptureMarkerVar)
	nvCaptureMarkerVar = "NoSuchMarker"
	if nvCaptureContainerRunning() {
		t.Error("bogus marker matched: command lines are not being read")
	}
	if got := nvMissing(); len(got) != 1 || !strings.Contains(got[0], "capture container") {
		t.Errorf("missing = %v, want only the capture container", got)
	}
}

func TestWaitReady(t *testing.T) {
	skipWithoutNvidia(t)
	var buf bytes.Buffer
	m := &Monitor{log: log.New(&buf, "", 0)}

	if !m.waitReady(context.Background()) {
		t.Fatal("waitReady = false although everything runs")
	}

	// Not ready: it must wait, name what is missing and give up after the timeout.
	defer func(old string, oldT time.Duration) { nvCaptureMarkerVar, readyTimeout = old, oldT }(nvCaptureMarkerVar, readyTimeout)
	nvCaptureMarkerVar, readyTimeout = "NoSuchMarker", 100*time.Millisecond
	start := time.Now()
	if m.waitReady(context.Background()) {
		t.Fatal("waitReady = true although the capture container is 'missing'")
	}
	if time.Since(start) > 10*time.Second {
		t.Errorf("waitReady took %s", time.Since(start))
	}
	if out := buf.String(); !strings.Contains(out, "not ready, waiting") || !strings.Contains(out, "capture container") {
		t.Errorf("log does not say what is missing:\n%s", out)
	}
}
