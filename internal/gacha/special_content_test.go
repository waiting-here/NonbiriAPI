package gacha

import (
	"database/sql"
	"slices"
	"testing"
)

func TestSingleSpecialTemplatesPreserveEconomyAndFeaturedProgress(t *testing.T) {
	want := map[string]string{"mirrorsea": "n-gemini", "amber": "o-claude", "elopement": "p-claude", "heart-thief": "q-glm", "tomorrow": "r-chatgpt"}
	for _, preset := range defaultPresets.Presets {
		id, ok := want[preset.ID]
		if !ok {
			continue
		}
		delete(want, preset.ID)
		if preset.Pool.Published || preset.Pool.StartsAt != nil || preset.Pool.EndsAt != nil || preset.Pool.Open(1800000000) {
			t.Fatal("special template opened without an activity schedule", preset)
		}
		if !slices.Equal(preset.Pool.Featured, []string{id}) || preset.Pool.Rules != spRules() || preset.Pool.Rules.Group() != spRules().Group() {
			t.Fatal("special template changed the economy or contains another featured card", preset)
		}
		p := freshPity(preset.Pool.Rules)
		p.SRFeatured = true
		p.SR = p.SRLimit - 1
		item, next, err := drawOne(preset.Pool.Rules, p, preset.Pool.Featured, func(n int) (int, error) { return n - 1, nil }, false)
		if err != nil || cardsByID[item.CardID].Rarity != SR || !next.SRFeatured {
			t.Fatal("a pool without featured SR erased existing featured progress", item, next, err)
		}
	}
	if len(want) != 0 {
		t.Fatal("missing templates", want)
	}
}

func TestNewSpecialOwnershipUnlocksCardAndThemeMomentsWithoutOpeningPool(t *testing.T) {
	f := newFixture(t)
	for _, id := range []string{"n-gemini", "o-claude", "p-claude", "q-glm", "r-chatgpt"} {
		f.exec(t, "INSERT INTO gacha_owned(user_id,card_id,obtained_at,source) VALUES(?,?,?,'shop')", f.user, id, f.at.Load())
	}
	tx, err := f.db.BeginTx(t.Context(), &sql.TxOptions{ReadOnly: true})
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	options, err := f.s.MomentOptionsTx(t.Context(), tx, f.user)
	if err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"n-gemini", "o-claude", "p-claude", "q-glm", "r-chatgpt"} {
		card := cardsByID[id]
		if card.Appearance == "" || !slices.Contains(options, "card:"+id) || !slices.Contains(options, "illustration:"+card.Theme) || slices.Contains(options, "cover:"+card.Theme) {
			t.Fatal("ownership and pool publication boundaries changed", id, options)
		}
	}
	other, err := f.s.MomentOptionsTx(t.Context(), tx, f.other)
	if err != nil || len(other) != 1 {
		t.Fatal(other, err)
	}
}
