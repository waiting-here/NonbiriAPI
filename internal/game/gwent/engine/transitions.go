package engine

import (
	"io"
	"slices"

	"github.com/waiting-here/NonbiriAPI/internal/game/randomness"
)

func (s *State) drain(random io.Reader) error {
	for steps := 0; !s.waiting() && len(s.Queue) > 0 && s.Result == nil; steps++ {
		if steps >= 4096 {
			return ErrState
		}
		e := s.Queue[0]
		s.Queue = s.Queue[1:]
		if err := s.effect(e, random); err != nil {
			return err
		}
	}
	return nil
}
func (s *State) effect(e Effect, random io.Reader) error {
	switch e.Kind {
	case "deal":
		s.draw(0, 10)
		s.draw(1, 10)
	case "mulligan":
		s.Stage = "mulligan"
		for seat := range 2 {
			s.offer(seat, Choice{Kind: "mulligan", Options: slices.Clone(s.Players[seat].Hand), Remaining: 2, CanQuit: true})
		}
	case "play":
		s.log("play", e.Seat, e.Card, 0)
		d := s.definition(e.Card)
		if s.has(e.Card, "decoy") {
			s.offer(e.Seat, Choice{Kind: "decoy", Source: e.Card, Options: s.ownUnits(e.Seat), Remaining: 1})
		} else if d.Type == "weather" {
			s.detach(e.Card)
			if s.has(e.Card, "clear") {
				s.leave(e.Card, "grave")
				s.clearWeather()
			} else {
				duplicate := false
				for _, id := range s.Weather {
					if s.definition(id).Abilities[0] == d.Abilities[0] {
						duplicate = true
					}
				}
				if duplicate {
					s.leave(e.Card, "grave")
				} else {
					s.Weather = append(s.Weather, e.Card)
				}
			}
			s.refreshAll()
		} else if d.Row == "special" && !s.has(e.Card, "horn") {
			s.leave(e.Card, "grave")
			s.prepend(Effect{Kind: "ability", Card: e.Card, Seat: e.Seat, Value: d.Abilities[0]})
		} else {
			s.prepend(Effect{Kind: "deploy", Card: e.Card, Row: e.Row})
		}
	case "deploy":
		d := s.definition(e.Card)
		row := e.Row
		if row < 0 && d.Row == "agile" {
			s.offer(s.card(e.Card).Owner, Choice{Kind: "row", Source: e.Card, Rows: slices.Clone(rows[:2]), Remaining: 1})
		} else {
			if row < 0 {
				row = slices.Index(rows, d.Row)
			}
			if row < 0 || row >= 3 {
				return ErrState
			}
			s.deploy(e.Card, row)
		}
	case "place":
		s.place(e.Card, e.Seat, e.Row)
	case "decoy_place":
		s.detach(e.Card)
		s.place(e.Card, e.Seat, e.Row)
	case "ability":
		return s.ability(e, random)
	case "refresh_row":
		s.refreshRow(e.Seat, e.Row)
	case "refresh_all":
		s.refreshAll()
	case "disable_leader":
		s.Players[e.Seat].LeaderUsed = true
	case "end_muster":
		s.Mustering, _ = removeString(s.Mustering, e.Value)
	case "leave":
		s.leave(e.Card, e.Value)
	case "end_turn":
		return s.endTurn(e.Seat, e.Value, random)
	case "round_end":
		return s.endRound(random)
	case "round_after_clear":
		if s.Players[0].Lives == 0 || s.Players[1].Lives == 0 {
			result := &Result{Reason: "rounds"}
			if s.Players[0].Lives > 0 {
				winner := 0
				result.Winner = &winner
			}
			if s.Players[1].Lives > 0 {
				winner := 1
				result.Winner = &winner
			}
			s.Result = result
			s.Stage = "terminal"
			s.Queue = []Effect{}
		} else {
			s.Round++
			last := s.Rounds[len(s.Rounds)-1]
			if last.Winner != nil {
				s.Turn = *last.Winner
			} else {
				s.Turn = s.First
			}
			s.First = s.Turn
			for seat := range 2 {
				for _, id := range s.ownUnits(seat) {
					s.card(id).Bonus = 0
					s.card(id).Growth = 0
				}
			}
			s.refreshAll()
			s.prepend(Effect{Kind: "round_start"})
		}
	case "round_start":
		s.Stage = "playing"
		if s.Round == 1 {
			s.Turn = s.First
		}
		var effects []Effect
		// Faction callbacks run in reverse registration order in the source rules.
		for _, seat := range []int{1, 0} {
			effects = append(effects, Effect{Kind: "faction_start", Seat: seat})
		}
		effects = append(effects, Effect{Kind: "begin_turn"})
		s.prepend(effects...)
	case "faction_start":
		p := &s.Players[e.Seat]
		if p.Faction == "openai" && len(s.Rounds) > 0 && s.Rounds[len(s.Rounds)-1].Winner != nil && *s.Rounds[len(s.Rounds)-1].Winner == e.Seat {
			s.draw(e.Seat, 1)
		}
		if s.Round == 3 && s.has(p.Leader, "leader_deepseek_rebirth") {
			pool := s.filterUnits(p.Grave)
			var effects []Effect
			for range min(2, len(pool)) {
				i, err := randomness.Index(random, uint64(len(pool)))
				if err != nil {
					return err
				}
				id := pool[i]
				pool = slices.Delete(pool, int(i), int(i)+1)
				effects = append(effects, Effect{Kind: "recover_sample", Card: id, Seat: e.Seat})
			}
			s.prepend(effects...)
		}
	case "recover_sample":
		if slices.Contains(s.Players[e.Seat].Grave, e.Card) {
			s.prepend(Effect{Kind: "deploy", Card: e.Card, Row: -1})
		}
	case "muster_member":
		p := s.Players[e.Seat]
		if slices.Contains(p.Hand, e.Card) || slices.Contains(p.Deck, e.Card) {
			s.prepend(Effect{Kind: "deploy", Card: e.Card, Row: -1})
		}
	case "begin_turn":
		for seat := range 2 {
			p := &s.Players[seat]
			if len(p.Hand) == 0 && p.LeaderUsed {
				p.Passed = true
			}
		}
		if s.Players[0].Passed && s.Players[1].Passed {
			s.prepend(Effect{Kind: "round_end"})
		} else if s.Players[s.Turn].Passed {
			s.Turn = 1 - s.Turn
		}
	default:
		return ErrState
	}
	return nil
}

func removeString(values []string, value string) ([]string, bool) {
	i := slices.Index(values, value)
	if i < 0 {
		return values, false
	}
	return slices.Delete(values, i, i+1), true
}
func (s *State) endTurn(seat int, kind string, _ io.Reader) error {
	// Preserve trigger registration order, including repeated deployments.
	for i := len(s.GrowthTriggers) - 1; i >= 0; i-- {
		id := s.GrowthTriggers[i]
		if _, _, found := s.locate(id); !found {
			s.GrowthTriggers = slices.Delete(s.GrowthTriggers, i, i+1)
			continue
		}
		if s.card(id).Owner == seat && kind != "pass" {
			s.card(id).Growth = min(6, s.card(id).Growth+1)
			side, row, _ := s.locate(id)
			s.refreshRow(side, row)
		}
	}
	p := &s.Players[seat]
	if len(p.Hand) == 0 && p.LeaderUsed {
		p.Passed = true
	}
	if p.Passed && s.Players[1-seat].Passed {
		s.prepend(Effect{Kind: "round_end"})
	} else if !s.Players[1-seat].Passed {
		s.Turn = 1 - seat
	}
	return nil
}
func (s *State) endRound(random io.Reader) error {
	diff := s.difference()
	record := RoundRecord{Round: s.Round, Scores: s.Scores()}
	if diff != 0 {
		winner := 0
		if diff < 0 {
			winner = 1
		}
		record.Winner = &winner
	}
	s.Rounds = append(s.Rounds, record)
	retained := map[int]bool{}
	for _, seat := range []int{1, 0} {
		p := &s.Players[seat]
		if p.Faction == "deepseek" && !s.has(p.Leader, "leader_deepseek_rebirth") {
			units := s.ownUnits(seat)
			if len(units) > 0 {
				i, err := randomness.Index(random, uint64(len(units)))
				if err != nil {
					return err
				}
				retained[units[i]] = true
			}
		}
		p.Boost = 0
		p.Shield = false
		p.Passed = false
		if record.Winner == nil || *record.Winner != seat {
			p.Lives--
		}
	}
	s.clearWeather()
	var effects []Effect
	// Snapshot before clearing: an avenger's successor remains for the next round.
	for _, seat := range []int{1, 0} {
		for order := range 3 {
			row := order
			if seat == 1 {
				row = 2 - order
			}
			r := s.Board[seat][row]
			for _, id := range r.Cards {
				if !retained[id] {
					effects = append(effects, Effect{Kind: "leave", Card: id, Value: "grave"})
				}
			}
			if r.Special != 0 {
				effects = append(effects, Effect{Kind: "leave", Card: r.Special, Value: "grave"})
			}
		}
	}
	effects = append(effects, Effect{Kind: "round_after_clear"})
	s.prepend(effects...)
	return nil
}
