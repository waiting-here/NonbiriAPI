package engine

import (
	_ "embed"
	"encoding/json"
	"io"
	"slices"
	"strings"

	"github.com/waiting-here/NonbiriAPI/internal/game/randomness"
)

//go:embed presets.json
var presetJSON []byte

type Preset struct {
	ID          string   `json:"id"`
	Name        string   `json:"name"`
	Description string   `json:"description"`
	Difficulty  string   `json:"difficulty"`
	CoreIDs     []string `json:"coreIds"`
	Deck        Deck     `json:"deck"`
}

func Presets() []Preset {
	var out []Preset
	if err := json.Unmarshal(presetJSON, &out); err != nil {
		panic(err)
	}
	return out
}
func PresetDeck(faction, id string) (Deck, bool) {
	for _, p := range Presets() {
		if p.Deck.Faction == faction && p.ID == id {
			return p.Deck, true
		}
	}
	return Deck{}, false
}

// DecisionState removes hidden opponent identities and unknown draw order.
func DecisionState(s State, seat int) State {
	body, _ := json.Marshal(s)
	_ = json.Unmarshal(body, &s)
	other := 1 - seat
	hidden := map[int]bool{}
	for _, id := range s.Players[other].Hand {
		hidden[id] = true
	}
	for _, id := range s.Players[other].Deck {
		hidden[id] = true
	}
	for i, c := range s.Cards {
		if hidden[c.ID] {
			s.Cards[i] = Card{}
		}
	}
	s.Players[other].Hand = make([]int, len(s.Players[other].Hand))
	s.Players[other].Deck = make([]int, len(s.Players[other].Deck))
	s.Players[other].KnownTop = nil
	slices.Sort(s.Players[seat].Deck)
	s.Choices[other] = nil
	s.Queue, s.Events = nil, nil
	return s
}

// LocalChoice extends the original controller across opening and effect windows.
func LocalChoice(s State, seat int, random io.Reader) (Action, error) {
	choice := s.Choices[seat]
	if choice == nil {
		return LocalDecision(s, seat, random)
	}
	if choice.Kind == "initiative" {
		return Action{Kind: "choose", Card: s.Players[seat].Leader}, nil
	}
	if choice.Kind == "mulligan" {
		legal := s.Legal(seat)
		musters, weather, normal := []int{}, []int{}, []int{}
		for _, id := range s.Players[seat].Hand {
			d := s.definition(id)
			if s.has(id, "muster") {
				musters = append(musters, id)
			}
			if d.Row == "weather" {
				weather = append(weather, id)
			}
			if d.Type != "hero" && len(d.Abilities) == 0 {
				normal = append(normal, id)
			}
		}
		candidates := []int{}
		for len(musters) > 0 {
			last := len(musters) - 1
			curr := musters[last]
			musters = musters[:last]
			name := strings.TrimSpace(strings.SplitN(s.definition(curr).Name, "-", 2)[0])
			group := []int{curr}
			for j := len(musters) - 1; j >= 0; j-- {
				if strings.HasPrefix(s.definition(musters[j]).Name, name) {
					group = append(group, musters[j])
					musters = slices.Delete(musters, j, j+1)
				}
			}
			slices.SortStableFunc(group, func(a, b int) int { return cardOrder[s.definition(a).ID] - cardOrder[s.definition(b).ID] })
			if len(group) > 1 {
				candidates = append(candidates, group[:len(group)-1]...)
			}
		}
		if len(weather) > 1 {
			keep, err := randomness.Index(random, uint64(len(weather)))
			if err != nil {
				return Action{}, err
			}
			for i, id := range weather {
				if i != int(keep) {
					candidates = append(candidates, id)
				}
			}
		}
		slices.SortStableFunc(normal, func(a, b int) int { return cardOrder[s.definition(a).ID] - cardOrder[s.definition(b).ID] })
		candidates = append(candidates, normal...)
		for _, id := range candidates {
			a := Action{Kind: "swap", Card: id}
			if s.definition(id).Power < 15 && slices.Contains(legal, a) {
				return a, nil
			}
		}
		return Action{Kind: "continue"}, nil
	}
	if choice.Kind == "context_window" {
		candidates := slices.Clone(choice.Initial)
		if len(candidates) == 0 {
			candidates = slices.Clone(choice.Options)
		}
		candidates = slices.DeleteFunc(candidates, func(id int) bool {
			return !slices.Contains(s.Players[seat].Hand, id) || slices.Contains(choice.Selected, id)
		})
		slices.SortStableFunc(candidates, func(a, b int) int { return s.definition(a).Power - s.definition(b).Power })
		if len(candidates) == 0 {
			return Action{Kind: "continue"}, nil
		}
		return Action{Kind: "choose", Card: candidates[0]}, nil
	}
	var choices AutomaticChoices
	return choices.Next(s, seat, random)
}
