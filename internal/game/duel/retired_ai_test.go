package duel_test

import (
	"bytes"
	"compress/gzip"
	_ "embed"
	"errors"
	"io"
	"testing"
	"time"

	"github.com/waiting-here/NonbiriAPI/internal/game/duel"
)

//go:embed testdata/retired_bidding.sql.txt
var retiredBiddingSQL string

func loadHistoricalFixture(t *testing.T, f *fixture, data string) {
	t.Helper()
	tx, err := f.db.BeginTx(f.ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(f.ctx, data); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
}
func unzipFixture(t *testing.T, data []byte) string {
	t.Helper()
	z, err := gzip.NewReader(bytes.NewReader(data))
	if err != nil {
		t.Fatal(err)
	}
	defer z.Close()
	raw, err := io.ReadAll(z)
	if err != nil {
		t.Fatal(err)
	}
	return string(raw)
}
func TestRetiredChallengeCancellationAndHistory(t *testing.T) {
	f := newUnrecoveredFixture(t, "bidding")
	loadHistoricalFixture(t, f, retiredBiddingSQL)
	for range 2 {
		if _, err := f.s.RecoverBeforeListenAt(f.ctx, 100, 100, time.Now().Add(2*time.Second)); err != nil {
			t.Fatal(err)
		}
	}
	result := f.read(0).LatestResult
	if result == nil || result.Outcome != "system_cancelled" || result.OwnRefund.Game != "3" || result.OwnRefund.General != "2" {
		t.Fatal(result)
	}
	var count int
	for _, query := range []string{
		"SELECT count(*) FROM credit_operations WHERE kind='ai_terminal'",
		"SELECT count(*) FROM game_ai_queue WHERE state='failed' AND failure='server_restart'",
	} {
		if err := f.db.QueryRow(query).Scan(&count); err != nil || count != 1 {
			t.Fatal(query, count, err)
		}
	}
	for _, query := range []string{"SELECT count(*) FROM game_duel_user_slots", "SELECT count(*) FROM game_ai_memories", "SELECT count(*) FROM game_ai_clears"} {
		if err := f.db.QueryRow(query).Scan(&count); err != nil || count != 0 {
			t.Fatal(query, count, err)
		}
	}
	if _, err := f.s.Enqueue(f.ctx, duel.EnqueueInput{Identity: f.identity(0), Mode: "ai", IdempotencyKey: f.key()}); !errors.Is(err, duel.ErrInvalidRequest) {
		t.Fatal(err)
	}
	f.ledger()
}
