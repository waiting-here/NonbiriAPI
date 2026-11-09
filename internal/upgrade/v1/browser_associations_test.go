package v1

import (
	"context"
	"database/sql"
	"errors"
	"reflect"
	"testing"

	"github.com/waiting-here/NonbiriAPI/internal/db"
)

func restoreSchema45LayoutForTest(t *testing.T, database *sql.DB) {
	t.Helper()
	restoreSchema46LayoutForTest(t, database)
	hostileMustExec(t, database, "DROP TABLE browser_associations")
	assertRetainedManifest(t, database, db.Schema45ManifestHash)
}

func TestBrowserAssociationSchema45UpgradeIsExplicitAndAtomic(t *testing.T) {
	path, vault := bootstrapTestPath(t, "browser.sqlite"), bootstrapTestVault(t)
	store, err := db.Open(path, vault)
	if err != nil {
		t.Fatal(err)
	}
	restoreSchema45LayoutForTest(t, store.DB())
	hostileMustExec(t, store.DB(), "UPDATE schema_state SET version=45")
	hostileInsertUser(t, store.DB(), "retained-browser-user", 0, 0)
	before := gatewayRows(t, store.DB())
	plan, err := storageSchema.plan(t.Context(), store.DB())
	if err != nil || len(plan.steps) != 5 || plan.steps[0].from != 45 || plan.steps[0].version != 46 || plan.steps[1].version != 47 || plan.auditsCredentials() {
		t.Fatal("plan", plan, err)
	}
	if err = store.Close(); err != nil {
		t.Fatal(err)
	}
	checkSource := func() {
		t.Helper()
		if err := db.InspectContext(t.Context(), path, vault, func(_ context.Context, database *sql.DB) error {
			assertExactPublishedSource(t, database, 45, db.Schema45ManifestHash, before)
			return nil
		}); err != nil {
			t.Fatal(err)
		}
	}
	if opened, err := db.Open(path, vault); err == nil {
		opened.Close()
		t.Fatal("old schema implicitly upgraded")
	}
	checkSource()
	checked, err := Run(t.Context(), path, vault, Options{})
	if err != nil || checked.Status != "ready" || checked.SourceSchemaVersion != 45 || checked.TargetSchemaVersion != db.CurrentSchemaVersion {
		t.Fatal("check", checked, err)
	}
	checkSource()
	index := migrationIndex(t, 45)
	original := storageSchema.versions[index].apply
	t.Cleanup(func() { storageSchema.versions[index].apply = original })
	for _, cancelled := range []bool{false, true} {
		ctx, cancel := context.WithCancel(t.Context())
		reached := false
		storageSchema.versions[index].apply = func(ctx context.Context, tx *sql.Tx) error {
			var n int
			if err := tx.QueryRowContext(ctx, "SELECT COUNT(*) FROM browser_associations").Scan(&n); err != nil || n != 0 {
				return errors.New("browser associations not initialized empty")
			}
			reached = true
			if cancelled {
				cancel()
				return ctx.Err()
			}
			return errors.New("injected after DDL")
		}
		if _, err := Run(ctx, path, vault, Options{Apply: true}); err == nil || !reached {
			t.Fatal("failed upgrade did not roll back", err)
		}
		cancel()
		checkSource()
	}
	storageSchema.versions[index].apply = original
	result, err := Run(t.Context(), path, vault, Options{Apply: true})
	if err != nil || result.Status != "converted" || result.TargetSchemaVersion != db.CurrentSchemaVersion {
		t.Fatal("upgrade", result, err)
	}
	store, err = db.Open(path, vault)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	after := gatewayRows(t, store.DB())
	delete(after, "browser_associations")
	if !reflect.DeepEqual(before, after) {
		t.Fatal("upgrade changed existing facts")
	}
	var count int
	if err = store.DB().QueryRow("SELECT COUNT(*) FROM browser_associations").Scan(&count); err != nil || count != 0 {
		t.Fatal("historical users were backfilled", count, err)
	}
	assertRetainedManifest(t, store.DB(), db.PinnedGenerationTwoManifestHash)
}
