package db

import (
	"bytes"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"testing"

	"github.com/waiting-here/NonbiriAPI/internal/dbtest"
	"github.com/waiting-here/NonbiriAPI/internal/secret"
)

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
	return st
}

// privateDBDir returns a fresh owner-only directory beneath t.TempDir() for
// database tests that expect Open to succeed. Go's testing.T.TempDir() creates
// the returned subdirectory with os.Mkdir(.., 0o777), so under a normal umask
// (0022) it is 0755 and grants group/other access. The S6 strict parent check
// (implementation contract §8.5) rejects any group/other access on the
// database parent, so tests open the database in an explicit 0700 parent to
// satisfy the contract for the right reason rather than relying on the no-op
// check on Windows. Tests that exercise a permissive parent call Open directly
// with a deliberately bad directory and do not use this helper.
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

// expectedTables lists every entity table the contract requires. A name here
// must resolve to a row in sqlite_master after bootstrap; a missing or extra
// table fails this test, guarding the entity set as a contract freeze point.
var expectedTables = []string{
	"users",
	"sessions",
	"caller_keys",
	"endpoints",
	"endpoint_keys",
	"fetched_models",
	"models",
	"model_bindings",
	"request_logs",
	"user_issues",
	"admin_alerts",
	"user_activity_daily",
	"site_activity_daily",
	"site_config",
}

func tableNames(t *testing.T, st *Store) []string {
	t.Helper()
	rows, err := st.DB().Query(`SELECT name FROM sqlite_master WHERE type='table' AND name NOT LIKE 'sqlite_%' ORDER BY name`)
	if err != nil {
		t.Fatalf("query tables: %v", err)
	}
	defer rows.Close()
	var names []string
	for rows.Next() {
		var n string
		if err := rows.Scan(&n); err != nil {
			t.Fatalf("scan table name: %v", err)
		}
		names = append(names, n)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("rows err: %v", err)
	}
	return names
}

// TestSchemaBootstrapsAllEntities opens a fresh database and asserts every
// contracted entity table was created.
func TestSchemaBootstrapsAllEntities(t *testing.T) {
	st := openTestStore(t, filepath.Join(t.TempDir(), "bootstrap.db"))
	defer st.Close()

	got := tableNames(t, st)
	want := append([]string(nil), expectedTables...)
	sort.Strings(want)
	if len(got) != len(want) {
		t.Fatalf("table count = %d (%v), want %d (%v)", len(got), got, len(want), want)
	}
	for i, name := range want {
		if got[i] != name {
			t.Fatalf("table[%d] = %q, want %q; got=%v", i, got[i], name, got)
		}
	}
}

func TestOpenCreatesPrivateDatabaseDirectory(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Windows does not expose Unix directory permission bits")
	}
	dir := filepath.Join(t.TempDir(), "private", "database")
	st := openTestStore(t, filepath.Join(dir, "nonbiriapi.db"))
	defer st.Close()
	info, err := os.Stat(dir)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm()&0o077 != 0 {
		t.Fatalf("new database directory permissions=%#o, want no group/other access", info.Mode().Perm())
	}
}

// TestOpenRejectsDirectoryAsDBPath verifies a directory at the configured
// database path is refused before SQLite attempts to open it. This shared
// check runs on every platform; the path-shape guard does not depend on POSIX
// permission bits. The database is opened in a 0700 parent (privateDBDir) so
// on Unix the failure is provably from the path-shape guard ("database file"),
// not from the strict parent check ("database directory").
func TestOpenRejectsDirectoryAsDBPath(t *testing.T) {
	dir := privateDBDir(t)
	dbPath := filepath.Join(dir, "is-a-dir")
	if err := os.Mkdir(dbPath, 0o700); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	key := bytes.Repeat([]byte{0x29}, secret.MasterKeyBytes)
	vault, err := secret.New(key)
	clear(key)
	if err != nil {
		t.Fatalf("create vault: %v", err)
	}
	defer vault.Close()
	_, err = Open(dbPath, vault)
	if err == nil {
		t.Fatal("Open accepted a directory as the database path")
	}
	if !strings.Contains(err.Error(), "database file") {
		t.Fatalf("Open failed for the wrong reason (want path-shape guard, got %q): %v", err.Error(), err)
	}
}

// TestSchemaBootstrapIsIdempotent asserts that re-opening the same database
// (which re-applies the schema via IF NOT EXISTS) succeeds and leaves the
// entity set intact with no duplicate artifacts.
func TestSchemaBootstrapIsIdempotent(t *testing.T) {
	path := filepath.Join(t.TempDir(), "idem.db")

	st := openTestStore(t, path)
	first := tableNames(t, st)
	st.Close()

	st2 := openTestStore(t, path)
	second := tableNames(t, st2)
	st2.Close()

	if len(first) != len(expectedTables) || len(second) != len(expectedTables) {
		t.Fatalf("table count changed after reopen: first=%d second=%d want=%d", len(first), len(second), len(expectedTables))
	}
	// Re-applying CREATE ... IF NOT EXISTS twice must not error and must not
	// duplicate table rows (verified implicitly by Open succeeding above).
	for i, name := range second {
		if first[i] != name {
			t.Fatalf("table set changed after reopen at [%d]: %q vs %q", i, first[i], name)
		}
	}
}

// TestSchemaCascadeAndCheckInvariants exercise a few DB-level invariants that
// are cheap to verify against the schema alone (no business logic):
//   - foreign_keys=ON makes endpoint -> endpoint_keys cascade-delete;
//   - the models full_name CHECK rejects a mismatched full_name.
func TestSchemaCascadeAndCheckInvariants(t *testing.T) {
	st := openTestStore(t, filepath.Join(t.TempDir(), "inv.db"))
	defer st.Close()
	d := st.DB()

	// Seed a user, an endpoint, and two endpoint_keys.
	now := int64(1)
	if _, err := d.Exec(`INSERT INTO users (discord_id, username, created_at, updated_at) VALUES ('u1', 'alice', ?, ?)`, now, now); err != nil {
		t.Fatalf("seed user: %v", err)
	}
	if _, err := d.Exec(`INSERT INTO endpoints (user_id, connector_type, base_url, created_at, updated_at) VALUES (1, 'openai-compatible', 'https://example.com', ?, ?)`, now, now); err != nil {
		t.Fatalf("seed endpoint: %v", err)
	}
	firstCiphertext, err := st.secrets.Seal([]byte{0x01})
	if err != nil {
		t.Fatalf("encrypt first endpoint key fixture: %v", err)
	}
	secondCiphertext, err := st.secrets.Seal([]byte{0x02})
	if err != nil {
		t.Fatalf("encrypt second endpoint key fixture: %v", err)
	}
	if _, err := d.Exec(`INSERT INTO endpoint_keys (endpoint_id, encrypted_secret, created_at, updated_at) VALUES (1, ?, ?, ?), (1, ?, ?, ?)`, firstCiphertext, now, now, secondCiphertext, now, now); err != nil {
		t.Fatalf("seed endpoint_keys: %v", err)
	}

	// Deleting the endpoint must cascade to both endpoint_keys (foreign_keys=ON).
	if _, err := d.Exec(`DELETE FROM endpoints WHERE id=1`); err != nil {
		t.Fatalf("delete endpoint: %v", err)
	}
	var n int
	if err := d.QueryRow(`SELECT COUNT(*) FROM endpoint_keys`).Scan(&n); err != nil {
		t.Fatalf("count endpoint_keys: %v", err)
	}
	if n != 0 {
		t.Fatalf("cascade delete failed: endpoint_keys count=%d, want 0 (foreign_keys may be off)", n)
	}

	// A models row whose full_name does not equal provider || '/' || model must
	// be rejected by the CHECK constraint.
	_, err = d.Exec(`INSERT INTO models (user_id, provider, model, full_name, route_strategy, created_at, updated_at) VALUES (1, 'openai', 'gpt', 'wrong/name', 'ordered', 1, 1)`)
	if err == nil {
		t.Fatalf("models CHECK(full_name = provider || '/' || model) did not reject a mismatched full_name")
	}

	// A valid models row is accepted, and the route_strategy CHECK rejects an
	// unknown strategy.
	if _, err := d.Exec(`INSERT INTO models (user_id, provider, model, full_name, route_strategy, created_at, updated_at) VALUES (1, 'openai', 'gpt', 'openai/gpt', 'ordered', 1, 1)`); err != nil {
		t.Fatalf("insert valid model: %v", err)
	}
	if _, err := d.Exec(`INSERT INTO models (user_id, provider, model, full_name, route_strategy, created_at, updated_at) VALUES (1, 'anthropic', 'claude', 'anthropic/claude', 'weighted', 1, 1)`); err == nil {
		t.Fatalf("models CHECK(route_strategy IN (...)) accepted an unknown strategy 'weighted'")
	}

	// External-name uniqueness: two provider/model splits yielding the same
	// full_name must collide on the unique (user_id, full_name).
	if _, err := d.Exec(`INSERT INTO models (user_id, provider, model, full_name, route_strategy, created_at, updated_at) VALUES (1, 'a/b', 'c', 'a/b/c', 'random', 2, 2)`); err != nil {
		t.Fatalf("insert a/b/c model: %v", err)
	}
	if _, err := d.Exec(`INSERT INTO models (user_id, provider, model, full_name, route_strategy, created_at, updated_at) VALUES (1, 'a', 'b/c', 'a/b/c', 'random', 3, 3)`); err == nil {
		t.Fatalf("UNIQUE(user_id, full_name) did not reject a field-pair collision producing the same external name 'a/b/c'")
	}
}
