package db

import (
	"database/sql"
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

	return &Store{db: d, secrets: secrets}, nil
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
