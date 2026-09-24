package adminusers

import (
	"database/sql"
	"net/http"
	"testing"
)

func TestActiveFactsProtectiveUnbanAndOrdinaryBanReplacement(t *testing.T) {
	for _, unban := range []bool{true, false} {
		t.Run(map[bool]string{true: "unban", false: "replace"}[unban], func(t *testing.T) {
			f := newAdminUsersFixture(t)
			user := f.seedUser("protected", false)
			if _, err := f.store.DB().Exec(`INSERT INTO user_activity_state(user_id,observation_started_at,last_active_at,activity_seq,activity_epoch,schedule_revision,next_due_at) VALUES(?,?,?,1,1,7,?)`, user, adminUsersTestNow-100, adminUsersTestNow-90, adminUsersTestNow+100); err != nil {
				t.Fatal(err)
			}
			if _, err := f.store.DB().Exec(`UPDATE users SET is_banned=1,banned_until=NULL,ban_kind='protective_inactivity' WHERE id=?`, user); err != nil {
				t.Fatal(err)
			}
			path, body := routeBan, `{"expected_revision":"1","reason":"ordinary penalty","duration_seconds":600}`
			if unban {
				path, body = routeUnban, `{"expected_revision":"1"}`
			}
			key := "ACTIVITYBANACTIVITYBAN1"
			for range 2 {
				got := f.request(http.MethodPost, path, "https://admin.example/admin/api/users/1/ban", body, user, key)
				if got.Code != http.StatusNoContent {
					t.Fatalf("mutation: %d %s", got.Code, got.Body.String())
				}
			}
			var kind string
			var seq, started, schedule int64
			var active, due sql.NullInt64
			if err := f.store.DB().QueryRow(`SELECT u.ban_kind,a.observation_started_at,a.last_active_at,a.activity_seq,a.schedule_revision,a.next_due_at FROM users u JOIN user_activity_state a ON a.user_id=u.id WHERE u.id=?`, user).Scan(&kind, &started, &active, &seq, &schedule, &due); err != nil {
				t.Fatal(err)
			}
			if kind != "" || schedule != 0 || due.Valid {
				t.Fatalf("marker/schedule: %q/%d/%+v", kind, schedule, due)
			}
			if unban {
				if started != adminUsersTestNow || active.Valid || seq != 2 {
					t.Fatalf("unban observation=%d,%+v,%d", started, active, seq)
				}
			} else if started != adminUsersTestNow-100 || !active.Valid || active.Int64 != adminUsersTestNow-90 || seq != 1 {
				t.Fatalf("ban refreshed activity: %d,%+v,%d", started, active, seq)
			}
		})
	}
}
