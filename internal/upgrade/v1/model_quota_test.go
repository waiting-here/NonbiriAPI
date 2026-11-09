package v1

import (
	"context"
	"database/sql"
	"errors"
	"testing"

	"github.com/waiting-here/NonbiriAPI/internal/db"
)

func restoreSchema49LayoutForTest(t *testing.T, database *sql.DB) {
	t.Helper()
	restoreSchema50LayoutForTest(t, database)
	hostileMustExec(t, database, `DROP TABLE donation_model_quotas;
DROP INDEX idx_lake_exchange_daily_source;
DROP INDEX idx_donation_usage_model_reserved;
ALTER TABLE donation_usage_reservations DROP COLUMN upstream_model_id;
DROP INDEX idx_donation_quota_rules_scope;
DROP INDEX idx_donation_quota_rules_key_order;
ALTER TABLE donation_quota_rules DROP COLUMN upstream_model_id;
CREATE UNIQUE INDEX idx_donation_quota_rules_key_order ON donation_quota_rules(donation_key_id, display_order) WHERE current_epoch IS NOT NULL;`)
	assertRetainedManifest(t, database, db.Schema49ManifestHash)
}

func TestModelQuotaUpgradeIsExplicitAndAtomic(t *testing.T) {
	path, vault := bootstrapTestPath(t, "model-quota.sqlite"), bootstrapTestVault(t)
	store, err := db.Open(path, vault)
	if err != nil {
		t.Fatal(err)
	}
	owner := hostileInsertUser(t, store.DB(), "quota-upgrade", 0, 1)
	restoreSchema49LayoutForTest(t, store.DB())
	hostileMustExec(t, store.DB(), "UPDATE schema_state SET version=49")
	if err = store.Close(); err != nil {
		t.Fatal(err)
	}
	if opened, err := db.Open(path, vault); err == nil {
		opened.Close()
		t.Fatal("old schema started without explicit upgrade")
	}
	checked, err := Run(t.Context(), path, vault, Options{})
	if err != nil || checked.Status != "ready" || checked.SourceSchemaVersion != 49 {
		t.Fatal(checked, err)
	}
	assertSource := func() {
		t.Helper()
		err := db.InspectContext(t.Context(), path, vault, func(ctx context.Context, database *sql.DB) error {
			assertRetainedManifest(t, database, db.Schema49ManifestHash)
			var version int
			return database.QueryRowContext(ctx, "SELECT version FROM schema_state WHERE id=1 AND version=49").Scan(&version)
		})
		if err != nil {
			t.Fatal(err)
		}
	}
	assertSource()
	index := migrationIndex(t, 49)
	original := storageSchema.versions[index].apply
	t.Cleanup(func() { storageSchema.versions[index].apply = original })
	storageSchema.versions[index].apply = func(ctx context.Context, tx *sql.Tx) error {
		var count int
		if err := tx.QueryRowContext(ctx, "SELECT count(*) FROM donation_model_quotas").Scan(&count); err != nil {
			return err
		}
		return errors.New("injected after quota DDL")
	}
	if _, err = Run(t.Context(), path, vault, Options{Apply: true}); err == nil {
		t.Fatal("injected upgrade unexpectedly committed")
	}
	storageSchema.versions[index].apply = original
	assertSource()
	result, err := Run(t.Context(), path, vault, Options{Apply: true})
	if err != nil || result.Status != "converted" || result.TargetSchemaVersion != db.CurrentSchemaVersion {
		t.Fatal(result, err)
	}
	store, err = db.Open(path, vault)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	var kept int
	if err = store.DB().QueryRow("SELECT count(*) FROM users WHERE id=?", owner).Scan(&kept); err != nil || kept != 1 {
		t.Fatal("retained user", kept, err)
	}
	if err = store.DB().QueryRow("SELECT count(*) FROM donation_model_quotas").Scan(&kept); err != nil || kept != 0 {
		t.Fatal("legacy usage was attributed to a model", kept, err)
	}
	assertRetainedManifest(t, store.DB(), db.PinnedGenerationTwoManifestHash)
}
