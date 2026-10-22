package app

import (
	"bytes"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/waiting-here/NonbiriAPI/internal/auth"
	"github.com/waiting-here/NonbiriAPI/internal/db"
)

func TestManagedCallerKeyRootBoundariesAndLifecycle(t *testing.T) {
	f := newGameWireFixture(t)
	base := fmt.Sprintf("/admin/api/users/%d/caller-key", f.userID)
	body := `{"expected_generation":"1"}`
	key := strings.Repeat("K", 22)
	headers := func(host, key string) map[string]string {
		return map[string]string{"Origin": "http://" + host, "Content-Type": "application/json", "Idempotency-Key": key}
	}
	call := func(method, host, path, body string, cookies []*http.Cookie, hdr map[string]string, want int) *httptest.ResponseRecorder {
		t.Helper()
		got := testApplicationRequest(t, f.app.handler, method, host, path, body, cookies, hdr)
		if got.Code != want {
			t.Fatalf("%s %s: %d want %d: %s", method, path, got.Code, want, got.Body)
		}
		return got
	}
	oldKey := "nbk_" + base64.RawURLEncoding.EncodeToString(bytes.Repeat([]byte{0x4a}, 32))
	caller := func(secret string, want int) {
		t.Helper()
		call("GET", auditUserHost, "/v1/models", "", nil, map[string]string{"Authorization": "Bearer " + secret}, want)
	}
	caller(oldKey, 200)
	call("GET", auditAdminHost, base, "", nil, nil, 401)
	call("GET", auditAdminHost, base, "", f.cookies, nil, 401)
	call("GET", auditAdminHost, base, "", nil, map[string]string{"Authorization": "Bearer " + oldKey}, 401)
	wrongOrigin := headers(auditAdminHost, key)
	wrongOrigin["Origin"] = "https://foreign.example.test"
	call("POST", auditAdminHost, base+"/revoke", body, f.adminCookies, wrongOrigin, 403)
	call("GET", auditAdminHost, base, "", f.adminCookies, nil, 200)
	first := call("POST", auditAdminHost, base+"/revoke", body, f.adminCookies, headers(auditAdminHost, key), 200)
	if first.Body.String() != `{"generation":"2","revoked":true}` {
		t.Fatal("unexpected revocation receipt", first.Body)
	}
	caller(oldKey, 401)
	// Existing sessions remain usable and can immediately create a replacement.
	call("GET", auditUserHost, "/api/caller-key", "", f.cookies, nil, 200)
	created := call("POST", auditUserHost, "/api/caller-key/regenerate", `{"expected_generation":"2"}`, f.cookies, headers(auditUserHost, strings.Repeat("G", 22)), 200)
	var replacement struct {
		Secret string `json:"secret"`
	}
	if err := json.Unmarshal(created.Body.Bytes(), &replacement); err != nil || replacement.Secret == "" {
		t.Fatal("missing replacement", err)
	}
	caller(replacement.Secret, 200)
	replay := call("POST", auditAdminHost, base+"/revoke", body, f.adminCookies, headers(auditAdminHost, key), 200)
	if !bytes.Equal(replay.Body.Bytes(), first.Body.Bytes()) {
		t.Fatal("lost-response replay changed receipt", replay.Body)
	}
	call("POST", auditAdminHost, base+"/revoke", body, f.adminCookies, headers(auditAdminHost, strings.Repeat("O", 22)), 409)
	caller(replacement.Secret, 200)
	call("GET", auditUserHost, "/api/caller-key", "", f.cookies, nil, 200)

	// Real regeneration and revocation compete for the same generation.
	start := make(chan struct{})
	type outcome struct {
		kind     string
		response *httptest.ResponseRecorder
	}
	results := make(chan outcome, 2)
	go func() {
		<-start
		results <- outcome{"regenerate", testApplicationRequest(t, f.app.handler, "POST", auditUserHost, "/api/caller-key/regenerate", `{"expected_generation":"3"}`, f.cookies, headers(auditUserHost, strings.Repeat("C", 22)))}
	}()
	go func() {
		<-start
		results <- outcome{"revoke", testApplicationRequest(t, f.app.handler, "POST", auditAdminHost, base+"/revoke", `{"expected_generation":"3"}`, f.adminCookies, headers(auditAdminHost, strings.Repeat("D", 22)))}
	}()
	close(start)
	winners, conflicts := 0, 0
	for range 2 {
		got := <-results
		switch got.response.Code {
		case 200:
			winners++
			if got.kind == "regenerate" {
				if err := json.Unmarshal(got.response.Body.Bytes(), &replacement); err != nil {
					t.Fatal(err)
				}
				caller(replacement.Secret, 200)
			}
		case 409:
			conflicts++
		default:
			t.Fatalf("concurrent %s: %d %s", got.kind, got.response.Code, got.response.Body)
		}
	}
	if winners != 1 || conflicts != 1 {
		t.Fatalf("concurrent results: successes=%d conflicts=%d", winners, conflicts)
	}
	var generation int64
	if err := f.store.DB().QueryRow("SELECT generation FROM caller_keys WHERE user_id=?", f.userID).Scan(&generation); err != nil || generation != 4 {
		t.Fatalf("concurrent generation: %d %v", generation, err)
	}
	call("GET", auditUserHost, "/api/caller-key", "", f.cookies, nil, 200)
}

func TestManagedCallerKeyStewardSessionGates(t *testing.T) {
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
	zero, one := db.EncodeU128(db.U128{}), db.EncodeU128(db.U128{15: 1})
	actor := exec(`INSERT INTO users(discord_id,username,level,donation_credit_mag,total_requests,total_uncached_input_tokens,total_cache_write_input_tokens,total_cache_read_input_tokens,total_output_tokens,total_unknown_usage_requests,revision,created_at,updated_at)
VALUES('managed-key-steward','Steward',5,?,?,?,?,?,?,?,?,?,?)`, zero, zero, zero, zero, zero, zero, zero, one, f.now, f.now)
	token := "managed_key_steward_session_0123456789"
	digest := sha256.Sum256([]byte(token))
	exec("INSERT INTO sessions(token_hash,user_id,last_seen_at,expires_at,absolute_expires_at,created_at,cred_gen) VALUES(?,?,?,?,?,?,?)", hex.EncodeToString(digest[:]), actor, f.now, f.now+3600, f.now+3600, f.now, "managed-key-session")
	cookies := []*http.Cookie{{Name: auth.UserSessionCookieName, Value: token}}
	base := fmt.Sprintf("/api/steward/users/%d/caller-key", f.userID)
	body, key := `{"expected_generation":"1"}`, strings.Repeat("S", 22)
	hdr := map[string]string{"Content-Type": "application/json", "Origin": "http://" + auditUserHost, "Idempotency-Key": key}
	call := func(method, path, body string, cookies []*http.Cookie, want int) {
		t.Helper()
		got := testApplicationRequest(t, f.app.handler, method, auditUserHost, path, body, cookies, hdr)
		if got.Code != want {
			t.Fatalf("%s %s: %d want %d: %s", method, path, got.Code, want, got.Body)
		}
	}
	call("GET", base, "", f.cookies, 403)
	call("GET", base, "", nil, 401)
	call("GET", base, "", cookies, 200)
	call("POST", base+"/revoke", body, cookies, 200)
	exec("UPDATE users SET level=5 WHERE id=?", f.userID)
	call("POST", base+"/revoke", body, cookies, 403)
	exec("UPDATE users SET level=6 WHERE id=?", actor)
	call("POST", base+"/revoke", body, cookies, 200)
	exec("UPDATE users SET level=4 WHERE id=?", actor)
	call("POST", base+"/revoke", body, cookies, 403)
	exec("UPDATE users SET level=5 WHERE id=?", actor)
	exec("UPDATE users SET level=4 WHERE id=?", f.userID)
	exec("UPDATE site_config SET value='1' WHERE key='mfa_required_level_5'")
	call("GET", base, "", cookies, 403)
	call("POST", base+"/revoke", body, cookies, 403)
	exec("UPDATE site_config SET value='0' WHERE key='mfa_required_level_5'")
	enabled := testApplicationRequest(t, f.app.handler, "POST", auditAdminHost, "/admin/api/maintenance/enable", `{"expected_revision":"2","reason":"Integration check","confirmation":true}`, f.adminCookies, map[string]string{"Origin": "http://" + auditAdminHost, "Content-Type": "application/json", "Idempotency-Key": strings.Repeat("M", 22)})
	if enabled.Code != 200 {
		t.Fatalf("enable maintenance: %d %s", enabled.Code, enabled.Body)
	}
	call("POST", base+"/revoke", body, cookies, 503)
}
