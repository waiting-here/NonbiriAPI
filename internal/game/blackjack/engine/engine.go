// Package engine implements a deterministic, simultaneous blackjack table.
// Its caller owns identity, admission, deadlines, payment and persistence.
package engine

import (
	"errors"
	"io"
	"slices"

	"github.com/waiting-here/NonbiriAPI/internal/game/randomness"
)

const (
	DeckSize        = 312
	MaxSeats        = 9
	MaxHands        = 2
	SeatingSeconds  = 15
	DecisionSeconds = 30
	RoundSeconds    = 60
)

var (
	ErrState    = errors.New("blackjack: invalid state")
	ErrAction   = errors.New("blackjack: invalid action")
	ErrRevision = errors.New("blackjack: hand changed")
	ErrRandom   = errors.New("blackjack: random source unavailable")
)

type Card uint16

func (c Card) Rank() int  { return int(c)%13 + 1 }
func (c Card) Suit() int  { return int(c) / 13 % 4 }
func (c Card) Value() int { return min(c.Rank(), 10) }

type Total struct {
	Value int  `json:"value"`
	Soft  bool `json:"soft"`
}

func Score(cards []Card) Total {
	total, aces := 0, 0
	for _, c := range cards {
		total += c.Value()
		if c.Rank() == 1 {
			aces++
		}
	}
	soft := aces > 0 && total+10 <= 21
	if soft {
		total += 10
	}
	return Total{Value: total, Soft: soft}
}

type Hand struct {
	Cards     []Card `json:"cards"`
	Revision  int64  `json:"revision,string"`
	Units     int    `json:"units"`
	Split     bool   `json:"split"`
	SplitAces bool   `json:"split_aces"`
	Stood     bool   `json:"stood"`
	Outcome   string `json:"outcome,omitempty"`
}

func (h Hand) Natural() bool { return !h.Split && len(h.Cards) == 2 && Score(h.Cards).Value == 21 }

// ReturnHalves is the gross return in half-base-stake units, before any fees.
func (h Hand) ReturnHalves() int {
	switch h.Outcome {
	case "natural":
		return 5 * h.Units
	case "win":
		return 4 * h.Units
	case "push":
		return 2 * h.Units
	default:
		return 0
	}
}

type Seat struct {
	Number int    `json:"number"`
	Hands  []Hand `json:"hands"`
}

// State contains the private shoe. Persist it only in the server's private
// state column; Project is the only wire representation of a table.
type State struct {
	Deck      [DeckSize]Card `json:"deck"`
	Cursor    int            `json:"cursor"`
	StartSeat int            `json:"start_seat"`
	Seats     []Seat         `json:"seats"`
	Dealer    []Card         `json:"dealer"`
	Finished  bool           `json:"finished"`
}

type Action struct {
	Seat     int    `json:"seat"`
	Hand     int    `json:"hand"`
	Revision int64  `json:"revision,string"`
	Kind     string `json:"kind"`
}

func Shuffle(random io.Reader) ([DeckSize]Card, error) {
	var deck [DeckSize]Card
	for i := range deck {
		deck[i] = Card(i)
	}
	for i := DeckSize - 1; i > 0; i-- {
		pick, err := randomness.Index(random, uint64(i+1))
		if err != nil {
			return [DeckSize]Card{}, ErrRandom
		}
		j := int(pick)
		deck[i], deck[j] = deck[j], deck[i]
	}
	return deck, nil
}

func New(seats []int, startSeat int, random io.Reader) (State, error) {
	deck, err := Shuffle(random)
	if err != nil {
		return State{}, err
	}
	return Deal(deck, seats, startSeat)
}

// Deal accepts a complete permutation, allowing rules to be tested without
// introducing a deterministic random source into the production interface.
func Deal(deck [DeckSize]Card, seats []int, startSeat int) (State, error) {
	if !validDeck(deck) || len(seats) < 1 || len(seats) > MaxSeats || startSeat < 0 || startSeat >= MaxSeats {
		return State{}, ErrState
	}
	numbers := slices.Clone(seats)
	slices.Sort(numbers)
	s := State{Deck: deck, StartSeat: startSeat, Seats: make([]Seat, len(numbers)), Dealer: []Card{}}
	for i, number := range numbers {
		if number < 0 || number >= MaxSeats || i > 0 && number == numbers[i-1] {
			return State{}, ErrState
		}
		s.Seats[i] = Seat{Number: number, Hands: []Hand{{Cards: []Card{}, Revision: 1, Units: 1}}}
	}
	for range 2 {
		for offset := range MaxSeats {
			if seat := s.seat((startSeat + offset) % MaxSeats); seat != nil {
				seat.Hands[0].Cards = append(seat.Hands[0].Cards, s.draw())
			}
		}
		s.Dealer = append(s.Dealer, s.draw())
	}
	for i := range s.Seats {
		if s.Seats[i].Hands[0].Natural() {
			s.Seats[i].Hands[0].Stood = true
		}
	}
	if Score(s.Dealer).Value == 21 {
		for i := range s.Seats {
			s.Seats[i].Hands[0].Stood = true
		}
	}
	s.finishIfReady()
	return s, s.Validate()
}

func (s *State) seat(number int) *Seat {
	for i := range s.Seats {
		if s.Seats[i].Number == number {
			return &s.Seats[i]
		}
	}
	return nil
}

func (s *State) draw() Card {
	// With six complete decks, at most sixteen player hands and one dealer
	// cannot exhaust the shoe under the 21-point stopping rules.
	card := s.Deck[s.Cursor]
	s.Cursor++
	return card
}

func validDeck(deck [DeckSize]Card) bool {
	var seen [DeckSize]bool
	for _, card := range deck {
		if card >= DeckSize || seen[card] {
			return false
		}
		seen[card] = true
	}
	return true
}

func (s State) Legal(number, hand int) []string {
	seat := s.seat(number)
	if s.Finished || seat == nil || hand < 0 || hand >= len(seat.Hands) {
		return []string{}
	}
	h := seat.Hands[hand]
	if h.Stood {
		return []string{}
	}
	result := []string{"hit", "stand"}
	if len(h.Cards) == 2 && !h.SplitAces {
		result = append(result, "double")
		if len(seat.Hands) == 1 && !h.Split && h.Cards[0].Value() == h.Cards[1].Value() {
			result = append(result, "split")
		}
	}
	return result
}

// AdditionalUnits validates an intent before the caller reserves its payment.
// No card or hand changes until that intent is included in ApplyBatch.
func (s State) AdditionalUnits(a Action) (int, error) {
	seat := s.seat(a.Seat)
	if seat == nil || a.Hand < 0 || a.Hand >= len(seat.Hands) {
		return 0, ErrAction
	}
	if seat.Hands[a.Hand].Revision != a.Revision {
		return 0, ErrRevision
	}
	if !slices.Contains(s.Legal(a.Seat, a.Hand), a.Kind) {
		return 0, ErrAction
	}
	if a.Kind == "double" || a.Kind == "split" {
		return 1, nil
	}
	return 0, nil
}

// ApplyBatch validates the entire batch against the same previous state, then
// allocates cards in rotating seat order. Every accepted payment must already
// be reserved by its caller in the same transaction as the stored intent.
func ApplyBatch(previous State, actions []Action) (State, error) {
	if err := previous.Validate(); err != nil {
		return State{}, err
	}
	if previous.Finished || len(actions) > MaxSeats {
		return State{}, ErrAction
	}
	seen := [MaxSeats]bool{}
	for _, a := range actions {
		if _, err := previous.AdditionalUnits(a); err != nil {
			return State{}, err
		}
		if seen[a.Seat] {
			return State{}, ErrAction
		}
		seen[a.Seat] = true
	}
	s := clone(previous)
	for offset := range MaxSeats {
		number := (s.StartSeat + offset) % MaxSeats
		for _, a := range actions {
			if a.Seat == number {
				s.apply(a)
			}
		}
	}
	s.finishIfReady()
	return s, s.Validate()
}

func (s *State) apply(a Action) {
	seat := s.seat(a.Seat)
	h := &seat.Hands[a.Hand]
	h.Revision++
	switch a.Kind {
	case "stand":
		h.Stood = true
	case "hit", "double":
		h.Cards = append(h.Cards, s.draw())
		if a.Kind == "double" {
			h.Units = 2
			h.Stood = true
		}
		if Score(h.Cards).Value >= 21 {
			h.Stood = true
		}
	case "split":
		cards, revision := slices.Clone(h.Cards), h.Revision
		aces := cards[0].Rank() == 1
		seat.Hands = []Hand{
			{Cards: []Card{cards[0], s.draw()}, Revision: revision, Units: 1, Split: true, SplitAces: aces},
			{Cards: []Card{cards[1], s.draw()}, Revision: 1, Units: 1, Split: true, SplitAces: aces},
		}
		for i := range seat.Hands {
			seat.Hands[i].Stood = aces || Score(seat.Hands[i].Cards).Value >= 21
		}
	}
}

// Stop automatically stands the listed seats (timeout, ban or deletion).
// Already accepted actions are applied by the caller before this transition.
func Stop(previous State, numbers ...int) (State, error) {
	if err := previous.Validate(); err != nil {
		return State{}, err
	}
	s := clone(previous)
	for _, number := range numbers {
		seat := s.seat(number)
		if seat == nil {
			return State{}, ErrAction
		}
		for i := range seat.Hands {
			if !seat.Hands[i].Stood {
				seat.Hands[i].Stood = true
				seat.Hands[i].Revision++
			}
		}
	}
	s.finishIfReady()
	return s, s.Validate()
}

func (s *State) finishIfReady() {
	if s.Finished {
		return
	}
	compare := false
	for _, seat := range s.Seats {
		for _, h := range seat.Hands {
			if !h.Stood {
				return
			}
			if !h.Natural() && Score(h.Cards).Value <= 21 {
				compare = true
			}
		}
	}
	dealerNatural := len(s.Dealer) == 2 && Score(s.Dealer).Value == 21
	if compare && !dealerNatural {
		for Score(s.Dealer).Value < 17 {
			s.Dealer = append(s.Dealer, s.draw())
		}
	}
	d := Score(s.Dealer).Value
	for i := range s.Seats {
		for j := range s.Seats[i].Hands {
			h := &s.Seats[i].Hands[j]
			h.Outcome = outcome(*h, d, dealerNatural)
		}
	}
	s.Finished = true
}

func outcome(h Hand, dealer int, dealerNatural bool) string {
	value := Score(h.Cards).Value
	switch {
	case value > 21:
		return "loss"
	case dealerNatural && h.Natural():
		return "push"
	case dealerNatural:
		return "loss"
	case h.Natural():
		return "natural"
	case dealer > 21 || value > dealer:
		return "win"
	case value == dealer:
		return "push"
	default:
		return "loss"
	}
}

func clone(s State) State {
	s.Dealer = slices.Clone(s.Dealer)
	s.Seats = slices.Clone(s.Seats)
	for i := range s.Seats {
		s.Seats[i].Hands = slices.Clone(s.Seats[i].Hands)
		for j := range s.Seats[i].Hands {
			s.Seats[i].Hands[j].Cards = slices.Clone(s.Seats[i].Hands[j].Cards)
		}
	}
	return s
}

func (s State) Validate() error {
	if !validDeck(s.Deck) || s.Cursor < 4 || s.Cursor > DeckSize || s.StartSeat < 0 || s.StartSeat >= MaxSeats || len(s.Seats) < 1 || len(s.Seats) > MaxSeats || len(s.Dealer) < 2 || len(s.Dealer) > 17 || !s.Finished && len(s.Dealer) != 2 {
		return ErrState
	}
	seenCards := [DeckSize]bool{}
	count := 0
	checkCards := func(cards []Card) bool {
		for _, c := range cards {
			if c >= DeckSize || seenCards[c] {
				return false
			}
			seenCards[c] = true
			count++
		}
		return true
	}
	if !checkCards(s.Dealer) {
		return ErrState
	}
	for n := 2; n < len(s.Dealer); n++ {
		if Score(s.Dealer[:n]).Value >= 17 {
			return ErrState
		}
	}
	dealerNatural := len(s.Dealer) == 2 && Score(s.Dealer).Value == 21
	if dealerNatural && !s.Finished {
		return ErrState
	}
	last, allStood := -1, true
	for _, seat := range s.Seats {
		if seat.Number <= last || seat.Number >= MaxSeats || len(seat.Hands) < 1 || len(seat.Hands) > MaxHands {
			return ErrState
		}
		last = seat.Number
		for _, h := range seat.Hands {
			if len(h.Cards) < 2 || len(h.Cards) > 21 || !checkCards(h.Cards) || h.Revision < 1 || h.Revision > 24 || h.Units < 1 || h.Units > 2 || h.Split != (len(seat.Hands) == 2) || h.SplitAces && (!h.Split || len(h.Cards) != 2 || !h.Stood || h.Units != 1) || h.Units == 2 && (len(h.Cards) != 3 || !h.Stood) || Score(h.Cards).Value >= 21 && !h.Stood {
				return ErrState
			}
			if s.Finished && (!h.Stood || !slices.Contains([]string{"win", "loss", "natural", "push"}, h.Outcome)) || !s.Finished && h.Outcome != "" {
				return ErrState
			}
			for n := 2; n < len(h.Cards); n++ {
				if Score(h.Cards[:n]).Value >= 21 {
					return ErrState
				}
			}
			if s.Finished && h.Outcome != outcome(h, Score(s.Dealer).Value, dealerNatural) {
				return ErrState
			}
			allStood = allStood && h.Stood
		}
	}
	if count != s.Cursor || allStood != s.Finished {
		return ErrState
	}
	for _, c := range s.Deck[:s.Cursor] {
		if !seenCards[c] {
			return ErrState
		}
	}
	return nil
}

type CardView struct {
	Rank int `json:"rank"`
	Suit int `json:"suit"`
}

type HandView struct {
	Cards    []CardView `json:"cards"`
	Revision int64      `json:"revision,string"`
	Units    int        `json:"units"`
	Total    Total      `json:"total"`
	Natural  bool       `json:"natural"`
	Split    bool       `json:"split"`
	Stood    bool       `json:"stood"`
	Outcome  string     `json:"outcome,omitempty"`
}

type SeatView struct {
	Number int        `json:"number"`
	Hands  []HandView `json:"hands"`
}

type View struct {
	Seats       []SeatView `json:"seats"`
	Dealer      []CardView `json:"dealer"`
	DealerTotal Total      `json:"dealer_total"`
	HoleHidden  bool       `json:"hole_hidden"`
	Finished    bool       `json:"finished"`
}

func cardsView(cards []Card) []CardView {
	result := make([]CardView, len(cards))
	for i, c := range cards {
		result[i] = CardView{Rank: c.Rank(), Suit: c.Suit()}
	}
	return result
}

func Project(s State) (View, error) {
	if err := s.Validate(); err != nil {
		return View{}, err
	}
	dealer := s.Dealer
	if !s.Finished {
		dealer = dealer[:1]
	}
	v := View{Seats: make([]SeatView, len(s.Seats)), Dealer: cardsView(dealer), DealerTotal: Score(dealer), HoleHidden: !s.Finished, Finished: s.Finished}
	for i, seat := range s.Seats {
		v.Seats[i] = SeatView{Number: seat.Number, Hands: make([]HandView, len(seat.Hands))}
		for j, h := range seat.Hands {
			v.Seats[i].Hands[j] = HandView{Cards: cardsView(h.Cards), Revision: h.Revision, Units: h.Units, Total: Score(h.Cards), Natural: h.Natural(), Split: h.Split, Stood: h.Stood, Outcome: h.Outcome}
		}
	}
	return v, nil
}
