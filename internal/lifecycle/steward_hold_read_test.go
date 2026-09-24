package lifecycle

import (
	"context"
	"database/sql"
	"errors"
	"testing"
)

type currentStewardHeldRead struct{}

func (currentStewardHeldRead) AuthorizeStewardRead(ctx context.Context, tx *sql.Tx, userID int64) error {
	var allowed bool
	if err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM users WHERE id=? AND is_admin=0 AND is_banned=0 AND level=6)`, userID).Scan(&allowed); err != nil {
		return err
	}
	if !allowed {
		return ErrForbidden
	}
	return nil
}

func TestStewardHeldReadAuditsRevocationAndAccountDeletion(t *testing.T) {
	f := newLifecycleTestFixture(t, 100)
	admin := seedLifecycleUser(t, f.store.DB(), "held-admin", true, 100)
	user := seedLifecycleUser(t, f.store.DB(), "held-steward", false, 100)
	owner := seedLifecycleUser(t, f.store.DB(), "held-caller", false, 100)
	if _, err := f.store.DB().Exec(`UPDATE users SET level=6 WHERE id=?`, user); err != nil {
		t.Fatal(err)
	}
	if _, err := f.store.DB().Exec(`INSERT INTO logical_requests(id,user_id,route_kind,state,attempt_limit,accounting_state,settlement_destination,ledger_rows_remaining,created_at)
VALUES(?,?,'openai_chat_completions','accepted',1,'none','user',zeroblob(16),100)`, "req_AAAAAAAAAAAAAAAAAAAAAA", owner); err != nil {
		t.Fatal(err)
	}
	if _, err := f.store.DB().Exec(`INSERT INTO request_logs(id,logical_request_id,user_id,started_at) VALUES(4242,?,?,100)`, "req_AAAAAAAAAAAAAAAAAAAAAA", owner); err != nil {
		t.Fatal(err)
	}
	f.config.HeldObjects.RequestLog = testHeldObjectAdapter{consume: func(ctx context.Context, tx *sql.Tx, ref string) error {
		_, err := tx.ExecContext(ctx, `UPDATE request_logs SET legal_hold_consumed=1 WHERE id=?`, ref)
		return err
	}}
	c := mustNewLifecycleCoordinator(t, f.config)
	created, err := c.CreateLegalHold(context.Background(), LegalHoldCreate{AdminID: admin, ObjectKind: HeldRequestLog, ObjectRef: "4242", Basis: "management read", ExpiresAt: 200, Confirmation: true, IdempotencyKey: legalHoldKey("steward-read"), DecisionNow: 100})
	if err != nil {
		t.Fatal(err)
	}
	read := func(now int64, commit bool) (bool, error) {
		t.Helper()
		tx, err := f.store.DB().BeginTx(context.Background(), nil)
		if err != nil {
			t.Fatal(err)
		}
		defer tx.Rollback()
		allowed, err := c.AuthorizeStewardHeldObjectRead(context.Background(), tx, user, HeldRequestLog, "4242", now, currentStewardHeldRead{})
		if err == nil && commit {
			err = tx.Commit()
		}
		return allowed, err
	}
	if allowed, err := read(110, false); err != nil || !allowed {
		t.Fatal(allowed, err)
	}
	var count int
	if err := f.store.DB().QueryRow(`SELECT COUNT(*) FROM legal_hold_steward_reads`).Scan(&count); err != nil || count != 0 {
		t.Fatal("rolled-back read persisted audit", count, err)
	}
	for _, now := range []int64{111, 112} {
		if allowed, err := read(now, true); err != nil || !allowed {
			t.Fatal(allowed, err)
		}
	}
	var first, last, reads int64
	if err := f.store.DB().QueryRow(`SELECT first_read_at,last_read_at,read_count FROM legal_hold_steward_reads WHERE user_id=?`, user).Scan(&first, &last, &reads); err != nil || first != 111 || last != 112 || reads != 2 {
		t.Fatal(first, last, reads, err)
	}
	if err := f.store.DB().QueryRow(`SELECT COUNT(*) FROM legal_hold_read_audits WHERE admin_user_id=?`, user).Scan(&count); err != nil || count != 0 {
		t.Fatal("steward became an administrator audit actor", count, err)
	}
	for _, mutation := range []string{
		`UPDATE legal_hold_steward_reads SET user_id=NULL`,
		`UPDATE legal_hold_steward_reads SET first_read_at=110`,
		`UPDATE legal_hold_steward_reads SET read_count=0`,
		`UPDATE legal_hold_steward_reads SET last_read_at=200,read_count=3`,
	} {
		if _, err := f.store.DB().Exec(mutation); err == nil {
			t.Fatalf("invalid audit mutation accepted: %s", mutation)
		}
	}
	if _, err := f.store.DB().Exec(`UPDATE users SET level=4 WHERE id=?`, user); err != nil {
		t.Fatal(err)
	}
	if allowed, err := read(113, true); !errors.Is(err, ErrForbidden) || allowed {
		t.Fatal("revoked read", allowed, err)
	}
	if _, err := f.store.DB().Exec(`UPDATE users SET level=6 WHERE id=?`, user); err != nil {
		t.Fatal(err)
	}
	tx, err := f.store.DB().BeginTx(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	allowed, err := c.AuthorizeStewardHeldObjectRead(context.Background(), tx, user, HeldMaintenanceEvent, maintenanceObjectID("R", 'Q'), 114, currentStewardHeldRead{})
	_ = tx.Rollback()
	if !errors.Is(err, ErrForbidden) || allowed {
		t.Fatal("unrelated held domain exposed", allowed, err)
	}
	if allowed, err := read(200, true); err != nil || allowed {
		t.Fatal("expired held read", allowed, err)
	}
	if _, err := f.store.DB().Exec(`DELETE FROM users WHERE id=?`, user); err != nil {
		t.Fatal("audit blocked account deletion", err)
	}
	var actor sql.NullInt64
	if err := f.store.DB().QueryRow(`SELECT user_id,read_count FROM legal_hold_steward_reads`).Scan(&actor, &reads); err != nil || actor.Valid || reads != 2 {
		t.Fatal("deleted identity remained", actor, reads, err)
	}
	if _, err := f.store.DB().Exec(`DELETE FROM legal_holds WHERE id=?`, created.Value.ID); err != nil {
		t.Fatal(err)
	}
	if err := f.store.DB().QueryRow(`SELECT COUNT(*) FROM legal_hold_steward_reads`).Scan(&count); err != nil || count != 0 {
		t.Fatal("audit outlived its hold", count, err)
	}
}
