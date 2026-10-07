package engine

import (
	"io"
	"slices"
)

// TimeoutChoice preserves the original human-choice deadline fallback:
// finish an optional selection, otherwise choose its first legal target.
func TimeoutChoice(s State, seat int) (Action, error) {
	legal := s.Legal(seat)
	if len(legal) == 0 || s.Choices[seat] == nil {
		return Action{}, ErrInvalid
	}
	for _, a := range legal {
		if a.Kind == "continue" {
			return a, nil
		}
	}
	return legal[0], nil
}

// AutomaticChoices lasts for a single automatic turn. In particular, context
// redraws select the original three weakest cards, not newly drawn replacements.
type AutomaticChoices struct {
	redrawSource int
	redraw       []int
}

func (c *AutomaticChoices) Next(s State, seat int, random io.Reader) (Action, error) {
	choice := s.Choices[seat]
	if choice == nil {
		return Action{}, ErrInvalid
	}
	e := newScorer(&s, seat, random)
	target := 0
	switch choice.Kind {
	case "medic":
		target = e.medic(choice.Options, nil)
	case "retrieve":
		value := -1
		for _, id := range choice.Options {
			next := s.definition(id).Power
			if s.has(id, "spy") {
				next = 20
			}
			if next > value {
				target = id
				value = next
			}
		}
	case "decoy":
		target = e.decoyTarget()
	case "copy":
		_, row, found := s.locate(choice.Source)
		if !found {
			return Action{}, ErrState
		}
		target = e.copyTarget(choice.Source, row, choice.Options)
	case "row":
		row := e.agileRow(choice.Source)
		return Action{Kind: "choose_row", Row: rows[row]}, e.err
	case "analysis":
		if e.weather(true, "") > 0 {
			target = choice.Options[0]
		} else {
			return Action{Kind: "continue"}, nil
		}
	case "reveal":
		return Action{Kind: "continue"}, nil
	case "context_window":
		if c.redrawSource != choice.Source {
			c.redrawSource = choice.Source
			c.redraw = slices.Clone(s.Players[seat].Hand)
			slices.SortStableFunc(c.redraw, func(a, b int) int { return s.definition(a).Power - s.definition(b).Power })
			c.redraw = c.redraw[:min(3, len(c.redraw))]
		}
		if len(c.redraw) == 0 {
			return Action{Kind: "continue"}, nil
		}
		target = c.redraw[0]
		c.redraw = c.redraw[1:]
	default:
		return TimeoutChoice(s, seat)
	}
	if target == 0 {
		return Action{}, ErrState
	}
	return Action{Kind: "choose", Card: target}, e.err
}
