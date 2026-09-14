package engine

import (
	"bytes"
	"encoding/json"
	"errors"
	mathrand "math/rand"
	"reflect"
	"slices"
	"testing"
)

func orderedGame(t *testing.T) State {
	t.Helper()
	state := State{Round: 1, JokerAvailable: [2]bool{true, true}}
	for side := range 2 {
		for index := range Rounds {
			state.Decks[side][index] = index + 1
			state.Hands[side] = append(state.Hands[side], index+1)
		}
		state.Played[side] = []int{}
	}
	beginRound(&state)
	if err := state.Validate(); err != nil {
		t.Fatal(err)
	}
	return state
}

func joker(t *testing.T, state State, use bool) State {
	t.Helper()
	if state.Phase != Joker {
		return state
	}
	next, err := DecideJoker(state, *Dealer(state.Round), use)
	if err != nil {
		t.Fatal(err)
	}
	return next
}

func bid(t *testing.T, state State, first, second int) (State, RoundRecord) {
	t.Helper()
	next, record, err := ResolveBids(state, [2]int{first, second})
	if err != nil {
		t.Fatalf("round %d: %v", state.Round, err)
	}
	return next, record
}

func TestDealerAndJokerSymmetry(t *testing.T) {
	counts := [2]int{}
	for round := 1; round <= Rounds; round++ {
		dealer := Dealer(round)
		if round == Rounds {
			if dealer != nil {
				t.Fatal("last round has a dealer")
			}
			continue
		}
		counts[*dealer]++
		if *dealer != (round-1)%2 {
			t.Fatal("incorrect dealer order")
		}
	}
	if counts != [2]int{6, 6} || Dealer(0) != nil || Dealer(14) != nil {
		t.Fatal("invalid dealer distribution")
	}
	state := orderedGame(t)
	if _, err := DecideJoker(state, 1, true); !errors.Is(err, ErrInvalidAction) {
		t.Fatal("non-dealer used joker")
	}
	state = joker(t, state, true)
	if state.Pool != 3 || state.Rewards[0].Multiplier != 2 || state.Rewards[1].Multiplier != 1 || state.JokerAvailable != [2]bool{false, true} {
		t.Fatal("red joker did not double only the red reward")
	}
	state, first := bid(t, state, 1, 1)
	if first.CarryAfter != 3 || first.JokerUsedBy == nil || *first.JokerUsedBy != 0 {
		t.Fatal("doubled reward lost in carry")
	}
	state = joker(t, state, true)
	if state.Pool != 9 || state.Rewards[2].Multiplier != 1 || state.Rewards[3].Multiplier != 2 || state.JokerAvailable != [2]bool{false, false} {
		t.Fatal("black joker did not double only the black reward")
	}
	state, second := bid(t, state, 3, 2)
	if second.FreshValue != 6 || second.CarryBefore != 3 || second.AwardedPoints != 9 || second.CarryAfter != 0 || len(second.ResolvedRewards) != 4 || state.Scores != [2]int{9, 0} {
		t.Fatalf("incorrect carry award: %+v", second)
	}
	if state.Phase != Bid {
		t.Fatal("spent joker caused another waiting phase")
	}
}

func TestTimeoutsCarryAndLastRoundDiscard(t *testing.T) {
	state := orderedGame(t)
	for round := 1; round <= Rounds; round++ {
		state = joker(t, state, false)
		cards := [2]int{}
		for seat := range 2 {
			var err error
			cards[seat], err = AutomaticBid(state, seat)
			if err != nil || cards[seat] != round {
				t.Fatalf("bad timeout card %v: %v", cards, err)
			}
		}
		next, record := bid(t, state, cards[0], cards[1])
		if record.AwardedTo != nil || record.AwardedPoints != 0 || record.FreshValue != round*2 {
			t.Fatalf("unexpected award: %+v", record)
		}
		if round < Rounds && (next.Result != nil || record.CarryAfter != round*(round+1)) {
			t.Fatal("tie must carry and play all rounds")
		}
		if round == Rounds && (record.CarryAfter != 0 || record.DiscardedPoints != 182 || len(record.ResolvedRewards) != 26) {
			t.Fatal("last tie must discard the entire pool")
		}
		state = next
	}
	if state.Phase != Terminal || state.Result == nil || state.Result.Winner != nil || state.Discard != 182 || state.Pool != 0 {
		t.Fatal("incorrect draw")
	}
	if _, _, err := ResolveBids(state, [2]int{1, 1}); !errors.Is(err, ErrInvalidAction) {
		t.Fatal("terminal game accepted another bid")
	}
}

func TestReversedHandsAndMaximumPoints(t *testing.T) {
	state := orderedGame(t)
	state.Decks[0][0], state.Decks[0][12] = 13, 1
	state.Decks[1][1], state.Decks[1][12] = 13, 2
	state.Rewards = nil
	state.Pool = 0
	beginRound(&state)
	for round := 1; round <= Rounds; round++ {
		state = joker(t, state, true)
		var record RoundRecord
		state, record = bid(t, state, round, 14-round)
		if record.FreshValue > 39 {
			t.Fatal("fresh reward exceeds its bound")
		}
		if round < Rounds && state.Result != nil {
			t.Fatal("game ended before all thirteen rounds")
		}
	}
	if state.Scores[0]+state.Scores[1]+state.Discard != MaxPoints || state.Discard != 0 || state.Result.Winner == nil {
		t.Fatalf("incorrect maximum: %+v", state)
	}
}

func TestProjectionAndInputIsolation(t *testing.T) {
	state := joker(t, orderedGame(t), false)
	before, _ := json.Marshal(state)
	view, err := Project(state, 0, pointer(12))
	if err != nil || view.OwnSelectedCard == nil || *view.OwnSelectedCard != 12 || len(view.HandRemaining[0]) != 13 || len(view.Played[0]) != 0 || len(view.Rewards) != 2 {
		t.Fatalf("invalid locked-card view: %+v, %v", view, err)
	}
	body, _ := json.Marshal(view)
	var fields map[string]json.RawMessage
	if json.Unmarshal(body, &fields) != nil || len(fields) != 8 || fields["decks"] != nil || fields["plans"] != nil {
		t.Fatal("projection contains private state")
	}
	other, err := Project(state, 1, nil)
	if err != nil || other.OwnSelectedCard != nil || len(other.HandRemaining[0]) != 13 {
		t.Fatal("first lock was exposed or removed a card")
	}
	view.HandRemaining[0][0] = 99
	view.Rewards[0].Rank = 99
	next, record := bid(t, state, 12, 1)
	next.Hands[0][0] = 99
	next.Rewards[0].Rank = 99
	record.ResolvedRewards[0].Rank = 99
	after, _ := json.Marshal(state)
	if !bytes.Equal(before, after) {
		t.Fatal("projection or transition mutated its input")
	}
	for _, cards := range [][2]int{{0, 1}, {14, 1}, {1, -1}} {
		if _, _, err := ResolveBids(state, cards); !errors.Is(err, ErrInvalidAction) {
			t.Fatalf("accepted invalid bid %v", cards)
		}
	}
}

func TestRandomGamesConserveCardsAndPoints(t *testing.T) {
	for seed := range int64(200) {
		random := mathrand.New(mathrand.NewSource(seed))
		state, err := New(random)
		if err != nil {
			t.Fatal(err)
		}
		for round := 1; round <= Rounds; round++ {
			state = joker(t, state, random.Intn(2) == 0)
			prior := clone(state)
			bids := [2]int{}
			for side := range 2 {
				bids[side] = state.Hands[side][random.Intn(len(state.Hands[side]))]
			}
			next, record := bid(t, state, bids[0], bids[1])
			if !reflect.DeepEqual(state, prior) {
				t.Fatal("transition mutated the previous state")
			}
			if record.FreshValue+record.CarryBefore != record.AwardedPoints+record.CarryAfter+record.DiscardedPoints {
				t.Fatalf("round points not conserved: %+v", record)
			}
			revealed := 0
			for _, reward := range next.Rewards {
				revealed += reward.Value()
			}
			if revealed != next.Scores[0]+next.Scores[1]+next.Pool+next.Discard {
				t.Fatal("revealed points not conserved")
			}
			state = next
		}
		if state.Result == nil || state.Pool != 0 {
			t.Fatal("thirteen rounds did not finish")
		}
	}
}

func TestShuffleUsesIndependentDrawsAndFailsClosed(t *testing.T) {
	first, err := New(mathrand.New(mathrand.NewSource(54)))
	if err != nil || first.Decks[0] == first.Decks[1] {
		t.Fatal("reward decks share an order")
	}
	again, err := New(mathrand.New(mathrand.NewSource(54)))
	if err != nil || !reflect.DeepEqual(first, again) {
		t.Fatal("injected random source is not reproducible")
	}
	if state, err := New(bytes.NewReader(nil)); !errors.Is(err, ErrRandom) || state.Round != 0 {
		t.Fatal("random failure returned a usable state")
	}
}

func TestRejectsCorruptPersistedState(t *testing.T) {
	state := joker(t, orderedGame(t), true)
	state, _ = bid(t, state, 3, 1)
	cases := []struct {
		name string
		edit func(*State)
	}{
		{"duplicate deck", func(s *State) { s.Decks[0][0] = s.Decks[0][1] }},
		{"wrong revealed rank", func(s *State) { s.Rewards[0].Rank++ }},
		{"duplicate hand", func(s *State) { s.Hands[0][0] = s.Hands[0][1] }},
		{"played card remains", func(s *State) { s.Hands[0][0] = 3; slices.Sort(s.Hands[0]) }},
		{"unearned score", func(s *State) { s.Scores[0]++ }},
		{"wrong owner", func(s *State) { s.Rewards[0].Owner = pointer(1) }},
		{"reward reclaimed", func(s *State) { s.Rewards[0].Status = "pool"; s.Rewards[0].Owner = nil }},
		{"restored joker", func(s *State) { s.JokerAvailable[0] = true }},
		{"wrong-side joker", func(s *State) { s.Rewards[1].Multiplier = 2; s.JokerAvailable[1] = false }},
		{"early result", func(s *State) { s.Result = &Result{} }},
		{"early terminal", func(s *State) { s.Phase = Terminal; s.Result = &Result{} }},
		{"unknown phase", func(s *State) { s.Phase = "unknown" }},
	}
	for _, item := range cases {
		t.Run(item.name, func(t *testing.T) {
			corrupt := clone(state)
			item.edit(&corrupt)
			if !errors.Is(corrupt.Validate(), ErrInvalidState) {
				t.Fatal("corrupt game accepted")
			}
		})
	}
}
