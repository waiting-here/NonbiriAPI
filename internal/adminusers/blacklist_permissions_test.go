package adminusers

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/waiting-here/NonbiriAPI/internal/authz"
)

func TestBlacklistGrantRequiresItsAccountBanPermissions(t *testing.T) {
	f, actor := newStewardUsersFixture(t)
	target := f.seedUser("blacklist-target", false)
	const discordID = "123456789012345678"
	if _, err := f.store.DB().Exec(`UPDATE users SET discord_id=? WHERE id=?`, discordID, target); err != nil {
		t.Fatal(err)
	}
	if _, err := f.store.DB().Exec(`UPDATE users SET level=5 WHERE id=?`, actor); err != nil {
		t.Fatal(err)
	}
	p := authz.DefaultStewardPermissions(5)
	p.Permissions["blacklist.add"] = true
	writePolicy := func() {
		t.Helper()
		permissions, _ := json.Marshal(p.Permissions)
		limits, _ := json.Marshal(p.Limits)
		if _, err := f.store.DB().Exec(`INSERT INTO steward_permissions(user_id,revision,permissions,limits,created_at,updated_at) VALUES(?,1,?,?,?,?) ON CONFLICT(user_id) DO UPDATE SET permissions=excluded.permissions,revision=steward_permissions.revision+1`, actor, string(permissions), string(limits), adminUsersTestNow, adminUsersTestNow); err != nil {
			t.Fatal(err)
		}
	}
	call := func(key string, want int) {
		t.Helper()
		got := stewardUserRequest(t, f, actor, 0, "POST", routeBlacklist, "", fmt.Sprintf(`{"discord_id":%q,"reason":"reviewed restriction"}`, discordID), strings.Repeat(key, 22))
		if got.Code != want || want == 403 && !strings.Contains(got.Body.String(), "steward_permission_denied") {
			t.Fatalf("blacklist: %d want %d %s", got.Code, want, got.Body)
		}
	}
	for _, grant := range []string{"users.ban", "users.ban_permanent"} {
		p.Permissions[grant] = false
		writePolicy()
		call("D", 403)
		p.Permissions[grant] = true
	}
	writePolicy()
	call("A", 204)
	call("A", 204)
	call("B", 403)
	p.Permissions["users.unban"] = true
	writePolicy()
	call("B", 204)
}
