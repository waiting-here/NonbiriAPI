//go:build unix

package db

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"

	"github.com/waiting-here/NonbiriAPI/internal/secret"
)

func newTestVault(t *testing.T) *secret.Vault {
	t.Helper()
	key := bytes.Repeat([]byte{0x29}, secret.MasterKeyBytes)
	vault, err := secret.New(key)
	clear(key)
	if err != nil {
		t.Fatalf("create vault: %v", err)
	}
	t.Cleanup(func() { _ = vault.Close() })
	return vault
}

// requireFileMode stats path and fails the test if it is missing or its
// permission bits differ from want. Used where the file is expected to exist
// after an explicit write.
func requireFileMode(t *testing.T, path string, want os.FileMode) {
	t.Helper()
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat %s: %v", path, err)
	}
	if info.Mode().Perm() != want {
		t.Errorf("%s mode = %#o, want %#o", path, info.Mode().Perm(), want)
	}
}

// TestOpenSecuresDBFilesUnderWideUmask opens a fresh database under a wide
// umask and verifies the database file and its WAL/SHM sidecars end up
// owner-only (0600). SQLite creates WAL/SHM sidecars with the database file's
// mode, so after Open chmods the database to 0600 every later sidecar creation
// matches 0600 as well; this test confirms the steady state does not depend on
// the process umask.
func TestOpenSecuresDBFilesUnderWideUmask(t *testing.T) {
	old := syscall.Umask(0o022)
	defer syscall.Umask(old)

	path := filepath.Join(privateDBDir(t), "wide-umask.db")
	st := openTestStore(t, path)
	if _, err := st.DB().Exec(`INSERT INTO users (discord_id, username, created_at, updated_at) VALUES ('u', 'u', 1, 1)`); err != nil {
		t.Fatalf("insert to force wal creation: %v", err)
	}
	requireFileMode(t, path, 0o600)
	requireFileMode(t, path+"-wal", 0o600)
	requireFileMode(t, path+"-shm", 0o600)
	if err := st.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}
}

// TestOpenReSecuresOnReopen verifies that closing and reopening the database
// leaves the database file and sidecars owner-only, exercising the close and
// reopen path called out by the contract.
func TestOpenReSecuresOnReopen(t *testing.T) {
	path := filepath.Join(privateDBDir(t), "reopen.db")
	st := openTestStore(t, path)
	if _, err := st.DB().Exec(`INSERT INTO users (discord_id, username, created_at, updated_at) VALUES ('u', 'u', 1, 1)`); err != nil {
		t.Fatalf("insert: %v", err)
	}
	if err := st.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}

	st2 := openTestStore(t, path)
	defer st2.Close()
	if _, err := st2.DB().Exec(`INSERT INTO users (discord_id, username, created_at, updated_at) VALUES ('u2', 'u2', 2, 2)`); err != nil {
		t.Fatalf("insert on reopen: %v", err)
	}
	requireFileMode(t, path, 0o600)
	requireFileMode(t, path+"-wal", 0o600)
	requireFileMode(t, path+"-shm", 0o600)
}

// TestOpenTightensPreExistingWorldReadableDB creates a database file with
// world-readable permissions and verifies Open tightens it to 0600 rather than
// accepting a permissive pre-existing file.
func TestOpenTightensPreExistingWorldReadableDB(t *testing.T) {
	path := filepath.Join(privateDBDir(t), "world.db")
	if err := os.WriteFile(path, nil, 0o644); err != nil {
		t.Fatalf("write empty db: %v", err)
	}
	if err := os.Chmod(path, 0o644); err != nil {
		t.Fatalf("chmod db: %v", err)
	}
	st := openTestStore(t, path)
	defer st.Close()
	requireFileMode(t, path, 0o600)
}

// TestOpenTightensResidualSidecars simulates a crash that left world-readable
// WAL/SHM sidecars next to the database, and verifies Open tightens the
// database and any sidecars that exist to 0600 on the next start.
func TestOpenTightensResidualSidecars(t *testing.T) {
	path := filepath.Join(privateDBDir(t), "residual.db")
	for _, name := range []string{path, path + "-wal", path + "-shm"} {
		if err := os.WriteFile(name, nil, 0o644); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
		if err := os.Chmod(name, 0o644); err != nil {
			t.Fatalf("chmod %s: %v", name, err)
		}
	}
	st := openTestStore(t, path)
	if _, err := st.DB().Exec(`INSERT INTO users (discord_id, username, created_at, updated_at) VALUES ('u', 'u', 1, 1)`); err != nil {
		t.Fatalf("insert: %v", err)
	}
	if err := st.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}

	st2 := openTestStore(t, path)
	defer st2.Close()
	if _, err := st2.DB().Exec(`INSERT INTO users (discord_id, username, created_at, updated_at) VALUES ('u2', 'u2', 2, 2)`); err != nil {
		t.Fatalf("insert on reopen: %v", err)
	}
	requireFileMode(t, path, 0o600)
	requireFileMode(t, path+"-wal", 0o600)
	requireFileMode(t, path+"-shm", 0o600)
}

// TestOpenRejectsGroupAccessibleParentDir verifies Open fails closed when the
// database directory grants any group or other access, regardless of owner.
func TestOpenRejectsGroupAccessibleParentDir(t *testing.T) {
	for _, mode := range []os.FileMode{0o750, 0o755, 0o705} {
		t.Run(fmt.Sprintf("%04o", mode), func(t *testing.T) {
			root := t.TempDir()
			dir := filepath.Join(root, "dbdir")
			if err := os.Mkdir(dir, mode); err != nil {
				t.Fatalf("mkdir: %v", err)
			}
			if err := os.Chmod(dir, mode); err != nil {
				t.Fatalf("chmod: %v", err)
			}
			path := filepath.Join(dir, "x.db")
			if _, err := Open(path, newTestVault(t)); err == nil {
				t.Fatalf("Open accepted a group/other-accessible database directory (mode %04o)", mode)
			}
			if _, err := os.Stat(path); !os.IsNotExist(err) {
				t.Fatalf("Open created a database file despite rejecting the directory: %v", err)
			}
		})
	}
}

// TestOpenRejectsSymlinkDBPath verifies a symlink at the configured database
// path is refused rather than followed, so a later chmod can never land on an
// unrelated target.
func TestOpenRejectsSymlinkDBPath(t *testing.T) {
	dir := privateDBDir(t)
	target := filepath.Join(dir, "real.db")
	if err := os.WriteFile(target, nil, 0o600); err != nil {
		t.Fatalf("write target: %v", err)
	}
	link := filepath.Join(dir, "link.db")
	if err := os.Symlink(target, link); err != nil {
		t.Fatalf("create symlink: %v", err)
	}
	_, err := Open(link, newTestVault(t))
	if err == nil {
		t.Fatal("Open accepted a symlink database path")
	}
	// The 0700 parent lets the failure be provably from the path-shape guard
	// ("database file must not be a symlink"), not the strict parent check.
	if !strings.Contains(err.Error(), "database file must not be a symlink") {
		t.Fatalf("Open failed for the wrong reason (want symlink path rejection, got %q)", err.Error())
	}
}

// assertSidecarSymlinkRejected is shared by the WAL and SHM symlink sidecar
// regression tests. A symlink sidecar is planted pointing at a controlled
// target whose content and mtime are recorded; Open must fail closed and the
// target must be left untouched (SQLite never followed the sidecar before the
// pre-check rejected it).
func assertSidecarSymlinkRejected(t *testing.T, sidecarSuffix string) {
	t.Helper()
	dir := privateDBDir(t)
	path := filepath.Join(dir, "db.db")
	target := filepath.Join(dir, "target"+sidecarSuffix)
	content := []byte("controlled target content, must not change")
	if err := os.WriteFile(target, content, 0o600); err != nil {
		t.Fatalf("write target: %v", err)
	}
	before, err := os.Stat(target)
	if err != nil {
		t.Fatalf("stat target: %v", err)
	}
	beforeMtime := before.ModTime()
	link := path + sidecarSuffix
	if err := os.Symlink(target, link); err != nil {
		t.Fatalf("create symlink sidecar: %v", err)
	}
	_, openErr := Open(path, newTestVault(t))
	if openErr == nil {
		t.Fatalf("Open accepted a symlink %s sidecar", sidecarSuffix)
	}
	// The 0700 parent lets the failure be provably from the sidecar pre-check
	// ("wal file"/"shm file" ... must not be a symlink), run before sql.Open,
	// not from the strict parent check ("database directory").
	if !strings.Contains(openErr.Error(), sidecarSuffix[1:]+" file") {
		t.Fatalf("Open failed for the wrong reason (want %s sidecar rejection, got %q)", sidecarSuffix[1:], openErr.Error())
	}
	// The controlled target must be untouched: content and mtime identical.
	after, err := os.ReadFile(target)
	if err != nil {
		t.Fatalf("read target after Open: %v", err)
	}
	if !bytes.Equal(after, content) {
		t.Fatalf("symlink target content was modified: got %q, want %q", after, content)
	}
	afterInfo, err := os.Stat(target)
	if err != nil {
		t.Fatalf("stat target after Open: %v", err)
	}
	if !afterInfo.ModTime().Equal(beforeMtime) {
		t.Fatalf("symlink target mtime changed: was %v, now %v", beforeMtime, afterInfo.ModTime())
	}
	// The symlink sidecar itself must still be a symlink to the same target;
	// Open must not have replaced or removed it.
	info, err := os.Lstat(link)
	if err != nil {
		t.Fatalf("lstat symlink sidecar after Open: %v", err)
	}
	if info.Mode()&os.ModeSymlink == 0 {
		t.Fatalf("%s sidecar is no longer a symlink: mode = %v", sidecarSuffix, info.Mode())
	}
	if got, err := os.Readlink(link); err != nil || got != target {
		t.Fatalf("%s sidecar link target changed: got %q, want %q (err %v)", sidecarSuffix, got, target, err)
	}
}

// TestOpenRejectsSymlinkWALSidecar verifies a symlink planted at the -wal
// sidecar path is rejected before sql.Open/PRAGMA, so SQLite never follows it
// and the controlled target is not touched.
func TestOpenRejectsSymlinkWALSidecar(t *testing.T) {
	assertSidecarSymlinkRejected(t, "-wal")
}

// TestOpenRejectsSymlinkSHMSidecar verifies the same fail-closed guarantee for
// the -shm sidecar.
func TestOpenRejectsSymlinkSHMSidecar(t *testing.T) {
	assertSidecarSymlinkRejected(t, "-shm")
}

// TestOpenRejectsNonRegularWALSidecar verifies a non-regular file (a directory)
// planted at the -wal sidecar path is rejected before sql.Open, so SQLite does
// not attempt to use it. The sidecar is left in place.
func TestOpenRejectsNonRegularWALSidecar(t *testing.T) {
	dir := privateDBDir(t)
	path := filepath.Join(dir, "db.db")
	walDir := path + "-wal"
	if err := os.Mkdir(walDir, 0o600); err != nil {
		t.Fatalf("mkdir sidecar: %v", err)
	}
	_, err := Open(path, newTestVault(t))
	if err == nil {
		t.Fatal("Open accepted a directory as the -wal sidecar")
	}
	// The 0700 parent lets the failure be provably from the sidecar pre-check
	// ("wal file must be a regular file"), not the strict parent check.
	if !strings.Contains(err.Error(), "wal file") {
		t.Fatalf("Open failed for the wrong reason (want wal sidecar rejection, got %q)", err.Error())
	}
	info, err := os.Lstat(walDir)
	if err != nil {
		t.Fatalf("lstat sidecar after Open: %v", err)
	}
	if !info.IsDir() {
		t.Fatalf("-wal sidecar was changed from a directory: mode = %v", info.Mode())
	}
}

// chdirForTest records the current working directory and returns a cleanup
// that restores it. Tests that exercise a relative database path must not run
// in parallel (none in this package do) because os.Chdir is process-global.
func chdirForTest(t *testing.T, dest string) {
	t.Helper()
	orig, err := os.Getwd()
	if err != nil {
		t.Fatalf("getcwd: %v", err)
	}
	t.Cleanup(func() { _ = os.Chdir(orig) })
	if err := os.Chdir(dest); err != nil {
		t.Fatalf("chdir %s: %v", dest, err)
	}
}

// TestPrepareDBDirectoryRejectsWorldReadableCWD verifies the strict parent
// check now covers the current directory for a relative database path: a
// group/other-accessible CWD fails closed instead of being skipped.
func TestPrepareDBDirectoryRejectsWorldReadableCWD(t *testing.T) {
	root := t.TempDir()
	cwd := filepath.Join(root, "public")
	if err := os.Mkdir(cwd, 0o755); err != nil {
		t.Fatalf("mkdir cwd: %v", err)
	}
	if err := os.Chmod(cwd, 0o755); err != nil {
		t.Fatalf("chmod cwd: %v", err)
	}
	chdirForTest(t, cwd)
	if err := prepareDBDirectory("relative.db"); err == nil {
		t.Fatal("prepareDBDirectory accepted a group/other-accessible current directory for a relative DB path")
	}
	if _, err := os.Stat("relative.db"); !os.IsNotExist(err) {
		t.Fatalf("prepareDBDirectory created a database file despite rejecting the current directory: %v", err)
	}
}

// TestPrepareDBDirectoryAcceptsOwnerOnlyCWD verifies the strict CWD check does
// not over-reject: an owner-only current directory owned by the current user
// is accepted for a relative database path.
func TestPrepareDBDirectoryAcceptsOwnerOnlyCWD(t *testing.T) {
	root := t.TempDir()
	cwd := filepath.Join(root, "owner-only")
	if err := os.Mkdir(cwd, 0o700); err != nil {
		t.Fatalf("mkdir cwd: %v", err)
	}
	if err := os.Chmod(cwd, 0o700); err != nil {
		t.Fatalf("chmod cwd: %v", err)
	}
	chdirForTest(t, cwd)
	if err := prepareDBDirectory("relative.db"); err != nil {
		t.Fatalf("prepareDBDirectory rejected an owner-only current directory: %v", err)
	}
	if _, err := os.Stat("relative.db"); !os.IsNotExist(err) {
		t.Fatalf("prepareDBDirectory created a database file it should only have checked a directory for: %v", err)
	}
}

// TestPrepareDBDirectoryRejectsWorldReadableRoot verifies the strict parent
// check now covers root for a root-level database path: a world-readable root
// fails closed instead of being skipped. The test skips the rare environment
// where root is already owner-only and owned by the current user, since that
// case cannot exercise rejection without creating a file in root.
func TestPrepareDBDirectoryRejectsWorldReadableRoot(t *testing.T) {
	rootInfo, err := os.Stat(string(filepath.Separator))
	if err != nil {
		t.Fatalf("stat root: %v", err)
	}
	if rootInfo.Mode().Perm()&0o077 == 0 {
		if stat, ok := rootInfo.Sys().(*syscall.Stat_t); ok && stat.Uid == uint32(syscall.Geteuid()) {
			t.Skip("root directory is owner-only and owned by the current user; cannot test rejection without creating a file in /")
		}
	}
	const rootDB = string(filepath.Separator) + "nonbiriapi-s6-root-test.db"
	if err := prepareDBDirectory(rootDB); err == nil {
		t.Fatal("prepareDBDirectory accepted a world-readable root directory as the DB parent")
	}
	if _, err := os.Stat(rootDB); !os.IsNotExist(err) {
		t.Fatalf("prepareDBDirectory created a file in / despite rejecting root: %v", err)
	}
}
