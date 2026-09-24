package antiabuse

import (
	"context"
	"database/sql"
	"testing"
)

func TestActiveFactsAutomaticPenaltyReplacesProtectionWithoutActivity(t *testing.T) {
	f := newAbuseFixture(t)
	user, now := f.user(), f.clock.Load()
	if _, err := f.store.DB().Exec(`INSERT INTO user_activity_state(user_id,observation_started_at,last_active_at,activity_seq,schedule_revision,next_due_at) VALUES(?,?,?,3,7,?)`, user, now-100, now-90, now+100); err != nil {
		t.Fatal(err)
	}
	if _, err := f.store.DB().Exec(`UPDATE users SET is_banned=1,banned_until=NULL,ban_kind='protective_inactivity' WHERE id=?`, user); err != nil {
		t.Fatal(err)
	}
	tx, err := f.store.DB().BeginTx(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	var revision []byte
	if err = tx.QueryRow(`SELECT revision FROM users WHERE id=?`, user).Scan(&revision); err != nil {
		t.Fatal(err)
	}
	if err = applyRestrictions(context.Background(), tx, user, now, revision, 60, 0); err != nil {
		t.Fatal(err)
	}
	if err = tx.Commit(); err != nil {
		t.Fatal(err)
	}
	var kind string
	var seq, active, schedule, until int64
	var due sql.NullInt64
	if err = f.store.DB().QueryRow(`SELECT u.ban_kind,u.banned_until,a.activity_seq,a.last_active_at,a.schedule_revision,a.next_due_at FROM users u JOIN user_activity_state a ON a.user_id=u.id WHERE u.id=?`, user).Scan(&kind, &until, &seq, &active, &schedule, &due); err != nil {
		t.Fatal(err)
	}
	if kind != "" || until != now+60 || seq != 3 || active != now-90 || schedule != 0 || due.Valid {
		t.Fatalf("restriction changed activity or marker: %q,%d,%d,%d,%d,%+v", kind, until, seq, active, schedule, due)
	}
}
