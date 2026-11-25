package duel_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/waiting-here/NonbiriAPI/internal/game/bidding"
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

func TestStartupLeavesRetiredCatalogEmpty(t *testing.T) {
	f := newFixture(t, "bidding")
	for _, table := range []string{"game_ai_policies", "game_ai_policy_versions", "game_ai_bots", "game_ai_challenges"} {
		var count int
		if err := f.db.QueryRow("SELECT count(*) FROM " + table).Scan(&count); err != nil || count != 0 {
			t.Fatal(table, count, err)
		}
	}
}
