//go:build linux && amd64

package db

import (
	"errors"
	"fmt"
	"os"
	"syscall"
)

// secureDBParentDir verifies the configured final parent itself rather than a
// symlink target. The common preflight has already walked every existing
// component before MkdirAll; using Lstat here preserves the same static
// no-follow rule for callers that invoke this helper directly. This is an
// inspection-time check, not descriptor binding for a later SQLite xOpen or a
// guarantee against a trusted local parent-directory race.
func secureDBParentDir(dir string) error {
	info, err := os.Lstat(dir)
	if err != nil {
		return fmt.Errorf("inspect database directory: %w", err)
	}
	if err := validateDBParentPath(dir, info); err != nil {
		return err
	}
	if info.Mode().Perm()&0o077 != 0 {
		return fmt.Errorf("database directory must grant no group or other access")
	}
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		return fmt.Errorf("verify database directory ownership: unsupported stat type")
	}
	if stat.Uid != uint32(syscall.Geteuid()) {
		return fmt.Errorf("database directory must be owned by the current user")
	}
	return nil
}

// secureDBFiles chmods the database file and its WAL/SHM sidecars to 0600 and
// fstat-verifies each is owned by the current effective user with exactly that
// mode. Each file is opened with O_NOFOLLOW so a symlink substituted at the
// configured pathname is refused rather than followed: chmod never lands on an
// unrelated target during this operation. This does not bind a later SQLite
// xOpen or defend a trusted local parent-directory race. Files that do not yet
// exist (for example a -wal that SQLite has not created) are skipped; the
// integration test covers the modes once they are actually produced. Any
// failure fails closed.
func secureDBFiles(path string) error {
	files := []struct {
		path string
		role string
	}{
		{path: path, role: "database file"},
		{path: path + "-wal", role: "wal file"},
		{path: path + "-shm", role: "shm file"},
	}
	for _, f := range files {
		if err := secureOneDBFile(f.path, f.role); err != nil {
			return err
		}
	}
	return nil
}

func secureOneDBFile(path, role string) error {
	if _, err := os.Lstat(path); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return fmt.Errorf("inspect %s: %w", role, err)
	}
	// O_NOFOLLOW refuses a final-component symlink, so this metadata operation
	// cannot fchmod a substituted target. It is not a promise that SQLite's
	// later pathname open is bound to this descriptor.
	fd, err := os.OpenFile(path, os.O_RDONLY|syscall.O_NOFOLLOW, 0)
	if err != nil {
		return fmt.Errorf("open %s for permission check: %w", role, err)
	}
	defer func() { _ = fd.Close() }()

	if err := syscall.Fchmod(int(fd.Fd()), 0o600); err != nil {
		return fmt.Errorf("set %s to owner-only permissions: %w", role, err)
	}
	info, err := fd.Stat()
	if err != nil {
		return fmt.Errorf("verify %s permissions: %w", role, err)
	}
	if info.Mode().Perm() != 0o600 {
		return fmt.Errorf("%s permissions are not owner-only after chmod", role)
	}
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		return fmt.Errorf("verify %s ownership: unsupported stat type", role)
	}
	if stat.Uid != uint32(syscall.Geteuid()) {
		return fmt.Errorf("%s must be owned by the current user", role)
	}
	return nil
}
