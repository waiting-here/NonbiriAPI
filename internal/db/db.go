package db

import (
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"

	"github.com/waiting-here/NonbiriAPI/internal/secret"

	_ "modernc.org/sqlite"
)

// Store wraps the SQLite handle and the recoverable-secret boundary. The
// caller owns the Codec lifecycle; typed repositories use it internally so
// plaintext never has to become a database argument or a Store field.
type Store struct {
	db      *sql.DB
	secrets secret.Codec
}

// Open opens (creating if necessary) the SQLite database at path, applies
// pragmas, and bootstraps the schema idempotently. A non-nil secret Codec is
// mandatory so no production Store can persist endpoint credentials without
// an authenticated-encryption boundary.
func Open(path string, secrets secret.Codec) (*Store, error) {
	if nilSecretCodec(secrets) {
		return nil, fmt.Errorf("open database: secret codec is required")
	}
	if err := prepareDBDirectory(path); err != nil {
		return nil, err
	}
	if err := validateDBPathShape(path); err != nil {
		return nil, err
	}
	// Pre-check existing WAL/SHM sidecars before sql.Open/PRAGMA so SQLite
	// cannot follow a malicious symlink or non-regular sidecar before
	// secureDBFiles tightens permissions.
	if err := validateDBSidecarPaths(path); err != nil {
		return nil, err
	}

	d, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, fmt.Errorf("open sqlite: %w", err)
	}
	// A single connection avoids per-request "database is locked" surprises.
	// SQLite's write model is serial regardless; concurrency is gained over
	// many goroutines sharing this one handle in WAL mode.
	d.SetMaxOpenConns(1)

	// foreign_keys=ON makes ON DELETE CASCADE genuine; without it the
	// cascade invariants (endpoint -> endpoint_keys -> fetched_models /
	// bindings, user -> everything) silently no-op. busy_timeout absorbs
	// brief writer contention. WAL improves crash recovery and reads.
	if _, err := d.Exec(`PRAGMA journal_mode=WAL; PRAGMA foreign_keys=ON; PRAGMA busy_timeout=5000;`); err != nil {
		_ = d.Close()
		return nil, fmt.Errorf("apply pragmas: %w", err)
	}

	if _, err := d.Exec(schema); err != nil {
		_ = d.Close()
		return nil, fmt.Errorf("apply schema: %w", err)
	}
	if err := ensureUsersGuildColumns(d); err != nil {
		_ = d.Close()
		return nil, fmt.Errorf("migrate users guild columns: %w", err)
	}
	if err := ensureUsersTemporalBanColumns(d); err != nil {
		_ = d.Close()
		return nil, fmt.Errorf("migrate users temporal ban columns: %w", err)
	}

	// Enforce owner-only permissions on the database file and its WAL/SHM
	// sidecars after SQLite has created them. On Unix this chmod+fstat-verifies
	// each file and fails closed when owner-only access cannot be guaranteed,
	// so startup no longer depends on the process umask. On Windows this is a
	// no-op; ACLs remain the operator's responsibility (see deployment docs).
	if err := secureDBFiles(path); err != nil {
		_ = d.Close()
		return nil, err
	}

	return &Store{db: d, secrets: secrets}, nil
}

// prepareDBDirectory creates the database parent directory owner-only when the
// path names a distinct directory, then verifies the actual parent directory
// is owned by the current effective user and grants no group or other access.
// The parent is resolved to an absolute path first, so the check covers the
// current directory for a relative path and root for a root-level path, per the
// implementation contract §8.5; the check is not skipped for either. A
// permissive parent fails closed rather than relying on the process umask. A
// new directory is created 0700, which no umask can widen with group or other
// bits (0700 only carries owner bits).
func prepareDBDirectory(path string) error {
	dir := filepath.Dir(path)
	// Only create a distinct directory; the current directory (a relative
	// path's parent) and root already exist and must not be created here.
	if dir != "." && dir != "" && dir != string(filepath.Separator) {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			return fmt.Errorf("create database directory: %w", err)
		}
	}
	// Resolve the actual parent to an absolute path so the ownership/mode
	// check covers the current directory for a relative path and root for a
	// root-level path, rather than skipping either as the old behavior did.
	absDir, err := filepath.Abs(dir)
	if err != nil {
		return fmt.Errorf("resolve database directory: %w", err)
	}
	return secureDBParentDir(absDir)
}

// requireRegularDBPath rejects an existing path that is a symlink or a
// non-regular file; a path that does not yet exist is allowed. A symlink is
// refused rather than followed, so neither SQLite nor a later chmod can land
// on an unrelated target (the contract forbids following a symlink to chmod
// its target).
func requireRegularDBPath(path, role string) error {
	info, err := os.Lstat(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return fmt.Errorf("inspect %s: %w", role, err)
	}
	mode := info.Mode()
	if mode&os.ModeSymlink != 0 {
		return fmt.Errorf("%s must not be a symlink", role)
	}
	if !mode.IsRegular() {
		return fmt.Errorf("%s must be a regular file", role)
	}
	return nil
}

// validateDBPathShape rejects a configured database path that already exists
// as a symlink or non-regular file. A symlink at the database path is refused
// rather than followed, so a later chmod can never land on an unrelated target
// (the contract forbids following a symlink to chmod its target). A path that
// does not yet exist is allowed; SQLite creates it.
func validateDBPathShape(path string) error {
	return requireRegularDBPath(path, "database file")
}

// validateDBSidecarPaths rejects an existing WAL/SHM sidecar that is a symlink
// or non-regular file. This pre-check runs before sql.Open/PRAGMA so SQLite
// cannot follow a malicious sidecar before secureDBFiles tightens permissions.
// Sidecars that do not yet exist are allowed; SQLite creates them, and
// secureDBFiles chmod+fstat-verifies them afterwards (on Unix).
func validateDBSidecarPaths(path string) error {
	if err := requireRegularDBPath(path+"-wal", "wal file"); err != nil {
		return err
	}
	return requireRegularDBPath(path+"-shm", "shm file")
}

// ensureUsersGuildColumns adds guild_nick and guild_avatar_url to a users
// table created before they existed. CREATE TABLE IF NOT EXISTS does not
// alter an existing table, so an alpha database on an earlier schema is
// migrated in place. The PRAGMA check makes it idempotent.
func ensureUsersGuildColumns(d *sql.DB) error {
	rows, err := d.Query(`PRAGMA table_info(users)`)
	if err != nil {
		return err
	}
	hasGuildNick := false
	hasGuildAvatarURL := false
	for rows.Next() {
		var cid int
		var name, ctype string
		var notnull, pk int
		var dflt sql.NullString
		if err := rows.Scan(&cid, &name, &ctype, &notnull, &dflt, &pk); err != nil {
			rows.Close()
			return err
		}
		switch name {
		case "guild_nick":
			hasGuildNick = true
		case "guild_avatar_url":
			hasGuildAvatarURL = true
		}
	}
	if err := rows.Err(); err != nil {
		return err
	}
	if !hasGuildNick {
		if _, err := d.Exec(`ALTER TABLE users ADD COLUMN guild_nick TEXT NOT NULL DEFAULT ''`); err != nil {
			return err
		}
	}
	if !hasGuildAvatarURL {
		if _, err := d.Exec(`ALTER TABLE users ADD COLUMN guild_avatar_url TEXT NOT NULL DEFAULT ''`); err != nil {
			return err
		}
	}
	return nil
}

// ensureUsersTemporalBanColumns adds the temporal ban/suspension columns to a
// users table created before they existed. CREATE TABLE IF NOT EXISTS does not
// alter an existing table, so an earlier database is migrated in place. The
// PRAGMA check makes it idempotent. Existing rows receive the neutral defaults:
// no deadline (permanent semantics never apply while is_banned=0), auto_banned=0.
func ensureUsersTemporalBanColumns(d *sql.DB) error {
	specs := []struct {
		name string
		sql  string
	}{
		{"banned_until", `ALTER TABLE users ADD COLUMN banned_until INTEGER`},
		{"auto_banned", `ALTER TABLE users ADD COLUMN auto_banned INTEGER NOT NULL DEFAULT 0`},
		{"charity_suspended_until", `ALTER TABLE users ADD COLUMN charity_suspended_until INTEGER`},
	}
	rows, err := d.Query(`PRAGMA table_info(users)`)
	if err != nil {
		return err
	}
	present := make(map[string]bool)
	for rows.Next() {
		var cid int
		var name, ctype string
		var notnull, pk int
		var dflt sql.NullString
		if err := rows.Scan(&cid, &name, &ctype, &notnull, &dflt, &pk); err != nil {
			rows.Close()
			return err
		}
		present[name] = true
	}
	if err := rows.Err(); err != nil {
		return err
	}
	rows.Close()
	for _, spec := range specs {
		if present[spec.name] {
			continue
		}
		if _, err := d.Exec(spec.sql); err != nil {
			return err
		}
	}
	return nil
}

func nilSecretCodec(codec secret.Codec) bool {
	if codec == nil {
		return true
	}
	value := reflect.ValueOf(codec)
	switch value.Kind() {
	case reflect.Chan, reflect.Func, reflect.Interface, reflect.Map, reflect.Pointer, reflect.Slice:
		return value.IsNil()
	default:
		return false
	}
}

// DB returns the underlying handle for use by later rails' typed repositories.
func (s *Store) DB() *sql.DB { return s.db }

// Close closes the database handle. The caller remains responsible for
// closing the injected secret Vault after database users have stopped.
func (s *Store) Close() error { return s.db.Close() }
