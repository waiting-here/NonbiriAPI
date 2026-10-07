package engine

import (
	"encoding/json"
	"slices"
)

// Decode checks the durable representation before following card references.
// Clients submit actions and decks; they never supply this state.
func Decode(raw []byte) (State, error) {
	var s State
	if len(raw) > 1<<20 || json.Unmarshal(raw, &s) != nil {
		return State{}, ErrState
	}
	if err := s.Validate(); err != nil {
		return State{}, err
	}
	return s, nil
}

func (s *State) Validate() error {
	if s.Version != Version || !slices.Contains([]string{"initiative", "mulligan", "playing", "terminal"}, s.Stage) ||
		s.Round < 1 || s.Round > 3 || s.Turn < 0 || s.Turn > 1 || s.First < 0 || s.First > 1 ||
		len(s.Cards) < 2 || len(s.Cards) > 1024 || len(s.Queue) > 4096 || len(s.Rounds) > 3 || len(s.Events) > 40 {
		return ErrState
	}
	valid := func(id int) bool { return id > 0 && id <= len(s.Cards) }
	for i, c := range s.Cards {
		if _, ok := definitionsByID[c.Definition]; !ok || c.ID != i+1 || c.Owner < 0 || c.Owner > 1 {
			return ErrState
		}
	}
	occupied := make([]bool, len(s.Cards)+1)
	zone := func(ids []int) bool {
		for _, id := range ids {
			if !valid(id) || occupied[id] {
				return false
			}
			occupied[id] = true
		}
		return true
	}
	for seat, p := range s.Players {
		if !slices.Contains(factions, p.Faction) || !valid(p.Leader) || s.definition(p.Leader).Type != "leader" ||
			s.definition(p.Leader).Faction != p.Faction || p.Lives < 0 || p.Lives > 2 ||
			!zone([]int{p.Leader}) || !zone(p.Hand) || !zone(p.Deck) || !zone(p.Grave) {
			return ErrState
		}
		if len(p.KnownTop) > len(p.Deck) || !slices.Equal(p.KnownTop, p.Deck[:len(p.KnownTop)]) {
			return ErrState
		}
		for _, row := range s.Board[seat] {
			if !zone(row.Cards) || row.Special != 0 && !zone([]int{row.Special}) {
				return ErrState
			}
		}
		if c := s.Choices[seat]; c != nil {
			if !slices.Contains([]string{"initiative", "mulligan", "context_window", "reveal", "row", "medic", "retrieve", "decoy", "copy", "analysis"}, c.Kind) ||
				c.Source != 0 && !valid(c.Source) || c.Remaining < 0 || c.Remaining > 3 || len(c.Options) > len(s.Cards) {
				return ErrState
			}
			for _, id := range c.Options {
				if !valid(id) {
					return ErrState
				}
			}
			for _, row := range c.Rows {
				if !slices.Contains(rows, row) {
					return ErrState
				}
			}
		}
	}
	if !zone(s.Weather) {
		return ErrState
	}
	for _, e := range s.Queue {
		if e.Seat < 0 || e.Seat > 1 || e.Row < -1 || e.Row > 2 || e.Card != 0 && !valid(e.Card) {
			return ErrState
		}
		for _, id := range e.IDs {
			if !valid(id) {
				return ErrState
			}
		}
	}
	for _, id := range s.GrowthTriggers {
		if !valid(id) {
			return ErrState
		}
	}
	if (s.Result != nil) != (s.Stage == "terminal") || s.Result != nil && s.Result.Winner != nil && (*s.Result.Winner < 0 || *s.Result.Winner > 1) {
		return ErrState
	}
	return nil
}
