package engine

import (
	"io"
	"slices"
	"strconv"
)

func (s *State) ability(e Effect, _ io.Reader) error {
	id := e.Card
	owner := s.card(id).Owner
	p := &s.Players[owner]
	switch e.Value {
	case "bond", "morale", "horn", "avenger", "decoy":
		// Persistent row effects are derived from its physical cards.
	case "spy":
		s.draw(owner, 2)
		s.card(id).Owner = 1 - owner
	case "medic", "leader_deepseek_recover":
		s.offer(owner, Choice{Kind: "medic", Source: id, Options: s.filterUnits(p.Grave), Remaining: 1})
	case "muster":
		group := s.definition(id).MusterGroup
		key := strconv.Itoa(owner) + ":" + group
		if slices.Contains(s.Mustering, key) {
			return nil
		}
		s.Mustering = append(s.Mustering, key)
		var effects []Effect
		for _, container := range [][]int{p.Hand, p.Deck} {
			for _, other := range container {
				if s.unit(other) && s.definition(other).MusterGroup == group {
					effects = append(effects, Effect{Kind: "muster_member", Card: other, Seat: owner})
				}
			}
		}
		effects = append(effects, Effect{Kind: "end_muster", Value: key})
		s.prepend(effects...)
	case "future_predict":
		p.KnownTop = slices.Clone(p.Deck[:min(2, len(p.Deck))])
		p.Boost += 3
		s.offer(owner, Choice{Kind: "reveal", Source: id, Options: slices.Clone(p.KnownTop), CanQuit: true})
	case "deep_think":
		s.GrowthTriggers = append(s.GrowthTriggers, id)
	case "long_context":
		s.prepend(Effect{Kind: "refresh_all"})
		var options []int
		for _, other := range s.ownUnits(1 - owner) {
			for _, ability := range s.definition(other).Abilities {
				if slices.Contains([]string{"medic", "morale", "scorch_c", "scorch_r", "scorch_s"}, ability) {
					options = append(options, other)
					break
				}
			}
		}
		s.offer(owner, Choice{Kind: "copy", Source: id, Options: options, Remaining: 1})
	case "visual_analysis":
		s.analysis(owner, id)
	case "chain_of_thought":
		p.Boost += 5
	case "safety_layer":
		p.Shield = true
	case "context_window":
		s.offer(owner, Choice{Kind: "context_window", Source: id, Options: slices.Clone(p.Hand), Remaining: 3, CanQuit: true})
	case "mardroeme":
		side, row, found := s.locate(id)
		if !found {
			return ErrState
		}
		var effects []Effect
		for _, other := range s.Board[side][row].Cards {
			if s.has(other, "berserker") {
				effects = append(effects, Effect{Kind: "ability", Card: other, Value: "berserker"})
			}
		}
		s.prepend(effects...)
	case "berserker":
		side, row, found := s.locate(id)
		if !found {
			return nil
		}
		_, _, catalyst := s.effects(side, row)
		if catalyst == 0 {
			return nil
		}
		form := s.definition(id).TransformForm
		s.detach(id)
		newID := s.makeCard(form.ID, owner)
		s.prepend(Effect{Kind: "place", Card: newID, Seat: side, Row: row})
	case "scorch":
		s.scorch(id, -1)
	case "scorch_c":
		s.scorch(id, 0)
	case "scorch_r":
		s.scorch(id, 1)
	case "scorch_s":
		s.scorch(id, 2)
	case "leader_openai":
		s.draw(owner, 1)
		p.Boost += 3
		s.prepend(Effect{Kind: "refresh_all"})
	case "leader_claude":
		s.draw(owner, 1)
		p.Shield = true
		s.prepend(Effect{Kind: "refresh_all"})
	case "leader_gemini":
		s.draw(owner, 1)
		s.analysis(owner, id)
		s.prepend(Effect{Kind: "refresh_all"})
	case "leader_deepseek":
		s.draw(owner, 1)
		units := s.ownUnits(owner)
		if len(units) > 0 {
			chosen := units[0]
			for _, other := range units[1:] {
				if s.Power(other) > s.Power(chosen) {
					chosen = other
				}
			}
			s.card(chosen).Bonus += 4
		}
		s.prepend(Effect{Kind: "refresh_all"})
	case "leader_gemini_clear":
		s.clearWeather()
		s.draw(owner, 2)
	case "leader_claude_deny":
		s.Players[1-owner].LeaderUsed = true
	case "leader_claude_retrieve":
		s.offer(owner, Choice{Kind: "retrieve", Source: id, Options: s.filterUnits(s.Players[1-owner].Grave), Remaining: 1})
	case "leader_openai_assault", "leader_openai_compute", "leader_gemini_sensors":
		row := map[string]int{"leader_openai_assault": 0, "leader_openai_compute": 2, "leader_gemini_sensors": 1}[e.Value]
		if s.Board[owner][row].Special == 0 {
			token := s.makeCard("compute_surge", owner)
			s.card(token).Ephemeral = true
			s.prepend(Effect{Kind: "place", Card: token, Seat: owner, Row: row})
		}
	default:
		return ErrState
	}
	return nil
}
func (s *State) analysis(owner, source int) {
	if len(s.Weather) == 0 {
		s.Players[owner].Boost += 3
		return
	}
	s.offer(owner, Choice{Kind: "analysis", Source: source, Options: slices.Clone(s.Weather), Remaining: 1, CanQuit: true})
}
func (s *State) scorch(caster, targetRow int) {
	owner := s.card(caster).Owner
	var candidates []int
	if targetRow < 0 {
		for _, seat := range []int{1, 0} {
			for order := range 3 {
				row := order
				if seat == 1 {
					row = 2 - order
				}
				for _, id := range s.Board[seat][row].Cards {
					if id != caster && s.unit(id) {
						candidates = append(candidates, id)
					}
				}
			}
		}
	} else if s.RowTotal(1-owner, targetRow) >= 10 {
		candidates = s.filterUnits(s.Board[1-owner][targetRow].Cards)
	}
	maximum := 0
	for _, id := range candidates {
		maximum = max(maximum, s.Power(id))
	}
	var targets []int
	for _, id := range candidates {
		if s.Power(id) == maximum {
			targets = append(targets, id)
		}
	}
	protected := [2]bool{}
	for _, id := range targets {
		seat, _, _ := s.locate(id)
		if seat != owner && s.Players[seat].Shield {
			protected[seat] = true
			s.Players[seat].Shield = false
		}
	}
	var effects []Effect
	for _, id := range targets {
		seat, _, _ := s.locate(id)
		if !protected[seat] {
			effects = append(effects, Effect{Kind: "leave", Card: id, Value: "grave"})
		}
	}
	effects = append(effects, Effect{Kind: "refresh_all"})
	s.prepend(effects...)
}
