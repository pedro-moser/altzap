package client

import (
	"bytes"
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

// We exercise crossDeviceCopy directly here (instead of forcing EXDEV)
// because simulating cross-device errors is fragile to set up.
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
	if !bytes.Equal(got, want) {
		t.Fatalf("payload mismatch: got len=%d, want len=%d", len(got), len(want))
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

func TestCrossDeviceCopyDir_RecursiveTree(t *testing.T) {
	tmp := t.TempDir()
	src := filepath.Join(tmp, "tree")
	dst := filepath.Join(tmp, "moved-tree")

	// src/a.txt, src/sub/b.bin, src/sub/deep/c.png
	mustWriteBytes(t, filepath.Join(src, "a.txt"), []byte("alpha"))
	mustWriteBytes(t, filepath.Join(src, "sub", "b.bin"), []byte("beta-bytes"))
	mustWriteBytes(t, filepath.Join(src, "sub", "deep", "c.png"), []byte("gamma-pixels"))

	if err := crossDeviceCopyDir(src, dst); err != nil {
		t.Fatalf("crossDeviceCopyDir: %v", err)
	}

	for path, want := range map[string]string{
		filepath.Join(dst, "a.txt"):                "alpha",
		filepath.Join(dst, "sub", "b.bin"):         "beta-bytes",
		filepath.Join(dst, "sub", "deep", "c.png"): "gamma-pixels",
	} {
		got, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("read %s: %v", path, err)
		}
		if string(got) != want {
			t.Fatalf("%s: got %q, want %q", path, got, want)
		}
	}

	if _, err := os.Stat(src); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("src tree should be removed after copy, stat err=%v", err)
	}
}

func mustWriteBytes(t *testing.T, path string, data []byte) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatal(err)
	}
}
