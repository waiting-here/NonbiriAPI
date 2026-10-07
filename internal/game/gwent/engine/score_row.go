package engine

import "slices"

// scoreRow is a small, isolated forecast. Evaluating a move changes neither
// the durable board nor the random stream used for dealing and effects.
type scoreRow struct {
	cards                  []Card
	weather                bool
	morale, horn, catalyst int
	bond                   map[string]int
}

func cardHas(c Card, ability string) bool {
	return ability == "morale" && c.CopiedMorale || slices.Contains(definitionsByID[c.Definition].Abilities, ability)
}
func (e *scorer) row(seat, row int) scoreRow {
	v := scoreRow{weather: e.s.weatherAt(row), bond: map[string]int{}}
	r := e.s.Board[seat][row]
	for _, id := range r.Cards {
		c := *e.s.card(id)
		v.cards = append(v.cards, c)
		v.effect(c, 1)
	}
	if r.Special != 0 {
		v.effect(*e.s.card(r.Special), 1)
	}
	return v
}
func (v *scoreRow) effect(c Card, direction int) {
	for _, name := range definitionsByID[c.Definition].Abilities {
		switch name {
		case "morale":
			v.morale += direction
		case "horn":
			v.horn += direction
		case "mardroeme":
			v.catalyst += direction
		case "bond":
			v.bond[c.Definition] += direction
		}
	}
	if c.CopiedMorale {
		v.morale += direction
	}
}
func (v *scoreRow) power(c Card) int {
	d := definitionsByID[c.Definition]
	if cardHas(c, "decoy") {
		return 0
	}
	if d.Type == "hero" {
		return d.Power + c.Growth
	}
	power := d.Power + c.Bonus
	if v.weather {
		power = min(1, power)
	}
	if n := v.bond[c.Definition]; n > 1 {
		power *= n
	}
	morale, horn := v.morale, v.horn
	if cardHas(c, "morale") {
		morale--
	}
	if cardHas(c, "horn") {
		horn--
	}
	power += max(0, morale)
	if horn > 0 {
		power *= 2
	}
	return power
}
func (v *scoreRow) total() int {
	total := 0
	for _, c := range v.cards {
		total += v.power(c)
	}
	return total
}
func (v *scoreRow) add(c Card) {
	if !(definitionsByID[c.Definition].Type == "skill" && cardHas(c, "horn")) {
		v.cards = append(v.cards, c)
	}
	v.effect(c, 1)
}
func (v *scoreRow) remove(id int) {
	for i, c := range v.cards {
		if c.ID == id {
			v.cards = slices.Delete(v.cards, i, i+1)
			v.effect(c, -1)
			return
		}
	}
}
func formCard(c Card, form *Form) Card {
	return Card{Definition: form.ID, Owner: c.Owner, Power: form.Power}
}
func (v *scoreRow) transform() {
	if v.catalyst == 0 {
		return
	}
	for i, c := range v.cards {
		if !cardHas(c, "berserker") {
			continue
		}
		form := formCard(c, definitionsByID[c.Definition].TransformForm)
		v.cards[i] = form
		v.effect(c, -1)
		v.effect(form, 1)
	}
}
func (e *scorer) maxUnits(seat, row int) []int {
	var ids []int
	highest := 0
	for _, id := range e.s.Board[seat][row].Cards {
		if !e.s.unit(id) {
			continue
		}
		power := e.s.Power(id)
		if len(ids) == 0 || power > highest {
			ids = []int{id}
			highest = power
		} else if power == highest {
			ids = append(ids, id)
		}
	}
	return ids
}
func (e *scorer) removalLoss(seat, row int, targets []int) float64 {
	if len(targets) == 0 {
		return 0
	}
	v := e.row(seat, row)
	before := v.total()
	for _, id := range targets {
		v.remove(id)
	}
	for _, id := range targets {
		if e.s.has(id, "avenger") {
			v.add(formCard(*e.s.card(id), e.s.definition(id).AvengerForm))
		}
	}
	return float64(before - v.total())
}
func (e *scorer) scorchRow(row int) float64 {
	opponent := 1 - e.seat
	if e.s.Board[opponent][row].Total < 10 || e.s.Players[opponent].Shield {
		return 0
	}
	return e.removalLoss(opponent, row, e.maxUnits(opponent, row))
}
func (e *scorer) scorch() float64 {
	maximum := 0
	for seat := range 2 {
		for _, id := range e.s.ownUnits(seat) {
			maximum = max(maximum, e.s.Power(id))
		}
	}
	score := 0.0
	for seat := range 2 {
		if seat != e.seat && e.s.Players[seat].Shield {
			continue
		}
		for row := range 3 {
			var targets []int
			for _, id := range e.s.Board[seat][row].Cards {
				if e.s.unit(id) && e.s.Power(id) == maximum {
					targets = append(targets, id)
				}
			}
			loss := e.removalLoss(seat, row, targets)
			if seat == e.seat {
				loss = -loss
			}
			score += loss
		}
	}
	return score
}
func (e *scorer) weather(clear bool, kind string) float64 {
	score := 0.0
	for seat := range 2 {
		for row := range 3 {
			v := e.row(seat, row)
			affected := kind == []string{"frost", "fog", "rain"}[row] || kind == "storm" && row > 0
			if clear && !v.weather || !clear && (!affected || v.weather) {
				continue
			}
			before := v.total()
			v.weather = !clear
			delta := float64(v.total() - before)
			if seat != e.seat {
				delta = -delta
			}
			score += delta
		}
	}
	return score
}
func (e *scorer) shield() float64 {
	p, op := e.s.Players[e.seat], e.s.Players[1-e.seat]
	if p.Shield || op.Passed || len(op.Hand) == 0 {
		return 0
	}
	maximum := 0
	for seat := range 2 {
		for _, id := range e.s.ownUnits(seat) {
			maximum = max(maximum, e.s.Power(id))
		}
	}
	loss, global := 0.0, 0.0
	for row := range 3 {
		r := e.s.Board[e.seat][row]
		if r.Total >= 10 {
			loss = max(loss, e.removalLoss(e.seat, row, e.maxUnits(e.seat, row)))
		}
		var targets []int
		for _, id := range r.Cards {
			if e.s.unit(id) && e.s.Power(id) == maximum {
				targets = append(targets, id)
			}
		}
		global += e.removalLoss(e.seat, row, targets)
	}
	return max(0, loss, global) * 0.5
}
