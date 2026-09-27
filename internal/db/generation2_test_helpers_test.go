package db

import (
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"os"
	"path/filepath"
	"runtime"
	"sync"
	"testing"

	"github.com/waiting-here/NonbiriAPI/internal/dbtest"
	"github.com/waiting-here/NonbiriAPI/internal/secret"
)

const testNow int64 = 1700000000

func openTestStore(t *testing.T, path string) *Store {
	t.Helper()
	key := bytes.Repeat([]byte{0x29}, secret.MasterKeyBytes)
	vault, err := secret.New(key)
	clear(key)
	if err != nil {
		t.Fatalf("create test secret vault: %v", err)
	}
	t.Cleanup(func() { _ = vault.Close() })
	dbtest.EnsureOwnerOnlyParent(t, path)
	st, err := Open(path, vault)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })
	return st
}

func privateDBDir(t *testing.T) string {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "dbdir")
	if err := os.Mkdir(dir, 0o700); err != nil {
		t.Fatalf("create private db dir: %v", err)
	}
	if err := os.Chmod(dir, 0o700); err != nil {
		t.Fatalf("chmod private db dir: %v", err)
	}
	return dir
}

// copyPrivateSQLiteTestImage gives each case its own database file. Callers
// must finish writing and close the source before passing its immutable image.
func copyPrivateSQLiteTestImage(t *testing.T, image []byte) string {
	t.Helper()
	if len(image) < 100 {
		t.Fatal("SQLite test image is too short")
	}
	path := filepath.Join(privateDBDir(t), "fixture.sqlite")
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		t.Fatalf("create private SQLite test copy: %v", err)
	}
	if _, err := file.Write(image); err != nil {
		_ = file.Close()
		t.Fatalf("write private SQLite test copy: %v", err)
	}
	if err := file.Close(); err != nil {
		t.Fatalf("close private SQLite test copy: %v", err)
	}
	return path
}

var generationTwoConstraintImage struct {
	once       sync.Once
	image      []byte
	schemaHash [32]byte
	imageHash  [32]byte
}

// Keep only immutable bytes after the first builder's temporary directory
// is cleaned up. Every consumer opens a private file, never a shared database.
func generationTwoConstraintImageForTest(t *testing.T) []byte {
	t.Helper()
	generationTwoConstraintImage.once.Do(func() {
		image := snapshotGenerationTwoDDLTestImage(t, openGenerationTwoDDLForTest(t))
		generationTwoConstraintImage.image = image
		generationTwoConstraintImage.schemaHash = sha256.Sum256([]byte(generationTwoSchema))
		generationTwoConstraintImage.imageHash = sha256.Sum256(image)
	})
	image := generationTwoConstraintImage.image
	if len(image) < 100 || generationTwoConstraintImage.schemaHash != sha256.Sum256([]byte(generationTwoSchema)) ||
		generationTwoConstraintImage.imageHash != sha256.Sum256(image) {
		t.Fatal("current DDL fixture image is missing or changed")
	}
	return image
}

func openGenerationTwoConstraintFixture(t *testing.T) *sql.DB {
	t.Helper()
	database, _ := openGenerationTwoDDLTestImage(t, generationTwoConstraintImageForTest(t))
	return database
}

// snapshotGenerationTwoDDLTestImage closes a current DDL fixture before
// reading its standalone image. It is for constraint matrices whose inputs
// do not need to exercise fresh bootstrap, upgrade, recovery or file locking.
func snapshotGenerationTwoDDLTestImage(t *testing.T, database *sql.DB) []byte {
	t.Helper()
	manifest, err := readGenerationManifest(context.Background(), database)
	if err != nil || generationManifestDigest(manifest) != PinnedGenerationTwoManifestHash {
		t.Fatalf("DDL fixture manifest=%s, err=%v", generationManifestDigest(manifest), err)
	}
	assertGenerationTwoDDLTestConnection(t, database)
	path := filepath.Join(privateDBDir(t), "baseline.sqlite")
	if _, err := database.Exec(`VACUUM INTO ?`, path); err != nil {
		t.Fatalf("snapshot DDL fixture: %v", err)
	}
	if err := database.Close(); err != nil {
		t.Fatalf("close DDL fixture: %v", err)
	}
	if err := os.Chmod(path, 0o600); err != nil {
		t.Fatalf("secure DDL fixture image: %v", err)
	}
	assertPrivateSQLiteTestImageFile(t, path)
	image, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read DDL fixture image: %v", err)
	}
	if !bytes.HasPrefix(image, []byte("SQLite format 3\x00")) {
		t.Fatal("DDL fixture image lacks a SQLite header")
	}
	return image
}

func openGenerationTwoDDLTestImage(t *testing.T, image []byte) (*sql.DB, string) {
	t.Helper()
	path := copyPrivateSQLiteTestImage(t, image)
	assertPrivateSQLiteTestImageFile(t, path)
	database, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatalf("open DDL fixture copy: %v", err)
	}
	database.SetMaxOpenConns(1)
	t.Cleanup(func() {
		if err := database.Close(); err != nil {
			t.Errorf("close DDL fixture copy: %v", err)
		}
		assertPrivateSQLiteTestImageFile(t, path)
	})
	// Match the original in-memory fixture's connection settings. MEMORY
	// also keeps each copy free of an on-disk journal or WAL/SHM files.
	if _, err := database.Exec(`PRAGMA foreign_keys=ON; PRAGMA journal_mode=MEMORY;`); err != nil {
		t.Fatalf("configure DDL fixture copy: %v", err)
	}
	assertGenerationTwoDDLTestConnection(t, database)
	return database, path
}

func assertGenerationTwoDDLTestConnection(t *testing.T, database *sql.DB) {
	t.Helper()
	var foreignKeys int
	var journalMode string
	if err := database.QueryRow(`PRAGMA foreign_keys`).Scan(&foreignKeys); err != nil || foreignKeys != 1 {
		t.Fatalf("DDL fixture foreign_keys=%d, err=%v", foreignKeys, err)
	}
	if err := database.QueryRow(`PRAGMA journal_mode`).Scan(&journalMode); err != nil || journalMode != "memory" {
		t.Fatalf("DDL fixture journal_mode=%s, err=%v", journalMode, err)
	}
	if connections := database.Stats().MaxOpenConnections; connections != 1 {
		t.Fatalf("DDL fixture MaxOpenConnections=%d", connections)
	}
}

func assertPrivateSQLiteTestImageFile(t *testing.T, path string) {
	t.Helper()
	info, err := os.Lstat(path)
	if err != nil || !info.Mode().IsRegular() {
		t.Fatalf("private SQLite test image is not a regular file: %v", err)
	}
	if runtime.GOOS != "windows" && info.Mode().Perm() != 0o600 {
		t.Fatalf("private SQLite test image mode=%o", info.Mode().Perm())
	}
	for _, suffix := range []string{"-wal", "-shm", "-journal"} {
		if _, err := os.Lstat(path + suffix); !os.IsNotExist(err) {
			t.Fatalf("private SQLite test image has sidecar %s: %v", suffix, err)
		}
	}
}

// seedUserRaw inserts the minimum valid Generation 2 identity row. It writes
// the fixed-width counters and revision explicitly so a fixture cannot rely
// on a retired schema default or silently bypass a NOT NULL contract.
func seedUserRaw(t *testing.T, st *Store, discordID string) int64 {
	t.Helper()
	zero := make([]byte, 16)
	res, err := st.DB().Exec(`
INSERT INTO users (
 discord_id, username, donation_credit_mag,
 total_requests, total_uncached_input_tokens, total_cache_write_input_tokens,
 total_cache_read_input_tokens, total_output_tokens,
 total_unknown_usage_requests, revision, created_at, updated_at
) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		discordID, "tester", zero, zero, zero, zero, zero, zero, zero, zero, testNow, testNow)
	if err != nil {
		t.Fatalf("seed Generation 2 user: %v", err)
	}
	id, err := res.LastInsertId()
	if err != nil {
		t.Fatalf("seed user last insert id: %v", err)
	}
	return id
}

func countRows(t *testing.T, st *Store, query string, args ...any) int {
	t.Helper()
	var count int
	if err := st.DB().QueryRow(query, args...).Scan(&count); err != nil {
		t.Fatalf("count rows: %v", err)
	}
	return count
}
