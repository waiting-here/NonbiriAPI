package blackjack_test

import (
	"errors"
	"math"
	"os"
	"testing"
	"time"

	"github.com/waiting-here/NonbiriAPI/internal/game/blackjack"
)

func TestBlackjackFullWaitingQueueAndDrain(t *testing.T) {
	if os.Getenv("BLACKJACK_FULL_QUEUE") != "1" {
		t.Skip("full queue resource gate")
	}
	started := time.Now()
	f := newFixture(t, 4106)
	config := f.read(0).ConfigHash
	for i := 0; i < 4105; i++ {
		if _, err := f.s.Enqueue(f.ctx, blackjack.EnqueueInput{Identity: f.users[i], Key: f.id("op_"), Stake: "5000", ConfigHash: config}); err != nil {
			t.Fatal(i, err)
		}
	}
	last := f.read(4104)
	if last.QueueCount != "4096" || last.You == nil || last.You.Position != "4096" || len(last.Table.Fact.Seats) != 9 {
		t.Fatal("queue boundary", last.QueueCount, last.You)
	}
	if _, err := f.s.Enqueue(f.ctx, blackjack.EnqueueInput{Identity: f.users[4105], Key: f.id("op_"), Stake: "5000", ConfigHash: config}); !errors.Is(err, blackjack.ErrLimit) {
		t.Fatal("over capacity admitted", err)
	}
	// The request is already funded and retains its position across a retry.
	if _, err := f.s.Enqueue(f.ctx, blackjack.EnqueueInput{Identity: f.users[4104], Key: f.id("op_"), Stake: "5000", ConfigHash: config}); err != nil {
		t.Fatal(err)
	}
	if f.read(4104).You.Position != "4096" {
		t.Fatal("duplicate request reordered queue")
	}
	admitted := time.Since(started)
	f.exec(`UPDATE site_config SET value='0' WHERE key='game_blackjack_enabled'`)
	for i := 0; i < 34; i++ {
		if _, err := f.s.RecoverBeforeListen(f.ctx, f.clock.Load(), 128, time.Now().Add(time.Minute)); err != nil {
			t.Fatal(err)
		}
	}
	var active int
	if err := f.db.QueryRow(`SELECT COUNT(*) FROM game_blackjack_entries WHERE state IN ('waiting','seated','playing')`).Scan(&active); err != nil || active != 0 {
		t.Fatal("unfinished drain", active, err)
	}
	var wrong int
	if err := f.db.QueryRow(`SELECT COUNT(*) FROM credit_accounts WHERE kind='user' AND asset_type='game' AND (balance_sign<>1 OR balance_mag<>(SELECT balance_mag FROM credit_accounts WHERE kind='user' AND asset_type='game' AND user_id=?))`, f.users[4105].UserID).Scan(&wrong); err != nil || wrong != 0 {
		t.Fatal("refund wallet mismatch", wrong, err)
	}
	f.recovery()
	t.Logf("4104 funded entries, 4096 waiters, admission=%s, complete drain and ledger recovery=%s", admitted, time.Since(started))
}

func TestBlackjackFutureLedgerExhaustionRollsBackAdmission(t *testing.T) {
	f := newFixture(t, 1)
	config := f.read(0).ConfigHash
	var previous int64
	if err := f.db.QueryRow(`SELECT last_ledger_seq FROM credit_capacity WHERE id=1`).Scan(&previous); err != nil {
		t.Fatal(err)
	}
	f.exec(`UPDATE credit_capacity SET last_ledger_seq=? WHERE id=1`, int64(math.MaxInt64-1))
	_, err := f.s.Enqueue(f.ctx, blackjack.EnqueueInput{Identity: f.users[0], Key: f.id("op_"), Stake: "5000", ConfigHash: config})
	if !errors.Is(err, blackjack.ErrUnavailable) {
		t.Fatal("future settlement capacity was not checked", err)
	}
	var count int
	if err := f.db.QueryRow(`SELECT COUNT(*) FROM game_blackjack_entries`).Scan(&count); err != nil || count != 0 {
		t.Fatal("partial admission persisted", count, err)
	}
	f.exec(`UPDATE credit_capacity SET last_ledger_seq=? WHERE id=1`, previous)
	f.recovery()
}
