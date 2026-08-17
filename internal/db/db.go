package db

import (
	"database/sql"
	"fmt"
	"os"
	"path/filepath"

	_ "modernc.org/sqlite"
)

// Store wraps the SQLite handle. Alpha keeps a single writer connection to
// avoid "database is locked" contention; the privacy policy (no request
// content persisted) keeps write pressure low. Access is via the package-level
// helpers and later rails' typed repositories.
type Store struct {
	db *sql.DB
}

// Open opens (creating if necessary) the SQLite database at path, applies
// pragmas, and bootstraps the schema idempotently. The encryption master key
// is intentionally NOT taken here: secret encryption is wired by a later rail
// and is out of scope for the skeleton.
func Open(path string) (*Store, error) {
	if dir := filepath.Dir(path); dir != "." && dir != "" && dir != string(filepath.Separator) {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return nil, fmt.Errorf("create db directory: %w", err)
		}
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

	return &Store{db: d}, nil
}

// DB returns the underlying handle for use by later rails' typed repositories.
func (s *Store) DB() *sql.DB { return s.db }

// Close closes the database handle.
func (s *Store) Close() error { return s.db.Close() }