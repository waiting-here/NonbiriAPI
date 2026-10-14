package duel_test

import "testing"

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
