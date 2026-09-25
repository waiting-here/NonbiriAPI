package adminusers

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http/httptest"
	"reflect"
	"strconv"
	"strings"
	"testing"

	"github.com/waiting-here/NonbiriAPI/internal/db"
	"github.com/waiting-here/NonbiriAPI/internal/pagination"
)

func (r *testRegistrar) RegisterStewardRoute(method, pattern string, handler AuthorizedAdminHandler) error {
	return r.RegisterAdminRoute(method, pattern, handler)
}

func stewardUserRequest(t *testing.T, f *adminUsersFixture, actor, target int64, method, route, query, body, key string) *httptest.ResponseRecorder {
	t.Helper()
	handler := f.registrar.handler(method, roleSteward.route(route))
	if handler == nil {
		t.Fatalf("missing steward route: %s %s", method, route)
	}
	url := roleSteward.route(route) + query
	if target > 0 {
		url = strings.ReplaceAll(url, "{id}", strconv.FormatInt(target, 10))
	}
	request := httptest.NewRequest(method, url, strings.NewReader(body))
	request.SetPathValue("id", strconv.FormatInt(target, 10))
	if key != "" {
		request.Header.Set("Idempotency-Key", key)
	}
	recorder := httptest.NewRecorder()
	handler(recorder, request, AdminPrincipal{UserID: actor})
	return recorder
}

func newStewardUsersFixture(t *testing.T) (*adminUsersFixture, int64) {
	t.Helper()
	f := newAdminUsersFixture(t)
	actor := f.seedUser("steward", false)
	if _, err := f.store.DB().Exec("UPDATE users SET level=6 WHERE id=?", actor); err != nil {
		t.Fatal(err)
	}
	if err := RegisterStewardRoutes(f.registrar, f.service); err != nil {
		t.Fatal(err)
	}
	if len(f.registrar.routes) != 25 {
		t.Fatalf("unexpected route count: %d", len(f.registrar.routes))
	}
	return f, actor
}

func TestUserLevelFilteringIncludesLazyPromotionsAndHighWater(t *testing.T) {
	f, actor := newStewardUsersFixture(t)
	for level, threshold := range map[int]string{2: "10", 3: "20", 4: "30"} {
		if _, err := f.store.DB().Exec("UPDATE site_config SET value=? WHERE key=?", threshold, fmt.Sprintf("level_threshold_%d_milli", level)); err != nil {
			t.Fatal(err)
		}
	}
	want := map[int][]string{1: {}, 2: {}, 3: {}, 4: {}, 5: {}, 6: {strconv.FormatInt(actor, 10)}}
	for index, row := range []struct {
		donation string
		auto     int
		manual   any
		level    int
	}{
		{"0", 1, nil, 1}, {"10", 1, nil, 2}, {"20", 1, nil, 3},
		{"30", 1, nil, 4}, {"0", 4, nil, 4}, {"999", 1, 1, 1},
		{"0", 1, 5, 5}, {"999", 2, 3, 3},
	} {
		id := f.seedUser(fmt.Sprintf("level-case-%d", index), false)
		credit, err := db.ParseU128Decimal(row.donation)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := f.store.DB().Exec("UPDATE users SET donation_credit_mag=?,auto_level=?,level=? WHERE id=?", db.EncodeU128(credit), row.auto, row.manual, id); err != nil {
			t.Fatal(err)
		}
		want[row.level] = append(want[row.level], strconv.FormatInt(id, 10))
	}
	for i := 0; i < 21; i++ {
		id := f.seedUser(fmt.Sprintf("level-four-%02d", i), false)
		if _, err := f.store.DB().Exec("UPDATE users SET auto_level=4 WHERE id=?", id); err != nil {
			t.Fatal(err)
		}
		want[4] = append(want[4], strconv.FormatInt(id, 10))
	}
	for level := 1; level <= 6; level++ {
		for _, role := range []managementRole{roleAdmin, roleSteward} {
			actorID := actor
			if role == roleAdmin {
				actorID = f.adminID
			}
			var got []string
			for page := 1; page <= (len(want[level])+19)/20; page++ {
				requested := pagination.Request{Page: int64(page), Size: 20}
				result, err := f.service.listUsers(context.Background(), actorID, role, UserListQuery{Level: level, Page: &requested})
				if err != nil {
					t.Fatal(err)
				}
				if result.Pagination.TotalItems != strconv.Itoa(len(want[level])) {
					t.Fatalf("level %d total: %+v", level, result.Pagination)
				}
				for _, user := range result.Data {
					if user.Level.Effective != level {
						t.Fatalf("filtered level %d projected as %+v", level, user.Level)
					}
					got = append(got, user.ID)
				}
			}
			if !reflect.DeepEqual(got, want[level]) {
				t.Fatalf("role %d level %d IDs: %v want %v", role, level, got, want[level])
			}
		}
	}
	var stored int
	if err := f.store.DB().QueryRow("SELECT auto_level FROM users WHERE username='level-case-3'").Scan(&stored); err != nil || stored != 1 {
		t.Fatalf("list materialized lazy promotion: %d %v", stored, err)
	}
	// Cursor, total, q and level use the same selection.
	page, err := f.service.listUsers(context.Background(), actor, roleSteward, UserListQuery{Level: 4, Q: "level-four-", Limit: 1})
	if err != nil || page.NextCursor == nil {
		t.Fatalf("first cursor: %+v %v", page, err)
	}
	for _, changed := range []UserListQuery{
		{Level: 3, Q: "level-four-", Limit: 1, Cursor: *page.NextCursor},
		{Level: 4, Q: "level-case-", Limit: 1, Cursor: *page.NextCursor},
	} {
		if _, err := f.service.listUsers(context.Background(), actor, roleSteward, changed); err == nil {
			t.Fatal("cursor accepted changed filters")
		}
	}
	if _, err := f.service.listUsers(context.Background(), f.adminID, roleAdmin, UserListQuery{Level: 4, Q: "level-four-", Limit: 1, Cursor: *page.NextCursor}); err == nil {
		t.Fatal("cursor crossed role/actor boundary")
	}
	for _, query := range []string{"?level=", "?level=0", "?level=7", "?level=01", "?level=1&level=2", "?level=1.0"} {
		if got := stewardUserRequest(t, f, actor, 0, "GET", routeUsers, query, "", ""); got.Code != 400 {
			t.Fatalf("level query %s: %d", query, got.Code)
		}
	}
}

func TestUserIDFilterIsExactAndComposesWithExistingFilters(t *testing.T) {
	f, actor := newStewardUsersFixture(t)
	match := f.seedUser("same-name", false)
	other := f.seedUser("same-name-other", false)
	if _, err := f.store.DB().Exec("UPDATE users SET level=5,is_banned=1,banned_until=? WHERE id=?", adminUsersTestNow+3600, other); err != nil {
		t.Fatal(err)
	}
	page, err := f.service.listUsers(context.Background(), actor, roleSteward, UserListQuery{
		UserID: match,
		Q:      "same-name",
		Limit:  20,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(page.Data) != 1 || page.Data[0].ID != strconv.FormatInt(match, 10) {
		t.Fatalf("exact user filter returned %+v", page.Data)
	}
	page, err = f.service.listUsers(context.Background(), actor, roleSteward, UserListQuery{
		UserID:   match,
		IsBanned: boolPointer(true),
		Limit:    20,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(page.Data) != 0 {
		t.Fatalf("user id did not compose with status filter: %+v", page.Data)
	}
	for _, query := range []string{
		"?user_id=0",
		"?user_id=01",
		"?user_id=9223372036854775808",
		"?user_id=not-a-number",
		"?user_id=1&user_id=2",
	} {
		if got := stewardUserRequest(t, f, actor, 0, "GET", routeUsers, query, "", ""); got.Code != 400 {
			t.Fatalf("user_id query %s: %d", query, got.Code)
		}
	}
}

func boolPointer(value bool) *bool { return &value }

func TestManagedUserLimitsRemainDecimalStrings(t *testing.T) {
	f, actor := newStewardUsersFixture(t)
	target := f.seedUser("limits", false)
	for i, raw := range []string{`"99"`, "null", `"999"`} {
		body := fmt.Sprintf(`{"mode":"profile","expected_revision":"%s","endpoint_limit":%s,"rpm_limit":%s,"concurrency_limit":%s}`, f.revision(target), raw, raw, raw)
		response := stewardUserRequest(t, f, actor, target, "PATCH", routeUser, "", body, strings.Repeat(string(rune('a'+i)), 22))
		if response.Code != 200 {
			t.Fatalf("valid limit %s: %d %s", raw, response.Code, response.Body)
		}
		var view map[string]json.RawMessage
		if err := json.Unmarshal(response.Body.Bytes(), &view); err != nil {
			t.Fatal(err)
		}
		for _, key := range []string{"endpoint_limit", "rpm_limit", "concurrency_limit"} {
			if string(view[key]) != raw {
				t.Fatalf("%s=%s want %s", key, view[key], raw)
			}
		}
	}
	for i, field := range []string{
		`"endpoint_limit":99`, `"endpoint_limit":"01"`, `"endpoint_limit":"1.0"`,
		`"endpoint_limit":"10001"`, `"rpm_limit":"0"`, `"rpm_limit":"4097"`,
		`"concurrency_limit":"100001"`, `"concurrency_limit":"-1"`, `"endpoint_limit":" 99"`,
	} {
		revision := f.revision(target)
		body := fmt.Sprintf(`{"mode":"profile","expected_revision":"%s",%s}`, revision, field)
		response := stewardUserRequest(t, f, actor, target, "PATCH", routeUser, "", body, strings.Repeat(string(rune('e'+i)), 22))
		if response.Code != 400 || f.revision(target) != revision {
			t.Fatalf("invalid limit %s: %d %s", field, response.Code, response.Body)
		}
	}
	body := fmt.Sprintf(`{"mode":"profile","expected_revision":"%s","endpoint_limit":"0","rpm_limit":"4096","concurrency_limit":"100000","level":4,"lang":"en"}`, f.revision(target))
	if got := stewardUserRequest(t, f, actor, target, "PATCH", routeUser, "", body, strings.Repeat("t", 22)); got.Code != 200 {
		t.Fatalf("limit bounds: %d %s", got.Code, got.Body)
	}
}

func TestStewardUserMutationsProtectRoleTargetAndReplay(t *testing.T) {
	f, actor := newStewardUsersFixture(t)
	target := f.seedUser("target", false)
	peer := f.seedUser("peer", false)
	if _, err := f.store.DB().Exec("UPDATE users SET level=6 WHERE id=?", peer); err != nil {
		t.Fatal(err)
	}
	for _, user := range []int64{actor, peer} {
		if got := stewardUserRequest(t, f, actor, user, "GET", routeUser, "", "", ""); got.Code != 200 {
			t.Fatalf("read peer/self: %d", got.Code)
		}
		for _, action := range []struct{ method, route, body string }{
			{"PATCH", routeUser, `{"mode":"profile","expected_revision":"1","lang":"en"}`},
			{"PATCH", routeUser, `{"mode":"economy","expected_revision":"1","target":"game_balance","direction":"increase","amount":"1","reason":"Correction"}`},
			{"POST", routeBan, `{"expected_revision":"1","reason":"Review","duration_seconds":null}`},
			{"POST", routeUnban, `{"expected_revision":"1"}`},
		} {
			got := stewardUserRequest(t, f, actor, user, action.method, action.route, "", action.body, strings.Repeat("p", 22))
			if got.Code != 403 {
				t.Fatalf("protected target %d %s: %d %s", user, action.route, got.Code, got.Body)
			}
		}
	}
	if got := stewardUserRequest(t, f, actor, f.adminID, "GET", routeUser, "", "", ""); got.Code != 404 {
		t.Fatalf("administrator exposed: %d", got.Code)
	}
	for i, body := range []string{
		`{"mode":"profile","expected_revision":"1","level":6}`,
		`{"mode":"economy","expected_revision":"1","target":"donation_credit","direction":"increase","amount":"1","reason":"Correction"}`,
	} {
		if got := stewardUserRequest(t, f, actor, target, "PATCH", routeUser, "", body, strings.Repeat(string(rune('c'+i)), 22)); got.Code != 403 {
			t.Fatalf("forbidden field accepted: %d %s", got.Code, got.Body)
		}
	}
	body := `{"mode":"economy","expected_revision":"1","target":"game_balance","direction":"decrease","amount":"1.234","reason":"Correction"}`
	key := strings.Repeat("g", 22)
	first := stewardUserRequest(t, f, actor, target, "PATCH", routeUser, "", body, key)
	replay := stewardUserRequest(t, f, actor, target, "PATCH", routeUser, "", body, key)
	if first.Code != 200 || replay.Code != 200 || !bytes.Equal(first.Body.Bytes(), replay.Body.Bytes()) {
		t.Fatalf("adjustment/replay: %d %d %s", first.Code, replay.Code, first.Body)
	}
	var user AdminUser
	if err := json.Unmarshal(first.Body.Bytes(), &user); err != nil || user.GameBalance != "-1.234" || user.Balance != "0" || user.DonationCredit != "0" {
		t.Fatalf("wallet projection: %+v %v", user, err)
	}
	var operationActor, entries int64
	if err := f.store.DB().QueryRow("SELECT actor_user_id FROM credit_operations WHERE kind='admin_user_adjustment'").Scan(&operationActor); err != nil || operationActor != actor {
		t.Fatalf("actual audit actor: %d %v", operationActor, err)
	}
	if err := f.store.DB().QueryRow("SELECT COUNT(*) FROM credit_entries WHERE asset_type='game'").Scan(&entries); err != nil || entries != 2 {
		t.Fatalf("actual game entries: %d %v", entries, err)
	}
	// Promotion after the original success blocks replay and a new mutation.
	if _, err := f.store.DB().Exec("UPDATE users SET level=6 WHERE id=?", target); err != nil {
		t.Fatal(err)
	}
	if got := stewardUserRequest(t, f, actor, target, "PATCH", routeUser, "", body, key); got.Code != 403 {
		t.Fatalf("promoted target replay: %d", got.Code)
	}
	if _, err := f.store.DB().Exec("UPDATE users SET level=NULL WHERE id=?", target); err != nil {
		t.Fatal(err)
	}
	banBody := fmt.Sprintf(`{"expected_revision":"%s","reason":"Review","duration_seconds":null}`, f.revision(target))
	if got := stewardUserRequest(t, f, actor, target, "POST", routeBan, "", banBody, strings.Repeat("b", 22)); got.Code != 204 {
		t.Fatalf("ban: %d %s", got.Code, got.Body)
	}
	unbanBody := fmt.Sprintf(`{"expected_revision":"%s"}`, f.revision(target))
	if got := stewardUserRequest(t, f, actor, target, "POST", routeUnban, "", unbanBody, strings.Repeat("u", 22)); got.Code != 204 {
		t.Fatalf("unban: %d %s", got.Code, got.Body)
	}
	if _, err := f.store.DB().Exec("UPDATE users SET level=4 WHERE id=?", actor); err != nil {
		t.Fatal(err)
	}
	for _, action := range []struct{ method, route, body, key string }{
		{"GET", routeUsers, "", ""},
		{"PATCH", routeUser, body, key},
		{"POST", routeBan, banBody, strings.Repeat("b", 22)},
		{"POST", routeUnban, unbanBody, strings.Repeat("u", 22)},
	} {
		if got := stewardUserRequest(t, f, actor, target, action.method, action.route, "", action.body, action.key); got.Code != 403 {
			t.Fatalf("demoted actor %s: %d %s", action.route, got.Code, got.Body)
		}
	}
}
