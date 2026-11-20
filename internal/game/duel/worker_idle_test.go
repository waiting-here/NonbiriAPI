package duel_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/waiting-here/NonbiriAPI/internal/game/bidding"
	"github.com/waiting-here/NonbiriAPI/internal/game/bidding/strategy"
)

func TestIdleWorkerAndFutureHumanDeadlineDoNotWrite(t *testing.T) {
	f := newFixture(t, "bidding")
	f.db.SetMaxOpenConns(1)
	queryOnly := func(on bool) {
		t.Helper()
		value := "OFF"
		if on {
			value = "ON"
		}
		if _, err := f.db.Exec(`PRAGMA query_only=` + value); err != nil {
			t.Fatal(err)
		}
	}
	queryOnly(true)
	if result, err := f.s.Tick(f.ctx); err != nil || result.Processed != 0 {
		t.Fatalf("idle tick wrote database: %+v %v", result, err)
	}
	queryOnly(false)
	state := f.matched()
	queryOnly(true)
	if result, err := f.s.Tick(f.ctx); err != nil || result.Processed != 0 {
		t.Fatalf("future human deadline wrote database: %+v %v", result, err)
	}
	queryOnly(false)
	// The same worker must resume exactly when the persisted deadline is due.
	var deadline int64
	if err := f.db.QueryRow(`SELECT phase_deadline FROM game_duel_sessions WHERE id=?`, state.ID).Scan(&deadline); err != nil {
		t.Fatal(err)
	}
	f.clock.Store(deadline)
	if result, err := f.s.Tick(f.ctx); err != nil || result.Processed == 0 {
		t.Fatalf("due deadline was skipped: %+v %v", result, err)
	}
}

func TestBiddingRecoveryResumesAfterAnUnusedTimeSlice(t *testing.T) {
	f := newFixture(t, "bidding")
	service, err := bidding.New(f.options)
	if err != nil {
		t.Fatal(err)
	}
	defer service.Close()
	run := func(deadline time.Time) error {
		_, err := service.RecoverBeforeListenAt(f.ctx, f.clock.Load(), 100, deadline)
		return err
	}
	if err := run(time.Now().Add(-time.Second)); !errors.Is(err, context.DeadlineExceeded) || service.Ready(f.ctx, nil) {
		t.Fatalf("expired recovery: err=%v ready=%v", err, service.Ready(f.ctx, nil))
	}
	if err := run(time.Now().Add(2 * time.Second)); err != nil || !service.Ready(f.ctx, nil) {
		t.Fatalf("fresh recovery: err=%v ready=%v", err, service.Ready(f.ctx, nil))
	}
}

type invalidThirdPreset struct{ bidding.AIAdapter }

func (invalidThirdPreset) Presets() any {
	presets := strategy.Presets()
	presets[2].Policy.Schema = "invalid-test-schema"
	return presets
}

func TestStartupDefaultsAreAtomicAndWarmRecoveryOnlyReads(t *testing.T) {
	f := newUnrecoveredFixture(t, "bidding")
	rowCounts := func(want int) {
		t.Helper()
		for _, table := range []string{"game_ai_policies", "game_ai_policy_versions", "game_ai_bots", "game_ai_challenges"} {
			var n int
			if err := f.db.QueryRow("SELECT count(*) FROM " + table).Scan(&n); err != nil || n != want {
				t.Fatalf("%s count=%d err=%v, want %d", table, n, err, want)
			}
		}
	}
	broken := f.options
	broken.AI = invalidThirdPreset{}
	bad, err := bidding.New(broken)
	if err != nil {
		t.Fatal(err)
	}
	defer bad.Close()
	if err := bad.Module().PrepareStartupDefaults(f.ctx); err == nil {
		t.Fatal("accepted an invalid third preset")
	}
	rowCounts(0)
	if err := f.s.Module().PrepareStartupDefaults(f.ctx); err != nil {
		t.Fatal(err)
	}
	rowCounts(4)
	if f.s.Ready(f.ctx, nil) {
		t.Fatal("defaults alone opened the game")
	}
	warm, err := bidding.New(f.options)
	if err != nil {
		t.Fatal(err)
	}
	defer warm.Close()
	// Fail any write on the single writer, independently of the reader pool.
	if _, err := f.db.Exec("PRAGMA query_only=ON"); err != nil {
		t.Fatal(err)
	}
	if err := warm.Module().PrepareStartupDefaults(f.ctx); err != nil {
		t.Fatal(err)
	}
	result, err := warm.RecoverBeforeListenAt(f.ctx, f.clock.Load(), 100, time.Now().Add(2*time.Second))
	if err != nil || result.More || result.Processed != 0 || !warm.Ready(f.ctx, nil) {
		t.Fatalf("warm empty recovery: result=%+v err=%v", result, err)
	}
	rowCounts(4)
}
