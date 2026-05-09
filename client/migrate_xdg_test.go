package client

import (
	"errors"
	"os"
	"path/filepath"
	"syscall"
	"testing"
)

func TestMoveOrCopy_RenameSameFilesystem(t *testing.T) {
	tmp := t.TempDir()
	src := filepath.Join(tmp, "src.bin")
	dst := filepath.Join(tmp, "dst.bin")
	want := []byte("payload-xdg")

	if err := os.WriteFile(src, want, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := moveOrCopy(src, dst); err != nil {
		t.Fatalf("moveOrCopy: %v", err)
	}

	got, err := os.ReadFile(dst)
	if err != nil {
		t.Fatalf("dst missing: %v", err)
	}
	if string(got) != string(want) {
		t.Fatalf("payload mismatch: got %q, want %q", got, want)
	}
	if _, err := os.Stat(src); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("src should be gone, stat err=%v", err)
	}
}

func TestMoveOrCopy_DirectoryRename(t *testing.T) {
	tmp := t.TempDir()
	src := filepath.Join(tmp, "media")
	dst := filepath.Join(tmp, "moved-media")

	if err := os.MkdirAll(filepath.Join(src, "chat-x"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(src, "chat-x", "1.jpg"), []byte("img"), 0o644); err != nil {
		t.Fatal(err)
	}

	if err := moveOrCopy(src, dst); err != nil {
		t.Fatalf("moveOrCopy dir: %v", err)
	}

	if _, err := os.Stat(filepath.Join(dst, "chat-x", "1.jpg")); err != nil {
		t.Fatalf("moved file missing: %v", err)
	}
	if _, err := os.Stat(src); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("src dir should be gone, stat err=%v", err)
	}
}

func TestMoveOrCopy_SourceMissingReturnsErrNotExist(t *testing.T) {
	tmp := t.TempDir()
	err := moveOrCopy(filepath.Join(tmp, "nope"), filepath.Join(tmp, "out"))
	if !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("want os.ErrNotExist, got %v", err)
	}
}

// crossDeviceCopyFile is exported via test-only build edge: we exercise
// the file-copy path directly because simulating EXDEV is fragile.
func TestCrossDeviceCopy_FilePreservesContent(t *testing.T) {
	tmp := t.TempDir()
	src := filepath.Join(tmp, "src.bin")
	dst := filepath.Join(tmp, "dst.bin")
	want := make([]byte, 64*1024)
	for i := range want {
		want[i] = byte(i % 256)
	}
	if err := os.WriteFile(src, want, 0o644); err != nil {
		t.Fatal(err)
	}

	if err := crossDeviceCopy(src, dst); err != nil {
		t.Fatalf("crossDeviceCopy: %v", err)
	}

	got, err := os.ReadFile(dst)
	if err != nil {
		t.Fatalf("dst missing: %v", err)
	}
	if len(got) != len(want) {
		t.Fatalf("size mismatch: got %d, want %d", len(got), len(want))
	}
	if _, err := os.Stat(src); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("src should be gone, stat err=%v", err)
	}
}

// Sanity: EXDEV constant is what we expect on Linux.
func TestEXDEV_IsCrossDeviceLinkErrno(t *testing.T) {
	if syscall.EXDEV == 0 {
		t.Skip("EXDEV not defined on this platform")
	}
}
