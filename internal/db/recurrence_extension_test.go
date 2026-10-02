package db

import (
	"context"
	"database/sql"
	"errors"
	"testing"
)

func recurrenceSourceFixture(t *testing.T) *sql.DB {
	t.Helper()
	database, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	database.SetMaxOpenConns(1)
	t.Cleanup(func() { database.Close() })
	hostileMustExec(t, database, "PRAGMA foreign_keys=ON;"+preRecurrenceSchema())
	assertRetainedManifest(t, database, preRecurrenceManifestHash)
	tx, err := database.Begin()
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	if err := seedGenerationTwo(context.Background(), tx, hostileOID("b1e_")); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	return database
}

func TestRecurrenceUpgradeRetainsEpochAndCanonicalIdentity(t *testing.T) {
	database := recurrenceSourceFixture(t)
	user := hostileInsertUser(t, database, "quota-donor", 0, 0)
	donation := hostileInsertDonation(t, database, user)
	endpoint := hostileInsertEndpoint(t, database, user, "https://fixture.example/v1")
	secret := hostileInsertSecret(t, database, "https://fixture.example/v1", 0)
	physicalKey := hostileInsertEndpointKey(t, database, endpoint, secret)
	key := hostileInsertDonationKey(t, database, donation, physicalKey)
	rule := hostileOID("qlr_")
	hostileMustExec(t, database, `INSERT INTO donation_quota_rules(id,donation_key_id) VALUES(?,?)`, rule, key)
	hostileMustExec(t, database, `INSERT INTO donation_quota_epochs(rule_id,epoch,mode,interval,alignment,time_zone,metric,limit_mag,effective_at,last_observed_at,pending_reserved) VALUES(?,1,'reset','month','calendar','UTC','calls',nbi_u128(17),1700000000,1700000000,nbi_u128(0))`, rule)
	hostileMustExec(t, database, `UPDATE donation_quota_rules SET current_epoch=1,display_order=0 WHERE id=?`, rule)
	for range 2 {
		if err := extendKnownGenerationTwoSchema(context.Background(), database); err != nil {
			t.Fatal(err)
		}
		assertForeignKeyEnforcement(t, database)
		if err := validateGenerationTwoManifest(context.Background(), database); err != nil {
			t.Fatal(err)
		}
	}
	var anchor sql.NullString
	var count int
	if err := database.QueryRow("SELECT anchor_local FROM donation_quota_epochs WHERE rule_id=? AND epoch=1", rule).Scan(&anchor); err != nil || anchor.Valid {
		t.Fatalf("anchor=%+v %v", anchor, err)
	}
	if err := database.QueryRow("SELECT count(*) FROM gateway_model_capabilities_state").Scan(&count); err != nil || count != 0 {
		t.Fatalf("import state=%d %v", count, err)
	}
	hostileMustExec(t, database, `UPDATE donation_quota_epochs SET alignment='exact_time',anchor_local='2026-10-27T03:00:01' WHERE rule_id=?`, rule)
	if _, err := database.Exec(`UPDATE donation_quota_epochs SET anchor_local=NULL WHERE rule_id=?`, rule); err == nil {
		t.Fatal("exact reset without anchor accepted")
	}
	if _, err := database.Exec(`UPDATE donation_quota_epochs SET mode='sliding',alignment=NULL WHERE rule_id=?`, rule); err == nil {
		t.Fatal("sliding rule with anchor accepted")
	}
}

func TestRecurrenceUpgradeRollsBackAndRestoresEnforcement(t *testing.T) {
	database := recurrenceSourceFixture(t)
	injected := errors.New("injected rollback")
	err := runGenerationTwoExtension(context.Background(), database, func(ctx context.Context, tx *sql.Tx) error {
		if err := applyRecurrenceExtension(ctx, tx); err != nil {
			return err
		}
		return injected
	})
	if !errors.Is(err, injected) {
		t.Fatal(err)
	}
	assertForeignKeyEnforcement(t, database)
	assertRetainedManifest(t, database, preRecurrenceManifestHash)
	if err := extendKnownGenerationTwoSchema(context.Background(), database); err != nil {
		t.Fatal(err)
	}
}
