package adminusers

import (
	"context"
	"fmt"
	"strings"
	"testing"
)

func TestTraineeUserManagementReadAndBanBoundaries(t *testing.T) {
	f, actor := newStewardUsersFixture(t)
	if _, err := f.store.DB().Exec("UPDATE users SET level=5 WHERE id=?", actor); err != nil {
		t.Fatal(err)
	}
	for level := 1; level <= 6; level++ {
		target := f.seedUser(fmt.Sprintf("managed-level-%d", level), false)
		if _, err := f.store.DB().Exec("UPDATE users SET level=? WHERE id=?", level, target); err != nil {
			t.Fatal(err)
		}
		for _, route := range []string{routeUser, routeUserLoans, routeUserPenalties} {
			if got := stewardUserRequest(t, f, actor, target, "GET", route, "", "", ""); got.Code != 200 {
				t.Fatalf("L%d %s read: %d %s", level, route, got.Code, got.Body)
			}
		}
		body := fmt.Sprintf(`{"expected_revision":%q,"reason":"Review","duration_seconds":null}`, f.revision(target))
		key := strings.Repeat(fmt.Sprint(level), 22)
		got := stewardUserRequest(t, f, actor, target, "POST", routeBan, "", body, key)
		want := 204
		if level >= 5 {
			want = 403
		}
		if got.Code != want {
			t.Fatalf("L%d ban: %d %s", level, got.Code, got.Body)
		}
		if level < 5 {
			replay := stewardUserRequest(t, f, actor, target, "POST", routeBan, "", body, key)
			if replay.Code != 204 || f.revision(target) != "2" {
				t.Fatalf("ban replay: %d revision %s", replay.Code, f.revision(target))
			}
			// Neither unban nor replacing a permanent ban with a shorter ban is allowed.
			for _, action := range []struct{ route, body string }{
				{routeUnban, `{"expected_revision":"2"}`},
				{routeBan, `{"expected_revision":"2","reason":"Replacement","duration_seconds":60}`},
			} {
				if got := stewardUserRequest(t, f, actor, target, "POST", action.route, "", action.body, key+"x"); got.Code != 403 {
					t.Fatalf("restricted existing ban: %d %s", got.Code, got.Body)
				}
			}
			if f.revision(target) != "2" {
				t.Fatal("denied mutation changed the user")
			}
			if _, err := f.store.DB().Exec("UPDATE users SET level=5 WHERE id=?", target); err != nil {
				t.Fatal(err)
			}
			if got := stewardUserRequest(t, f, actor, target, "POST", routeBan, "", body, key); got.Code != 403 {
				t.Fatalf("promoted target replay: %d", got.Code)
			}
		}
	}
	for _, target := range []int64{actor, f.adminID} {
		got := stewardUserRequest(t, f, actor, target, "POST", routeBan, "", `{"expected_revision":"1","reason":"Review","duration_seconds":null}`, strings.Repeat("S", 22))
		if got.Code != 403 && (target != f.adminID || got.Code != 404) {
			t.Fatalf("protected target: %d %s", got.Code, got.Body)
		}
	}
	if got := stewardUserRequest(t, f, actor, 0, "GET", routeUsers, "?account_state=all&page=1&page_size=20", "", ""); got.Code != 200 {
		t.Fatalf("trainee user list: %d %s", got.Code, got.Body)
	}
	for _, method := range []string{"GET", "POST"} {
		got := managementRequest(f, actor, method, roleSteward.route(routeBlacklist), roleSteward.route(routeBlacklist), ``, "")
		if method == "POST" {
			got = managementRequest(f, actor, method, roleSteward.route(routeBlacklist), roleSteward.route(routeBlacklist), `{"discord_id":"123456789012345678","reason":"Review"}`, strings.Repeat("K", 22))
		}
		if got.Code != 403 {
			t.Fatalf("blacklist %s: %d %s", method, got.Code, got.Body)
		}
	}
}

func TestTraineeCannotEditUserProfileOrEconomy(t *testing.T) {
	f, actor := newStewardUsersFixture(t)
	target := f.seedUser("read-only-target", false)
	if _, err := f.store.DB().Exec("UPDATE users SET level=5 WHERE id=?", actor); err != nil {
		t.Fatal(err)
	}
	for index, fields := range []string{
		`"mode":"profile","endpoint_limit":"20"`,
		`"mode":"profile","rpm_limit":"100"`,
		`"mode":"profile","concurrency_limit":"30"`,
		`"mode":"profile","level":4`,
		`"mode":"profile","lang":"en"`,
		`"mode":"profile","discord_gate_policy":"exempt"`,
		`"mode":"economy","target":"balance","direction":"increase","amount":"1","reason":"Correction"`,
		`"mode":"economy","target":"game_balance","direction":"increase","amount":"1","reason":"Correction"`,
		`"mode":"economy","target":"donation_credit","direction":"increase","amount":"1","reason":"Correction"`,
	} {
		got := stewardUserRequest(t, f, actor, target, "PATCH", routeUser, "", `{"expected_revision":"1",`+fields+`}`, strings.Repeat(string(rune('a'+index)), 22))
		if got.Code != 403 || f.revision(target) != "1" {
			t.Fatalf("forbidden edit %s: %d %s", fields, got.Code, got.Body)
		}
	}
}

func TestTraineeBanRechecksAuthorityAfterIdentityBarrier(t *testing.T) {
	for _, changed := range []string{"actor", "target"} {
		t.Run(changed, func(t *testing.T) {
			f, actor := newStewardUsersFixture(t)
			target := f.seedUser("authority-change", false)
			if _, err := f.store.DB().Exec("UPDATE users SET level=5 WHERE id=?", actor); err != nil {
				t.Fatal(err)
			}
			locks := 0
			f.service.identityBarrier = func(context.Context, string) (func(), error) {
				locks++
				id, level := target, 5
				if changed == "actor" {
					id, level = actor, 4
				}
				_, err := f.store.DB().Exec("UPDATE users SET level=? WHERE id=?", level, id)
				return func() {}, err
			}
			got := stewardUserRequest(t, f, actor, target, "POST", routeBan, "", `{"expected_revision":"1","reason":"Review","duration_seconds":null}`, strings.Repeat("R", 22))
			if got.Code != 403 || locks != 1 || f.revision(target) != "1" {
				t.Fatalf("authority changed during preflight: %d locks=%d %s", got.Code, locks, got.Body)
			}
		})
	}
}

func TestTraineeCanBanAfterExpiredBan(t *testing.T) {
	f, actor := newStewardUsersFixture(t)
	target := f.seedUser("expired-ban", false)
	if _, err := f.store.DB().Exec("UPDATE users SET level=5 WHERE id=?", actor); err != nil {
		t.Fatal(err)
	}
	if _, err := f.store.DB().Exec("UPDATE users SET is_banned=1,banned_until=? WHERE id=?", adminUsersTestNow-1, target); err != nil {
		t.Fatal(err)
	}
	got := stewardUserRequest(t, f, actor, target, "POST", routeBan, "", `{"expected_revision":"1","reason":"New review","duration_seconds":60}`, strings.Repeat("E", 22))
	if got.Code != 204 {
		t.Fatalf("expired ban blocks new ban: %d %s", got.Code, got.Body)
	}
}

func TestStewardCanAssignAndManageTrainee(t *testing.T) {
	f, actor := newStewardUsersFixture(t)
	target := f.seedUser("new-trainee", false)
	body := fmt.Sprintf(`{"mode":"profile","expected_revision":"%s","level":5}`, f.revision(target))
	assigned := stewardUserRequest(t, f, actor, target, "PATCH", routeUser, "", body, strings.Repeat("T", 22))
	if assigned.Code != 200 {
		t.Fatalf("assign trainee: %d %s", assigned.Code, assigned.Body)
	}
	var level int
	if err := f.store.DB().QueryRow("SELECT level FROM users WHERE id=?", target).Scan(&level); err != nil || level != 5 {
		t.Fatal(level, err)
	}
	body = fmt.Sprintf(`{"expected_revision":"%s","reason":"Review","duration_seconds":null}`, f.revision(target))
	banned := stewardUserRequest(t, f, actor, target, "POST", routeBan, "", body, strings.Repeat("B", 22))
	if banned.Code != 204 {
		t.Fatalf("manage trainee: %d %s", banned.Code, banned.Body)
	}
}
