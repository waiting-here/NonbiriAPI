package official

import (
	"math/rand"
	"testing"

	"github.com/waiting-here/NonbiriAPI/internal/game/gwent/engine"
)

func TestGwentPlaysVisibleWindowsToCompletion(t *testing.T) {
	for seed := range 2 {
		random := rand.New(rand.NewSource(42 + int64(seed)))
		state, err := engine.New([2]engine.Deck{engine.StarterDeck("claude"), engine.StarterDeck("gemini")}, random)
		if err != nil {
			t.Fatal(err)
		}
		for steps := 0; state.Result == nil; steps++ {
			if steps >= 500 {
				t.Fatal("game stalled")
			}
			waiting := state.Required()
			for seat, need := range waiting {
				if !need {
					continue
				}
				view, err := engine.Project(state, seat)
				if err != nil {
					t.Fatal(err)
				}
				action, err := gwentAction(view)
				if err != nil {
					t.Fatal(err)
				}
				state, err = engine.Apply(state, seat, action, random)
				if err != nil {
					t.Fatalf("%s %+v: %v", view.Phase, action, err)
				}
			}
		}
	}
}

func TestGwentKeepsCardsAfterOpponentPasses(t *testing.T) {
	view := engine.View{Self: engine.PlayerView{Lives: 1}, Enemy: engine.PlayerView{Passed: true},
		Board: []engine.RowView{{Side: "self", Total: 12}, {Side: "enemy", Total: 10}},
		Legal: []engine.Action{{Kind: "play", Card: 1}, {Kind: "pass"}}}
	action, err := gwentAction(view)
	if err != nil || action.Kind != "pass" {
		t.Fatalf("expected pass, got %+v: %v", action, err)
	}
}

func TestGwentPlaysOutMatchPoint(t *testing.T) {
	view := engine.View{Self: engine.PlayerView{Lives: 1, HandCount: 2}, Enemy: engine.PlayerView{HandCount: 3},
		Board: []engine.RowView{{Side: "self", Total: 16}, {Side: "enemy", Total: 2}},
		Hand:  []engine.CardView{{InstanceID: 1, BasePower: 6}},
		Legal: []engine.Action{{Kind: "play", Card: 1}, {Kind: "pass"}}}
	action, err := gwentAction(view)
	if err != nil || action.Kind != "play" {
		t.Fatalf("unexpected last-life action %+v: %v", action, err)
	}
}
