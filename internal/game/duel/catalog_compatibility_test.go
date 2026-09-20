package duel_test

import (
	"testing"
	"time"

	"github.com/waiting-here/NonbiriAPI/internal/game/duel"
	"github.com/waiting-here/NonbiriAPI/internal/game/likes"
	"github.com/waiting-here/NonbiriAPI/internal/game/likes/catalog"
)

func TestSavedLegacyCatalogSupportsRecoveryHistoryAndArchival(t *testing.T) {
	current, err := likes.NewRules()
	if err != nil {
		t.Fatal(err)
	}
	snapshot, err := catalog.PublicLegacy("quick")
	if err != nil {
		t.Fatal(err)
	}
	legacy, err := current.ResolveCatalog("quick", snapshot.ContentHash)
	if err != nil {
		t.Fatal(err)
	}
	f := newFixture(t, "likes", legacy)
	state := f.matched()
	f.action(0, state, basicPlan)
	f.action(1, state, basicPlan)
	f.s.Close()
	f.rules, f.options.Rules = current, current
	f.s, err = duel.New(f.options)
	if err != nil {
		t.Fatal(err)
	}
	if err := f.s.ValidatePersistedState(f.ctx); err != nil {
		t.Fatal("old snapshot rejected before recovery", err)
	}
	if _, err := f.s.RecoverBeforeListenAt(f.ctx, 100, 100, time.Now().Add(2*time.Second)); err != nil {
		t.Fatal(err)
	}
	result := f.read(0).LatestResult
	if result == nil || result.Reason != "server_restart" || result.OwnRefund.Game != "3" {
		t.Fatal("legacy cancellation/refund failed", result)
	}
	detail, err := f.s.HistoryDetail(f.ctx, f.identity(0), state.ID)
	if err != nil || detail.ContentHash != snapshot.ContentHash {
		t.Fatal("legacy history lost identity", err)
	}
	page, err := f.s.Rounds(f.ctx, f.identity(0), state.ID, duel.PageInput{}, false)
	if err != nil || len(page.Items) != 1 {
		t.Fatal("legacy round unreadable", err)
	}
	f.ledger()
	// A supported hash does not authorize arbitrary saved catalog contents.
	if _, err := f.db.Exec(`UPDATE game_duel_catalogs SET catalog_json='{}' WHERE content_hash=?`, snapshot.ContentHash); err == nil {
		t.Fatal("catalog was mutable")
	}
	// Simulate a damaged file beyond the normal write API in this isolated DB.
	if _, err := f.db.Exec(`DROP TRIGGER game_duel_catalog_immutable`); err != nil {
		t.Fatal(err)
	}
	if _, err := f.db.Exec(`UPDATE game_duel_catalogs SET catalog_json='{}' WHERE content_hash=?`, snapshot.ContentHash); err != nil {
		t.Fatal(err)
	}
	if err := f.s.ValidatePersistedState(f.ctx); err == nil {
		t.Fatal("catalog corruption accepted")
	}
	encoded, err := legacy.Catalog("quick")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.db.Exec(`UPDATE game_duel_catalogs SET catalog_json=? WHERE content_hash=?`, string(encoded.JSON), snapshot.ContentHash); err != nil {
		t.Fatal(err)
	}
	if _, err := f.db.Exec(`CREATE TRIGGER game_duel_catalog_immutable BEFORE UPDATE ON game_duel_catalogs BEGIN SELECT RAISE(ABORT,'duel catalog immutable'); END`); err != nil {
		t.Fatal(err)
	}
	f.clock.Add(duel.RetentionSeconds)
	if _, err := f.s.Retain(f.ctx, f.clock.Load(), 100, time.Now().Add(2*time.Second)); err != nil {
		t.Fatal(err)
	}
	if err := f.s.ValidatePersistedState(f.ctx); err != nil {
		t.Fatal("legacy anonymous history rejected", err)
	}
	for _, test := range [][2]string{{"quick", "unknown"}, {"standard", snapshot.ContentHash}} {
		if _, err := current.ResolveCatalog(test[0], test[1]); err == nil {
			t.Fatal("unsupported rule identity accepted", test)
		}
	}
}
