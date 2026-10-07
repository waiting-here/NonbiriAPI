package engine

import (
	"encoding/json"
	"io"
	"slices"

	"github.com/waiting-here/NonbiriAPI/internal/game/randomness"
)

const Version = 1

type Card struct {
	ID           int    `json:"id"`
	Definition   string `json:"definition"`
	Owner        int    `json:"owner"`
	Power        int    `json:"power"`
	Bonus        int    `json:"bonus,omitempty"`
	Growth       int    `json:"growth,omitempty"`
	CopiedMorale bool   `json:"copied_morale,omitempty"`
	Ephemeral    bool   `json:"ephemeral,omitempty"`
}
type Player struct {
	Faction    string `json:"faction"`
	Leader     int    `json:"leader"`
	LeaderUsed bool   `json:"leader_used"`
	Lives      int    `json:"lives"`
	Passed     bool   `json:"passed"`
	Boost      int    `json:"boost"`
	Shield     bool   `json:"shield"`
	Hand       []int  `json:"hand"`
	Deck       []int  `json:"deck"`
	Grave      []int  `json:"grave"`
	KnownTop   []int  `json:"known_top"`
}
type Row struct {
	Cards   []int `json:"cards"`
	Special int   `json:"special,omitempty"`
	Total   int   `json:"total"`
}
type RoundRecord struct {
	Round  int    `json:"round"`
	Scores [2]int `json:"scores"`
	Winner *int   `json:"winner"`
}
type Result struct {
	Winner *int   `json:"winner"`
	Reason string `json:"reason"`
}
type Action struct {
	Kind string `json:"kind"`
	Card int    `json:"card,omitempty"`
	Row  string `json:"row,omitempty"`
}

// A continuation contains only data. Nested effects can pause at any choice,
// survive serialization, and resume without replaying earlier payments/actions.
type Effect struct {
	Kind  string `json:"kind"`
	Card  int    `json:"card,omitempty"`
	Seat  int    `json:"seat"`
	Row   int    `json:"row,omitempty"`
	Value string `json:"value,omitempty"`
	IDs   []int  `json:"ids,omitempty"`
}
type Choice struct {
	Kind      string   `json:"kind"`
	Source    int      `json:"source,omitempty"`
	Options   []int    `json:"options"`
	Rows      []string `json:"rows,omitempty"`
	Remaining int      `json:"remaining"`
	CanQuit   bool     `json:"can_quit"`
}
type Event struct {
	Kind  string `json:"kind"`
	Seat  int    `json:"seat"`
	Card  int    `json:"card,omitempty"`
	Value int    `json:"value,omitempty"`
}
type State struct {
	Version        int           `json:"version"`
	Stage          string        `json:"stage"`
	Round          int           `json:"round"`
	Turn           int           `json:"turn"`
	First          int           `json:"first"`
	Players        [2]Player     `json:"players"`
	Cards          []Card        `json:"cards"`
	Board          [2][3]Row     `json:"board"`
	Weather        []int         `json:"weather"`
	Choices        [2]*Choice    `json:"choices"`
	Queue          []Effect      `json:"queue"`
	GrowthTriggers []int         `json:"growth_triggers"`
	Mustering      []string      `json:"mustering"`
	Rounds         []RoundRecord `json:"rounds"`
	Events         []Event       `json:"events"`
	Result         *Result       `json:"result,omitempty"`
}

func New(decks [2]Deck, random io.Reader) (State, error) {
	s := State{Version: Version, Stage: "setup", Round: 1, Cards: []Card{}, Weather: []int{}, Queue: []Effect{}, Rounds: []RoundRecord{}, Events: []Event{}, GrowthTriggers: []int{}, Mustering: []string{}}
	for seat, deck := range decks {
		if err := ValidateDeck(deck); err != nil {
			return State{}, err
		}
		p := &s.Players[seat]
		*p = Player{Faction: deck.Faction, Lives: 2, Hand: []int{}, Deck: []int{}, Grave: []int{}, KnownTop: []int{}}
		p.Leader = s.makeCard(deck.Leader, seat)
		p.LeaderUsed = s.has(p.Leader, "leader_deepseek_rebirth")
		for _, entry := range deck.Cards {
			for range entry.Count {
				id := s.makeCard(entry.ID, seat)
				if err := s.insertRandom(seat, id, random); err != nil {
					return State{}, err
				}
			}
		}
		for i := range 3 {
			s.Board[seat][i].Cards = []int{}
		}
	}
	first, err := randomness.Index(random, 2)
	if err != nil {
		return State{}, err
	}
	s.First, s.Turn = int(first), int(first)
	s.Queue = []Effect{{Kind: "deal"}, {Kind: "mulligan"}, {Kind: "round_start"}}
	for seat, p := range s.Players {
		if p.Faction == "gemini" && s.Players[1-seat].Faction != "gemini" {
			s.Stage = "initiative"
			s.Choices[seat] = &Choice{Kind: "initiative", Options: []int{s.Players[seat].Leader, s.Players[1-seat].Leader}, Remaining: 1, CanQuit: true}
		}
	}
	if err := s.drain(random); err != nil {
		return State{}, err
	}
	return s, nil
}

func (s *State) makeCard(definition string, owner int) int {
	id := len(s.Cards) + 1
	s.Cards = append(s.Cards, Card{ID: id, Definition: definition, Owner: owner, Power: definitionsByID[definition].Power, Ephemeral: definitionsByID[definition].Ephemeral})
	return id
}
func (s *State) card(id int) *Card            { return &s.Cards[id-1] }
func (s *State) definition(id int) Definition { return definitionsByID[s.card(id).Definition] }
func (s *State) has(id int, ability string) bool {
	return ability == "morale" && s.card(id).CopiedMorale || slices.Contains(s.definition(id).Abilities, ability)
}
func (s *State) unit(id int) bool { return s.definition(id).Type == "unit" }
func (s *State) ownUnits(seat int) []int {
	var ids []int
	for _, row := range s.Board[seat] {
		for _, id := range row.Cards {
			if s.unit(id) {
				ids = append(ids, id)
			}
		}
	}
	return ids
}
func (s *State) filterUnits(ids []int) []int {
	var out []int
	for _, id := range ids {
		if s.unit(id) {
			out = append(out, id)
		}
	}
	return out
}
func (s *State) locate(id int) (seat, row int, found bool) {
	for seat, side := range s.Board {
		for row, value := range side {
			if slices.Contains(value.Cards, id) || value.Special == id {
				return seat, row, true
			}
		}
	}
	return 0, 0, false
}
func remove(ids []int, id int) ([]int, bool) {
	i := slices.Index(ids, id)
	if i < 0 {
		return ids, false
	}
	return slices.Delete(ids, i, i+1), true
}
func (s *State) insertRandom(seat, id int, random io.Reader) error {
	p := &s.Players[seat]
	p.Deck = append(p.Deck, id)
	i, err := randomness.Index(random, uint64(len(p.Deck)))
	if err != nil {
		return err
	}
	p.Deck[i], p.Deck[len(p.Deck)-1] = p.Deck[len(p.Deck)-1], p.Deck[i]
	p.KnownTop = []int{}
	return nil
}
func (s *State) draw(seat, count int) {
	p := &s.Players[seat]
	for range count {
		if len(p.Deck) == 0 {
			break
		}
		id := p.Deck[0]
		p.Deck = p.Deck[1:]
		s.addHand(seat, id, 0)
		p.KnownTop, _ = remove(p.KnownTop, id)
	}
}

// The source rules sort the first seat's hand and prepend the second seat's
// draws. A replacement retains its slot except at index zero.
func (s *State) addHand(seat, id, index int) {
	p := &s.Players[seat]
	if index == 0 && seat == 0 {
		p.Hand = s.insertSorted(p.Hand, id)
	} else {
		p.Hand = slices.Insert(p.Hand, index, id)
	}
}
func (s *State) prepend(effects ...Effect) { s.Queue = append(effects, s.Queue...) }
func (s *State) log(kind string, seat, card, value int) {
	s.Events = append(s.Events, Event{kind, seat, card, value})
	if len(s.Events) > 40 {
		s.Events = slices.Clone(s.Events[len(s.Events)-40:])
	}
}
func (s *State) waiting() bool { return s.Choices[0] != nil || s.Choices[1] != nil }
func (s *State) Required() [2]bool {
	if s.Result != nil {
		return [2]bool{}
	}
	if s.waiting() {
		return [2]bool{s.Choices[0] != nil, s.Choices[1] != nil}
	}
	return [2]bool{s.Turn == 0, s.Turn == 1}
}
func (s *State) Phase() string {
	if s.Result != nil {
		return "terminal"
	}
	if s.Stage == "initiative" || s.Stage == "mulligan" {
		return s.Stage
	}
	if s.waiting() {
		return "choice"
	}
	return "turn"
}
func (s *State) Seconds() int64 {
	switch s.Phase() {
	case "turn":
		return 30
	case "mulligan":
		return 20
	case "terminal":
		return 0
	default:
		return 15
	}
}

func Apply(state State, seat int, action Action, random io.Reader) (State, error) {
	return applyAction(state, seat, action, random, nil)
}

// ApplyWithRounds captures only completed round boundaries. Persisting a full
// before/after state for every card selection would duplicate entire decks.
func ApplyWithRounds(state State, seat int, action Action, random io.Reader) (State, []State, error) {
	var rounds []State
	next, err := applyAction(state, seat, action, random, &rounds)
	return next, rounds, err
}

func applyAction(state State, seat int, action Action, random io.Reader, rounds *[]State) (State, error) {
	if seat < 0 || seat > 1 || !slices.Contains(state.Legal(seat), action) {
		return State{}, ErrInvalid
	}
	raw, err := json.Marshal(state)
	if err != nil {
		return State{}, ErrState
	}
	var next State
	if json.Unmarshal(raw, &next) != nil {
		return State{}, ErrState
	}
	if choice := next.Choices[seat]; choice != nil {
		err = next.choose(seat, *choice, action, random)
	} else {
		next.prepend(Effect{Kind: "end_turn", Seat: seat, Value: action.Kind})
		switch action.Kind {
		case "pass":
			next.Players[seat].Passed = true
		case "leader":
			next.prepend(Effect{Kind: "ability", Card: next.Players[seat].Leader, Seat: seat, Value: next.definition(next.Players[seat].Leader).Abilities[0]}, Effect{Kind: "disable_leader", Seat: seat})
		case "play":
			next.prepend(Effect{Kind: "play", Seat: seat, Card: action.Card, Row: slices.Index(rows, action.Row)})
		default:
			return State{}, ErrInvalid
		}
	}
	if err != nil {
		return State{}, err
	}
	if err = next.drainRounds(random, rounds); err != nil {
		return State{}, err
	}
	return next, nil
}
