package adminusers

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"

	"github.com/waiting-here/NonbiriAPI/internal/pagination"
)

func TestForbiddenManagementWritesDoNotRetireTargetRequests(t *testing.T) {
	f, actor := newStewardUsersFixture(t)
	target := f.seedUser("protected-peer", false)
	const discordID = "123456789012345950"
	if _, err := f.store.DB().Exec(`UPDATE users SET discord_id=?,level=6 WHERE id=?`, discordID, target); err != nil {
		t.Fatal(err)
	}
	locks := 0
	f.service.identityBarrier = func(context.Context, string) (func(), error) {
		locks++
		return func() {}, nil
	}
	ctx := context.Background()
	if _, _, err := f.service.lockUserIdentity(ctx, actor, target, roleSteward); !errors.Is(err, ErrForbidden) {
		t.Fatalf("peer identity preflight: %v", err)
	}
	if _, err := f.service.setBlacklist(ctx, actor, roleSteward, ControlMutation{}, discordID, "forbidden", true); !errors.Is(err, ErrForbidden) {
		t.Fatalf("peer blacklist preflight: %v", err)
	}
	if locks != 0 {
		t.Fatalf("forbidden requests entered the target retirement barrier %d times", locks)
	}
}

func managementRequest(f *adminUsersFixture, actor int64, method, route, target, body, key string) *httptest.ResponseRecorder {
	f.t.Helper()
	handler := f.registrar.handler(method, route)
	if handler == nil {
		f.t.Fatalf("route absent: %s %s", method, route)
	}
	request := httptest.NewRequest(method, target, strings.NewReader(body))
	if key != "" {
		request.Header.Set("Idempotency-Key", key)
	}
	if strings.Contains(route, "{discordID}") {
		pieces := strings.Split(strings.TrimPrefix(target, "/"), "/")
		for _, part := range pieces {
			if validDiscordID(part) {
				request.SetPathValue("discordID", part)
				break
			}
		}
	}
	if strings.Contains(route, "{recordID}") {
		pieces := strings.Split(strings.TrimPrefix(target, "/"), "/")
		for _, part := range pieces {
			if id, err := strconv.ParseInt(part, 10, 64); err == nil && id > 0 {
				request.SetPathValue("recordID", part)
				break
			}
		}
	}
	w := httptest.NewRecorder()
	handler(w, request, AdminPrincipal{UserID: actor})
	return w
}

func TestBlacklistFirstEventStewardAccessAndPromotionRecheck(t *testing.T) {
	f := newAdminUsersFixture(t)
	steward := f.seedUser("blacklist-steward", false)
	if _, err := f.store.DB().Exec(`UPDATE users SET level=6 WHERE id=?`, steward); err != nil {
		t.Fatal(err)
	}
	if err := RegisterStewardRoutes(f.registrar, f.service); err != nil {
		t.Fatal(err)
	}
	const discordID = "123456789012345900"
	target := f.seedUser("blacklist-target", false)
	if _, err := f.store.DB().Exec(`UPDATE users SET discord_id=?,level=5 WHERE id=?`, discordID, target); err != nil {
		t.Fatal(err)
	}
	first := managementRequest(f, steward, "POST", roleSteward.route(routeBlacklist), roleSteward.route(routeBlacklist), `{"discord_id":"`+discordID+`","reason":"first note"}`, "AAABBBCCCDDDEEEFFFGGGA")
	if first.Code != http.StatusNoContent {
		t.Fatalf("first=%d %s", first.Code, first.Body.String())
	}
	again := managementRequest(f, f.adminID, "POST", routeBlacklist, routeBlacklist, `{"discord_id":"`+discordID+`","reason":"second note"}`, "AAABBBCCCDDDEEEFFFGGGB")
	if again.Code != http.StatusNoContent {
		t.Fatalf("again=%d %s", again.Code, again.Body.String())
	}
	var reason, kind string
	var actor int64
	if err := f.store.DB().QueryRow(`SELECT b.reason,o.first_actor_kind,o.first_actor_user_id FROM discord_blacklist b JOIN discord_blacklist_origins o USING(discord_id) WHERE b.discord_id=?`, discordID).Scan(&reason, &kind, &actor); err != nil {
		t.Fatal(err)
	}
	if reason != "first note" || kind != "steward6" || actor != steward {
		t.Fatalf("first event changed: %q %q %d", reason, kind, actor)
	}
	events := managementRequest(f, steward, "GET", roleSteward.route(routeBlacklistEvents), "/api/steward/blacklist/"+discordID+"/events?page=1&page_size=20", "", "")
	if events.Code != http.StatusOK {
		t.Fatalf("events=%d %s", events.Code, events.Body.String())
	}
	var eventPage Page[BlacklistEvent]
	if err := json.Unmarshal(events.Body.Bytes(), &eventPage); err != nil {
		t.Fatal(err)
	}
	if len(eventPage.Data) != 2 || eventPage.Data[0].ActorKind != "steward6" || eventPage.Data[1].ActorKind != "admin" {
		t.Fatalf("events=%+v", eventPage.Data)
	}
	const adminOriginID = "123456789012345910"
	adminOrigin := managementRequest(f, f.adminID, "POST", routeBlacklist, routeBlacklist, `{"discord_id":"`+adminOriginID+`","reason":"administrator origin"}`, "AAABBBCCCDDDEEEFFFGGGF")
	if adminOrigin.Code != http.StatusNoContent {
		t.Fatalf("admin origin=%d %s", adminOrigin.Code, adminOrigin.Body.String())
	}
	visible, err := f.service.listBlacklistFiltered(context.Background(), steward, roleSteward,
		BlacklistQuery{ActorKind: "admin", DiscordID: adminOriginID, Page: &pagination.Request{Page: 1, Size: 20}})
	if err != nil || len(visible.Data) != 1 || visible.Data[0].FirstActorKind != "admin" {
		t.Fatalf("steward cannot see ordinary target with admin-origin event: %+v %v", visible, err)
	}
	adminEvents := managementRequest(f, steward, "GET", roleSteward.route(routeBlacklistEvents), "/api/steward/blacklist/"+adminOriginID+"/events?page=1&page_size=20", "", "")
	if adminEvents.Code != http.StatusOK {
		t.Fatalf("steward admin-origin event=%d %s", adminEvents.Code, adminEvents.Body.String())
	}
	if f.registrar.handler("POST", roleSteward.route(routeBlacklistRemove)) != nil {
		t.Fatal("steward removal route was registered")
	}
	// The identity gate can serialize a concurrent promotion before the write TX.
	f.service.identityMu.Lock()
	f.service.identityBarrier = func(_ context.Context, id string) (func(), error) {
		if id != discordID {
			t.Fatalf("wrong identity lock %q", id)
		}
		if _, err := f.store.DB().Exec(`UPDATE users SET level=6 WHERE id=?`, target); err != nil {
			return nil, err
		}
		return func() {}, nil
	}
	f.service.identityMu.Unlock()
	denied := managementRequest(f, steward, "POST", roleSteward.route(routeBlacklist), roleSteward.route(routeBlacklist), `{"discord_id":"`+discordID+`","reason":"third note"}`, "AAABBBCCCDDDEEEFFFGGGC")
	if denied.Code != http.StatusForbidden {
		t.Fatalf("same-level promotion=%d %s", denied.Code, denied.Body.String())
	}
	visibleSix, err := f.service.listBlacklistFiltered(context.Background(), steward, roleSteward,
		BlacklistQuery{DiscordID: discordID, Page: &pagination.Request{Page: 1, Size: 20}})
	if err != nil || len(visibleSix.Data) != 1 {
		t.Fatalf("current level-six target should remain readable: %+v %v", visibleSix, err)
	}
	sixEvents := managementRequest(f, steward, "GET", roleSteward.route(routeBlacklistEvents), "/api/steward/blacklist/"+discordID+"/events?page=1&page_size=20", "", "")
	if sixEvents.Code != http.StatusOK {
		t.Fatalf("current level-six events=%d %s", sixEvents.Code, sixEvents.Body.String())
	}
	var count int
	if err := f.store.DB().QueryRow(`SELECT COUNT(*) FROM discord_blacklist_events WHERE discord_id=?`, discordID).Scan(&count); err != nil || count != 2 {
		t.Fatalf("unexpected event count %d %v", count, err)
	}
}

func TestStewardCannotWriteOrReadCurrentAdministratorBlacklistTarget(t *testing.T) {
	f := newAdminUsersFixture(t)
	steward := f.seedUser("admin-target-steward", false)
	if _, err := f.store.DB().Exec(`UPDATE users SET level=6 WHERE id=?`, steward); err != nil {
		t.Fatal(err)
	}
	if err := RegisterStewardRoutes(f.registrar, f.service); err != nil {
		t.Fatal(err)
	}
	const adminDiscord = "123456789012345911"
	// A protected administrator cannot be added through the management API.
	if _, err := f.store.DB().Exec(`UPDATE users SET discord_id=? WHERE id=?`, adminDiscord, f.adminID); err != nil {
		t.Fatal(err)
	}
	denied := managementRequest(f, steward, "POST", roleSteward.route(routeBlacklist), roleSteward.route(routeBlacklist), `{"discord_id":"`+adminDiscord+`","reason":"forbidden"}`, "AAABBBCCCDDDEEEFFFGGGG")
	if denied.Code != http.StatusForbidden {
		t.Fatalf("administrator target write=%d %s", denied.Code, denied.Body.String())
	}
	// A historically blocked identity can later belong to a protected
	// administrator. The steward read surface must still hide that row.
	if _, err := f.store.DB().Exec(`UPDATE users SET discord_id='discord-administrator' WHERE id=?`, f.adminID); err != nil {
		t.Fatal(err)
	}
	added := managementRequest(f, f.adminID, "POST", routeBlacklist, routeBlacklist, `{"discord_id":"`+adminDiscord+`","reason":"historical block"}`, "AAABBBCCCDDDEEEFFFGGGH")
	if added.Code != http.StatusNoContent {
		t.Fatalf("historical block=%d %s", added.Code, added.Body.String())
	}
	if _, err := f.store.DB().Exec(`UPDATE users SET discord_id=?,is_banned=1,banned_until=NULL WHERE id=?`, adminDiscord, f.adminID); err != nil {
		t.Fatal(err)
	}
	page, err := f.service.listBlacklistFiltered(context.Background(), steward, roleSteward,
		BlacklistQuery{DiscordID: adminDiscord, Page: &pagination.Request{Page: 1, Size: 20}})
	if err != nil || len(page.Data) != 0 {
		t.Fatalf("administrator target leaked in list: %+v %v", page, err)
	}
	events := managementRequest(f, steward, "GET", roleSteward.route(routeBlacklistEvents), "/api/steward/blacklist/"+adminDiscord+"/events?page=1&page_size=20", "", "")
	if events.Code != http.StatusForbidden {
		t.Fatalf("administrator target events=%d %s", events.Code, events.Body.String())
	}
}

func seedDeletedAccount(t *testing.T, database *sql.DB, former int64, discordID string, level any, banned any, snapshot string) int64 {
	t.Helper()
	result, err := database.Exec(`INSERT INTO admin_alerts(kind,message,created_at,resolved,resolved_at,resolution_kind) VALUES('account_deleted','Account deleted.',?,1,?,'')`, adminUsersTestNow, adminUsersTestNow)
	if err != nil {
		t.Fatal(err)
	}
	alertID, err := result.LastInsertId()
	if err != nil {
		t.Fatal(err)
	}
	_, err = database.Exec(`INSERT INTO admin_account_deletions(alert_id,snapshot_json,snapshot_version,former_user_id,discord_id,deleted_at,effective_level,source,ban_active,blacklist_action) VALUES(?,?,1,?,?,?,?,'unknown',?,'unknown')`, alertID, snapshot, former, discordID, adminUsersTestNow, level, banned)
	if err != nil {
		t.Fatal(err)
	}
	return alertID
}

func TestDeletedAccountsMixedFiltersAndReadOnlyAuthorization(t *testing.T) {
	f := newAdminUsersFixture(t)
	steward := f.seedUser("history-steward", false)
	trainee := f.seedUser("history-trainee", false)
	if _, err := f.store.DB().Exec(`UPDATE users SET level=6 WHERE id=?`, steward); err != nil {
		t.Fatal(err)
	}
	if _, err := f.store.DB().Exec(`UPDATE users SET level=5 WHERE id=?`, trainee); err != nil {
		t.Fatal(err)
	}
	if err := RegisterStewardRoutes(f.registrar, f.service); err != nil {
		t.Fatal(err)
	}
	const discordID = "123456789012345901"
	active := f.seedUser("history-current", false)
	if _, err := f.store.DB().Exec(`UPDATE users SET discord_id=? WHERE id=?`, discordID, active); err != nil {
		t.Fatal(err)
	}
	old1 := seedDeletedAccount(t, f.store.DB(), 100001, discordID, nil, nil, `{"user_id":"100001","discord_id":"`+discordID+`","general_balance":"-2"}`)
	_ = seedDeletedAccount(t, f.store.DB(), 100002, discordID, 5, 1, `{"user_id":"100002","discord_id":"`+discordID+`","general_balance":"0"}`)
	page, err := f.service.listManagedAccounts(context.Background(), steward, roleSteward, UserListQuery{Q: discordID, Page: &pagination.Request{Page: 1, Size: 20}})
	if err != nil || len(page.Data) != 3 || page.Pagination.TotalItems != "3" {
		t.Fatalf("all=%+v %v", page, err)
	}
	known := true
	page, err = f.service.listManagedAccounts(context.Background(), steward, roleSteward, UserListQuery{Q: discordID, IsBanned: &known, Page: &pagination.Request{Page: 1, Size: 20}})
	if err != nil || len(page.Data) != 1 || page.Data[0].AccountState != "deleted" || page.Data[0].Deleted.FormerUserID == nil || *page.Data[0].Deleted.FormerUserID != "100002" {
		t.Fatalf("banned=%+v %v", page, err)
	}
	detail, err := f.service.getDeletedAccount(context.Background(), steward, old1, roleSteward)
	if err != nil || detail.Ban.State != "unknown" || detail.RegisteredAt != nil || detail.EffectiveLevel != nil || detail.GeneralBalance == nil || *detail.GeneralBalance != "-2" || detail.AlertID != nil {
		t.Fatalf("unknown=%+v %v", detail, err)
	}
	if _, err := f.service.getDeletedAccount(context.Background(), trainee, old1, roleSteward); err == nil {
		t.Fatal("level five read history")
	}
	adminDetail, err := f.service.getDeletedAccount(context.Background(), f.adminID, old1, roleAdmin)
	if err != nil || adminDetail.AlertID == nil || *adminDetail.AlertID != strconv.FormatInt(old1, 10) {
		t.Fatalf("admin detail=%+v %v", adminDetail, err)
	}
	if f.registrar.handler("PATCH", roleSteward.route(routeDeletedUser)) != nil {
		t.Fatal("history mutation route was registered")
	}
	nearby := f.seedUser("history-nearby-identity", false)
	if _, err := f.store.DB().Exec(`UPDATE users SET discord_id=? WHERE id=?`, discordID+"0", nearby); err != nil {
		t.Fatal(err)
	}
	exact, err := f.service.listManagedAccounts(context.Background(), steward, roleSteward,
		UserListQuery{DiscordID: discordID, Page: &pagination.Request{Page: 1, Size: 20}})
	if err != nil || len(exact.Data) != 3 || exact.Pagination.TotalItems != "3" {
		t.Fatalf("exact Discord must exclude prefix match: %+v %v", exact, err)
	}
	filtered := managementRequest(f, steward, "GET", roleSteward.route(routeUsers), "/api/steward/users?account_state=all&discord_id="+discordID+"&page=1&page_size=20", "", "")
	if filtered.Code != http.StatusOK {
		t.Fatalf("exact Discord HTTP filter=%d %s", filtered.Code, filtered.Body.String())
	}
}

func TestBlacklistUnicodeNoteLimitAndPagedFilters(t *testing.T) {
	f := newAdminUsersFixture(t)
	const discordID = "123456789012345902"
	note := strings.Repeat("界", 2000)
	response := managementRequest(f, f.adminID, "POST", routeBlacklist, routeBlacklist,
		`{"discord_id":"`+discordID+`","reason":"`+note+`"}`, "AAABBBCCCDDDEEEFFFGGGD")
	if response.Code != http.StatusNoContent {
		t.Fatalf("2000-scalar note=%d %s", response.Code, response.Body.String())
	}
	var first, event string
	if err := f.store.DB().QueryRow(`SELECT reason FROM discord_blacklist WHERE discord_id=?`, discordID).Scan(&first); err != nil {
		t.Fatal(err)
	}
	if err := f.store.DB().QueryRow(`SELECT safe_note FROM discord_blacklist_events WHERE discord_id=?`, discordID).Scan(&event); err != nil {
		t.Fatal(err)
	}
	if first != note || event != note {
		t.Fatal("first note and event did not retain full Unicode text")
	}
	response = managementRequest(f, f.adminID, "POST", routeBlacklist, routeBlacklist,
		`{"discord_id":"`+discordID+`","reason":"`+note+`界"}`, "AAABBBCCCDDDEEEFFFGGGE")
	if response.Code != http.StatusBadRequest {
		t.Fatalf("2001-scalar note=%d", response.Code)
	}
	page, err := f.service.listBlacklistFiltered(context.Background(), f.adminID, roleAdmin,
		BlacklistQuery{ActorKind: "admin", ActorUserID: f.adminID, DiscordID: discordID, Page: &pagination.Request{Page: 1, Size: 20}})
	if err != nil || len(page.Data) != 1 || page.Data[0].FirstActorKind != "admin" || page.Pagination.TotalItems != "1" {
		t.Fatalf("filtered=%+v %v", page, err)
	}
	page, err = f.service.listBlacklistFiltered(context.Background(), f.adminID, roleAdmin,
		BlacklistQuery{ActorKind: "steward6", Page: &pagination.Request{Page: 1, Size: 20}})
	if err != nil || len(page.Data) != 0 {
		t.Fatalf("wrong actor filter=%+v %v", page, err)
	}
	response = managementRequest(f, f.adminID, "GET", routeBlacklist, routeBlacklist+"?page=1&cursor=invalid", "", "")
	if response.Code != http.StatusBadRequest {
		t.Fatalf("mixed pagination=%d", response.Code)
	}
}

func TestDeletionDuelAbortExactDiscordAndRetentionWindow(t *testing.T) {
	f := newAdminUsersFixture(t)
	const discordID = "123456789012345903"
	for index, occurred := range []int64{adminUsersTestNow - 1, adminUsersTestNow - 7776000} {
		_, err := f.store.DB().Exec(`INSERT INTO self_deletion_duel_aborts(discord_id,game_key,match_id,former_user_id,reason,occurred_at,expires_at) VALUES(?,'bidding',?,123,'self_deletion_cancelled_match',?,?)`, discordID, fmt.Sprintf("match-%d", index), occurred, occurred+7776000)
		if err != nil {
			t.Fatal(err)
		}
	}
	page, err := f.service.listDeletionDuelAborts(context.Background(), f.adminID, discordID, &pagination.Request{Page: 1, Size: 20})
	if err != nil || len(page.Data) != 1 || page.Data[0].MatchID != "match-0" || page.Pagination.TotalItems != "1" {
		t.Fatalf("retained=%+v %v", page, err)
	}
	other, err := f.service.listDeletionDuelAborts(context.Background(), f.adminID, "123456789012345904", &pagination.Request{Page: 1, Size: 20})
	if err != nil || len(other.Data) != 0 {
		t.Fatalf("cross Discord=%+v %v", other, err)
	}
	response := managementRequest(f, f.adminID, "GET", routeDeletionDuelAborts, routeDeletionDuelAborts+"?discord_id="+discordID+"&page=1&page_size=20", "", "")
	if response.Code != http.StatusOK {
		t.Fatalf("admin read=%d %s", response.Code, response.Body.String())
	}
	if f.registrar.handler("GET", roleSteward.route(routeDeletionDuelAborts)) != nil {
		t.Fatal("duel abort route exposed to steward")
	}
}
