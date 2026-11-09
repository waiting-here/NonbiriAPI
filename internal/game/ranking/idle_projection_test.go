package ranking

import (
	"context"
	"database/sql"
	"errors"
	"testing"
	"time"

	"github.com/waiting-here/NonbiriAPI/internal/pagination"
)

func TestIdleRankingCatchUpUsesReader(t *testing.T) {
	f, store := newRankingReadFixture(t)
	user := f.user(true)
	f.add(user, f.epoch, 100, 0, "fishing")
	now := f.epoch + 1
	service, err := NewWithReader(f.db, store.ReadDB(), allowRankUser{}, func() time.Time { return time.Unix(now, 0) })
	if err != nil {
		t.Fatal(err)
	}
	if ready, err := service.AdvanceBidding(t.Context(), now); err != nil || !ready {
		t.Fatal("initial bidding publication", ready, err)
	}
	held, err := f.db.BeginTx(t.Context(), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer held.Rollback()
	before := f.db.Stats().WaitCount
	ctx, cancel := context.WithTimeout(t.Context(), 2*time.Second)
	defer cancel()
	for name, advance := range map[string]func(context.Context, int64) (bool, error){"expiry": service.Advance, "bidding": service.AdvanceBidding} {
		if ready, err := advance(ctx, now); err != nil || !ready {
			t.Fatalf("idle %s waited for writer: ready=%v error=%v", name, ready, err)
		}
	}
	board, err := service.Read(ctx, user, "game_charity", "7d", pagination.Default())
	if err != nil || len(board.Rows) != 1 {
		t.Fatal("ready ranking depended on writer", board, err)
	}
	if f.db.Stats().WaitCount != before || store.ReadDB().Stats().InUse != 0 {
		t.Fatal("idle catch-up acquired writer or retained reader")
	}
	if err := held.Rollback(); err != nil {
		t.Fatal(err)
	}
	canceled, stop := context.WithCancel(t.Context())
	stop()
	if _, err := service.Advance(canceled, now); !errors.Is(err, context.Canceled) {
		t.Fatal("canceled probe", err)
	}
	if ready, err := service.Advance(t.Context(), now); err != nil || !ready {
		t.Fatal("reader was not reusable", ready, err)
	}
}

func TestRankingCatchUpRechecksAfterWriterWait(t *testing.T) {
	for _, mode := range []string{"expiry", "bidding"} {
		t.Run(mode, func(t *testing.T) {
			f, store := newRankingReadFixture(t)
			user := f.user(true)
			f.add(user, f.epoch, 100, 0, "bidding")
			now := f.epoch + 1
			if mode == "expiry" {
				now = f.epoch + week
			}
			service, err := NewWithReader(f.db, store.ReadDB(), allowRankUser{}, nil)
			if err != nil {
				t.Fatal(err)
			}
			advance := service.Advance
			advanceTx := AdvanceTx
			if mode == "bidding" {
				advance, advanceTx = service.AdvanceBidding, advanceBiddingTx
			}
			held, err := f.db.BeginTx(t.Context(), nil)
			if err != nil {
				t.Fatal(err)
			}
			defer held.Rollback()
			ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
			defer cancel()
			waits := f.db.Stats().WaitCount
			done := make(chan error, 1)
			go func() {
				ready, err := advance(ctx, now)
				if err == nil && !ready {
					err = errors.New("catch-up remained pending")
				}
				done <- err
			}()
			awaitRankingWriter(t, ctx, f.db, waits)
			if store.ReadDB().Stats().InUse != 0 {
				t.Fatal("probe retained reader while waiting for writer")
			}
			// Another writer consumes the observed work before this caller wins
			// the connection. Its authoritative pass must not apply it twice.
			if ready, err := advanceTx(ctx, held, now); err != nil || !ready {
				t.Fatal("competing catch-up", ready, err)
			}
			if err := held.Commit(); err != nil {
				t.Fatal(err)
			}
			if err := <-done; err != nil {
				t.Fatal(err)
			}
			board := "game_charity"
			want := "0"
			if mode == "bidding" {
				board, want = biddingBoard, "-100"
			}
			if got, _, _ := f.total(user, board, "7d"); got != want {
				t.Fatalf("projection applied twice: got %s want %s", got, want)
			}
		})
	}
}

func awaitRankingWriter(t *testing.T, ctx context.Context, database *sql.DB, before int64) {
	t.Helper()
	ticker := time.NewTicker(time.Millisecond)
	defer ticker.Stop()
	for database.Stats().WaitCount == before {
		select {
		case <-ctx.Done():
			t.Fatal("catch-up did not reach writer", ctx.Err())
		case <-ticker.C:
		}
	}
}

func TestRankingProbePreservesEmptyRebuildAndTimeExpiry(t *testing.T) {
	f, store := newRankingReadFixture(t)
	service, err := NewWithReader(f.db, store.ReadDB(), allowRankUser{}, nil)
	if err != nil {
		t.Fatal(err)
	}
	// An empty event queue is not enough: an unpublished projection still
	// needs its authoritative state transition, including during maintenance.
	if _, err := f.db.Exec(`UPDATE maintenance_state SET enabled=1 WHERE id=1;
UPDATE game_rank_net_rebuild SET phase=0,through_seq=NULL WHERE id=1`); err != nil {
		t.Fatal(err)
	}
	if ready, err := service.Advance(t.Context(), f.epoch); err != nil || !ready {
		t.Fatal("empty rebuild", ready, err)
	}
	var phase int
	if err := f.db.QueryRow("SELECT phase FROM game_rank_net_rebuild WHERE id=1").Scan(&phase); err != nil || phase != 2 {
		t.Fatal("unpublished empty projection", phase, err)
	}
	if ready, err := service.AdvanceBidding(t.Context(), f.epoch); err != nil || !ready {
		t.Fatal("empty bidding rebuild", ready, err)
	}
	if _, err := f.db.Exec("UPDATE maintenance_state SET enabled=0 WHERE id=1"); err != nil {
		t.Fatal(err)
	}
	user := f.user(true)
	f.add(user, f.epoch, 100, 50, "blackjack")
	if value, _, _ := f.total(user, "blackjack", "30d"); value != "50" {
		t.Fatal("missing initial profit", value)
	}
	for _, now := range []int64{f.epoch + week, f.epoch + 30*86400} {
		if ready, err := service.Advance(t.Context(), now); err != nil || !ready {
			t.Fatal("time-only expiry", ready, err)
		}
	}
	if value, _, _ := f.total(user, "blackjack", "30d"); value != "0" {
		t.Fatal("time-only expiry retained profit", value)
	}
	if _, err := f.db.Exec("DELETE FROM game_rank_net_rebuild WHERE id=1"); err != nil {
		t.Fatal(err)
	}
	if _, err := service.Advance(t.Context(), f.epoch); !errors.Is(err, sql.ErrNoRows) {
		t.Fatal("missing projection appeared ready", err)
	}
}
