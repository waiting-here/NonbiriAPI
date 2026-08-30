package db

import (
	"bytes"
	"os"
	"path/filepath"
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
