package engine

import (
	"math"
	"slices"
	"strings"
)

type forecastOptions struct {
	fromHand, growth bool
	seen             []int
}

func normalForecast() forecastOptions { return forecastOptions{fromHand: true, growth: true} }
func (e *scorer) recovery(id int, seen []int, immediate bool) float64 {
	// Only recursive recovery effects depend on the already visited medics.
	// Reuse other card forecasts across branches of the same recovery chain.
	if !e.s.has(id, "medic") && !e.s.has(id, "long_context") {
		seen = nil
	}
	key := recoveryKey{ID: id, Immediate: immediate}
	copy(key.Seen[:], seen)
	slices.Sort(key.Seen[:])
	if value, ok := e.recoveries[key]; ok {
		return value
	}
	d := e.s.definition(id)
	positions := []string{d.Row}
	if d.Row == "agile" {
		positions = rows[:2]
	}
	value := math.Inf(-1)
	for _, row := range positions {
		value = max(value, e.immediate(Action{Kind: "play", Card: id, Row: row}, forecastOptions{seen: seen}))
	}
	if !immediate && e.s.has(id, "spy") {
		value += 9 * float64(min(2, len(e.s.Players[e.seat].Deck)))
	}
	e.recoveries[key] = value
	return value
}
func (e *scorer) medic(ids, seen []int) int {
	best := 0
	value := math.Inf(-1)
	for _, id := range ids {
		if !e.s.unit(id) || slices.Contains(seen, id) {
			continue
		}
		next := e.recovery(id, seen, false)
		if best == 0 || next > value {
			best = id
			value = next
		}
	}
	return best
}
func (e *scorer) copyValue(target, card, row int, immediate bool) float64 {
	for _, ability := range e.s.definition(target).Abilities {
		switch ability {
		case "medic":
			id := e.medic(e.s.Players[e.seat].Grave, nil)
			if id == 0 {
				return 0
			}
			return e.recovery(id, nil, immediate)
		case "morale":
			v := e.row(e.seat, row)
			if !slices.Contains(e.s.Board[e.seat][row].Cards, card) {
				v.cards = append(v.cards, *e.s.card(card))
			}
			before := v.total()
			v.morale++
			return float64(v.total() - before)
		case "scorch_c", "scorch_r", "scorch_s":
			return e.scorchRow(scorchPosition(ability))
		}
	}
	return 0
}
func (e *scorer) copyTarget(card, row int, ids []int) int {
	best := 0
	value := math.Inf(-1)
	for _, id := range ids {
		next := e.copyValue(id, card, row, false)
		if best == 0 || next > value {
			best = id
			value = next
		}
	}
	return best
}
func (e *scorer) decoyValue(id int) float64 {
	c, d := e.s.card(id), e.s.definition(id)
	p := e.s.Players[e.seat]
	if e.s.has(id, "avenger") {
		return float64(d.AvengerForm.Power - c.Power)
	}
	if e.s.has(id, "spy") && len(p.Deck) > 0 {
		return float64(9*min(2, len(p.Deck)) - c.Power - d.Power)
	}
	if e.s.has(id, "medic") && len(e.s.filterUnits(p.Grave)) > 0 {
		return float64(12 - c.Power)
	}
	for _, ability := range d.Abilities {
		if strings.HasPrefix(ability, "scorch_") {
			return e.scorchRow(scorchPosition(ability)) - float64(c.Power)
		}
	}
	scores := e.s.Scores()
	if p.Lives > 1 && scores[e.seat]-c.Power > scores[1-e.seat] {
		return 1
	}
	return 0
}
func (e *scorer) decoyTarget() int {
	best := 0
	value := math.Inf(-1)
	for _, id := range e.s.ownUnits(e.seat) {
		next := e.decoyValue(id)
		if best == 0 || next > value {
			best = id
			value = next
		}
	}
	return best
}
func (e *scorer) analysis() float64 {
	clear := e.weather(true, "")
	if clear > 0 {
		return clear
	}
	if len(e.s.filterUnits(e.s.Players[e.seat].Hand)) > 0 {
		return 3
	}
	return 0
}
func scorchPosition(ability string) int {
	return map[string]int{"scorch_c": 0, "scorch_r": 1, "scorch_s": 2}[ability]
}
func hornPosition(ability string) (int, bool) {
	row, ok := map[string]int{"leader_openai_assault": 0, "leader_openai_compute": 2, "leader_gemini_sensors": 1}[ability]
	return row, ok
}
func (e *scorer) immediate(a Action, opts forecastOptions) float64 {
	growth := 0.0
	if opts.growth {
		for _, row := range e.s.Board[e.seat] {
			for _, id := range row.Cards {
				if e.s.has(id, "deep_think") {
					growth += float64(max(0, min(1, 6-e.s.card(id).Growth)))
				}
			}
		}
	}
	if a.Kind == "pass" {
		return 0
	}
	p := e.s.Players[e.seat]
	if a.Kind == "leader" {
		ability := e.s.definition(p.Leader).Abilities[0]
		if row, ok := hornPosition(ability); ok {
			if e.s.Board[e.seat][row].Special != 0 {
				return growth
			}
			v := e.row(e.seat, row)
			before := v.total()
			v.horn++
			return float64(v.total()-before) + growth
		}
		switch ability {
		case "leader_gemini_clear":
			return e.weather(true, "") + growth
		case "leader_deepseek_recover":
			id := e.medic(p.Grave, nil)
			if id != 0 {
				return e.recovery(id, nil, true) + growth
			}
			return growth
		case "leader_claude_deny", "leader_claude_retrieve":
			return growth
		}
		if p.Faction == "gemini" {
			return max(0, e.weather(true, "")) + growth
		}
		if p.Faction != "deepseek" {
			return growth
		}
		best := 0
		for _, id := range e.s.ownUnits(e.seat) {
			if best == 0 || e.s.Power(id) > e.s.Power(best) {
				best = id
			}
		}
		if best == 0 {
			return growth
		}
		_, row, _ := e.s.locate(best)
		v := e.row(e.seat, row)
		c := *e.s.card(best)
		c.Bonus += 4
		return float64(v.power(c)-e.s.Power(best)) + growth
	}
	id := a.Card
	c := *e.s.card(id)
	d := e.s.definition(id)
	ability := firstAbility(d)
	if ability == "decoy" {
		target := e.decoyTarget()
		if target == 0 {
			return growth
		}
		_, row, _ := e.s.locate(target)
		v := e.row(e.seat, row)
		before := v.total()
		v.remove(target)
		if e.s.has(target, "avenger") {
			v.add(formCard(*e.s.card(target), e.s.definition(target).AvengerForm))
		}
		return float64(v.total()-before) + growth
	}
	if d.Row == "weather" {
		return e.weather(ability == "clear", ability) + growth
	}
	if ability == "scorch" {
		return e.scorch() + growth
	}
	row := slices.Index(rows, a.Row)
	if row < 0 {
		return growth
	}
	side := e.seat
	if e.s.has(id, "spy") {
		side = 1 - side
	}
	v := e.row(side, row)
	before := v.total()
	if d.Type == "unit" && opts.fromHand {
		c.Bonus += p.Boost
	}
	if ability == "deep_think" {
		c.Growth += max(0, min(1, 6-c.Growth))
	}
	v.add(c)
	v.transform()
	gain := float64(v.total() - before)
	if side != e.seat {
		gain = -gain
	}
	if ability == "visual_analysis" {
		gain += max(0, e.weather(true, ""))
	}
	if strings.HasPrefix(ability, "scorch_") {
		gain += e.scorchRow(scorchPosition(ability))
	}
	if ability == "medic" && !slices.Contains(opts.seen, id) && len(opts.seen) < 3 {
		seen := append(slices.Clone(opts.seen), id)
		target := e.medic(p.Grave, seen)
		if target != 0 {
			gain += e.recovery(target, seen, true)
		}
	}
	if ability == "long_context" {
		var ids []int
		for _, other := range e.s.ownUnits(1 - e.seat) {
			for _, ability := range e.s.definition(other).Abilities {
				if slices.Contains([]string{"medic", "morale", "scorch_c", "scorch_r", "scorch_s"}, ability) {
					ids = append(ids, other)
					break
				}
			}
		}
		if target := e.copyTarget(id, row, ids); target != 0 {
			gain += e.copyValue(target, id, row, true)
		}
	}
	if ability == "muster" {
		for _, container := range [][]int{p.Hand, p.Deck} {
			for _, other := range container {
				md := e.s.definition(other)
				if other == id || !e.s.unit(other) || md.MusterGroup != d.MusterGroup {
					continue
				}
				ri := slices.Index(rows, md.Row)
				if md.Row == "agile" {
					ri = e.agileRow(other)
				}
				ms := e.seat
				if e.s.has(other, "spy") {
					ms = 1 - ms
				}
				rv := e.row(ms, ri)
				gain += float64(rv.power(*e.s.card(other)))
			}
		}
	}
	return gain + growth
}
func (e *scorer) agileRow(id int) int {
	side := e.seat
	if e.s.has(id, "spy") {
		side = 1 - side
	}
	delta := [2]int{}
	for row := range 2 {
		v := e.row(side, row)
		before := v.total()
		v.add(*e.s.card(id))
		delta[row] = v.total() - before
	}
	if delta[0] > delta[1] {
		return 0
	}
	if delta[0] < delta[1] {
		return 1
	}
	// Match the source controller's tie distribution using the recorded stream.
	if e.roll() > 0.5 {
		return 0
	}
	return 1
}
