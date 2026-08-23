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

// Open classifies path as either completely fresh or current generation one.
// Existing databases are validated from a private read-only snapshot before
// SQLite is allowed to open the source path. No migration or repair is ever
// attempted.
func Open(path string, secrets secret.Codec) (*Store, error) {
	if nilSecretCodec(secrets) {
		return nil, fmt.Errorf("open database: secret codec is required")
	}
	if err := prepareDBDirectory(path); err != nil {
		return nil, err
	}
	return openGenerationOne(path, secrets)
}

// prepareDBDirectory creates a distinct missing parent owner-only and then
// applies the existing canonical owner/mode gate to the resolved parent.
func prepareDBDirectory(path string) error {
	dir := filepath.Dir(path)
	if dir != "." && dir != "" && dir != string(filepath.Separator) {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			return fmt.Errorf("create database directory: %w", err)
		}
	}
	absDir, err := filepath.Abs(dir)
	if err != nil {
		return fmt.Errorf("resolve database directory: %w", err)
	}
	return secureDBParentDir(absDir)
}

func requireRegularDBPath(path, role string) error {
	info, err := os.Lstat(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return fmt.Errorf("inspect %s: %w", role, err)
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("%s must not be a symlink", role)
	}
	if !info.Mode().IsRegular() {
		return fmt.Errorf("%s must be a regular file", role)
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

// DB returns the underlying handle for typed repositories.
func (s *Store) DB() *sql.DB { return s.db }

// Close closes the database handle. The injected secret codec remains owned
// by the caller.
func (s *Store) Close() error { return s.db.Close() }
