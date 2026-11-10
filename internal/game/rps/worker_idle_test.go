package rps

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/waiting-here/NonbiriAPI/internal/game"
)

func TestIdleWorkerChecksDoNotAcquireWriter(t *testing.T) {
	f := newRPSFixture(t)
	for i, name := range []string{"waiting-one", "waiting-two"} {
		user, _ := f.seedUser(name, rpsTestFunding)
		f.enqueue(user, game.RPSModeQuick, byte(i+1), byte(i+1), 94000+i)
	}
	writer, err := f.database.Conn(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	defer writer.Close()
	ctx, cancel := context.WithTimeout(t.Context(), 2*time.Second)
	defer cancel()
	for _, mode := range []string{game.RPSModeQuick, game.RPSModeStandard, game.RPSModeDeathmatch} {
		if matched, err := f.service.MatchOnce(ctx, mode); err != nil || matched {
			t.Fatalf("insufficient queue waited for writer: matched=%v err=%v", matched, err)
		}
	}
	if n, err := f.service.expireRankFactsBatch(ctx, f.clock.Load()); err != nil || n != 0 {
		t.Fatalf("empty rank maintenance waited for writer: n=%d err=%v", n, err)
	}
	if n, err := f.service.expireRankFacts(ctx, f.clock.Load()); err != nil || n != 0 {
		t.Fatalf("empty leaderboard expiry waited for writer: n=%d err=%v", n, err)
	}
	if _, err := f.service.expireRankFactsBatch(ctx, -1); !errors.Is(err, ErrInvalidRequest) {
		t.Fatalf("invalid decision time: %v", err)
	}
	if n := f.store.ReadDB().Stats().InUse; n != 0 {
		t.Fatalf("idle checks retained %d readers", n)
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	user, _ := f.seedUser("arriving-third", rpsTestFunding)
	f.enqueue(user, game.RPSModeQuick, 3, 3, 94002)
	if matched, err := f.service.MatchOnce(t.Context(), game.RPSModeQuick); err != nil || !matched {
		t.Fatalf("later arrival did not match: matched=%v err=%v", matched, err)
	}
}

func TestMatchProbeRechecksMaintenanceAfterWriterWait(t *testing.T) {
	f := newRPSFixture(t)
	for i, name := range []string{"probe-one", "probe-two", "probe-three"} {
		user, _ := f.seedUser(name, rpsTestFunding)
		f.enqueue(user, game.RPSModeQuick, byte(i+1), byte(i+1), 94100+i)
	}
	writer, err := f.database.Conn(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	defer writer.Close()
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	waits := f.database.Stats().WaitCount
	done := make(chan error, 1)
	go func() {
		matched, err := f.service.MatchOnce(ctx, game.RPSModeQuick)
		if matched && err == nil {
			err = errors.New("matched after maintenance was enabled")
		}
		done <- err
	}()
	ticker := time.NewTicker(time.Millisecond)
	defer ticker.Stop()
	for f.database.Stats().WaitCount == waits {
		select {
		case err := <-done:
			t.Fatalf("match returned before writer acquisition: %v", err)
		case <-ctx.Done():
			t.Fatal(ctx.Err())
		case <-ticker.C:
		}
	}
	if _, err := writer.ExecContext(ctx, `UPDATE maintenance_state SET enabled=1 WHERE id=1`); err != nil {
		t.Fatal(err)
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	if f.scalar(`SELECT COUNT(*) FROM game_rps_queue`) != 3 || f.scalar(`SELECT COUNT(*) FROM game_rps_sessions`) != 0 {
		t.Fatal("maintenance recheck changed the accepted queues")
	}
}
