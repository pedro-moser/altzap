package client

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestSingleInstanceSocketPath_RespectsXDGRuntimeDir(t *testing.T) {
	xdg := t.TempDir()
	t.Setenv("XDG_RUNTIME_DIR", xdg)

	got := SingleInstanceSocketPath()
	want := filepath.Join(xdg, "altzap.sock")

	if got != want {
		t.Fatalf("XDG_RUNTIME_DIR not honored: want %q, got %q", want, got)
	}
}

func TestSingleInstanceSocketPath_FallsBackToTmp(t *testing.T) {
	t.Setenv("XDG_RUNTIME_DIR", "")

	got := SingleInstanceSocketPath()
	wantSuffix := fmt.Sprintf("altzap-%d.sock", os.Getuid())

	if !strings.HasPrefix(got, "/tmp/") {
		t.Fatalf("fallback should be under /tmp/: got %q", got)
	}
	if !strings.HasSuffix(got, wantSuffix) {
		t.Fatalf("fallback should end with %q: got %q", wantSuffix, got)
	}
}

func TestAcquire_PrimaryWhenNoExistingSocket(t *testing.T) {
	sock := filepath.Join(t.TempDir(), "altzap.sock")

	release, isSecondary, err := Acquire(sock, func() {})
	if err != nil {
		t.Fatalf("Acquire: %v", err)
	}
	defer release()

	if isSecondary {
		t.Fatal("expected primary, got secondary")
	}
	if release == nil {
		t.Fatal("release must be non-nil on primary path")
	}
	if _, err := os.Stat(sock); err != nil {
		t.Fatalf("socket file not created: %v", err)
	}
}

func TestAcquire_SecondaryTriggersOnShowOnPrimary(t *testing.T) {
	sock := filepath.Join(t.TempDir(), "altzap.sock")

	shown := make(chan struct{}, 1)
	primaryRelease, isSecondary, err := Acquire(sock, func() {
		shown <- struct{}{}
	})
	if err != nil {
		t.Fatalf("primary Acquire: %v", err)
	}
	defer primaryRelease()
	if isSecondary {
		t.Fatal("first Acquire should be primary")
	}

	secondaryRelease, isSecondary, err := Acquire(sock, func() {
		t.Fatal("secondary onShow must not be invoked")
	})
	if err != nil {
		t.Fatalf("secondary Acquire: %v", err)
	}
	if !isSecondary {
		t.Fatal("second Acquire should be secondary")
	}
	if secondaryRelease != nil {
		t.Fatal("secondary release must be nil — caller exits, no resources owned")
	}

	select {
	case <-shown:
	case <-time.After(2 * time.Second):
		t.Fatal("primary onShow not invoked within 2s after secondary signal")
	}
}

func TestAcquire_RecoversFromStaleSocket(t *testing.T) {
	sock := filepath.Join(t.TempDir(), "altzap.sock")

	// Simulate a stale socket file: a regular file at the path but no
	// listener bound to it. Real-world cause: app crash without cleanup.
	if err := os.WriteFile(sock, []byte("stale"), 0o600); err != nil {
		t.Fatalf("seed stale file: %v", err)
	}

	release, isSecondary, err := Acquire(sock, func() {})
	if err != nil {
		t.Fatalf("Acquire over stale socket: %v", err)
	}
	defer release()

	if isSecondary {
		t.Fatal("stale socket should not be treated as live primary")
	}
	info, err := os.Stat(sock)
	if err != nil {
		t.Fatalf("socket missing after recovery: %v", err)
	}
	if info.Mode()&os.ModeSocket == 0 {
		t.Fatalf("path %q is not a socket after recovery: mode=%v", sock, info.Mode())
	}
}

func TestAcquire_ReleaseClosesListenerAndRemovesSocket(t *testing.T) {
	sock := filepath.Join(t.TempDir(), "altzap.sock")

	release, _, err := Acquire(sock, func() {})
	if err != nil {
		t.Fatalf("Acquire: %v", err)
	}
	if err := release(); err != nil {
		t.Fatalf("release: %v", err)
	}

	if _, err := os.Stat(sock); !os.IsNotExist(err) {
		t.Fatalf("socket file still present after release: stat err=%v", err)
	}

	// A fresh Acquire on the same path must take the primary slot — proving
	// the listener was actually closed (no stale FD holding the address).
	release2, isSecondary, err := Acquire(sock, func() {})
	if err != nil {
		t.Fatalf("re-Acquire after release: %v", err)
	}
	defer release2()
	if isSecondary {
		t.Fatal("re-Acquire should be primary after the original released")
	}
}

func TestAcquire_ConcurrentSecondariesAllTriggerOnShow(t *testing.T) {
	sock := filepath.Join(t.TempDir(), "altzap.sock")

	var shows int32
	primaryRelease, _, err := Acquire(sock, func() {
		atomic.AddInt32(&shows, 1)
	})
	if err != nil {
		t.Fatalf("primary Acquire: %v", err)
	}
	defer primaryRelease()

	const n = 20
	done := make(chan struct{}, n)
	for i := 0; i < n; i++ {
		go func() {
			defer func() { done <- struct{}{} }()
			_, isSecondary, err := Acquire(sock, nil)
			if err != nil {
				t.Errorf("secondary Acquire: %v", err)
				return
			}
			if !isSecondary {
				t.Error("expected secondary in concurrent contention")
			}
		}()
	}
	for i := 0; i < n; i++ {
		<-done
	}

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if atomic.LoadInt32(&shows) == n {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("expected %d onShow invocations, got %d", n, atomic.LoadInt32(&shows))
}
