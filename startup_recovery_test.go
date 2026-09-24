package main

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"fmt"
	"math/big"
	"testing"
	"time"

	"github.com/waiting-here/NonbiriAPI/internal/game/ranking"
	"github.com/waiting-here/NonbiriAPI/internal/secret"
)

type startupTestAuthorizer struct{}

func (startupTestAuthorizer) AuthorizeUserMutation(context.Context, *sql.Tx, int64) error { return nil }

type startupAdvanceFunc func(context.Context, int64) (bool, error)

func (f startupAdvanceFunc) Advance(ctx context.Context, now int64) (bool, error) { return f(ctx, now) }

type startupLifecycleFunc func(context.Context, int64) error

func (f startupLifecycleFunc) RecoverBeforeListenerAt(ctx context.Context, now int64) error {
	return f(ctx, now)
}

func TestStartupRankingFailureKeepsLifecycleClosed(t *testing.T) {
	want := errors.New("ranking unavailable")
	called := false
	advance := startupAdvanceFunc(func(ctx context.Context, now int64) (bool, error) {
		deadline, ok := ctx.Deadline()
		if !ok || time.Until(deadline) > 2*time.Second || now != 123 {
			t.Fatal("unbounded batch or changed recovery time")
		}
		return false, want
	})
	lifecycle := startupLifecycleFunc(func(context.Context, int64) error { called = true; return nil })
	if err := recoverRankingsAndLifecycleBeforeListener(context.Background(), advance, lifecycle, 123); !errors.Is(err, want) || called {
		t.Fatalf("failed prerequisite opened recovery: %v, %v", err, called)
	}
}

func TestStartupRankingBacklogCancellationAndExpiredGameRestart(t *testing.T) {
	f := newBlackjackHTTPFixture(t)
	id := dealBlackjackHTTP(f)
	if err := f.app.Close(); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	tx, err := f.store.DB().BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	// More than one batch for both rebuild and expiry, with a retained cohort
	// on the next second to detect duplicate subtraction on restart.
	for i := range 1400 {
		at := f.now + int64(i/700)
		if err := ranking.RecordTx(ctx, tx, ranking.Contribution{UserID: f.users[0], Game: "fishing", Source: fmt.Sprintf("historical-%d", i), SettledAt: at, Loss: big.NewInt(-1), PositiveProfit: new(big.Int)}); err != nil {
			t.Fatal(err)
		}
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	for _, q := range []string{
		`DELETE FROM game_rank_totals WHERE board IN ('game_net_profit','fishing_net_profit','blackjack_net_profit')`,
		`DELETE FROM game_rank_net_rebuild_totals`,
		`UPDATE game_rank_net_rebuild SET phase=0,through_seq=NULL,last_seq=zeroblob(16) WHERE id=1`,
	} {
		if _, err := f.store.DB().Exec(q); err != nil {
			t.Fatal(err)
		}
	}
	decisionNow := f.now + 7*24*60*60
	ranks, err := ranking.New(f.store.DB(), startupTestAuthorizer{}, nil)
	if err != nil {
		t.Fatal(err)
	}
	cancelCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	calls := 0
	first := startupAdvanceFunc(func(ctx context.Context, now int64) (bool, error) {
		calls++
		ready, err := ranks.Advance(ctx, now)
		if ready || err != nil || now != decisionNow {
			t.Fatalf("first batch: %v %v %d", ready, err, now)
		}
		cancel()
		return ready, err
	})
	closed := startupLifecycleFunc(func(context.Context, int64) error { t.Fatal("games recovered during cancellation"); return nil })
	if err := recoverRankingsAndLifecycleBeforeListener(cancelCtx, first, closed, decisionNow); !errors.Is(err, context.Canceled) || calls != 1 {
		t.Fatalf("cancellation: %v calls=%d", err, calls)
	}
	var progress, phase string
	if err := f.store.DB().QueryRow(`SELECT hex(last_seq) FROM game_rank_net_rebuild WHERE id=1`).Scan(&progress); err != nil || progress == "00000000000000000000000000000000" {
		t.Fatalf("batch progress lost: %s %v", progress, err)
	}
	if err := f.store.DB().QueryRow(`SELECT phase FROM game_blackjack_sessions WHERE id=?`, id).Scan(&phase); err != nil || phase != "decision" {
		t.Fatalf("game changed before prerequisites: %s %v", phase, err)
	}

	vault, err := secret.New(bytes.Repeat([]byte{0x53}, secret.MasterKeyBytes))
	if err != nil {
		t.Fatal(err)
	}
	defer vault.Close()
	f.clock.Store(decisionNow)
	restart := func() {
		t.Helper()
		app, err := buildApplicationWithGameClock(auditConfig(), f.store, vault, func() time.Time { return time.Unix(f.clock.Load(), 0) })
		if err != nil {
			t.Fatal(err)
		}
		f.app = app
		t.Cleanup(func() { _ = app.Close() })
		f.checkLedger()
		if err := app.Close(); err != nil {
			t.Fatal(err)
		}
	}
	restart()
	if err := f.store.DB().QueryRow(`SELECT phase FROM game_blackjack_sessions WHERE id=?`, id).Scan(&phase); err != nil || phase != "cancelled" {
		t.Fatalf("expired game not recovered: %s %v", phase, err)
	}
	var total []byte
	if err := f.store.DB().QueryRow(`SELECT amount_mag FROM game_rank_totals WHERE user_id=? AND board='fishing_net_profit' AND window='7d'`, f.users[0]).Scan(&total); err != nil || new(big.Int).SetBytes(total).String() != "700" {
		t.Fatalf("expiry/backfill total: %x %v", total, err)
	}
	var operations, after int
	if err := f.store.DB().QueryRow(`SELECT count(*) FROM credit_operations`).Scan(&operations); err != nil {
		t.Fatal(err)
	}
	restart()
	if err := f.store.DB().QueryRow(`SELECT count(*) FROM credit_operations`).Scan(&after); err != nil || after != operations {
		t.Fatalf("repeated settlement: %d %d %v", operations, after, err)
	}
}
