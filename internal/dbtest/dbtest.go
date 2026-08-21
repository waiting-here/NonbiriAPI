// Package dbtest holds shared test helpers for packages that open a
// NonbiriAPI SQLite database in tests. It is test infrastructure: production
// code must not depend on it.
package dbtest

import (
	"os"
	"path/filepath"
	"testing"
)

// EnsureOwnerOnlyParent makes the parent directory of path owner-only (0700)
// when it already exists and grants any group or other access, so db.Open's
// strict database-parent check (implementation contract §8.5, Unix) succeeds.
//
// Go's testing.T.TempDir() creates its returned subdirectory with
// os.Mkdir(.., 0o777); under a normal umask (0022) that is 0755 and grants
// group/other access, which the strict check rejects on Unix (the check is a
// no-op on Windows). Rather than rely on the Windows no-op or weaken the
// check, tests that expect db.Open to succeed chmod the parent to 0700 first,
// so the check passes for the right reason. A parent that does not yet exist
// is left for db.Open to create owner-only, preserving tests that exercise
// db.Open creating a nested directory.
//
// Tests that deliberately use a permissive parent to exercise the failure path
// must call db.Open directly and must not use this helper.
func EnsureOwnerOnlyParent(t testing.TB, path string) {
	t.Helper()
	dir := filepath.Dir(path)
	if dir == "." || dir == "" || dir == string(filepath.Separator) {
		return
	}
	info, err := os.Stat(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return
		}
		t.Fatalf("dbtest: stat database parent %s: %v", dir, err)
	}
	if !info.IsDir() {
		t.Fatalf("dbtest: database parent %s is not a directory", dir)
	}
	if info.Mode().Perm()&0o077 == 0 {
		return
	}
	if err := os.Chmod(dir, 0o700); err != nil {
		t.Fatalf("dbtest: chmod database parent %s to 0700: %v", dir, err)
	}
}
