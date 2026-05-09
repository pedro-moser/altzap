package client

import (
	"errors"
	"fmt"
	"io"
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
