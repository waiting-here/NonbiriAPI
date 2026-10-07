package engine

import (
	"slices"
)

func (s *State) weatherAt(row int) bool {
	for _, id := range s.Weather {
		if s.has(id, "frost") && row == 0 || s.has(id, "fog") && row == 1 || s.has(id, "rain") && row == 2 || s.has(id, "storm") && row > 0 {
			return true
		}
	}
	return false
}
func (s *State) effects(seat, row int) (morale, horn, catalyst int) {
	r := s.Board[seat][row]
	ids := r.Cards
	if r.Special != 0 {
		ids = append(slices.Clone(ids), r.Special)
	}
	for _, id := range ids {
		if s.has(id, "morale") {
			morale++
		}
		if s.has(id, "horn") {
			horn++
		}
		if s.has(id, "mardroeme") {
			catalyst++
		}
	}
	return
}
func (s *State) Power(id int) int {
	return s.card(id).Power
}
func (s *State) calculatePower(id int) int {
	c := s.card(id)
	d := s.definition(id)
	if s.has(id, "decoy") {
		return 0
	}
	if d.Type == "hero" {
		return d.Power + c.Growth
	}
	value := d.Power + c.Bonus
	seat, row, onBoard := s.locate(id)
	if !onBoard {
		return value
	}
	if s.weatherAt(row) {
		value = min(1, value)
	}
	if s.has(id, "bond") {
		count := 0
		for _, other := range s.Board[seat][row].Cards {
			if s.card(other).Definition == c.Definition {
				count++
			}
		}
		value *= max(1, count)
	}
	morale, horn, _ := s.effects(seat, row)
	if s.has(id, "morale") {
		morale--
	}
	if s.has(id, "horn") {
		horn--
	}
	value += max(0, morale)
	if horn > 0 {
		value *= 2
	}
	return value
}
func (s *State) RowTotal(seat, row int) int {
	return s.Board[seat][row].Total
}
func (s *State) refreshRow(seat, row int) {
	total := 0
	for _, id := range s.Board[seat][row].Cards {
		s.card(id).Power = s.calculatePower(id)
		total += s.card(id).Power
	}
	s.Board[seat][row].Total = total
}
func (s *State) refreshAll() {
	for seat := range 2 {
		for row := range 3 {
			s.refreshRow(seat, row)
		}
	}
}
func (s *State) Scores() [2]int {
	var out [2]int
	for seat := range 2 {
		for row := range 3 {
			out[seat] += s.RowTotal(seat, row)
		}
	}
	return out
}
func (s *State) difference() int {
	scores := s.Scores()
	diff := scores[0] - scores[1]
	if diff == 0 && (s.Players[0].Faction == "claude") != (s.Players[1].Faction == "claude") {
		if s.Players[0].Faction == "claude" {
			return 1
		}
		return -1
	}
	return diff
}

// detach removes an existing physical card. Its holder follows the original
// spy/retrieval rules, independently of the board side containing it.
func (s *State) detach(id int) (fromRow bool, seat, row int) {
	seat, row, fromRow = s.locate(id)
	if fromRow {
		r := &s.Board[seat][row]
		if r.Special == id {
			r.Special = 0
		} else {
			r.Cards, _ = remove(r.Cards, id)
		}
		c := s.card(id)
		c.Bonus, c.Growth = 0, 0
		c.Power = s.definition(id).Power
		s.refreshRow(seat, row)
		return
	}
	for i := range s.Players {
		p := &s.Players[i]
		for _, container := range []*[]int{&p.Hand, &p.Deck, &p.Grave} {
			var found bool
			*container, found = remove(*container, id)
			if found {
				p.KnownTop, _ = remove(p.KnownTop, id)
				return
			}
		}
	}
	s.Weather, _ = remove(s.Weather, id)
	return
}
func (s *State) leave(id int, destination string) {
	fromRow, seat, row := s.detach(id)
	c := s.card(id)
	if destination == "hand" {
		s.addHand(c.Owner, id, 0)
	}
	if destination == "grave" && !c.Ephemeral {
		s.Players[c.Owner].Grave = append(s.Players[c.Owner].Grave, id)
	}
	if fromRow && s.has(id, "avenger") {
		form := s.definition(id).AvengerForm
		newID := s.makeCard(form.ID, c.Owner)
		s.prepend(Effect{Kind: "place", Card: newID, Seat: seat, Row: row, Value: "fixed_side"})
	}
}
func (s *State) deploy(id, row int) {
	c := s.card(id)
	owner := c.Owner
	if slices.Contains(s.Players[owner].Hand, id) && s.unit(id) {
		c.Bonus += s.Players[owner].Boost
		s.Players[owner].Boost = 0
	}
	s.detach(id)
	side := owner
	if s.has(id, "spy") {
		side = 1 - side
	}
	s.place(id, side, row)
}
func (s *State) place(id, side, row int) {
	r := &s.Board[side][row]
	if s.definition(id).Type == "skill" && s.has(id, "horn") {
		r.Special = id
	} else {
		r.Cards = s.insertSorted(r.Cards, id)
	}
	var effects []Effect
	for _, ability := range s.definition(id).Abilities {
		effects = append(effects, Effect{Kind: "ability", Card: id, Seat: s.card(id).Owner, Row: row, Value: ability})
	}
	effects = append(effects, Effect{Kind: "refresh_row", Seat: side, Row: row})
	s.prepend(effects...)
}

func (s *State) insertSorted(ids []int, id int) []int {
	order := cardOrder[s.card(id).Definition]
	for i, other := range ids {
		if order < cardOrder[s.card(other).Definition] {
			return slices.Insert(ids, i, id)
		}
	}
	return append(ids, id)
}
func (s *State) clearWeather() {
	ids := slices.Clone(s.Weather)
	for _, id := range ids {
		s.leave(id, "grave")
	}
	s.refreshAll()
}

func (s *State) Legal(seat int) []Action {
	out := []Action{}
	if seat < 0 || seat > 1 || s.Result != nil {
		return out
	}
	if choice := s.Choices[seat]; choice != nil {
		if len(choice.Rows) > 0 {
			for _, row := range choice.Rows {
				out = append(out, Action{Kind: "choose_row", Row: row})
			}
		} else if choice.Remaining > 0 {
			kind := "choose"
			if choice.Kind == "mulligan" {
				kind = "redraw"
			}
			for _, id := range choice.Options {
				out = append(out, Action{Kind: kind, Card: id})
			}
		}
		if choice.CanQuit || choice.Remaining == 0 {
			out = append(out, Action{Kind: "continue"})
		}
		return out
	}
	if s.waiting() || s.Turn != seat || s.Players[seat].Passed {
		return out
	}
	for _, id := range s.Players[seat].Hand {
		d := s.definition(id)
		if s.has(id, "decoy") {
			if len(s.ownUnits(seat)) > 0 {
				out = append(out, Action{Kind: "play", Card: id})
			}
		} else if d.Type == "skill" && s.has(id, "horn") {
			for row, name := range rows {
				if s.Board[seat][row].Special == 0 {
					out = append(out, Action{Kind: "play", Card: id, Row: name})
				}
			}
		} else if d.Row == "agile" {
			for _, name := range rows[:2] {
				out = append(out, Action{Kind: "play", Card: id, Row: name})
			}
		} else if d.Row == "special" || d.Type == "weather" {
			out = append(out, Action{Kind: "play", Card: id})
		} else {
			out = append(out, Action{Kind: "play", Card: id, Row: d.Row})
		}
	}
	if !s.Players[seat].LeaderUsed {
		out = append(out, Action{Kind: "leader"})
	}
	return append(out, Action{Kind: "pass"})
}
