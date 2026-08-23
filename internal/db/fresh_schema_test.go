package db

import (
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/waiting-here/NonbiriAPI/internal/dbtest"
	"github.com/waiting-here/NonbiriAPI/internal/secret"
)

type pathEvidence struct {
	exists bool
	info   os.FileInfo
	size   int64
	mtime  int64
	mode   os.FileMode
	digest [sha256.Size]byte
}

func fixedVault(t *testing.T, value byte) *secret.Vault {
	t.Helper()
	key := bytes.Repeat([]byte{value}, secret.MasterKeyBytes)
	vault, err := secret.New(key)
	clear(key)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = vault.Close() })
	return vault
}

func openWithVault(t *testing.T, path string, vault secret.Codec) *Store {
	t.Helper()
	dbtest.EnsureOwnerOnlyParent(t, path)
	store, err := Open(path, vault)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	return store
}

func capturePathEvidence(t *testing.T, path string) pathEvidence {
	t.Helper()
	info, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return pathEvidence{}
	}
	if err != nil {
		t.Fatal(err)
	}
	evidence := pathEvidence{exists: true, info: info, size: info.Size(), mtime: info.ModTime().UnixNano(), mode: info.Mode()}
	if info.Mode().IsRegular() {
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		evidence.digest = sha256.Sum256(data)
	}
	return evidence
}

func captureDatabaseEvidence(t *testing.T, path string) map[string]pathEvidence {
	t.Helper()
	result := make(map[string]pathEvidence)
	for _, suffix := range []string{"", "-journal", "-wal", "-shm"} {
		result[suffix] = capturePathEvidence(t, path+suffix)
	}
	return result
}

func assertDatabaseEvidenceUnchanged(t *testing.T, path string, before map[string]pathEvidence) {
	t.Helper()
	for _, suffix := range []string{"", "-journal", "-wal", "-shm"} {
		want := before[suffix]
		got := capturePathEvidence(t, path+suffix)
		if got.exists != want.exists {
			t.Fatalf("%s existence changed: got %v want %v", suffix, got.exists, want.exists)
		}
		if !got.exists {
			continue
		}
		if !os.SameFile(want.info, got.info) || got.size != want.size || got.mtime != want.mtime || got.mode != want.mode || got.digest != want.digest {
			t.Fatalf("%s source evidence changed", suffix)
		}
	}
}

func requireStartupKind(t *testing.T, err error, want StartupErrorKind) {
	t.Helper()
	var startup *StartupError
	if !errors.As(err, &startup) || startup.Kind != want {
		t.Fatalf("startup error=%v, want kind %s", err, want)
	}
}

func TestGenerationOneMarkersSeedsAndHash(t *testing.T) {
	path := filepath.Join(t.TempDir(), "markers.db")
	store := openTestStore(t, path)
	var applicationID, userVersion uint32
	if err := store.DB().QueryRow(`PRAGMA application_id`).Scan(&applicationID); err != nil {
		t.Fatal(err)
	}
	if err := store.DB().QueryRow(`PRAGMA user_version`).Scan(&userVersion); err != nil {
		t.Fatal(err)
	}
	if applicationID != DatabaseApplicationID || userVersion != DatabaseUserVersion {
		t.Fatalf("markers=(%#x,%d)", applicationID, userVersion)
	}
	wantSeeds := map[string]string{
		"maintenance_mode": "1", "registration_open": "0", "games_enabled": "0", "game_fishing_enabled": "0",
	}
	for key, want := range wantSeeds {
		var value string
		if err := store.DB().QueryRow(`SELECT value FROM site_config WHERE key=?`, key).Scan(&value); err != nil || value != want {
			t.Fatalf("seed %s=(%q,%v), want %q", key, value, err, want)
		}
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	header, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(header) < 100 || binary.BigEndian.Uint32(header[68:72]) != DatabaseApplicationID || binary.BigEndian.Uint32(header[60:64]) != DatabaseUserVersion {
		t.Fatal("raw header markers differ from the generation-one contract")
	}
	t.Logf("generation-one schema sha256=%s", GenerationOneSchemaHash())
	if GenerationOneSchemaHash() != PinnedGenerationOneSchemaHash {
		t.Fatalf("schema hash=%s want %s", GenerationOneSchemaHash(), PinnedGenerationOneSchemaHash)
	}
}

func TestFreshFailureRollsBackAndRemovesOwnedFiles(t *testing.T) {
	for _, failure := range []string{"transaction_interrupted", "busy", "disk_full"} {
		t.Run(failure, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), failure+".db")
			dbtest.EnsureOwnerOnlyParent(t, path)
			freshSchemaFailureHook = func() error { return errors.New(failure) }
			t.Cleanup(func() { freshSchemaFailureHook = nil })
			_, err := Open(path, fixedVault(t, 0x31))
			requireStartupKind(t, err, StartupInitialization)
			freshSchemaFailureHook = nil
			for _, suffix := range []string{"", "-journal", "-wal", "-shm"} {
				if _, err := os.Lstat(path + suffix); !errors.Is(err, os.ErrNotExist) {
					t.Fatalf("failed fresh initialization left %s: %v", suffix, err)
				}
			}
		})
	}
}

func createLegacyDatabase(t *testing.T, path string) {
	t.Helper()
	dbtest.EnsureOwnerOnlyParent(t, path)
	raw, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := raw.Exec(`CREATE TABLE users(id INTEGER PRIMARY KEY, username TEXT NOT NULL)`); err != nil {
		_ = raw.Close()
		t.Fatal(err)
	}
	if err := raw.Close(); err != nil {
		t.Fatal(err)
	}
	_ = os.Chmod(path, 0o600)
}

func patchHeaderUint32(t *testing.T, path string, offset int64, value uint32) {
	t.Helper()
	f, err := os.OpenFile(path, os.O_WRONLY, 0)
	if err != nil {
		t.Fatal(err)
	}
	var encoded [4]byte
	binary.BigEndian.PutUint32(encoded[:], value)
	if _, err := f.WriteAt(encoded[:], offset); err != nil {
		_ = f.Close()
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestRejectedExistingDatabaseIsByteAndMetadataStable(t *testing.T) {
	cases := []struct {
		name string
		kind StartupErrorKind
		make func(*testing.T, string)
	}{
		{name: "empty", kind: StartupInvalidHeader, make: func(t *testing.T, path string) {
			dbtest.EnsureOwnerOnlyParent(t, path)
			if err := os.WriteFile(path, nil, 0o600); err != nil {
				t.Fatal(err)
			}
		}},
		{name: "alpha2", kind: StartupWrongIdentity, make: createLegacyDatabase},
		{name: "lower_generation", kind: StartupWrongGeneration, make: func(t *testing.T, path string) {
			store := openTestStore(t, path)
			if err := store.Close(); err != nil {
				t.Fatal(err)
			}
			patchHeaderUint32(t, path, 60, 0)
		}},
		{name: "higher_generation", kind: StartupWrongGeneration, make: func(t *testing.T, path string) {
			store := openTestStore(t, path)
			if err := store.Close(); err != nil {
				t.Fatal(err)
			}
			patchHeaderUint32(t, path, 60, 2)
		}},
		{name: "damaged_header", kind: StartupInvalidHeader, make: func(t *testing.T, path string) {
			dbtest.EnsureOwnerOnlyParent(t, path)
			if err := os.WriteFile(path, bytes.Repeat([]byte{0x7f}, 100), 0o600); err != nil {
				t.Fatal(err)
			}
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), tc.name+".db")
			tc.make(t, path)
			before := captureDatabaseEvidence(t, path)
			_, err := Open(path, fixedVault(t, 0x42))
			requireStartupKind(t, err, tc.kind)
			assertDatabaseEvidenceUnchanged(t, path, before)
		})
	}
}

func TestMissingMainResidualSidecarAndHardLinkAreRejectedWithoutWrites(t *testing.T) {
	t.Run("residual sidecar", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "residual.db")
		dbtest.EnsureOwnerOnlyParent(t, path)
		if err := os.WriteFile(path+"-wal", []byte("residual"), 0o600); err != nil {
			t.Fatal(err)
		}
		_ = os.Chmod(path+"-wal", 0o600)
		before := captureDatabaseEvidence(t, path)
		_, err := Open(path, fixedVault(t, 0x43))
		requireStartupKind(t, err, StartupIncompleteFresh)
		assertDatabaseEvidenceUnchanged(t, path, before)
	})

	t.Run("residual rollback journal", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "residual-journal.db")
		dbtest.EnsureOwnerOnlyParent(t, path)
		if err := os.WriteFile(path+"-journal", []byte("residual"), 0o600); err != nil {
			t.Fatal(err)
		}
		_ = os.Chmod(path+"-journal", 0o600)
		before := captureDatabaseEvidence(t, path)
		_, err := Open(path, fixedVault(t, 0x45))
		requireStartupKind(t, err, StartupRollbackJournal)
		assertDatabaseEvidenceUnchanged(t, path, before)
	})

	t.Run("hard link", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "linked.db")
		store := openTestStore(t, path)
		if err := store.Close(); err != nil {
			t.Fatal(err)
		}
		link := path + ".link"
		if err := os.Link(path, link); err != nil {
			t.Skipf("hard links unavailable: %v", err)
		}
		before := captureDatabaseEvidence(t, path)
		_, err := Open(path, fixedVault(t, 0x44))
		requireStartupKind(t, err, StartupUnsafePath)
		assertDatabaseEvidenceUnchanged(t, path, before)
	})
}

func TestRollbackJournalShapesAreRejectedWithoutWrites(t *testing.T) {
	tests := []struct {
		name string
		kind StartupErrorKind
		make func(*testing.T, string)
	}{
		{name: "ordinary residual", kind: StartupRollbackJournal, make: func(t *testing.T, journal string) {
			if err := os.WriteFile(journal, []byte("not hot"), 0o600); err != nil {
				t.Fatal(err)
			}
			_ = os.Chmod(journal, 0o600)
		}},
		{name: "symlink", kind: StartupUnsafePath, make: func(t *testing.T, journal string) {
			target := filepath.Join(t.TempDir(), "journal-target")
			if err := os.WriteFile(target, []byte("target"), 0o600); err != nil {
				t.Fatal(err)
			}
			if err := os.Symlink(target, journal); err != nil {
				t.Skipf("symlinks unavailable: %v", err)
			}
		}},
		{name: "directory", kind: StartupUnsafePath, make: func(t *testing.T, journal string) {
			if err := os.Mkdir(journal, 0o700); err != nil {
				t.Fatal(err)
			}
		}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "current.db")
			store := openTestStore(t, path)
			if err := store.Close(); err != nil {
				t.Fatal(err)
			}
			tc.make(t, path+"-journal")
			before := captureDatabaseEvidence(t, path)
			_, err := Open(path, fixedVault(t, 0x46))
			requireStartupKind(t, err, tc.kind)
			assertDatabaseEvidenceUnchanged(t, path, before)
		})
	}
}

const hotRollbackCrashEnv = "NONBIRI_TEST_HOT_ROLLBACK_CRASH"

func TestHotRollbackJournalCrashHelper(t *testing.T) {
	if os.Getenv(hotRollbackCrashEnv) != "1" {
		return
	}
	path := os.Getenv("NONBIRI_TEST_HOT_ROLLBACK_PATH")
	if path == "" {
		t.Fatal("missing crash fixture path")
	}
	d, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	d.SetMaxOpenConns(1)
	var mode string
	if err := d.QueryRow(`PRAGMA journal_mode=DELETE`).Scan(&mode); err != nil || mode != "delete" {
		t.Fatalf("journal mode=%q err=%v", mode, err)
	}
	if _, err := d.Exec(`PRAGMA synchronous=FULL; PRAGMA cache_size=4; PRAGMA cache_spill=ON;`); err != nil {
		t.Fatal(err)
	}
	tx, err := d.Begin()
	if err != nil {
		t.Fatal(err)
	}
	result, err := tx.Exec(`UPDATE site_config SET value=? WHERE key LIKE 'hot-journal-%'`, strings.Repeat("B", 8192))
	if err != nil {
		t.Fatal(err)
	}
	updated, err := result.RowsAffected()
	if err != nil || updated < 128 {
		t.Fatalf("updated=%d err=%v", updated, err)
	}
	// Deliberately terminate the whole helper process without Commit, Rollback,
	// Close, or test cleanup. The tiny cache forces dirty pages to spill, so the
	// surviving DELETE-mode journal is a real recovery-bearing hot journal.
	os.Exit(86)
}

func TestHotRollbackJournalFromCrashedSubprocessIsNeverRecovered(t *testing.T) {
	path := filepath.Join(t.TempDir(), "hot.db")
	store := openTestStore(t, path)
	tx, err := store.DB().Begin()
	if err != nil {
		t.Fatal(err)
	}
	value := strings.Repeat("A", 8192)
	for i := 0; i < 192; i++ {
		if _, err := tx.Exec(`INSERT INTO site_config(key,value,updated_at) VALUES(?,?,0)`, fmt.Sprintf("hot-journal-%03d", i), value); err != nil {
			_ = tx.Rollback()
			t.Fatal(err)
		}
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}

	preCrash, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	preCrash.SetMaxOpenConns(1)
	var busy, logFrames, checkpointed int
	if err := preCrash.QueryRow(`PRAGMA wal_checkpoint(TRUNCATE)`).Scan(&busy, &logFrames, &checkpointed); err != nil || busy != 0 {
		_ = preCrash.Close()
		t.Fatalf("checkpoint=(%d,%d,%d) err=%v", busy, logFrames, checkpointed, err)
	}
	var mode string
	if err := preCrash.QueryRow(`PRAGMA journal_mode=DELETE`).Scan(&mode); err != nil || mode != "delete" {
		_ = preCrash.Close()
		t.Fatalf("journal mode=%q err=%v", mode, err)
	}
	if err := preCrash.Close(); err != nil {
		t.Fatal(err)
	}

	cmd := exec.Command(os.Args[0], "-test.run=^TestHotRollbackJournalCrashHelper$")
	cmd.Env = append(os.Environ(), hotRollbackCrashEnv+"=1", "NONBIRI_TEST_HOT_ROLLBACK_PATH="+path)
	output, crashErr := cmd.CombinedOutput()
	var exitErr *exec.ExitError
	if !errors.As(crashErr, &exitErr) || exitErr.ExitCode() != 86 {
		if len(output) > 4096 {
			output = output[:4096]
		}
		t.Fatalf("crash helper error=%v output=%q", crashErr, output)
	}

	journalPath := path + "-journal"
	journal, err := os.Open(journalPath)
	if err != nil {
		t.Fatalf("hot journal missing: %v", err)
	}
	var header [8]byte
	_, readErr := io.ReadFull(journal, header[:])
	info, statErr := journal.Stat()
	closeErr := journal.Close()
	if readErr != nil || statErr != nil || closeErr != nil {
		t.Fatalf("hot journal read=%v stat=%v close=%v", readErr, statErr, closeErr)
	}
	if info.Size() <= 512 {
		t.Fatalf("hot journal size=%d", info.Size())
	}
	wantMagic := [8]byte{0xd9, 0xd5, 0x05, 0xf9, 0x20, 0xa1, 0x63, 0xd7}
	if header != wantMagic {
		t.Fatalf("rollback journal was not hot: header=%x", header)
	}

	before := captureDatabaseEvidence(t, path)
	_, err = Open(path, fixedVault(t, 0x47))
	requireStartupKind(t, err, StartupRollbackJournal)
	assertDatabaseEvidenceUnchanged(t, path, before)
}

func mutateDatabase(t *testing.T, path, statement string) {
	t.Helper()
	d, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	d.SetMaxOpenConns(1)
	if _, err := d.Exec(statement); err != nil {
		_ = d.Close()
		t.Fatal(err)
	}
	if err := d.Close(); err != nil {
		t.Fatal(err)
	}
}

func rewriteSchemaSQL(t *testing.T, path, object, old, replacement string) {
	t.Helper()
	d, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	d.SetMaxOpenConns(1)
	var version int
	if err := d.QueryRow(`PRAGMA schema_version`).Scan(&version); err != nil {
		_ = d.Close()
		t.Fatal(err)
	}
	if _, err := d.Exec(`PRAGMA writable_schema=ON`); err != nil {
		_ = d.Close()
		t.Fatal(err)
	}
	result, err := d.Exec(`UPDATE sqlite_schema SET sql=replace(sql,?,?) WHERE name=?`, old, replacement, object)
	if err != nil {
		_ = d.Close()
		t.Fatal(err)
	}
	affected, _ := result.RowsAffected()
	if affected != 1 {
		_ = d.Close()
		t.Fatalf("rewrite affected %d rows", affected)
	}
	if _, err := d.Exec(fmt.Sprintf(`PRAGMA schema_version=%d`, version+1)); err != nil {
		_ = d.Close()
		t.Fatal(err)
	}
	if _, err := d.Exec(`PRAGMA writable_schema=RESET`); err != nil {
		_ = d.Close()
		t.Fatal(err)
	}
	if err := d.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestManifestRejectsEverySchemaMutationClassWithoutWritingSource(t *testing.T) {
	mutations := []struct {
		name  string
		apply func(*testing.T, string)
	}{
		{"extra_table", func(t *testing.T, p string) { mutateDatabase(t, p, `CREATE TABLE injected_table(id INTEGER)`) }},
		{"missing_table", func(t *testing.T, p string) { mutateDatabase(t, p, `DROP TABLE game_fishing_best`) }},
		{"extra_index", func(t *testing.T, p string) { mutateDatabase(t, p, `CREATE INDEX injected_index ON users(username)`) }},
		{"missing_index", func(t *testing.T, p string) { mutateDatabase(t, p, `DROP INDEX idx_game_settlements_due`) }},
		{"extra_trigger", func(t *testing.T, p string) {
			mutateDatabase(t, p, `CREATE TRIGGER injected_trigger AFTER INSERT ON users BEGIN SELECT 1; END`)
		}},
		{"missing_column", func(t *testing.T, p string) {
			mutateDatabase(t, p, `ALTER TABLE endpoint_keys DROP COLUMN force_store_false`)
		}},
		{"foreign_key", func(t *testing.T, p string) {
			rewriteSchemaSQL(t, p, "game_fishing_best", "REFERENCES users(id) ON DELETE CASCADE", "REFERENCES users(id) ON DELETE RESTRICT")
		}},
		{"check", func(t *testing.T, p string) {
			rewriteSchemaSQL(t, p, "users", "concurrency_limit BETWEEN 1 AND 100000", "concurrency_limit BETWEEN 1 AND 99999")
		}},
	}
	for _, mutation := range mutations {
		t.Run(mutation.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), mutation.name+".db")
			store := openTestStore(t, path)
			if err := store.Close(); err != nil {
				t.Fatal(err)
			}
			mutation.apply(t, path)
			before := captureDatabaseEvidence(t, path)
			_, err := Open(path, fixedVault(t, 0x52))
			requireStartupKind(t, err, StartupSchemaMismatch)
			assertDatabaseEvidenceUnchanged(t, path, before)
		})
	}
}

func seedContextualCredential(t *testing.T, store *Store) int64 {
	t.Helper()
	user, err := store.CreateDiscordUser("credential-owner", "owner", "")
	if err != nil {
		t.Fatal(err)
	}
	endpoint, err := store.CreateEndpoint(context.Background(), user.ID, "openai-compatible", "https://credential.example/v1", "", true, time.Now().Unix())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.CreateEndpointKey(context.Background(), user.ID, endpoint.ID, []byte("credential-value"), "", "", "", true, time.Now().Unix()); err != nil {
		t.Fatal(err)
	}
	return user.ID
}

func TestCurrentCredentialValidationIsReadOnlyAndV2Only(t *testing.T) {
	path := filepath.Join(t.TempDir(), "credentials.db")
	good := fixedVault(t, 0x61)
	store := openWithVault(t, path, good)
	seedContextualCredential(t, store)
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	before := captureDatabaseEvidence(t, path)
	_, err := Open(path, fixedVault(t, 0x62))
	requireStartupKind(t, err, StartupCredentialReject)
	assertDatabaseEvidenceUnchanged(t, path, before)
	reopened := openWithVault(t, path, good)
	if err := reopened.Close(); err != nil {
		t.Fatal(err)
	}

	legacyPath := filepath.Join(t.TempDir(), "v1.db")
	legacy := openWithVault(t, legacyPath, good)
	user, err := legacy.CreateDiscordUser("legacy-envelope", "legacy", "")
	if err != nil {
		t.Fatal(err)
	}
	endpoint, err := legacy.CreateEndpoint(context.Background(), user.ID, "openai-compatible", "https://legacy.example/v1", "", true, 1)
	if err != nil {
		t.Fatal(err)
	}
	v1, err := good.Seal([]byte("legacy-secret"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := legacy.DB().Exec(`INSERT INTO endpoint_keys(endpoint_id,encrypted_secret,created_at,updated_at) VALUES(?,?,1,1)`, endpoint.ID, v1); err != nil {
		t.Fatal(err)
	}
	if err := legacy.Close(); err != nil {
		t.Fatal(err)
	}
	legacyBefore := captureDatabaseEvidence(t, legacyPath)
	_, err = Open(legacyPath, good)
	requireStartupKind(t, err, StartupCredentialReject)
	assertDatabaseEvidenceUnchanged(t, legacyPath, legacyBefore)

	for _, tc := range []struct {
		name   string
		kind   StartupErrorKind
		mutate func(*testing.T, *Store)
	}{
		{name: "unknown envelope", kind: StartupCredentialReject, mutate: func(t *testing.T, store *Store) {
			if _, err := store.DB().Exec(`UPDATE endpoint_keys SET encrypted_secret=replace(encrypted_secret,'nbsec:v2:','nbsec:v9:')`); err != nil {
				t.Fatal(err)
			}
		}},
		{name: "wrong origin context", kind: StartupCredentialReject, mutate: func(t *testing.T, store *Store) {
			if _, err := store.DB().Exec(`UPDATE endpoints SET base_url='https://changed-origin.example/v1'`); err != nil {
				t.Fatal(err)
			}
		}},
		{name: "orphan", kind: StartupCorruptDatabase, mutate: func(t *testing.T, store *Store) {
			if _, err := store.DB().Exec(`PRAGMA foreign_keys=OFF`); err != nil {
				t.Fatal(err)
			}
			if _, err := store.DB().Exec(`DELETE FROM endpoints`); err != nil {
				t.Fatal(err)
			}
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "credential.db")
			store := openWithVault(t, path, good)
			seedContextualCredential(t, store)
			tc.mutate(t, store)
			if err := store.Close(); err != nil {
				t.Fatal(err)
			}
			before := captureDatabaseEvidence(t, path)
			_, err := Open(path, good)
			requireStartupKind(t, err, tc.kind)
			assertDatabaseEvidenceUnchanged(t, path, before)
		})
	}
}

func TestCredentialValidationUsesBoundedTypedProjection(t *testing.T) {
	good := fixedVault(t, 0x63)
	oversized := strings.Repeat("Z", 2<<20)
	tests := []struct {
		name   string
		mutate func(*testing.T, *Store)
	}{
		{name: "oversized base URL text", mutate: func(t *testing.T, store *Store) {
			if _, err := store.DB().Exec(`UPDATE endpoints SET base_url=?`, oversized); err != nil {
				t.Fatal(err)
			}
		}},
		{name: "oversized ciphertext text", mutate: func(t *testing.T, store *Store) {
			if _, err := store.DB().Exec(`UPDATE endpoint_keys SET encrypted_secret=?`, oversized); err != nil {
				t.Fatal(err)
			}
		}},
		{name: "oversized base URL blob", mutate: func(t *testing.T, store *Store) {
			if _, err := store.DB().Exec(`UPDATE endpoints SET base_url=?`, []byte(oversized)); err != nil {
				t.Fatal(err)
			}
		}},
		{name: "oversized ciphertext blob", mutate: func(t *testing.T, store *Store) {
			if _, err := store.DB().Exec(`UPDATE endpoint_keys SET encrypted_secret=?`, []byte(oversized)); err != nil {
				t.Fatal(err)
			}
		}},
		{name: "base URL invalid UTF-8 text", mutate: func(t *testing.T, store *Store) {
			if _, err := store.DB().Exec(`UPDATE endpoints SET base_url=CAST(X'fffe' AS TEXT)`); err != nil {
				t.Fatal(err)
			}
		}},
		{name: "ciphertext invalid UTF-8 text", mutate: func(t *testing.T, store *Store) {
			if _, err := store.DB().Exec(`UPDATE endpoint_keys SET encrypted_secret=CAST(X'fffe' AS TEXT)`); err != nil {
				t.Fatal(err)
			}
		}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "bounded.db")
			store := openWithVault(t, path, good)
			seedContextualCredential(t, store)
			tc.mutate(t, store)
			var applicationID, userVersion uint32
			if err := store.DB().QueryRow(`PRAGMA application_id`).Scan(&applicationID); err != nil {
				t.Fatal(err)
			}
			if err := store.DB().QueryRow(`PRAGMA user_version`).Scan(&userVersion); err != nil {
				t.Fatal(err)
			}
			if applicationID != DatabaseApplicationID || userVersion != DatabaseUserVersion {
				t.Fatalf("fixture markers=(%#x,%d)", applicationID, userVersion)
			}
			if err := validateGenerationOneManifest(context.Background(), store.DB()); err != nil {
				t.Fatalf("fixture schema: %v", err)
			}
			if err := store.Close(); err != nil {
				t.Fatal(err)
			}
			before := captureDatabaseEvidence(t, path)
			_, err := Open(path, good)
			requireStartupKind(t, err, StartupCredentialReject)
			assertDatabaseEvidenceUnchanged(t, path, before)
		})
	}
}

func copyFileFixture(t *testing.T, source, destination string) {
	t.Helper()
	in, err := os.Open(source)
	if err != nil {
		t.Fatal(err)
	}
	defer in.Close()
	out, err := os.OpenFile(destination, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := io.Copy(out, in); err != nil {
		_ = out.Close()
		t.Fatal(err)
	}
	if err := out.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestCommittedWALWithoutSourceSHMUsesOnlyPrivateValidationSHM(t *testing.T) {
	good := fixedVault(t, 0x71)
	source := filepath.Join(t.TempDir(), "source.db")
	store := openWithVault(t, source, good)
	seedContextualCredential(t, store)
	if _, err := os.Stat(source + "-wal"); err != nil {
		t.Fatalf("committed WAL missing: %v", err)
	}

	makeFixture := func(name string) string {
		dir := filepath.Join(t.TempDir(), name)
		if err := os.Mkdir(dir, 0o700); err != nil {
			t.Fatal(err)
		}
		_ = os.Chmod(dir, 0o700)
		path := filepath.Join(dir, "database.db")
		copyFileFixture(t, source, path)
		copyFileFixture(t, source+"-wal", path+"-wal")
		return path
	}
	validPath := makeFixture("valid")
	invalidPath := makeFixture("invalid")
	// Commit a page-one marker change only to WAL. Raw main still says
	// generation one; the merged read-only copy must observe and reject two.
	if _, err := store.DB().Exec(`PRAGMA user_version=2`); err != nil {
		t.Fatal(err)
	}
	markerPath := makeFixture("wal-marker")
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}

	checkedBeforeWritable := false
	beforeWritableOpenHook = func() {
		checkedBeforeWritable = true
		if _, err := os.Lstat(validPath + "-shm"); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("source SHM appeared during read-only validation: %v", err)
		}
	}
	t.Cleanup(func() { beforeWritableOpenHook = nil })
	valid := openWithVault(t, validPath, good)
	beforeWritableOpenHook = nil
	if !checkedBeforeWritable {
		t.Fatal("writable-open boundary hook did not run")
	}
	var users int
	if err := valid.DB().QueryRow(`SELECT COUNT(*) FROM users WHERE discord_id='credential-owner'`).Scan(&users); err != nil || users != 1 {
		t.Fatalf("committed WAL frame was not visible: users=%d err=%v", users, err)
	}
	if err := valid.Close(); err != nil {
		t.Fatal(err)
	}

	invalidBefore := captureDatabaseEvidence(t, invalidPath)
	_, err := Open(invalidPath, fixedVault(t, 0x72))
	requireStartupKind(t, err, StartupCredentialReject)
	assertDatabaseEvidenceUnchanged(t, invalidPath, invalidBefore)

	markerBefore := captureDatabaseEvidence(t, markerPath)
	_, err = Open(markerPath, good)
	requireStartupKind(t, err, StartupWrongGeneration)
	assertDatabaseEvidenceUnchanged(t, markerPath, markerBefore)
}

func TestSourceChangesDuringCopyOrBeforeWritableOpenFailClosed(t *testing.T) {
	for _, stage := range []string{"after_copy", "before_writable"} {
		t.Run(stage, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), stage+".db")
			store := openTestStore(t, path)
			if err := store.Close(); err != nil {
				t.Fatal(err)
			}
			mutate := func() {
				f, err := os.OpenFile(path, os.O_WRONLY|os.O_APPEND, 0)
				if err != nil {
					t.Fatal(err)
				}
				if _, err := f.Write([]byte{0}); err != nil {
					_ = f.Close()
					t.Fatal(err)
				}
				_ = f.Close()
			}
			if stage == "after_copy" {
				afterSnapshotCopyHook = mutate
				t.Cleanup(func() { afterSnapshotCopyHook = nil })
			} else {
				beforeWritableOpenHook = mutate
				t.Cleanup(func() { beforeWritableOpenHook = nil })
			}
			_, err := Open(path, fixedVault(t, 0x73))
			afterSnapshotCopyHook = nil
			beforeWritableOpenHook = nil
			requireStartupKind(t, err, StartupSourceChanged)
		})
	}
}

func TestRollbackJournalAppearingDuringValidationFailsClosed(t *testing.T) {
	path := filepath.Join(t.TempDir(), "journal-appeared.db")
	store := openTestStore(t, path)
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	journalPath := path + "-journal"
	afterSnapshotCopyHook = func() {
		if err := os.WriteFile(journalPath, []byte("appeared during validation"), 0o600); err != nil {
			t.Fatal(err)
		}
		_ = os.Chmod(journalPath, 0o600)
	}
	t.Cleanup(func() { afterSnapshotCopyHook = nil })
	_, err := Open(path, fixedVault(t, 0x74))
	afterSnapshotCopyHook = nil
	requireStartupKind(t, err, StartupSourceChanged)
	data, readErr := os.ReadFile(journalPath)
	if readErr != nil || string(data) != "appeared during validation" {
		t.Fatalf("appearing journal was changed or removed: %q %v", data, readErr)
	}
}

func TestCleanupRefusesReplacedOwnedFiles(t *testing.T) {
	workspace, err := newValidationWorkspace()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(workspace.mainPath, []byte("owned"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := workspace.recordOwned(workspace.mainPath); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(workspace.mainPath); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(workspace.mainPath, []byte("replacement"), 0o600); err != nil {
		t.Fatal(err)
	}
	requireStartupKind(t, workspace.cleanup(), StartupCleanupFailure)
	data, err := os.ReadFile(workspace.mainPath)
	if err != nil || string(data) != "replacement" {
		t.Fatalf("replacement was removed or changed: %q %v", data, err)
	}
	if err := os.Remove(workspace.mainPath); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(workspace.dir); err != nil {
		t.Fatal(err)
	}

	path := filepath.Join(t.TempDir(), "fresh-owned.db")
	if err := os.WriteFile(path, []byte("owned"), 0o600); err != nil {
		t.Fatal(err)
	}
	ownedInfo, err := os.Lstat(path)
	if err != nil {
		t.Fatal(err)
	}
	owned := newFreshOwnership(path, ownedInfo)
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("replacement"), 0o600); err != nil {
		t.Fatal(err)
	}
	requireStartupKind(t, owned.cleanup(), StartupCleanupFailure)
	data, err = os.ReadFile(path)
	if err != nil || string(data) != "replacement" {
		t.Fatalf("fresh replacement was removed or changed: %q %v", data, err)
	}
}

func TestCopySnapshotFailureRemovesOnlyItsPartialFile(t *testing.T) {
	sourcePath := filepath.Join(t.TempDir(), "source")
	if err := os.WriteFile(sourcePath, []byte("source-data"), 0o600); err != nil {
		t.Fatal(err)
	}
	source, err := captureSourceFile(sourcePath, "test source")
	if err != nil {
		t.Fatal(err)
	}
	defer source.file.Close()

	// Force the copy-length check to fail after O_EXCL creation. Cleanup must
	// use the identity captured from that exact file, not a later path lookup.
	source.size++
	destination := filepath.Join(t.TempDir(), "partial")
	requireStartupKind(t, copySnapshot(source, destination), StartupSourceChanged)
	if _, err := os.Lstat(destination); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("partial copy remained after failure: %v", err)
	}
}

func TestGameSchemaOwnershipIdempotencyPendingAndRetentionInvariants(t *testing.T) {
	store := openTestStore(t, filepath.Join(t.TempDir(), "game-schema.db"))
	defer store.Close()
	d := store.DB()
	if _, err := d.Exec(`INSERT INTO users(id,discord_id,username,created_at,updated_at) VALUES(1,'g1','g1',1,1),(2,'g2','g2',1,1)`); err != nil {
		t.Fatal(err)
	}
	insertSettlement := func(id string, userID int) {
		t.Helper()
		if _, err := d.Exec(`INSERT INTO game_settlements(id,user_id,game_type,game_version,state,entry_milli,payout_milli,auto_settle_at,next_attempt_at,created_at,updated_at) VALUES(?,?,'fishing',1,'reserved',10,20,121,121,1,1)`, id, userID); err != nil {
			t.Fatal(err)
		}
	}
	hash := bytes.Repeat([]byte{1}, sha256.Size)
	requestHash := bytes.Repeat([]byte{2}, sha256.Size)
	insertSettlement("settlement-1", 1)
	if _, err := d.Exec(`INSERT INTO game_rounds(id,user_id,game_type,game_version,settlement_id,start_key_hash,start_request_hash,created_at,updated_at) VALUES('round-1',1,'fishing',1,'settlement-1',?,?,1,1)`, hash, requestHash); err != nil {
		t.Fatal(err)
	}
	insertSettlement("settlement-2", 1)
	if _, err := d.Exec(`INSERT INTO game_rounds(id,user_id,game_type,game_version,settlement_id,start_key_hash,start_request_hash,created_at,updated_at) VALUES('round-2',1,'fishing',1,'settlement-2',?,?,1,1)`, bytes.Repeat([]byte{3}, 32), requestHash); err == nil {
		t.Fatal("partial unique index allowed two pending rounds")
	}
	insertSettlement("settlement-other", 2)
	if _, err := d.Exec(`INSERT INTO game_rounds(id,user_id,game_type,game_version,settlement_id,start_key_hash,start_request_hash,created_at,updated_at) VALUES('wrong-owner',1,'fishing',1,'settlement-other',?,?,1,1)`, bytes.Repeat([]byte{4}, 32), requestHash); err == nil {
		t.Fatal("composite ownership FK accepted a cross-user settlement")
	}
	if _, err := d.Exec(`INSERT INTO game_fishing_outcomes(round_id,bait,species_key,tier,size_cm) VALUES('round-1','worm','whitebait','small',12)`); err != nil {
		t.Fatal(err)
	}
	if _, err := d.Exec(`INSERT INTO game_fishing_best(user_id,round_id,species_key,tier,size_cm,caught_at) VALUES(1,'round-1','whitebait','small',12,1)`); err != nil {
		t.Fatal(err)
	}
	if _, err := d.Exec(`INSERT INTO credit_ledger(operation_id,user_id,kind,credits_after,donation_credit_after,game_settlement_id,created_at) VALUES('sys.game.1',1,'game_settlement',0,0,'settlement-1',1)`); err != nil {
		t.Fatal(err)
	}
	if _, err := d.Exec(`INSERT INTO credit_ledger(operation_id,user_id,kind,credits_after,donation_credit_after,game_settlement_id,created_at) VALUES('sys.game.1',1,'game_settlement',0,0,'settlement-1',1)`); err == nil {
		t.Fatal("operation idempotency key allowed a duplicate ledger write")
	}
	if _, err := d.Exec(`INSERT INTO credit_ledger(operation_id,user_id,kind,credits_after,donation_credit_after,reservation_id,game_settlement_id,created_at) VALUES('bad-correlation',1,'game_settlement',0,0,1,'settlement-1',1)`); err == nil {
		t.Fatal("ledger accepted charity and game correlations together")
	}
	var retentionIndex int
	if err := d.QueryRow(`SELECT COUNT(*) FROM sqlite_schema WHERE type='index' AND name='idx_game_rounds_retention' AND sql LIKE '%settled_at IS NOT NULL%'`).Scan(&retentionIndex); err != nil || retentionIndex != 1 {
		t.Fatalf("retention index=(%d,%v)", retentionIndex, err)
	}
	if _, err := d.Exec(`DELETE FROM game_settlements WHERE id='settlement-1'`); err != nil {
		t.Fatal(err)
	}
	var rounds, outcomes int
	var bestRound sql.NullString
	if err := d.QueryRow(`SELECT COUNT(*) FROM game_rounds WHERE id='round-1'`).Scan(&rounds); err != nil {
		t.Fatal(err)
	}
	if err := d.QueryRow(`SELECT COUNT(*) FROM game_fishing_outcomes WHERE round_id='round-1'`).Scan(&outcomes); err != nil {
		t.Fatal(err)
	}
	if err := d.QueryRow(`SELECT round_id FROM game_fishing_best WHERE user_id=1`).Scan(&bestRound); err != nil {
		t.Fatal(err)
	}
	if rounds != 0 || outcomes != 0 || bestRound.Valid {
		t.Fatalf("retention cascade rounds=%d outcomes=%d bestRound=%v", rounds, outcomes, bestRound)
	}
}

func TestGenerationOneNewColumnsDefaultsAndHardRanges(t *testing.T) {
	store := openTestStore(t, filepath.Join(t.TempDir(), "new-columns.db"))
	defer store.Close()
	d := store.DB()
	if _, err := d.Exec(`INSERT INTO users(id,discord_id,username,created_at,updated_at) VALUES(1,'u','u',1,1)`); err != nil {
		t.Fatal(err)
	}
	var concurrency sql.NullInt64
	var public int
	if err := d.QueryRow(`SELECT concurrency_limit,game_profile_public FROM users WHERE id=1`).Scan(&concurrency, &public); err != nil || concurrency.Valid || public != 0 {
		t.Fatalf("user defaults=(%v,%d,%v)", concurrency, public, err)
	}
	for _, statement := range []string{
		`UPDATE users SET concurrency_limit=0 WHERE id=1`,
		`UPDATE users SET concurrency_limit=100001 WHERE id=1`,
		`UPDATE users SET rpm_limit=0 WHERE id=1`,
		`UPDATE users SET rpm_limit=4097 WHERE id=1`,
		`UPDATE users SET endpoint_limit=-1 WHERE id=1`,
		`UPDATE users SET endpoint_limit=10001 WHERE id=1`,
		`UPDATE users SET game_profile_public=2 WHERE id=1`,
	} {
		if _, err := d.Exec(statement); err == nil {
			t.Fatalf("hard-range statement succeeded: %s", statement)
		}
	}
	if _, err := d.Exec(`INSERT INTO endpoints(user_id,connector_type,base_url,created_at,updated_at) VALUES(1,'unknown','https://example.com',1,1)`); err == nil {
		t.Fatal("unknown connector type passed the schema CHECK")
	}
	if _, err := d.Exec(`INSERT INTO endpoints(id,user_id,connector_type,base_url,created_at,updated_at) VALUES(1,1,'openai-compatible','https://example.com',1,1)`); err != nil {
		t.Fatal(err)
	}
	if _, err := d.Exec(`INSERT INTO endpoint_keys(endpoint_id,encrypted_secret,force_store_false,created_at,updated_at) VALUES(1,'fixture',2,1,1)`); err == nil {
		t.Fatal("invalid force_store_false passed the schema CHECK")
	}
	if _, err := d.Exec(`INSERT INTO models(user_id,provider,model,full_name,flatten_tool_calls,created_at,updated_at) VALUES(1,'p','m','p/m',2,1,1)`); err == nil {
		t.Fatal("invalid personal flatten_tool_calls passed the schema CHECK")
	}
	if _, err := d.Exec(`INSERT INTO charity_models(provider,model,full_name,pricing_mode,flatten_tool_calls,created_at,updated_at) VALUES('p','m','[公益]p/m','per_request',2,1,1)`); err == nil {
		t.Fatal("invalid charity flatten_tool_calls passed the schema CHECK")
	}
}

func TestBackupRestoreReopensAsCurrentGeneration(t *testing.T) {
	vault := fixedVault(t, 0x7f)
	primary := filepath.Join(t.TempDir(), "primary.db")
	store := openWithVault(t, primary, vault)
	seedContextualCredential(t, store)
	if _, err := store.DB().Exec(`PRAGMA wal_checkpoint(TRUNCATE)`); err != nil {
		t.Fatal(err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	restoreDir := filepath.Join(t.TempDir(), "restore")
	if err := os.Mkdir(restoreDir, 0o700); err != nil {
		t.Fatal(err)
	}
	_ = os.Chmod(restoreDir, 0o700)
	restoredPath := filepath.Join(restoreDir, "restored.db")
	copyFileFixture(t, primary, restoredPath)
	restored := openWithVault(t, restoredPath, vault)
	defer restored.Close()
	var count int
	if err := restored.DB().QueryRow(`SELECT COUNT(*) FROM endpoint_keys`).Scan(&count); err != nil || count != 1 {
		t.Fatalf("restored credential count=(%d,%v)", count, err)
	}
}

func TestNoRuntimeMigrationStatementsRemain(t *testing.T) {
	for _, source := range []string{generationOneSchema} {
		upper := strings.ToUpper(source)
		if strings.Contains(upper, "ALTER TABLE") || strings.Contains(upper, "IF NOT EXISTS") {
			t.Fatal("generation-one source contains a migration/idempotent DDL statement")
		}
	}
}
