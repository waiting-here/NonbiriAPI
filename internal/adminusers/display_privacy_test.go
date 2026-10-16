package adminusers

import (
	"bytes"
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
	for _, route := range []string{routeBlacklist, routeBlacklistEvents} {
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
