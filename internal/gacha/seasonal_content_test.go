package gacha

import (
	"slices"
	"testing"
)

func TestSeasonalTemplatesUseStandardRulesAndEqualSSRChoices(t *testing.T) {
	for _, tc := range []struct {
		id, cover string
		featured  []string
	}{
		{"sweet-hide-and-seek", "s", []string{"s-chatgpt", "s-claude"}},
		{"christmas-date", "t", []string{"t-chatgpt", "t-claude", "t-glm", "t-deepseek", "t-gemini"}},
		{"encore", "u", []string{"u-gemini", "u-glm", "u-deepseek"}},
	} {
		t.Run(tc.id, func(t *testing.T) {
			assertSeasonalTemplate(t, tc.id, tc.cover, tc.featured)
		})
	}
}

func assertSeasonalTemplate(t *testing.T, presetID, cover string, featured []string) {
	t.Helper()
	var pool Pool
	for _, preset := range defaultPresets.Presets {
		if preset.ID == presetID {
			pool = preset.Pool
			break
		}
	}
	if pool.Kind != "limited" || pool.Cover != cover || !slices.Equal(pool.Featured, featured) {
		t.Fatal("unexpected seasonal template", pool)
	}
	if pool.Published || pool.Paused || pool.StartsAt != nil || pool.EndsAt != nil || pool.Open(1800000000) {
		t.Fatal("template opened without an activity schedule", pool)
	}
	if pool.Rules != DefaultRules() {
		t.Fatal("template changed the standard limited rules", pool.Rules)
	}
	validated, err := validatePool(pool)
	if err != nil || validated.Group != DefaultRules().Group() {
		t.Fatal("template did not use the shared pity group", validated.Group, err)
	}
	for choice, id := range pool.Featured {
		card := cardsByID[id]
		if card.Theme != cover || card.Rarity != SSR || card.Appearance != "" {
			t.Fatal("unexpected seasonal card", card)
		}
		pity := freshPity(pool.Rules)
		pity.SSR = pity.SSRLimit - 1
		pity.SSRFeatured = true
		calls := 0
		item, next, err := drawOne(pool.Rules, pity, pool.Featured, func(n int) (int, error) {
			calls++
			if n != len(featured) {
				t.Fatalf("featured selection has %d choices, want %d", n, len(featured))
			}
			return choice, nil
		}, false)
		if err != nil || calls != 1 || item.CardID != id || next.SSRFeatured || next.SSRMisses != 0 {
			t.Fatal("featured guarantee did not select each card with equal weight", item, next, err)
		}
	}
}
