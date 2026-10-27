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
	"github.com/waiting-here/NonbiriAPI/internal/limitedactivities"
	"github.com/waiting-here/NonbiriAPI/internal/pagination"
	"github.com/waiting-here/NonbiriAPI/internal/rewardasset"
)

func statisticsQueryDefault() StatisticsQuery { return StatisticsQuery{Page: pagination.Default()} }

func TestStatisticsDrawParticipantsReplayRollbackAndLifecycle(t *testing.T) {
	f := newFixture(t)
	f.enter(t)
	if _, err := f.act(f.other, "enter-other", Action{Action: "enter"}); err != nil {
		t.Fatal(err)
	}
	f.s.random = func(int) (int, error) { return 0, nil }
	draw := func(user int64, key, pool string) Outcome {
		t.Helper()
		out, err := f.act(user, key, Action{Action: "draw", PoolID: pool, Quantity: 1})
		if err != nil {
			t.Fatal(err)
		}
		return out
	}
	first := draw(f.user, "one", "permanent")
	card := first.Draw.Items[0].CardID
	draw(f.user, "two", "permanent")
	draw(f.user, "two", "permanent")
	tx, err := f.db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	if _, err = GrantRewardTx(t.Context(), tx, f.other, f.at.Load(), rewardasset.Item{Asset: "gacha:card:" + card, Quantity: "1"}); err != nil {
		tx.Rollback()
		t.Fatal(err)
	}
	if err = tx.Commit(); err != nil {
		t.Fatal(err)
	}
	draw(f.other, "reward-owned", "permanent")
	assert := func(pool, total, participants, copies, cardPeople, firsts string) {
		t.Helper()
		out, err := f.s.AdminCardStatistics(t.Context(), f.admin, pool, StatisticsQuery{Page: pagination.Default(), Search: card})
		if err != nil || out.Pool.TotalDraws != total || out.Pool.ParticipantCount != participants {
			t.Fatalf("pool %+v %v", out, err)
		}
		for _, item := range out.Items {
			if item.CardID == card {
				if item.Count != copies || item.ParticipantCount != cardPeople || item.FirstAcquisitionCount != firsts {
					t.Fatalf("card %+v", item)
				}
				return
			}
		}
		t.Fatal("card missing")
	}
	assert("permanent", "3", "2", "3", "2", "1")
	f.recorder.fail.Store(true)
	if _, err = f.act(f.user, "rollback", Action{Action: "draw", PoolID: "permanent", Quantity: 1}); err == nil {
		t.Fatal("injected failure committed")
	}
	f.recorder.fail.Store(false)
	assert("permanent", "3", "2", "3", "2", "1")
	pool := f.pool(t, "Separate period", DefaultRules(), "i-chatgpt")
	f.exec(t, "UPDATE gacha_profiles SET limited=2 WHERE user_id=?", f.user)
	draw(f.user, "limited", pool.ID)
	assert(pool.ID, "1", "1", "1", "1", "0")
	// Shop redemptions and reward grants are not draw copies.
	f.exec(t, "UPDATE gacha_profiles SET ssr_shards=20 WHERE user_id=?", f.user)
	if _, err = f.act(f.user, "shop", Action{Action: "redeem", Product: "g-chatgpt", Revision: 1}); err != nil {
		t.Fatal(err)
	}
	assert("permanent", "3", "2", "3", "2", "1")
	f.at.Add(181 * 86400)
	if _, err = f.s.Retain(t.Context(), f.at.Load(), 100, time.Now().Add(2*time.Second)); err != nil {
		t.Fatal(err)
	}
	if f.scalar(t, "SELECT count(*) FROM gacha_draws") != 0 {
		t.Fatal("draw details not retained")
	}
	draw(f.user, "two", "permanent")
	assert("permanent", "3", "2", "3", "2", "1")
	draw(f.user, "after-retention", "permanent")
	assert("permanent", "4", "2", "4", "2", "1")
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
	assert("permanent", "4", "2", "4", "2", "1")
	assert(pool.ID, "1", "1", "1", "1", "0")
	if f.scalar(t, "SELECT count(*) FROM gacha_pool_participants WHERE user_id=?", f.user) != 0 || f.scalar(t, "SELECT count(*) FROM gacha_card_participants WHERE user_id=?", f.user) != 0 {
		t.Fatal("deleted membership survived")
	}
	// Detached pools retain their original identity and date range.
	f.exec(t, "DELETE FROM gacha_pools WHERE id=?", pool.ID)
	page, err := f.s.AdminPoolStatistics(t.Context(), f.admin, StatisticsQuery{Page: pagination.Default(), Search: "Separate"})
	if err != nil || len(page.Items) != 1 || page.Items[0].Available || page.Items[0].ID != pool.ID || *page.Items[0].StartsAt != *pool.StartsAt {
		t.Fatal(page, err)
	}
	if _, err := f.s.AdminSave(t.Context(), f.admin, "reuse-old-period-0123456789", AdminInput{Kind: "pool", Pool: &pool}); !errors.Is(err, ErrConflict) {
		t.Fatal("a detached pool identity was reused", err)
	}
}

func TestStatisticsPaginationExactCountsExportAndAdminAuthority(t *testing.T) {
	f := newFixture(t)
	f.enter(t)
	f.s.random = func(int) (int, error) { return 0, nil }
	out, err := f.act(f.user, "draw", Action{Action: "draw", PoolID: "permanent", Quantity: 1})
	if err != nil {
		t.Fatal(err)
	}
	card := out.Draw.Items[0].CardID
	wide, err := db.ParseU128Decimal("18446744073709551617")
	if err != nil {
		t.Fatal(err)
	}
	f.exec(t, "UPDATE gacha_pool_statistics SET total_draws=? WHERE pool_id='permanent'", db.EncodeU128(wide))
	f.exec(t, "UPDATE gacha_card_statistics SET draw_count=? WHERE pool_id='permanent' AND card_id=?", db.EncodeU128(wide), card)
	page, err := f.s.AdminCardStatistics(t.Context(), f.admin, "permanent", StatisticsQuery{Page: pagination.Request{Page: 1, Size: 10}, Search: "no-match"})
	if err != nil || len(page.Items) != 0 || len(page.TopCards) != 1 || page.TopCards[0].Count != wide.Decimal() || page.Pool.TotalDraws != wide.Decimal() {
		t.Fatal(page, err)
	}
	var jsonBody, csvBody bytes.Buffer
	if err = f.s.ExportStatistics(t.Context(), f.admin, "permanent", "json", &jsonBody); err != nil {
		t.Fatal(err)
	}
	var exported struct {
		Pool     PoolStatistics     `json:"pool"`
		Cards    []CardStatistics   `json:"cards"`
		Rarities []RarityStatistics `json:"rarity_totals"`
	}
	if err = json.Unmarshal(jsonBody.Bytes(), &exported); err != nil || len(exported.Cards) <= 10 || exported.Cards[0].Count != wide.Decimal() {
		t.Fatal(exported, err)
	}
	if bytes.Contains(jsonBody.Bytes(), []byte("user_id")) {
		t.Fatal("aggregate export exposed identities")
	}
	if err = f.s.ExportStatistics(t.Context(), f.admin, "permanent", "csv", &csvBody); err != nil {
		t.Fatal(err)
	}
	records, err := csv.NewReader(&csvBody).ReadAll()
	if err != nil || len(records) != len(exported.Cards)+1 || records[1][2] != wide.Decimal() || records[1][8] != wide.Decimal() {
		t.Fatal(records, err)
	}
	if statisticsCSVCell(" =SUM(1,2)") != "' =SUM(1,2)" {
		t.Fatal("unsafe CSV formula")
	}
	for _, actor := range []int64{0, f.user, f.other} {
		if _, err = f.s.AdminPoolStatistics(t.Context(), actor, statisticsQueryDefault()); !errors.Is(err, authz.ErrForbidden) {
			t.Fatal("pool authority", err)
		}
		if _, err = f.s.AdminCardStatistics(t.Context(), actor, "permanent", statisticsQueryDefault()); !errors.Is(err, authz.ErrForbidden) {
			t.Fatal("card authority", err)
		}
		var denied bytes.Buffer
		if err = f.s.ExportStatistics(t.Context(), actor, "permanent", "json", &denied); !errors.Is(err, authz.ErrForbidden) || denied.Len() != 0 {
			t.Fatal("export authority", err)
		}
	}
}

type statisticsRoutes map[string]limitedactivities.AuthorizedAdminHandler

func (r statisticsRoutes) RegisterAdminRoute(method, path string, h limitedactivities.AuthorizedAdminHandler) error {
	r[method+" "+path] = h
	return nil
}
func TestStatisticsHTTPQueryAndAuthority(t *testing.T) {
	f := newFixture(t)
	routes := statisticsRoutes{}
	if err := registerStatisticsRoutes(routes, f.s); err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		query  string
		status int
	}{{"", 200}, {"?page=999&page_size=10&sort=draws", 200}, {"?q=unknown", 200}, {"?sort=invalid", 400}, {"?page=0", 400}, {"?page_size=11", 400}, {"?q=a&q=b", 400}, {"?cursor=x", 400}} {
		request := httptest.NewRequest(http.MethodGet, statisticsPoolsRoute+test.query, nil)
		response := httptest.NewRecorder()
		routes[http.MethodGet+" "+statisticsPoolsRoute](response, request, limitedactivities.AdminPrincipal{UserID: f.admin})
		if response.Code != test.status {
			t.Fatalf("%s: %d %s", test.query, response.Code, response.Body.String())
		}
	}
	for _, path := range []string{statisticsPoolsRoute, statisticsCardsRoute, statisticsExportRoute} {
		request := httptest.NewRequest(http.MethodGet, strings.ReplaceAll(path, "{id}", "permanent")+"?format=json", nil)
		request.SetPathValue("id", "permanent")
		if path != statisticsExportRoute {
			request.URL.RawQuery = ""
		}
		response := httptest.NewRecorder()
		routes[http.MethodGet+" "+path](response, request, limitedactivities.AdminPrincipal{UserID: f.user})
		if response.Code != 403 {
			t.Fatalf("%s: %d %s", path, response.Code, response.Body.String())
		}
	}
}
