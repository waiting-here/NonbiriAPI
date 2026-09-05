package db

import (
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"

	"github.com/waiting-here/NonbiriAPI/internal/secret"

	_ "modernc.org/sqlite"
)

// Store wraps the SQLite handle and the recoverable-secret boundary. The
// caller owns the codec lifecycle; typed repositories use it internally so
// plaintext never has to become a database argument or a Store field.
type Store struct {
	db      *sql.DB
	secrets secret.GenerationTwoContextCodec
}

// Open classifies path as either completely fresh or current Generation 2.
// Existing databases are validated from a private read-only snapshot before
// SQLite is allowed to open the source path. Only the exact supported prior
// Generation 2 schema receives the additive routing table; no repair is attempted.
func Open(path string, secrets secret.GenerationTwoContextCodec) (*Store, error) {
	if nilSecretCodec(secrets) {
		return nil, fmt.Errorf("open database: secret codec is required")
	}
	if err := prepareDBDirectory(path); err != nil {
		return nil, err
	}
	return openGenerationTwo(path, secrets)
}

// prepareDBDirectory validates every existing parent component before it
// creates anything. In particular, MkdirAll follows an existing symlink (and
// a Windows junction/reparse point), so checking only the final directory
// after creation would be too late even under the beta.1 trusted-host threat
// model. The checks are deliberately static; they do not claim to close a
// same-UID/local-admin race between this function and SQLite's open.
func prepareDBDirectory(path string) error {
	dir := filepath.Dir(path)
	absDir, err := filepath.Abs(dir)
	if err != nil {
		return startupError(StartupUnsafePath)
	}
	if err := inspectDBParentPathComponents(absDir); err != nil {
		return startupError(StartupUnsafePath)
	}
	if dir != "." && dir != "" && dir != string(filepath.Separator) {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			return startupError(StartupInitialization)
		}
	}
	if err := inspectDBParentPathComponents(absDir); err != nil {
		return startupError(StartupUnsafePath)
	}
	if err := secureDBParentDir(absDir); err != nil {
		return startupError(StartupUnsafePath)
	}
	return nil
}

func requireRegularDBPath(path, role string) error {
	info, err := os.Lstat(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return fmt.Errorf("inspect %s: %w", role, err)
	}
	if err := validateDBRegularPath(path, info); err != nil {
		return fmt.Errorf("%s: %w", role, err)
	}
	return nil
}

// inspectDBParentPathComponents walks the lexical absolute parent path with
// Lstat, stopping at the first missing component. The second walk in
// prepareDBDirectory verifies components created by MkdirAll without ever
// allowing the first walk's missing suffix to be followed implicitly.
func inspectDBParentPathComponents(dir string) error {
	absDir, err := filepath.Abs(dir)
	if err != nil {
		return err
	}
	volume := filepath.VolumeName(absDir)
	rest := strings.TrimPrefix(absDir, volume)
	current := volume
	if strings.HasPrefix(rest, string(filepath.Separator)) {
		current += string(filepath.Separator)
		rest = strings.TrimLeft(rest, string(filepath.Separator))
	}
	for _, component := range strings.FieldsFunc(rest, func(r rune) bool {
		return r == rune(filepath.Separator)
	}) {
		if current == "" {
			current = component
		} else {
			current = filepath.Join(current, component)
		}
		info, err := os.Lstat(current)
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		if err != nil {
			return fmt.Errorf("inspect database directory component: %w", err)
		}
		if err := validateDBParentPath(current, info); err != nil {
			return err
		}
	}
	return nil
}

func nilSecretCodec(codec secret.GenerationTwoContextCodec) bool {
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
