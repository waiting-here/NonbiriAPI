package engine

import (
	"io"
	"math"
	"slices"
	"strings"

	"github.com/waiting-here/NonbiriAPI/internal/game/randomness"
)

// WeightedAction describes one physical card's preferred legal placement.
// Weights preserve the original local controller's card and faction heuristics.
type WeightedAction struct {
	Action Action
	Weight float64
}
type recoveryKey struct {
	ID        int
	Immediate bool
	Seen      [3]int
}
type scorer struct {
	s          *State
	seat       int
	random     io.Reader
	err        error
	recoveries map[recoveryKey]float64
}

func newScorer(s *State, seat int, random io.Reader) *scorer {
	return &scorer{s: s, seat: seat, random: random, recoveries: map[recoveryKey]float64{}}
}
func (e *scorer) roll() float64 {
	if e.err != nil {
		return 0
	}
	value, err := randomness.Index(e.random, 1<<53)
	if err != nil {
		e.err = err
		return 0
	}
	return float64(value) / (1 << 53)
}
func firstAbility(d Definition) string {
	if len(d.Abilities) == 0 {
		return ""
	}
	return d.Abilities[0]
}
func (e *scorer) setup(a Action) float64 {
	if a.Row == "" || !e.s.unit(a.Card) {
		return 0
	}
	d := e.s.definition(a.Card)
	row := slices.Index(rows, a.Row)
	side := e.seat
	if e.s.has(a.Card, "spy") {
		side = 1 - side
	}
	v := e.row(side, row)
	if e.s.has(a.Card, "bond") {
		companions := 0
		for _, id := range e.s.Players[e.seat].Hand {
			if id != a.Card && e.s.unit(id) && e.s.definition(id).Name == d.Name {
				companions++
			}
		}
		base := d.Power
		if v.weather {
			base = min(1, base)
		}
		return float64(min(12, companions*base))
	}
	if v.catalyst > 0 {
		return 0
	}
	if e.s.has(a.Card, "berserker") {
		for _, id := range e.s.Players[e.seat].Hand {
			if e.s.has(id, "mardroeme") && e.s.definition(id).Row == a.Row {
				return float64(max(0, v.power(formCard(*e.s.card(a.Card), d.TransformForm))-v.power(*e.s.card(a.Card)))) * 0.75
			}
		}
	}
	if e.s.has(a.Card, "mardroeme") {
		gain := 0
		for _, id := range e.s.Players[e.seat].Hand {
			target := e.s.definition(id)
			if e.s.has(id, "berserker") && target.Row == a.Row {
				gain += max(0, v.power(formCard(*e.s.card(id), target.TransformForm))-v.power(*e.s.card(id)))
			}
		}
		return min(12, float64(gain)*0.75)
	}
	return 0
}
func (e *scorer) passWeight() float64 {
	p, op := e.s.Players[e.seat], e.s.Players[1-e.seat]
	if p.Lives == 1 {
		return 0
	}
	scores := e.s.Scores()
	deficit := scores[1-e.seat] - scores[e.seat]
	if deficit < -30 && len(op.Hand)-len(p.Hand) > 2 {
		return 100
	}
	weight := math.Abs(float64(deficit))
	if deficit > 30 {
		weight = 100
	}
	risk := map[string]float64{"openai": 0.7, "deepseek": 0.4, "claude": 0.3, "gemini": 0.6}[p.Faction]
	return weight * (1.6 - risk)
}
func (e *scorer) difference(own, opponent float64) float64 {
	delta := own - opponent
	me, op := e.s.Players[e.seat].Faction, e.s.Players[1-e.seat].Faction
	if delta == 0 && (me == "claude") != (op == "claude") {
		if me == "claude" {
			return 1
		}
		return -1
	}
	return delta
}
func (e *scorer) weight(a Action) float64 {
	p, op := e.s.Players[e.seat], e.s.Players[1-e.seat]
	scores := e.s.Scores()
	if a.Kind == "pass" {
		if len(p.Hand) == 0 && p.LeaderUsed {
			return 100
		}
		if op.Passed {
			if e.difference(float64(scores[e.seat]), float64(scores[1-e.seat])) > 0 {
				return 100
			}
			return 0
		}
		return e.passWeight()
	}
	if a.Kind == "leader" {
		ability := firstAbility(e.s.definition(p.Leader))
		if _, ok := hornPosition(ability); ok {
			return max(0, e.immediate(a, normalForecast()))
		}
		switch ability {
		case "leader_gemini_clear":
			return max(0, e.immediate(a, normalForecast())+9*float64(min(2, len(p.Deck))))
		case "leader_deepseek_recover":
			if target := e.medic(p.Grave, nil); target != 0 {
				return max(0, e.recovery(target, nil, false))
			}
			return 0
		case "leader_claude_deny":
			if !op.LeaderUsed {
				return 8
			}
			return 0
		case "leader_claude_retrieve":
			if len(e.s.filterUnits(op.Grave)) > 0 {
				return 12
			}
			return 0
		}
		draw := 0.0
		if len(p.Deck) > 0 {
			draw = 9
		}
		switch p.Faction {
		case "deepseek":
			return draw + max(0, e.immediate(a, normalForecast())*2)
		case "claude":
			return draw + e.shield()
		case "gemini":
			return max(0, e.analysis()+draw)
		default:
			if len(e.s.filterUnits(p.Hand)) > 0 {
				draw += 3
			}
			return draw
		}
	}
	id := a.Card
	d := e.s.definition(id)
	ability := firstAbility(d)
	usesImmediate := slices.Contains([]string{"muster", "mardroeme", "berserker", "morale", "bond", "horn", "long_context", "spy"}, ability) || strings.HasPrefix(ability, "scorch_") || d.Row == "agile"
	score := 0.0
	switch {
	case d.Type == "skill" && e.s.has(id, "horn"):
		row := slices.Index(rows, a.Row)
		if e.s.Board[e.seat][row].Special == 0 {
			v := e.row(e.seat, row)
			before := v.total()
			v.effect(*e.s.card(id), 1)
			score = max(0, float64(v.total()-before))
		}
	case ability == "decoy":
		if target := e.decoyTarget(); target != 0 {
			score = max(0, e.decoyValue(target))
		}
	case ability == "spy":
		score = e.immediate(a, normalForecast()) + 9*float64(min(2, len(p.Deck)))
	case usesImmediate:
		score = e.immediate(a, normalForecast())
	case ability == "avenger":
		score = 1
		if p.Lives > 1 {
			score = float64(d.AvengerForm.Power)
		}
	case ability == "scorch":
		score = max(0, e.scorch())
	case ability == "chain_of_thought":
		if len(e.s.filterUnits(p.Hand)) > 0 {
			score = 6
		}
	case ability == "safety_layer":
		score = e.shield()
	case ability == "context_window":
		if len(p.Hand) >= 3 {
			score = 4
		}
	case d.Row == "weather":
		score = max(0, e.weather(ability == "clear", ability))
	default:
		side, row := e.seat, slices.Index(rows, d.Row)
		if e.s.has(id, "spy") {
			side = 1 - side
		}
		if row >= 0 {
			v := e.row(side, row)
			score = float64(v.power(*e.s.card(id)))
			if len(d.Abilities) > 0 && d.Abilities[len(d.Abilities)-1] == "medic" {
				if target := e.medic(p.Grave, nil); target != 0 {
					score += max(0, e.recovery(target, nil, false))
				}
			}
		}
	}
	score += e.setup(a)
	if a.Row != "" && e.s.unit(id) && p.Boost != 0 && !usesImmediate {
		side := e.seat
		if e.s.has(id, "spy") {
			side = 1 - side
		}
		v := e.row(side, slices.Index(rows, a.Row))
		c := *e.s.card(id)
		v.add(c)
		before := v.total()
		for i := range v.cards {
			if v.cards[i].ID == id {
				v.cards[i].Bonus += p.Boost
			}
		}
		gain := float64(v.total() - before)
		if side != e.seat {
			gain = -gain
		}
		score += gain
	}
	if ability == "deep_think" {
		score += float64(min(6, len(p.Hand)))
	}
	if ability == "future_predict" && len(e.s.filterUnits(p.Hand)) > 0 {
		score += 4
	}
	if ability == "visual_analysis" {
		score += e.analysis()
	}
	return max(0, e.strategy(a, score))
}
func (e *scorer) strategy(a Action, base float64) float64 {
	d := e.s.definition(a.Card)
	ability := firstAbility(d)
	scores := e.s.Scores()
	faction := e.s.Players[e.seat].Faction
	if d.Type == "hero" {
		if faction == "deepseek" {
			count := 0
			for _, row := range e.s.Board[e.seat] {
				count += len(row.Cards)
			}
			return base + float64(max(0, 9-count))
		}
		if e.s.Round == 1 && scores[1-e.seat] < 10 {
			return base * 0.5
		}
	}
	if faction == "claude" && slices.Contains([]string{"scorch", "frost", "fog", "rain", "medic"}, ability) {
		return base * 1.35
	}
	if faction == "openai" && scores[e.seat] < scores[1-e.seat] && d.Power >= scores[1-e.seat]-scores[e.seat] {
		return base * 1.2
	}
	if faction == "gemini" && slices.Contains([]string{"clear", "medic"}, ability) {
		return base * 1.25
	}
	return base
}
func (e *scorer) options(actions []Action) []WeightedAction {
	var out []WeightedAction
	indexes := map[Action]int{}
	for _, a := range actions {
		key := a
		key.Row = ""
		candidate := WeightedAction{a, e.weight(a)}
		if i, ok := indexes[key]; ok {
			if candidate.Weight > out[i].Weight {
				out[i] = candidate
			}
		} else {
			indexes[key] = len(out)
			out = append(out, candidate)
		}
	}
	return out
}
func (e *scorer) cost(a Action) int {
	if a.Kind == "leader" {
		return 10
	}
	d := e.s.definition(a.Card)
	cost := max(1, d.Power)
	if d.Type == "hero" {
		cost += 20
	}
	if e.s.has(a.Card, "medic") {
		cost += 8
	}
	return cost
}
func (e *scorer) choose(actions []Action) Action {
	scores := e.s.Scores()
	p, op := e.s.Players[e.seat], e.s.Players[1-e.seat]
	delta := e.difference(float64(scores[e.seat]), float64(scores[1-e.seat]))
	if op.Passed && (delta > 0 || delta == 0 && op.Lives == 1 && p.Lives > 1) {
		return Action{Kind: "pass"}
	}
	if op.Passed {
		var finishers []Action
		for _, a := range actions {
			if a.Kind != "pass" && e.difference(float64(scores[e.seat])+e.immediate(a, normalForecast()), float64(scores[1-e.seat])) > 0 {
				finishers = append(finishers, a)
			}
		}
		slices.SortStableFunc(finishers, func(a, b Action) int {
			if d := e.cost(a) - e.cost(b); d != 0 {
				return d
			}
			d := e.immediate(a, normalForecast()) - e.immediate(b, normalForecast())
			if d < 0 {
				return -1
			}
			if d > 0 {
				return 1
			}
			return 0
		})
		if len(finishers) > 0 {
			return finishers[0]
		}
	}
	options := e.options(actions)
	total := 0.0
	for _, option := range options {
		total += option.Weight
	}
	if total == 0 {
		return Action{Kind: "pass"}
	}
	roll := e.roll() * total
	for _, option := range options {
		roll -= option.Weight
		if roll < 0 {
			return option.Action
		}
	}
	return options[len(options)-1].Action
}

// LocalDecision can also be used by a future opponent provider. It reads public
// enemy state and the acting seat's cards; it never reads enemy hidden cards.
func LocalDecision(s State, seat int, random io.Reader) (Action, error) {
	legal := s.Legal(seat)
	if len(legal) == 0 || s.Choices[seat] != nil {
		return Action{}, ErrInvalid
	}
	e := newScorer(&s, seat, random)
	action := e.choose(legal)
	return action, e.err
}
