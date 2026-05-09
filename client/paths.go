package client

import (
	"log"
	"os"
	"path/filepath"
)

// AppDataDir returns the canonical data directory for AltZap, creating it
// (with mode 0o755) if missing. Resolution order:
//  1. ALTZAP_DATA_DIR env var (if non-empty)
//  2. $XDG_DATA_HOME/altzap (if XDG_DATA_HOME is non-empty)
//  3. ~/.local/share/altzap
//
// Failure to resolve home or to create the directory is fatal — without
// a data directory the app cannot run.
func AppDataDir() string {
	dir := resolveAppDataDir()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		log.Fatalf("could not create data dir %q: %v", dir, err)
	}
	return dir
}

func resolveAppDataDir() string {
	if v := os.Getenv("ALTZAP_DATA_DIR"); v != "" {
		return v
	}
	if v := os.Getenv("XDG_DATA_HOME"); v != "" {
		return filepath.Join(v, "altzap")
	}
	home, err := os.UserHomeDir()
	if err != nil {
		log.Fatalf("could not resolve home dir: %v", err)
	}
	return filepath.Join(home, ".local", "share", "altzap")
}
