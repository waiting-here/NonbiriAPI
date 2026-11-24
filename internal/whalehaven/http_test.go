package whalehaven

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"testing/iotest"
	"time"

	"github.com/waiting-here/NonbiriAPI/internal/activities"
	"github.com/waiting-here/NonbiriAPI/internal/authz"
	"github.com/waiting-here/NonbiriAPI/internal/db"
	"github.com/waiting-here/NonbiriAPI/internal/dbfixture"
	"github.com/waiting-here/NonbiriAPI/internal/ledger"
	"github.com/waiting-here/NonbiriAPI/internal/resources"
	"github.com/waiting-here/NonbiriAPI/internal/secret"
	"github.com/waiting-here/NonbiriAPI/internal/whalehaven/assets"
	"github.com/waiting-here/NonbiriAPI/internal/whalehaven/config"
)

type stationFixture struct {
	store   *db.Store
	service *Service
	at      time.Time
	denied  bool
	routes  map[string]resources.AuthorizedUserHandler
}

func (f *stationFixture) AuthorizeUserMutation(context.Context, *sql.Tx, int64) error {
	if f.denied {
		return authz.ErrForbidden
	}
	return nil
}
func (f *stationFixture) AuthorizeAdmin(context.Context, *sql.Tx, int64) error        { return nil }
func (f *stationFixture) AuthorizeUserActivity(context.Context, *sql.Tx, int64) error { return nil }
func (f *stationFixture) RegisterUserRoute(method, path string, handler resources.AuthorizedUserHandler) error {
	f.routes[method+" "+path] = handler
	return nil
}
func newStationFixture(t *testing.T) *stationFixture {
	t.Helper()
	v, err := secret.New(make([]byte, secret.MasterKeyBytes))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { v.Close() })
	path := filepath.Join(t.TempDir(), "whale.sqlite")
	dbfixture.Materialize(t, path, v)
	store, err := db.Open(path, v)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { store.Close() })
	f := &stationFixture{store: store, at: time.Unix(1794992400, 0), routes: map[string]resources.AuthorizedUserHandler{}}
	repo, err := activities.NewRepository(activities.RepositoryConfig{Store: store, UserFinalAuth: f, AdminFinalAuth: f, UserGate: f, CursorKeys: v})
	if err != nil {
		t.Fatal(err)
	}
	pools, err := activities.NewService(activities.ServiceConfig{Repository: repo})
	if err != nil {
		t.Fatal(err)
	}
	f.service = New(store, f, pools, func() time.Time { return f.at })
	if err = f.service.Register(f); err != nil {
		t.Fatal(err)
	}
	return f
}
func (f *stationFixture) enable(t *testing.T) {
	t.Helper()
	if err := f.store.SetSiteTimezoneOffsetMinutes(480); err != nil {
		t.Fatal(err)
	}
	if _, err := f.store.DB().Exec("UPDATE site_config SET value='1',updated_at=? WHERE key='games_enabled'", f.at.Unix()); err != nil {
		t.Fatal(err)
	}
	cfg := config.Defaults()
	cfg.Enabled = true
	cfg.ExchangeEnabled = true
	raw, _ := json.Marshal(cfg)
	if _, err := f.store.DB().Exec("INSERT INTO whale_haven_settings(revision,effective_at,document) VALUES(2,?,?)", f.at.UnixMilli(), string(raw)); err != nil {
		t.Fatal(err)
	}
}
func (f *stationFixture) user(t *testing.T, nick string) (id int64) {
	t.Helper()
	ctx := t.Context()
	tx, err := f.store.DB().BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	row, err := tx.Exec(`INSERT INTO users(username,donation_credit_mag,total_requests,total_uncached_input_tokens,total_cache_write_input_tokens,total_cache_read_input_tokens,total_output_tokens,total_unknown_usage_requests,revision,created_at,updated_at)
 VALUES(?,zeroblob(16),zeroblob(16),zeroblob(16),zeroblob(16),zeroblob(16),zeroblob(16),zeroblob(16),zeroblob(16),?,?)`, nick, f.at.Unix(), f.at.Unix())
	if err != nil {
		t.Fatal(err)
	}
	id, err = row.LastInsertId()
	if err != nil {
		t.Fatal(err)
	}
	for _, currency := range []ledger.Asset{ledger.General, ledger.Game} {
		if _, err = ledger.CreateUserAssetAccount(ctx, tx, id, currency, f.at.Unix()); err != nil {
			t.Fatal(err)
		}
	}
	if err = tx.Commit(); err != nil {
		t.Fatal(err)
	}
	return
}
func (f *stationFixture) call(t *testing.T, id int64, route string, input map[string]any, requestKey string, status int) map[string]any {
	t.Helper()
	method := "GET"
	var raw []byte
	if input != nil {
		method = "POST"
		raw, _ = json.Marshal(input)
	}
	req := httptest.NewRequestWithContext(t.Context(), method, "/api/games/whale-haven/"+route, bytes.NewReader(raw))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Idempotency-Key", requestKey)
	rec := httptest.NewRecorder()
	handler := f.routes[method+" "+req.URL.Path]
	if handler == nil {
		t.Fatal("missing route", route)
	}
	handler(rec, req, resources.UserPrincipal{UserID: id})
	if rec.Code != status {
		t.Fatalf("%s: %d want %d: %s", route, rec.Code, status, rec.Body.String())
	}
	var value map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &value); err != nil {
		t.Fatal(err)
	}
	return value
}
func (f *stationFixture) adopt(t *testing.T, id int64, name string) map[string]any {
	return f.call(t, id, "actions", map[string]any{"type": "adopt", "name": name, "open": true, "expected_profile_revision": "1", "expected_settings_revision": "2"}, "adopt-whale-fresh-player-house", 200)
}

type whaleMutationCheck func() error

func (check whaleMutationCheck) AuthorizeUserMutation(context.Context, *sql.Tx, int64) error {
	return check()
}

func TestWhaleHTTPCanceledRequests(t *testing.T) {
	f := newStationFixture(t)
	var logs bytes.Buffer
	previous := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&logs, nil)))
	t.Cleanup(func() { slog.SetDefault(previous) })
	serverFailure := errors.New("server stopped the operation")
	for _, tc := range []struct {
		name                                           string
		cancelRequest, expired, duringBody, noResponse bool
		cause, failure                                 error
	}{
		{name: "client left during request", cancelRequest: true, failure: fmt.Errorf("read: %w", context.Canceled), noResponse: true},
		{name: "client canceled all operations", cancelRequest: true, failure: errors.Join(context.Canceled, fmt.Errorf("close: %w", context.Canceled)), noResponse: true},
		{name: "rollback failed after client canceled", cancelRequest: true, failure: errors.Join(context.Canceled, serverFailure)},
		{name: "client left while sending body", duringBody: true, cancelRequest: true, failure: context.Canceled, noResponse: true},
		{name: "body read deadline expired", duringBody: true, failure: context.DeadlineExceeded},
		{name: "dependency canceled a live request", failure: context.Canceled},
		{name: "service failed as client left", cancelRequest: true, failure: serverFailure},
		{name: "server canceled with a cause", cancelRequest: true, cause: serverFailure, failure: context.Canceled},
		{name: "request deadline expired", expired: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx, cancel := context.WithCancelCause(t.Context())
			defer cancel(nil)
			reqCtx := ctx
			if tc.expired {
				deadline, stop := context.WithDeadline(ctx, time.Now().Add(-time.Second))
				defer stop()
				reqCtx = deadline
			}
			f.service.auth = whaleMutationCheck(func() error {
				if tc.cancelRequest {
					cancel(tc.cause)
				}
				return tc.failure
			})
			const path = "/api/games/whale-haven/actions"
			var body io.Reader = strings.NewReader(`{"type":"recall_home"}`)
			if tc.duringBody {
				if tc.cancelRequest {
					cancel(tc.cause)
				}
				body = iotest.ErrReader(tc.failure)
			}
			req := httptest.NewRequestWithContext(reqCtx, "POST", path, body)
			rec := httptest.NewRecorder()
			rec.Code = 0 // Distinguish returning without writing from sending a header.
			logs.Reset()
			f.routes["POST "+path](rec, req, resources.UserPrincipal{UserID: 1})
			logged := strings.Contains(logs.String(), "Whale Haven request failed")
			if tc.noResponse {
				if rec.Code != 0 || rec.Body.Len() != 0 || logged {
					t.Fatal(rec.Code, rec.Body.String(), logs.String())
				}
			} else if rec.Code != 500 || !strings.Contains(rec.Body.String(), "internal_error") || !logged {
				t.Fatal(rec.Code, rec.Body.String(), logs.String())
			}
		})
	}
}

func TestBowlRefillHonorsConfirmedSpillAfterAutomaticRefill(t *testing.T) {
	f := newStationFixture(t)
	f.enable(t)
	user := f.user(t, "keeper")
	profile := f.adopt(t, user, "Fin")
	if _, err := f.store.DB().Exec(`UPDATE whale_haven_settings SET document=json_set(document,'$.values.trough_grams',250) WHERE revision=2`); err != nil {
		t.Fatal(err)
	}
	if _, err := f.store.DB().Exec(`UPDATE whale_haven_profiles SET snapshot=json_set(snapshot,
		'$.inventory.silver_fish',3,'$.home.buffs.autoRefillEnabled',json('true')) WHERE user_id=?`, user); err != nil {
		t.Fatal(err)
	}
	action := map[string]any{"type": "use_feed", "feedId": "silver_fish", "maxSpillGrams": 0,
		"expected_profile_revision":  strconv.FormatInt(int64(profile["snapshot"].(map[string]any)["revision"].(float64)), 10),
		"expected_settings_revision": profile["settings_revision"]}
	// A refill ticket adds a 180 g serving between the last snapshot and the click.
	f.at = f.at.Add(time.Second)
	f.call(t, user, "actions", action, "fill-bowl-without-any-spill", http.StatusConflict)
	afterRefusal := f.call(t, user, "profile", nil, "", http.StatusOK)["snapshot"].(map[string]any)
	if afterRefusal["inventory"].(map[string]any)["silver_fish"] != 2.0 {
		t.Fatal("an unconfirmed manual refill spent a serving")
	}
	action["maxSpillGrams"] = 110
	for range 2 {
		result := f.call(t, user, "actions", action, "fill-bowl-with-confirmed-spill", http.StatusOK)["snapshot"].(map[string]any)
		if result["inventory"].(map[string]any)["silver_fish"] != 1.0 || result["home"].(map[string]any)["troughGrams"] != 250.0 {
			t.Fatalf("unexpected confirmed refill: %v", result)
		}
	}
}

func TestStationExchangeSharedWalletAndReplay(t *testing.T) {
	f := newStationFixture(t)
	user := f.user(t, "player")
	if v := f.call(t, user, "availability", nil, "", http.StatusOK); v["enabled"] != false {
		t.Fatal(v)
	}
	f.enable(t)
	tx, err := f.store.DB().Begin()
	if err != nil {
		t.Fatal(err)
	}
	if err = assets.MoveTx(t.Context(), tx, user, f.at.UnixMilli(), assets.Balance{Pearls: 80, Shards: 2}, "site_reward", ""); err != nil {
		t.Fatal(err)
	}
	if err = tx.Commit(); err != nil {
		t.Fatal(err)
	}
	f.adopt(t, user, "蓝鲸")
	quote := f.call(t, user, "exchange/quote", map[string]any{"quantity": "1"}, "", 200)
	if quote["max_lots"] != "15" || quote["net_general_milli"] != "4550000" || quote["platform_milli"] != "150000" || quote["thursday_milli"] != "150000" || quote["welfare_milli"] != "150000" {
		t.Fatal(quote)
	}
	body := map[string]any{"quantity": "1", "expected_profile_revision": quote["profile_revision"], "expected_settings_revision": quote["settings_revision"]}
	const token = "exchange-whale-once-for-owner"
	value := f.call(t, user, "exchange", body, token, 200)
	got := value["snapshot"].(map[string]any)["wallet"].(map[string]any)
	if got["generalMilli"] != "4550000" || got["pearls"] != float64(75) || got["conchShards"] != float64(2) || got["dailyPearlExchanged"] != float64(5) {
		t.Fatal(got)
	}
	if f.call(t, user, "exchange", body, token, 200)["operation_id"] != value["operation_id"] {
		t.Fatal("repeat changed operation")
	}
	f.denied = true
	f.call(t, user, "exchange", body, token, 403)
	f.denied = false
	rejected := f.call(t, user, "exchange", body, token+"-stale", 409)
	if !strings.Contains(rejected["error"].(map[string]any)["message"].(string), "小筑状态有变化") {
		t.Fatal(rejected)
	}
	outdatedSettings := map[string]any{"quantity": "1", "expected_profile_revision": quote["profile_revision"], "expected_settings_revision": "1"}
	rejected = f.call(t, user, "exchange", outdatedSettings, token+"-old-rules", 409)
	if !strings.Contains(rejected["error"].(map[string]any)["message"].(string), "运营设置已有调整") {
		t.Fatal(rejected)
	}
	body["quantity"] = "2"
	f.call(t, user, "exchange", body, token, 409)
	var n int
	err = f.store.ReadDB().QueryRow("SELECT count(*) FROM whale_haven_movements WHERE user_id=? AND reason='exchange'", user).Scan(&n)
	if err != nil || n != 1 {
		t.Fatal(n, err)
	}
	// The general balance is insufficient evidence for a feed purchase: only the game wallet pays.
	snapshot := value["snapshot"].(map[string]any)
	buy := map[string]any{"type": "buy_feed", "feedId": "silver_fish", "expected_settings_revision": "2", "expected_profile_revision": strconv.Itoa(int(snapshot["revision"].(float64)))}
	f.call(t, user, "actions", buy, "buy-from-empty-game-wallet", 409)
	profile := f.call(t, user, "profile", nil, "", 200)["snapshot"].(map[string]any)
	if profile["inventory"].(map[string]any)["silver_fish"] != float64(0) {
		t.Fatal(profile["inventory"])
	}
	// Fund only game credits and buy several portions in a single operation.
	tx, err = f.store.DB().BeginTx(t.Context(), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	purse, err := ledger.UserAssetAccount(t.Context(), tx, user, ledger.Game)
	if err != nil {
		t.Fatal(err)
	}
	issuing, err := ledger.CodedAssetAccount(t.Context(), tx, "external", ledger.Game)
	if err != nil {
		t.Fatal(err)
	}
	op, err := db.GenerateOpaqueID("op_")
	if err != nil {
		t.Fatal(err)
	}
	price := config.Defaults().Value("feed_silver_fish_price_milli")
	plan, err := ledger.NewAdminGameAdjustment(ledger.Meta{OperationID: op, ActorUserID: user, CreatedAt: f.at.Unix()}, purse.ID, issuing.ID, ledger.AmountFromMilli(3*price), "fund purchase test")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = ledger.Apply(t.Context(), tx, plan); err != nil {
		t.Fatal(err)
	}
	if err = tx.Commit(); err != nil {
		t.Fatal(err)
	}
	buy["quantity"] = 0
	f.call(t, user, "actions", buy, "zero-portion-game-purchase", 400)
	buy["quantity"] = 3
	bought := f.call(t, user, "actions", buy, "three-portion-game-purchase", 200)["snapshot"].(map[string]any)
	if bought["inventory"].(map[string]any)["silver_fish"] != float64(3) || bought["wallet"].(map[string]any)["gameMilli"] != "0" || bought["wallet"].(map[string]any)["generalMilli"] != "4550000" {
		t.Fatal(bought)
	}

}
func TestStationInteractionAwardsPersistAndRenewAtSiteMidnight(t *testing.T) {
	f := newStationFixture(t)
	// The fixture's site day changes at 16:00 UTC.
	f.at = time.Date(2026, 11, 20, 15, 59, 55, 0, time.UTC)
	f.enable(t)
	user := f.user(t, "keeper")
	other := f.user(t, "neighbor")
	read := func(v map[string]any) map[string]any { return v["snapshot"].(map[string]any) }
	value := read(f.adopt(t, user, "小鲸"))
	f.adopt(t, other, "鲸鲸")
	var body map[string]any
	for i := 1; i <= 4; i++ {
		body = map[string]any{"type": "chat", "expected_profile_revision": strconv.Itoa(int(value["revision"].(float64))), "expected_settings_revision": "2"}
		value = read(f.call(t, user, "actions", body, "award-on-chat-number-"+strconv.Itoa(i), 200))
		if value["interactionAffectionRemaining"] != float64(max(0, 3-i)) || value["whale"].(map[string]any)["affection"] != float64(120+2*min(i, 3)) {
			t.Fatalf("chat %d: %+v", i, value["whale"])
		}
	}
	if v := read(f.call(t, other, "profile", nil, "", 200)); v["interactionAffectionRemaining"] != float64(3) {
		t.Fatal("another player lost an award")
	}
	tx, err := f.store.ReadDB().BeginTx(t.Context(), &sql.TxOptions{ReadOnly: true})
	if err != nil {
		t.Fatal(err)
	}
	exported, err := Export(t.Context(), tx, user, f.at.Unix(), 100)
	_ = tx.Rollback()
	if err != nil {
		t.Fatal(err)
	}
	var saved struct {
		Profile map[string]any `json:"profile"`
	}
	if err := json.Unmarshal(exported, &saved); err != nil || saved.Profile["daily_interaction_affection_remaining"] != float64(0) {
		t.Fatalf("exported interaction allowance = %v, error = %v", saved.Profile["daily_interaction_affection_remaining"], err)
	}
	f.at = f.at.Add(6 * time.Second)
	value = read(f.call(t, user, "actions", body, "award-on-chat-number-4", 200))
	if value["interactionAffectionRemaining"] != float64(3) || value["whale"].(map[string]any)["affection"] != float64(126) {
		t.Fatal("replayed chat at site midnight consumed an award")
	}
	body["expected_profile_revision"] = strconv.Itoa(int(value["revision"].(float64)))
	value = read(f.call(t, user, "actions", body, "new-day-interaction-award", 200))
	if value["interactionAffectionRemaining"] != float64(2) || value["whale"].(map[string]any)["affection"] != float64(128) {
		t.Fatal("first chat on the new site day missing")
	}
}

func TestStationNeighborVisibilityAndGrantBeforeAdoption(t *testing.T) {
	f := newStationFixture(t)
	a := f.user(t, "public-captain")
	b := f.user(t, "never-leak-this-private-name")
	f.enable(t)
	f.adopt(t, a, "海")
	v := f.adopt(t, b, "雾")
	door := v["social"].(map[string]any)["door"].(string)
	for _, q := range []struct {
		search string
		size   int
	}{{"never-leak-this-private-name", 0}, {door, 1}, {"雾", 1}} {
		items := f.call(t, a, "neighbors/search?q="+q.search, nil, "", 200)["items"].([]any)
		if len(items) != q.size {
			t.Fatal(q, items)
		}
		if len(items) > 0 {
			p := items[0].(map[string]any)
			if p["uid"] != nil || p["discordUsername"] != nil || p["discordId"] != nil || p["publicNickname"] != "" {
				t.Fatal(p)
			}
		}
	}
	f.call(t, b, "actions", map[string]any{"type": "set_open", "open": false, "expected_profile_revision": "2", "expected_settings_revision": "2"}, "close-neighbor-whale-home", 200)
	if len(f.call(t, a, "neighbors/search?q="+door, nil, "", 200)["items"].([]any)) != 0 {
		t.Fatal("closed home listed")
	}
	f.call(t, b, "neighbors/search", nil, "", 409)
}
