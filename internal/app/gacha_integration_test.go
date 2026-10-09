package app

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"

	"github.com/waiting-here/NonbiriAPI/internal/elevation"
	"github.com/waiting-here/NonbiriAPI/internal/gacha"
	"github.com/waiting-here/NonbiriAPI/internal/lifecycle"
)

func TestGachaRootSessionCSRFReplayExportAndDeletion(t *testing.T) {
	f := newActivityWireFixture(t)
	if _, err := f.store.DB().Exec("INSERT INTO site_config(key,value,updated_at) VALUES('site_timezone_offset_minutes','480',0) ON CONFLICT(key) DO UPDATE SET value='480'"); err != nil {
		t.Fatal(err)
	}
	user := f.users[0]
	request := func(key, body string) *http.Request {
		r, _ := http.NewRequest("POST", "http://"+f.cfg.UserHost+"/api/gacha/actions", strings.NewReader(body))
		r.Host = f.cfg.UserHost
		r.Header.Set("Origin", "http://"+f.cfg.UserHost)
		r.Header.Set("Content-Type", "application/json")
		r.Header.Set("Idempotency-Key", key)
		r.AddCookie(user.Cookie)
		return r
	}
	call := func(key, body string) gacha.Outcome {
		t.Helper()
		key += strings.Repeat("x", max(0, 22-len(key)))
		r := testApplicationRequest(t, f.app.Load().handler, "POST", f.cfg.UserHost, "/api/gacha/actions", body, []*http.Cookie{user.Cookie}, map[string]string{
			"Origin": "http://" + f.cfg.UserHost, "Content-Type": "application/json", "Idempotency-Key": key,
		})
		if r.Code != http.StatusOK {
			t.Fatalf("action failed: %d %s", r.Code, r.Body.String())
		}
		var out gacha.Outcome
		if err := json.Unmarshal(r.Body.Bytes(), &out); err != nil {
			t.Fatal(err)
		}
		return out
	}
	out := call("enter-0123456789abcdef", `{"action":"enter"}`)
	if out.Snapshot.Tickets["permanent"] != 10 || len(out.Snapshot.Pools) != 1 {
		t.Fatal(out)
	}
	out = call("draw-0123456789abcdef", `{"action":"draw","pool_id":"permanent","quantity":10}`)
	firstID := out.Draw.ID
	foundSSR := false
	for _, item := range out.Draw.Items {
		if strings.HasPrefix(item.CardID, "g-") {
			foundSSR = true
		}
	}
	if !foundSSR || out.Snapshot.Newcomer || out.Snapshot.Tickets["permanent"] != 0 {
		t.Fatal(out)
	}
	replay := call("draw-0123456789abcdef", `{"action":"draw","pool_id":"permanent","quantity":10}`)
	if replay.Draw.ID != firstID || replay.Snapshot.Tickets["permanent"] != 0 {
		t.Fatal("retry charged or rerolled")
	}
	other := f.call("GET", "/api/gacha/state", nil, f.users[1].Cookie, false)
	if len(other["owned"].(map[string]any)) != 0 {
		t.Fatal("cross-user collection")
	}
	for _, body := range []string{`{"action":"enter","user_id":1}`, `{"action":"draw","action":"enter"}`, `{"action":"buy","ticket":"permanent","quantity":null,"revision":1}`, `{"action":"enter"}{}`} {
		r := testApplicationRequest(t, f.app.Load().handler, "POST", f.cfg.UserHost, "/api/gacha/actions", body, []*http.Cookie{user.Cookie}, map[string]string{
			"Origin": "http://" + f.cfg.UserHost, "Content-Type": "application/json", "Idempotency-Key": "invalid-0123456789abcdef",
		})
		if r.Code != http.StatusBadRequest {
			t.Fatalf("ambiguous body admitted: %d", r.Code)
		}
	}
	r := request("csrf-0123456789abcdef", `{"action":"check-in"}`)
	r.Header.Set("Origin", "https://untrusted.example")
	rejected := httptest.NewRecorder()
	f.app.Load().handler.ServeHTTP(rejected, r)
	if rejected.Code != http.StatusForbidden {
		t.Fatalf("csrf accepted: %d", rejected.Code)
	}
	if res := f.request("GET", "/admin/api/gacha", nil, user.Cookie, true); res.Code != http.StatusUnauthorized && res.Code != http.StatusForbidden {
		t.Fatalf("user reached admin config: %d", res.Code)
	}
	call("purchase-0123456789", `{"action":"buy","ticket":"permanent","quantity":1,"revision":1}`)
	userID, err := strconv.ParseInt(user.ID, 10, 64)
	if err != nil {
		t.Fatal(err)
	}
	binding := sha256.Sum256([]byte(user.Cookie.Value))
	lifecycleCall := func(path, body string) []byte {
		t.Helper()
		token, _, err := f.app.Load().authRuntime.ElevationManager().IssueBound(userID, elevation.KindUser, hex.EncodeToString(binding[:]))
		if err != nil {
			t.Fatal(err)
		}
		result := testApplicationRequest(t, f.app.Load().handler, "POST", f.cfg.UserHost, path, body, []*http.Cookie{user.Cookie}, map[string]string{
			"Content-Type": "application/json", "Origin": "http://" + f.cfg.UserHost, "X-Elevated-Token": token,
		})
		if result.Code != http.StatusOK && result.Code != http.StatusNoContent {
			t.Fatalf("lifecycle: %d %s", result.Code, result.Body.String())
		}
		return result.Body.Bytes()
	}
	var document lifecycle.ExportDocument
	if err = json.Unmarshal(lifecycleCall("/api/account/export", ""), &document); err != nil {
		t.Fatal(err)
	}
	var exported gacha.UserExport
	if err = json.Unmarshal(document.Gacha, &exported); err != nil || len(exported.Draws) != 1 || len(exported.Operations) != 3 || exported.Balance[0] != 1 || len(exported.Owned) == 0 {
		t.Fatal(exported, err)
	}
	if strings.Contains(string(document.Gacha), "request_hash") || strings.Contains(string(document.Gacha), "key_hash") {
		t.Fatal("export leaked recovery hashes")
	}
	lifecycleCall("/api/account/delete", `{"confirm":"DELETE"}`)
	for _, table := range []string{"gacha_profiles", "gacha_pity", "gacha_owned", "gacha_claims", "gacha_draws"} {
		var count int
		if err = f.store.DB().QueryRow("SELECT count(*) FROM "+table+" WHERE user_id=?", userID).Scan(&count); err != nil || count != 0 {
			t.Fatal(table, count, err)
		}
	}
	var count int
	if err = f.store.DB().QueryRow("SELECT count(*) FROM gacha_operations WHERE user_id IS NULL AND key_hash IS NULL AND request_hash IS NULL").Scan(&count); err != nil || count != 3 {
		t.Fatal(count, err)
	}
	if late := f.request("POST", "/api/gacha/actions", map[string]string{"action": "enter"}, user.Cookie, false); late.Code != http.StatusUnauthorized {
		t.Fatal("deleted account admitted", late.Code)
	}
}
