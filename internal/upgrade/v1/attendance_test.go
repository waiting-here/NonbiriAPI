package v1

import (
	"context"
	"database/sql"
	"errors"
	"testing"

	"github.com/waiting-here/NonbiriAPI/internal/db"
)

func restoreSchema48LayoutForTest(t *testing.T, database *sql.DB) {
	t.Helper()
	hostileMustExec(t, database, "DROP TABLE attendance_awards; DROP TABLE attendance_sign_ins; DROP TABLE attendance_progress; DROP TABLE attendance_tiers; DROP TABLE attendance_periods;")
	assertRetainedManifest(t, database, db.Schema48ManifestHash)
}

func TestAttendanceSchema48UpgradeIsExplicitAndAtomic(t *testing.T) {
	path, vault := bootstrapTestPath(t, "attendance.sqlite"), bootstrapTestVault(t)
	store, err := db.Open(path, vault)
	if err != nil {
		t.Fatal(err)
	}
	owner := hostileInsertUser(t, store.DB(), "attendance-upgrade", 0, 1)
	restoreSchema48LayoutForTest(t, store.DB())
	hostileMustExec(t, store.DB(), "UPDATE schema_state SET version=48")
	if err = store.Close(); err != nil {
		t.Fatal(err)
	}
	if opened, err := db.Open(path, vault); err == nil {
		opened.Close()
		t.Fatal("old schema started without explicit upgrade")
	}
	checked, err := Run(t.Context(), path, vault, Options{})
	if err != nil || checked.Status != "ready" || checked.SourceSchemaVersion != 48 {
		t.Fatal(checked, err)
	}
	assertSource := func() {
		t.Helper()
		err := db.InspectContext(t.Context(), path, vault, func(ctx context.Context, database *sql.DB) error {
			assertRetainedManifest(t, database, db.Schema48ManifestHash)
			var version int
			return database.QueryRowContext(ctx, "SELECT version FROM schema_state WHERE id=1 AND version=48").Scan(&version)
		})
		if err != nil {
			t.Fatal(err)
		}
	}
	assertSource()
	index := migrationIndex(t, 48)
	original := storageSchema.versions[index].apply
	t.Cleanup(func() { storageSchema.versions[index].apply = original })
	storageSchema.versions[index].apply = func(ctx context.Context, tx *sql.Tx) error {
		var count int
		if err := tx.QueryRowContext(ctx, "SELECT count(*) FROM attendance_periods").Scan(&count); err != nil {
			return err
		}
		return errors.New("injected after attendance DDL")
	}
	if _, err = Run(t.Context(), path, vault, Options{Apply: true}); err == nil {
		t.Fatal("injected upgrade unexpectedly committed")
	}
	storageSchema.versions[index].apply = original
	assertSource()
	result, err := Run(t.Context(), path, vault, Options{Apply: true})
	if err != nil || result.Status != "converted" || result.TargetSchemaVersion != 49 {
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
	assertRetainedManifest(t, store.DB(), db.PinnedGenerationTwoManifestHash)
}
