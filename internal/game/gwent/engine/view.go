package engine

import "slices"

type CardView struct {
	Definition
	InstanceID int `json:"instance_id"`
	BasePower  int `json:"base_power"`
	Power      int `json:"power"`
}
type PlayerView struct {
	Faction         string     `json:"faction"`
	Lives           int        `json:"lives"`
	Passed          bool       `json:"passed"`
	HandCount       int        `json:"hand_count"`
	DeckCount       int        `json:"deck_count"`
	Grave           []CardView `json:"grave"`
	Leader          CardView   `json:"leader"`
	LeaderAvailable bool       `json:"leader_available"`
	Boost           int        `json:"boost"`
	Shield          bool       `json:"shield"`
	KnownDeckTop    []CardView `json:"known_deck_top,omitempty"`
}
type RowView struct {
	Side    string     `json:"side"`
	Row     string     `json:"row"`
	Total   int        `json:"total"`
	Weather bool       `json:"weather"`
	Cards   []CardView `json:"cards"`
	Special *CardView  `json:"special,omitempty"`
}
type ChoiceView struct {
	Kind      string     `json:"kind"`
	Cards     []CardView `json:"cards"`
	Rows      []string   `json:"rows"`
	Remaining int        `json:"remaining"`
	CanQuit   bool       `json:"can_quit"`
}
type View struct {
	Version int           `json:"version"`
	Round   int           `json:"round"`
	Phase   string        `json:"phase"`
	Turn    int           `json:"turn"`
	Self    PlayerView    `json:"self"`
	Enemy   PlayerView    `json:"enemy"`
	Hand    []CardView    `json:"hand"`
	Board   []RowView     `json:"board"`
	Weather []CardView    `json:"weather"`
	Choice  *ChoiceView   `json:"choice,omitempty"`
	Legal   []Action      `json:"legal_actions"`
	Rounds  []RoundRecord `json:"rounds"`
	Result  *Result       `json:"result,omitempty"`
}

func (s *State) cardView(id int) CardView {
	d := s.definition(id)
	d.Abilities = slices.Clone(d.Abilities)
	if s.card(id).CopiedMorale {
		d.Abilities = append(d.Abilities, "morale")
	}
	return CardView{Definition: d, InstanceID: id, BasePower: d.Power, Power: s.Power(id)}
}
func (s *State) cardViews(ids []int) []CardView {
	out := make([]CardView, 0, len(ids))
	for _, id := range ids {
		out = append(out, s.cardView(id))
	}
	return out
}
func Project(s State, viewer int) (View, error) {
	if viewer < 0 || viewer > 1 {
		return View{}, ErrInvalid
	}
	player := func(seat int) PlayerView {
		p := s.Players[seat]
		return PlayerView{Faction: p.Faction, Lives: p.Lives, Passed: p.Passed, HandCount: len(p.Hand), DeckCount: len(p.Deck), Grave: s.cardViews(p.Grave), Leader: s.cardView(p.Leader), LeaderAvailable: !p.LeaderUsed, Boost: p.Boost, Shield: p.Shield}
	}
	v := View{Version: Version, Round: s.Round, Phase: s.Phase(), Turn: s.Turn, Self: player(viewer), Enemy: player(1 - viewer), Hand: s.cardViews(s.Players[viewer].Hand), Board: []RowView{}, Weather: s.cardViews(s.Weather), Legal: s.Legal(viewer), Rounds: slices.Clone(s.Rounds), Result: s.Result}
	v.Self.KnownDeckTop = s.cardViews(s.Players[viewer].KnownTop)
	for _, seat := range []int{1 - viewer, viewer} {
		side := "self"
		if seat != viewer {
			side = "enemy"
		}
		for row, name := range rows {
			r := s.Board[seat][row]
			out := RowView{Side: side, Row: name, Total: s.RowTotal(seat, row), Weather: s.weatherAt(row), Cards: s.cardViews(r.Cards)}
			if r.Special != 0 {
				card := s.cardView(r.Special)
				out.Special = &card
			}
			v.Board = append(v.Board, out)
		}
	}
	if c := s.Choices[viewer]; c != nil {
		v.Choice = &ChoiceView{Kind: c.Kind, Cards: s.cardViews(c.Options), Rows: slices.Clone(c.Rows), Remaining: c.Remaining, CanQuit: c.CanQuit}
	}
	return v, nil
}
