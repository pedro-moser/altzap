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

// AppConfigDir returns the canonical config directory for AltZap,
// creating it (with mode 0o755) if missing. Honors XDG_CONFIG_HOME
// via os.UserConfigDir.
func AppConfigDir() string {
	base, err := os.UserConfigDir()
	if err != nil {
		log.Fatalf("could not resolve config dir: %v", err)
	}
	dir := filepath.Join(base, "altzap")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		log.Fatalf("could not create config dir %q: %v", dir, err)
	}
	return dir
}

// MediaDir returns the directory where downloaded media (chat-jid
// subdirs and avatars/) live. Equivalent to AppDataDir()+"/media".
// Does not create the directory itself — call sites already MkdirAll
// the per-chat sub-paths before writing.
func MediaDir() string {
	return filepath.Join(AppDataDir(), "media")
}

// AppStateDir returns the canonical state directory for AltZap (logs,
// volatile runtime artifacts), creating it if missing. Honors
// XDG_STATE_HOME; defaults to ~/.local/state/altzap.
//
// State is XDG-classified for files that should persist across reboots
// but are not portable like config or precious like data — the perfect
// fit for a rolling log file the user can grep when something looks off.
func AppStateDir() string {
	base := os.Getenv("XDG_STATE_HOME")
	if base == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			log.Fatalf("could not resolve home dir: %v", err)
		}
		base = filepath.Join(home, ".local", "state")
	}
	dir := filepath.Join(base, "altzap")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		log.Fatalf("could not create state dir %q: %v", dir, err)
	}
	return dir
}
