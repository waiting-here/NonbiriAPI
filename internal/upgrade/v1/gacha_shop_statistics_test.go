package v1

import (
	"context"
	"database/sql"
	"errors"
	"reflect"
	"testing"

	"github.com/waiting-here/NonbiriAPI/internal/db"
)

func TestShopStatisticsSchema39EvidenceUnionAtomicAndOnce(t *testing.T) {
	path, vault := bootstrapTestPath(t, "shop-statistics.sqlite"), bootstrapTestVault(t)
	store, err := db.Open(path, vault)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { store.Close() })
	user := hostileInsertUser(t, store.DB(), "shop-first", 0, 0)
	other := hostileInsertUser(t, store.DB(), "shop-second", 0, 0)
	unused := hostileInsertUser(t, store.DB(), "shop-unused", 0, 0)
	owned := func(user int64, card, source string) {
		t.Helper()
		hostileMustExec(t, store.DB(), `INSERT INTO gacha_owned(user_id,card_id,obtained_at,source) VALUES(?,?,1,?)`, user, card, source)
	}
	claim := func(user int64, card string) {
		t.Helper()
		hostileMustExec(t, store.DB(), `INSERT INTO gacha_claims(user_id,kind,target,created_at) VALUES(?,'card',?,1)`, user, card)
	}
	// Overlap is one redemption; each user/card pair remains independent.
	owned(user, "g-chatgpt", "shop")
	claim(user, "g-chatgpt")
	owned(other, "g-chatgpt", "shop")
	claim(other, "g-chatgpt")
	owned(user, "g-claude", "shop")
	claim(user, "g-claude")
	// Removed cards have explicit ownership evidence; known card-only claims
	// also retain their identity without requiring a current ownership row.
	owned(user, "removed-card", "shop")
	claim(user, "removed-card")
	claim(other, "s-chatgpt")
	for _, card := range []string{"unknown-claim", "bulk-permanent", "bulk-limited", "ticket-n", "ticket-r", "ticket-sr", "ticket-ssr"} {
		claim(unused, card)
	}
	owned(unused, "g-glm", "reward")
	owned(unused, "g-gemini", "draw")
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
		return errors.New("after shop statistics backfill")
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
	check(`SELECT total_redemptions FROM gacha_shop_statistics`, "5")
	check(`SELECT participant_count FROM gacha_shop_statistics`, "2")
	for _, v := range []struct{ card, count string }{{"g-chatgpt", "2"}, {"g-claude", "1"}, {"removed-card", "1"}, {"s-chatgpt", "1"}} {
		check(`SELECT redemption_count FROM gacha_shop_card_statistics WHERE card_id=?`, v.count, v.card)
	}
	var n int
	if err = store.DB().QueryRow(`SELECT count(*) FROM gacha_shop_card_statistics`).Scan(&n); err != nil || n != 4 {
		t.Fatal(n, err)
	}
	if err = store.DB().QueryRow(`SELECT count(*) FROM gacha_shop_participants WHERE user_id=?`, unused).Scan(&n); err != nil || n != 0 {
		t.Fatal("non-card purchaser counted", n, err)
	}
	after := gatewayRows(t, store.DB())
	for _, table := range []string{"gacha_owned", "gacha_claims", "gacha_operations", "users"} {
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
	check(`SELECT total_redemptions FROM gacha_shop_statistics`, "5")
	hostileMustExec(t, store.DB(), `DELETE FROM users WHERE id=?`, user)
	check(`SELECT participant_count FROM gacha_shop_statistics`, "2")
	check(`SELECT redemption_count FROM gacha_shop_card_statistics WHERE card_id='g-chatgpt'`, "2")
	if err = store.DB().QueryRow(`SELECT count(*) FROM gacha_shop_participants WHERE user_id=?`, user).Scan(&n); err != nil || n != 0 {
		t.Fatal("deleted identity remains", n, err)
	}
	if err = foreignKeyCheck(t.Context(), store.DB()); err != nil {
		t.Fatal(err)
	}
}
