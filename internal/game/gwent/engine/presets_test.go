package engine

import (
	"encoding/json"
	"slices"
	"testing"
)

func TestLocalContextTargetsSurviveEmptyDeckAndSerialization(t *testing.T) {
	s := fixture()
	ids := hand(&s, 0, "openai_infra_planner", "openai_infra_solver", "openai_infra_verifier")
	s.Choices[0] = &Choice{Kind: "context_window", Options: append([]int{}, ids...), Initial: append([]int{}, ids...), Remaining: 3, CanQuit: true}
	picked := map[int]bool{}
	for range 3 {
		body, _ := json.Marshal(s)
		_ = json.Unmarshal(body, &s)
		a, err := LocalChoice(DecisionState(s, 0), 0, zeroRandom{})
		if err != nil {
			t.Fatal(err)
		}
		if !slices.Contains(s.Legal(0), a) {
			t.Fatalf("illegal context choice: %+v", a)
		}
		if a.Kind != "choose" || picked[a.Card] {
			t.Fatal("redrew a returned card instead of the next original target", a)
		}
		picked[a.Card] = true
		s = apply(t, s, 0, a)
	}
	if len(picked) != 3 {
		t.Fatal(picked)
	}
}

func TestLocalChoiceRedrawsOrdinaryOpeningCard(t *testing.T) {
	for seat := range 2 {
		s := fixture()
		ids := hand(&s, seat, "openai_infra_planner")
		original := ids[0]
		d := s.definition(original)
		if d.Type != "unit" || len(d.Abilities) != 0 || d.Power >= 15 {
			t.Fatal("fixture must contain an ordinary redraw candidate", d)
		}
		replacement := s.makeCard("openai_infra_solver", seat)
		s.Players[seat].Deck = []int{replacement}
		s.Choices[seat] = &Choice{Kind: "mulligan", Options: ids, Remaining: 2, CanQuit: true}
		a, err := LocalChoice(DecisionState(s, seat), seat, zeroRandom{})
		if err != nil {
			t.Fatal(err)
		}
		if a != (Action{Kind: "redraw", Card: original}) || !slices.Contains(s.Legal(seat), a) {
			t.Fatalf("seat %d: expected legal redraw of ordinary card, got %+v", seat, a)
		}
		next, err := Apply(s, seat, a, fractionRandom(0.75))
		if err != nil {
			t.Fatal(err)
		}
		if !slices.Equal(next.Players[seat].Hand, []int{replacement}) || !slices.Equal(next.Players[seat].Deck, []int{original}) || next.Choices[seat].Remaining != 1 {
			t.Fatalf("seat %d: redraw did not replace the card or consume a selection: %+v", seat, next.Players[seat])
		}
	}
}

func TestLocalChoiceInitiativeChoosesOwnLeader(t *testing.T) {
	s, err := New([2]Deck{StarterDeck("openai"), StarterDeck("gemini")}, zeroRandom{})
	if err != nil {
		t.Fatal(err)
	}
	a, err := LocalChoice(DecisionState(s, 1), 1, zeroRandom{})
	if err != nil {
		t.Fatal(err)
	}
	if a != (Action{Kind: "choose", Card: s.Players[1].Leader}) || !slices.Contains(s.Legal(1), a) {
		t.Fatalf("illegal initiative choice: %+v", a)
	}
	s = apply(t, s, 1, a)
	if s.First != 1 || s.Stage != "mulligan" {
		t.Fatalf("initiative did not select own first turn: %+v", s)
	}
}
