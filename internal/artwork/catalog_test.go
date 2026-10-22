package artwork

import (
	"encoding/json"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
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
func TestBuiltInCatalogMatchesProductAndRasterAssets(t *testing.T) {
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
	root := "../../web/public/assets/gacha"
	count := 0
	files := map[string]bool{}
	err = filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		if !slices.Contains([]string{".webp", ".png", ".jpg", ".jpeg", ".avif", ".gif", ".svg", ".bmp"}, strings.ToLower(filepath.Ext(p))) {
			return nil
		}
		relative, err := filepath.Rel(root, p)
		if err != nil {
			return err
		}
		relative = filepath.ToSlash(relative)
		files[relative] = true
		count++
		if _, ok := AssetRating(relative); !ok {
			t.Error("unclassified raster", relative)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if count == 0 {
		t.Fatal("no raster assets inspected")
	}
	t.Logf("classified %d raster assets", count)
	for file := range builtIn.files {
		if !files[file] {
			t.Error("missing registered raster asset", file)
		}
	}
	for _, rule := range builtIn.prefixes {
		found := false
		for file := range files {
			if strings.HasPrefix(file, rule.prefix) {
				found = true
				break
			}
		}
		if !found {
			t.Error("unused asset prefix", rule.prefix)
		}
	}
}
