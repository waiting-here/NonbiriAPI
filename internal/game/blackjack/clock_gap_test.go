package blackjack_test

import "testing"

func TestElapsedBatchesConsumeEachQueuedActionOnce(t *testing.T) {
	f := newFixture(t, 2)
	f.join(0)
	f.join(1)
	id := f.installPairTable()
	if _, err := f.s.Act(f.ctx, f.intent(0, 0, "hit")); err != nil {
		t.Fatal(err)
	}
	if _, err := f.s.Act(f.ctx, f.intent(1, 0, "stand")); err != nil {
		t.Fatal(err)
	}
	// The persisted intents can span separate batches after a delayed worker.
	f.exec(`UPDATE game_blackjack_entries SET pending_batch=130 WHERE session_id=? AND seat_no=1`, id)
	f.clock.Store(144)
	home := f.read(0)
	if home.Phase != "decision" || home.You.Pending {
		t.Fatalf("unexpected delayed result: %+v", home)
	}
	seats := home.Table.Fact.Cards.Seats
	if len(seats[0].Hands[0].Cards) != 3 || !seats[1].Hands[0].Stood {
		t.Fatalf("queued intents were repeated or skipped: %+v", seats)
	}
	var actions, pending int
	if err := f.db.QueryRow(`SELECT COUNT(*) FROM game_blackjack_events WHERE session_id=? AND kind='actions'`, id).Scan(&actions); err != nil || actions != 2 {
		t.Fatalf("action batches: %d, %v", actions, err)
	}
	if err := f.db.QueryRow(`SELECT COUNT(*) FROM game_blackjack_entries WHERE session_id=? AND pending_json IS NOT NULL`, id).Scan(&pending); err != nil || pending != 0 {
		t.Fatalf("pending intents: %d, %v", pending, err)
	}
	if again := f.read(0); again.Table.Revision != home.Table.Revision {
		t.Fatal("repeated read advanced the same actions twice")
	}
	f.clock.Store(145)
	if home := f.read(0); home.Phase != "result" {
		t.Fatal("decision deadline did not settle the table")
	}
	f.recovery()
}
