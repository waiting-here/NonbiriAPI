package v1

import (
	"context"
	"database/sql"
	"errors"
	"testing"

	"github.com/waiting-here/NonbiriAPI/internal/db"
)

func restoreSchema52LayoutForTest(t *testing.T, database *sql.DB) {
	t.Helper()
	hostileMustExec(t, database, `DROP TRIGGER credit_operation_assets_insert;
DROP TRIGGER credit_operation_assets_delete;
DROP TABLE credit_operation_assets;
DROP INDEX idx_request_logs_user_started;
CREATE INDEX idx_request_logs_user_started ON request_logs(user_id,started_at,id);
DROP INDEX idx_image_activity_tasks_user;
CREATE INDEX idx_image_activity_tasks_user ON image_activity_tasks(user_id,created_at,id);
DROP INDEX idx_image_activity_tasks_unfinished;
CREATE INDEX idx_image_activity_tasks_unfinished ON image_activity_tasks(user_id,finance_state,accepted_seq);`)
	assertRetainedManifest(t, database, db.Schema52ManifestHash)
}

func TestCreditOperationAssetUpgradeIsExplicitAndAtomic(t *testing.T) {
	path, vault := bootstrapTestPath(t, "asset-index.sqlite"), bootstrapTestVault(t)
	store, err := db.Open(path, vault)
	if err != nil {
		t.Fatal(err)
	}
	id := hostileOID("op_")
	hostileMustExec(t, store.DB(), `INSERT INTO credit_operations(id,ledger_seq,kind,source_type,source_id,source_seq,donation_credit_delta_sign,donation_credit_delta_mag,created_at)
VALUES(?,1,'admin_user_adjustment','operation',?,zeroblob(16),0,zeroblob(16),0)`, id, id)
	for line, asset := range []string{"general", "general", "game"} {
		hostileMustExec(t, store.DB(), `INSERT INTO credit_entries(operation_id,line_no,account_kind_snapshot,asset_type,delta_sign,delta_mag)
VALUES(?,?,'user',?,1,?)`, id, line, asset, hostileBlob16(1))
	}
	restoreSchema52LayoutForTest(t, store.DB())
	hostileMustExec(t, store.DB(), "UPDATE schema_state SET version=52")
	store.Close()
	if opened, err := db.Open(path, vault); err == nil {
		opened.Close()
		t.Fatal("old schema opened without explicit upgrade")
	}
	if result, err := Run(t.Context(), path, vault, Options{}); err != nil || result.Status != "ready" || result.SourceSchemaVersion != 52 {
		t.Fatal(result, err)
	}
	index := migrationIndex(t, 52)
	original := storageSchema.versions[index].apply
	t.Cleanup(func() { storageSchema.versions[index].apply = original })
	storageSchema.versions[index].apply = func(context.Context, *sql.Tx) error { return errors.New("injected after asset backfill") }
	if _, err := Run(t.Context(), path, vault, Options{Apply: true}); err == nil {
		t.Fatal("failed backfill committed")
	}
	storageSchema.versions[index].apply = original
	if err := db.InspectContext(t.Context(), path, vault, func(ctx context.Context, database *sql.DB) error {
		assertRetainedManifest(t, database, db.Schema52ManifestHash)
		var count int
		if err := database.QueryRowContext(ctx, "SELECT count(*) FROM credit_entries").Scan(&count); err != nil || count != 3 {
			t.Fatal(count, err)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if result, err := Run(t.Context(), path, vault, Options{Apply: true}); err != nil || result.Status != "converted" {
		t.Fatal(result, err)
	}
	store, err = db.Open(path, vault)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	var count int
	if err := store.DB().QueryRow(`SELECT count(*) FROM credit_operation_assets WHERE operation_id=? AND ledger_seq=1 AND created_at=0 AND kind='admin_user_adjustment'`, id).Scan(&count); err != nil || count != 2 {
		t.Fatal(count, err)
	}
}
