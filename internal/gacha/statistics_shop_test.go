package gacha

import (
	"bytes"
	"encoding/csv"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/waiting-here/NonbiriAPI/internal/authz"
	"github.com/waiting-here/NonbiriAPI/internal/db"
	"github.com/waiting-here/NonbiriAPI/internal/ledger"
	"github.com/waiting-here/NonbiriAPI/internal/limitedactivities"
	"github.com/waiting-here/NonbiriAPI/internal/pagination"
	"github.com/waiting-here/NonbiriAPI/internal/rewardasset"
)

func TestShopStatisticsRealRedemptionReplayRollbackAndLifecycle(t *testing.T) {
	f := newFixture(t)
	f.enter(t)
	if _, err := f.act(f.other, "enter-other", Action{Action: "enter"}); err != nil {
		t.Fatal(err)
	}
	f.exec(t, "UPDATE gacha_profiles SET n_shards=1000,r_shards=1000,sr_shards=1000,ssr_shards=1000")
	redeem := func(user int64, key, card string) error {
		_, err := f.act(user, key, Action{Action: "redeem", Product: card, Revision: 1})
		return err
	}
	assert := func(total, people string, cards map[string]string) {
		t.Helper()
		out, err := f.s.AdminShopCardStatistics(t.Context(), f.admin, statisticsQueryDefault())
		if err != nil || out.Summary != (ShopStatistics{total, people}) {
			t.Fatal(out, err)
		}
		found := map[string]string{}
		for _, item := range out.TopCards {
			if item.Count != item.ParticipantCount || item.Count != item.FirstAcquisitionCount {
				t.Fatal(item)
			}
			found[item.CardID] = item.Count
		}
		if len(found) != len(cards) {
			t.Fatal(found, cards)
		}
		for id, count := range cards {
			if found[id] != count {
				t.Fatal(found, cards)
			}
		}
	}
	f.recorder.fail.Store(true)
	if err := redeem(f.user, "first", "g-chatgpt"); err == nil {
		t.Fatal("first redemption failure committed")
	}
	f.recorder.fail.Store(false)
	assert("0", "0", map[string]string{})
	if f.scalar(t, "SELECT count(*) FROM gacha_shop_participants") != 0 {
		t.Fatal("failed first redemption retained participant")
	}
	for _, v := range []struct {
		user      int64
		key, card string
	}{{f.user, "first", "g-chatgpt"}, {f.other, "same-card", "g-chatgpt"}, {f.user, "second-card", "g-claude"}, {f.user, "first", "g-chatgpt"}} {
		if err := redeem(v.user, v.key, v.card); err != nil {
			t.Fatal(err)
		}
	}
	counts := map[string]string{"g-chatgpt": "2", "g-claude": "1"}
	assert("3", "2", counts)
	if err := redeem(f.user, "already-owned", "g-chatgpt"); !errors.Is(err, ErrOwned) {
		t.Fatal(err)
	}
	var shopCard string
	for _, card := range catalog.Cards {
		if card.Source == "shop" {
			shopCard = card.ID
			break
		}
	}
	if shopCard == "" {
		t.Fatal("no shop card")
	}
	before := bulkState(t, f)
	f.recorder.fail.Store(true)
	if err := redeem(f.user, "rollback", shopCard); err == nil {
		t.Fatal("late failure committed")
	}
	f.recorder.fail.Store(false)
	after := bulkState(t, f)
	if before.Fragments[N] != after.Fragments[N] || before.Fragments[R] != after.Fragments[R] || f.scalar(t, "SELECT count(*) FROM gacha_owned WHERE user_id=? AND card_id=?", f.user, shopCard) != 0 || f.scalar(t, "SELECT count(*) FROM gacha_claims WHERE user_id=? AND target=?", f.user, shopCard) != 0 {
		t.Fatal("rollback changed card or shards")
	}
	assert("3", "2", counts)
	if err := redeem(f.user, "rollback", shopCard); err != nil {
		t.Fatal(err)
	}
	counts[shopCard] = "1"
	f.pool(t, "Limited shop card", DefaultRules(), "s-chatgpt")
	if err := redeem(f.user, "limited-card", "s-chatgpt"); err != nil {
		t.Fatal(err)
	}
	counts["s-chatgpt"] = "1"
	assert("5", "2", counts)
	// Ticket purchases, shard exchanges, daily bundles, draws and reward grants
	// use their existing paths and never add card-redemption facts.
	f.fund(t, ledger.Game, 50_000_000)
	for _, action := range []Action{{Action: "buy", Ticket: "permanent", Quantity: 1, Revision: 1}, {Action: "redeem", Product: "ticket-n", Revision: 1}, {Action: "redeem", Product: "ticket-r", Revision: 1}, {Action: "redeem", Product: "ticket-sr", Revision: 1}, {Action: "redeem", Product: "ticket-ssr", Revision: 1}, {Action: "redeem", Product: "bulk-permanent", Revision: 1}, {Action: "redeem", Product: "bulk-limited", Revision: 1}, {Action: "draw", PoolID: "permanent", Quantity: 1}} {
		if _, err := f.act(f.user, action.Action+action.Product, action); err != nil {
			t.Fatal(action, err)
		}
	}
	tx, err := f.db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	if _, err = GrantRewardTx(t.Context(), tx, f.user, f.at.Load(), rewardasset.Item{Asset: "gacha:card:g-glm", Quantity: "1"}); err != nil {
		tx.Rollback()
		t.Fatal(err)
	}
	if err = tx.Commit(); err != nil {
		t.Fatal(err)
	}
	if err = redeem(f.user, "reward-owned", "g-glm"); !errors.Is(err, ErrOwned) {
		t.Fatal(err)
	}
	assert("5", "2", counts)
	f.at.Add(181 * 86400)
	if _, err = f.s.Retain(t.Context(), f.at.Load(), 100, time.Now().Add(2*time.Second)); err != nil {
		t.Fatal(err)
	}
	if err = redeem(f.user, "first", "g-chatgpt"); err != nil {
		t.Fatal(err)
	}
	assert("5", "2", counts)
	tx, err = f.db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	if err = f.s.DeleteUserTx(t.Context(), tx, f.user); err != nil {
		tx.Rollback()
		t.Fatal(err)
	}
	if _, err = tx.Exec("DELETE FROM users WHERE id=?", f.user); err != nil {
		tx.Rollback()
		t.Fatal(err)
	}
	if err = tx.Commit(); err != nil {
		t.Fatal(err)
	}
	if f.scalar(t, "SELECT count(*) FROM gacha_shop_participants WHERE user_id=?", f.user) != 0 {
		t.Fatal("deleted membership survived")
	}
	assert("5", "2", counts)
	if err = redeem(f.other, "post-retention", "g-claude"); err != nil {
		t.Fatal(err)
	}
	counts["g-claude"] = "2"
	assert("6", "2", counts)
}

func TestShopStatisticsQueryExportsExactCountsAndAuthority(t *testing.T) {
	f := newFixture(t)
	wide, err := db.ParseU128Decimal("18446744073709551617")
	if err != nil {
		t.Fatal(err)
	}
	f.exec(t, "INSERT INTO gacha_shop_card_statistics(card_id,redemption_count) VALUES(?,?)", "removed-card", db.EncodeU128(wide))
	f.exec(t, "UPDATE gacha_shop_statistics SET total_redemptions=?,participant_count=?", db.EncodeU128(wide), db.EncodeU128(wide))
	page, err := f.s.AdminShopCardStatistics(t.Context(), f.admin, StatisticsQuery{Page: pagination.Request{Page: 1, Size: 10}, Search: "no-match"})
	if err != nil || len(page.Items) != 0 || len(page.TopCards) != 1 || page.TopCards[0].Count != wide.Decimal() || page.TopCards[0].Rarity != nil || page.Summary.TotalRedemptions != wide.Decimal() {
		t.Fatal(page, err)
	}
	var jsonBody, csvBody bytes.Buffer
	if err = f.s.ExportShopStatistics(t.Context(), f.admin, "json", &jsonBody); err != nil {
		t.Fatal(err)
	}
	var exported struct {
		Summary  ShopStatistics     `json:"summary"`
		Cards    []CardStatistics   `json:"cards"`
		Rarities []RarityStatistics `json:"rarity_totals"`
	}
	if err = json.Unmarshal(jsonBody.Bytes(), &exported); err != nil || len(exported.Cards) <= 10 || exported.Cards[0].Count != wide.Decimal() {
		t.Fatal(exported, err)
	}
	for _, card := range exported.Cards {
		if c, ok := cardsByID[card.CardID]; ok && c.Source == "permanent" && c.Rarity != SSR {
			t.Fatal("unredeemable zero row", card)
		}
	}
	if bytes.Contains(jsonBody.Bytes(), []byte("user_id")) {
		t.Fatal("identities exposed")
	}
	if err = f.s.ExportShopStatistics(t.Context(), f.admin, "csv", &csvBody); err != nil {
		t.Fatal(err)
	}
	records, err := csv.NewReader(&csvBody).ReadAll()
	if err != nil || len(records) != len(exported.Cards)+1 || len(records[0]) != 9 || records[0][0] != "total_redemptions" || records[0][1] != "shop_participant_count" || records[1][0] != wide.Decimal() || records[1][6] != wide.Decimal() {
		t.Fatal(records, err)
	}
	for _, actor := range []int64{0, f.user, f.other} {
		if _, err = f.s.AdminShopCardStatistics(t.Context(), actor, statisticsQueryDefault()); !errors.Is(err, authz.ErrForbidden) {
			t.Fatal(err)
		}
		var denied bytes.Buffer
		if err = f.s.ExportShopStatistics(t.Context(), actor, "json", &denied); !errors.Is(err, authz.ErrForbidden) || denied.Len() != 0 {
			t.Fatal(err)
		}
	}
	routes := statisticsRoutes{}
	if err = registerStatisticsRoutes(routes, f.s); err != nil {
		t.Fatal(err)
	}
	for _, v := range []struct {
		path, query string
		status      int
	}{{statisticsShopCardsRoute, "", 200}, {statisticsShopCardsRoute, "?sort=rarity&page=2&page_size=10", 200}, {statisticsShopCardsRoute, "?sort=name&q=chatgpt", 200}, {statisticsShopCardsRoute, "?sort=draws", 400}, {statisticsShopCardsRoute, "?page=0", 400}, {statisticsShopCardsRoute, "?q=a&q=b", 400}, {statisticsShopExportRoute, "?format=json", 200}, {statisticsShopExportRoute, "?format=csv", 200}, {statisticsShopExportRoute, "?format=json&page=1", 400}} {
		r := httptest.NewRequest(http.MethodGet, v.path+v.query, nil)
		w := httptest.NewRecorder()
		routes[http.MethodGet+" "+v.path](w, r, limitedactivities.AdminPrincipal{UserID: f.admin})
		if w.Code != v.status {
			t.Fatal(v, w.Code, w.Body.String())
		}
		if v.path == statisticsShopExportRoute && v.status == 200 && !strings.Contains(w.Header().Get("Content-Disposition"), "shop-statistics") {
			t.Fatal(w.Header())
		}
	}
	for _, path := range []string{statisticsShopCardsRoute, statisticsShopExportRoute} {
		r := httptest.NewRequest(http.MethodGet, path, nil)
		if path == statisticsShopExportRoute {
			r.URL.RawQuery = "format=json"
		}
		w := httptest.NewRecorder()
		routes[http.MethodGet+" "+path](w, r, limitedactivities.AdminPrincipal{UserID: f.user})
		if w.Code != 403 {
			t.Fatal(w.Code, w.Body.String())
		}
	}
}
