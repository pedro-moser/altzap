package client

import (
	"os"
	"path/filepath"
	"testing"
)

func TestAppDataDir_RespectsAltzapEnv(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("ALTZAP_DATA_DIR", tmp)

	got := AppDataDir()

	if got != tmp {
		t.Fatalf("ALTZAP_DATA_DIR not honored: want %q, got %q", tmp, got)
	}
	if info, err := os.Stat(got); err != nil || !info.IsDir() {
		t.Fatalf("data dir not present after AppDataDir(): err=%v", err)
	}
}

func TestAppDataDir_RespectsXDGDataHome(t *testing.T) {
	xdg := t.TempDir()
	t.Setenv("ALTZAP_DATA_DIR", "")
	t.Setenv("XDG_DATA_HOME", xdg)

	got := AppDataDir()
	want := filepath.Join(xdg, "altzap")

	if got != want {
		t.Fatalf("XDG_DATA_HOME not honored: want %q, got %q", want, got)
	}
	if info, err := os.Stat(got); err != nil || !info.IsDir() {
		t.Fatalf("data dir not created: err=%v", err)
	}
}

func TestAppDataDir_DefaultPath(t *testing.T) {
	home := t.TempDir()
	t.Setenv("ALTZAP_DATA_DIR", "")
	t.Setenv("XDG_DATA_HOME", "")
	t.Setenv("HOME", home)

	got := AppDataDir()
	want := filepath.Join(home, ".local", "share", "altzap")

	if got != want {
		t.Fatalf("default path wrong: want %q, got %q", want, got)
	}
	if info, err := os.Stat(got); err != nil || !info.IsDir() {
		t.Fatalf("default data dir not created: err=%v", err)
	}
}

func TestAppConfigDir_RespectsXDGConfigHome(t *testing.T) {
	xdg := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", xdg)

	got := AppConfigDir()
	want := filepath.Join(xdg, "altzap")

	if got != want {
		t.Fatalf("config dir wrong: want %q, got %q", want, got)
	}
	if info, err := os.Stat(got); err != nil || !info.IsDir() {
		t.Fatalf("config dir not created: err=%v", err)
	}
}

func TestMediaDir_IsAppDataDirSlashMedia(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("ALTZAP_DATA_DIR", tmp)

	got := MediaDir()
	want := filepath.Join(tmp, "media")

	if got != want {
		t.Fatalf("media dir wrong: want %q, got %q", want, got)
	}
}
