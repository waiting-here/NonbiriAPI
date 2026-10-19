package app

import (
	"encoding/json"
	"fmt"
	"github.com/waiting-here/NonbiriAPI/internal/stewardautomation"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestPricingAndHistoryRegisteredSessionRoutes(t *testing.T) {
	f := newGameWireFixture(t)
	if len(stewardautomation.RouteMethods("/api/steward/automation/charity-price-templates")) != 0 {
		t.Fatal("new template route inherited automation authority")
	}
	exec := func(query string, args ...any) int64 {
		t.Helper()
		r, err := f.store.DB().Exec(query, args...)
		if err != nil {
			t.Fatal(err)
		}
		id, _ := r.LastInsertId()
		return id
	}
	call := func(method, path, body, key string, admin bool) *httptest.ResponseRecorder {
		t.Helper()
		station, cookies := auditUserHost, f.cookies
		if admin {
			station, cookies = auditAdminHost, f.adminCookies
		}
		headers := map[string]string{"Content-Type": "application/json", "Origin": "http://" + station}
		if key != "" {
			headers["Idempotency-Key"] = key
		}
		return testApplicationRequest(t, f.app.handler, method, station, path, body, cookies, headers)
	}
	require := func(w *httptest.ResponseRecorder, status int) {
		t.Helper()
		if w.Code != status {
			t.Fatalf("status=%d want=%d body=%s", w.Code, status, w.Body)
		}
	}
	body := `{"name":"Synthetic template","description":"","enabled":true,"pricing":{"mode":"per_request","user_price":"0","donor_reward":"0","long_context":null,"context_penalty":null},"token_reserve_credits":null,"discount":{"enabled":false,"percent":0,"start_at":null,"end_at":null}}`
	made := call("POST", "/admin/api/charity-price-templates", body, strings.Repeat("T", 22), true)
	require(made, 201)
	var template struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(made.Body.Bytes(), &template); err != nil {
		t.Fatal(err)
	}
	require(call("POST", "/admin/api/charity-price-templates", body, strings.Repeat("T", 22), true), 201)
	exec("UPDATE users SET level=5 WHERE id=?", f.userID)
	require(call("GET", "/api/steward/charity-price-templates", "", "", false), 403)
	require(call("GET", "/api/steward/charity-price-templates/"+template.ID, "", "", false), 403)
	exec("UPDATE users SET level=6 WHERE id=?", f.userID)
	require(call("GET", "/api/steward/charity-price-templates", "", "", false), 200)
	require(call("GET", "/api/steward/charity-price-templates?state=all", "", "", false), 403)
	denied := call("POST", "/api/steward/charity-price-templates", body, strings.Repeat("S", 22), false)
	if denied.Code < 400 {
		t.Fatal("steward created template", denied.Code)
	}
	// The private history route uses the same real session and CSRF guard.
	id := exec(`INSERT INTO donations(user_id,status,revision,description,created_at,updated_at,terminal_at) VALUES(?,'rejected',1,'history',?,?,?)`, f.userID, f.now, f.now, f.now)
	exec(`INSERT INTO donation_handling(donation_id,state,revision,created_at,updated_at) VALUES(?,'legacy',1,?,?)`, id, f.now, f.now)
	path := fmt.Sprintf("/api/donations/%d/history", id)
	key := strings.Repeat("H", 22)
	req := testApplicationRequest(t, f.app.handler, "PUT", auditUserHost, path, `{"hidden":true}`, f.cookies, map[string]string{"Content-Type": "application/json", "Origin": "http://foreign.example", "Idempotency-Key": key})
	if req.Code != 403 {
		t.Fatal("CSRF", req.Code, req.Body)
	}
	saved := call("PUT", path, `{"hidden":true}`, key, false)
	require(saved, 200)
	var receipt struct {
		ID string `json:"donation_id"`
		At *int64 `json:"owner_hidden_at"`
	}
	if err := json.Unmarshal(saved.Body.Bytes(), &receipt); err != nil || receipt.ID != fmt.Sprint(id) || receipt.At == nil {
		t.Fatal(receipt, err)
	}
	hidden := call("GET", "/api/donations?view=hidden&page=1&page_size=20", "", "", false)
	require(hidden, 200)
	if !strings.Contains(hidden.Body.String(), `"owner_hidden_at":`+fmt.Sprint(f.now)) {
		t.Fatal(hidden.Body)
	}
	detail := call("GET", fmt.Sprintf("/api/donations/%d", id), "", "", false)
	require(detail, 200)
	if !strings.Contains(detail.Body.String(), `"owner_hidden_at":`+fmt.Sprint(f.now)) {
		t.Fatal(detail.Body)
	}
	current := call("GET", "/api/donations?view=current&page=1&page_size=20", "", "", false)
	require(current, 200)
	if !strings.Contains(current.Body.String(), `"total_items":"0"`) {
		t.Fatal(current.Body)
	}
	restore := call("POST", "/api/donations/history/batch", `{"action":"restore","status":null,"q":"","cursor":null}`, strings.Repeat("R", 22), false)
	require(restore, 200)
	if !strings.Contains(restore.Body.String(), `"changed_count":"1"`) {
		t.Fatal(restore.Body)
	}
	management := call("GET", "/admin/api/donations?page=1&page_size=20", "", "", true)
	require(management, 200)
	if strings.Contains(management.Body.String(), "owner_hidden_at") {
		t.Fatal("private preference entered management projection")
	}
	exec("DELETE FROM sessions WHERE user_id IN (SELECT id FROM users WHERE is_admin=1)")
	revokedAdmin := call("POST", "/admin/api/charity-price-templates", body, strings.Repeat("T", 22), true)
	if revokedAdmin.Code != 401 && revokedAdmin.Code != 403 {
		t.Fatal("revoked admin replay", revokedAdmin.Code)
	}
	exec("DELETE FROM sessions WHERE user_id=?", f.userID)
	revoked := call("PUT", path, `{"hidden":true}`, key, false)
	if revoked.Code != 401 && revoked.Code != 403 {
		t.Fatal("revoked replay", revoked.Code, revoked.Body)
	}
}
