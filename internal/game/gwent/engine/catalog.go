// Package engine implements the authoritative card game as serializable state
// transitions. Waiting for a player never retains a goroutine or callback.
package engine

import (
	_ "embed"
	"encoding/json"
	"errors"
	"slices"
)

var ErrInvalid = errors.New("gwent: invalid action or deck")
var ErrState = errors.New("gwent: invalid state")

//go:embed cards.json
var cardsJSON []byte

// The source comparator orders type, base power, then localized name. Freeze
// its catalog order so server locale changes cannot alter random target pools.
//
//go:embed order.json
var orderJSON []byte

var cardOrder = loadOrder()

func loadOrder() map[string]int {
	var ids []string
	if err := json.Unmarshal(orderJSON, &ids); err != nil {
		panic(err)
	}
	out := make(map[string]int, len(ids))
	for i, id := range ids {
		out[id] = i
	}
	return out
}

type Form struct {
	ID        string   `json:"id"`
	Name      string   `json:"name"`
	Power     int      `json:"power"`
	Row       string   `json:"row"`
	Abilities []string `json:"abilities"`
}

type Definition struct {
	ID            string   `json:"id"`
	Name          string   `json:"name"`
	Faction       string   `json:"faction"`
	Type          string   `json:"type"`
	Power         int      `json:"power"`
	Row           string   `json:"row"`
	Abilities     []string `json:"abilities"`
	MaxCopies     int      `json:"maxCopies"`
	Image         string   `json:"image"`
	Thumbnail     string   `json:"thumbnail,omitempty"`
	Category      string   `json:"category,omitempty"`
	StarterCopies int      `json:"starterCopies"`
	MusterGroup   string   `json:"musterGroup,omitempty"`
	TransformForm *Form    `json:"transformForm,omitempty"`
	AvengerForm   *Form    `json:"avengerForm,omitempty"`
	Generated     bool     `json:"generated,omitempty"`
	Ephemeral     bool     `json:"ephemeral,omitempty"`
	OriginID      string   `json:"originId,omitempty"`
}

var factions = []string{"openai", "deepseek", "claude", "gemini"}
var rows = []string{"close", "ranged", "siege"}
var definitions, definitionsByID = loadCatalog()

func loadCatalog() ([]Definition, map[string]Definition) {
	var cards []Definition
	if err := json.Unmarshal(cardsJSON, &cards); err != nil {
		panic(err)
	}
	all := slices.Clone(cards)
	for _, source := range cards {
		for _, entry := range []struct {
			form      *Form
			ephemeral bool
		}{{source.TransformForm, false}, {source.AvengerForm, true}} {
			if entry.form == nil {
				continue
			}
			f := entry.form
			card := source
			card.ID, card.Name, card.Power, card.Row, card.Abilities = f.ID, f.Name, f.Power, f.Row, slices.Clone(f.Abilities)
			card.Type, card.Category, card.OriginID = "unit", "generated", source.ID
			card.TransformForm, card.AvengerForm = nil, nil
			card.MaxCopies, card.StarterCopies = 1, 0
			card.Generated, card.Ephemeral = true, entry.ephemeral
			all = append(all, card)
		}
		if source.Type != "leader" {
			continue
		}
		options := map[string][][2]string{
			"openai":   {{"assault", "前线统筹"}, {"compute", "算力统筹"}},
			"deepseek": {{"recover", "检查点调度"}, {"rebirth", "第三局重启"}},
			"claude":   {{"deny", "协议封锁"}, {"retrieve", "上下文回收"}},
			"gemini":   {{"clear", "晴空解析"}, {"sensors", "感知统筹"}},
		}
		for _, option := range options[source.Faction] {
			card := source
			card.ID, card.Name = source.ID+"_"+option[0], source.Name+" · "+option[1]
			card.Abilities = []string{"leader_" + source.Faction + "_" + option[0]}
			all = append(all, card)
		}
	}
	byID := make(map[string]Definition, len(all))
	for _, card := range all {
		if _, exists := byID[card.ID]; exists || card.ID == "" {
			panic("duplicate gwent card")
		}
		byID[card.ID] = card
	}
	return all, byID
}

func Catalog() []Definition {
	// Catalog values contain slices and forms; callers must not mutate the registry.
	data, _ := json.Marshal(definitions)
	var out []Definition
	_ = json.Unmarshal(data, &out)
	return out
}

type DeckEntry struct {
	ID    string `json:"id"`
	Count int    `json:"count"`
}
type Deck struct {
	Faction string      `json:"faction"`
	Leader  string      `json:"leader"`
	Cards   []DeckEntry `json:"cards"`
}

func ValidateDeck(deck Deck) error {
	leader, ok := definitionsByID[deck.Leader]
	if !ok || leader.Type != "leader" || leader.Faction != deck.Faction || !slices.Contains(factions, deck.Faction) {
		return ErrInvalid
	}
	seen := map[string]bool{}
	units, specials, heroes := 0, 0, 0
	for _, entry := range deck.Cards {
		card, ok := definitionsByID[entry.ID]
		if !ok || seen[entry.ID] || card.Type == "leader" || card.Generated || (card.Faction != deck.Faction && card.Faction != "neutral") || entry.Count < 1 || entry.Count > card.MaxCopies {
			return ErrInvalid
		}
		seen[entry.ID] = true
		if card.Type == "unit" || card.Type == "hero" {
			units += entry.Count
		} else {
			specials += entry.Count
		}
		if card.Type == "hero" {
			heroes += entry.Count
		}
	}
	if units < 22 || specials > 10 || heroes > 4 {
		return ErrInvalid
	}
	return nil
}

func StarterDeck(faction string) Deck {
	deck := Deck{Faction: faction, Leader: faction + "_leader", Cards: []DeckEntry{}}
	for _, card := range definitions {
		if card.Type != "leader" && !card.Generated && (card.Faction == faction || card.Faction == "neutral") && card.StarterCopies > 0 {
			deck.Cards = append(deck.Cards, DeckEntry{card.ID, card.StarterCopies})
		}
	}
	return deck
}
