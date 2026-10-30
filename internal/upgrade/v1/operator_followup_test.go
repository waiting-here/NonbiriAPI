package v1

import (
	"context"
	"database/sql"
	"errors"
	"reflect"
	"testing"

	"github.com/waiting-here/NonbiriAPI/internal/db"
)

func restoreSchema42LayoutForTest(t *testing.T, database *sql.DB) {
	t.Helper()
	hostileMustExec(t, database, `DROP TABLE agent_operation_diagnostics;
DROP TRIGGER management_capture_donation_service_health_insert;
DROP TRIGGER management_capture_donation_service_health_update;
DROP TRIGGER management_capture_donation_service_health_delete;
DROP INDEX idx_charity_outcomes_success;
DROP INDEX idx_fishing_rank_active_user;`)
	assertRetainedManifest(t, database, db.Schema42ManifestHash)
}

func TestOperatorFollowupSchema42UpgradeIsAdditiveAndAtomic(t *testing.T) {
	path, vault := bootstrapTestPath(t, "operator-followup.sqlite"), bootstrapTestVault(t)
	store, err := db.Open(path, vault)
	if err != nil {
		t.Fatal(err)
	}
	restoreSchema42LayoutForTest(t, store.DB())
	hostileMustExec(t, store.DB(), "UPDATE schema_state SET version=42")
	hostileInsertUser(t, store.DB(), "retained-followup", 0, 1)
	hostileMustExec(t, store.DB(), `INSERT INTO agent_driver_profiles(id,revision,name,settings_json,updated_at) VALUES(7,4,'Preserved Codex profile','{"provider":"codex","model":"retained"}',12)`)
	before := gatewayRows(t, store.DB())
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	checkSource := func() {
		t.Helper()
		if err := db.InspectContext(t.Context(), path, vault, func(_ context.Context, database *sql.DB) error {
			assertExactPublishedSource(t, database, 42, db.Schema42ManifestHash, before)
			return nil
		}); err != nil {
			t.Fatal(err)
		}
	}
	if opened, err := db.Open(path, vault); err == nil {
		opened.Close()
		t.Fatal("schema42 started without explicit upgrade")
	}
	checked, err := Run(t.Context(), path, vault, Options{})
	if err != nil || checked.Status != "ready" || checked.SourceSchemaVersion != 42 || checked.TargetSchemaVersion != 43 {
		t.Fatal("check", checked, err)
	}
	checkSource()
	index := migrationIndex(t, 42)
	original := storageSchema.versions[index].apply
	t.Cleanup(func() { storageSchema.versions[index].apply = original })
	for _, cancelAtEnd := range []bool{false, true} {
		ctx, cancel := context.WithCancel(t.Context())
		reachedDDL := false
		storageSchema.versions[index].apply = func(ctx context.Context, tx *sql.Tx) error {
			var exists int
			if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM sqlite_schema WHERE name='agent_operation_diagnostics'`).Scan(&exists); err != nil || exists != 1 {
				return errors.New("additive DDL was not applied")
			}
			reachedDDL = true
			if cancelAtEnd {
				cancel()
				return ctx.Err()
			}
			return errors.New("injected after additive DDL")
		}
		_, err := Run(ctx, path, vault, Options{Apply: true})
		cancel()
		storageSchema.versions[index].apply = original
		if err == nil || !reachedDDL {
			t.Fatalf("failure did not exercise rollback after additive DDL: reached=%v err=%v", reachedDDL, err)
		}
		checkSource()
	}
	result, err := Run(t.Context(), path, vault, Options{Apply: true})
	if err != nil || result.Status != "converted" || result.SourceSchemaVersion != 42 || result.TargetSchemaVersion != 43 {
		t.Fatal("apply", result, err)
	}
	store, err = db.Open(path, vault)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	after := gatewayRows(t, store.DB())
	for table, rows := range before {
		if !reflect.DeepEqual(after[table], rows) {
			t.Errorf("preserved source rows changed: %s", table)
		}
	}
	if countRows(t, store, "SELECT COUNT(*) FROM agent_operation_diagnostics") != 0 {
		t.Fatal("upgrade invented administrator diagnostics")
	}
}
