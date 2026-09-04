package db

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/binary"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/waiting-here/NonbiriAPI/internal/dbtest"
	"github.com/waiting-here/NonbiriAPI/internal/secret"
)

const (
	bootstrapTestCredentialOrigin = "https://api.example.com:443"
	bootstrapTestConnector        = "openai-compatible"
	bootstrapHookWait             = 60 * time.Second
)

// bootstrapTestFileImage is intentionally limited to the four source paths.
// A rejected startup must not rewrite bytes, replace an inode, or adjust the
// source file mode/time while it is doing its read-only preflight.
type bootstrapTestFileImage struct {
	present bool
	data    []byte
	mode    os.FileMode
	modTime time.Time
}

func bootstrapTestVault(t *testing.T) *secret.Vault {
	t.Helper()
	vault := bootstrapTestVaultNoCleanup()
	t.Cleanup(func() { _ = vault.Close() })
	return vault
}

func bootstrapTestVaultNoCleanup() *secret.Vault {
	key := bytes.Repeat([]byte{0x29}, secret.MasterKeyBytes)
	vault, err := secret.New(key)
	clear(key)
	if err != nil {
		panic(fmt.Sprintf("create bootstrap test vault: %v", err))
	}
	return vault
}

func bootstrapTestPath(t *testing.T, name string) string {
	t.Helper()
	dir := privateDBDir(t)
	return filepath.Join(dir, name)
}

func bootstrapSourceSuffixes() []string {
	return []string{"", "-journal", "-wal", "-shm"}
}

func snapshotBootstrapSources(t *testing.T, path string) map[string]bootstrapTestFileImage {
	t.Helper()
	images := make(map[string]bootstrapTestFileImage, len(bootstrapSourceSuffixes()))
	for _, suffix := range bootstrapSourceSuffixes() {
		name := path + suffix
		info, err := os.Lstat(name)
		if errors.Is(err, os.ErrNotExist) {
			images[suffix] = bootstrapTestFileImage{}
			continue
		}
		if err != nil {
			t.Fatalf("snapshot %s: %v", suffix, err)
		}
		if !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 {
			t.Fatalf("snapshot %s is not a regular file", suffix)
		}
		data, err := os.ReadFile(name)
		if err != nil {
			t.Fatalf("read snapshot %s: %v", suffix, err)
		}
		images[suffix] = bootstrapTestFileImage{
			present: true,
			data:    data,
			mode:    info.Mode(),
			modTime: info.ModTime(),
		}
	}
	return images
}

func assertBootstrapSourcesUnchanged(t *testing.T, path string, before map[string]bootstrapTestFileImage) {
	t.Helper()
	for _, suffix := range bootstrapSourceSuffixes() {
		want := before[suffix]
		name := path + suffix
		info, err := os.Lstat(name)
		if !want.present {
			if !errors.Is(err, os.ErrNotExist) {
				t.Errorf("source %s was created during rejected startup", suffix)
			}
			continue
		}
		if err != nil {
			t.Errorf("source %s disappeared after rejected startup: %v", suffix, err)
			continue
		}
		if !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 {
			t.Errorf("source %s changed to a non-regular file", suffix)
			continue
		}
		got, readErr := os.ReadFile(name)
		if readErr != nil {
			t.Errorf("read source %s after rejected startup: %v", suffix, readErr)
			continue
		}
		if !bytes.Equal(want.data, got) {
			t.Errorf("source %s bytes changed during rejected startup", suffix)
		}
		if info.Mode() != want.mode {
			t.Errorf("source %s mode changed from %v to %v", suffix, want.mode, info.Mode())
		}
		if !info.ModTime().Equal(want.modTime) {
			t.Errorf("source %s modification time changed during rejected startup", suffix)
		}
	}
}

func assertBootstrapStartupKind(t *testing.T, err error, want StartupErrorKind) *StartupError {
	t.Helper()
	if err == nil {
		t.Fatalf("expected startup error %q", want)
	}
	var startupErr *StartupError
	if !errors.As(err, &startupErr) {
		t.Fatalf("expected StartupError %q, got %T: %v", want, err, err)
	}
	if startupErr.Kind != want {
		t.Fatalf("startup error kind = %q, want %q", startupErr.Kind, want)
	}
	return startupErr
}

func preserveBootstrapHooks(t *testing.T) {
	t.Helper()
	oldBefore := beforeWritableOpenHook
	oldAfterCopy := afterSnapshotCopyHook
	oldBeforeFreshCreate := beforeFreshExclusiveCreateHook
	oldFreshFailure := freshSchemaFailureHook
	oldRecovery := writableRecoveryPhaseHook
	t.Cleanup(func() {
		beforeWritableOpenHook = oldBefore
		afterSnapshotCopyHook = oldAfterCopy
		beforeFreshExclusiveCreateHook = oldBeforeFreshCreate
		freshSchemaFailureHook = oldFreshFailure
		writableRecoveryPhaseHook = oldRecovery
	})
	beforeWritableOpenHook = nil
	afterSnapshotCopyHook = nil
	beforeFreshExclusiveCreateHook = nil
	freshSchemaFailureHook = nil
	writableRecoveryPhaseHook = nil
}

func writeBootstrapFile(t *testing.T, path string, data []byte) {
	t.Helper()
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatalf("write fixture %s: %v", path, err)
	}
	if err := os.Chmod(path, 0o600); err != nil {
		t.Fatalf("chmod fixture %s: %v", path, err)
	}
}

func createBootstrapMarkerDB(t *testing.T, path string, applicationID, generation uint32) {
	t.Helper()
	dbtest.EnsureOwnerOnlyParent(t, path)
	database, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatalf("open marker fixture: %v", err)
	}
	database.SetMaxOpenConns(1)
	if _, err := database.Exec(fmt.Sprintf("PRAGMA application_id=%d; PRAGMA user_version=%d;", applicationID, generation)); err != nil {
		_ = database.Close()
		t.Fatalf("write marker fixture: %v", err)
	}
	if err := database.Close(); err != nil {
		t.Fatalf("close marker fixture: %v", err)
	}
	if err := os.Chmod(path, 0o600); err != nil {
		t.Fatalf("chmod marker fixture: %v", err)
	}
}

func mutateBootstrapCurrent(t *testing.T, path, statement string, args ...any) {
	t.Helper()
	database, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatalf("open current database for external mutation: %v", err)
	}
	database.SetMaxOpenConns(1)
	if _, err := database.Exec(statement, args...); err != nil {
		_ = database.Close()
		t.Fatalf("mutate current database: %v", err)
	}
	if err := database.Close(); err != nil {
		t.Fatalf("close external mutation database: %v", err)
	}
}

func mutateBootstrapSchemaObjectSQL(t *testing.T, path, objectType, objectName, oldFragment, newFragment string) {
	t.Helper()
	database, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatalf("open current database to mutate schema object: %v", err)
	}
	database.SetMaxOpenConns(1)
	defer func() { _ = database.Close() }()

	var original string
	if err := database.QueryRow(`SELECT sql FROM sqlite_schema WHERE type=? AND name=?`, objectType, objectName).Scan(&original); err != nil {
		t.Fatalf("read schema object %s: %v", objectName, err)
	}
	if !strings.Contains(original, oldFragment) {
		t.Fatalf("schema object %s does not contain current contract", objectName)
	}
	mutated := strings.Replace(original, oldFragment, newFragment, 1)
	if _, err := database.Exec(`PRAGMA writable_schema=ON`); err != nil {
		t.Fatalf("enable writable schema for %s: %v", objectName, err)
	}
	result, err := database.Exec(`UPDATE sqlite_schema SET sql=? WHERE type=? AND name=?`, mutated, objectType, objectName)
	if err != nil {
		t.Fatalf("mutate schema object %s: %v", objectName, err)
	}
	if rows, rowsErr := result.RowsAffected(); rowsErr != nil || rows != 1 {
		if rowsErr != nil {
			t.Fatalf("observe schema object %s mutation: %v", objectName, rowsErr)
		}
		t.Fatalf("schema object %s mutation changed %d rows", objectName, rows)
	}
	if _, err := database.Exec(`PRAGMA writable_schema=OFF`); err != nil {
		t.Fatalf("disable writable schema for %s: %v", objectName, err)
	}
}

func insertBootstrapSecretRow(t *testing.T, path string, contextID []byte, encrypted string, orphanedAt any) int64 {
	t.Helper()
	database, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatalf("open database to insert secret fixture: %v", err)
	}
	database.SetMaxOpenConns(1)
	defer func() { _ = database.Close() }()
	result, err := database.Exec(`
INSERT INTO endpoint_key_secrets(
 context_id,canonical_base_url,connector_type,encrypted_secret,created_at,orphaned_at
) VALUES(?,?,?,?,?,?)`, contextID, bootstrapTestCredentialOrigin, bootstrapTestConnector, encrypted, 0, orphanedAt)
	if err != nil {
		t.Fatalf("insert endpoint credential fixture: %v", err)
	}
	id, err := result.LastInsertId()
	if err != nil {
		t.Fatalf("read endpoint credential fixture id: %v", err)
	}
	return id
}

func insertBootstrapOrphanRows(t *testing.T, store *Store, vault *secret.Vault, count int, orphanedAt *int64, seed uint64) []int64 {
	t.Helper()
	if count < 0 {
		t.Fatalf("negative orphan fixture count %d", count)
	}
	tx, err := store.DB().BeginTx(context.Background(), nil)
	if err != nil {
		t.Fatalf("begin orphan fixture transaction: %v", err)
	}
	rollback := true
	defer func() {
		if rollback {
			_ = tx.Rollback()
		}
	}()
	ids := make([]int64, 0, count)
	for i := 0; i < count; i++ {
		contextID := make([]byte, 16)
		binary.BigEndian.PutUint64(contextID[:8], seed)
		binary.BigEndian.PutUint64(contextID[8:], uint64(i)+1)
		credentialContext, err := secret.NewGenerationTwoEndpointKeyContext(contextID)
		if err != nil {
			t.Fatalf("create orphan credential context: %v", err)
		}
		envelope, err := vault.SealForGenerationTwoContext([]byte("orphan-test-credential"), credentialContext)
		clear(contextID)
		if err != nil {
			t.Fatalf("seal orphan credential fixture: %v", err)
		}
		result, err := tx.ExecContext(context.Background(), `
INSERT INTO endpoint_key_secrets(
 context_id,canonical_base_url,connector_type,encrypted_secret,created_at,orphaned_at
) VALUES(?,?,?,?,?,?)`, credentialContext.ContextID(), bootstrapTestCredentialOrigin, bootstrapTestConnector, envelope, 0, orphanedAt)
		clear([]byte(envelope))
		if err != nil {
			t.Fatalf("insert orphan credential fixture %d: %v", i, err)
		}
		id, err := result.LastInsertId()
		if err != nil {
			t.Fatalf("read orphan credential fixture id %d: %v", i, err)
		}
		ids = append(ids, id)
	}
	if err := tx.Commit(); err != nil {
		t.Fatalf("commit orphan fixture transaction: %v", err)
	}
	rollback = false
	return ids
}

func queryBootstrapAnnouncementEpoch(t *testing.T, store *Store) string {
	t.Helper()
	var epoch string
	if err := store.DB().QueryRow(`SELECT value FROM site_config WHERE key='announcement_epoch'`).Scan(&epoch); err != nil {
		t.Fatalf("query announcement epoch: %v", err)
	}
	return epoch
}

func queryBootstrapIdentity(t *testing.T, store *Store) (uint32, uint32) {
	t.Helper()
	var applicationID, generation uint32
	if err := store.DB().QueryRow(`PRAGMA application_id`).Scan(&applicationID); err != nil {
		t.Fatalf("query application id: %v", err)
	}
	if err := store.DB().QueryRow(`PRAGMA user_version`).Scan(&generation); err != nil {
		t.Fatalf("query user version: %v", err)
	}
	return applicationID, generation
}

func TestGenerationTwoBootstrapFreshRequiresAbsentSidecarsAndSeedsIdentity(t *testing.T) {
	path := bootstrapTestPath(t, "fresh.db")
	for _, suffix := range bootstrapSourceSuffixes() {
		if _, err := os.Lstat(path + suffix); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("fresh fixture source %s unexpectedly exists: %v", suffix, err)
		}
	}

	store := openTestStore(t, path)
	applicationID, generation := queryBootstrapIdentity(t, store)
	if applicationID != DatabaseApplicationID || generation != DatabaseUserVersion {
		t.Fatalf("fresh identity = (%#x,%d), want (%#x,%d)", applicationID, generation, DatabaseApplicationID, DatabaseUserVersion)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat fresh database: %v", err)
	}
	if !info.Mode().IsRegular() {
		t.Fatal("fresh database is not a regular file")
	}
	if runtime.GOOS != "windows" && info.Mode().Perm() != 0o600 {
		t.Fatalf("fresh database mode = %o, want 600", info.Mode().Perm())
	}
}

func TestGenerationTwoCurrentFreshEpochAndIdentityAreStableAcrossRestart(t *testing.T) {
	path := bootstrapTestPath(t, "restart.db")
	first := openTestStore(t, path)
	firstEpoch := queryBootstrapAnnouncementEpoch(t, first)
	firstApplicationID, firstGeneration := queryBootstrapIdentity(t, first)
	if err := first.Close(); err != nil {
		t.Fatalf("close first store: %v", err)
	}

	secondVault := bootstrapTestVault(t)
	second, err := Open(path, secondVault)
	if err != nil {
		t.Fatalf("reopen current Generation Two database: %v", err)
	}
	t.Cleanup(func() { _ = second.Close() })
	secondEpoch := queryBootstrapAnnouncementEpoch(t, second)
	secondApplicationID, secondGeneration := queryBootstrapIdentity(t, second)
	if secondEpoch != firstEpoch {
		t.Fatalf("announcement epoch changed across restart: %q -> %q", firstEpoch, secondEpoch)
	}
	if secondApplicationID != firstApplicationID || secondGeneration != firstGeneration {
		t.Fatalf("identity changed across restart: (%#x,%d) -> (%#x,%d)", firstApplicationID, firstGeneration, secondApplicationID, secondGeneration)
	}
}

func TestGenerationTwoCurrentEnabledRPSSnapshotReopensWithSameVault(t *testing.T) {
	path := bootstrapTestPath(t, "restart-enabled-rps.db")
	vault := bootstrapTestVault(t)
	first, err := Open(path, vault)
	if err != nil {
		t.Fatalf("fresh Generation Two open: %v", err)
	}
	if first == nil || first.DB() == nil {
		t.Fatal("fresh Generation Two open returned no store")
	}

	// Apply a structurally valid enabled snapshot one key at a time so every
	// mutation exercises the same transaction/revision path used by the
	// configuration owner. RPS health is deliberately not consulted here; the
	// games owner supplies that explicit gate when activation is requested.
	for _, setting := range []struct{ key, value string }{
		{key: "games_enabled", value: "1"},
		{key: "game_rps_enabled", value: "1"},
		{key: "game_rps_quick_b_milli", value: "1"},
		{key: "game_rps_quick_enabled", value: "1"},
	} {
		tx, txErr := first.DB().BeginTx(context.Background(), nil)
		if txErr != nil {
			_ = first.Close()
			t.Fatalf("begin RPS configuration transaction for %s: %v", setting.key, txErr)
		}
		if txErr = validateAndWriteGenerationTwoSiteConfigTx(context.Background(), tx, setting.key, setting.value, 1); txErr != nil {
			_ = tx.Rollback()
			_ = first.Close()
			t.Fatalf("write RPS configuration %s=%s: %v", setting.key, setting.value, txErr)
		}
		if txErr = tx.Commit(); txErr != nil {
			_ = first.Close()
			t.Fatalf("commit RPS configuration %s=%s: %v", setting.key, setting.value, txErr)
		}
	}
	if err := first.Close(); err != nil {
		t.Fatalf("close enabled RPS Generation Two store: %v", err)
	}

	second, err := Open(path, vault)
	if err != nil {
		t.Fatalf("reopen enabled RPS Generation Two store: %v", err)
	}
	t.Cleanup(func() { _ = second.Close() })
	values, err := readGenerationTwoSiteConfigSnapshot(context.Background(), second.DB())
	if err != nil {
		t.Fatalf("read enabled RPS snapshot after reopen: %v", err)
	}
	if err := ValidateGenerationTwoConfigSnapshot(generationTwoConfigSnapshotForValidation(values)); err != nil {
		t.Fatalf("enabled RPS snapshot failed structural validation after reopen: %v", err)
	}
	if err := ValidateGenerationTwoRPSConfigHealth(generationTwoConfigSnapshotForValidation(values), false); err == nil {
		t.Fatal("explicitly unhealthy RPS readiness was accepted")
	}
	if err := ValidateGenerationTwoRPSConfigHealth(generationTwoConfigSnapshotForValidation(values), true); err != nil {
		t.Fatalf("explicitly healthy RPS readiness rejected after reopen: %v", err)
	}
}

func TestGenerationTwoCurrentEnabledWelfareZeroSnapshotReopensWithSameVault(t *testing.T) {
	path := bootstrapTestPath(t, "restart-enabled-welfare-zero.db")
	vault := bootstrapTestVault(t)
	first, err := Open(path, vault)
	if err != nil {
		t.Fatalf("fresh Generation Two open: %v", err)
	}
	if first == nil || first.DB() == nil {
		t.Fatal("fresh Generation Two open returned no store")
	}

	for _, setting := range []struct{ key, value string }{
		{key: "site_timezone_offset_minutes", value: "0"},
		{key: "activity_welfare_threshold_milli", value: "0"},
		{key: "activity_welfare_cap_milli", value: "0"},
		{key: "activities_enabled", value: "1"},
		{key: "activity_welfare_enabled", value: "1"},
	} {
		tx, txErr := first.DB().BeginTx(context.Background(), nil)
		if txErr != nil {
			_ = first.Close()
			t.Fatalf("begin welfare configuration transaction for %s: %v", setting.key, txErr)
		}
		if txErr = validateAndWriteGenerationTwoSiteConfigTx(context.Background(), tx, setting.key, setting.value, 1); txErr != nil {
			_ = tx.Rollback()
			_ = first.Close()
			t.Fatalf("write welfare configuration %s=%s: %v", setting.key, setting.value, txErr)
		}
		if txErr = tx.Commit(); txErr != nil {
			_ = first.Close()
			t.Fatalf("commit welfare configuration %s=%s: %v", setting.key, setting.value, txErr)
		}
	}
	if err := first.Close(); err != nil {
		t.Fatalf("close enabled welfare Generation Two store: %v", err)
	}

	second, err := Open(path, vault)
	if err != nil {
		t.Fatalf("reopen enabled welfare Generation Two store: %v", err)
	}
	t.Cleanup(func() { _ = second.Close() })
	values, err := readGenerationTwoSiteConfigSnapshot(context.Background(), second.DB())
	if err != nil {
		t.Fatalf("read enabled welfare snapshot after reopen: %v", err)
	}
	clean := generationTwoConfigSnapshotForValidation(values)
	if err := ValidateGenerationTwoConfigSnapshot(clean); err != nil {
		t.Fatalf("enabled welfare snapshot failed validation after reopen: %v", err)
	}
	for key, want := range map[string]string{
		"site_timezone_offset_minutes":     "0",
		"activities_enabled":               "1",
		"activity_welfare_enabled":         "1",
		"activity_welfare_threshold_milli": "0",
		"activity_welfare_cap_milli":       "0",
	} {
		if got := clean[key]; got != want {
			t.Fatalf("reopened welfare configuration %s=%q, want %q", key, got, want)
		}
	}
}

func TestGenerationTwoFreshFailureIsAtomicAndCleansOnlyOwnedFile(t *testing.T) {
	preserveBootstrapHooks(t)
	path := bootstrapTestPath(t, "fresh-failure.db")
	freshSchemaFailureHook = func() error { return errors.New("injected fresh transaction failure") }

	_, err := Open(path, bootstrapTestVault(t))
	assertBootstrapStartupKind(t, err, StartupInitialization)
	for _, suffix := range bootstrapSourceSuffixes() {
		if _, statErr := os.Lstat(path + suffix); !errors.Is(statErr, os.ErrNotExist) {
			t.Fatalf("failed fresh startup left source %s: %v", suffix, statErr)
		}
	}
}

func TestGenerationTwoFreshConcurrentSingleOEXCLWinnerDoesNotDeleteWinner(t *testing.T) {
	preserveBootstrapHooks(t)
	path := bootstrapTestPath(t, "concurrent.db")
	const contenderCount = 9
	entered := make(chan struct{}, contenderCount)
	release := make(chan struct{})
	var releaseOnce sync.Once
	releaseContenders := func() { releaseOnce.Do(func() { close(release) }) }
	beforeFreshExclusiveCreateHook = func() {
		entered <- struct{}{}
		<-release
	}

	type result struct {
		store *Store
		err   error
		vault *secret.Vault
	}
	results := make(chan result, contenderCount)
	var wg sync.WaitGroup
	for i := 0; i < contenderCount; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			vault := bootstrapTestVaultNoCleanup()
			store, err := Open(path, vault)
			results <- result{store: store, err: err, vault: vault}
		}()
	}

	timer := time.NewTimer(bootstrapHookWait)
	arrivals := 0
	var early *result
	for arrivals < contenderCount && early == nil {
		select {
		case <-entered:
			arrivals++
		case outcome := <-results:
			early = &outcome
		case <-timer.C:
			releaseContenders()
			wg.Wait()
			close(results)
			for outcome := range results {
				if outcome.store != nil {
					_ = outcome.store.Close()
				}
				_ = outcome.vault.Close()
			}
			t.Fatalf("only %d/%d fresh contenders reached the pre-O_EXCL seam within %s", arrivals, contenderCount, bootstrapHookWait)
		}
	}
	if !timer.Stop() {
		select {
		case <-timer.C:
		default:
		}
	}
	releaseContenders()
	wg.Wait()
	close(results)

	outcomes := make([]result, 0, contenderCount)
	if early != nil {
		outcomes = append(outcomes, *early)
	}
	for outcome := range results {
		outcomes = append(outcomes, outcome)
	}
	for _, outcome := range outcomes {
		if outcome.store != nil {
			if err := outcome.store.Close(); err != nil {
				t.Errorf("close fresh contender store: %v", err)
			}
		}
		if err := outcome.vault.Close(); err != nil {
			t.Errorf("close fresh contender vault: %v", err)
		}
	}
	if early != nil {
		t.Fatalf("fresh contender returned before all callers reached the pre-O_EXCL seam: %v", early.err)
	}
	if len(outcomes) != contenderCount {
		t.Fatalf("fresh contender outcomes=%d, want %d", len(outcomes), contenderCount)
	}
	winners := 0
	for _, outcome := range outcomes {
		if outcome.err == nil && outcome.store != nil {
			winners++
			continue
		}
		if outcome.store != nil {
			t.Fatalf("failed fresh contender returned a store: %v", outcome.err)
		}
		assertBootstrapStartupKind(t, outcome.err, StartupInitialization)
	}
	if winners != 1 {
		t.Fatalf("fresh O_EXCL winners=%d, want 1", winners)
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("winner source disappeared while losers failed: %v", err)
	}

	reopened := bootstrapTestVault(t)
	store, err := Open(path, reopened)
	if err != nil {
		t.Fatalf("winner database was damaged by loser cleanup: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })
	if epoch := queryBootstrapAnnouncementEpoch(t, store); epoch == "" {
		t.Fatal("winner database has no announcement epoch")
	}
}

func TestGenerationTwoRejectZeroByteDatabaseWithoutSourceWrite(t *testing.T) {
	path := bootstrapTestPath(t, "zero-byte.db")
	writeBootstrapFile(t, path, nil)
	before := snapshotBootstrapSources(t, path)
	_, err := Open(path, bootstrapTestVault(t))
	assertBootstrapStartupKind(t, err, StartupIncompleteFresh)
	assertBootstrapSourcesUnchanged(t, path, before)
}

func TestGenerationTwoRejectAlpha3AndUnknownGenerationsWithoutSourceWrite(t *testing.T) {
	cases := []struct {
		name       string
		generation uint32
	}{
		{name: "alpha3", generation: 1},
		{name: "zero", generation: 0},
		{name: "unknown-high", generation: 3},
		{name: "unknown-large", generation: 99},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			path := bootstrapTestPath(t, tc.name+".db")
			createBootstrapMarkerDB(t, path, DatabaseApplicationID, tc.generation)
			before := snapshotBootstrapSources(t, path)
			_, err := Open(path, bootstrapTestVault(t))
			startupErr := assertBootstrapStartupKind(t, err, StartupWrongGeneration)
			if startupErr.ExpectedGeneration != DatabaseUserVersion || startupErr.ActualGeneration != tc.generation {
				t.Fatalf("generation details = (%d,%d), want (%d,%d)", startupErr.ExpectedGeneration, startupErr.ActualGeneration, DatabaseUserVersion, tc.generation)
			}
			assertBootstrapSourcesUnchanged(t, path, before)
		})
	}
}

func TestGenerationTwoRejectWrongApplicationIDWithoutSourceWrite(t *testing.T) {
	path := bootstrapTestPath(t, "wrong-application.db")
	createBootstrapMarkerDB(t, path, 0x01020304, DatabaseUserVersion)
	before := snapshotBootstrapSources(t, path)
	_, err := Open(path, bootstrapTestVault(t))
	assertBootstrapStartupKind(t, err, StartupWrongIdentity)
	assertBootstrapSourcesUnchanged(t, path, before)
}

func TestGenerationTwoCurrentRejectsExternallyMutatedValidDatabaseWithoutSourceWrite(t *testing.T) {
	cases := []struct {
		name   string
		mutate func(t *testing.T, path string)
	}{
		{name: "missing-schema-index", mutate: func(t *testing.T, path string) {
			t.Helper()
			mutateBootstrapCurrent(t, path, `DROP INDEX idx_endpoints_user_cursor`)
		}},
		{name: "extra-schema-table", mutate: func(t *testing.T, path string) {
			t.Helper()
			mutateBootstrapCurrent(t, path, `CREATE TABLE bootstrap_extra_schema_object(id INTEGER PRIMARY KEY)`)
		}},
		{name: "old-maintenance-actor-contract", mutate: func(t *testing.T, path string) {
			t.Helper()
			const currentActorContract = "CHECK((actor_user_id IS NULL AND actor_discord_id IS NULL AND actor_role IS NULL) OR (actor_user_id IS NOT NULL AND actor_discord_id IS NULL AND actor_role IS NOT NULL AND actor_role='admin') OR (actor_user_id IS NOT NULL AND actor_discord_id IS NOT NULL AND actor_role IS NOT NULL AND actor_role='steward'))"
			const oldActorContract = "CHECK((actor_user_id IS NULL AND actor_discord_id IS NULL AND actor_role IS NULL) OR (actor_user_id IS NOT NULL AND actor_discord_id IS NOT NULL AND actor_role IS NOT NULL))"
			mutateBootstrapSchemaObjectSQL(t, path, "table", "maintenance_events", currentActorContract, oldActorContract)
		}},
		{name: "old-maintenance-deidentification-trigger", mutate: func(t *testing.T, path string) {
			t.Helper()
			const currentDeadlineContract = " OR\n     (OLD.resolved_at IS NOT NULL AND OLD.deidentify_at IS NOT NULL AND unixepoch()>=OLD.deidentify_at)"
			mutateBootstrapSchemaObjectSQL(t, path, "trigger", "maintenance_events_no_update", currentDeadlineContract, "")
		}},
		{name: "missing-fixed-seed", mutate: func(t *testing.T, path string) {
			t.Helper()
			mutateBootstrapCurrent(t, path, `DELETE FROM credit_accounts WHERE code='external'`)
		}},
		{name: "missing-thursday-seed", mutate: func(t *testing.T, path string) {
			t.Helper()
			mutateBootstrapCurrent(t, path, `DELETE FROM shared_pools WHERE pool_type='thursday' AND period_id IS NULL`)
		}},
		{name: "malformed-known-config", mutate: func(t *testing.T, path string) {
			t.Helper()
			mutateBootstrapCurrent(t, path, `UPDATE site_config SET value='2' WHERE key='maintenance_mode'`)
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			path := bootstrapTestPath(t, tc.name+".db")
			fixture := openTestStore(t, path)
			if err := fixture.Close(); err != nil {
				t.Fatalf("close valid current fixture: %v", err)
			}
			tc.mutate(t, path)
			before := snapshotBootstrapSources(t, path)
			_, err := Open(path, bootstrapTestVault(t))
			assertBootstrapStartupKind(t, err, StartupSchemaMismatch)
			assertBootstrapSourcesUnchanged(t, path, before)
		})
	}
}

func TestGenerationTwoRejectIsolatedSidecarsWithoutSourceWrite(t *testing.T) {
	cases := []struct {
		suffix string
		want   StartupErrorKind
	}{
		{suffix: "-journal", want: StartupRollbackJournal},
		{suffix: "-wal", want: StartupIncompleteFresh},
		{suffix: "-shm", want: StartupIncompleteFresh},
	}
	for _, tc := range cases {
		t.Run(tc.suffix[1:], func(t *testing.T) {
			path := bootstrapTestPath(t, "isolated"+tc.suffix+".db")
			writeBootstrapFile(t, path+tc.suffix, []byte("orphaned sidecar"))
			before := snapshotBootstrapSources(t, path)
			_, err := Open(path, bootstrapTestVault(t))
			assertBootstrapStartupKind(t, err, tc.want)
			assertBootstrapSourcesUnchanged(t, path, before)
		})
	}
}

func TestGenerationTwoRejectRollbackJournalWithoutSourceWrite(t *testing.T) {
	path := bootstrapTestPath(t, "rollback.db")
	store := openTestStore(t, path)
	if err := store.Close(); err != nil {
		t.Fatalf("close current fixture: %v", err)
	}
	writeBootstrapFile(t, path+"-journal", []byte("rollback material"))
	before := snapshotBootstrapSources(t, path)
	_, err := Open(path, bootstrapTestVault(t))
	assertBootstrapStartupKind(t, err, StartupRollbackJournal)
	assertBootstrapSourcesUnchanged(t, path, before)
}

func TestGenerationTwoCurrentRejectsSourceRecheckMutation(t *testing.T) {
	preserveBootstrapHooks(t)
	path := bootstrapTestPath(t, "source-recheck.db")
	store := openTestStore(t, path)
	if err := store.Close(); err != nil {
		t.Fatalf("close current fixture: %v", err)
	}
	before := snapshotBootstrapSources(t, path)
	var hookCalled bool
	var hookErr error
	afterSnapshotCopyHook = func() {
		hookCalled = true
		file, err := os.OpenFile(path, os.O_WRONLY|os.O_APPEND, 0o600)
		if err != nil {
			hookErr = err
			return
		}
		if _, err := file.Write([]byte{0x5a}); err != nil {
			hookErr = err
		}
		if err := file.Close(); err != nil && hookErr == nil {
			hookErr = err
		}
	}

	_, err := Open(path, bootstrapTestVault(t))
	assertBootstrapStartupKind(t, err, StartupSourceChanged)
	if !hookCalled {
		t.Fatal("source recheck mutation hook was not called")
	}
	if hookErr != nil {
		t.Fatalf("source recheck mutation hook: %v", hookErr)
	}
	if got := snapshotBootstrapSources(t, path)[""]; !got.present || !bytes.Equal(got.data, append(append([]byte(nil), before[""].data...), 0x5a)) {
		t.Fatal("source recheck fixture was not changed only by the injected append")
	}
}

func TestGenerationTwoBootstrapRecoveryRunsAfterCheckpointBeforeStoreReturn(t *testing.T) {
	preserveBootstrapHooks(t)
	path := bootstrapTestPath(t, "recovery-order.db")
	fixture := openTestStore(t, path)
	if err := fixture.Close(); err != nil {
		t.Fatalf("close current fixture: %v", err)
	}

	var order []string
	var hookErr error
	entered := make(chan struct{})
	release := make(chan struct{})
	beforeWritableOpenHook = func() { order = append(order, "before-writable-open") }
	writableRecoveryPhaseHook = func(ctx context.Context, database *sql.DB) error {
		order = append(order, "recovery")
		if ctx == nil || database == nil {
			hookErr = errors.New("recovery seam received nil dependency")
		} else {
			var queryOnly int
			if err := database.QueryRowContext(ctx, `PRAGMA query_only`).Scan(&queryOnly); err != nil {
				hookErr = fmt.Errorf("query writable state: %w", err)
			} else if queryOnly != 0 {
				hookErr = fmt.Errorf("recovery hook saw query_only=%d", queryOnly)
			}
			var busy, logFrames, checkpointed int
			if hookErr == nil {
				if err := database.QueryRowContext(ctx, `PRAGMA wal_checkpoint(PASSIVE)`).Scan(&busy, &logFrames, &checkpointed); err != nil {
					hookErr = fmt.Errorf("observe checkpoint: %w", err)
				} else if busy != 0 || logFrames < 0 || checkpointed < 0 || checkpointed > logFrames {
					hookErr = fmt.Errorf("invalid checkpoint result (%d,%d,%d)", busy, logFrames, checkpointed)
				}
			}
		}
		close(entered)
		<-release
		return hookErr
	}

	type result struct {
		store *Store
		err   error
		vault *secret.Vault
	}
	done := make(chan result, 1)
	go func() {
		vault := bootstrapTestVaultNoCleanup()
		store, err := Open(path, vault)
		done <- result{store: store, err: err, vault: vault}
	}()
	timer := time.NewTimer(bootstrapHookWait)
	select {
	case <-entered:
		if !timer.Stop() {
			select {
			case <-timer.C:
			default:
			}
		}
	case early := <-done:
		close(release)
		if early.store != nil {
			_ = early.store.Close()
		}
		_ = early.vault.Close()
		t.Fatalf("Open returned before reaching the recovery hook: %v", early.err)
	case <-timer.C:
		close(release)
		outcome := <-done
		if outcome.store != nil {
			_ = outcome.store.Close()
		}
		_ = outcome.vault.Close()
		t.Fatalf("recovery hook was not reached within %s; Open result: %v", bootstrapHookWait, outcome.err)
	}
	select {
	case early := <-done:
		if early.store != nil {
			_ = early.store.Close()
		}
		_ = early.vault.Close()
		t.Fatal("Open returned before pre-listener recovery was released")
	default:
	}
	close(release)
	outcome := <-done
	t.Cleanup(func() {
		if outcome.store != nil {
			_ = outcome.store.Close()
		}
		_ = outcome.vault.Close()
	})
	if outcome.err != nil || outcome.store == nil {
		t.Fatalf("current recovery startup failed: %v", outcome.err)
	}
	if hookErr != nil {
		t.Fatalf("recovery hook: %v", hookErr)
	}
	if len(order) != 2 || order[0] != "before-writable-open" || order[1] != "recovery" {
		t.Fatalf("startup hook order = %v, want before-writable-open then recovery", order)
	}
}

func TestGenerationTwoCurrentReopensWithControlledWALCheckpoint(t *testing.T) {
	path := bootstrapTestPath(t, "wal-reopen.db")
	fixture := openTestStore(t, path)
	if err := fixture.Close(); err != nil {
		t.Fatalf("close current WAL fixture: %v", err)
	}

	writer, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatalf("open WAL fixture writer: %v", err)
	}
	defer func() { _ = writer.Close() }()
	writer.SetMaxOpenConns(1)
	if _, err := writer.Exec(`PRAGMA busy_timeout=5000;`); err != nil {
		t.Fatalf("configure WAL fixture writer: %v", err)
	}
	var journalMode string
	if err := writer.QueryRow(`PRAGMA journal_mode=WAL`).Scan(&journalMode); err != nil {
		t.Fatalf("enable WAL fixture: %v", err)
	}
	if journalMode != "wal" && journalMode != "WAL" {
		t.Fatalf("WAL fixture journal mode = %q", journalMode)
	}
	if _, err := writer.Exec(`INSERT INTO site_config(key,value,updated_at) VALUES('alert_prefs_wal_test','1',0)`); err != nil {
		t.Fatalf("write WAL fixture frame: %v", err)
	}
	walInfo, err := os.Stat(path + "-wal")
	if err != nil {
		t.Fatalf("WAL sidecar was not created: %v", err)
	}
	shmInfo, err := os.Stat(path + "-shm")
	if err != nil {
		t.Fatalf("SHM sidecar was not created: %v", err)
	}
	if walInfo.Size() == 0 || shmInfo.Size() == 0 {
		t.Fatalf("WAL/SHM fixture sidecars are empty: wal=%d shm=%d", walInfo.Size(), shmInfo.Size())
	}

	vault := bootstrapTestVault(t)
	reopened, err := Open(path, vault)
	if err != nil {
		t.Fatalf("reopen current database with controlled WAL checkpoint: %v", err)
	}
	t.Cleanup(func() { _ = reopened.Close() })
	var marker string
	if err := reopened.DB().QueryRow(`SELECT value FROM site_config WHERE key='alert_prefs_wal_test'`).Scan(&marker); err != nil {
		t.Fatalf("read WAL-committed config after reopen: %v", err)
	}
	if marker != "1" {
		t.Fatalf("WAL-committed config = %q, want 1", marker)
	}
	var busy, logFrames, checkpointed int
	if err := reopened.DB().QueryRow(`PRAGMA wal_checkpoint(PASSIVE)`).Scan(&busy, &logFrames, &checkpointed); err != nil {
		t.Fatalf("observe post-reopen checkpoint: %v", err)
	}
	if busy != 0 || logFrames != 0 || checkpointed != 0 {
		t.Fatalf("post-reopen WAL checkpoint = (%d,%d,%d), want (0,0,0)", busy, logFrames, checkpointed)
	}
}

func TestGenerationTwoCurrentRecoveryFailurePreservesStableClassification(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want StartupErrorKind
	}{
		{name: "credential", err: startupError(StartupCredentialReject), want: StartupCredentialReject},
		{name: "schema", err: startupError(StartupSchemaMismatch), want: StartupSchemaMismatch},
		{name: "generic", err: errors.New("recovery failed"), want: StartupInitialization},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			preserveBootstrapHooks(t)
			path := bootstrapTestPath(t, tc.name+"-recovery-failure.db")
			fixture := openTestStore(t, path)
			if err := fixture.Close(); err != nil {
				t.Fatalf("close current fixture: %v", err)
			}
			writableRecoveryPhaseHook = func(context.Context, *sql.DB) error { return tc.err }
			_, err := Open(path, bootstrapTestVault(t))
			assertBootstrapStartupKind(t, err, tc.want)
		})
	}
}

func TestGenerationTwoCurrentRejectsNonGenerationTwoSecretEnvelopes(t *testing.T) {
	cases := []struct {
		name       string
		makeCipher func(t *testing.T, vault *secret.Vault, contextID []byte) string
	}{
		{name: "legacy-v1", makeCipher: func(t *testing.T, _ *secret.Vault, _ []byte) string {
			t.Helper()
			// Canonical-shape retired envelope fixture. It is intentionally not
			// produced by the Generation 2 vault, whose API has no unbound seal.
			return "nbsec:" + "v1:aes-256-gcm:" + string(bytes.Repeat([]byte{'A'}, 16)) + ":" + string(bytes.Repeat([]byte{'A'}, 39))
		}},
		{name: "generation-two-wrong-context", makeCipher: func(t *testing.T, vault *secret.Vault, contextID []byte) string {
			t.Helper()
			otherID := bytes.Repeat([]byte{0x41}, 16)
			if bytes.Equal(otherID, contextID) {
				otherID[0]++
			}
			otherContext, err := secret.NewGenerationTwoEndpointKeyContext(otherID)
			clear(otherID)
			if err != nil {
				t.Fatalf("create wrong generation-two context: %v", err)
			}
			envelope, err := vault.SealForGenerationTwoContext([]byte("wrong-context-secret"), otherContext)
			if err != nil {
				t.Fatalf("seal wrong-context fixture: %v", err)
			}
			return envelope
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			path := bootstrapTestPath(t, tc.name+".db")
			fixture := openTestStore(t, path)
			if err := fixture.Close(); err != nil {
				t.Fatalf("close current fixture: %v", err)
			}
			sealVault := bootstrapTestVault(t)
			contextID := bytes.Repeat([]byte{0x22}, 16)
			envelope := tc.makeCipher(t, sealVault, contextID)
			insertBootstrapSecretRow(t, path, contextID, envelope, nil)
			clear(contextID)
			before := snapshotBootstrapSources(t, path)
			_, err := Open(path, bootstrapTestVault(t))
			assertBootstrapStartupKind(t, err, StartupCredentialReject)
			assertBootstrapSourcesUnchanged(t, path, before)
		})
	}
}

func TestGenerationTwoOrphanRecoveryHandles101RowsBoundaryAndRestart(t *testing.T) {
	path := bootstrapTestPath(t, "orphan-recovery.db")
	store := openTestStore(t, path)
	sealVault := bootstrapTestVault(t)
	now := time.Now().Unix()
	old := now - 2*3600
	exactBoundary := now - 3600
	recent := now - 3590

	oldIDs := insertBootstrapOrphanRows(t, store, sealVault, 100, &old, 0x100)
	exactIDs := insertBootstrapOrphanRows(t, store, sealVault, 1, &exactBoundary, 0x200)
	recentIDs := insertBootstrapOrphanRows(t, store, sealVault, 1, &recent, 0x300)
	unmarkedIDs := insertBootstrapOrphanRows(t, store, sealVault, 1, nil, 0x400)
	if len(oldIDs) != 100 || len(exactIDs) != 1 || len(recentIDs) != 1 || len(unmarkedIDs) != 1 {
		t.Fatal("orphan fixture IDs were not created")
	}
	if err := store.Close(); err != nil {
		t.Fatalf("close orphan fixture before restart: %v", err)
	}

	reopened := bootstrapTestVault(t)
	recovered, err := Open(path, reopened)
	if err != nil {
		t.Fatalf("reopen orphan fixture: %v", err)
	}
	t.Cleanup(func() { _ = recovered.Close() })
	var remaining int
	if err := recovered.DB().QueryRow(`SELECT COUNT(*) FROM endpoint_key_secrets`).Scan(&remaining); err != nil {
		t.Fatalf("count recovered endpoint secrets: %v", err)
	}
	if remaining != 2 {
		t.Fatalf("remaining detached secrets = %d, want recent boundary-adjacent + newly marked", remaining)
	}
	var exactExists, oldExists int
	if err := recovered.DB().QueryRow(`SELECT EXISTS(SELECT 1 FROM endpoint_key_secrets WHERE id=?)`, exactIDs[0]).Scan(&exactExists); err != nil {
		t.Fatalf("check exact-boundary orphan: %v", err)
	}
	if err := recovered.DB().QueryRow(`SELECT EXISTS(SELECT 1 FROM endpoint_key_secrets WHERE id=?)`, oldIDs[0]).Scan(&oldExists); err != nil {
		t.Fatalf("check old orphan: %v", err)
	}
	if exactExists != 0 || oldExists != 0 {
		t.Fatalf("orphan rows at or beyond one hour survived: exact=%d old=%d", exactExists, oldExists)
	}
	var recentExists, recentMarker int
	if err := recovered.DB().QueryRow(`SELECT EXISTS(SELECT 1 FROM endpoint_key_secrets WHERE id=?), EXISTS(SELECT 1 FROM endpoint_key_secrets WHERE id=? AND orphaned_at IS NOT NULL)`, recentIDs[0], unmarkedIDs[0]).Scan(&recentExists, &recentMarker); err != nil {
		t.Fatalf("check recent and newly marked orphans: %v", err)
	}
	if recentExists != 1 || recentMarker != 1 {
		t.Fatalf("recent orphan/marker state = (%d,%d), want (1,1)", recentExists, recentMarker)
	}

	if err := recovered.Close(); err != nil {
		t.Fatalf("close recovered orphan fixture: %v", err)
	}
	restartedVault := bootstrapTestVault(t)
	restarted, err := Open(path, restartedVault)
	if err != nil {
		t.Fatalf("second reopen orphan fixture: %v", err)
	}
	t.Cleanup(func() { _ = restarted.Close() })
	if err := restarted.DB().QueryRow(`SELECT COUNT(*) FROM endpoint_key_secrets`).Scan(&remaining); err != nil {
		t.Fatalf("count orphans after second restart: %v", err)
	}
	if remaining != 2 {
		t.Fatalf("remaining detached secrets after restart = %d, want 2", remaining)
	}
	var restartedMarker int
	if err := restarted.DB().QueryRow(`SELECT EXISTS(SELECT 1 FROM endpoint_key_secrets WHERE id=? AND orphaned_at IS NOT NULL)`, unmarkedIDs[0]).Scan(&restartedMarker); err != nil {
		t.Fatalf("check marker after restart: %v", err)
	}
	if restartedMarker != 1 {
		t.Fatal("newly marked orphan disappeared on ordinary restart")
	}
}
