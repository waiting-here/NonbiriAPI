package connectionstore_test

import (
	"database/sql"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"

	"github.com/waiting-here/NonbiriAPI/internal/db"
	_ "modernc.org/sqlite"
)

type connectionFixtureCache struct {
	once  sync.Once
	image []byte
	err   error
}

var connectionSchemaImage connectionFixtureCache

func (cache *connectionFixtureCache) load(t *testing.T) ([]byte, error) {
	t.Helper()
	cache.once.Do(func() {
		path := filepath.Join(t.TempDir(), "schema.sqlite")
		if err := os.Chmod(filepath.Dir(path), 0o700); err != nil {
			cache.err = err
			return
		}
		cache.image, cache.err = buildConnectionFixtureImage(path)
	})
	return cache.image, cache.err
}

// Retain only a closed image; each caller owns its database and transaction.
func buildConnectionFixtureImage(path string) ([]byte, error) {
	body, err := os.ReadFile(filepath.Join("..", "db", "generation_two.sql"))
	if err != nil {
		return nil, err
	}
	database, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		return nil, err
	}
	database.SetMaxOpenConns(1)
	defer database.Close()
	schema := string(body) + db.ServiceKeyScopeSQL + db.ManagementOperationsSQL +
		strings.Split(db.ManagementDetailsSQL, "\nCREATE TRIGGER")[0] +
		strings.Split(db.RerankPathSQL, "\nDROP TRIGGER management_capture_donation_uses_update;")[0] +
		db.CompleteDiscoveryURLSQL
	if _, err := database.Exec(schema); err != nil {
		return nil, err
	}
	if _, err := database.Exec("VACUUM INTO ?", path); err != nil {
		return nil, err
	}
	if err := database.Close(); err != nil {
		return nil, err
	}
	if err := os.Chmod(path, 0o600); err != nil {
		return nil, err
	}
	return os.ReadFile(path)
}

func writeConnectionFixtureImage(path string, image []byte) error {
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return err
	}
	_, writeErr := file.Write(image)
	if err := errors.Join(writeErr, file.Close()); err != nil {
		return errors.Join(err, os.Remove(path))
	}
	return nil
}

func openConnectionFixtureDatabase(t *testing.T) *sql.DB {
	t.Helper()
	image, err := connectionSchemaImage.load(t)
	if err != nil {
		t.Fatal(err)
	}
	return openConnectionFixtureImage(t, image)
}

func openConnectionFixtureImage(t *testing.T, image []byte) *sql.DB {
	t.Helper()
	path := filepath.Join(t.TempDir(), "fixture.sqlite")
	if err := os.Chmod(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := writeConnectionFixtureImage(path, image); err != nil {
		t.Fatal(err)
	}
	database, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	database.SetMaxOpenConns(1)
	t.Cleanup(func() { _ = database.Close() })
	// Preserve the original in-memory fixture's foreign keys and journal.
	if _, err := database.Exec("PRAGMA foreign_keys=ON; PRAGMA journal_mode=MEMORY;"); err != nil {
		t.Fatal(err)
	}
	return database
}

func TestConnectionFixtureCopiesSurviveBuilderCleanupAndRollbackIndependently(t *testing.T) {
	var cache connectionFixtureCache
	t.Run("build", func(t *testing.T) {
		if _, err := cache.load(t); err != nil {
			t.Fatal(err)
		}
	})
	image, err := cache.load(t)
	if err != nil {
		t.Fatal(err)
	}
	first := openConnectionFixtureImage(t, image)
	second := openConnectionFixtureImage(t, image)
	var paths []os.FileInfo
	for _, database := range []*sql.DB{first, second} {
		var sequence int
		var name, path string
		if err := database.QueryRow("PRAGMA database_list").Scan(&sequence, &name, &path); err != nil {
			t.Fatal(err)
		}
		info, err := os.Stat(path)
		if err != nil {
			t.Fatal(err)
		}
		paths = append(paths, info)
		if runtime.GOOS != "windows" && info.Mode().Perm() != 0o600 {
			t.Fatalf("fixture mode=%o", info.Mode().Perm())
		}
		parent, err := os.Stat(filepath.Dir(path))
		if err != nil {
			t.Fatal(err)
		}
		if runtime.GOOS != "windows" && parent.Mode().Perm() != 0o700 {
			t.Fatalf("fixture parent mode=%o", parent.Mode().Perm())
		}
	}
	if os.SameFile(paths[0], paths[1]) {
		t.Fatal("fixtures share a database file")
	}
	tx, err := first.BeginTx(t.Context(), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	if _, err := tx.Exec("CREATE TABLE fixture_probe(value TEXT)"); err != nil {
		t.Fatal(err)
	}
	var tables int
	if err := second.QueryRow("SELECT count(*) FROM sqlite_master WHERE name='fixture_probe'").Scan(&tables); err != nil || tables != 0 {
		t.Fatalf("second fixture inherited mutation: tables=%d err=%v", tables, err)
	}
	if err := tx.Rollback(); err != nil {
		t.Fatal(err)
	}
	if err := first.QueryRow("SELECT count(*) FROM sqlite_master WHERE name='fixture_probe'").Scan(&tables); err != nil || tables != 0 {
		t.Fatalf("fixture transaction survived rollback: tables=%d err=%v", tables, err)
	}
	var foreignKeys int
	var journal string
	if err := first.QueryRow("PRAGMA foreign_keys").Scan(&foreignKeys); err != nil || foreignKeys != 1 {
		t.Fatal("fixture foreign keys", foreignKeys, err)
	}
	if err := first.QueryRow("PRAGMA journal_mode").Scan(&journal); err != nil || journal != "memory" {
		t.Fatal("fixture journal", journal, err)
	}
}

func TestConnectionFixtureImageDoesNotOverwriteExistingFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "existing.sqlite")
	if err := os.WriteFile(path, []byte("existing data"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := writeConnectionFixtureImage(path, []byte("replacement")); !errors.Is(err, os.ErrExist) {
		t.Fatalf("existing target error=%v", err)
	}
	body, err := os.ReadFile(path)
	if err != nil || string(body) != "existing data" {
		t.Fatalf("existing target changed: %q %v", body, err)
	}
}
