package blackjack_test

import (
	"testing"
	"time"
)

func TestIdleWorkerDoesNotWriteAndResumesForNewQueue(t *testing.T) {
	f := newFixture(t, 1)
	f.db.SetMaxOpenConns(1)
	f.exec(`PRAGMA query_only=ON`)
	for range 3 {
		f.clock.Add(1)
		result, err := f.s.RecoverBeforeListen(f.ctx, f.clock.Load(), 128, time.Now().Add(time.Second))
		if err != nil || result.Processed != 0 {
			t.Fatalf("idle tick wrote database: %+v %v", result, err)
		}
	}
	f.exec(`PRAGMA query_only=OFF`)
	f.clock.Store(131)
	f.join(0)
	f.clock.Store(150)
	result, err := f.s.RecoverBeforeListen(f.ctx, 150, 128, time.Now().Add(time.Second))
	if err != nil || result.Processed == 0 {
		t.Fatalf("new queue did not resume: %+v %v", result, err)
	}
	var seated int
	if err := f.db.QueryRow(`SELECT COUNT(*) FROM game_blackjack_entries WHERE state='seated'`).Scan(&seated); err != nil || seated != 1 {
		t.Fatalf("new table not seated: %d %v", seated, err)
	}
}
