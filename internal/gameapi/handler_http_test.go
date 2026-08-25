package gameapi

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/waiting-here/NonbiriAPI/internal/auth"
	"github.com/waiting-here/NonbiriAPI/internal/db"
	"github.com/waiting-here/NonbiriAPI/internal/dbtest"
	"github.com/waiting-here/NonbiriAPI/internal/game"
	"github.com/waiting-here/NonbiriAPI/internal/host"
	"github.com/waiting-here/NonbiriAPI/internal/secret"
)

type gameHTTPProvider struct{}

func (gameHTTPProvider) AuthorizationURL(context.Context, auth.DiscordAuthorizeRequest) (string, error) {
	return "https://discord.example/authorize", nil
}

func (gameHTTPProvider) Exchange(context.Context, string, string) (auth.DiscordLogin, error) {
	return auth.DiscordLogin{}, fmt.Errorf("provider exchange is not used")
}

type gameHTTPZeroSource struct{}

func (gameHTTPZeroSource) Uint64n(uint64) (uint64, error) { return 0, nil }

type gameHTTPEnv struct {
	store      *db.Store
	user       *db.User
	other      *db.User
	admin      *db.User
	settlement *db.GameSettlementService
	userAuth   *auth.UserAuth
	adminAuth  *auth.AdminAuth
	userMount  http.Handler
	adminMount http.Handler
}

func newGameHTTPEnv(t *testing.T) *gameHTTPEnv {
	t.Helper()
	key := bytes.Repeat([]byte{0x65}, secret.MasterKeyBytes)
	vault, err := secret.New(key)
	clear(key)
	if err != nil {
		t.Fatalf("secret.New: %v", err)
	}
	path := filepath.Join(t.TempDir(), "games-http.db")
	dbtest.EnsureOwnerOnlyParent(t, path)
	store, err := db.Open(path, vault)
	if err != nil {
		_ = vault.Close()
		t.Fatalf("db.Open: %v", err)
	}
	user, err := store.CreateUser("discord-http-user", "http-user", "global")
	if err != nil {
		t.Fatalf("CreateUser: %v", err)
	}
	other, err := store.CreateUser("discord-http-other", "other-user", "")
	if err != nil {
		t.Fatalf("CreateUser other: %v", err)
	}
	admin, err := store.EnsureAdminUser("http-admin")
	if err != nil {
		t.Fatalf("EnsureAdminUser: %v", err)
	}
	if _, err := store.DB().Exec(`UPDATE users SET credits=? WHERE id IN (?,?)`, 10_000_000, user.ID, other.ID); err != nil {
		t.Fatalf("seed credits: %v", err)
	}
	for key, value := range map[string]string{game.GamesEnabledKey: "1", game.FishingEnabledKey: "1"} {
		if err := store.SetSiteConfigValue(key, value); err != nil {
			t.Fatalf("enable %s: %v", key, err)
		}
	}
	settlement, err := db.NewGameSettlementService(db.GameSettlementServiceConfig{
		Store: store, OutcomeSource: gameHTTPZeroSource{}, Now: time.Now,
	})
	if err != nil {
		t.Fatalf("NewGameSettlementService: %v", err)
	}
	userAuth, err := auth.NewUserAuth(auth.UserAuthConfig{
		Store: store, Provider: gameHTTPProvider{}, ClientID: "game-http-test", SiteBaseURL: "https://example.com",
	})
	if err != nil {
		t.Fatalf("NewUserAuth: %v", err)
	}
	adminAuth, err := auth.NewAdminAuth(auth.AdminAuthConfig{
		Store: store, Username: "http-admin", Password: "test-password", SiteBaseURL: "https://example.com",
	})
	if err != nil {
		_ = userAuth.Close()
		_ = settlement.Close()
		_ = store.Close()
		_ = vault.Close()
		t.Fatalf("NewAdminAuth: %v", err)
	}
	base := NewHandler(HandlerDeps{Store: store, Settlement: settlement})
	env := &gameHTTPEnv{store: store, user: user, other: other, admin: admin, settlement: settlement,
		userAuth: userAuth, adminAuth: adminAuth, userMount: auth.UserSessionMiddleware(userAuth, base),
		adminMount: auth.AdminSessionMiddleware(adminAuth, base)}
	t.Cleanup(func() {
		_ = userAuth.Close()
		_ = adminAuth.Close()
		_ = settlement.Close()
		_ = store.Close()
		_ = vault.Close()
	})
	return env
}

func gameHTTPRequest(method, target string, station host.Station, body io.Reader) *http.Request {
	r := httptest.NewRequest(method, target, body)
	r.RemoteAddr = "198.51.100.30:4000"
	return r.WithContext(host.WithStation(r.Context(), station))
}

func gameHTTPUserRequest(t *testing.T, env *gameHTTPEnv, userID int64, method, target string, body io.Reader) *http.Request {
	t.Helper()
	token, _, err := env.store.CreateUserSession(userID)
	if err != nil {
		t.Fatalf("CreateUserSession: %v", err)
	}
	r := gameHTTPRequest(method, target, host.StationUser, body)
	r.AddCookie(&http.Cookie{Name: auth.UserSessionCookieName, Value: token})
	return r
}

func gameHTTPAdminRequest(t *testing.T, env *gameHTTPEnv, method, target string, body io.Reader) *http.Request {
	t.Helper()
	token, _, err := env.store.CreateAdminSession(env.admin.ID)
	if err != nil {
		t.Fatalf("CreateAdminSession: %v", err)
	}
	r := gameHTTPRequest(method, target, host.StationAdmin, body)
	r.AddCookie(&http.Cookie{Name: auth.AdminSessionCookieName, Value: token})
	return r
}

func gameHTTPDo(t *testing.T, h http.Handler, r *http.Request) *httptest.ResponseRecorder {
	t.Helper()
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, r)
	if rec.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("Cache-Control = %q, want no-store for %s %s", rec.Header().Get("Cache-Control"), r.Method, r.URL.Path)
	}
	return rec
}

func decodeJSONObject(t *testing.T, body []byte) map[string]json.RawMessage {
	t.Helper()
	var value map[string]json.RawMessage
	if err := json.Unmarshal(body, &value); err != nil {
		t.Fatalf("decode response %q: %v", body, err)
	}
	return value
}

func assertJSONKeys(t *testing.T, object map[string]json.RawMessage, want ...string) {
	t.Helper()
	expected := make(map[string]struct{}, len(want))
	for _, key := range want {
		expected[key] = struct{}{}
	}
	if len(object) != len(expected) {
		t.Fatalf("JSON keys = %v, want exactly %v", sortedJSONKeys(object), want)
	}
	for key := range object {
		if _, ok := expected[key]; !ok {
			t.Fatalf("unexpected JSON key %q in %v", key, sortedJSONKeys(object))
		}
	}
}

func sortedJSONKeys(object map[string]json.RawMessage) []string {
	keys := make([]string, 0, len(object))
	for key := range object {
		keys = append(keys, key)
	}
	return keys
}

func responseStringField(t *testing.T, object map[string]json.RawMessage, key string) string {
	t.Helper()
	var value string
	if err := json.Unmarshal(object[key], &value); err != nil {
		t.Fatalf("field %s: %v", key, err)
	}
	return value
}

func responseBoolField(t *testing.T, object map[string]json.RawMessage, key string) bool {
	t.Helper()
	var value bool
	if err := json.Unmarshal(object[key], &value); err != nil {
		t.Fatalf("field %s: %v", key, err)
	}
	return value
}

func assertGameError(t *testing.T, rec *httptest.ResponseRecorder, status int, codeString string) {
	t.Helper()
	if rec.Code != status {
		t.Fatalf("status=%d want=%d body=%s", rec.Code, status, rec.Body.String())
	}
	var envelope struct {
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &envelope); err != nil || envelope.Error.Code != codeString {
		t.Fatalf("error code=%q err=%v body=%s", envelope.Error.Code, err, rec.Body.String())
	}
}

func insertHTTPLeaderboardRound(t *testing.T, store *db.Store, userID int64, index int, settledAt, payout int64) {
	t.Helper()
	settlementID := fmt.Sprintf("http-leader-settlement-%02d", index)
	roundID := fmt.Sprintf("http-leader-round-%02d", index)
	createdAt := settledAt - 1
	hash := make([]byte, 32)
	hash[0] = byte(index)
	if _, err := store.DB().Exec(`INSERT INTO game_settlements(
		id,user_id,game_type,game_version,state,entry_milli,payout_milli,auto_settle_at,created_at,finalized_at,updated_at
	) VALUES(?,?,?,?,?,?,?,?,?,?,?)`, settlementID, userID, game.FishingID, game.FishingVersion, "committed", 2_500_000, payout,
		createdAt+120, createdAt, settledAt, settledAt); err != nil {
		t.Fatalf("insert HTTP leaderboard settlement: %v", err)
	}
	if _, err := store.DB().Exec(`INSERT INTO game_rounds(
		id,user_id,game_type,game_version,settlement_id,start_key_hash,start_request_hash,event_seq,created_at,settled_at,updated_at
	) VALUES(?,?,?,?,?,?,?,?,?,?,?)`, roundID, userID, game.FishingID, game.FishingVersion, settlementID, hash, hash, 2, createdAt, settledAt, settledAt); err != nil {
		t.Fatalf("insert HTTP leaderboard round: %v", err)
	}
	if _, err := store.DB().Exec(`INSERT INTO game_fishing_outcomes(round_id,bait,species_key,tier,size_cm) VALUES(?,?,?,?,?)`,
		roundID, "worm", "koi", "legend", 100); err != nil {
		t.Fatalf("insert HTTP leaderboard outcome: %v", err)
	}
}

func TestFishingHTTPLeaderboardExactWirePrivacyAndFilters(t *testing.T) {
	env := newGameHTTPEnv(t)
	nowUnix := time.Now().Unix()
	users := make([]*db.User, 0, 25)
	users = append(users, env.user)
	for index := 1; index < 25; index++ {
		user, err := env.store.CreateUser(fmt.Sprintf("http-leader-%02d", index), fmt.Sprintf("http-leader-%02d", index), "")
		if err != nil {
			t.Fatal(err)
		}
		users = append(users, user)
	}
	public := users[1]
	if _, err := env.store.DB().Exec(`UPDATE users SET game_profile_public=1,level=4,guild_nick=?,avatar='a_globalhash',guild_avatar_url=? WHERE id=?`,
		"Current Guild Nick", fmt.Sprintf("https://cdn.discordapp.com/guilds/guild-1/users/%s/avatars/guildhash.gif?size=64", public.DiscordID), public.ID); err != nil {
		t.Fatal(err)
	}
	for index, user := range users {
		size, caughtAt := 101, nowUnix
		if index == 0 {
			size, caughtAt = 100, nowUnix+1
		}
		if _, err := env.store.DB().Exec(`INSERT INTO game_fishing_best(user_id,round_id,species_key,tier,size_cm,caught_at) VALUES(?,?,?,?,?,?)`,
			user.ID, nil, "koi", "legend", size, caughtAt); err != nil {
			t.Fatal(err)
		}
		insertHTTPLeaderboardRound(t, env.store, user.ID, index, nowUnix, func() int64 {
			if index == 0 {
				return 1
			}
			return 100
		}())
	}
	admin, err := env.store.EnsureAdminUser("http-leader-admin")
	if err != nil {
		t.Fatal(err)
	}
	banned, err := env.store.CreateUser("http-leader-banned", "banned", "")
	if err != nil {
		t.Fatal(err)
	}
	deleted, err := env.store.CreateUser("http-leader-deleted", "deleted", "")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := env.store.DB().Exec(`UPDATE users SET is_banned=1,banned_until=? WHERE id=?`, nowUnix+3600, banned.ID); err != nil {
		t.Fatal(err)
	}
	for index, user := range []*db.User{admin, banned, deleted} {
		if _, err := env.store.DB().Exec(`INSERT INTO game_fishing_best(user_id,round_id,species_key,tier,size_cm,caught_at) VALUES(?,?,?,?,?,?)`,
			user.ID, nil, "koi", "legend", 200, nowUnix-int64(index)); err != nil {
			t.Fatal(err)
		}
		insertHTTPLeaderboardRound(t, env.store, user.ID, 30+index, nowUnix, 10_000)
	}
	if err := env.store.DeleteUserAccount(context.Background(), deleted.ID); err != nil {
		t.Fatal(err)
	}

	request := func(target string) *httptest.ResponseRecorder {
		return gameHTTPDo(t, env.userMount, gameHTTPUserRequest(t, env, env.user.ID, http.MethodGet, target, nil))
	}
	rec := request("https://example.com/api/games/fishing/leaderboard?board=single")
	if rec.Code != http.StatusOK {
		t.Fatalf("single leaderboard status=%d body=%s", rec.Code, rec.Body.String())
	}
	object := decodeJSONObject(t, rec.Body.Bytes())
	assertJSONKeys(t, object, "board", "window_start", "entries", "me")
	if got := responseStringField(t, object, "board"); got != "single" {
		t.Fatalf("single board=%q", got)
	}
	if string(object["window_start"]) != "null" {
		t.Fatalf("single window_start=%s", object["window_start"])
	}
	var entries []map[string]json.RawMessage
	if err := json.Unmarshal(object["entries"], &entries); err != nil {
		t.Fatal(err)
	}
	if len(entries) != 20 {
		t.Fatalf("single entries=%d", len(entries))
	}
	assertJSONKeys(t, entries[0], "rank", "species_key", "size_cm", "display_name", "avatar_url", "level4_badge", "is_me")
	if responseStringField(t, entries[0], "display_name") != "Current Guild Nick" ||
		responseStringField(t, entries[0], "avatar_url") != "https://cdn.discordapp.com/guilds/guild-1/users/"+public.DiscordID+"/avatars/guildhash.png?size=64" ||
		!responseBoolField(t, entries[0], "level4_badge") {
		t.Fatalf("public entry=%v", entries[0])
	}
	assertJSONKeys(t, entries[1], "rank", "species_key", "size_cm", "is_me")
	if responseBoolField(t, entries[1], "is_me") {
		t.Fatal("non-owner entry marked is_me")
	}
	var mine map[string]json.RawMessage
	if err := json.Unmarshal(object["me"], &mine); err != nil {
		t.Fatal(err)
	}
	assertJSONKeys(t, mine, "rank", "species_key", "size_cm", "is_me")
	if !responseBoolField(t, mine, "is_me") || string(mine["rank"]) != "25" {
		t.Fatalf("mine=%v", mine)
	}
	for _, entry := range entries {
		if string(entry["display_name"]) == `"banned"` || string(entry["display_name"]) == `"deleted"` {
			t.Fatalf("ineligible identity exposed: %v", entry)
		}
	}

	if _, err := env.store.DB().Exec(`UPDATE users SET game_profile_public=0 WHERE id=?`, public.ID); err != nil {
		t.Fatal(err)
	}
	rec = request("https://example.com/api/games/fishing/leaderboard?board=single")
	object = decodeJSONObject(t, rec.Body.Bytes())
	entries = nil
	if err := json.Unmarshal(object["entries"], &entries); err != nil {
		t.Fatal(err)
	}
	assertJSONKeys(t, entries[0], "rank", "species_key", "size_cm", "is_me")

	if _, err := env.store.DB().Exec(`UPDATE users SET game_profile_public=1 WHERE id=?`, public.ID); err != nil {
		t.Fatal(err)
	}
	rec = request("https://example.com/api/games/fishing/leaderboard?board=total")
	if rec.Code != http.StatusOK {
		t.Fatalf("total leaderboard status=%d body=%s", rec.Code, rec.Body.String())
	}
	object = decodeJSONObject(t, rec.Body.Bytes())
	assertJSONKeys(t, object, "board", "window_start", "entries", "me")
	if got := responseStringField(t, object, "board"); got != "total" {
		t.Fatalf("total board=%q", got)
	}
	if string(object["window_start"]) == "null" {
		t.Fatal("total window_start omitted")
	}
	entries = nil
	if err := json.Unmarshal(object["entries"], &entries); err != nil {
		t.Fatal(err)
	}
	if len(entries) != 20 {
		t.Fatalf("total entries=%d", len(entries))
	}
	assertJSONKeys(t, entries[0], "rank", "total_credits", "display_name", "avatar_url", "level4_badge", "is_me")
	if responseStringField(t, entries[0], "display_name") != "Current Guild Nick" || !responseBoolField(t, entries[0], "level4_badge") {
		t.Fatalf("total public entry=%v", entries[0])
	}
	mine = nil
	if err := json.Unmarshal(object["me"], &mine); err != nil {
		t.Fatal(err)
	}
	assertJSONKeys(t, mine, "rank", "total_credits", "is_me")
	if !responseBoolField(t, mine, "is_me") || string(mine["rank"]) != "25" {
		t.Fatalf("total mine=%v", mine)
	}
}

func TestFishingHTTPWireLifecycleAndAuthBoundaries(t *testing.T) {
	env := newGameHTTPEnv(t)
	user := env.userMount
	admin := env.adminMount

	// A session for one station cannot cross either station boundary.
	rec := gameHTTPDo(t, user, gameHTTPRequest(http.MethodGet, "https://example.com/api/games", host.StationAdmin, nil))
	assertGameError(t, rec, http.StatusForbidden, "forbidden")
	adminToken, _, err := env.store.CreateAdminSession(env.admin.ID)
	if err != nil {
		t.Fatal(err)
	}
	adminCookieRequest := gameHTTPRequest(http.MethodGet, "https://example.com/api/games", host.StationUser, nil)
	adminCookieRequest.AddCookie(&http.Cookie{Name: auth.AdminSessionCookieName, Value: adminToken})
	rec = gameHTTPDo(t, user, adminCookieRequest)
	assertGameError(t, rec, http.StatusUnauthorized, "unauthorized")
	rec = gameHTTPDo(t, admin, gameHTTPRequest(http.MethodGet, "https://example.com/admin/api/games/config", host.StationUser, nil))
	assertGameError(t, rec, http.StatusForbidden, "forbidden")
	userToken, _, err := env.store.CreateUserSession(env.user.ID)
	if err != nil {
		t.Fatal(err)
	}
	userCookieRequest := gameHTTPRequest(http.MethodGet, "https://example.com/admin/api/games/config", host.StationAdmin, nil)
	userCookieRequest.AddCookie(&http.Cookie{Name: auth.UserSessionCookieName, Value: userToken})
	rec = gameHTTPDo(t, admin, userCookieRequest)
	assertGameError(t, rec, http.StatusUnauthorized, "unauthorized")

	start := func(target, key, body string, userID int64) *httptest.ResponseRecorder {
		r := gameHTTPUserRequest(t, env, userID, http.MethodPost, target, strings.NewReader(body))
		r.Header.Add("Idempotency-Key", key)
		return gameHTTPDo(t, user, r)
	}

	rec = start("https://example.com/api/games/fishing/rounds?ignored=1", "query-start", `{"bait":"worm"}`, env.user.ID)
	assertGameError(t, rec, http.StatusBadRequest, "invalid_request")
	rec = start("https://example.com/api/games/fishing/rounds", "wire-one", `{"bait":"worm"}`, env.user.ID)
	if rec.Code != http.StatusOK {
		t.Fatalf("start status=%d body=%s", rec.Code, rec.Body.String())
	}
	startObject := decodeJSONObject(t, rec.Body.Bytes())
	assertJSONKeys(t, startObject, "round_id", "game_id", "game_version", "bait", "price", "credits", "state", "created_at", "auto_settle_at", "idempotent_replay")
	if got := responseStringField(t, startObject, "state"); got != "pending" {
		t.Fatalf("start state=%q want pending", got)
	}
	if responseBoolField(t, startObject, "idempotent_replay") {
		t.Fatal("first start marked as replay")
	}
	roundID := responseStringField(t, startObject, "round_id")

	rec = start("https://example.com/api/games/fishing/rounds", "wire-one", `{"bait":"worm"}`, env.user.ID)
	if rec.Code != http.StatusOK || !responseBoolField(t, decodeJSONObject(t, rec.Body.Bytes()), "idempotent_replay") {
		t.Fatalf("pending replay status=%d body=%s", rec.Code, rec.Body.String())
	}
	rec = start("https://example.com/api/games/fishing/rounds", "wire-one", `{"bait":"lure"}`, env.user.ID)
	assertGameError(t, rec, http.StatusConflict, "conflict")

	rec = gameHTTPDo(t, user, gameHTTPUserRequest(t, env, env.user.ID, http.MethodGet, "https://example.com/api/games/fishing/state", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("pending state status=%d body=%s", rec.Code, rec.Body.String())
	}
	stateObject := decodeJSONObject(t, rec.Body.Bytes())
	assertJSONKeys(t, stateObject, "pending_round", "unrevealed_result", "has_more_unrevealed")
	var pending map[string]json.RawMessage
	if err := json.Unmarshal(stateObject["pending_round"], &pending); err != nil {
		t.Fatalf("pending round: %v", err)
	}
	assertJSONKeys(t, pending, "round_id", "bait", "price", "created_at", "auto_settle_at")

	rec = gameHTTPDo(t, user, gameHTTPUserRequest(t, env, env.user.ID, http.MethodPost,
		"https://example.com/api/games/fishing/rounds/"+roundID+"/settle?ignored=1", nil))
	assertGameError(t, rec, http.StatusBadRequest, "invalid_request")
	rec = gameHTTPDo(t, user, gameHTTPUserRequest(t, env, env.user.ID, http.MethodPost,
		"https://example.com/api/games/fishing/rounds/"+roundID+"/settle", strings.NewReader(" \n")))
	assertGameError(t, rec, http.StatusBadRequest, "invalid_request")
	rec = gameHTTPDo(t, user, gameHTTPUserRequest(t, env, env.user.ID, http.MethodPost,
		"https://example.com/api/games/fishing/rounds/"+roundID+"/settle", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("settle status=%d body=%s", rec.Code, rec.Body.String())
	}
	settleObject := decodeJSONObject(t, rec.Body.Bytes())
	assertJSONKeys(t, settleObject, "round_id", "game_id", "game_version", "bait", "price", "species_key", "tier", "size_cm", "is_junk", "is_treasure", "meter", "credits_won", "credits", "settled_at", "idempotent_replay")
	if responseBoolField(t, settleObject, "idempotent_replay") {
		t.Fatal("first settle marked as replay")
	}

	rec = gameHTTPDo(t, user, gameHTTPUserRequest(t, env, env.user.ID, http.MethodGet, "https://example.com/api/games/fishing/state", nil))
	stateObject = decodeJSONObject(t, rec.Body.Bytes())
	var unrevealed map[string]json.RawMessage
	if err := json.Unmarshal(stateObject["unrevealed_result"], &unrevealed); err != nil {
		t.Fatalf("unrevealed result: %v", err)
	}
	assertJSONKeys(t, unrevealed, "round_id", "game_id", "game_version", "bait", "price", "species_key", "tier", "size_cm", "is_junk", "is_treasure", "meter", "credits_won", "credits", "settled_at")
	if _, exists := unrevealed["idempotent_replay"]; exists {
		t.Fatal("state unrevealed result exposed idempotent_replay")
	}

	rec = gameHTTPDo(t, user, gameHTTPUserRequest(t, env, env.user.ID, http.MethodPost,
		"https://example.com/api/games/fishing/rounds/"+roundID+"/ack?ignored=1", nil))
	assertGameError(t, rec, http.StatusBadRequest, "invalid_request")
	rec = gameHTTPDo(t, user, gameHTTPUserRequest(t, env, env.user.ID, http.MethodPost,
		"https://example.com/api/games/fishing/rounds/"+roundID+"/ack", strings.NewReader("\t")))
	assertGameError(t, rec, http.StatusBadRequest, "invalid_request")
	rec = gameHTTPDo(t, user, gameHTTPUserRequest(t, env, env.user.ID, http.MethodPost,
		"https://example.com/api/games/fishing/rounds/"+roundID+"/ack", nil))
	if rec.Code != http.StatusNoContent || rec.Body.Len() != 0 {
		t.Fatalf("ack status=%d body=%q", rec.Code, rec.Body.String())
	}
	rec = gameHTTPDo(t, user, gameHTTPUserRequest(t, env, env.user.ID, http.MethodPost,
		"https://example.com/api/games/fishing/rounds/"+roundID+"/ack", nil))
	if rec.Code != http.StatusNoContent {
		t.Fatalf("ack replay status=%d body=%s", rec.Code, rec.Body.String())
	}
	rec = gameHTTPDo(t, user, gameHTTPUserRequest(t, env, env.user.ID, http.MethodGet, "https://example.com/api/games/fishing/state", nil))
	stateObject = decodeJSONObject(t, rec.Body.Bytes())
	if string(stateObject["pending_round"]) != "null" || string(stateObject["unrevealed_result"]) != "null" || responseBoolField(t, stateObject, "has_more_unrevealed") {
		t.Fatalf("acknowledged state = %s", rec.Body.String())
	}

	rec = start("https://example.com/api/games/fishing/rounds", "wire-one", `{"bait":"worm"}`, env.user.ID)
	startObject = decodeJSONObject(t, rec.Body.Bytes())
	if responseStringField(t, startObject, "state") != "committed" || !responseBoolField(t, startObject, "idempotent_replay") {
		t.Fatalf("terminal start replay = %s", rec.Body.String())
	}
	rec = gameHTTPDo(t, user, gameHTTPUserRequest(t, env, env.user.ID, http.MethodPost,
		"https://example.com/api/games/fishing/rounds/"+roundID+"/settle", nil))
	settleObject = decodeJSONObject(t, rec.Body.Bytes())
	if !responseBoolField(t, settleObject, "idempotent_replay") {
		t.Fatalf("terminal settle replay = %s", rec.Body.String())
	}

	otherSettle := gameHTTPDo(t, user, gameHTTPUserRequest(t, env, env.other.ID, http.MethodPost,
		"https://example.com/api/games/fishing/rounds/"+roundID+"/settle", nil))
	unknownSettle := gameHTTPDo(t, user, gameHTTPUserRequest(t, env, env.other.ID, http.MethodPost,
		"https://example.com/api/games/fishing/rounds/gst_"+strings.Repeat("a", 26)+"/settle", nil))
	if otherSettle.Code != http.StatusNotFound || unknownSettle.Code != http.StatusNotFound || otherSettle.Body.String() != unknownSettle.Body.String() {
		t.Fatalf("wrong-owner/not-found mismatch: wrong=%s unknown=%s", otherSettle.Body.String(), unknownSettle.Body.String())
	}
}

func TestFishingHTTPStrictBodyAndAdminConfigConcurrency(t *testing.T) {
	env := newGameHTTPEnv(t)
	user := env.userMount
	start := func(key, body string) *httptest.ResponseRecorder {
		r := gameHTTPUserRequest(t, env, env.user.ID, http.MethodPost, "https://example.com/api/games/fishing/rounds", strings.NewReader(body))
		r.Header.Set("Idempotency-Key", key)
		return gameHTTPDo(t, user, r)
	}
	for index, body := range []string{
		`{"bait":"worm","bait":"lure"}`,
		`{"bait":"worm"}{"bait":"worm"}`,
		`{"bait":"worm","unknown":true}`,
	} {
		rec := start(fmt.Sprintf("strict-%d", index), body)
		assertGameError(t, rec, http.StatusBadRequest, "invalid_request")
	}
	deep := `{"bait":` + strings.Repeat("[", maxGameJSONDepth+1) + `"worm"` + strings.Repeat("]", maxGameJSONDepth+1) + `}`
	assertGameError(t, start("strict-deep", deep), http.StatusBadRequest, "invalid_request")
	var fields strings.Builder
	fields.WriteString(`{"bait":"worm"`)
	for i := 0; i <= maxGameJSONFields; i++ {
		fmt.Fprintf(&fields, `,"x%d":0`, i)
	}
	fields.WriteByte('}')
	assertGameError(t, start("strict-fields", fields.String()), http.StatusBadRequest, "invalid_request")
	assertGameError(t, start("strict-large", `{"bait":"`+strings.Repeat("a", maxGameJSONBodyBytes)+`"}`), http.StatusBadRequest, "invalid_request")

	getConfig := func() map[string]json.RawMessage {
		r := gameHTTPAdminRequest(t, env, http.MethodGet, "https://example.com/admin/api/games/config", nil)
		rec := gameHTTPDo(t, env.adminMount, r)
		if rec.Code != http.StatusOK {
			t.Fatalf("config GET status=%d body=%s", rec.Code, rec.Body.String())
		}
		return decodeJSONObject(t, rec.Body.Bytes())
	}
	before := getConfig()
	var beforeFishing map[string]json.RawMessage
	if err := json.Unmarshal(before["fishing"], &beforeFishing); err != nil {
		t.Fatal(err)
	}
	invalid := gameHTTPAdminRequest(t, env, http.MethodPatch, "https://example.com/admin/api/games/config", strings.NewReader(`{"fishing":{"bait_prices":{"worm":"3000000"},"rtp_percent":{"standard":0}}}`))
	if rec := gameHTTPDo(t, env.adminMount, invalid); rec.Code != http.StatusBadRequest {
		t.Fatalf("invalid combination status=%d body=%s", rec.Code, rec.Body.String())
	}
	afterInvalid := getConfig()
	var afterInvalidFishing map[string]json.RawMessage
	if err := json.Unmarshal(afterInvalid["fishing"], &afterInvalidFishing); err != nil {
		t.Fatal(err)
	}
	if string(afterInvalidFishing["bait_prices"]) != string(beforeFishing["bait_prices"]) || string(afterInvalidFishing["rtp_percent"]) != string(beforeFishing["rtp_percent"]) {
		t.Fatalf("invalid combination partially wrote config: before=%s after=%s", string(before["fishing"]), string(afterInvalid["fishing"]))
	}

	var wg sync.WaitGroup
	errs := make(chan error, 2)
	for _, body := range []string{
		`{"fishing":{"rtp_percent":{"standard":91}}}`,
		`{"fishing":{"rtp_percent":{"premium":87}}}`,
	} {
		body := body
		wg.Add(1)
		go func() {
			defer wg.Done()
			token, _, err := env.store.CreateAdminSession(env.admin.ID)
			if err != nil {
				errs <- err
				return
			}
			r := gameHTTPRequest(http.MethodPatch, "https://example.com/admin/api/games/config", host.StationAdmin, strings.NewReader(body))
			r.AddCookie(&http.Cookie{Name: auth.AdminSessionCookieName, Value: token})
			rec := httptest.NewRecorder()
			env.adminMount.ServeHTTP(rec, r)
			if rec.Code != http.StatusOK {
				errs <- fmt.Errorf("concurrent patch status=%d body=%s", rec.Code, rec.Body.String())
			}
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	final := getConfig()
	var finalFishing map[string]json.RawMessage
	if err := json.Unmarshal(final["fishing"], &finalFishing); err != nil {
		t.Fatal(err)
	}
	var finalRTP map[string]json.RawMessage
	if err := json.Unmarshal(finalFishing["rtp_percent"], &finalRTP); err != nil {
		t.Fatal(err)
	}
	if string(finalRTP["standard"]) != "91" || string(finalRTP["premium"]) != "87" {
		t.Fatalf("concurrent partial patch lost a field: %s", string(finalFishing["rtp_percent"]))
	}
}
