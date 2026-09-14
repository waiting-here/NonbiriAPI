package bidding

import (
	"encoding/json"
	"testing"

	"github.com/waiting-here/NonbiriAPI/internal/game/duel"
)

func TestRuleAdapterCompletesGameWithoutExposingDecks(t *testing.T) {
	r := Rules{}
	s, err := r.Create("tier1", [2]json.RawMessage{})
	if err != nil {
		t.Fatal(err)
	}
	rounds := 0
	for steps := 0; steps < 30; steps++ {
		info, err := r.Inspect("tier1", s)
		if err != nil {
			t.Fatal(err)
		}
		for viewer := range 2 {
			body, err := r.View("tier1", s, viewer, info.Result != nil, nil)
			if err != nil {
				t.Fatal(err)
			}
			var fields map[string]any
			_ = json.Unmarshal(body, &fields)
			if len(fields) != 8 || fields["decks"] != nil {
				t.Fatal("unsafe projection", string(body))
			}
		}
		if info.Result != nil {
			if rounds != 13 || info.Result.Winner != nil {
				t.Fatal(info, rounds)
			}
			return
		}
		var actions [2]json.RawMessage
		for seat := range 2 {
			actions[seat], err = r.Automatic("tier1", s, seat)
			if err != nil {
				t.Fatal(err)
			}
		}
		next, err := r.Resolve("tier1", s, actions)
		if err != nil {
			t.Fatal(err)
		}
		if len(next.Record) > 0 {
			rounds++
			if _, err := r.RoundView("tier1", next.Record, 0, false); err != nil {
				t.Fatal(err)
			}
		}
		s = next.State
	}
	t.Fatal("game did not finish")
}

func TestBiddingActionShapeAndDealer(t *testing.T) {
	r := Rules{}
	s, _ := r.Create("tier1", [2]json.RawMessage{})
	for _, body := range []string{`{"kind":"joker","use":null}`, `{"kind":"joker","use":false,"card":null}`, `{"kind":"bid","card":1}`, `{"kind":"joker","use":false,"Use":true}`} {
		if _, err := r.Accept("tier1", s, 0, []byte(body)); err != duel.ErrInvalidRequest {
			t.Fatal(body, err)
		}
	}
	if _, err := r.Accept("tier1", s, 1, []byte(`{"kind":"joker","use":true}`)); err != duel.ErrInvalidRequest {
		t.Fatal(err)
	}
}
