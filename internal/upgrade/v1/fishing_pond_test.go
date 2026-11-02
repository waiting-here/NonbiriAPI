package v1

import (
	"context"
	"database/sql"
	"errors"
	"reflect"
	"testing"

	"github.com/waiting-here/NonbiriAPI/internal/db"
)

func isFishingPondTableForTest(table string) bool {
	switch table {
	case "game_fishing_pond_outcomes", "game_fishing_pond_queue", "game_fishing_pond_queue_state", "game_fishing_pond_counts", "game_fishing_pond_daily", "game_fishing_pond_totals":
		return true
	}
	return false
}

func assertInitialFishingPondTableForTest(t *testing.T, database *sql.DB, table string) {
	t.Helper()
	query := "SELECT COUNT(*) FROM " + quoteSQLiteIdentifier(table)
	want := 0
	if table == "game_fishing_pond_queue_state" {
		query += " WHERE id=1 AND item_count=0"
		want = 1
	}
	var count int
	if err := database.QueryRow(query).Scan(&count); err != nil || count != want {
		t.Fatalf("pond table %s was backfilled: %d %v", table, count, err)
	}
}

func assertFishingRecentOrderProjectionForTest(t *testing.T, database *sql.DB) {
	t.Helper()
	expected := `SELECT l.batch_id_text,f.user_id,f.expires_at,COALESCE(l.blue_fat_fish_length_cm,l.milk_crab_length_cm,CAST(l.size_cm AS TEXT)),l.caught_at,l.ordinal
FROM game_fishing_length_facts l JOIN game_fishing_rank_facts f ON f.batch_id_text=l.batch_id_text WHERE f.aggregate_applied=1`
	actual := `SELECT batch_id_text,user_id,expires_at,length_cm,caught_at,ordinal FROM game_fishing_recent_order`
	for _, query := range []string{expected + " EXCEPT " + actual, actual + " EXCEPT " + expected} {
		var mismatch bool
		if err := database.QueryRow("SELECT EXISTS(" + query + ")").Scan(&mismatch); err != nil || mismatch {
			t.Fatal("recent order does not exactly project original active length facts", mismatch, err)
		}
	}
}

func restoreSchema44LayoutForTest(t *testing.T, database *sql.DB) {
	t.Helper()
	restoreSchema45LayoutForTest(t, database)
	hostileMustExec(t, database, `DROP INDEX idx_lake_notes_live_lease;
DROP TRIGGER fishing_length_fact_recent_order;
DROP TABLE game_fishing_recent_order;
DROP TABLE game_fishing_pond_outcomes;
DROP TABLE game_fishing_pond_queue;
DROP TABLE game_fishing_pond_queue_state;
DROP TABLE game_fishing_pond_counts;
DROP TABLE game_fishing_pond_daily;
DROP TABLE game_fishing_pond_totals;`)
	assertRetainedManifest(t, database, db.Schema44ManifestHash)
}

func TestFishingPondSchema44UpgradeIsAdditiveAndAtomic(t *testing.T) {
	path, vault := bootstrapTestPath(t, "pond.sqlite"), bootstrapTestVault(t)
	store, err := db.Open(path, vault)
	if err != nil {
		t.Fatal(err)
	}
	restoreSchema44LayoutForTest(t, store.DB())
	hostileMustExec(t, store.DB(), "UPDATE schema_state SET version=44")
	owner := hostileInsertUser(t, store.DB(), "retained-pond-owner", 0, 0)
	batch := hostileOIDVariant("fb_", 'p', 'Q')
	hostileInsertFishingBatch(t, store.DB(), batch, owner, hostileOIDVariant("op_", 'p', 'Q'))
	hostileMustExec(t, store.DB(), `INSERT INTO game_fishing_outcomes(batch_id,ordinal,species_key,tier,size_cm,payout_milli) VALUES(?,0,'boot','junk',0,0)`, batch)
	hostileMustExec(t, store.DB(), `UPDATE game_fishing_batches SET state='committed',payout_total_milli=0,settled_at=100,ledger_rows_remaining=zeroblob(16),next_attempt_at=NULL WHERE id=?`, batch)
	hostileMustExec(t, store.DB(), `INSERT INTO game_fishing_rank_facts VALUES(?,?,100,2592100,zeroblob(16),1)`, batch, owner)
	hostileMustExec(t, store.DB(), `INSERT INTO game_fishing_length_facts VALUES(?,0,'boot','junk',0,0,NULL,NULL)`, batch)
	plan, err := storageSchema.plan(t.Context(), store.DB())
	if err != nil || len(plan.steps) != 2 || plan.steps[0].from != 44 || plan.steps[0].version != 45 || plan.auditsCredentials() {
		t.Fatal("additive upgrade plan", plan, err)
	}
	before := gatewayRows(t, store.DB())
	if err = store.Close(); err != nil {
		t.Fatal(err)
	}
	checkSource := func() {
		t.Helper()
		if err := db.InspectContext(t.Context(), path, vault, func(_ context.Context, database *sql.DB) error {
			assertExactPublishedSource(t, database, 44, db.Schema44ManifestHash, before)
			return nil
		}); err != nil {
			t.Fatal(err)
		}
	}
	if opened, err := db.Open(path, vault); err == nil {
		opened.Close()
		t.Fatal("schema44 started without explicit upgrade")
	}
	checkSource()
	checked, err := Run(t.Context(), path, vault, Options{})
	if err != nil || checked.Status != "ready" || checked.SourceSchemaVersion != 44 || checked.TargetSchemaVersion != db.CurrentSchemaVersion {
		t.Fatal("check", checked, err)
	}
	checkSource()
	index := migrationIndex(t, 44)
	original := storageSchema.versions[index].apply
	t.Cleanup(func() { storageSchema.versions[index].apply = original })
	for _, cancelAtEnd := range []bool{false, true} {
		ctx, cancel := context.WithCancel(t.Context())
		reachedDDL := false
		storageSchema.versions[index].apply = func(ctx context.Context, tx *sql.Tx) error {
			var count int
			if err := tx.QueryRowContext(ctx, "SELECT COUNT(*) FROM game_fishing_pond_queue").Scan(&count); err != nil || count != 0 {
				return errors.New("pond tables were not added empty")
			}
			reachedDDL = true
			if cancelAtEnd {
				cancel()
				return ctx.Err()
			}
			return errors.New("injected after pond DDL")
		}
		_, err := Run(ctx, path, vault, Options{Apply: true})
		cancel()
		storageSchema.versions[index].apply = original
		if err == nil || !reachedDDL {
			t.Fatalf("rollback not exercised: reached=%v err=%v", reachedDDL, err)
		}
		checkSource()
	}
	result, err := Run(t.Context(), path, vault, Options{Apply: true})
	if err != nil || result.Status != "converted" || result.SourceSchemaVersion != 44 || result.TargetSchemaVersion != db.CurrentSchemaVersion {
		t.Fatal("apply", result, err)
	}
	store, err = db.Open(path, vault)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	if after := gatewayRows(t, store.DB()); !reflect.DeepEqual(before, after) {
		t.Fatal("pond upgrade changed retained records")
	}
	for _, table := range []string{"game_fishing_pond_outcomes", "game_fishing_pond_queue", "game_fishing_pond_counts", "game_fishing_pond_daily", "game_fishing_pond_totals"} {
		var count int
		if err := store.DB().QueryRow("SELECT COUNT(*) FROM " + table).Scan(&count); err != nil || count != 0 {
			t.Fatal("historical catches were backfilled", table, count, err)
		}
	}
	assertRetainedManifest(t, store.DB(), db.PinnedGenerationTwoManifestHash)
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	again, err := Run(t.Context(), path, vault, Options{Apply: true})
	if err != nil || again.Status != "current" {
		t.Fatal("repeated upgrade", again, err)
	}
}
