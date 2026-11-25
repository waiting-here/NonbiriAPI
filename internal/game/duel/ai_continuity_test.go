package duel_test

import (
	"strings"
	"testing"
)

func TestHistoricalChallengeDeletion(t *testing.T) {
	f := finishedChallengeFixture(t)
	var session, bot, challenge string
	if err := f.db.QueryRow("SELECT session_id,bot_id,challenge_id FROM game_ai_sessions").Scan(&session, &bot, &challenge); err != nil {
		t.Fatal(err)
	}
	tx, err := f.db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	done, err := f.s.PrepareDeleteTx(f.ctx, tx, f.users[0], f.clock.Load())
	if err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	done.Commit()
	var rows int
	if err := f.db.QueryRow("SELECT count(*) FROM game_ai_memories").Scan(&rows); err != nil || rows != 0 {
		t.Fatal(rows, err)
	}
	if err := f.db.QueryRow("SELECT count(*) FROM identity_continuity_facts").Scan(&rows); err != nil || rows != 1 {
		t.Fatal(rows, err)
	}
	var header string
	if err := f.db.QueryRow("SELECT header_json FROM game_duel_anonymous WHERE mode='ai'").Scan(&header); err != nil {
		t.Fatal(err)
	}
	for _, private := range []string{session, bot, challenge, "accepted_at", "phase_seq", "random_seed"} {
		if strings.Contains(header, private) {
			t.Fatal(private)
		}
	}
	if err := f.s.ValidatePersistedState(f.ctx); err != nil {
		t.Fatal(err)
	}
}
