package gacha

import (
	"errors"
	"slices"
	"testing"

	"github.com/waiting-here/NonbiriAPI/internal/authz"
	"github.com/waiting-here/NonbiriAPI/internal/db"
)

func TestEntryVisibilityIsDefaultOffRevisionedAndReadOnly(t *testing.T) {
	f := newFixture(t)
	entry, err := f.s.Entry(t.Context(), f.user)
	if err != nil || entry.Enabled || entry.Entered {
		t.Fatal(entry, err)
	}
	var profiles int
	if err = f.db.QueryRow("SELECT count(*) FROM gacha_profiles").Scan(&profiles); err != nil || profiles != 0 {
		t.Fatal(profiles, err)
	}
	config, err := f.s.AdminConfig(t.Context(), f.admin)
	if err != nil {
		t.Fatal(err)
	}
	enabled := true
	input := AdminInput{Kind: "settings", ExpectedRevision: config.Revision, Rules: &config.Rules, Shop: &config.Shop, EntryEnabled: &enabled}
	if _, err = f.s.AdminSave(t.Context(), f.user, "visibility-denied-0123456789", input); !errors.Is(err, authz.ErrForbidden) {
		t.Fatal(err)
	}
	saved := f.save(t, "entry-enabled", input)
	if !saved.EntryEnabled {
		t.Fatal(saved)
	}
	if _, err = f.s.AdminSave(t.Context(), f.admin, "visibility-stale-0123456789", input); !errors.Is(err, ErrConflict) {
		t.Fatal(err)
	}
	entry, err = f.s.Entry(t.Context(), f.user)
	if err != nil || !entry.Enabled || entry.Entered {
		t.Fatal(entry, err)
	}
	f.enter(t)
	entry, err = f.s.Entry(t.Context(), f.user)
	if err != nil || !entry.Entered {
		t.Fatal(entry, err)
	}
	// An older settings client must preserve the independent visibility switch.
	saved = f.save(t, "entry-preserved", AdminInput{Kind: "settings", ExpectedRevision: saved.Revision, Rules: &saved.Rules, Shop: &saved.Shop})
	if !saved.EntryEnabled {
		t.Fatal(saved)
	}
}

func TestLimitedThemeVisibilityUsesActualOpenIntervals(t *testing.T) {
	f := newFixture(t)
	has := func(theme string) bool {
		t.Helper()
		state, err := f.s.State(t.Context(), f.user)
		if err != nil {
			t.Fatal(err)
		}
		return slices.Contains(state.VisibleThemes, theme)
	}
	if has("i") || !has("a") || !has("k") {
		t.Fatal("unexpected initial themes")
	}
	id, err := db.GenerateOpaqueID("gp_")
	if err != nil {
		t.Fatal(err)
	}
	start, end := f.at.Load()+100, f.at.Load()+200
	pool := Pool{ID: id, Name: "Future", Kind: "limited", Cover: "i", Rules: DefaultRules(), Featured: []string{"i-chatgpt"}, StartsAt: &start, EndsAt: &end, Published: true}
	saved := f.save(t, "schedule", AdminInput{Kind: "pool", Pool: &pool})
	if has("i") {
		t.Fatal("future theme exposed")
	}
	pool = saved.Pools[1]
	pool.Paused = true
	f.at.Add(10)
	saved = f.save(t, "pause-before-start", AdminInput{Kind: "pool", Pool: &pool, ExpectedRevision: pool.Revision})
	f.at.Add(100)
	if has("i") {
		t.Fatal("never-open paused theme exposed")
	}
	pool = saved.Pools[1]
	pool.Paused = false
	saved = f.save(t, "resume", AdminInput{Kind: "pool", Pool: &pool, ExpectedRevision: pool.Revision})
	if !has("i") {
		t.Fatal("open theme hidden")
	}
	f.at.Add(1)
	pool = saved.Pools[1]
	pool.Paused = true
	f.save(t, "pause-after-start", AdminInput{Kind: "pool", Pool: &pool, ExpectedRevision: pool.Revision})
	f.at.Add(1000)
	if !has("i") {
		t.Fatal("previously opened theme forgotten")
	}
	f.exec(t, "INSERT INTO gacha_owned(user_id,card_id,obtained_at,source) VALUES(?,?,?,'draw')", f.user, "l-chatgpt", f.at.Load())
	if !has("l") {
		t.Fatal("owned card hidden")
	}
}
