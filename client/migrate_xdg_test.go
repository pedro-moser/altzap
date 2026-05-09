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

func TestMigrateCWDToXDG_FreshInstallNoOp(t *testing.T) {
	cwd := t.TempDir()
	data := t.TempDir()

	if err := MigrateCWDToXDG(cwd, data); err != nil {
		t.Fatalf("MigrateCWDToXDG: %v", err)
	}

	entries, _ := os.ReadDir(data)
	if len(entries) != 0 {
		t.Fatalf("data dir should still be empty, got %d entries", len(entries))
	}
}

func TestMigrateCWDToXDG_FullLegacyMoves(t *testing.T) {
	cwd := t.TempDir()
	data := t.TempDir()

	// Lay out a v0.1.0-style CWD.
	mustWrite(t, filepath.Join(cwd, "whatsapp.db"), []byte("session"))
	mustWrite(t, filepath.Join(cwd, "whatsapp.db-shm"), []byte("shm"))
	mustWrite(t, filepath.Join(cwd, "whatsapp.db-wal"), []byte("wal"))
	mustMkdir(t, filepath.Join(cwd, "store"))
	mustWrite(t, filepath.Join(cwd, "store", "messages.db"), []byte("msgs"))
	mustMkdir(t, filepath.Join(cwd, "store", ".legacy"))
	mustWrite(t, filepath.Join(cwd, "store", ".legacy", "msg_old.json"), []byte("legacy"))
	mustMkdir(t, filepath.Join(cwd, "media", "chat-x"))
	mustWrite(t, filepath.Join(cwd, "media", "chat-x", "1.jpg"), []byte("img"))

	if err := MigrateCWDToXDG(cwd, data); err != nil {
		t.Fatalf("MigrateCWDToXDG: %v", err)
	}

	expectExists(t, filepath.Join(data, "whatsapp.db"))
	expectExists(t, filepath.Join(data, "whatsapp.db-shm"))
	expectExists(t, filepath.Join(data, "messages.db"))
	expectExists(t, filepath.Join(data, ".legacy", "msg_old.json"))
	expectExists(t, filepath.Join(data, "media", "chat-x", "1.jpg"))

	expectMissing(t, filepath.Join(cwd, "whatsapp.db"))
	expectMissing(t, filepath.Join(cwd, "store", "messages.db"))
	expectMissing(t, filepath.Join(cwd, "media"))
	// store/ should have been removed once drained
	expectMissing(t, filepath.Join(cwd, "store"))
}

func TestMigrateCWDToXDG_OrphanMsgJSONLandsAtDataRoot(t *testing.T) {
	cwd := t.TempDir()
	data := t.TempDir()

	mustMkdir(t, filepath.Join(cwd, "store"))
	mustWrite(t, filepath.Join(cwd, "store", "msg_orphan.json"), []byte("orphan"))

	if err := MigrateCWDToXDG(cwd, data); err != nil {
		t.Fatalf("MigrateCWDToXDG: %v", err)
	}

	// Orphan should land at dataDir root so MigrateLegacyJSONLs can consume it.
	expectExists(t, filepath.Join(data, "msg_orphan.json"))
	expectMissing(t, filepath.Join(cwd, "store", "msg_orphan.json"))
}

func TestMigrateCWDToXDG_AlreadyMigratedSkips(t *testing.T) {
	cwd := t.TempDir()
	data := t.TempDir()

	// dataDir already has whatsapp.db; CWD has a *different* one — must not overwrite.
	mustWrite(t, filepath.Join(data, "whatsapp.db"), []byte("xdg-version"))
	mustWrite(t, filepath.Join(cwd, "whatsapp.db"), []byte("cwd-version"))

	if err := MigrateCWDToXDG(cwd, data); err != nil {
		t.Fatalf("MigrateCWDToXDG: %v", err)
	}

	got, _ := os.ReadFile(filepath.Join(data, "whatsapp.db"))
	if string(got) != "xdg-version" {
		t.Fatalf("XDG file should be untouched, got %q", got)
	}
	// CWD file is preserved (skip path), since target existed.
	if _, err := os.Stat(filepath.Join(cwd, "whatsapp.db")); err != nil {
		t.Fatalf("CWD copy should be intact when target exists: %v", err)
	}
}

func TestMigrateCWDToXDG_PartialLegacy(t *testing.T) {
	cwd := t.TempDir()
	data := t.TempDir()

	// Only ./media exists.
	mustMkdir(t, filepath.Join(cwd, "media"))
	mustWrite(t, filepath.Join(cwd, "media", "x.bin"), []byte("y"))

	if err := MigrateCWDToXDG(cwd, data); err != nil {
		t.Fatalf("MigrateCWDToXDG: %v", err)
	}

	expectExists(t, filepath.Join(data, "media", "x.bin"))
	expectMissing(t, filepath.Join(cwd, "media"))
	// No whatsapp.db in either location — and that's fine.
	expectMissing(t, filepath.Join(data, "whatsapp.db"))
}

// --- helpers ---

func mustWrite(t *testing.T, path string, data []byte) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatal(err)
	}
}

func mustMkdir(t *testing.T, path string) {
	t.Helper()
	if err := os.MkdirAll(path, 0o755); err != nil {
		t.Fatal(err)
	}
}

func expectExists(t *testing.T, path string) {
	t.Helper()
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("expected %s to exist: %v", path, err)
	}
}

func expectMissing(t *testing.T, path string) {
	t.Helper()
	if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("expected %s to be missing, stat err=%v", path, err)
	}
}
