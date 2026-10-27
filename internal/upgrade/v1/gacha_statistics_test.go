package v1

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"reflect"
	"testing"

	"github.com/waiting-here/NonbiriAPI/internal/db"
)

func TestGachaStatisticsSchema39BackfillIsAtomicAndOnce(t *testing.T) {
	path, vault := bootstrapTestPath(t, "pool-statistics.sqlite"), bootstrapTestVault(t)
	store, err := db.Open(path, vault)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { store.Close() })
	user := hostileInsertUser(t, store.DB(), "pool-first", 0, 0)
	other := hostileInsertUser(t, store.DB(), "pool-other", 0, 0)
	live, err := db.GenerateOpaqueID("gp_")
	if err != nil {
		t.Fatal(err)
	}
	detached, err := db.GenerateOpaqueID("gp_")
	if err != nil {
		t.Fatal(err)
	}
	liveConfig := `{"id":"` + live + `","kind":"limited","name":"Current period","featured":["i-chatgpt"],"starts_at":10,"ends_at":50}`
	detachedConfig := `{"id":"` + detached + `","kind":"limited","name":"Original period","featured":["legacy-card"],"starts_at":1,"ends_at":9}`
	hostileMustExec(t, store.DB(), `INSERT INTO gacha_pools(id,config,revision,created_at) VALUES(?,?,1,0)`, live, liveConfig)
	hostileMustExec(t, store.DB(), `INSERT INTO gacha_config_events(target,revision,config,created_at) VALUES(?,1,?,0)`, detached, detachedConfig)
	seed := func(owner int64, pool, name, items string) {
		t.Helper()
		operation, err := db.GenerateOpaqueID("op_")
		if err != nil {
			t.Fatal(err)
		}
		hostileMustExec(t, store.DB(), `INSERT INTO gacha_operations(id,user_id,action,delta,balance_after,created_at) VALUES(?,?,'draw','[0,0,0,0,0,0]','[0,0,0,0,0,0]',5)`, operation, owner)
		receipt := `{"id":"` + operation + `","pool_id":"` + pool + `","pool_name":"` + name + `","created_at":5,"items":` + items + `}`
		hostileMustExec(t, store.DB(), `INSERT INTO gacha_draws(operation_id,user_id,receipt,rules,roster,catalog_version,created_at) VALUES(?,?,?,'{}','["a-chatgpt","c-chatgpt","legacy-card"]',1,5)`, operation, owner, receipt)
	}
	seed(user, "permanent", "Original permanent", `[{"card_id":"a-chatgpt","duplicate":false},{"card_id":"a-chatgpt","duplicate":true},{"card_id":"c-chatgpt","duplicate":false}]`)
	seed(other, "permanent", "Original permanent", `[{"card_id":"a-chatgpt","duplicate":false}]`)
	seed(user, "permanent", "Original permanent", `[{"card_id":"c-chatgpt","duplicate":true}]`)
	seed(user, detached, "Old name", `[{"card_id":"a-chatgpt","duplicate":true},{"card_id":"legacy-card","duplicate":false}]`)
	seed(user, live, "Current period", `[{"card_id":"a-chatgpt","duplicate":true}]`)
	hostileMustExec(t, store.DB(), `INSERT INTO gacha_owned(user_id,card_id,obtained_at,source) VALUES(?,'g-chatgpt',1,'reward')`, other)
	restoreSchema39LayoutForTest(t, store.DB())
	hostileMustExec(t, store.DB(), `UPDATE schema_state SET version=39`)
	before := gatewayRows(t, store.DB())
	assertRetainedManifest(t, store.DB(), db.Schema39ManifestHash)
	store.Close()
	index := migrationIndex(t, 39)
	original := storageSchema.versions[index].apply
	storageSchema.versions[index].apply = func(ctx context.Context, tx *sql.Tx) error {
		if err := original(ctx, tx); err != nil {
			return err
		}
		return errors.New("after statistics backfill")
	}
	_, err = Run(t.Context(), path, vault, Options{Apply: true})
	storageSchema.versions[index].apply = original
	if err == nil {
		t.Fatal("injected failure committed")
	}
	if err = db.InspectContext(t.Context(), path, vault, func(ctx context.Context, read *sql.DB) error {
		assertExactPublishedSource(t, read, 39, db.Schema39ManifestHash, before)
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	explicitUpgrade(t, path, vault)
	store, err = db.Open(path, vault)
	if err != nil {
		t.Fatal(err)
	}
	check := func(query, want string, args ...any) {
		t.Helper()
		var raw []byte
		if err := store.DB().QueryRow(query, args...).Scan(&raw); err != nil {
			t.Fatal(err)
		}
		count, err := db.DecodeU128(raw)
		if err != nil || count.Decimal() != want {
			t.Fatalf("%s: %s != %s: %v", query, count.Decimal(), want, err)
		}
	}
	check(`SELECT total_draws FROM gacha_pool_statistics WHERE pool_id='permanent'`, "5")
	check(`SELECT participant_count FROM gacha_pool_statistics WHERE pool_id='permanent'`, "2")
	for _, test := range []struct{ card, copies, participants, first string }{{"a-chatgpt", "3", "2", "2"}, {"c-chatgpt", "2", "1", "1"}} {
		check(`SELECT draw_count FROM gacha_card_statistics WHERE pool_id='permanent' AND card_id=?`, test.copies, test.card)
		check(`SELECT participant_count FROM gacha_card_statistics WHERE pool_id='permanent' AND card_id=?`, test.participants, test.card)
		check(`SELECT first_acquisition_count FROM gacha_card_statistics WHERE pool_id='permanent' AND card_id=?`, test.first, test.card)
	}
	check(`SELECT total_draws FROM gacha_pool_statistics WHERE pool_id=?`, "2", detached)
	check(`SELECT total_draws FROM gacha_pool_statistics WHERE pool_id=?`, "1", live)
	var config string
	if err = store.DB().QueryRow(`SELECT config FROM gacha_pool_statistics WHERE pool_id=?`, detached).Scan(&config); err != nil {
		t.Fatal(err)
	}
	var metadata map[string]any
	if err = json.Unmarshal([]byte(config), &metadata); err != nil || metadata["name"] != "Original period" || metadata["starts_at"] != float64(1) {
		t.Fatal(metadata, err)
	}
	after := gatewayRows(t, store.DB())
	for _, table := range []string{"gacha_draws", "gacha_operations", "gacha_pools", "gacha_owned", "gacha_config_events"} {
		if !reflect.DeepEqual(before[table], after[table]) {
			t.Fatal("source facts changed", table)
		}
	}
	store.Close()
	result, err := Run(t.Context(), path, vault, Options{Apply: true})
	if err != nil || result.Status != "current" {
		t.Fatal(result, err)
	}
	store, err = db.Open(path, vault)
	if err != nil {
		t.Fatal(err)
	}
	check(`SELECT total_draws FROM gacha_pool_statistics WHERE pool_id='permanent'`, "5")
	hostileMustExec(t, store.DB(), `DELETE FROM users WHERE id=?`, user)
	check(`SELECT participant_count FROM gacha_pool_statistics WHERE pool_id='permanent'`, "2")
	if err = foreignKeyCheck(t.Context(), store.DB()); err != nil {
		t.Fatal(err)
	}
}
