package artwork

import (
	"encoding/json"
	"os"
	"slices"
	"testing"
)

func fixtureDocument() document {
	return document{Version: 1, Artworks: map[string]Definition{
		"appearance:default": {Rating: General}, "appearance:heart-moment": {Rating: General},
		"cover:permanent": {Rating: General, Name: "Safe", NameEN: "Safe"},
		"card:rated":      {Rating: R18, Scene: "illustration:scene"}, "card:safe": {Rating: General, Scene: "illustration:rated"},
		"illustration:scene": {Rating: General}, "illustration:rated": {Rating: R18},
		"appearance:derived": {Rating: General, Uses: []string{"card:rated"}},
	}, Assets: []AssetRule{
		{Owner: "card:rated", Files: []string{"cards/rated.webp"}, Prefixes: []string{"motion/rated/"}},
		{Owner: "appearance:default", Files: []string{"motion/rated/safe.webp"}, Prefixes: []string{"motion/rated/safe-"}},
		{Owner: "appearance:derived", Files: []string{"appearances/derived.webp"}},
	}}
}
func parseFixture(t *testing.T, doc document) *Catalog {
	t.Helper()
	data, err := json.Marshal(doc)
	if err != nil {
		t.Fatal(err)
	}
	c, err := Parse(data)
	if err != nil {
		t.Fatal(err)
	}
	return c
}
func TestEffectiveRatingsAndAssetSpecificity(t *testing.T) {
	c := parseFixture(t, fixtureDocument())
	for _, p := range []string{"cards/rated.webp", "motion/rated/hero.png", "motion/rated/unused.webp", "appearances/derived.webp"} {
		if rating, ok := c.AssetRating(p); !ok || rating != R18 {
			t.Errorf("%s: %s %v", p, rating, ok)
		}
	}
	for _, p := range []string{"motion/rated/safe.webp", "motion/rated/safe-preview.webp"} {
		if rating, ok := c.AssetRating(p); !ok || rating != General {
			t.Errorf("%s: %s %v", p, rating, ok)
		}
	}
	if c.Allowed("appearance:derived", true) || !c.Allowed("appearance:derived", false) || c.Allowed("missing", false) {
		t.Fatal("allowance mismatch")
	}
	if !c.Allowed("card:safe", true) || c.MomentAllowed("card:safe", true) || !c.MomentAllowed("card:safe", false) {
		t.Fatal("moment scenery did not remain independent")
	}
	for _, p := range []string{"../cards/rated.webp", "/cards/rated.webp", "motion/rated/../x", "cards/rated.webp?x=1"} {
		if _, ok := c.AssetRating(p); ok {
			t.Fatal(p)
		}
	}
	covers := c.Covers()
	covers[0].ID = "changed"
	if c.Covers()[0].ID != "permanent" {
		t.Fatal("mutable covers")
	}
}
func TestCatalogRejectsInvalidDefinitions(t *testing.T) {
	cases := map[string]func(*document){
		"missing rating": func(d *document) { d.Artworks["card:rated"] = Definition{} },
		"unknown rating": func(d *document) { d.Artworks["card:rated"] = Definition{Rating: "adult"} },
		"unknown dependency": func(d *document) {
			d.Artworks["card:rated"] = Definition{Rating: General, Uses: []string{"card:unknown"}}
		},
		"cycle": func(d *document) {
			d.Artworks["card:rated"] = Definition{Rating: General, Uses: []string{"appearance:derived"}}
		},
		"unknown scene": func(d *document) {
			d.Artworks["card:rated"] = Definition{Rating: General, Scene: "illustration:missing"}
		},
		"wrong scene":   func(d *document) { d.Artworks["card:rated"] = Definition{Rating: General, Scene: "card:safe"} },
		"unknown owner": func(d *document) { d.Assets[0].Owner = "card:unknown" },
		"exact conflict": func(d *document) {
			d.Assets = append(d.Assets, AssetRule{Owner: "appearance:default", Files: []string{"cards/rated.webp"}})
		},
		"prefix conflict": func(d *document) {
			d.Assets = append(d.Assets, AssetRule{Owner: "appearance:default", Prefixes: []string{"motion/rated/"}})
		},
		"unsafe default": func(d *document) { d.Artworks["appearance:default"] = Definition{Rating: R18} },
		"unsafe inherited default": func(d *document) {
			d.Artworks["appearance:default"] = Definition{Rating: General, Uses: []string{"card:rated"}}
		},
		"missing default": func(d *document) { delete(d.Artworks, "cover:permanent") },
		"unsafe path":     func(d *document) { d.Assets[0].Files = []string{"../secret.webp"} },
	}
	for name, change := range cases {
		t.Run(name, func(t *testing.T) {
			doc := fixtureDocument()
			change(&doc)
			data, err := json.Marshal(doc)
			if err != nil {
				t.Fatal(err)
			}
			if _, err = Parse(data); err == nil {
				t.Fatal("accepted invalid catalog")
			}
		})
	}
}
func TestBuiltInCatalogMatchesProduct(t *testing.T) {
	var product struct {
		Cards  []struct{ ID, Theme string }
		Themes []struct{ ID, Source string }
	}
	data, err := os.ReadFile("../gacha/catalog.json")
	if err != nil {
		t.Fatal(err)
	}
	if err = json.Unmarshal(data, &product); err != nil {
		t.Fatal(err)
	}
	var appearances []struct{ ID, Card string }
	data, err = os.ReadFile("../appearance/catalog.json")
	if err != nil {
		t.Fatal(err)
	}
	if err = json.Unmarshal(data, &appearances); err != nil {
		t.Fatal(err)
	}
	expected := []string{"cover:permanent", "cover:entry"}
	for _, card := range product.Cards {
		key := "card:" + card.ID
		expected = append(expected, key)
		if builtIn.definitions[key].Scene != "illustration:"+card.Theme {
			t.Error("scene drift", key)
		}
	}
	for _, theme := range product.Themes {
		expected = append(expected, "illustration:"+theme.ID)
		if theme.Source == "limited" {
			expected = append(expected, "cover:"+theme.ID)
		}
	}
	for _, appearance := range appearances {
		key := "appearance:" + appearance.ID
		expected = append(expected, key)
		if appearance.Card != "" && !slices.Contains(builtIn.definitions[key].Uses, "card:"+appearance.Card) {
			t.Error("missing shared source", key)
		}
	}
	if len(expected) != len(builtIn.definitions) {
		t.Fatal("catalog membership drift", len(expected), len(builtIn.definitions))
	}
	for _, key := range expected {
		if rating, ok := Rating(key); !ok || rating != General && rating != R18 {
			t.Error("missing explicit classification", key)
		}
	}
}

func TestTomorrowArtworkRemainsAvailableWithR18Disabled(t *testing.T) {
	for _, key := range []string{"card:r-chatgpt", "illustration:r", "cover:r", "appearance:tomorrow"} {
		definition, ok := builtIn.definitions[key]
		if !ok || definition.Rating != General || !Allowed(key, true) {
			t.Fatal("theme inherited restricted artwork", key, definition)
		}
		for _, dependency := range definition.Uses {
			if !Allowed(dependency, true) {
				t.Fatal("theme uses restricted artwork", key, dependency)
			}
		}
	}
	for _, key := range []string{"card:r-chatgpt", "illustration:r", "cover:r"} {
		if !MomentAllowed(key, true) {
			t.Fatal("theme moment blocked by artwork policy", key)
		}
	}
	for _, file := range []string{
		"cards/r-chatgpt.webp", "hires/cards/r-chatgpt.webp", "thumbs/r-chatgpt.webp",
		"backgrounds/r.webp", "hires/backgrounds/r.webp", "groups/r.webp", "hires/groups/r.webp",
		"covers/r.webp", "hires/covers/r.webp",
		"appearances/tomorrow-light.webp", "appearances/tomorrow-dark.webp",
		"hires/appearances/tomorrow-light.webp", "hires/appearances/tomorrow-dark.webp",
		"motion/r-chatgpt/hero.webp", "hires/motion/r-chatgpt/hero-dark.webp",
		"motion/r-chatgpt/background-light.webp", "hires/motion/r-chatgpt/background-dark.webp",
		"tomorrow/hero.webp", "hires/tomorrow/hero.webp",
	} {
		if rating, ok := AssetRating(file); !ok || rating != General {
			t.Error("theme asset is not general", file, rating, ok)
		}
	}
}
