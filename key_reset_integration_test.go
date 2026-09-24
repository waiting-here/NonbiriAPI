package main

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/waiting-here/NonbiriAPI/internal/db"
)

func TestFailureResetRegisteredRoutesUseCurrentSessions(t *testing.T) {
	f := newGameWireFixture(t)
	exec := func(query string, args ...any) int64 {
		t.Helper()
		result, err := f.store.DB().Exec(query, args...)
		if err != nil {
			t.Fatal(err)
		}
		id, err := result.LastInsertId()
		if err != nil {
			t.Fatal(err)
		}
		return id
	}
	exec(`UPDATE site_config SET value='1' WHERE key IN ('donation_accept_enabled','charity_enabled')`)
	endpoint := exec(`INSERT INTO endpoints(user_id,connector_type,base_url,note,enabled,revision,created_at,updated_at)
 VALUES(?,'openai-compatible','https://reset.example.test/v1','',1,1,?,?)`, f.userID, f.now, f.now)
	secret := exec(`INSERT INTO endpoint_key_secrets(context_id,canonical_base_url,connector_type,encrypted_secret,created_at)
 VALUES(randomblob(16),'https://reset.example.test/v1','openai-compatible','synthetic-envelope',?)`, f.now)
	physical := exec(`INSERT INTO endpoint_keys(endpoint_id,secret_ref_id,secret_fingerprint,display_head,display_tail,note,enabled,force_store_false,revision,created_at,updated_at)
 VALUES(?,?,randomblob(32),'head','tail','',1,0,1,?,?)`, endpoint, secret, f.now, f.now)
	headers := func(host, key string) map[string]string {
		return map[string]string{"Content-Type": "application/json", "Origin": "http://" + host, "Idempotency-Key": strings.Repeat(key, 22)}
	}
	created := testApplicationRequest(t, f.app.handler, "POST", auditUserHost, "/api/donations",
		fmt.Sprintf(`{"description":"Reset route fixture","keys":[{"endpoint_key_id":"%d","expires_at":null}],"ownership_authorized":true,"discord_public_thanks":false}`, physical),
		f.cookies, headers(auditUserHost, "C"))
	var d struct {
		ID, Revision string
		Keys         []struct{ ID string }
	}
	if created.Code != 201 || json.Unmarshal(created.Body.Bytes(), &d) != nil || len(d.Keys) != 1 {
		t.Fatalf("create: %d %s", created.Code, created.Body)
	}
	key := d.Keys[0].ID
	approved := testApplicationRequest(t, f.app.handler, "POST", auditAdminHost, "/admin/api/donations/"+d.ID+"/review",
		fmt.Sprintf(`{"decision":"approve","expected_revision":%q,"reason":"Approved","key_settings":[{"donation_key_id":%q,"enabled":true,"price_limit":null,"calls_limit":null,"tokens_limit":null,"token_reserve":0,"safe_note":"","expires_at":null}]}`, d.Revision, key),
		f.adminCookies, headers(auditAdminHost, "A"))
	if approved.Code != 200 {
		t.Fatalf("approval: %d %s", approved.Code, approved.Body)
	}
	exec(`UPDATE donation_keys SET failure_disabled=1,failure_streak=? WHERE id=?`, db.EncodeU128(db.U128{15: 9}), key)
	path := "/api/donations/" + d.ID + "/keys/" + key + "/failure-streak-reset"
	rejectedOrigin := testApplicationRequest(t, f.app.handler, "POST", auditUserHost, path, `{"expected_revision":"2"}`, f.cookies, map[string]string{"Content-Type": "application/json", "Origin": "https://foreign.example.test", "Idempotency-Key": strings.Repeat("R", 22)})
	if rejectedOrigin.Code != 403 {
		t.Fatalf("origin: %d %s", rejectedOrigin.Code, rejectedOrigin.Body)
	}
	owner := testApplicationRequest(t, f.app.handler, "POST", auditUserHost, path, `{"expected_revision":"2"}`, f.cookies, headers(auditUserHost, "R"))
	if owner.Code != 200 || !strings.Contains(owner.Body.String(), `"revision":"3"`) {
		t.Fatalf("owner reset: %d %s", owner.Code, owner.Body)
	}
	selection := `{"selection":{"view":"donations","status":"approved"},"cursor":null}`
	stewardPath := "/api/steward/donation-keys/failure-streak-reset"
	denied := testApplicationRequest(t, f.app.handler, "POST", auditUserHost, stewardPath+"/selection", selection, f.cookies, headers(auditUserHost, "S"))
	if denied.Code != 403 {
		t.Fatalf("ordinary selection: %d %s", denied.Code, denied.Body)
	}
	exec(`UPDATE users SET level=6 WHERE id=?`, f.userID)
	selected := testApplicationRequest(t, f.app.handler, "POST", auditUserHost, stewardPath+"/selection", selection, f.cookies, headers(auditUserHost, "S"))
	if selected.Code != 200 || !strings.Contains(selected.Body.String(), `"expected_revision":"3"`) {
		t.Fatalf("steward selection: %d %s", selected.Code, selected.Body)
	}
	batch := fmt.Sprintf(`{"items":[{"donation_id":%q,"key_id":%q,"expected_revision":"3"}]}`, d.ID, key)
	reset := testApplicationRequest(t, f.app.handler, "POST", auditUserHost, stewardPath, batch, f.cookies, headers(auditUserHost, "B"))
	if reset.Code != 200 || !strings.Contains(reset.Body.String(), `"status":"reset"`) {
		t.Fatalf("steward reset: %d %s", reset.Code, reset.Body)
	}
	exec(`UPDATE users SET level=4 WHERE id=?`, f.userID)
	replay := testApplicationRequest(t, f.app.handler, "POST", auditUserHost, stewardPath, batch, f.cookies, headers(auditUserHost, "B"))
	if replay.Code != 403 {
		t.Fatalf("demoted replay: %d %s", replay.Code, replay.Body)
	}
	adminPath := "/admin/api/donation-keys/failure-streak-reset"
	selected = testApplicationRequest(t, f.app.handler, "POST", auditAdminHost, adminPath+"/selection", selection, f.adminCookies, headers(auditAdminHost, "S"))
	if selected.Code != 200 || !strings.Contains(selected.Body.String(), `"expected_revision":"4"`) {
		t.Fatalf("admin selection: %d %s", selected.Code, selected.Body)
	}
	batch = strings.Replace(batch, `"expected_revision":"3"`, `"expected_revision":"4"`, 1)
	reset = testApplicationRequest(t, f.app.handler, "POST", auditAdminHost, adminPath, batch, f.adminCookies, headers(auditAdminHost, "B"))
	if reset.Code != 200 || !strings.Contains(reset.Body.String(), `"revision":"5"`) {
		t.Fatalf("admin reset: %d %s", reset.Code, reset.Body)
	}
	var audits int
	if err := f.store.DB().QueryRow(`SELECT COUNT(*) FROM donation_reviews WHERE donation_id=? AND action='failure_streak_reset'`, d.ID).Scan(&audits); err != nil {
		t.Fatal(err)
	}
	if audits != 3 {
		t.Fatalf("reset audit count: %d", audits)
	}
	// A batch reset is a session interface; a CallerKey cannot authorize it.
	denied = testApplicationRequest(t, f.app.handler, "POST", auditUserHost, stewardPath, batch, nil, map[string]string{"Authorization": "Bearer invalid-synthetic-caller", "Idempotency-Key": strings.Repeat("K", 22)})
	if denied.Code != 401 {
		t.Fatalf("caller credential: %d %s", denied.Code, denied.Body)
	}
}
