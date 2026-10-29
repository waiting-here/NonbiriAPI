package adminusers

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/waiting-here/NonbiriAPI/internal/authz"
)

func TestConcurrencyDefaultsClearingOverrideChecksCurrentStewardCeiling(t *testing.T) {
	f, manager := newStewardUsersFixture(t)
	actor := f.seedUser("concurrency-steward", false)
	target := f.seedUser("concurrency-member", false)
	if _, err := f.store.DB().Exec("UPDATE users SET level=5 WHERE id=?", actor); err != nil {
		t.Fatal(err)
	}
	policy := authz.DefaultStewardPermissions(5)
	policy.Limits["users.concurrency_limit_max"] = "10"
	body, err := json.Marshal(map[string]any{"expected_revision": "0", "permissions": policy.Permissions, "limits": policy.Limits, "donation_scope": policy.DonationScope})
	if err != nil {
		t.Fatal(err)
	}
	put := stewardUserRequest(t, f, manager, actor, "PUT", routeStewardPermissions, "", string(body), strings.Repeat("P", 22))
	if put.Code != 200 {
		t.Fatalf("permission setup=%d: %s", put.Code, put.Body)
	}
	patch := func(fields, key string, status int) AdminUser {
		t.Helper()
		revision := f.revision(target)
		body := fmt.Sprintf(`{"mode":"profile","expected_revision":%q,%s}`, revision, fields)
		response := stewardUserRequest(t, f, actor, target, "PATCH", routeUser, "", body, strings.Repeat(key, 22))
		if response.Code != status {
			t.Fatalf("profile=%d want=%d: %s", response.Code, status, response.Body)
		}
		var user AdminUser
		if status == 200 {
			if err := json.Unmarshal(response.Body.Bytes(), &user); err != nil {
				t.Fatal(err)
			}
		} else if f.revision(target) != revision {
			t.Fatal("denied clearing changed target")
		}
		return user
	}
	// The absent heavy default fits the ceiling, while the light default does not.
	user := patch(`"concurrency_limit":null,"light_concurrency_limit":"8"`, "A", 200)
	if user.EffectiveConcurrencyLimit != "5" || user.EffectiveLightConcurrencyLimit != "8" {
		t.Fatalf("initial defaults: %+v", user)
	}
	patch(`"light_concurrency_limit":null`, "B", 403)
	if _, err := f.store.DB().Exec("INSERT INTO site_config(key,value,updated_at) VALUES('default_concurrency_per_user','11',0),('default_light_concurrency_per_user','12',0)"); err != nil {
		t.Fatal(err)
	}
	user = patch(`"concurrency_limit":"8","light_concurrency_limit":"9"`, "C", 200)
	if user.EffectiveConcurrencyLimit != "8" || user.EffectiveLightConcurrencyLimit != "9" {
		t.Fatalf("explicit overrides: %+v", user)
	}
	patch(`"concurrency_limit":null`, "D", 403)
	patch(`"light_concurrency_limit":null`, "E", 403)
	if _, err := f.store.DB().Exec("UPDATE site_config SET value='10' WHERE key IN ('default_concurrency_per_user','default_light_concurrency_per_user')"); err != nil {
		t.Fatal(err)
	}
	user = patch(`"concurrency_limit":null,"light_concurrency_limit":null`, "F", 200)
	if user.ConcurrencyLimit != nil || user.LightConcurrencyLimit != nil || user.EffectiveConcurrencyLimit != "10" || user.EffectiveLightConcurrencyLimit != "10" {
		t.Fatalf("current defaults at ceiling: %+v", user)
	}
	if len(f.invalidator.users) != 0 {
		t.Fatal("limit edits revoked active requests")
	}
}
