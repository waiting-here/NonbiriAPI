package db

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"testing"
)

// Reconstruct the exact released CHECKs; the independent pinned manifest in
// each caller proves this fixture represents a supported deployed structure.
func makePreHourlyQuotaFixture(t *testing.T, database *sql.DB) {
	t.Helper()
	var text string
	var version int
	if err := database.QueryRow(`SELECT sql FROM sqlite_schema WHERE name='donation_quota_epochs'`).Scan(&text); err != nil {
		t.Fatal(err)
	}
	for _, pair := range [][2]string{
		{"interval IN ('1h','5h','day','week','month')", "interval IN ('5h','day','week','month')"},
		{"interval IN ('1h','5h')", "interval='5h'"},
	} {
		if strings.Count(text, pair[0]) != 1 {
			t.Fatal("current quota fixture is missing its expected CHECK")
		}
		text = strings.Replace(text, pair[0], pair[1], 1)
	}
	if err := database.QueryRow(`PRAGMA schema_version`).Scan(&version); err != nil {
		t.Fatal(err)
	}
	hostileMustExec(t, database, `BEGIN; PRAGMA writable_schema=ON`)
	hostileMustExec(t, database, `UPDATE sqlite_schema SET sql=? WHERE type='table' AND name='donation_quota_epochs'`, text)
	hostileMustExec(t, database, fmt.Sprintf(`PRAGMA schema_version=%d; PRAGMA writable_schema=RESET; COMMIT`, version+1))
}

func TestHourlyQuotaExtensionPreservesStorageAndEnforcesChecks(t *testing.T) {
	database, rule := quotaConstraintFixture(t)
	makeRetainedSource(t, database, preHourlyQuotaManifestHash)
	before := retainedTableImages(t, database, nil)
	var rootBefore, rootAfter int
	if err := database.QueryRow(`SELECT rootpage FROM sqlite_schema WHERE name='donation_quota_epochs'`).Scan(&rootBefore); err != nil {
		t.Fatal(err)
	}
	hostileMustFail(t, database, `UPDATE donation_quota_epochs SET interval='1h' WHERE rule_id=?`, rule)
	for round := 0; round < 2; round++ {
		if err := extendKnownGenerationTwoSchema(context.Background(), database); err != nil {
			t.Fatal(err)
		}
		assertRetainedManifest(t, database, PinnedGenerationTwoManifestHash)
		assertRetainedImages(t, database, before)
		assertHourlySchemaPragmas(t, database)
	}
	if err := database.QueryRow(`SELECT rootpage FROM sqlite_schema WHERE name='donation_quota_epochs'`).Scan(&rootAfter); err != nil || rootAfter != rootBefore {
		t.Fatalf("quota storage was rebuilt: %d -> %d: %v", rootBefore, rootAfter, err)
	}
	hostileMustExec(t, database, `UPDATE donation_quota_epochs SET interval='1h' WHERE rule_id=?`, rule)
	hostileMustFail(t, database, `UPDATE donation_quota_epochs SET alignment='calendar' WHERE rule_id=?`, rule)
	hostileMustFail(t, database, `UPDATE donation_quota_epochs SET interval='2h' WHERE rule_id=?`, rule)
	hostileMustExec(t, database, `UPDATE donation_quota_epochs SET mode='sliding',alignment=NULL WHERE rule_id=?`, rule)
	hostileMustFail(t, database, `UPDATE donation_quota_epochs SET week_starts_on=1 WHERE rule_id=?`, rule)
	var integrity string
	if err := database.QueryRow(`PRAGMA integrity_check`).Scan(&integrity); err != nil || integrity != "ok" {
		t.Fatal(integrity, err)
	}
}

func TestHourlyQuotaExtensionRollsBackAfterValidationFailure(t *testing.T) {
	database, _ := quotaConstraintFixture(t)
	makeRetainedSource(t, database, preHourlyQuotaManifestHash)
	// Inject a data-only FK failure so the complete source manifest remains
	// accepted and the failure occurs after the schema update, before commit.
	hostileMustExec(t, database, `PRAGMA foreign_keys=OFF; INSERT INTO endpoint_key_limits(endpoint_key_id,max_rpm) VALUES(999999,1); PRAGMA foreign_keys=ON`)
	before := retainedTableImages(t, database, nil)
	if err := extendKnownGenerationTwoSchema(context.Background(), database); err == nil {
		t.Fatal("extension accepted dangling business data")
	}
	assertRetainedManifest(t, database, preHourlyQuotaManifestHash)
	assertRetainedImages(t, database, before)
	assertHourlySchemaPragmas(t, database)
	hostileMustFail(t, database, `UPDATE donation_quota_epochs SET interval='1h'`)
	hostileMustExec(t, database, `DELETE FROM endpoint_key_limits WHERE endpoint_key_id=999999`)
	if err := extendKnownGenerationTwoSchema(context.Background(), database); err != nil {
		t.Fatal(err)
	}
	assertRetainedManifest(t, database, PinnedGenerationTwoManifestHash)
	assertHourlySchemaPragmas(t, database)
}

func assertHourlySchemaPragmas(t *testing.T, database *sql.DB) {
	t.Helper()
	var writable, foreignKeys int
	if err := database.QueryRow(`PRAGMA writable_schema`).Scan(&writable); err != nil {
		t.Fatal(err)
	}
	if err := database.QueryRow(`PRAGMA foreign_keys`).Scan(&foreignKeys); err != nil {
		t.Fatal(err)
	}
	if writable != 0 || foreignKeys != 1 {
		t.Fatalf("connection protections changed: writable_schema=%d foreign_keys=%d", writable, foreignKeys)
	}
}
