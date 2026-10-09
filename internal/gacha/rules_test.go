package gacha

import (
	"math"
	"testing"
)

func TestDefaultRatesAndPity(t *testing.T) {
	r := DefaultRules()
	got, err := ConsolidatedRates(r)
	if err != nil {
		t.Fatal(err)
	}
	want := [4]float64{.51317544, .32656619, .13817568, .02208269}
	for i, n := range got {
		if math.Abs(n-want[i]) > 1e-8 {
			t.Fatalf("rate %d: %v", i, n)
		}
	}
	roll := func(int) (int, error) { return 0, nil }
	p := freshPity(r)
	for i := 1; i <= 60; i++ {
		item, next, err := drawOne(r, p, nil, roll, false)
		if err != nil {
			t.Fatal(err)
		}
		p = next
		if i == 60 && cardsByID[item.CardID].Rarity != SSR {
			t.Fatal("missing SSR hard pity")
		}
		if i%10 == 0 && cardsByID[item.CardID].Rarity != SR && cardsByID[item.CardID].Rarity != SSR {
			t.Fatal("missing SR hard pity")
		}
	}
	if p.SR != 0 || p.SSR != 0 {
		t.Fatal(p)
	}
}
func TestIndependentFeaturedGuaranteesAndAbsentTier(t *testing.T) {
	r := DefaultRules()
	r.Weights = [4]int{0, 0, 10000, 0}
	p := freshPity(r)
	list := []string{"h-chatgpt", "i-chatgpt"}
	miss := func(n int) (int, error) { return n - 1, nil }
	item, p, err := drawOne(r, p, list, miss, false)
	if err != nil {
		t.Fatal(err)
	}
	if cardsByID[item.CardID].Source != "permanent" || !p.SRFeatured || p.SSRFeatured {
		t.Fatal(item, p)
	}
	_, absent, err := drawOne(r, p, []string{"i-chatgpt"}, miss, false)
	if err != nil {
		t.Fatal(err)
	}
	if !absent.SRFeatured {
		t.Fatal("empty tier consumed guarantee")
	}
	item, p, err = drawOne(r, p, list, miss, false)
	if err != nil {
		t.Fatal(err)
	}
	if item.CardID != "h-chatgpt" || p.SRFeatured {
		t.Fatal(item, p)
	}
	p.SRFeatured = true
	item, p, err = drawOne(r, p, list, miss, true)
	if err != nil {
		t.Fatal(err)
	}
	if cardsByID[item.CardID].Rarity != SSR || !p.SRFeatured || !p.SSRFeatured {
		t.Fatal(item, p)
	}
}
func TestProtectedPityAcrossRepeatedConfigurationChanges(t *testing.T) {
	r := DefaultRules()
	p := freshPity(r)
	p.SSR = 40
	p.SR = 4
	r.SSRPity = 100
	r.SRPity = 20
	p = protectPity(p, r)
	if p.SSRLimit != 60 || p.SRLimit != 10 {
		t.Fatal(p)
	}
	r.SSRPity = 50
	r.SRPity = 8
	p = protectPity(p, r)
	r.SSRPity = 100
	r.SRPity = 20
	p = protectPity(p, r)
	if p.SSRLimit != 50 || p.SRLimit != 8 {
		t.Fatal(p)
	}
	item, p, err := drawOne(r, p, nil, func(int) (int, error) { return 0, nil }, true)
	if err != nil || item.Guarantee != "newcomer" || p.SSRLimit != 100 || p.SRLimit != 20 {
		t.Fatal(item, p, err)
	}
}
func TestCatalogCardAndThemeIntegrity(t *testing.T) {
	if len(catalog.Cards) != 55 || len(catalog.Themes) != 11 {
		t.Fatal("unexpected launch catalog")
	}
	for _, theme := range catalog.Themes {
		seen := map[string]bool{}
		for _, card := range catalog.Cards {
			if card.Theme == theme.ID {
				if card.Rarity != theme.Rarity || card.Source != theme.Source || seen[card.Character] {
					t.Fatal(card)
				}
				seen[card.Character] = true
			}
		}
		if len(seen) != 5 {
			t.Fatal(theme)
		}
	}
}
