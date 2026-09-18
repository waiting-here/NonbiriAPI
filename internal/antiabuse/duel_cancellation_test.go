package antiabuse

import (
	"context"
	"database/sql"
	"testing"
)

func TestAutomaticBanDuelCancellationIsAtomic(t *testing.T) {
	f := newAbuseFixture(t)
	user := f.user()
	f.set(KeyCharityViolationBanSeconds, "60")
	if _, err := f.store.DB().Exec(`CREATE TABLE cancellation_probe(value INTEGER NOT NULL)`); err != nil {
		t.Fatal(err)
	}
	calls, commits, aborts := 0, 0, 0
	f.service.config.CancelUserDuelsTx = func(ctx context.Context, tx *sql.Tx, id int64, reason string, now int64) (func(bool), error) {
		calls++
		var banned int
		if err := tx.QueryRowContext(ctx, `SELECT is_banned FROM users WHERE id=?`, id).Scan(&banned); err != nil {
			return nil, err
		}
		if id != user || reason != "account_unavailable" || now != f.clock.Load() || banned != 0 {
			t.Fatal("incorrect cancellation authority or order")
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO cancellation_probe VALUES(1)`); err != nil {
			return nil, err
		}
		return func(ok bool) {
			if ok {
				commits++
			} else {
				aborts++
			}
		}, nil
	}
	if _, err := f.store.DB().Exec(`CREATE TRIGGER reject_revoke BEFORE UPDATE ON caller_keys BEGIN SELECT RAISE(ABORT,'injected'); END`); err != nil {
		t.Fatal(err)
	}
	if _, err := f.service.RecordShort(context.Background(), user, "[公益]provider/model", 1); err == nil {
		t.Fatal("injected failure accepted")
	}
	if calls != 1 || commits != 0 || aborts != 1 || f.scalar(`SELECT COUNT(*) FROM cancellation_probe`) != 0 || f.scalar(`SELECT is_banned FROM users WHERE id=?`, user) != 0 {
		t.Fatal("partial cancellation survived rollback")
	}
	if _, err := f.store.DB().Exec(`DROP TRIGGER reject_revoke`); err != nil {
		t.Fatal(err)
	}
	if _, err := f.service.RecordShort(context.Background(), user, "[公益]provider/model", 1); err != nil {
		t.Fatal(err)
	}
	if calls != 2 || commits != 1 || aborts != 1 || f.scalar(`SELECT COUNT(*) FROM cancellation_probe`) != 1 || f.scalar(`SELECT is_banned FROM users WHERE id=?`, user) != 1 {
		t.Fatal("cancellation and ban did not commit together")
	}
}
