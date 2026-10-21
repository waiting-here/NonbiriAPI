package adminusers

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
)

func TestAdmissionProfileRequiresStrictlyHigherStewardAndRechecksReplay(t *testing.T) {
	for _, actorLevel := range []int{5, 6} {
		f, actor := newStewardUsersFixture(t)
		if _, err := f.store.DB().Exec("UPDATE users SET level=? WHERE id=?", actorLevel, actor); err != nil {
			t.Fatal(err)
		}
		for targetLevel := 1; targetLevel <= 6; targetLevel++ {
			target := f.seedUser(fmt.Sprintf("limit-target-%d", targetLevel), false)
			if _, err := f.store.DB().Exec("UPDATE users SET level=? WHERE id=?", targetLevel, target); err != nil {
				t.Fatal(err)
			}
			body := `{"mode":"profile","expected_revision":"1","rpm_limit":"7","concurrency_limit":"8","light_rpm_limit":"9","light_concurrency_limit":"10","global_rpm_limit":"11"}`
			key := strings.Repeat(fmt.Sprint(targetLevel), 22)
			got := stewardUserRequest(t, f, actor, target, "PATCH", routeUser, "", body, key)
			if targetLevel >= actorLevel {
				if got.Code != 403 || f.revision(target) != "1" {
					t.Fatalf("actor%d target%d: %d %s", actorLevel, targetLevel, got.Code, got.Body)
				}
				continue
			}
			if got.Code != 200 {
				t.Fatalf("actor%d target%d: %d %s", actorLevel, targetLevel, got.Code, got.Body)
			}
			var user AdminUser
			if err := json.Unmarshal(got.Body.Bytes(), &user); err != nil {
				t.Fatal(err)
			}
			if user.EffectiveRPMLimit != "7" || user.EffectiveConcurrencyLimit != "8" || user.EffectiveLightRPMLimit != "9" || user.EffectiveLightConcurrencyLimit != "10" || user.EffectiveGlobalRPMLimit != "11" {
				t.Fatalf("projection: %+v", user)
			}
			if replay := stewardUserRequest(t, f, actor, target, "PATCH", routeUser, "", body, key); replay.Code != 200 || f.revision(target) != "2" {
				t.Fatalf("replay: %d %s", replay.Code, replay.Body)
			}
			if _, err := f.store.DB().Exec("UPDATE users SET level=? WHERE id=?", actorLevel, target); err != nil {
				t.Fatal(err)
			}
			if replay := stewardUserRequest(t, f, actor, target, "PATCH", routeUser, "", body, key); replay.Code != 403 {
				t.Fatalf("promoted target replay: %d", replay.Code)
			}
		}
		for _, target := range []int64{actor, f.adminID} {
			got := stewardUserRequest(t, f, actor, target, "PATCH", routeUser, "", `{"mode":"profile","expected_revision":"1","global_rpm_limit":"20"}`, strings.Repeat("P", 22))
			if got.Code != 403 && got.Code != 404 {
				t.Fatalf("protected target: %d", got.Code)
			}
		}
		if len(f.invalidator.limitUsers) != actorLevel-1 || len(f.invalidator.users) != 0 {
			t.Fatalf("limit notifications repeated or revoked authority: %+v", f.invalidator)
		}
	}
}

func TestAdmissionProfileNullDefaultsAndMixedTraineeFields(t *testing.T) {
	f, actor := newStewardUsersFixture(t)
	target := f.seedUser("null-limits", false)
	if _, err := f.store.DB().Exec("UPDATE users SET level=5 WHERE id=?", actor); err != nil {
		t.Fatal(err)
	}
	if _, err := f.store.DB().Exec("UPDATE site_config SET value='99' WHERE key='default_per_endpoint_concurrency'"); err != nil {
		t.Fatal(err)
	}
	for _, field := range []string{`"lang":"en"`, `"endpoint_limit":"2"`, `"level":4`} {
		body := `{"mode":"profile","expected_revision":"1","light_rpm_limit":"40",` + field + `}`
		got := stewardUserRequest(t, f, actor, target, "PATCH", routeUser, "", body, strings.Repeat("M", 22))
		if got.Code != 403 || f.revision(target) != "1" {
			t.Fatalf("mixed authority: %d %s", got.Code, got.Body)
		}
	}
	body := `{"mode":"profile","expected_revision":"1","rpm_limit":null,"concurrency_limit":null,"light_rpm_limit":null,"light_concurrency_limit":null,"global_rpm_limit":null}`
	got := stewardUserRequest(t, f, actor, target, "PATCH", routeUser, "", body, strings.Repeat("N", 22))
	if got.Code != 200 {
		t.Fatalf("null profile: %d %s", got.Code, got.Body)
	}
	var user AdminUser
	if err := json.Unmarshal(got.Body.Bytes(), &user); err != nil {
		t.Fatal(err)
	}
	if user.RPMLimit != nil || user.LightRPMLimit != nil || user.LightConcurrencyLimit != nil || user.GlobalRPMLimit != nil || user.EffectiveConcurrencyLimit != "5" || user.EffectiveLightConcurrencyLimit != "15" || user.EffectiveLightRPMLimit != "30" || user.EffectiveGlobalRPMLimit != "60" {
		t.Fatalf("defaults: %+v", user)
	}
	if len(f.invalidator.users) != 0 || len(f.invalidator.limitUsers) != 1 {
		t.Fatal("changing limits revoked active requests")
	}
	for i, field := range []string{`"light_rpm_limit":"0"`, `"light_rpm_limit":"4097"`, `"light_rpm_limit":30`, `"light_concurrency_limit":"100001"`, `"global_rpm_limit":"0"`, `"global_rpm_limit":"01"`} {
		got := stewardUserRequest(t, f, actor, target, "PATCH", routeUser, "", `{"mode":"profile","expected_revision":"2",`+field+`}`, strings.Repeat(string(rune('a'+i)), 22))
		if got.Code != 400 || f.revision(target) != "2" {
			t.Fatalf("invalid field %s: %d", field, got.Code)
		}
	}
}
