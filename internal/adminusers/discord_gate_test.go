package adminusers

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"

	"github.com/waiting-here/NonbiriAPI/internal/authz"
)

func TestDiscordGatePolicyAdminProfileCASReplayAndAuthority(t *testing.T) {
	f := newAdminUsersFixture(t)
	userID := f.seedUser("gate-owner", false)
	f.addSession(userID, "existing-session")
	for i, policy := range []string{"exempt", "require", "inherit"} {
		body := fmt.Sprintf(`{"mode":"profile","expected_revision":%q,"discord_gate_policy":%q}`, f.revision(userID), policy)
		key := strings.Repeat(string(rune('a'+i)), 22)
		got := f.request(http.MethodPatch, routeUser, "https://admin.example/admin/api/users/1", body, userID, key)
		var user AdminUser
		if err := json.Unmarshal(got.Body.Bytes(), &user); err != nil || got.Code != http.StatusOK || user.DiscordGatePolicy != policy {
			t.Fatal("admin policy mutation", got.Code, got.Body, err)
		}
		replay := f.request(http.MethodPatch, routeUser, "https://admin.example/admin/api/users/1", body, userID, key)
		if replay.Code != http.StatusOK || !bytes.Equal(got.Body.Bytes(), replay.Body.Bytes()) {
			t.Fatal("replay changed policy result", replay.Code, replay.Body)
		}
		changedBody := strings.Replace(body, `"discord_gate_policy":"`+policy+`"`, `"discord_gate_policy":"`+map[string]string{"exempt": "require", "require": "inherit", "inherit": "exempt"}[policy]+`"`, 1)
		if mismatch := f.request(http.MethodPatch, routeUser, "https://admin.example/admin/api/users/1", changedBody, userID, key); mismatch.Code != http.StatusConflict {
			t.Fatal("policy missing from idempotency digest", mismatch.Code)
		}
	}
	for i, value := range []string{`""`, `"allow"`, `null`, `true`, `1`} {
		body := fmt.Sprintf(`{"mode":"profile","expected_revision":%q,"discord_gate_policy":%s}`, f.revision(userID), value)
		if got := f.request(http.MethodPatch, routeUser, "https://admin.example/admin/api/users/1", body, userID, strings.Repeat(string(rune('j'+i)), 22)); got.Code != http.StatusBadRequest {
			t.Fatal("invalid policy accepted", value, got.Code)
		}
	}
	f.auth.err = authz.ErrForbidden
	body := fmt.Sprintf(`{"mode":"profile","expected_revision":%q,"discord_gate_policy":"exempt"}`, f.revision(userID))
	if got := f.request(http.MethodPatch, routeUser, "https://admin.example/admin/api/users/1", body, userID, strings.Repeat("z", 22)); got.Code != http.StatusForbidden {
		t.Fatal("missing final admin authorization", got.Code)
	}
	var sessions int
	if err := f.store.DB().QueryRow("SELECT count(*) FROM sessions WHERE user_id=?", userID).Scan(&sessions); err != nil || sessions != 1 || len(f.invalidator.users) != 0 {
		t.Fatal("policy changed existing session authority", sessions, err)
	}
}

func TestDiscordGatePolicyRejectsStewardMutation(t *testing.T) {
	f, actor := newStewardUsersFixture(t)
	userID := f.seedUser("gate-target", false)
	for i, policy := range []string{"inherit", "require", "exempt"} {
		body := fmt.Sprintf(`{"mode":"profile","expected_revision":%q,"discord_gate_policy":%q}`, f.revision(userID), policy)
		got := stewardUserRequest(t, f, actor, userID, http.MethodPatch, routeUser, "", body, strings.Repeat(string(rune('a'+i)), 22))
		if got.Code != http.StatusForbidden {
			t.Fatal("steward changed login gate", got.Code, got.Body)
		}
	}
	var policy string
	if err := f.store.DB().QueryRow("SELECT discord_gate_policy FROM users WHERE id=?", userID).Scan(&policy); err != nil || policy != "inherit" {
		t.Fatal("denied mutation changed policy", policy, err)
	}
}
