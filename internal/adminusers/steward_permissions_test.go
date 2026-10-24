package adminusers

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/waiting-here/NonbiriAPI/internal/authz"
)

func TestPerUserStewardPermissionsDelegationBoundsAndRevokedReplay(t *testing.T) {
	f, manager := newStewardUsersFixture(t)
	actor := f.seedUser("configured-steward", false)
	target := f.seedUser("managed-member", false)
	if _, err := f.store.DB().Exec(`UPDATE users SET level=5 WHERE id=?`, actor); err != nil {
		t.Fatal(err)
	}
	policy := authz.DefaultStewardPermissions(5)
	put := func(who int64, revision string, key string, want int) {
		t.Helper()
		b, _ := json.Marshal(map[string]any{"expected_revision": revision, "permissions": policy.Permissions, "limits": policy.Limits, "donation_scope": policy.DonationScope})
		got := stewardUserRequest(t, f, who, actor, "PUT", routeStewardPermissions, "", string(b), strings.Repeat(key, 22))
		if got.Code != want {
			t.Fatalf("policy PUT=%d want %d: %s", got.Code, want, got.Body)
		}
	}
	policy.Permissions["users.profile"] = true
	policy.Permissions["users.level"] = true
	policy.Permissions["users.balance"] = true
	policy.Limits["users.rpm_limit_max"] = "10"
	put(manager, "0", "A", 200)
	put(actor, "1", "B", 403)
	patch := func(body, key string, want int) {
		t.Helper()
		got := stewardUserRequest(t, f, actor, target, "PATCH", routeUser, "", body, strings.Repeat(key, 22))
		if got.Code != want {
			t.Fatalf("profile=%d want %d: %s", got.Code, want, got.Body)
		}
	}
	patch(`{"mode":"profile","expected_revision":"1","rpm_limit":"11"}`, "C", 403)
	patch(`{"mode":"profile","expected_revision":"1","level":5}`, "D", 403)
	body := `{"mode":"profile","expected_revision":"1","lang":"en","rpm_limit":"10"}`
	patch(body, "E", 200)
	policy.Permissions["users.profile"] = false
	put(manager, "1", "F", 200)
	patch(body, "E", 403)
	put(manager, "0", "G", 409)
	var audits int
	if err := f.store.DB().QueryRow(`SELECT COUNT(*) FROM steward_permission_audits WHERE user_id=?`, actor).Scan(&audits); err != nil || audits != 2 {
		t.Fatal("audit count", audits, err)
	}
	for _, level := range []int{5, 6} {
		if _, err := f.store.DB().Exec(`UPDATE users SET level=? WHERE id=?`, level, target); err != nil {
			t.Fatal(err)
		}
		patch(fmt.Sprintf(`{"mode":"profile","expected_revision":%q,"rpm_limit":"2"}`, f.revision(target)), fmt.Sprint(level), 403)
	}
}
