package app

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"github.com/waiting-here/NonbiriAPI/internal/elevation"
	"github.com/waiting-here/NonbiriAPI/internal/lifecycle"
	"github.com/waiting-here/NonbiriAPI/internal/rewards"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
)

func TestRewardsRootAuthorizationReplayExportAndDeletion(t *testing.T) {
	f := newActivityWireFixture(t)
	user := f.users[0]
	request := func(admin bool, path, body string, cookie *http.Cookie, origin string) *httptest.ResponseRecorder {
		host := f.cfg.UserHost
		if admin {
			host = f.cfg.AdminHost
		}
		return testApplicationRequest(t, f.app.Load().handler, "POST", host, path, body, []*http.Cookie{cookie}, map[string]string{"Origin": origin, "Content-Type": "application/json", "Idempotency-Key": "root-reward-create-key-0001"})
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
		r := request(false, "/api/rewards/redeem", `{"code":"root-gift"}`, user.Cookie, origin)
		if r.Code != 200 {
			t.Fatal(r.Code, r.Body.String())
		}
		if e := json.Unmarshal(r.Body.Bytes(), &receipt); e != nil {
			t.Fatal(e)
		}
		if receipt.AlreadyClaimed != (attempt > 0) {
			t.Fatal("redemption status", attempt, receipt.AlreadyClaimed)
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
