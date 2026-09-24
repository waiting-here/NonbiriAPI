package inactivity

import (
	"context"
	"database/sql"
	"errors"
	"github.com/waiting-here/NonbiriAPI/internal/ledger"
	"sync"
	"testing"
	"time"
)

func TestDecayUsesLiveActivityRoleAndAvailableWallets(t *testing.T) {
	e := fixture(t)
	user := e.user(t, "donor", 1, 10000, 2000)
	five := e.user(t, "trainee", 5, 10000, 2000)
	six := e.user(t, "steward", 6, 10000, 2000)
	fresh := e.user(t, "active", 1, 10000, 2000)
	negative := e.user(t, "negative", 1, -100, -200)
	e.policy(t, testPolicy())
	tx, err := e.store.DB().Begin()
	if err != nil {
		t.Fatal(err)
	}
	if err = InitializeTx(context.Background(), tx, user, testNow); err != nil {
		t.Fatal(err)
	}
	if err = tx.Commit(); err != nil {
		t.Fatal(err)
	}
	if scalar(t, e.store.DB(), `SELECT observation_started_at FROM user_activity_state WHERE user_id=?`, user) != testNow-100*day {
		t.Fatal("startup reset the observation clock")
	}
	active(t, e, fresh, testNow, true)
	active(t, e, user, testNow, false)
	result := runBatch(t, e.s, testNow)
	if result.Decayed != 2 {
		t.Fatalf("actions %+v", result)
	}
	if e.balance(t, user, ledger.General) != "9000" || e.balance(t, user, ledger.Game) != "1500" {
		t.Fatal("donor not charged correctly")
	}
	for _, id := range []int64{five, six, fresh} {
		if e.balance(t, id, ledger.General) != "10000" {
			t.Fatal("exempt or active account charged")
		}
	}
	if e.balance(t, negative, ledger.General) != "-100" {
		t.Fatal("negative balance deepened")
	}
	if scalar(t, e.store.DB(), `SELECT count(*) FROM inactivity_runs WHERE user_id=? AND ledger_operation_id IS NULL`, negative) != 1 {
		t.Fatal("zero charge created ledger operation")
	}
	if again := runBatch(t, e.s, testNow); again.Decayed != 0 {
		t.Fatal("replayed decay")
	}
	later := testNow + 80*day
	runBatch(t, e.s, later)
	if e.balance(t, user, ledger.General) != "8100" {
		t.Fatal("downtime caused multiple charges")
	}
	if got := scalar(t, e.store.DB(), `SELECT next_due_at FROM user_activity_state WHERE user_id=?`, user); got != later+7*day {
		t.Fatal("next charge not based on processing time")
	}
	active(t, e, user, later+1, true)
	runBatch(t, e.s, later+7*day)
	if e.balance(t, user, ledger.General) != "8100" {
		t.Fatal("renewed activity did not stop decay")
	}
}

func TestProtectionWinsRevokesAndRestartsObservation(t *testing.T) {
	e := fixture(t)
	id := e.user(t, "protected", 1, 10000, 2000)
	p := testPolicy()
	p.Protection = ProtectionPolicy{true, days(45)}
	e.policy(t, p)
	callbacks := 0
	if _, err := e.store.DB().Exec(`INSERT INTO sessions(token_hash,user_id,last_seen_at,expires_at,absolute_expires_at,created_at,cred_gen) VALUES('protect-session',?,?,?,?,?,'test')`, id, testNow, testNow+600, testNow+1200, testNow); err != nil {
		t.Fatal(err)
	}
	e.s.cancelUser = func(context.Context, *sql.Tx, int64, string, int64) (func(bool), error) {
		return func(committed bool) {
			if committed {
				callbacks++
			}
		}, nil
	}
	r := runBatch(t, e.s, testNow)
	if r.Protected != 1 || r.Decayed != 0 || callbacks != 1 || e.invalidator.count.Load() != 1 {
		t.Fatal(r, callbacks)
	}
	if e.balance(t, id, ledger.General) != "10000" || scalar(t, e.store.DB(), `SELECT key_hash IS NULL AND generation=2 FROM caller_keys WHERE user_id=?`, id) != 1 {
		t.Fatal("ban charged or left key active")
	}
	if scalar(t, e.store.DB(), `SELECT count(*) FROM sessions WHERE user_id=?`, id) != 0 {
		t.Fatal("protective ban left a session active")
	}
	tx, err := e.store.DB().Begin()
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	allowed, err := DonorAllowedTx(context.Background(), tx, id, testNow)
	if err != nil || !allowed {
		t.Fatal("protected donation unavailable", err)
	}
	if err = ResetObservationTx(context.Background(), tx, id, testNow+day); err != nil {
		t.Fatal(err)
	}
	if _, err = tx.Exec(`UPDATE users SET is_banned=0,ban_kind='',banned_reason='',banned_until=NULL WHERE id=?`, id); err != nil {
		t.Fatal(err)
	}
	if err = tx.Commit(); err != nil {
		t.Fatal(err)
	}
	runBatch(t, e.s, testNow+day)
	if scalar(t, e.store.DB(), `SELECT observation_started_at FROM user_activity_state WHERE user_id=?`, id) != testNow+day {
		t.Fatal("unban failed to restart observation")
	}
	if _, err = e.store.DB().Exec(`UPDATE users SET is_banned=1,ban_kind='',banned_until=NULL WHERE id=?`, id); err != nil {
		t.Fatal(err)
	}
	tx, err = e.store.DB().Begin()
	if err != nil {
		t.Fatal(err)
	}
	allowed, err = DonorAllowedTx(context.Background(), tx, id, testNow+day)
	_ = tx.Rollback()
	if err != nil || allowed {
		t.Fatal("ordinary ban inherited donation exception")
	}
}

func TestProtectionCancellationRollbackAndPartialRetry(t *testing.T) {
	e := fixture(t)
	a := e.user(t, "first", 1, 1000, 1000)
	b := e.user(t, "second", 1, 1000, 1000)
	p := testPolicy()
	p.Protection = ProtectionPolicy{true, days(1)}
	e.policy(t, p)
	failure := errors.New("cancellation failed")
	e.s.cancelUser = func(_ context.Context, _ *sql.Tx, id int64, _ string, _ int64) (func(bool), error) {
		if id == b {
			return nil, failure
		}
		return func(bool) {}, nil
	}
	r, err := e.s.Process(context.Background(), testNow, 100, time.Now().Add(2*time.Second))
	if !errors.Is(err, failure) || r.Protected != 1 {
		t.Fatal(r, err)
	}
	if scalar(t, e.store.DB(), `SELECT is_banned FROM users WHERE id=?`, a) != 1 || scalar(t, e.store.DB(), `SELECT is_banned FROM users WHERE id=?`, b) != 0 {
		t.Fatal("partial commits not isolated")
	}
	e.s.cancelUser = func(context.Context, *sql.Tx, int64, string, int64) (func(bool), error) { return func(bool) {}, nil }
	runBatch(t, e.s, testNow)
	if scalar(t, e.store.DB(), `SELECT count(*) FROM inactivity_runs`) != 2 {
		t.Fatal("retry duplicated a completed ban")
	}
}

func TestConcurrentWorkersAndActivityDoNotDuplicateCharges(t *testing.T) {
	e := fixture(t)
	id := e.user(t, "race", 1, 10000, 2000)
	e.policy(t, testPolicy())
	var wg sync.WaitGroup
	errs := make(chan error, 3)
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := e.s.Process(context.Background(), testNow, 100, time.Now().Add(2*time.Second))
			errs <- err
		}()
	}
	wg.Add(1)
	go func() {
		defer wg.Done()
		tx, err := e.store.DB().Begin()
		if err != nil {
			errs <- err
			return
		}
		defer tx.Rollback()
		err = RecordActiveTx(context.Background(), tx, ActiveEvent{id, testNow, "game", true})
		if err == nil {
			err = tx.Commit()
		}
		errs <- err
	}()
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	count := scalar(t, e.store.DB(), `SELECT count(*) FROM inactivity_runs WHERE user_id=?`, id)
	if count > 1 {
		t.Fatal("concurrent duplicate charge")
	}
	want := "10000"
	if count == 1 {
		want = "9000"
	}
	if got := e.balance(t, id, ledger.General); got != want {
		t.Fatal(got, want)
	}
	if scalar(t, e.store.DB(), `SELECT activity_seq FROM user_activity_state WHERE user_id=?`, id) != 1 {
		t.Fatal("activity lost")
	}
}
