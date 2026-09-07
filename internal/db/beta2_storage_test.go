package db

import (
	"context"
	"database/sql"
	"testing"
)

// dropBetaTwoAdditiveObjects removes all beta.2 additive tables so the
// database manifest returns to the pre-beta.2 (complete beta.1) digest.
// foreign_keys is toggled off because donation_quota_rules and
// donation_quota_epochs form a circular FK that otherwise blocks DROP.
func dropBetaTwoAdditiveObjects(t *testing.T, database *sql.DB) {
	t.Helper()
	if _, err := database.Exec(`PRAGMA foreign_keys=OFF;
DROP TABLE IF EXISTS donation_quota_receipts;
DROP TABLE IF EXISTS donation_quota_periods;
DROP TABLE IF EXISTS donation_quota_buckets;
DROP TABLE IF EXISTS donation_quota_epochs;
DROP TABLE IF EXISTS donation_quota_rules;
DROP TABLE IF EXISTS donation_handling;
DROP TABLE IF EXISTS charity_model_access;
DROP TABLE IF EXISTS donation_quota_capacity;
DROP TABLE IF EXISTS game_rps_presentation;
DROP TABLE IF EXISTS game_rps_pending_presentation;
DROP TABLE IF EXISTS game_rps_summary_presentation;
PRAGMA foreign_keys=ON;`); err != nil {
		t.Fatalf("drop beta.2 additive objects: %v", err)
	}
}

// TestBetaTwoExtensionSeedsSidecarDefaultsFromBeta1 verifies that opening a
// complete beta.1 database (pre-beta.2 manifest) applies the beta.2 additive
// schema and seeds the required default sidecar rows: every existing donation
// gets a legacy handling row, every charity model gets a full-access mask with
// an empty description, and the quota capacity singleton is initialized. A
// second startup must not duplicate or alter those rows.
func TestBetaTwoExtensionSeedsSidecarDefaultsFromBeta1(t *testing.T) {
	path := bootstrapTestPath(t, "beta2-upgrade-sidecar.sqlite")
	vault := bootstrapTestVault(t)
	store, err := Open(path, vault)
	if err != nil {
		t.Fatalf("fresh open: %v", err)
	}
	if _, err := store.DB().Exec(`INSERT INTO donations(status,revision,description,review_note,reviewed_by_role,created_at,updated_at) VALUES('approved',1,'','','',1000,1000)`); err != nil {
		t.Fatalf("seed donation: %v", err)
	}
	if _, err := store.DB().Exec(`INSERT INTO charity_models(provider,model,full_name,enabled,pricing_mode,created_at,updated_at) VALUES('p','m','[公益]p/m',0,'per_request',1000,1000)`); err != nil {
		t.Fatalf("seed charity model: %v", err)
	}
	dropBetaTwoAdditiveObjects(t, store.DB())
	manifest, err := readGenerationManifest(context.Background(), store.DB())
	if err != nil || generationManifestDigest(manifest) != preBetaTwoManifestHash {
		t.Fatalf("fixture does not match pre-beta.2 manifest: %v", err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	store, err = Open(path, vault)
	if err != nil {
		t.Fatalf("upgrade open: %v", err)
	}
	defer store.Close()
	var handlingState string
	if err := store.DB().QueryRow(`SELECT state FROM donation_handling`).Scan(&handlingState); err != nil {
		t.Fatalf("read donation_handling: %v", err)
	}
	if handlingState != "legacy" {
		t.Fatalf("donation_handling state = %q, want legacy", handlingState)
	}
	var mask int
	var desc string
	if err := store.DB().QueryRow(`SELECT allowed_level_mask, public_description FROM charity_model_access`).Scan(&mask, &desc); err != nil {
		t.Fatalf("read charity_model_access: %v", err)
	}
	if mask != 31 || desc != "" {
		t.Fatalf("charity_model_access = (%d,%q), want (31,'')", mask, desc)
	}
	var capID, rowsUsed, rowsHeld int
	if err := store.DB().QueryRow(`SELECT id, rows_used, rows_held FROM donation_quota_capacity`).Scan(&capID, &rowsUsed, &rowsHeld); err != nil {
		t.Fatalf("read capacity: %v", err)
	}
	if capID != 1 || rowsUsed != 0 || rowsHeld != 0 {
		t.Fatalf("capacity = (%d,%d,%d), want (1,0,0)", capID, rowsUsed, rowsHeld)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	store, err = Open(path, vault)
	if err != nil {
		t.Fatalf("second open: %v", err)
	}
	defer store.Close()
	var handlingCount, accessCount int
	if err := store.DB().QueryRow(`SELECT COUNT(*) FROM donation_handling`).Scan(&handlingCount); err != nil {
		t.Fatal(err)
	}
	if err := store.DB().QueryRow(`SELECT COUNT(*) FROM charity_model_access`).Scan(&accessCount); err != nil {
		t.Fatal(err)
	}
	if handlingCount != 1 || accessCount != 1 {
		t.Fatalf("after second open: handling=%d access=%d, want 1/1", handlingCount, accessCount)
	}
}

// TestBetaTwoFreshSeedsCapacitySingleton verifies the fresh bootstrap seeds the
// quota capacity singleton so the runtime never reads a missing capacity row.
func TestBetaTwoFreshSeedsCapacitySingleton(t *testing.T) {
	path := bootstrapTestPath(t, "beta2-fresh-capacity.sqlite")
	vault := bootstrapTestVault(t)
	store, err := Open(path, vault)
	if err != nil {
		t.Fatalf("fresh open: %v", err)
	}
	defer store.Close()
	var capID, rowsUsed, rowsHeld int
	if err := store.DB().QueryRow(`SELECT id, rows_used, rows_held FROM donation_quota_capacity`).Scan(&capID, &rowsUsed, &rowsHeld); err != nil {
		t.Fatalf("read capacity: %v", err)
	}
	if capID != 1 || rowsUsed != 0 || rowsHeld != 0 {
		t.Fatalf("fresh capacity = (%d,%d,%d), want (1,0,0)", capID, rowsUsed, rowsHeld)
	}
}

// TestBetaTwoSidecarHostileConstraints verifies the new beta.2 sidecar tables
// reject hostile inserts: dangling FK targets, invalid enum states, illegal
// state/nullable combinations, the capacity singleton bound and the capacity
// row-sum ceiling.
func TestBetaTwoSidecarHostileConstraints(t *testing.T) {
	path := bootstrapTestPath(t, "beta2-hostile-sidecar.sqlite")
	vault := bootstrapTestVault(t)
	store, err := Open(path, vault)
	if err != nil {
		t.Fatalf("fresh open: %v", err)
	}
	defer store.Close()
	db := store.DB()
	if _, err := db.Exec(`INSERT INTO donations(status,revision,description,review_note,reviewed_by_role,created_at,updated_at) VALUES('approved',1,'','','',1000,1000)`); err != nil {
		t.Fatalf("seed donation: %v", err)
	}
	var donationID int64
	if err := db.QueryRow(`SELECT id FROM donations`).Scan(&donationID); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO charity_models(provider,model,full_name,enabled,pricing_mode,created_at,updated_at) VALUES('p','m','[公益]p/m',0,'per_request',1000,1000)`); err != nil {
		t.Fatalf("seed charity model: %v", err)
	}
	var modelID int64
	if err := db.QueryRow(`SELECT id FROM charity_models`).Scan(&modelID); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO donation_handling(donation_id, state, revision, created_at, updated_at) VALUES(999999, 'legacy', 1, 1000, 1000)`); err == nil {
		t.Fatal("donation_handling accepted non-existent donation_id")
	}
	if _, err := db.Exec(`INSERT INTO charity_model_access(model_id, allowed_level_mask, public_description) VALUES(999999, 31, '')`); err == nil {
		t.Fatal("charity_model_access accepted non-existent model_id")
	}
	if _, err := db.Exec(`INSERT INTO donation_handling(donation_id, state, revision, created_at, updated_at) VALUES(?, 'bogus', 1, 1000, 1000)`, donationID); err == nil {
		t.Fatal("donation_handling accepted bogus state")
	}
	if _, err := db.Exec(`INSERT INTO donation_handling(donation_id, state, revision, processed_at, processed_by_user_id, processed_by_role, closed_at, closed_reason, created_at, updated_at) VALUES(?, 'processed', 1, NULL, NULL, '', NULL, '', 1000, 1000)`, donationID); err == nil {
		t.Fatal("donation_handling accepted processed state without processed_at/role")
	}
	if _, err := db.Exec(`INSERT INTO charity_model_access(model_id, allowed_level_mask, public_description) VALUES(?, 32, '')`, modelID); err == nil {
		t.Fatal("charity_model_access accepted mask=32")
	}
	if _, err := db.Exec(`INSERT INTO donation_quota_capacity(id, rows_used, rows_held) VALUES(2, 0, 0)`); err == nil {
		t.Fatal("capacity accepted id=2")
	}
	if _, err := db.Exec(`UPDATE donation_quota_capacity SET rows_used=5000000`); err != nil {
		t.Fatalf("capacity rejected rows_used=5000000: %v", err)
	}
	if _, err := db.Exec(`UPDATE donation_quota_capacity SET rows_held=1`); err == nil {
		t.Fatal("capacity accepted rows_used+rows_held > 5000000")
	}
}
