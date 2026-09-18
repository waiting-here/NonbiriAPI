package duel_test

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/waiting-here/NonbiriAPI/internal/game/duel"
	likeengine "github.com/waiting-here/NonbiriAPI/internal/game/likes/engine"
)

const basicPlan = `{"kind":"plan","plan":{"purchases":[],"main":{"skillId":"PUB01"},"extra":[]}}`

func TestAnonymousBiddingRetainsDecksWithoutParticipantDisclosure(t *testing.T) {
	f := newFixture(t, "bidding")
	state := f.matched()
	var commitment string
	if err := f.db.QueryRow(`SELECT json_extract(private_json,'$.commitment') FROM game_random_proofs WHERE resource_id=?`, state.ID).Scan(&commitment); err != nil {
		t.Fatal(err)
	}
	if _, err := f.s.Surrender(f.ctx, duel.ActionInput{Identity: f.identity(0), IdempotencyKey: f.key(), SessionID: state.ID, PhaseSeq: state.PhaseSeq}); err != nil {
		t.Fatal(err)
	}
	detail, err := f.s.HistoryDetail(f.ctx, f.identity(0), state.ID)
	if err != nil {
		t.Fatal(err)
	}
	body, _ := json.Marshal(detail)
	if strings.Contains(string(body), "reward_decks") || strings.Contains(string(body), `"decks"`) {
		t.Fatal("unrevealed cards disclosed to participant")
	}
	f.clock.Store(100 + duel.RetentionSeconds)
	if _, err := f.s.Retain(f.ctx, f.clock.Load(), 100, time.Now().Add(2*time.Second)); err != nil {
		t.Fatal(err)
	}
	var raw string
	if err := f.db.QueryRow(`SELECT header_json FROM game_duel_anonymous`).Scan(&raw); err != nil {
		t.Fatal(err)
	}
	var archived struct {
		Initial struct {
			Decks [2][]int `json:"reward_decks"`
		} `json:"initial"`
	}
	if json.Unmarshal([]byte(raw), &archived) != nil || len(archived.Initial.Decks[0]) != 13 || len(archived.Initial.Decks[1]) != 13 || strings.Contains(raw, state.ID) {
		t.Fatal("anonymous gameplay facts lost or linked")
	}
	var proofs int
	if err := f.db.QueryRow(`SELECT COUNT(*) FROM game_random_proofs WHERE resource_id=?`, state.ID).Scan(&proofs); err != nil || proofs != 0 || strings.Contains(raw, commitment) {
		t.Fatal("anonymous history retained a random fingerprint", err)
	}
	f.ledger()
}

func TestHistoryFogPagingAcceptedPlansAndExactExpiry(t *testing.T) {
	f := newFixture(t, "likes")
	state := f.matched()
	f.action(0, state, basicPlan)
	page, err := f.s.Rounds(f.ctx, f.identity(0), state.ID, duel.PageInput{}, true)
	if err != nil || len(page.Items) != 0 {
		t.Fatal(page, err)
	}
	if _, err := f.s.HistoryDetail(f.ctx, f.identity(0), state.ID); !errors.Is(err, duel.ErrNotFound) {
		t.Fatal(err)
	}
	f.action(1, state, basicPlan)
	for user := range 2 {
		page, err = f.s.Rounds(f.ctx, f.identity(user), state.ID, duel.PageInput{Limit: 1}, true)
		if err != nil || len(page.Items) != 1 {
			t.Fatal(page, err)
		}
		var view likeengine.View
		if json.Unmarshal(page.Items[0].Before, &view) != nil || !view.Players[1-f.read(user).Current.You].Fog || len(view.Players[1-f.read(user).Current.You].Loadout) != 0 {
			t.Fatal("round leaked an unplayed loadout")
		}
	}
	f.clock.Store(*f.read(0).Current.Deadline)
	state = *f.read(0).Current
	if state.RoundStart == nil || state.RoundStart.StartedAt != f.clock.Load() || state.RoundStart.Round != 2 || !strings.Contains(string(state.RoundStart.Events), `"before"`) || !strings.Contains(string(state.RoundStart.Events), `"after"`) {
		t.Fatal("round-start resource facts missing")
	}
	f.action(0, state, basicPlan)
	f.action(1, state, basicPlan)
	page, err = f.s.Rounds(f.ctx, f.identity(0), state.ID, duel.PageInput{Limit: 1}, true)
	if err != nil || len(page.Items) != 1 || page.NextCursor == nil {
		t.Fatal(page, err)
	}
	if _, err := f.s.Rounds(f.ctx, f.identity(1), state.ID, duel.PageInput{Cursor: *page.NextCursor}, true); !errors.Is(err, duel.ErrInvalidRequest) {
		t.Fatal("cross-viewer cursor", err)
	}
	second, err := f.s.Rounds(f.ctx, f.identity(0), state.ID, duel.PageInput{Cursor: *page.NextCursor}, true)
	if err != nil || len(second.Items) != 1 || second.Items[0].Round != 2 || len(second.Items[0].StartEvents) == 0 || second.NextCursor != nil {
		t.Fatal(second, err)
	}
	f.clock.Store(*f.read(0).Current.Deadline)
	state = *f.read(0).Current
	f.action(0, state, basicPlan)
	if _, err := f.s.Surrender(f.ctx, duel.ActionInput{Identity: f.identity(1), IdempotencyKey: f.key(), SessionID: state.ID, PhaseSeq: state.PhaseSeq}); err != nil {
		t.Fatal(err)
	}
	detail, err := f.s.HistoryDetail(f.ctx, f.identity(0), state.ID)
	if err != nil {
		t.Fatal(err)
	}
	if string(detail.TerminalActions[state.You]) != basicPlan {
		t.Fatal("accepted unfinished plan lost", string(detail.TerminalActions[state.You]))
	}
	var initial likeengine.View
	if json.Unmarshal(detail.Initial, &initial) != nil || initial.Players[1-state.You].Fog || len(initial.Players[1-state.You].Loadout) == 0 {
		t.Fatal("terminal loadout missing")
	}
	f.ledger()
	f.clock.Add(duel.RetentionSeconds)
	if _, err := f.s.HistoryDetail(f.ctx, f.identity(0), state.ID); !errors.Is(err, duel.ErrNotFound) {
		t.Fatal("expired detail", err)
	}
	if _, err := f.s.Rounds(f.ctx, f.identity(0), state.ID, duel.PageInput{Cursor: *page.NextCursor}, true); !errors.Is(err, duel.ErrNotFound) {
		t.Fatal("expired rounds", err)
	}
	list, err := f.s.History(f.ctx, f.identity(0), duel.PageInput{})
	if err != nil || len(list.Items) != 0 {
		t.Fatal(list, err)
	}
	if _, err := f.s.Retain(f.ctx, f.clock.Load(), 100, time.Now().Add(2*time.Second)); err != nil {
		t.Fatal(err)
	}
	if _, err := f.s.Retain(f.ctx, f.clock.Load(), 100, time.Now().Add(2*time.Second)); err != nil {
		t.Fatal(err)
	}
	var count int
	var archive, header string
	if err := f.db.QueryRow(`SELECT COUNT(*),archive_id,header_json FROM game_duel_anonymous`).Scan(&count, &archive, &header); err != nil || count != 1 || archive == state.ID {
		t.Fatal(count, archive, err)
	}
	for _, forbidden := range []string{state.ID, `"user_id"`, `"started_at"`, `"ends_at"`, `"terminal_at"`, `"payment"`, `"deadline"`, `"operation"`} {
		if strings.Contains(header, forbidden) {
			t.Fatal("anonymous header retained", forbidden)
		}
	}
	f.ledger()
}

func TestDeleteCancellationRollbackAndDeidentification(t *testing.T) {
	f := newFixture(t, "likes")
	state := f.matched()
	f.action(0, state, basicPlan)
	f.action(1, state, basicPlan)
	tx, err := f.db.BeginTx(f.ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	end, err := f.s.PrepareDeleteTx(f.ctx, tx, f.users[0], 100)
	if err != nil {
		t.Fatal(err)
	}
	if err := tx.Rollback(); err != nil {
		t.Fatal(err)
	}
	end.Abort()
	if f.read(0).Current == nil || f.read(0).Current.Phase != "settlement" {
		t.Fatal("rollback cancelled match")
	}
	f.ledger()
	tx, err = f.db.BeginTx(f.ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	end, err = f.s.PrepareDeleteTx(f.ctx, tx, f.users[0], 100)
	if err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	end.Commit()
	if f.read(0).Current != nil || f.read(0).LatestResult != nil {
		t.Fatal("deleted participant retained lookup")
	}
	other := f.read(1).LatestResult
	if other == nil || other.Outcome != "system_cancelled" || other.Reason != "account_unavailable" || other.OwnRefund.Game != "4" || other.Resolution != nil {
		t.Fatal(other)
	}
	f.ledger()
	tx, err = f.db.BeginTx(f.ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	end, err = f.s.PrepareDeleteTx(f.ctx, tx, f.users[1], 100)
	if err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	end.Commit()
	var originals, archives int
	if err := f.db.QueryRow(`SELECT (SELECT COUNT(*) FROM game_duel_sessions),(SELECT COUNT(*) FROM game_duel_anonymous)`).Scan(&originals, &archives); err != nil || originals != 0 || archives != 1 {
		t.Fatal(originals, archives, err)
	}
	f.ledger()
}
