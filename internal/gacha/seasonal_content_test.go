package gacha

import (
	"slices"
	"testing"
)

func TestSweetHideAndSeekUsesStandardRulesAndEqualSSRChoices(t *testing.T) {
	var pool Pool
	for _, preset := range defaultPresets.Presets {
		if preset.ID == "sweet-hide-and-seek" {
			pool = preset.Pool
			break
		}
	}
	if pool.Cover != "s" || !slices.Equal(pool.Featured, []string{"s-chatgpt", "s-claude"}) {
		t.Fatal("missing two-card limited template", pool)
	}
	if pool.Published || pool.StartsAt != nil || pool.EndsAt != nil || pool.Open(1800000000) {
		t.Fatal("template opened without an activity schedule", pool)
	}
	if pool.Rules != DefaultRules() || pool.Rules.Group() != DefaultRules().Group() {
		t.Fatal("template changed the standard limited rules or shared pity group", pool.Rules)
	}
	if _, err := validatePool(pool); err != nil {
		t.Fatal(err)
	}
	for choice, id := range pool.Featured {
		pity := freshPity(pool.Rules)
		pity.SSR = pity.SSRLimit - 1
		pity.SSRFeatured = true
		calls := 0
		item, next, err := drawOne(pool.Rules, pity, pool.Featured, func(n int) (int, error) {
			calls++
			if n != 2 {
				t.Fatalf("featured selection uses %d choices instead of two equal choices", n)
			}
			return choice, nil
		}, false)
		if err != nil || calls != 1 || item.CardID != id || next.SSRFeatured || next.SSRMisses != 0 {
			t.Fatal("featured guarantee did not select the two cards equally", item, next, err)
		}
	}
}
