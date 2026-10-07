package duel_test

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/waiting-here/NonbiriAPI/internal/game/duel"
	"github.com/waiting-here/NonbiriAPI/internal/game/gwent/engine"
	"github.com/waiting-here/NonbiriAPI/internal/game/rating"
)

func TestGwentSimultaneousMulliganResumeAndDrawRefund(t *testing.T) {
	f := newFixture(t, "gwent")
	first := f.matched()
	other := *f.read(1).Current
	if first.Phase != "mulligan" || first.DecisionID == "" || other.DecisionID == "" {
		t.Fatal(first, other)
	}
	f.action(0, first, `{"kind":"continue"}`)
	// The opponent's still-open decision remains valid despite the new revision.
	f.action(1, other, `{"kind":"continue"}`)
	old := *f.read(0).Current
	if old.Phase != "turn" {
		t.Fatal(old)
	}
	if err := f.s.Close(); err != nil {
		t.Fatal(err)
	}
	f.clock.Store(1000)
	restarted, err := duel.New(f.options)
	if err != nil {
		t.Fatal(err)
	}
	f.s = restarted
	t.Cleanup(func() { restarted.Close() })
	if _, err := restarted.RecoverBeforeListenAt(f.ctx, 1000, 100, time.Now().Add(2*time.Second)); err != nil {
		t.Fatal(err)
	}
	current := *f.read(0).Current
	if string(current.View) != string(old.View) || current.DecisionID == old.DecisionID && !current.Locked[current.You] || *current.Deadline != 1030 {
		t.Fatal("restart changed cards or retained stale decision", old, current)
	}
	if _, err := restarted.Action(f.ctx, duel.ActionInput{Identity: f.identity(0), IdempotencyKey: f.key(), SessionID: old.ID, PhaseSeq: old.PhaseSeq, DecisionID: old.DecisionID, Action: []byte(`{"kind":"pass"}`)}); !errors.Is(err, duel.ErrConflict) {
		t.Fatal("pre-restart action accepted", err)
	}
	for range 8 {
		home := f.read(0)
		if home.Current == nil {
			break
		}
		seat := 0
		if home.Current.Locked[home.Current.You] {
			seat = 1
		}
		f.action(seat, *f.read(seat).Current, `{"kind":"pass"}`)
	}
	home := f.read(0)
	if home.Current != nil || home.LatestResult == nil || home.LatestResult.Outcome != "draw" || home.LatestResult.OwnRefund.Game != "3" || home.LatestResult.OwnRefund.General != "2" {
		t.Fatal(home)
	}
	var count int
	if err := f.db.QueryRow(`SELECT count(*) FROM game_duel_results WHERE result=1 AND rating_before=1500 AND rating_after=1500`).Scan(&count); err != nil || count != 2 {
		t.Fatal(count, err)
	}
	f.ledger()
}

func TestGwentInitiativeChoicePrecedesMulligan(t *testing.T) {
	f := newFixture(t, "gwent")
	f.loadouts[0], _ = json.Marshal(engine.StarterDeck("gemini"))
	state := f.matched()
	if state.Phase != "choice" || state.Locked[state.You] || *state.Deadline != 115 {
		t.Fatal("initiative window", state)
	}
	var view engine.View
	if err := json.Unmarshal(state.View, &view); err != nil || view.Choice == nil || view.Choice.Kind != "initiative" {
		t.Fatal(view, err)
	}
	var action engine.Action
	for _, candidate := range view.Legal {
		if candidate.Card == view.Self.Leader.InstanceID {
			action = candidate
		}
	}
	if action.Kind == "" {
		t.Fatal("cannot choose own initiative")
	}
	body, _ := json.Marshal(action)
	f.action(0, state, string(body))
	for seat := range 2 {
		current := *f.read(seat).Current
		if current.Phase != "mulligan" || current.Locked[current.You] {
			t.Fatal("missing simultaneous opening hand", current)
		}
		f.action(seat, current, `{"kind":"continue"}`)
	}
	current := *f.read(0).Current
	if current.Phase != "turn" || current.Locked[current.You] {
		t.Fatal("initiative choice was lost", current)
	}
	f.ledger()
}

func TestGwentSequentialActionRetryAndCompetitiveSettlement(t *testing.T) {
	f := newFixture(t, "gwent")
	f.matched()
	for seat := range 2 {
		f.action(seat, *f.read(seat).Current, `{"kind":"continue"}`)
	}
	state := *f.read(0).Current
	seat := 0
	if state.Locked[state.You] {
		seat = 1
		state = *f.read(seat).Current
	}
	var view engine.View
	if err := json.Unmarshal(state.View, &view); err != nil {
		t.Fatal(err)
	}
	var action engine.Action
	for _, a := range view.Legal {
		if a.Kind == "play" {
			action = a
			break
		}
	}
	if action.Kind == "" {
		t.Fatal("no opening play")
	}
	body, _ := json.Marshal(action)
	in := duel.ActionInput{Identity: f.identity(seat), IdempotencyKey: f.key(), SessionID: state.ID, PhaseSeq: state.PhaseSeq, DecisionID: state.DecisionID, Action: body}
	if _, err := f.s.Action(f.ctx, in); err != nil {
		t.Fatal(err)
	}
	if result, err := f.s.Action(f.ctx, in); err != nil || !result.Replayed {
		t.Fatal(result, err)
	}
	in.IdempotencyKey = f.key()
	if _, err := f.s.Action(f.ctx, in); !errors.Is(err, duel.ErrConflict) {
		t.Fatal("stale choice accepted", err)
	}
	loser := 1 - seat
	state = *f.read(loser).Current
	if _, err := f.s.Surrender(f.ctx, duel.ActionInput{Identity: f.identity(loser), IdempotencyKey: f.key(), SessionID: state.ID, PhaseSeq: state.PhaseSeq}); err != nil {
		t.Fatal(err)
	}
	tx, err := f.db.BeginTx(f.ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	if err := rating.RecordTx(f.ctx, tx, "gwent", state.ID); err != nil {
		t.Fatal(err)
	}
	board, err := rating.ReadTx(f.ctx, tx, "gwent", f.users[seat], "7d", 100)
	if err != nil || len(board.Rows) != 1 || board.Rows[0].Wins != "1" || !board.Rows[0].IsMe {
		t.Fatal(board, err)
	}
	var value, played int
	if err := tx.QueryRow(`SELECT rating,played FROM game_duel_ratings WHERE user_id=?`, f.users[seat]).Scan(&value, &played); err != nil || value != 1516 || played != 1 {
		t.Fatal(value, played, err)
	}
	tx.Rollback()
	f.ledger()
}

func TestGwentCompetitiveExportAndRetention(t *testing.T) {
	f := newFixture(t, "gwent")
	state := f.matched()
	if _, err := f.s.Surrender(f.ctx, duel.ActionInput{Identity: f.identity(1), IdempotencyKey: f.key(), SessionID: state.ID, PhaseSeq: state.PhaseSeq}); err != nil {
		t.Fatal(err)
	}
	tx, err := f.db.BeginTx(f.ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	value, _, err := f.s.ExportTx(f.ctx, tx, f.users[0], 100, 100)
	if err != nil {
		t.Fatal(err)
	}
	exported := value.(duel.Export)
	if exported.Competitive == nil || exported.Competitive.Rating != 1516 || len(exported.History) != 1 || exported.History[0].Competitive == nil || exported.History[0].Competitive.RatingBefore != 1500 || exported.History[0].Competitive.RatingAfter != 1516 {
		t.Fatalf("incomplete own rating export: %+v", exported)
	}
	board, err := rating.ReadTx(f.ctx, tx, "gwent", f.users[0], "7d", 100)
	if err != nil {
		t.Fatal(err)
	}
	body, _ := json.Marshal(board)
	if strings.Contains(string(body), "rating") || strings.Contains(string(body), "user_id") {
		t.Fatal("private ranking fields entered public response", string(body))
	}
	board, err = rating.ReadTx(f.ctx, tx, "gwent", f.users[0], "7d", 100+7*86400)
	if err != nil || len(board.Rows) != 0 {
		t.Fatal("expired weekly result", board, err)
	}
	tx.Rollback()
	f.clock.Store(100 + 30*86400)
	if result, err := f.s.Retain(f.ctx, f.clock.Load(), 100, time.Now().Add(2*time.Second)); err != nil || result.Processed != 1 {
		t.Fatal(result, err)
	}
	var results, ratings, anonymous int
	if err := f.db.QueryRow(`SELECT (SELECT count(*) FROM game_duel_results),(SELECT count(*) FROM game_duel_ratings),(SELECT count(*) FROM game_duel_anonymous)`).Scan(&results, &ratings, &anonymous); err != nil || results != 0 || ratings != 2 || anonymous != 1 {
		t.Fatal("retention must expire matches but preserve accumulated strength", results, ratings, anonymous, err)
	}
	f.ledger()
}

func TestGwentAccountRemovalErasesRatingsTransactionally(t *testing.T) {
	f := newFixture(t, "gwent")
	state := f.matched()
	if _, err := f.s.Surrender(f.ctx, duel.ActionInput{Identity: f.identity(1), IdempotencyKey: f.key(), SessionID: state.ID, PhaseSeq: state.PhaseSeq}); err != nil {
		t.Fatal(err)
	}
	for _, commit := range []bool{false, true} {
		tx, err := f.db.BeginTx(f.ctx, nil)
		if err != nil {
			t.Fatal(err)
		}
		end, err := f.s.PrepareDeleteTx(f.ctx, tx, f.users[0], 100)
		if err != nil {
			t.Fatal(err)
		}
		if commit {
			if err := tx.Commit(); err != nil {
				t.Fatal(err)
			}
			end.Commit()
		} else {
			tx.Rollback()
			end.Abort()
		}
		var results, ratings int
		if err := f.db.QueryRow(`SELECT (SELECT count(*) FROM game_duel_results WHERE user_id=?),(SELECT count(*) FROM game_duel_ratings WHERE user_id=?)`, f.users[0], f.users[0]).Scan(&results, &ratings); err != nil {
			t.Fatal(err)
		}
		want := 1
		if commit {
			want = 0
		}
		if results != want || ratings != want {
			t.Fatal("removal escaped transaction", commit, results, ratings)
		}
	}
	var peerResults int
	if err := f.db.QueryRow(`SELECT count(*) FROM game_duel_results WHERE user_id=?`, f.users[1]).Scan(&peerResults); err != nil || peerResults != 1 {
		t.Fatal("removed opponent result", peerResults, err)
	}
	f.ledger()
}

func TestGwentSystemCancellationDoesNotRank(t *testing.T) {
	f := newFixture(t, "gwent")
	f.matched()
	tx, err := f.db.BeginTx(f.ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	end, err := f.s.PrepareDeleteTx(f.ctx, tx, f.users[0], 100)
	if err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	end.Commit()
	var results, ratings int
	if err := f.db.QueryRow(`SELECT (SELECT count(*) FROM game_duel_results),(SELECT count(*) FROM game_duel_ratings)`).Scan(&results, &ratings); err != nil || results != 0 || ratings != 0 {
		t.Fatal(results, ratings, err)
	}
	if result := f.read(1).LatestResult; result == nil || result.Outcome != "system_cancelled" || result.OwnRefund.Game != "4" || result.OwnRefund.General != "1" {
		t.Fatal(result)
	}
	f.ledger()
}
