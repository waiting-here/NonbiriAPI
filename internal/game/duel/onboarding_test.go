package duel_test

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/waiting-here/NonbiriAPI/internal/game/duel"
	likeengine "github.com/waiting-here/NonbiriAPI/internal/game/likes/engine"
)

func enableNewRewards(f *fixture, mode string) {
	f.t.Helper()
	var epoch int64
	if err := f.db.QueryRow(`SELECT started_at FROM game_statistics_epoch`).Scan(&epoch); err != nil {
		f.t.Fatal(err)
	}
	now := epoch + 60
	f.clock.Store(now)
	if _, err := f.db.Exec(`UPDATE sessions SET created_at=?,last_seen_at=?,expires_at=?,absolute_expires_at=?`, now, now, now+3600, now+7200); err != nil {
		f.t.Fatal(err)
	}
	f.mode = mode
	for field, value := range map[string]string{"enabled": "1", "ticket_milli": "5000", "rake_platform_bp": "0", "rake_welfare_bp": "0", "rake_thursday_bp": "0"} {
		if _, err := f.db.Exec(`UPDATE site_config SET value=? WHERE key=?`, value, "game_"+f.rules.ID()+"_"+mode+"_"+field); err != nil {
			f.t.Fatal(err)
		}
	}
	if f.rules.ID() == "likes" {
		e, err := likeengine.New(mode)
		if err != nil {
			f.t.Fatal(err)
		}
		c := e.Catalog()
		for seat := range 2 {
			for _, skill := range c.Skills {
				selection := likeengine.Selection{Role: c.Roles[seat].ID, Skills: []string{skill.ID}}
				if e.ValidateSelection(selection) == nil {
					f.loadouts[seat], _ = json.Marshal(selection)
					break
				}
			}
		}
	}
}

func assertRewardRows(f *fixture, user, amount int64, count int) {
	f.t.Helper()
	var gotCount int
	var gotAmount int64
	if err := f.db.QueryRow(`SELECT count(*),COALESCE(sum(award_milli),0) FROM game_onboarding_completions WHERE user_id=? AND game_key=?`, user, f.rules.ID()).Scan(&gotCount, &gotAmount); err != nil || gotCount != count || gotAmount != amount {
		f.t.Fatal("reward", gotCount, gotAmount, err)
	}
}

func TestNewcomerDuelSurrenderAtomicReplayAndEveryMode(t *testing.T) {
	for _, tc := range []struct {
		game, mode string
		award      int64
	}{
		{"bidding", "tier1", 3000000}, {"bidding", "tier2", 4000000}, {"bidding", "tier3", 7000000}, {"likes", "quick", 3000000}, {"likes", "standard", 15000000},
	} {
		t.Run(tc.game+"/"+tc.mode, func(t *testing.T) {
			f := newFixture(t, tc.game)
			enableNewRewards(f, tc.mode)
			state := f.matched()
			var count int
			if err := f.db.QueryRow(`SELECT count(*) FROM game_onboarding_holds WHERE duel_session_id=?`, state.ID).Scan(&count); err != nil || count != 4 {
				t.Fatal("seat transfer", count, err)
			}
			input := duel.ActionInput{Identity: f.identity(0), IdempotencyKey: f.key(), SessionID: state.ID, PhaseSeq: state.PhaseSeq}
			if _, err := f.db.Exec(`CREATE TRIGGER fail_onboarding BEFORE INSERT ON game_onboarding_completions WHEN NEW.task_key LIKE '%win' BEGIN SELECT RAISE(ABORT,'reward failure'); END`); err != nil {
				t.Fatal(err)
			}
			if _, err := f.s.Surrender(f.ctx, input); err == nil {
				t.Fatal("partial settlement accepted")
			}
			assertRewardRows(f, f.users[1], 0, 0)
			if current := f.read(0).Current; current == nil || current.ID != state.ID {
				t.Fatal("failed reward committed game result")
			}
			if _, err := f.db.Exec(`DROP TRIGGER fail_onboarding`); err != nil {
				t.Fatal(err)
			}
			calls := make([]func() error, 4)
			for i := range calls {
				calls[i] = func() error { _, err := f.s.Surrender(f.ctx, input); return err }
			}
			for _, err := range together(calls...) {
				if err != nil {
					t.Fatal(err)
				}
			}
			assertRewardRows(f, f.users[0], 0, 0)
			assertRewardRows(f, f.users[1], tc.award, 2)
			if err := f.db.QueryRow(`SELECT count(*) FROM game_onboarding_holds`).Scan(&count); err != nil || count != 0 {
				t.Fatal("remaining holds", count, err)
			}
			f.ledger()
		})
	}
}

func TestNewcomerDuelTimeoutCompletionAndRecoveryCancellation(t *testing.T) {
	t.Run("normal_timeout_play", func(t *testing.T) {
		f := newFixture(t, "bidding")
		enableNewRewards(f, "tier1")
		f.matched()
		for steps := 0; steps < 40; steps++ {
			current := f.read(0).Current
			if current == nil {
				break
			}
			f.clock.Store(*current.Deadline)
			f.tick()
		}
		for _, user := range f.users {
			var n int
			if err := f.db.QueryRow(`SELECT count(*) FROM game_onboarding_completions WHERE user_id=? AND task_key='complete_tier_1'`, user).Scan(&n); err != nil || n != 1 {
				t.Fatal("timeout completion", n, err)
			}
		}
		f.ledger()
	})
	t.Run("recovery_releases_all", func(t *testing.T) {
		f := newFixture(t, "likes")
		enableNewRewards(f, "quick")
		f.enqueue(0, 0)
		f.enqueue(1, 0)
		if _, err := f.s.RecoverBeforeListenAt(f.ctx, f.clock.Load(), 100, time.Now().Add(time.Minute)); err != nil {
			t.Fatal(err)
		}
		var holds int
		if err := f.db.QueryRow(`SELECT count(*) FROM game_onboarding_holds`).Scan(&holds); err != nil || holds != 0 {
			t.Fatal(holds, err)
		}
		for _, user := range f.users {
			assertRewardRows(f, user, 0, 0)
		}
		f.ledger()
	})
}
