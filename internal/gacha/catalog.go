// Package gacha provides the card collection activity.
package gacha

import (
	_ "embed"
	"encoding/json"
	"fmt"
)

type Rarity string

const (
	N   Rarity = "N"
	R   Rarity = "R"
	SR  Rarity = "SR"
	SSR Rarity = "SSR"
)

var rarities = [4]Rarity{N, R, SR, SSR}

type Card struct {
	ID        string `json:"id"`
	Theme     string `json:"theme"`
	Character string `json:"character"`
	Name      string `json:"name"`
	NameEN    string `json:"name_en"`
	Rarity    Rarity `json:"rarity"`
	Source    string `json:"source"`
	Art       string `json:"art"`
	Thumb     string `json:"thumb"`
}
type Theme struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	NameEN    string `json:"name_en"`
	Rarity    Rarity `json:"rarity"`
	Source    string `json:"source"`
	Tagline   string `json:"tagline"`
	TaglineEN string `json:"tagline_en"`
	Art       string `json:"art"`
}
type Catalog struct {
	Version    int      `json:"version"`
	Characters []string `json:"characters"`
	Themes     []Theme  `json:"themes"`
	Cards      []Card   `json:"cards"`
}

//go:embed catalog.json
var catalogJSON []byte

var catalog = readCatalog()
var cardsByID = func() map[string]Card {
	out := make(map[string]Card, len(catalog.Cards))
	for _, card := range catalog.Cards {
		out[card.ID] = card
	}
	return out
}()

func readCatalog() Catalog {
	var out Catalog
	if err := json.Unmarshal(catalogJSON, &out); err != nil {
		panic(fmt.Sprintf("invalid card catalog: %v", err))
	}
	seen := map[string]bool{}
	for _, card := range out.Cards {
		if seen[card.ID] || card.ID == "" {
			panic("duplicate or empty card identifier")
		}
		seen[card.ID] = true
	}
	return out
}
func permanentCards(rarity Rarity) []Card {
	out := []Card{}
	for _, card := range catalog.Cards {
		if card.Rarity == rarity && card.Source == "permanent" {
			out = append(out, card)
		}
	}
	return out
}
