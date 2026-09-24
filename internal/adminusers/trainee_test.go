package adminusers

import (
	"fmt"
	"strings"
	"testing"
)

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
