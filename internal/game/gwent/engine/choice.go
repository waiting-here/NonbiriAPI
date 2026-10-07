package engine

import (
	"io"
	"slices"
)

func (s *State) offer(seat int, choice Choice) {
	if len(choice.Options) == 0 && len(choice.Rows) == 0 {
		return
	}
	if choice.Options == nil {
		choice.Options = []int{}
	}
	s.Choices[seat] = &choice
}
func (s *State) choose(seat int, choice Choice, action Action, random io.Reader) error {
	s.Choices[seat] = nil
	id := action.Card
	p := &s.Players[seat]
	switch choice.Kind {
	case "initiative":
		if id != 0 {
			s.First = s.card(id).Owner
		}
		s.Turn = s.First
		s.Stage = "setup"
	case "mulligan", "context_window":
		if id != 0 {
			index := slices.Index(p.Hand, id)
			if index < 0 {
				return ErrState
			}
			p.Hand = slices.Delete(p.Hand, index, index+1)
			if choice.Kind == "mulligan" {
				if err := s.insertRandom(seat, id, random); err != nil {
					return err
				}
			} else {
				choice.Selected = append(choice.Selected, id)
				p.Deck = append(p.Deck, id)
			}
			drawn := p.Deck[0]
			p.Deck = p.Deck[1:]
			p.KnownTop, _ = remove(p.KnownTop, drawn)
			s.addHand(seat, drawn, index)
			choice.Remaining--
			if choice.Remaining > 0 {
				choice.Options = slices.Clone(p.Hand)
				s.offer(seat, choice)
			}
		}
	case "reveal":
	case "row":
		s.prepend(Effect{Kind: "deploy", Card: choice.Source, Row: slices.Index(rows, action.Row)})
	case "medic":
		if id != 0 {
			s.prepend(Effect{Kind: "deploy", Card: id, Row: -1})
		}
	case "retrieve":
		if id != 0 {
			s.detach(id)
			s.card(id).Owner = seat
			s.addHand(seat, id, 0)
		}
	case "decoy":
		side, row, found := s.locate(id)
		if !found {
			return ErrState
		}
		s.prepend(Effect{Kind: "decoy_place", Card: choice.Source, Seat: side, Row: row})
		s.leave(id, "hand")
	case "copy":
		for _, ability := range s.definition(id).Abilities {
			if !slices.Contains([]string{"medic", "morale", "scorch_c", "scorch_r", "scorch_s"}, ability) {
				continue
			}
			if ability == "morale" {
				s.card(choice.Source).CopiedMorale = true
			} else {
				s.prepend(Effect{Kind: "ability", Card: choice.Source, Seat: seat, Value: ability})
			}
			break
		}
	case "analysis":
		if id != 0 {
			s.clearWeather()
		} else {
			p.Boost += 3
		}
	default:
		return ErrState
	}
	return nil
}
