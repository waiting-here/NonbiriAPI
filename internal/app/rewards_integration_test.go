package app

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"github.com/waiting-here/NonbiriAPI/internal/elevation"
	"github.com/waiting-here/NonbiriAPI/internal/ledger"
	"github.com/waiting-here/NonbiriAPI/internal/lifecycle"
	"github.com/waiting-here/NonbiriAPI/internal/rewards"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
)

func TestRedemptionRootPenaltyInvalidatesTheSession(t *testing.T) {
	f := newActivityWireFixture(t)
	user := f.users[0]
	origin := "http://" + f.cfg.UserHost
	for attempt := range 5 {
		r := testApplicationRequest(t, f.app.Load().handler, "POST", f.cfg.UserHost, "/api/rewards/redeem", `{"code":"UNKNOWN-GIFT"}`, []*http.Cookie{user.Cookie}, map[string]string{
			"Origin": origin, "Content-Type": "application/json", "Idempotency-Key": "root-redemption-invalid-" + strconv.Itoa(attempt),
		})
		var result struct {
			Rejection   string `json:"rejection"`
			BannedUntil int64  `json:"banned_until"`
		}
		if r.Code != http.StatusOK || json.Unmarshal(r.Body.Bytes(), &result) != nil {
			t.Fatal(r.Code, r.Body.String())
		}
		if attempt < 4 && (result.Rejection != "unavailable" || result.BannedUntil != 0) {
			t.Fatal("penalty preceded the default threshold", attempt, result)
		}
		if attempt == 4 && (result.Rejection != "banned" || result.BannedUntil == 0) {
			t.Fatal("default threshold did not ban", result)
		}
	}
	r := testApplicationRequest(t, f.app.Load().handler, "GET", f.cfg.UserHost, "/api/rewards/history", "", []*http.Cookie{user.Cookie}, nil)
	if r.Code != http.StatusUnauthorized {
		t.Fatal("banned session retained access", r.Code)
	}
}

func TestRedemptionRootPenaltyCancelsPaidGameOnce(t *testing.T) {
	f := newDuelWireFixture(t)
	wallets := func() [2][2]string {
		t.Helper()
		tx, err := f.store.DB().BeginTx(t.Context(), nil)
		if err != nil {
			t.Fatal(err)
		}
		defer tx.Rollback()
		var balances [2][2]string
		for seat, user := range f.users {
			for i, asset := range []ledger.Asset{ledger.General, ledger.Game} {
				account, err := ledger.UserAssetAccount(t.Context(), tx, user, asset)
				if err != nil {
					t.Fatal(err)
				}
				balances[seat][i] = account.Balance.Decimal()
			}
		}
		return balances
	}
	before := wallets()
	session := f.matched("bidding", "tier1")
	other := f.read(1, "bidding").Current
	if session.OwnPayment.General != "6" || session.OwnPayment.Game != "4" || other == nil || other.OwnPayment.General != "7" || other.OwnPayment.Game != "3" {
		t.Fatal("fixture did not accept both mixed-asset payments", session.OwnPayment, other)
	}
	paid := wallets()
	assertTerminals := func(want int) {
		t.Helper()
		var count int
		if err := f.store.DB().QueryRow(`SELECT count(*) FROM credit_operations WHERE kind='duel_terminal' AND source_id=?`, session.ID).Scan(&count); err != nil || count != want {
			t.Fatal("terminal accounting", count, want, err)
		}
	}
	assertActive := func() {
		t.Helper()
		current := f.read(1, "bidding").Current
		if current == nil || current.ID != session.ID || wallets() != paid {
			t.Fatal("ordinary redemption or replay changed the accepted game")
		}
		assertTerminals(0)
	}
	f.admin("POST", "/admin/api/rewards/create", json.RawMessage(`{"kind":"code","title":"Active game gift","code":"ACTIVE-GAME-GIFT","enabled":true,"rewards":[{"asset":"gacha:permanent","quantity":"1"}],"conditions":{"op":"all"}}`), http.StatusOK)
	redeem := func(key, code string) *httptest.ResponseRecorder {
		t.Helper()
		return testApplicationRequest(t, f.app.handler, "POST", auditUserHost, "/api/rewards/redeem", wireBody(t, map[string]string{"code": code}), []*http.Cookie{f.userCookies[0]}, map[string]string{
			"Origin": "http://" + auditUserHost, "Content-Type": "application/json", "Idempotency-Key": key,
		})
	}
	valid := redeem("active-game-reward-key-01", "ACTIVE-GAME-GIFT")
	var receipt rewards.Redemption
	if valid.Code != http.StatusOK || json.Unmarshal(valid.Body.Bytes(), &receipt) != nil || receipt.ID == 0 || receipt.AlreadyClaimed {
		t.Fatal("successful redemption", valid.Code, valid.Body.String())
	}
	if replay := redeem("active-game-reward-key-01", "ACTIVE-GAME-GIFT"); replay.Code != valid.Code || replay.Body.String() != valid.Body.String() {
		t.Fatal("successful replay changed", replay.Code, replay.Body.String())
	}
	assertActive()
	for attempt := range 5 {
		key := "active-game-invalid-key-" + strconv.Itoa(attempt)
		r := redeem(key, "UNKNOWN-GIFT")
		var result struct {
			Rejection   string `json:"rejection"`
			BannedUntil int64  `json:"banned_until"`
		}
		if r.Code != http.StatusOK || json.Unmarshal(r.Body.Bytes(), &result) != nil {
			t.Fatal(r.Code, r.Body.String())
		}
		if attempt < 4 {
			if result.Rejection != "unavailable" || result.BannedUntil != 0 {
				t.Fatal("penalty preceded the default threshold", attempt, result)
			}
			if attempt == 0 {
				if replay := redeem(key, "UNKNOWN-GIFT"); replay.Code != r.Code || replay.Body.String() != r.Body.String() {
					t.Fatal("rejected replay changed", replay.Code, replay.Body.String())
				}
			}
			assertActive()
		} else if result.Rejection != "banned" || result.BannedUntil == 0 {
			t.Fatal("default threshold did not ban", result)
		}
	}
	f.assertCancelled("bidding", session.ID, "account_unavailable")
	if refunded := wallets(); refunded != before {
		t.Fatal("cancellation did not return both original assets", refunded, before)
	}
	assertTerminals(1)
	path := "/api/games/bidding/sessions/" + session.ID + "/surrender"
	if late := f.call(0, "POST", path, map[string]string{"phase_seq": session.PhaseSeq}, false); late.Code != http.StatusUnauthorized && late.Code != http.StatusForbidden {
		t.Fatal("banned player retained game access", late.Code, late.Body.String())
	}
	if late := f.call(1, "POST", path, map[string]string{"phase_seq": session.PhaseSeq}, false); late.Code != http.StatusConflict {
		t.Fatal("cancelled game accepted another action", late.Code, late.Body.String())
	}
	if replay := redeem("active-game-invalid-key-4", "UNKNOWN-GIFT"); replay.Code != http.StatusUnauthorized {
		t.Fatal("revoked session replayed a redemption", replay.Code, replay.Body.String())
	}
	assertTerminals(1)
	if wallets() != before {
		t.Fatal("late actions repeated the refund")
	}
	var events, cases int
	if err := f.store.DB().QueryRow(`SELECT count(*) FROM abuse_window_events WHERE user_id=? AND violation_kind='redemption_code'`, f.users[0]).Scan(&events); err != nil || events != 5 {
		t.Fatal("redemption violation count", events, err)
	}
	if err := f.store.DB().QueryRow(`SELECT count(*) FROM abuse_cases WHERE reason_code='redemption_code'`).Scan(&cases); err != nil || cases != 1 {
		t.Fatal("redemption penalty count", cases, err)
	}
	f.checkLedger()
}

func TestRewardsRootAuthorizationReplayExportAndDeletion(t *testing.T) {
	f := newActivityWireFixture(t)
	user := f.users[0]
	requestNumber := 0
	request := func(admin bool, path, body string, cookie *http.Cookie, origin string, keys ...string) *httptest.ResponseRecorder {
		host := f.cfg.UserHost
		requestNumber++
		key := "root-reward-submit-key-" + strconv.Itoa(requestNumber)
		if admin {
			host = f.cfg.AdminHost
			key = "root-reward-create-key-0001"
		}
		if len(keys) > 0 {
			key = keys[0]
		}
		return testApplicationRequest(t, f.app.Load().handler, "POST", host, path, body, []*http.Cookie{cookie}, map[string]string{"Origin": origin, "Content-Type": "application/json", "Idempotency-Key": key})
	}
	body := `{"kind":"code","title":"Gift","code":"ROOT-GIFT","enabled":true,"rewards":[{"asset":"general","quantity":"1.125"}],"conditions":{"op":"all"}}`
	root := "/admin/api/rewards"
	adminOrigin := "http://" + f.cfg.AdminHost
	origin := "http://" + f.cfg.UserHost
	denied := request(true, root+"/create", body, user.Cookie, adminOrigin)
	if denied.Code != 401 && denied.Code != 403 {
		t.Fatal(denied.Code)
	}
	created := request(true, root+"/create", body, f.adminCookie, adminOrigin)
	if created.Code != 200 {
		t.Fatal(created.Code, created.Body.String())
	}
	var campaign rewards.Campaign
	if e := json.Unmarshal(created.Body.Bytes(), &campaign); e != nil {
		t.Fatal(e)
	}
	if e := json.Unmarshal(request(true, root+"/create", body, f.adminCookie, adminOrigin).Body.Bytes(), &campaign); e != nil {
		t.Fatal(e)
	}
	for _, bad := range []string{`{"code":"ROOT-GIFT","user_id":1}`, `{"code":"ROOT-GIFT","code":"OTHER"}`, `{"code":null}`} {
		r := request(false, "/api/rewards/redeem", bad, user.Cookie, origin)
		if r.Code != 400 {
			t.Fatal("invalid body", r.Code)
		}
	}
	if r := request(false, "/api/rewards/redeem", `{"code":"ROOT-GIFT"}`, user.Cookie, "https://untrusted.example"); r.Code != 403 {
		t.Fatal("CSRF accepted", r.Code)
	}
	var receipt rewards.Redemption
	for attempt := range 2 {
		key := "root-reward-valid-key-" + strconv.Itoa(attempt)
		r := request(false, "/api/rewards/redeem", `{"code":"root-gift"}`, user.Cookie, origin, key)
		if r.Code != 200 {
			t.Fatal(r.Code, r.Body.String())
		}
		if e := json.Unmarshal(r.Body.Bytes(), &receipt); e != nil {
			t.Fatal(e)
		}
		if receipt.AlreadyClaimed != (attempt > 0) {
			t.Fatal("redemption status", attempt, receipt.AlreadyClaimed)
		}
		replayed := request(false, "/api/rewards/redeem", `{"code":"root-gift"}`, user.Cookie, origin, key)
		if replayed.Code != r.Code || replayed.Body.String() != r.Body.String() {
			t.Fatal("submission replay changed the response", replayed.Code, replayed.Body.String())
		}
	}
	other := f.call("GET", "/api/rewards/history", nil, f.users[1].Cookie, false)
	if len(other["items"].([]any)) != 0 {
		t.Fatal("cross-user history")
	}
	id, e := strconv.ParseInt(user.ID, 10, 64)
	if e != nil {
		t.Fatal(e)
	}
	binding := sha256.Sum256([]byte(user.Cookie.Value))
	account := func(path, body string) []byte {
		token, _, e := f.app.Load().authRuntime.ElevationManager().IssueBound(id, elevation.KindUser, hex.EncodeToString(binding[:]))
		if e != nil {
			t.Fatal(e)
		}
		r := testApplicationRequest(t, f.app.Load().handler, "POST", f.cfg.UserHost, path, body, []*http.Cookie{user.Cookie}, map[string]string{"Origin": origin, "Content-Type": "application/json", "X-Elevated-Token": token})
		if r.Code != 200 && r.Code != 204 {
			t.Fatal(r.Code, r.Body.String())
		}
		return r.Body.Bytes()
	}
	var exported lifecycle.ExportDocument
	if e = json.Unmarshal(account("/api/account/export", ""), &exported); e != nil {
		t.Fatal(e)
	}
	var receipts []rewards.Receipt
	if e = json.Unmarshal(exported.Rewards, &receipts); e != nil || len(receipts) != 1 || receipts[0].ID != receipt.ID {
		t.Fatal(receipts, e)
	}
	if strings.Contains(string(exported.Rewards), "ROOT-GIFT") || strings.Contains(string(exported.Rewards), "key_hash") || strings.Contains(string(exported.Rewards), "already_claimed") {
		t.Fatal("private configuration exported")
	}
	account("/api/account/delete", `{"confirm":"DELETE"}`)
	var count, claimed int
	if e = f.store.DB().QueryRow("SELECT count(*) FROM reward_claims WHERE user_id=?", id).Scan(&count); e != nil || count != 0 {
		t.Fatal(count, e)
	}
	if e = f.store.DB().QueryRow("SELECT claimed FROM reward_campaigns WHERE id=?", campaign.ID).Scan(&claimed); e != nil || claimed != 1 {
		t.Fatal("quota restored by deletion", claimed, e)
	}
	if r := request(false, "/api/rewards/redeem", `{"code":"ROOT-GIFT"}`, user.Cookie, origin); r.Code != 401 {
		t.Fatal(r.Code)
	}
}
