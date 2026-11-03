package v1

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"

	"github.com/waiting-here/NonbiriAPI/internal/db"
)

func restoreSchema46LayoutForTest(t *testing.T, database *sql.DB) {
	t.Helper()
	restoreSchema47LayoutForTest(t, database)
	hostileMustExec(t, database, `DROP TRIGGER fishing_pond_queue_head_insert;
DROP TRIGGER fishing_pond_queue_head_delete;
DROP TABLE game_fishing_pond_queue_heads;`)
	assertRetainedManifest(t, database, db.Schema46ManifestHash)
}

func TestPondHeadSchema46UpgradePreservesQueueAndRollsBack(t *testing.T) {
	path, vault := bootstrapTestPath(t, "pond-heads.sqlite"), bootstrapTestVault(t)
	store, err := db.Open(path, vault)
	if err != nil {
		t.Fatal(err)
	}
	restoreSchema46LayoutForTest(t, store.DB())
	hostileMustExec(t, store.DB(), "UPDATE schema_state SET version=46")
	owner := hostileInsertUser(t, store.DB(), "retained-head-owner", 0, 0)
	other := hostileInsertUser(t, store.DB(), "retained-head-other", 0, 0)
	for _, entry := range []struct{ id, donor int64 }{{30, owner}, {10, owner}, {20, other}, {40, other}} {
		hostileMustExec(t, store.DB(), `INSERT INTO game_fishing_pond_queue(id,donor_user_id,source_batch_id,source_ordinal,species_key,donor_display_name,returned_at) VALUES(?,?,?,0,'boot','Retained',100)`, entry.id, entry.donor, fmt.Sprintf("fb_%021dA", entry.id))
	}
	hostileMustExec(t, store.DB(), `INSERT INTO game_fishing_pond_counts VALUES(?,'gifts',100,7)`, owner)
	hostileMustExec(t, store.DB(), `INSERT INTO game_fishing_pond_daily VALUES(?,'gifts',0,7,100)`, owner)
	hostileMustExec(t, store.DB(), `INSERT INTO game_fishing_pond_totals VALUES(?,'gifts',7,100,zeroblob(32))`, owner)
	hostileMustExec(t, store.DB(), `INSERT INTO browser_associations VALUES(?,?,1,2,3,2592001)`, owner, strings.Repeat("a", 64))
	rows, err := store.DB().Query(`EXPLAIN QUERY PLAN SELECT donor_user_id,MIN(id) FROM game_fishing_pond_queue GROUP BY donor_user_id`)
	if err != nil {
		t.Fatal(err)
	}
	var backfillPlans []string
	for rows.Next() {
		var id, parent, unused int
		var detail string
		if err := rows.Scan(&id, &parent, &unused, &detail); err != nil {
			rows.Close()
			t.Fatal(err)
		}
		backfillPlans = append(backfillPlans, detail)
	}
	if err := errors.Join(rows.Err(), rows.Close()); err != nil {
		t.Fatal(err)
	}
	backfillPlan := strings.Join(backfillPlans, " | ")
	if !strings.Contains(backfillPlan, "COVERING INDEX idx_fishing_pond_queue_donor") || strings.Contains(backfillPlan, "TEMP B-TREE") {
		t.Fatal("backfill lost ordered donor index", backfillPlan)
	}
	t.Log("backfill plan:", backfillPlan)
	before := gatewayRows(t, store.DB(), true)
	plan, err := storageSchema.plan(t.Context(), store.DB())
	if err != nil || len(plan.steps) != 2 || plan.steps[0].from != 46 || plan.steps[0].version != 47 || plan.auditsCredentials() || plan.rebuildsTables() {
		t.Fatal("head upgrade plan", plan, err)
	}
	if err = store.Close(); err != nil {
		t.Fatal(err)
	}
	checkSource := func() {
		t.Helper()
		if err := db.InspectContext(t.Context(), path, vault, func(_ context.Context, database *sql.DB) error {
			assertExactPublishedSource(t, database, 46, db.Schema46ManifestHash, before, true)
			return nil
		}); err != nil {
			t.Fatal(err)
		}
	}
	if opened, err := db.Open(path, vault); err == nil {
		opened.Close()
		t.Fatal("schema46 started without explicit upgrade")
	}
	checkSource()
	checked, err := Run(t.Context(), path, vault, Options{})
	if err != nil || checked.Status != "ready" || checked.SourceSchemaVersion != 46 || checked.TargetSchemaVersion != db.CurrentSchemaVersion {
		t.Fatal("check", checked, err)
	}
	checkSource()
	index := migrationIndex(t, 46)
	original := storageSchema.versions[index].apply
	t.Cleanup(func() { storageSchema.versions[index].apply = original })
	for _, cancelled := range []bool{false, true} {
		ctx, cancel := context.WithCancel(t.Context())
		reached := false
		storageSchema.versions[index].apply = func(ctx context.Context, tx *sql.Tx) error {
			var count int
			if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM game_fishing_pond_queue_heads h WHERE queue_id=(SELECT MIN(id) FROM game_fishing_pond_queue q WHERE q.donor_user_id=h.donor_user_id)`).Scan(&count); err != nil || count != 2 {
				return errors.New("heads not backfilled before fault")
			}
			reached = true
			if cancelled {
				cancel()
				return ctx.Err()
			}
			return errors.New("injected after head backfill")
		}
		_, err := Run(ctx, path, vault, Options{Apply: true})
		cancel()
		if err == nil || !reached {
			t.Fatal("head backfill rollback not exercised", reached, err)
		}
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
	// Compare all original tables, including queue IDs, signatures, total
	// counts, and sqlite_sequence; only the derived projection is added.
	after := gatewayRows(t, store.DB(), true)
	delete(after, "game_fishing_pond_queue_heads")
	if !reflect.DeepEqual(before, after) {
		t.Fatal("head upgrade changed retained facts")
	}
	for donor, want := range map[int64]int64{owner: 10, other: 20} {
		var head int64
		if err = store.DB().QueryRow(`SELECT queue_id FROM game_fishing_pond_queue_heads WHERE donor_user_id=?`, donor).Scan(&head); err != nil || head != want {
			t.Fatalf("backfilled head=%d want=%d err=%v", head, want, err)
		}
	}
	assertRetainedManifest(t, store.DB(), db.PinnedGenerationTwoManifestHash)
	hostileMustExec(t, store.DB(), `DELETE FROM game_fishing_pond_queue WHERE id=10`)
	var next int64
	if err = store.DB().QueryRow(`SELECT queue_id FROM game_fishing_pond_queue_heads WHERE donor_user_id=?`, owner).Scan(&next); err != nil || next != 30 {
		t.Fatal("upgraded trigger did not advance head", next, err)
	}
}
