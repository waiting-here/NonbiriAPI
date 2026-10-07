package gwent

import (
	"context"
	"encoding/json"
	"math/rand/v2"
	"testing"

	"github.com/waiting-here/NonbiriAPI/internal/game/ai"
	"github.com/waiting-here/NonbiriAPI/internal/game/gwent/engine"
)

func TestOriginalPresetPoliciesAndCompleteLocalGames(t *testing.T) {
	presets := engine.Presets()
	if len(presets) != 16 {
		t.Fatal(len(presets))
	}
	for i, p := range presets {
		if err := engine.ValidateDeck(p.Deck); err != nil {
			t.Fatal(p.Deck.Faction, p.ID, err)
		}
		raw, _ := json.Marshal(Policy{policySchema, p.Deck.Faction, p.ID})
		if _, err := (AIAdapter{}).Compile(raw); err != nil {
			t.Fatal(err)
		}
		s, err := engine.New([2]engine.Deck{p.Deck, presets[(i+5)%len(presets)].Deck}, randomReader{rand.New(rand.NewPCG(uint64(i), 7))})
		if err != nil {
			t.Fatal(err)
		}
		for move := 0; s.Result == nil && move < 512; move++ {
			required := s.Required()
			seat := 0
			if !required[0] {
				seat = 1
			}
			wrapper, _ := json.Marshal(state{Game: s, Windows: [2]uint64{1, 1}})
			request, err := (AIAdapter{}).Request(wrapper, seat)
			if err != nil {
				t.Fatal(err)
			}
			request.Random = rand.New(rand.NewPCG(uint64(move), uint64(i)))
			request.DecisionID = "test"
			result, err := (AIAdapter{}).Source().Decide(context.Background(), request)
			if err != nil {
				t.Fatalf("%s/%s move %d phase %s: %v", p.Deck.Faction, p.ID, move, s.Phase(), err)
			}
			choice, err := ai.ResolveChoice(request, result)
			if err != nil {
				t.Fatal(err)
			}
			s, _, err = engine.ApplyWithRounds(s, seat, choice.(engine.Action), randomReader{rand.New(rand.NewPCG(uint64(move), 19))})
			if err != nil {
				t.Fatal(err)
			}
		}
		if s.Result == nil {
			t.Fatal("local game did not terminate", p)
		}
	}
}
func TestDecisionObservationDoesNotExposeEnemyHiddenCards(t *testing.T) {
	s, err := engine.New([2]engine.Deck{engine.StarterDeck("openai"), engine.StarterDeck("deepseek")}, randomReader{rand.New(rand.NewPCG(7, 8))})
	if err != nil {
		t.Fatal(err)
	}
	wrapper, _ := json.Marshal(state{Game: s, Windows: [2]uint64{1, 1}})
	request, err := (AIAdapter{}).Request(wrapper, 0)
	if err != nil {
		t.Fatal(err)
	}
	o := request.Observation.(observation).State
	for _, id := range append(append([]int{}, s.Players[1].Hand...), s.Players[1].Deck...) {
		if o.Cards[id-1].Definition != "" {
			t.Fatal("hidden identity", id)
		}
	}
	if len(o.Players[1].Hand) != len(s.Players[1].Hand) || len(o.Players[1].Deck) != len(s.Players[1].Deck) {
		t.Fatal("public counts lost")
	}
	changed := s
	body, _ := json.Marshal(s)
	_ = json.Unmarshal(body, &changed)
	for _, id := range append(append([]int{}, changed.Players[1].Hand...), changed.Players[1].Deck...) {
		changed.Cards[id-1].Definition = "hidden"
	}
	a, _ := json.Marshal(engine.DecisionState(s, 0))
	b, _ := json.Marshal(engine.DecisionState(changed, 0))
	if string(a) != string(b) {
		t.Fatal("hidden identities affect observation")
	}
}
