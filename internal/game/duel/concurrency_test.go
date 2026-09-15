package duel_test

import (
	"errors"
	"testing"

	"github.com/waiting-here/NonbiriAPI/internal/game/duel"
)

func together(tasks ...func() error) []error {
	start := make(chan struct{})
	results := make(chan error, len(tasks))
	for _, task := range tasks {
		go func() { <-start; results <- task() }()
	}
	close(start)
	errs := make([]error, len(tasks))
	for i := range errs {
		errs[i] = <-results
	}
	return errs
}

func TestConcurrentLocksSettleExactlyOnce(t *testing.T) {
	f := newFixture(t, "likes")
	state := f.matched()
	tasks := []func() error{}
	for user := range 2 {
		in := duel.ActionInput{Identity: f.identity(user), IdempotencyKey: f.key(), SessionID: state.ID, PhaseSeq: state.PhaseSeq, Action: []byte(basicPlan)}
		tasks = append(tasks, func() error { _, err := f.s.Action(f.ctx, in); return err })
	}
	for _, err := range together(tasks...) {
		if err != nil {
			t.Fatal(err)
		}
	}
	if got := f.read(0).Current; got.Phase != "settlement" || got.Round != 1 {
		t.Fatal(got)
	}
	var count int
	if err := f.db.QueryRow(`SELECT COUNT(*) FROM game_duel_rounds WHERE session_id=?`, state.ID).Scan(&count); err != nil || count != 1 {
		t.Fatal(count, err)
	}
	f.ledger()
}

func TestDeadlineWorkerAndLastActionUseOneRound(t *testing.T) {
	f := newFixture(t, "likes")
	state := f.matched()
	f.action(0, state, basicPlan)
	f.clock.Store(*state.Deadline)
	in := duel.ActionInput{Identity: f.identity(1), IdempotencyKey: f.key(), SessionID: state.ID, PhaseSeq: state.PhaseSeq, Action: []byte(basicPlan)}
	errs := together(func() error { _, err := f.s.Tick(f.ctx); return err }, func() error {
		_, err := f.s.Action(f.ctx, in)
		if !errors.Is(err, duel.ErrConflict) {
			return errors.New("late action did not lose to deadline")
		}
		return nil
	})
	for _, err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	page, err := f.s.Rounds(f.ctx, f.identity(0), state.ID, duel.PageInput{}, true)
	if err != nil || len(page.Items) != 1 || !page.Items[0].Timeouts[1-state.You] || page.Items[0].Timeouts[state.You] {
		t.Fatal(page, err)
	}
	f.ledger()
}

func TestSettlementProgressAndQualificationCancellationAreAtomic(t *testing.T) {
	for _, deletion := range []bool{false, true} {
		t.Run(map[bool]string{false: "ban", true: "delete"}[deletion], func(t *testing.T) {
			f := newFixture(t, "likes")
			state := f.matched()
			f.action(0, state, basicPlan)
			f.action(1, state, basicPlan)
			f.clock.Store(105)
			cancel := func() error {
				tx, err := f.db.BeginTx(f.ctx, nil)
				if err != nil {
					return err
				}
				defer tx.Rollback()
				prepare := f.s.CancelUserTx
				if deletion {
					prepare = f.s.PrepareDeleteTx
				}
				end, err := prepare(f.ctx, tx, f.users[0], 100)
				if err != nil {
					return err
				}
				defer end.Abort()
				if !deletion {
					if _, err := tx.ExecContext(f.ctx, `UPDATE users SET is_banned=1,banned_until=NULL WHERE id=?`, f.users[0]); err != nil {
						return err
					}
				}
				if err := tx.Commit(); err != nil {
					return err
				}
				end.Commit()
				return nil
			}
			for _, err := range together(func() error { _, err := f.s.Tick(f.ctx); return err }, cancel) {
				if err != nil {
					t.Fatal(err)
				}
			}
			home := f.read(1)
			if home.Current != nil || home.LatestResult == nil || home.LatestResult.Outcome != "system_cancelled" || home.LatestResult.TerminalAt != 105 || home.LatestResult.OwnRefund.Game != "4" {
				t.Fatal(home)
			}
			var operations int
			if err := f.db.QueryRow(`SELECT COUNT(*) FROM credit_operations WHERE kind='duel_terminal' AND source_id=?`, state.ID).Scan(&operations); err != nil || operations != 1 {
				t.Fatal(operations, err)
			}
			f.ledger()
		})
	}
}
