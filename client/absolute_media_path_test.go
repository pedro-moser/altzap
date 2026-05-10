package client

import (
	"path/filepath"
	"testing"
)

func TestAbsoluteMediaPath_LeavesAbsoluteUnchanged(t *testing.T) {
	abs := "/home/pedro/.local/share/altzap/media/foo@g.us/abc.jpg"
	if got := AbsoluteMediaPath(abs); got != abs {
		t.Fatalf("absolute path mutated: want %q, got %q", abs, got)
	}
}

func TestAbsoluteMediaPath_PrependsDataDirToRelative(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("ALTZAP_DATA_DIR", tmp)

	rel := "media/120363@g.us/AC56.jpg"
	want := filepath.Join(tmp, rel)

	if got := AbsoluteMediaPath(rel); got != want {
		t.Fatalf("relative path not resolved: want %q, got %q", want, got)
	}
}

func TestAbsoluteMediaPath_EmptyPassesThrough(t *testing.T) {
	if got := AbsoluteMediaPath(""); got != "" {
		t.Fatalf("empty path should pass through, got %q", got)
	}
}
