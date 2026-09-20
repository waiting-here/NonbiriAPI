package blackjack_test

import (
	"testing"
	"time"
)

func TestNewcomerDeleteReleasesEveryHoldBeforeLateSettlement(t *testing.T) {
	for _, phase := range []string{"waiting", "seating", "decision"} {
		t.Run(phase, func(t *testing.T) {
			f := newFixture(t, 2)
			var epoch int64
			if err := f.db.QueryRow(`SELECT started_at FROM game_statistics_epoch`).Scan(&epoch); err != nil {
				t.Fatal(err)
			}
			start := (epoch/60 + 1) * 60
			f.clock.Store(start)
			f.exec(`UPDATE sessions SET created_at=?,last_seen_at=?,expires_at=?,absolute_expires_at=?`, start, start, start+3600, start+7200)
			if phase == "waiting" {
				f.clock.Add(20)
			}
			f.join(0)
			if phase == "decision" {
				f.clock.Store(start + 5)
				if home := f.read(0); home.Phase != "decision" {
					t.Fatal("expected active hand", home.Phase)
				}
			}
			var holds int
			if err := f.db.QueryRow(`SELECT count(*) FROM game_onboarding_holds WHERE user_id=?`, f.users[0].UserID).Scan(&holds); err != nil || holds != 5 {
				t.Fatal("missing reservations", holds, err)
			}
			tx, err := f.db.Begin()
			if err != nil {
				t.Fatal(err)
			}
			defer tx.Rollback()
			final, err := f.s.PrepareDeleteTx(f.ctx, tx, f.users[0].UserID, f.clock.Load())
			if err != nil {
				t.Fatal(err)
			}
			if err := tx.Commit(); err != nil {
				t.Fatal(err)
			}
			final.Commit()
			f.clock.Store(start + 25)
			if _, err := f.s.RecoverBeforeListen(f.ctx, f.clock.Load(), 128, time.Now().Add(time.Minute)); err != nil {
				t.Fatal(err)
			}
			for _, table := range []string{"game_onboarding_holds", "game_onboarding_completions"} {
				if err := f.db.QueryRow(`SELECT count(*) FROM `+table+` WHERE user_id=?`, f.users[0].UserID).Scan(&holds); err != nil || holds != 0 {
					t.Fatal("deleted player's reward survived", table, holds, err)
				}
			}
			f.recovery()
		})
	}
}
