package client

import (
	"bufio"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// SingleInstanceSocketPath returns the canonical Unix socket path used to
// detect a running AltZap instance. Resolution order:
//  1. $XDG_RUNTIME_DIR/altzap.sock (preferred — tmpfs, mode 0700, auto-purged)
//  2. /tmp/altzap-<uid>.sock (fallback for environments without XDG_RUNTIME_DIR)
func SingleInstanceSocketPath() string {
	if rt := os.Getenv("XDG_RUNTIME_DIR"); rt != "" {
		return filepath.Join(rt, "altzap.sock")
	}
	return fmt.Sprintf("/tmp/altzap-%d.sock", os.Getuid())
}

// Acquire takes ownership of the single-instance Unix socket at socketPath.
//
// On the primary path it binds the socket, spawns a listener goroutine that
// invokes onShow for each "show" line received from secondaries, and returns
// (release, false, nil). release closes the listener and removes the socket.
//
// On the secondary path (a primary is already listening) it sends "show\n"
// down the socket and returns (nil, true, nil) — the caller should exit
// cleanly. A stale socket (file exists but nothing accepts) is removed and
// the call falls through to primary.
func Acquire(socketPath string, onShow func()) (release func() error, isSecondary bool, err error) {
	if conn, dialErr := net.DialTimeout("unix", socketPath, 200*time.Millisecond); dialErr == nil {
		defer conn.Close()
		_, _ = conn.Write([]byte("show\n"))
		return nil, true, nil
	}
	// No live primary: either the socket never existed (ENOENT) or it's
	// stale (ECONNREFUSED). Remove is idempotent for both.
	_ = os.Remove(socketPath)

	ln, err := net.Listen("unix", socketPath)
	if err != nil {
		return nil, false, err
	}

	go acceptLoop(ln, onShow)

	return func() error {
		ln.Close()
		// Go's Unix listener unlinks the socket on Close, so a follow-up
		// Remove would normally hit ENOENT — tolerate it. Guards against
		// future Go versions that change Close semantics, and against
		// external removal between Close and Remove.
		if err := os.Remove(socketPath); err != nil && !os.IsNotExist(err) {
			return err
		}
		return nil
	}, false, nil
}

func acceptLoop(ln net.Listener, onShow func()) {
	for {
		conn, err := ln.Accept()
		if err != nil {
			return
		}
		go handleConn(conn, onShow)
	}
}

func handleConn(conn net.Conn, onShow func()) {
	defer conn.Close()
	scanner := bufio.NewScanner(conn)
	if scanner.Scan() && strings.TrimSpace(scanner.Text()) == "show" {
		onShow()
	}
}
