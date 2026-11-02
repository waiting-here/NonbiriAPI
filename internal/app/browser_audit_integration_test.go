package app

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/waiting-here/NonbiriAPI/internal/auth"
	"github.com/waiting-here/NonbiriAPI/internal/authz"
	"github.com/waiting-here/NonbiriAPI/internal/ledger"
	"github.com/waiting-here/NonbiriAPI/internal/riskaudit"
)

func TestBrowserAuditHTTPCollectionAndPermissionMFA(t *testing.T) {
	f := newEmbeddingHTTPFixture(t)
	now := time.Now().Unix()
	tx, err := f.store.DB().BeginTx(t.Context(), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	for _, user := range []int64{f.userID, f.donorID} {
		if _, err = ledger.CreateUserAssetAccount(t.Context(), tx, user, ledger.Game, now); err != nil {
			t.Fatal(err)
		}
	}
	if err = tx.Commit(); err != nil {
		t.Fatal(err)
	}
	session := func(user int64, token string) *http.Cookie {
		digest := sha256.Sum256([]byte(token))
		f.exec(t, `INSERT INTO sessions(token_hash,user_id,last_seen_at,expires_at,absolute_expires_at,created_at,cred_gen) VALUES(?,?,?,?,?,?,?)`, fmt.Sprintf("%x", digest), user, now, now+3600, now+3600, now, "browser-session")
		return &http.Cookie{Name: auth.UserSessionCookieName, Value: token}
	}
	one := session(f.userID, "browser-one-session")
	two := session(f.donorID, "browser-two-session")
	first := testApplicationRequest(t, f.app.handler, "GET", auditUserHost, "/api/session", "", []*http.Cookie{one}, nil)
	if first.Code != 200 {
		t.Fatal(first.Code, first.Body)
	}
	browser := responseCookieNamed(t, first, auth.BrowserAuditCookieName)
	second := testApplicationRequest(t, f.app.handler, "GET", auditUserHost, "/api/session", "", []*http.Cookie{two, browser}, nil)
	if second.Code != 200 {
		t.Fatal(second.Code, second.Body)
	}
	path := "/api/steward/abuse-audit/shared-browsers?page=1&page_size=10"
	memberPath := fmt.Sprintf("/api/steward/abuse-audit/shared-browsers/%x?page=1&page_size=10", sha256.Sum256([]byte(browser.Value)))
	check := func(want int, code string) {
		t.Helper()
		got := testApplicationRequest(t, f.app.handler, "GET", auditUserHost, path, "", []*http.Cookie{two, browser}, nil)
		if got.Code != want || code != "" && !strings.Contains(got.Body.String(), code) {
			t.Fatalf("audit status %d: %s", got.Code, got.Body)
		}
		members := testApplicationRequest(t, f.app.handler, "GET", auditUserHost, memberPath, "", []*http.Cookie{two, browser}, nil)
		if members.Code != want || code != "" && !strings.Contains(members.Body.String(), code) {
			t.Fatalf("member status %d: %s", members.Code, members.Body)
		}
		if want == 200 {
			if strings.Contains(got.Body.String(), `"associations"`) || strings.Contains(got.Body.String(), `"associations_truncated"`) {
				t.Fatal("summary response included member data", got.Body)
			}
			var page riskaudit.Page[riskaudit.SharedBrowser]
			if json.Unmarshal(got.Body.Bytes(), &page) != nil || page.TotalItems != "1" || len(page.Items) != 1 || page.Items[0].Users != 2 {
				t.Fatal("wrong browser projection", got.Body)
			}
			if strings.Contains(got.Body.String(), browser.Value) {
				t.Fatal("raw browser token leaked")
			}
			var detail riskaudit.SharedBrowserDetail
			if json.Unmarshal(members.Body.Bytes(), &detail) != nil || detail.TotalItems != "2" || len(detail.Associations) != 2 {
				t.Fatal("member projection", members.Body)
			}
		}
	}
	check(403, "")
	for _, level := range []int{5, 6} {
		f.exec(t, "UPDATE users SET level=? WHERE id=?", level, f.donorID)
		policy := authz.DefaultStewardPermissions(level)
		policy.Permissions["risk.read"] = true
		grants, _ := json.Marshal(policy.Permissions)
		limits, _ := json.Marshal(policy.Limits)
		f.exec(t, `INSERT INTO steward_permissions(user_id,revision,permissions,limits,created_at,updated_at) VALUES(?,1,?,?,?,?) ON CONFLICT(user_id) DO UPDATE SET permissions=excluded.permissions,revision=steward_permissions.revision+1`, f.donorID, string(grants), string(limits), now, now)
		check(200, "")
		f.exec(t, "UPDATE site_config SET value='1' WHERE key=?", fmt.Sprintf("mfa_required_level_%d", level))
		check(403, "mfa_required")
		f.exec(t, "UPDATE site_config SET value='0' WHERE key=?", fmt.Sprintf("mfa_required_level_%d", level))
		policy.Permissions["risk.read"] = false
		grants, _ = json.Marshal(policy.Permissions)
		f.exec(t, "UPDATE steward_permissions SET permissions=?,revision=revision+1 WHERE user_id=?", string(grants), f.donorID)
		if level == 5 {
			check(403, "")
		} else {
			check(200, "")
		}
	}
	login := testApplicationRequest(t, f.app.handler, "POST", auditAdminHost, "/admin/api/login", `{"username":"operator","password":"correct horse battery staple"}`, nil, map[string]string{"Content-Type": "application/json"})
	admin := responseCookieNamed(t, login, auth.AdminSessionCookieName)
	for _, cookie := range login.Result().Cookies() {
		if cookie.Name == auth.BrowserAuditCookieName {
			t.Fatal("admin collected")
		}
	}
	adminPage := testApplicationRequest(t, f.app.handler, "GET", auditAdminHost, "/admin/api/abuse-audit/shared-browsers", "", []*http.Cookie{admin}, nil)
	if adminPage.Code != 200 {
		t.Fatal("admin audit", adminPage.Code, adminPage.Body)
	}
	adminMembers := testApplicationRequest(t, f.app.handler, "GET", auditAdminHost, strings.Replace(memberPath, "/api/steward/", "/admin/api/", 1), "", []*http.Cookie{admin}, nil)
	if adminMembers.Code != 200 {
		t.Fatal("admin member audit", adminMembers.Code, adminMembers.Body)
	}
	denied := testApplicationRequest(t, f.app.handler, "GET", auditUserHost, path, "", nil, map[string]string{"Authorization": "Bearer " + f.caller})
	if denied.Code != 401 && denied.Code != 403 {
		t.Fatal("CallerKey accessed browser audit", denied.Code)
	}
	// The inference path does not issue a browser identifier or add associations.
	var before, after int
	if err := f.store.ReadDB().QueryRow("SELECT COUNT(*) FROM browser_associations").Scan(&before); err != nil {
		t.Fatal(err)
	}
	call := testApplicationRequest(t, f.app.handler, "GET", auditUserHost, "/v1/models", "", []*http.Cookie{browser}, map[string]string{"Authorization": "Bearer " + f.caller})
	for _, cookie := range call.Result().Cookies() {
		if cookie.Name == auth.BrowserAuditCookieName {
			t.Fatal("inference issued browser cookie")
		}
	}
	if err := f.store.ReadDB().QueryRow("SELECT COUNT(*) FROM browser_associations").Scan(&after); err != nil || before != after {
		t.Fatal("inference collected browser", before, after, err)
	}
}
