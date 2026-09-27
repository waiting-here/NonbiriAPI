//go:build linux && amd64

package db

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	sqlite "modernc.org/sqlite"
)

const (
	posixLockChildMode = "NONBIRI_TEST_POSIX_LOCK_CHILD_MODE"
	posixLockChildPath = "NONBIRI_TEST_POSIX_LOCK_CHILD_PATH"

	// SQLite's default Unix VFS holds a SHARED lock on one byte of this range
	// for the lifetime of a WAL connection. The WAL-index locks are byte ranges
	// of the separate -shm inode.
	mainSharedFirst int64 = 0x40000000 + 2
	mainSharedSize  int64 = 510
	shmWriterByte   int64 = 120
	shmDeadmanByte  int64 = 128

	probeFree     = 0
	probeConflict = 3
	probeFailure  = 4
)

// TestSQLitePOSIXLockProbeProcess is re-executed by the parent test. A POSIX
// lock probe must run in another process: a descriptor in the SQLite process
// could clear that process's locks simply by closing.
func TestSQLitePOSIXLockProbeProcess(t *testing.T) {
	mode := os.Getenv(posixLockChildMode)
	if mode == "" {
		return
	}
	path := os.Getenv(posixLockChildPath)
	var code int
	switch mode {
	case "main_shared":
		code = probePOSIXByteRange(path, mainSharedFirst, mainSharedSize)
	case "shm_deadman":
		code = probePOSIXByteRange(path+"-shm", shmDeadmanByte, 1)
	case "shm_writer":
		code = probePOSIXByteRange(path+"-shm", shmWriterByte, 1)
	case "sqlite_reader", "sqlite_writer":
		code = probeSQLitePeer(path, mode)
	case "leave_committed_wal":
		code = leaveCommittedWAL(path)
	default:
		fmt.Fprintln(os.Stderr, "unknown lock probe mode")
		code = probeFailure
	}
	os.Exit(code)
}

func probePOSIXByteRange(path string, start, length int64) int {
	fd, err := syscall.Open(path, syscall.O_RDWR|syscall.O_CLOEXEC, 0)
	if err != nil {
		fmt.Fprintln(os.Stderr, "open probe file:", err)
		return probeFailure
	}
	lock := syscall.Flock_t{Type: syscall.F_WRLCK, Whence: int16(0), Start: start, Len: length}
	err = syscall.FcntlFlock(uintptr(fd), syscall.F_SETLK, &lock)
	if err == nil {
		lock.Type = syscall.F_UNLCK
		if err = syscall.FcntlFlock(uintptr(fd), syscall.F_SETLK, &lock); err != nil {
			fmt.Fprintln(os.Stderr, "release probe lock:", err)
			_ = syscall.Close(fd)
			return probeFailure
		}
	}
	if closeErr := syscall.Close(fd); closeErr != nil {
		fmt.Fprintln(os.Stderr, "close probe file:", closeErr)
		return probeFailure
	}
	switch {
	case err == nil:
		return probeFree
	case errors.Is(err, syscall.EACCES), errors.Is(err, syscall.EAGAIN):
		return probeConflict
	default:
		fmt.Fprintln(os.Stderr, "fcntl probe:", err)
		return probeFailure
	}
}

func probeSQLitePeer(path, mode string) int {
	database, err := openSQLite(path, "rw")
	if err != nil {
		fmt.Fprintln(os.Stderr, "open SQLite peer:", err)
		return probeFailure
	}
	defer database.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if _, err := database.ExecContext(ctx, `PRAGMA busy_timeout=100`); err != nil {
		fmt.Fprintln(os.Stderr, "set SQLite peer busy timeout:", err)
		return probeFailure
	}
	if mode == "sqlite_reader" {
		var count int
		if err := database.QueryRowContext(ctx, `SELECT count(*) FROM site_config`).Scan(&count); err != nil {
			fmt.Fprintln(os.Stderr, "SQLite peer read:", err)
			return probeFailure
		}
		return probeFree
	}
	if _, err := database.ExecContext(ctx, `BEGIN IMMEDIATE`); err == nil {
		if _, rollbackErr := database.ExecContext(ctx, `ROLLBACK`); rollbackErr != nil {
			fmt.Fprintln(os.Stderr, "SQLite peer rollback:", rollbackErr)
			return probeFailure
		}
		return probeFree
	} else {
		var sqliteErr *sqlite.Error
		if errors.As(err, &sqliteErr) && sqliteErr.Code()&0xff == 5 { // SQLITE_BUSY
			return probeConflict
		}
		fmt.Fprintln(os.Stderr, "SQLite peer writer:", err)
		return probeFailure
	}
}

// The child deliberately exits without sql.DB.Close after a committed write.
// SQLite then leaves a genuine WAL and shared-memory file in this disposable
// fixture, without corrupting or truncating either file by hand.
func leaveCommittedWAL(path string) int {
	database, err := openSQLite(path, "rw")
	if err != nil {
		fmt.Fprintln(os.Stderr, "open WAL fixture:", err)
		return probeFailure
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if _, err := database.ExecContext(ctx, `PRAGMA wal_autocheckpoint=0`); err != nil {
		fmt.Fprintln(os.Stderr, "disable fixture auto-checkpoint:", err)
		return probeFailure
	}
	result, err := database.ExecContext(ctx, `UPDATE site_config SET updated_at=updated_at+1 WHERE key='site_name'`)
	if err != nil {
		fmt.Fprintln(os.Stderr, "commit WAL fixture change:", err)
		return probeFailure
	}
	if affected, err := result.RowsAffected(); err != nil || affected != 1 {
		fmt.Fprintln(os.Stderr, "WAL fixture update affected unexpected rows:", affected, err)
		return probeFailure
	}
	return probeFree
}

func runSQLiteLockChild(t *testing.T, path, mode string) int {
	t.Helper()
	command := exec.Command(os.Args[0], "-test.run=^TestSQLitePOSIXLockProbeProcess$", "-test.timeout=15s")
	command.Env = append(os.Environ(), posixLockChildMode+"="+mode, posixLockChildPath+"="+path)
	output, err := command.CombinedOutput()
	if err == nil {
		return probeFree
	}
	var exit *exec.ExitError
	if errors.As(err, &exit) {
		if exit.ExitCode() == probeConflict {
			return probeConflict
		}
		t.Fatalf("%s child exit=%d: %s", mode, exit.ExitCode(), strings.TrimSpace(string(output)))
	}
	t.Fatalf("start %s child: %v", mode, err)
	return probeFailure
}

func requireLockState(t *testing.T, path, mode string, want int) {
	t.Helper()
	if got := runSQLiteLockChild(t, path, mode); got != want {
		t.Fatalf("%s fcntl/SQLite result=%s, want %s", mode, lockStateName(got), lockStateName(want))
	}
}

func lockStateName(code int) string {
	switch code {
	case probeFree:
		return "acquired"
	case probeConflict:
		return "conflict"
	default:
		return strconv.Itoa(code)
	}
}

// This control establishes that the probe observes real modernc SQLite
// locks on this kernel when no application preflight touches the same inode.
// It cannot substitute for the Open-path regression below.
func TestSQLitePOSIXLockProbeControl(t *testing.T) {
	if sqlite.OFDLockingEnabled() {
		t.Fatal("required POSIX regression cannot run with OFD locking enabled")
	}
	path := bootstrapTestPath(t, "probe-control.sqlite")
	database, err := openSQLite(path, "rwc")
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	if _, err := database.Exec(`CREATE TABLE site_config (key TEXT PRIMARY KEY); INSERT INTO site_config(key) VALUES('control')`); err != nil {
		t.Fatalf("create control database: %v", err)
	}
	var mode string
	if err := database.QueryRow(`PRAGMA journal_mode=WAL`).Scan(&mode); err != nil || mode != "wal" {
		t.Fatalf("enable control WAL: mode=%q err=%v", mode, err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	connection, err := database.Conn(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer connection.Close()
	if _, err := connection.ExecContext(ctx, `BEGIN`); err != nil {
		t.Fatal(err)
	}
	defer func() { _, _ = connection.ExecContext(ctx, `ROLLBACK`) }()
	var count int
	if err := connection.QueryRowContext(ctx, `SELECT count(*) FROM site_config`).Scan(&count); err != nil {
		t.Fatal(err)
	}
	requireLockState(t, path, "main_shared", probeConflict)
	requireLockState(t, path, "shm_deadman", probeConflict)
	if _, err := connection.ExecContext(ctx, `ROLLBACK`); err != nil {
		t.Fatal(err)
	}
	if _, err := connection.ExecContext(ctx, `BEGIN IMMEDIATE`); err != nil {
		t.Fatal(err)
	}
	requireLockState(t, path, "shm_writer", probeConflict)
	if _, err := connection.ExecContext(ctx, `ROLLBACK`); err != nil {
		t.Fatal(err)
	}
	if err := connection.Close(); err != nil {
		t.Fatal(err)
	}
	if err := database.Close(); err != nil {
		t.Fatal(err)
	}
	requireLockState(t, path, "main_shared", probeFree)
}

// TestSQLitePOSIXLocksFromActualOpen checks kernel-visible locks held by the
// application's real Open path. Auxiliary descriptor closes on the active
// main or SHM inode must make this regression fail.
func TestSQLitePOSIXLocksFromActualOpen(t *testing.T) {
	if sqlite.OFDLockingEnabled() {
		t.Fatal("required POSIX regression cannot run with OFD locking enabled")
	}
	for _, fixture := range []string{"fresh", "current", "persistent_wal"} {
		t.Run(fixture, func(t *testing.T) {
			path := bootstrapTestPath(t, fixture+".sqlite")
			vault := bootstrapTestVault(t)
			if fixture != "fresh" {
				first, err := Open(path, vault)
				if err != nil {
					t.Fatalf("create current fixture: %v", err)
				}
				if err := first.Close(); err != nil {
					t.Fatalf("close current fixture: %v", err)
				}
			}
			if fixture == "persistent_wal" {
				requireLockState(t, path, "leave_committed_wal", probeFree)
				for _, suffix := range []string{"-wal", "-shm"} {
					info, err := os.Stat(path + suffix)
					if err != nil || !info.Mode().IsRegular() || info.Size() == 0 {
						t.Fatalf("persistent %s fixture missing or empty: info=%v err=%v", suffix, info, err)
					}
				}
			}

			for opening := 0; opening < 2; opening++ {
				store, err := Open(path, vault)
				if err != nil {
					t.Fatalf("Open attempt %d: %v", opening+1, err)
				}
				defer func() { _ = store.Close() }()
				checkActualSQLiteLocks(t, path, store)
				if err := store.Close(); err != nil {
					t.Fatalf("Close attempt %d: %v", opening+1, err)
				}
				requireLockState(t, path, "main_shared", probeFree)
				if _, err := os.Stat(path + "-shm"); err == nil {
					requireLockState(t, path, "shm_deadman", probeFree)
				} else if !errors.Is(err, os.ErrNotExist) {
					t.Fatalf("inspect closed SHM file: %v", err)
				}
			}
		})
	}
}

func checkActualSQLiteLocks(t *testing.T, path string, store *Store) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	connection, err := store.DB().Conn(ctx)
	if err != nil {
		t.Fatalf("hold application SQLite connection: %v", err)
	}
	defer connection.Close()
	if _, err := connection.ExecContext(ctx, `BEGIN`); err != nil {
		t.Fatalf("begin application read transaction: %v", err)
	}
	defer func() { _, _ = connection.ExecContext(ctx, `ROLLBACK`) }()
	var count int
	if err := connection.QueryRowContext(ctx, `SELECT count(*) FROM site_config`).Scan(&count); err != nil {
		t.Fatalf("establish application read transaction: %v", err)
	}
	requireLockState(t, path, "main_shared", probeConflict)
	requireLockState(t, path, "shm_deadman", probeConflict)
	requireLockState(t, path, "sqlite_reader", probeFree)
	if _, err := connection.ExecContext(ctx, `ROLLBACK`); err != nil {
		t.Fatalf("end application read transaction: %v", err)
	}
	if _, err := connection.ExecContext(ctx, `BEGIN IMMEDIATE`); err != nil {
		t.Fatalf("begin application write transaction: %v", err)
	}
	requireLockState(t, path, "shm_writer", probeConflict)
	requireLockState(t, path, "sqlite_reader", probeFree)
	requireLockState(t, path, "sqlite_writer", probeConflict)
	if _, err := connection.ExecContext(ctx, `ROLLBACK`); err != nil {
		t.Fatalf("end application write transaction: %v", err)
	}
}
