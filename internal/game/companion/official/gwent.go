package official

import (
	"encoding/json"
	"math"
	"slices"

	"github.com/waiting-here/NonbiriAPI/internal/game/gwent/engine"
)

func gwentCardValue(card engine.CardView, view engine.View) int {
	value := card.BasePower
	for _, ability := range card.Abilities {
		switch ability {
		case "spy":
			value = 10*min(2, view.Self.DeckCount) - card.BasePower
		case "medic":
			for _, fallen := range view.Self.Grave {
				if fallen.Type == "unit" {
					value = max(value, card.BasePower+fallen.BasePower)
				}
			}
		case "bond", "muster", "morale":
			value += 3
		}
	}
	return value
}

func gwentAction(view engine.View) (engine.Action, error) {
	if len(view.Legal) == 0 {
		return engine.Action{}, errDecision
	}
	cards := make(map[int]engine.CardView, len(view.Hand))
	for _, card := range view.Hand {
		cards[card.InstanceID] = card
	}
	if view.Choice != nil {
		for _, card := range view.Choice.Cards {
			cards[card.InstanceID] = card
		}
	}
	lead := 0
	for _, row := range view.Board {
		if row.Side == "self" {
			lead += row.Total
		} else {
			lead -= row.Total
		}
	}
	winning := lead > 0 || lead == 0 && view.Self.Faction == "claude" && view.Enemy.Faction != "claude"
	best, high := view.Legal[0], math.Inf(-1)
	for _, action := range view.Legal {
		card := cards[action.Card]
		value := gwentCardValue(card, view)
		score := float64(value)
		switch action.Kind {
		case "pass":
			score = -80
			if view.Enemy.Passed && winning || view.Self.Lives > 1 && lead < -15 && view.Self.HandCount <= view.Enemy.HandCount {
				score = 1000
			} else if view.Self.Lives > 1 && lead > 12 && view.Self.HandCount <= view.Enemy.HandCount {
				score = 28
			}
		case "play":
			score += float64(boardSwing(card, action.Row, view))
			if slices.Contains(card.Abilities, "spy") && view.Enemy.Passed {
				score = -float64(card.BasePower)
			}
			for _, row := range view.Board {
				if row.Side != "self" || row.Row != action.Row {
					continue
				}
				if row.Weather && card.Type != "hero" {
					score -= float64(max(0, card.BasePower-1))
				}
				if row.Special != nil && !slices.Contains(card.Abilities, "spy") {
					score += float64(card.BasePower) / 2
				}
			}
			// With the other seat finished, spend the smallest sufficient unit.
			if view.Enemy.Passed && !winning && lead+int(score) > 0 {
				score = 90 - score/3
			}
		case "leader":
			score = 4 + float64(6*min(1, view.Self.DeckCount))
			if view.Self.HandCount < 3 {
				score += 3
			}
		case "redraw":
			score = 5 - float64(value)
		case "continue":
			score = 0.5
		case "choose":
			if view.Choice != nil && view.Choice.Kind == "initiative" && action.Card == view.Enemy.Leader.InstanceID {
				score = 100
			}
		case "choose_row":
			for _, row := range view.Board {
				if row.Side == "self" && row.Row == action.Row {
					if row.Weather {
						score -= 8
					}
					if row.Special != nil {
						score += 4
					}
				}
			}
		}
		if score > high {
			best, high = action, score
		}
	}
	return best, nil
}

// Score the change on both visible sides. A weather or scorch card with no
// printed strength can win a round or destroy the player's own lead.
func boardSwing(card engine.CardView, target string, view engine.View) int {
	weatherRow := map[string]string{"close": "frost", "ranged": "fog", "siege": "rain"}
	strongest, scorch, swing := 0, 0, 0
	for _, row := range view.Board {
		sign := 1
		if row.Side == "enemy" {
			sign = -1
		}
		for _, unit := range row.Cards {
			if unit.Type == "hero" {
				continue
			}
			if row.Weather && slices.Contains(card.Abilities, "clear") {
				swing += sign * max(0, unit.BasePower-1)
			}
			if !row.Weather && slices.Contains(card.Abilities, weatherRow[row.Row]) {
				swing -= sign * max(0, unit.Power-1)
			}
			if row.Side == "self" && row.Row == target && row.Special == nil && slices.Contains(card.Abilities, "horn") {
				swing += unit.Power
			}
			if unit.Power > strongest {
				strongest, scorch = unit.Power, 0
			}
			if unit.Power == strongest {
				scorch -= sign * unit.Power
			}
			if unit.ID == card.ID && row.Side == "self" && row.Row == target && slices.Contains(card.Abilities, "bond") {
				swing += unit.Power + card.BasePower
			}
		}
	}
	if slices.Contains(card.Abilities, "scorch") {
		swing += scorch
	}
	return swing
}

func gwentDecision(phase string, raw json.RawMessage, examples []json.RawMessage, preferred string) (any, error) {
	if phase == "setup" {
		// The filtered candidates already satisfy any player-selected faction.
		var selected *engine.Deck
		for _, candidate := range examples {
			var deck engine.Deck
			if json.Unmarshal(candidate, &deck) == nil && engine.ValidateDeck(deck) == nil {
				if selected == nil || deck.Faction == preferred {
					selected = &deck
				}
			}
		}
		if selected == nil {
			return nil, errDecision
		}
		if preset, ok := engine.PresetDeck(selected.Faction, "standard-bond"); ok {
			return preset, nil
		}
		return selected, nil
	}
	var view engine.View
	if err := json.Unmarshal(raw, &view); err != nil {
		return nil, err
	}
	return gwentAction(view)
}
