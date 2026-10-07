package gwent

import (
	"encoding/json"
	"reflect"
	"testing"

	"github.com/waiting-here/NonbiriAPI/internal/game/duel"
	"github.com/waiting-here/NonbiriAPI/internal/game/gwent/engine"
)

type fixedRandom struct{}

func (fixedRandom) Read(b []byte) (int, error) { clear(b); return len(b), nil }

func initial(t *testing.T) json.RawMessage {
	t.Helper()
	value, err := (Rules{}).CreateWithRandom("standard", [2]json.RawMessage{}, fixedRandom{})
	if err != nil {
		t.Fatal(err)
	}
	return value
}

func takeStep(t *testing.T, raw json.RawMessage, seat int, action engine.Action, timeout bool) duel.StepTransition {
	t.Helper()
	body, err := json.Marshal(action)
	if err != nil {
		t.Fatal(err)
	}
	next, err := (Rules{}).Step("standard", raw, seat, body, timeout, fixedRandom{})
	if err != nil {
		t.Fatal(err)
	}
	return next
}

func TestIndependentMulligansPreserveOtherWindowAndDeadline(t *testing.T) {
	r := Rules{}
	raw := initial(t)
	ids, _ := r.Decisions("standard", raw)
	s, _ := r.state("standard", raw)
	next := takeStep(t, raw, 0, s.Game.Legal(0)[0], false)
	after, _ := r.Decisions("standard", next.State)
	if !next.KeepDeadline || ids[0] == after[0] || ids[1] != after[1] {
		t.Fatalf("mulligan invalidated peer or extended timer: before=%v after=%v keep=%t", ids, after, next.KeepDeadline)
	}
	peer, _ := r.state("standard", next.State)
	if _, err := r.Accept("standard", next.State, 1, mustJSON(t, peer.Game.Legal(1)[0])); err != nil {
		t.Fatalf("peer choice lost after other seat acted: %v", err)
	}
	resumed, err := r.Resume("standard", next.State)
	if err != nil {
		t.Fatal(err)
	}
	resumedState, _ := r.state("standard", resumed)
	resumedIDs, _ := r.Decisions("standard", resumed)
	if !reflect.DeepEqual(resumedState.Game, peer.Game) || resumedIDs[0] == after[0] || resumedIDs[1] == after[1] {
		t.Fatal("restart changed cards or reused an old decision ID")
	}
}

func mustJSON(t *testing.T, value any) json.RawMessage {
	t.Helper()
	body, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return body
}

func TestCompletedRoundsKeepContiguousCompactHistory(t *testing.T) {
	r := Rules{}
	raw := initial(t)
	var records []duel.CompletedRound
	for step := 0; step < 100; step++ {
		s, err := r.state("standard", raw)
		if err != nil {
			t.Fatal(err)
		}
		if s.Game.Result != nil {
			if len(records) != 2 {
				t.Fatalf("draw should finish in two rounds: %d", len(records))
			}
			return
		}
		for seat, required := range s.Game.Required() {
			if !required {
				continue
			}
			// Finish mulligans, then pass both sides to complete a drawn match.
			actions := s.Game.Legal(seat)
			action := actions[len(actions)-1]
			if s.Game.Phase() == "turn" {
				action = engine.Action{Kind: "pass"}
			}
			next := takeStep(t, raw, seat, action, false)
			for _, record := range next.Rounds {
				before, err := r.Inspect("standard", record.Before)
				if err != nil || before.Round != len(records)+1 || before.Result != nil {
					t.Fatalf("invalid round start: %+v %v", before, err)
				}
				after, err := r.Inspect("standard", record.After)
				if err != nil || after.Round < record.Round || after.Round > record.Round+1 {
					t.Fatalf("invalid round end: %+v %v", after, err)
				}
				if _, err := r.RoundView("standard", record.Facts, 0, true); err != nil {
					t.Fatal(err)
				}
				records = append(records, record)
			}
			raw = next.State
			break
		}
	}
	t.Fatal("match did not terminate")
}

func TestThirdConsecutiveTurnTimeoutForfeits(t *testing.T) {
	r := Rules{}
	raw := initial(t)
	for {
		s, _ := r.state("standard", raw)
		if s.Game.Phase() == "turn" {
			seat := s.Game.Turn
			s.TimeoutStreak[seat] = 2
			raw = mustJSON(t, s)
			action, err := r.Automatic("standard", raw, seat)
			if err != nil {
				t.Fatal(err)
			}
			next, err := r.Step("standard", raw, seat, action, true, fixedRandom{})
			if err != nil {
				t.Fatal(err)
			}
			info, err := r.Inspect("standard", next.State)
			if err != nil || info.Result == nil || info.Result.Reason != "afk" || info.Result.Winner == nil || *info.Result.Winner != 1-seat {
				t.Fatalf("timeout did not forfeit: %+v %v", info, err)
			}
			return
		}
		for seat, required := range s.Game.Required() {
			if required {
				action, _ := r.Automatic("standard", raw, seat)
				next, err := r.Step("standard", raw, seat, action, true, fixedRandom{})
				if err != nil {
					t.Fatal(err)
				}
				raw = next.State
				break
			}
		}
	}
}
