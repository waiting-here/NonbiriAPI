package blackjack_test

import (
	"bytes"
	"database/sql"
	"encoding/json"
	"errors"
	"testing"

	"github.com/waiting-here/NonbiriAPI/internal/game/blackjack"
	"github.com/waiting-here/NonbiriAPI/internal/game/randomness"
	"github.com/waiting-here/NonbiriAPI/internal/ledger"
)

func TestThirtySecondBoundariesDoNotCreateEmptyOrMissedTables(t *testing.T) {
	f := newFixture(t, 1)
	for _, tc := range []struct {
		now, deadline, next int64
		phase               string
	}{
		{120, 125, 150, "seating"}, {124, 125, 150, "seating"},
		{125, 145, 150, "decision"}, {144, 145, 150, "decision"},
		{145, 150, 150, "result"}, {149, 150, 150, "result"}, {150, 155, 180, "seating"},
	} {
		f.clock.Store(tc.now)
		h := f.read(0)
		if h.Phase != tc.phase || h.Deadline != tc.deadline || h.NextRoundAt != tc.next || h.Table != nil {
			t.Fatalf("time %d: %+v", tc.now, h)
		}
	}
	f.clock.Store(175)
	receipt := f.join(0)
	f.clock.Store(232)
	h := f.read(0)
	if h.Table != nil || h.You == nil || h.You.ID != receipt.ID || h.You.State != "waiting" {
		t.Fatal("missed seating window created a paid table", h)
	}
	f.s.Close()
	f.s = f.service()
	if h = f.read(0); h.Table != nil || h.You.State != "waiting" {
		t.Fatal("restart changed waiting entry", h)
	}
	f.clock.Store(240)
	h = f.read(0)
	if h.Table == nil || h.Table.StartedAt != 240 || h.You.ID != receipt.ID || h.Deadline != 245 {
		t.Fatal("current window did not admit the original entry", h)
	}
	var n int
	if err := f.db.QueryRow("SELECT COUNT(*) FROM game_blackjack_sessions").Scan(&n); err != nil || n != 1 {
		t.Fatal("created past tables", n, err)
	}
	f.recovery()
}

func TestQuickStakeHashChangesPreserveAcceptedTermsAndReplay(t *testing.T) {
	f := newFixture(t, 2)
	f.clock.Store(146)
	original := f.read(0)
	input := blackjack.EnqueueInput{Identity: f.users[0], Key: f.id("op_"), Stake: "5000", ConfigHash: original.ConfigHash}
	receipt, err := f.s.Enqueue(f.ctx, input)
	if err != nil {
		t.Fatal(err)
	}
	terms, _ := json.Marshal(f.read(0).You)
	before := f.balance(1, ledger.Game)
	f.exec(`UPDATE site_config SET value='["1000","10000"]' WHERE key='game_blackjack_quick_stakes'`)
	if f.read(1).ConfigHash == original.ConfigHash {
		t.Fatal("quick stakes omitted from hash")
	}
	_, err = f.s.Enqueue(f.ctx, blackjack.EnqueueInput{Identity: f.users[1], Key: f.id("op_"), Stake: "5000", ConfigHash: original.ConfigHash})
	if !errors.Is(err, blackjack.ErrConflict) {
		t.Fatal("stale hash accepted", err)
	}
	if f.balance(1, ledger.Game) != before || f.read(1).You != nil {
		t.Fatal("stale request changed funds or queue")
	}
	replay, err := f.s.Enqueue(f.ctx, input)
	if err != nil || !replay.Replayed || !bytes.Equal(replay.Body, receipt.Body) {
		t.Fatal("accepted request cannot replay", err)
	}
	after, _ := json.Marshal(f.read(0).You)
	if !bytes.Equal(terms, after) || bytes.Contains(after, []byte("quick_stakes")) {
		t.Fatal("existing entry terms changed")
	}
	f.s.Close()
	f.s = f.service()
	after, _ = json.Marshal(f.read(0).You)
	if !bytes.Equal(terms, after) {
		t.Fatal("restart changed accepted terms")
	}
	f.recovery()
}

func TestRecoveryAcceptsPendingActionsFromFormerSchedule(t *testing.T) {
	f := newFixture(t, 2)
	f.join(0)
	f.join(1)
	id := f.installPairTable()
	if _, err := f.s.Act(f.ctx, f.intent(0, 0, "stand")); err != nil {
		t.Fatal(err)
	}
	// The old decision window accepted this batch. Startup must validate it
	// before recovery cancels and refunds the unfinished table.
	f.exec(`UPDATE game_blackjack_sessions SET last_batch=163,revision=revision+1 WHERE id=?`, id)
	f.exec(`UPDATE game_blackjack_entries SET pending_batch=164 WHERE session_id=? AND pending_json IS NOT NULL`, id)
	f.clock.Store(164)
	f.s.Close()
	f.s = f.service()
	for i := range 2 {
		if f.balance(i, ledger.Game) != 25_000_000 || f.balance(i, ledger.General) != 100_000_000 {
			t.Fatal("old pending action did not refund original funds")
		}
	}
	var phase string
	if err := f.db.QueryRow(`SELECT phase FROM game_blackjack_sessions WHERE id=?`, id).Scan(&phase); err != nil || phase != "cancelled" {
		t.Fatal("old table survived recovery", err)
	}
	f.recovery()
}

func TestPublicProofExpiresAtNextRoundWhileParticipantRetainsHistory(t *testing.T) {
	f := newFixture(t, 2)
	f.join(0)
	id := f.read(0).Table.ID
	secret, err := randomness.New("blackjack", id, "six-decks-s17-v1", nil)
	if err != nil {
		t.Fatal(err)
	}
	tx, err := f.db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	if err := randomness.Insert(f.ctx, tx, secret); err != nil {
		tx.Rollback()
		t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	f.clock.Store(145)
	f.read(0)
	for _, tc := range []struct {
		user    int
		now     int64
		allowed bool
	}{{1, 149, true}, {1, 150, false}, {0, 150, true}} {
		tx, err := f.db.Begin()
		if err != nil {
			t.Fatal(err)
		}
		proof, err := randomness.ReadForUser(f.ctx, tx, "blackjack", id, f.users[tc.user].UserID, tc.now)
		tx.Rollback()
		if tc.allowed && (err != nil || proof == nil) || !tc.allowed && !errors.Is(err, sql.ErrNoRows) {
			t.Fatal("public round boundary or participant history changed", tc, err)
		}
	}
}
