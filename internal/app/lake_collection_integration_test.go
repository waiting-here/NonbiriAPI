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

	"github.com/waiting-here/NonbiriAPI/internal/db"
	"github.com/waiting-here/NonbiriAPI/internal/elevation"
	"github.com/waiting-here/NonbiriAPI/internal/lakenotes"
	"github.com/waiting-here/NonbiriAPI/internal/lakenotes/rules"
	"github.com/waiting-here/NonbiriAPI/internal/lifecycle"
)

func TestLakeCollectionRootLedgerPromotionReplayAndLifecycle(t *testing.T) {
	f := newActivityWireFixture(t)
	user := f.users[0]
	base := "/api/games/lake-notes"
	config := f.call("GET", "/admin/api/games/config", nil, f.adminCookie, true)
	f.call("PATCH", "/admin/api/games/config", map[string]any{"expected_revision": config["revision"], "master_enabled": true, "lakenotes": map[string]any{"enabled": true}}, f.adminCookie, true)
	var before lakenotes.ProfileView
	r := f.request("GET", base+"/profile", nil, user.Cookie, false)
	if r.Code != http.StatusOK || json.Unmarshal(r.Body.Bytes(), &before) != nil {
		t.Fatal(r.Code, r.Body.String())
	}
	// Existing catches become eligible without requiring another catch.
	var catalog rules.Catalog
	if err := json.Unmarshal(rules.CatalogJSON(), &catalog); err != nil {
		t.Fatal(err)
	}
	p := before.Profile
	for _, fish := range catalog.Fish {
		p.Records[fish.Kind] = rules.Record{Caught: "1", MaxLength: fish.Length[0], PerfectCount: "0"}
	}
	raw, err := rules.EncodeProfile(p)
	if err != nil {
		t.Fatal(err)
	}
	at := f.now().Unix()
	if _, err = f.store.DB().Exec("INSERT INTO lake_notes_profiles(user_id,rules_id,storage_version,revision,profile,coin_mag,updated_at) VALUES(?,?,1,1,?,zeroblob(16),?) ON CONFLICT(user_id) DO UPDATE SET profile=excluded.profile,revision=revision+1", user.ID, rules.RulesID, raw, at); err != nil {
		t.Fatal(err)
	}
	if _, err = f.store.DB().Exec("UPDATE users SET level=NULL,auto_level=1 WHERE id=?", user.ID); err != nil {
		t.Fatal(err)
	}
	if _, err = f.store.DB().Exec("UPDATE site_config SET value=CASE key WHEN 'level_threshold_2_milli' THEN '50000' WHEN 'level_threshold_3_milli' THEN '100000' ELSE '150000' END WHERE key IN ('level_threshold_2_milli','level_threshold_3_milli','level_threshold_4_milli')"); err != nil {
		t.Fatal(err)
	}
	claim := func(location, key, origin string, cookie *http.Cookie) *httptest.ResponseRecorder {
		return testApplicationRequest(t, f.app.Load().handler, "POST", f.cfg.UserHost, base+"/collection-claims", wireBody(t, map[string]string{"location": location}), []*http.Cookie{cookie}, map[string]string{"Content-Type": "application/json", "Origin": origin, "Idempotency-Key": key + strings.Repeat("x", max(0, 22-len(key)))})
	}
	origin := "http://" + f.cfg.UserHost
	if r := claim("lake", "csrf", "https://untrusted.example", user.Cookie); r.Code != http.StatusForbidden {
		t.Fatal(r.Code, r.Body.String())
	}
	if r := claim("lake", "foreign", origin, f.users[1].Cookie); r.Code != http.StatusConflict {
		t.Fatal(r.Code, r.Body.String())
	}
	// Fail after the real ledger callback to prove all writes roll back together.
	if _, err = f.store.DB().Exec("CREATE TRIGGER reject_collection_test BEFORE UPDATE ON lake_notes_profiles WHEN json_extract(CAST(NEW.profile AS TEXT),'$.collectionClaims.lake') IS NOT NULL BEGIN SELECT RAISE(ABORT,'collection test failure'); END"); err != nil {
		t.Fatal(err)
	}
	if r := claim("lake", "first-lake", origin, user.Cookie); r.Code != http.StatusServiceUnavailable {
		t.Fatal(r.Code, r.Body.String())
	}
	var donationRaw []byte
	var operations int
	if err = f.store.DB().QueryRow("SELECT donation_credit_mag,(SELECT count(*) FROM credit_operations WHERE kind='lake_collection_reward') FROM users WHERE id=?", user.ID).Scan(&donationRaw, &operations); err != nil {
		t.Fatal(err)
	}
	donation, _ := db.DecodeU128(donationRaw)
	if donation.Decimal() != "0" || operations != 0 {
		t.Fatal("rolled-back claim leaked", donation.Decimal(), operations)
	}
	if _, err = f.store.DB().Exec("DROP TRIGGER reject_collection_test"); err != nil {
		t.Fatal(err)
	}
	for _, location := range []string{"lake", "river", "coast", "jungle"} {
		key := "first-" + location
		result := claim(location, key, origin, user.Cookie)
		var view lakenotes.ProfileView
		if result.Code != http.StatusOK || json.Unmarshal(result.Body.Bytes(), &view) != nil {
			t.Fatal(location, result.Code, result.Body.String())
		}
		if view.Profile.CollectionClaims[location] != at || view.Wallet != before.Wallet || view.Profile.Coins != before.Profile.Coins {
			t.Fatal("incorrect reward", view)
		}
		if replay := claim(location, key, origin, user.Cookie); replay.Code != http.StatusOK {
			t.Fatal(replay.Code, replay.Body.String())
		}
		if duplicate := claim(location, "again-"+location, origin, user.Cookie); duplicate.Code != http.StatusConflict {
			t.Fatal(duplicate.Code, duplicate.Body.String())
		}
	}
	var level, entries int
	if err = f.store.DB().QueryRow("SELECT donation_credit_mag,auto_level,(SELECT count(*) FROM credit_operations WHERE kind='lake_collection_reward'),(SELECT count(*) FROM credit_entries e JOIN credit_operations o ON o.id=e.operation_id WHERE o.kind='lake_collection_reward') FROM users WHERE id=?", user.ID).Scan(&donationRaw, &level, &operations, &entries); err != nil {
		t.Fatal(err)
	}
	donation, _ = db.DecodeU128(donationRaw)
	if donation.Decimal() != "200000" || level != 4 || operations != 4 || entries != 0 {
		t.Fatal(donation.Decimal(), level, operations, entries)
	}
	uid, _ := strconv.ParseInt(user.ID, 10, 64)
	binding := sha256.Sum256([]byte(user.Cookie.Value))
	elevated := func() map[string]string {
		token, _, e := f.app.Load().authRuntime.ElevationManager().IssueBound(uid, elevation.KindUser, hex.EncodeToString(binding[:]))
		if e != nil {
			t.Fatal(e)
		}
		return map[string]string{"Content-Type": "application/json", "Origin": origin, "X-Elevated-Token": token}
	}
	exported := testApplicationRequest(t, f.app.Load().handler, "POST", f.cfg.UserHost, "/api/account/export", "", []*http.Cookie{user.Cookie}, elevated())
	var document lifecycle.ExportDocument
	if exported.Code != http.StatusOK || json.Unmarshal(exported.Body.Bytes(), &document) != nil {
		t.Fatal(exported.Code, exported.Body.String())
	}
	encoded, _ := json.Marshal(document.LakeNotes.Profile)
	if !strings.Contains(string(encoded), "\"collectionClaims\"") {
		t.Fatal("export omitted claims")
	}
	deleted := testApplicationRequest(t, f.app.Load().handler, "POST", f.cfg.UserHost, "/api/account/delete", wireBody(t, map[string]string{"confirm": "DELETE"}), []*http.Cookie{user.Cookie}, elevated())
	if deleted.Code != http.StatusNoContent {
		t.Fatal(deleted.Code, deleted.Body.String())
	}
	if err = f.store.DB().QueryRow("SELECT count(*) FROM lake_notes_profiles WHERE user_id=?", uid).Scan(&operations); err != nil || operations != 0 {
		t.Fatal(operations, err)
	}
	if late := claim("lake", "late", origin, user.Cookie); late.Code != http.StatusUnauthorized {
		t.Fatal(late.Code, late.Body.String())
	}
}
