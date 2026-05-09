package client

import (
	"errors"
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"
	"syscall"
)

// moveOrCopy moves src to dst. Tries os.Rename first (instant on the
// same filesystem). On EXDEV (different filesystems — e.g. home on ZFS,
// CWD on tmpfs) it falls back to a copy + verify + remove.
//
// For directories the cross-device path walks recursively. In practice
// EXDEV is rare; the fast path covers ~99% of users.
func moveOrCopy(src, dst string) error {
	if _, err := os.Stat(src); err != nil {
		return err
	}
	if err := os.Rename(src, dst); err == nil {
		return nil
	} else if !errors.Is(err, syscall.EXDEV) {
		return fmt.Errorf("rename %s -> %s: %w", src, dst, err)
	}

	info, err := os.Stat(src)
	if err != nil {
		return err
	}
	if info.IsDir() {
		return crossDeviceCopyDir(src, dst)
	}
	return crossDeviceCopy(src, dst)
}

// crossDeviceCopy copies a single file src to dst via a `.tmp`
// intermediate, fsyncs, renames it into place, then removes src.
// Verifies the copy size before removing the source.
func crossDeviceCopy(src, dst string) error {
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return err
	}
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()

	tmp := dst + ".tmp"
	out, err := os.Create(tmp)
	if err != nil {
		return err
	}

	written, err := io.Copy(out, in)
	if syncErr := out.Sync(); err == nil {
		err = syncErr
	}
	if cerr := out.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("copy %s -> %s: %w", src, dst, err)
	}

	srcInfo, err := os.Stat(src)
	if err != nil {
		_ = os.Remove(tmp)
		return err
	}
	if written != srcInfo.Size() {
		_ = os.Remove(tmp)
		return fmt.Errorf("copy size mismatch: wrote %d, src is %d", written, srcInfo.Size())
	}

	if err := os.Rename(tmp, dst); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	return os.Remove(src)
}

// crossDeviceCopyDir copies a directory tree recursively to dst, then
// removes src. Stops at first error and leaves partial state for human
// inspection (does NOT remove src on failure).
func crossDeviceCopyDir(src, dst string) error {
	err := filepath.WalkDir(src, func(p string, d os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		rel, err := filepath.Rel(src, p)
		if err != nil {
			return err
		}
		target := filepath.Join(dst, rel)
		if d.IsDir() {
			return os.MkdirAll(target, 0o755)
		}
		return crossDeviceCopy(p, target)
	})
	if err != nil {
		return err
	}
	return os.RemoveAll(src)
}

// MigrateCWDToXDG performs a one-shot v0.1.0 → v0.2 data migration.
// It moves any legacy artifacts found in legacyDir into dataDir using
// the XDG-flat layout described in the design spec.
//
// Idempotent: if the target already exists it is left alone (no
// overwrite). Missing sources are silently skipped. Cross-device
// renames fall back to copy via moveOrCopy.
//
// Returns nil on success; an error means startup should NOT proceed
// (partial state may exist on disk for inspection).
func MigrateCWDToXDG(legacyDir, dataDir string) error {
	if err := os.MkdirAll(dataDir, 0o755); err != nil {
		return fmt.Errorf("mkdir data dir: %w", err)
	}

	// Top-level files in legacyDir that map to same name in dataDir.
	topLevel := []string{
		"whatsapp.db",
		"whatsapp.db-shm",
		"whatsapp.db-wal",
	}
	for _, name := range topLevel {
		if err := migrateOne(filepath.Join(legacyDir, name), filepath.Join(dataDir, name)); err != nil {
			return err
		}
	}

	// ./media -> dataDir/media (whole directory).
	if err := migrateOne(filepath.Join(legacyDir, "media"), filepath.Join(dataDir, "media")); err != nil {
		return err
	}

	// Drain ./store/ — every entry moves to dataDir/<same-name>. This
	// covers messages.db (+ sidecars), .legacy/, and any orphan
	// msg_*.json files left by the v0.1 JSONL→SQLite migration.
	storeDir := filepath.Join(legacyDir, "store")
	if err := drainDir(storeDir, dataDir); err != nil {
		return err
	}

	return nil
}

// migrateOne moves src to dst, skipping if dst exists or src is missing.
func migrateOne(src, dst string) error {
	if _, err := os.Stat(dst); err == nil {
		return nil // already migrated
	} else if !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("stat %s: %w", dst, err)
	}
	if _, err := os.Stat(src); errors.Is(err, os.ErrNotExist) {
		return nil // nothing to migrate
	} else if err != nil {
		return fmt.Errorf("stat %s: %w", src, err)
	}
	if err := moveOrCopy(src, dst); err != nil {
		return err
	}
	log.Printf("migrated %s -> %s", src, dst)
	return nil
}

// drainDir moves every entry from src into dst (one level deep). If src
// becomes empty afterwards, it is removed. If src does not exist, this
// is a no-op.
func drainDir(src, dst string) error {
	entries, err := os.ReadDir(src)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("read %s: %w", src, err)
	}
	for _, e := range entries {
		from := filepath.Join(src, e.Name())
		to := filepath.Join(dst, e.Name())
		if err := migrateOne(from, to); err != nil {
			return err
		}
	}
	// Try to remove src if it ended up empty; ignore error.
	_ = os.Remove(src)
	return nil
}
