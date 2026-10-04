package db

import (
	"testing"
)

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
	if _, err := db.Exec(`INSERT INTO charity_model_access(model_id, allowed_level_mask, public_description) VALUES(?, 64, '')`, modelID); err == nil {
		t.Fatal("charity_model_access accepted mask=64")
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
