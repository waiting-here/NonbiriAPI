package adminusers

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
)

func TestUserAndBlacklistDisplaysRedactReasonsWithoutChangingStorage(t *testing.T) {
	f := newAdminUsersFixture(t)
	user := f.seedUser("display-fixture", false)
	const reason = "Source 192.0.2.1"
	if _, err := f.store.DB().Exec(`UPDATE users SET is_banned=1,banned_reason=? WHERE id=?`, reason, user); err != nil {
		t.Fatal(err)
	}
	for _, target := range []struct{ pattern, url string }{
		{routeUser, "/admin/api/users/" + strconv.FormatInt(user, 10)},
		{routeUsers, "/admin/api/users?user_id=" + strconv.FormatInt(user, 10)},
	} {
		w := f.request("GET", target.pattern, target.url, "", user, "")
		if w.Code != 200 || strings.Contains(w.Body.String(), "192.0.2.1") || !strings.Contains(w.Body.String(), "Source [redacted]") {
			t.Fatalf("user display: %d %s", w.Code, w.Body)
		}
	}
	var stored string
	if err := f.store.DB().QueryRow(`SELECT banned_reason FROM users WHERE id=?`, user).Scan(&stored); err != nil || stored != reason {
		t.Fatal("stored ban changed", stored, err)
	}
	const discord = "123456789012345678"
	if w := blacklistRequest(f, true, discord, reason, "AAAAAAAAAAAAAAAAAAAAAA"); w.Code != http.StatusNoContent {
		t.Fatalf("blacklist fixture: %d %s", w.Code, w.Body)
	}
	for _, route := range []string{routeBlacklist} {
		req := httptest.NewRequest("GET", strings.ReplaceAll(route, "{discordID}", discord), nil)
		req.SetPathValue("discordID", discord)
		w := httptest.NewRecorder()
		f.registrar.handler("GET", route)(w, req, AdminPrincipal{UserID: f.adminID})
		if w.Code != 200 || strings.Contains(w.Body.String(), "192.0.2.1") || !strings.Contains(w.Body.String(), "Source [redacted]") {
			t.Fatalf("blacklist display: %d %s", w.Code, w.Body)
		}
	}
	if err := f.store.DB().QueryRow(`SELECT reason FROM discord_blacklist WHERE discord_id=?`, discord).Scan(&stored); err != nil || stored != reason {
		t.Fatal("stored blacklist changed", stored, err)
	}
}

func TestUserMutationDisplayKeepsReceiptAndConfigurationOutputSeparate(t *testing.T) {
	body := []byte(`{"banned_reason":"Source 192.0.2.1","revision":"2"}`)
	original := bytes.Clone(body)
	for range 2 {
		w := httptest.NewRecorder()
		writeDisplayMutation(w, 200, body)
		if w.Code != 200 || strings.Contains(w.Body.String(), "192.0.2.1") || !strings.Contains(w.Body.String(), "[redacted]") {
			t.Fatal(w.Code, w.Body)
		}
	}
	if !bytes.Equal(body, original) {
		t.Fatal("stored receipt changed")
	}
	w := httptest.NewRecorder()
	writeJSON(w, 200, map[string]string{"base_url": "https://192.0.2.1/v1"})
	if !strings.Contains(w.Body.String(), "192.0.2.1") {
		t.Fatal("editable endpoint was redacted")
	}
}

func TestSavedHistoryDisplaysPreserveOriginalReasons(t *testing.T) {
	f := newAdminUsersFixture(t)
	steward := f.seedUser("history-display-steward", false)
	trainee := f.seedUser("history-display-trainee", false)
	if _, err := f.store.DB().Exec(`UPDATE users SET level=6 WHERE id=?`, steward); err != nil {
		t.Fatal(err)
	}
	if _, err := f.store.DB().Exec(`UPDATE users SET level=5 WHERE id=?`, trainee); err != nil {
		t.Fatal(err)
	}
	if err := RegisterStewardRoutes(f.registrar, f.service); err != nil {
		t.Fatal(err)
	}
	const discordID = "123456789012340007"
	reason := "Source 192.0.2.7 person@example.invalid <script> " + strings.Repeat("x", 7000)
	snapshot, err := json.Marshal(map[string]any{"user_id": "100007", "discord_id": discordID, "ban": map[string]any{"state": "active", "reason": reason}})
	if err != nil {
		t.Fatal(err)
	}
	id := seedDeletedAccount(t, f.store.DB(), 100007, discordID, 1, 1, string(snapshot))
	for _, actor := range []int64{f.adminID, steward, trainee} {
		role := roleAdmin
		if actor != f.adminID {
			role = roleSteward
		}
		list := managementRequest(f, actor, "GET", role.route(routeUsers), role.route(routeUsers)+"?account_state=deleted&discord_id="+discordID, "", "")
		req := httptest.NewRequest("GET", role.route(routeUsers)+"/deleted/"+strconv.FormatInt(id, 10), nil)
		req.SetPathValue("id", "deleted")
		req.SetPathValue("recordID", strconv.FormatInt(id, 10))
		detail := httptest.NewRecorder()
		f.registrar.handler("GET", role.route(routeDeletedUser))(detail, req, AdminPrincipal{UserID: actor})
		for _, response := range []*httptest.ResponseRecorder{list, detail} {
			if actor == trainee {
				if response.Code != http.StatusForbidden {
					t.Fatal("L5 read saved history", response.Code, response.Body)
				}
				continue
			}
			if response.Code != 200 || !strings.Contains(response.Body.String(), "192.0.2.7") || strings.Contains(response.Body.String(), "<script>") {
				t.Fatal("incomplete or unsafe history", response.Code, response.Body)
			}
			var value any
			if err := json.Unmarshal(response.Body.Bytes(), &value); err != nil {
				t.Fatal(err)
			}
			encoded, _ := json.Marshal(value)
			quoted, _ := json.Marshal(reason)
			if !bytes.Contains(encoded, quoted) {
				t.Fatal("saved history reason truncated")
			}
		}
	}
	const blacklistReason = "Source 192.0.2.8 <script>"
	if w := blacklistRequest(f, true, discordID, blacklistReason, "AAAAAAAAAAAAAAAAAAAAAA"); w.Code != http.StatusNoContent {
		t.Fatal(w.Code, w.Body)
	}
	req := httptest.NewRequest("GET", "/admin/api/blacklist/"+discordID+"/events", nil)
	req.SetPathValue("discordID", discordID)
	w := httptest.NewRecorder()
	f.registrar.handler("GET", routeBlacklistEvents)(w, req, AdminPrincipal{UserID: f.adminID})
	if w.Code != 200 || !strings.Contains(w.Body.String(), "192.0.2.8") || strings.Contains(w.Body.String(), "<script>") {
		t.Fatal("blacklist audit history changed", w.Code, w.Body)
	}
}
