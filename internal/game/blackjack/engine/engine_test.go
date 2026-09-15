package engine

import (
	"bytes"
	"encoding/json"
	"errors"
	"math/rand/v2"
	"slices"
	"testing"
)

func shoe(t *testing.T, ranks ...int) [DeckSize]Card {
	t.Helper()
	var deck [DeckSize]Card
	used := [DeckSize]bool{}
	for i, rank := range ranks {
		found := false
		for c := range DeckSize {
			if Card(c).Rank() == rank && !used[c] {
				deck[i] = Card(c)
				used[c] = true
				found = true
				break
			}
		}
		if !found {
			t.Fatal("invalid scripted shoe")
		}
	}
	i := len(ranks)
	for c := range DeckSize {
		if !used[c] {
			deck[i] = Card(c)
			i++
		}
	}
	return deck
}

func deal(t *testing.T, ranks ...int) State {
	t.Helper()
	s, err := Deal(shoe(t, ranks...), []int{0}, 0)
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func act(t *testing.T, s State, seat, hand int, kind string) State {
	t.Helper()
	next, err := ApplyBatch(s, []Action{{Seat: seat, Hand: hand, Revision: s.seat(seat).Hands[hand].Revision, Kind: kind}})
	if err != nil {
		t.Fatal(err)
	}
	return next
}

func TestScoresAndSoftSeventeen(t *testing.T) {
	for _, tc := range []struct {
		ranks []int
		value int
		soft  bool
	}{
		{[]int{1, 1}, 12, true}, {[]int{1, 1, 9}, 21, true}, {[]int{1, 1, 9, 9}, 20, false},
		{[]int{1, 6}, 17, true}, {[]int{1, 6, 10}, 17, false}, {[]int{11, 12, 13}, 30, false},
	} {
		cards := shoe(t, tc.ranks...)
		if got := Score(cards[:len(tc.ranks)]); got != (Total{tc.value, tc.soft}) {
			t.Fatalf("%v: %+v", tc.ranks, got)
		}
	}
	s := act(t, deal(t, 10, 1, 8, 6, 10), 0, 0, "stand")
	if !s.Finished || len(s.Dealer) != 2 || s.Seats[0].Hands[0].Outcome != "win" {
		t.Fatalf("soft 17 must stand: %+v", s)
	}
}

func TestNaturalPrecedenceAndPeek(t *testing.T) {
	for _, tc := range []struct {
		name    string
		ranks   []int
		action  string
		outcome string
		halves  int
	}{
		{"both natural", []int{1, 10, 13, 1}, "", "push", 2},
		{"player natural", []int{1, 10, 10, 9}, "", "natural", 5},
		{"dealer natural", []int{10, 1, 9, 11}, "", "loss", 0},
		{"ordinary 21", []int{10, 10, 5, 7, 6}, "hit", "win", 4},
		{"player bust first", []int{10, 10, 9, 6, 10, 10}, "hit", "loss", 0},
		{"ordinary push", []int{10, 10, 7, 7}, "stand", "push", 2},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := deal(t, tc.ranks...)
			if tc.action != "" {
				s = act(t, s, 0, 0, tc.action)
			}
			h := s.Seats[0].Hands[0]
			if !s.Finished || h.Outcome != tc.outcome || h.ReturnHalves() != tc.halves {
				t.Fatalf("unexpected outcome: %+v", h)
			}
		})
	}
}

func TestSplitAndDoubleRules(t *testing.T) {
	t.Run("split aces stop with ordinary 21", func(t *testing.T) {
		s := act(t, deal(t, 1, 10, 1, 7, 10, 13), 0, 0, "split")
		if !s.Finished || len(s.Seats[0].Hands) != 2 {
			t.Fatal("split aces did not finish")
		}
		for i, h := range s.Seats[0].Hands {
			if h.Natural() || h.Outcome != "win" || h.ReturnHalves() != 4 || len(s.Legal(0, i)) != 0 {
				t.Fatalf("bad split ace result: %+v", h)
			}
		}
	})
	t.Run("double both split hands", func(t *testing.T) {
		s := act(t, deal(t, 8, 10, 8, 7, 3, 3, 10, 10), 0, 0, "split")
		if slices.Contains(s.Legal(0, 0), "split") || !slices.Contains(s.Legal(0, 1), "double") {
			t.Fatal("split/double legality")
		}
		s = act(t, s, 0, 1, "double")
		if s.Finished {
			t.Fatal("other hand is still deciding")
		}
		s = act(t, s, 0, 0, "double")
		for _, h := range s.Seats[0].Hands {
			if h.Units != 2 || h.ReturnHalves() != 8 {
				t.Fatalf("double: %+v", h)
			}
		}
	})
	t.Run("ten values may split", func(t *testing.T) {
		s := deal(t, 10, 10, 12, 7)
		if !slices.Contains(s.Legal(0, 0), "split") {
			t.Fatal("10 and Q must be splittable")
		}
	})
	t.Run("third card disables doubling", func(t *testing.T) {
		s := act(t, deal(t, 2, 10, 3, 7, 2), 0, 0, "hit")
		if slices.Contains(s.Legal(0, 0), "double") {
			t.Fatal("three cards cannot double")
		}
	})
}

func TestBatchesUseSeatOrderAndIndependentRevisions(t *testing.T) {
	// Starting with seat 1 deals in order 1, 0, dealer; the same order applies
	// to every action batch regardless of HTTP arrival order.
	s, err := Deal(shoe(t, 2, 3, 10, 2, 3, 7, 4, 5, 6), []int{0, 1}, 1)
	if err != nil {
		t.Fatal(err)
	}
	actions := []Action{{Seat: 0, Hand: 0, Revision: 1, Kind: "hit"}, {Seat: 1, Hand: 0, Revision: 1, Kind: "hit"}}
	next, err := ApplyBatch(s, actions)
	if err != nil {
		t.Fatal(err)
	}
	if next.seat(1).Hands[0].Cards[2].Rank() != 4 || next.seat(0).Hands[0].Cards[2].Rank() != 5 {
		t.Fatal("arrival order changed card allocation")
	}
	if s.Cursor != 6 || len(s.seat(0).Hands[0].Cards) != 2 {
		t.Fatal("input state was mutated")
	}
	onlyOne := act(t, s, 0, 0, "hit")
	if _, err := onlyOne.AdditionalUnits(actions[1]); err != nil {
		t.Fatalf("another seat invalidated hand: %v", err)
	}
	if _, err := onlyOne.AdditionalUnits(actions[0]); !errors.Is(err, ErrRevision) {
		t.Fatalf("old own revision accepted: %v", err)
	}
	if _, err := ApplyBatch(s, []Action{actions[0], actions[0]}); !errors.Is(err, ErrAction) {
		t.Fatal("two actions in one seat batch")
	}
	view, err := Project(next)
	if err != nil {
		t.Fatal(err)
	}
	if len(view.Dealer) != 1 || !view.HoleHidden || view.DealerTotal.Value != 10 {
		t.Fatal("dealer hole leaked")
	}
	body, _ := json.Marshal(view)
	if bytes.Contains(body, []byte("deck")) || bytes.Contains(body, []byte("cursor")) {
		t.Fatal("private sequence projected")
	}
	next, err = Stop(next, 0, 1)
	if err != nil || !next.Finished {
		t.Fatalf("timeout: %v", err)
	}
}

func TestShuffleAndCorruptStates(t *testing.T) {
	if _, err := Shuffle(bytes.NewReader(nil)); !errors.Is(err, ErrRandom) {
		t.Fatal("random failures must close admission")
	}
	deck, err := Shuffle(nil)
	if err != nil || !validDeck(deck) {
		t.Fatalf("shuffle: %v", err)
	}
	counts := [13]int{}
	for _, c := range deck {
		counts[c.Rank()-1]++
	}
	for _, count := range counts {
		if count != 24 {
			t.Fatal("not six full decks")
		}
	}
	s := deal(t, 10, 10, 8, 7)
	for _, mutate := range []func(*State){
		func(s *State) { s.Deck[1] = s.Deck[0] },
		func(s *State) { s.Cursor++ },
		func(s *State) { s.Seats[0].Hands[0].Cards[0] = s.Dealer[0] },
		func(s *State) { s.Seats[0].Hands[0].Units = 2 },
		func(s *State) { s.Seats[0].Hands[0].Revision = -1 },
		func(s *State) { s.Seats[0].Hands[0].Outcome = "win" },
	} {
		bad := clone(s)
		mutate(&bad)
		if bad.Validate() == nil {
			t.Fatal("corrupt state accepted")
		}
	}
	final := act(t, s, 0, 0, "stand")
	final.Seats[0].Hands[0].Outcome = "loss"
	if final.Validate() == nil {
		t.Fatal("forged result accepted")
	}
}

func TestFullTablesConserveShoeAndBoundInvestment(t *testing.T) {
	random := rand.New(rand.NewPCG(72, 196))
	for round := range 2000 {
		deck := shoe(t)
		random.Shuffle(len(deck), func(i, j int) { deck[i], deck[j] = deck[j], deck[i] })
		seats := []int{0, 1, 2, 3, 4, 5, 6, 7}
		s, err := Deal(deck, seats, round%MaxSeats)
		if err != nil {
			t.Fatal(err)
		}
		for batch := 0; !s.Finished; batch++ {
			if batch > 42 {
				t.Fatal("unbounded decisions")
			}
			actions := []Action{}
			for _, seat := range s.Seats {
				for hand, h := range seat.Hands {
					legal := s.Legal(seat.Number, hand)
					if len(legal) == 0 {
						continue
					}
					actions = append(actions, Action{seat.Number, hand, h.Revision, legal[random.IntN(len(legal))]})
					break
				}
			}
			s, err = ApplyBatch(s, actions)
			if err != nil {
				t.Fatalf("round %d: %v", round, err)
			}
		}
		for _, seat := range s.Seats {
			units, gross := 0, 0
			for _, h := range seat.Hands {
				units += h.Units
				gross += h.ReturnHalves()
			}
			if units > 4 || gross > 16 {
				t.Fatal("financial bound exceeded")
			}
		}
	}
}
