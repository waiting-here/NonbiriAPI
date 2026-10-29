package gacha

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/waiting-here/NonbiriAPI/internal/appearance"
	"github.com/waiting-here/NonbiriAPI/internal/authz"
	"github.com/waiting-here/NonbiriAPI/internal/db"
)

func newAppearanceService(t *testing.T, f *fixture, visibilityRead func()) *appearance.Service {
	t.Helper()
	service, err := appearance.New(appearance.Config{
		Database: f.db, ReadDatabase: f.s.config.ReadDatabase, Users: testAuthority{f.admin}, Gate: f.gate, Moments: f.s.MomentOptionsTx, Now: func() time.Time { return time.Unix(f.at.Load(), 0) },
		Unlock: func(ctx context.Context, tx *sql.Tx, user int64, key string) (bool, error) {
			var owned bool
			err := tx.QueryRowContext(ctx, "SELECT EXISTS(SELECT 1 FROM gacha_owned WHERE user_id=? AND card_id=?)", user, strings.TrimPrefix(key, "card:")).Scan(&owned)
			return owned, err
		},
		Visible: func(ctx context.Context, tx *sql.Tx, keys []string) (map[string]bool, error) {
			if visibilityRead != nil {
				visibilityRead()
			}
			ids := make([]string, len(keys))
			for i, key := range keys {
				ids[i] = strings.TrimPrefix(key, "card:")
			}
			offered, err := f.s.CardsWereOfferedTx(ctx, tx, ids)
			out := make(map[string]bool, len(keys))
			for i, key := range keys {
				out[key] = offered[ids[i]]
			}
			return out, err
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	return service
}

func TestAppearanceVisibilityFollowsOwnershipAndActualOpening(t *testing.T) {
	f := newFixture(t)
	visibilityReads := 0
	service := newAppearanceService(t, f, func() { visibilityReads++ })
	check := func(user int64, visible, unlocked []string) {
		t.Helper()
		before := visibilityReads
		got, err := service.State(t.Context(), user)
		if err != nil || !slices.Equal(got.Visible, visible) || !slices.Equal(got.Unlocked, unlocked) {
			t.Fatal(got, err)
		}
		if visibilityReads-before > 1 {
			t.Fatal("appearance repeated eligibility reads")
		}
	}
	base := []string{"default", "heart-moment"}
	known := []string{"default", "heart-moment", "celestial"}
	check(f.user, base, base)
	id, err := db.GenerateOpaqueID("gp_")
	if err != nil {
		t.Fatal(err)
	}
	start, end := f.at.Load()+100, f.at.Load()+200
	pool := Pool{ID: id, Name: "Scheduled", Kind: "limited", Cover: "l", Rules: spRules(), Featured: []string{"l-chatgpt"}, StartsAt: &start, EndsAt: &end}
	saved := f.save(t, "draft-appearance", AdminInput{Kind: "pool", Pool: &pool})
	check(f.user, base, base)
	pool = saved.Pools[1]
	pool.Published = true
	saved = f.save(t, "schedule-appearance", AdminInput{Kind: "pool", Pool: &pool, ExpectedRevision: pool.Revision})
	check(f.user, base, base)
	f.at.Add(10)
	pool = saved.Pools[1]
	pool.Paused = true
	saved = f.save(t, "pause-appearance", AdminInput{Kind: "pool", Pool: &pool, ExpectedRevision: pool.Revision})
	f.at.Add(100)
	check(f.user, base, base)
	pool = saved.Pools[1]
	pool.Paused = false
	f.save(t, "open-appearance", AdminInput{Kind: "pool", Pool: &pool, ExpectedRevision: pool.Revision})
	check(f.user, known, base)
	if _, err = service.Select(t.Context(), f.user, "celestial", ""); !errors.Is(err, authz.ErrForbidden) {
		t.Fatal(err)
	}
	f.at.Add(1000)
	check(f.user, known, base)
	f.exec(t, "INSERT INTO gacha_owned(user_id,card_id,obtained_at,source) VALUES(?,?,?,'shop')", f.user, "m-deepseek", f.at.Load())
	check(f.user, []string{"default", "heart-moment", "celestial", "wandering"}, []string{"default", "heart-moment", "wandering"})
	check(f.other, known, base)
	selected, err := service.Select(t.Context(), f.user, "wandering", "")
	if err != nil || selected.Selected != "wandering" || len(selected.Visible) != 4 {
		t.Fatal(selected, err)
	}
	tx, err := f.db.BeginTx(t.Context(), &sql.TxOptions{ReadOnly: true})
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	exported, err := service.ExportUserTx(t.Context(), tx, f.user)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := json.Marshal(exported)
	if err != nil || strings.Contains(string(raw), "visible") {
		t.Fatal(string(raw), err)
	}
	var profiles int
	if err := tx.QueryRowContext(t.Context(), "SELECT count(*) FROM gacha_profiles").Scan(&profiles); err != nil || profiles != 0 {
		t.Fatal("appearance read granted activity entry", profiles, err)
	}
}

func TestTomorrowAcquisitionUnlocksAppearanceWithoutSelectingIt(t *testing.T) {
	f := newFixture(t)
	service := newAppearanceService(t, f, nil)
	state, err := service.State(t.Context(), f.user)
	if err != nil || slices.Contains(state.Visible, "tomorrow") || slices.Contains(state.Unlocked, "tomorrow") {
		t.Fatal("unpublished appearance became visible", state, err)
	}
	if _, err = service.Select(t.Context(), f.user, "tomorrow", ""); !errors.Is(err, authz.ErrForbidden) {
		t.Fatal(err)
	}
	f.enter(t)
	var pool Pool
	for _, preset := range defaultPresets.Presets {
		if preset.ID == "tomorrow" {
			pool = preset.Pool
		}
	}
	pool.ID, err = db.GenerateOpaqueID("gp_")
	if err != nil {
		t.Fatal(err)
	}
	saved := f.save(t, "draft-tomorrow", AdminInput{Kind: "pool", Pool: &pool})
	for _, stored := range saved.Pools {
		if stored.ID == pool.ID {
			pool = stored
		}
	}
	if _, err = f.act(f.user, "unpublished-tomorrow", Action{Action: "draw", PoolID: pool.ID, Quantity: 1}); !errors.Is(err, ErrClosed) {
		t.Fatal("an unpublished pool accepted a draw", err)
	}
	start, end := f.at.Load(), f.at.Load()+86400
	pool.StartsAt, pool.EndsAt, pool.Published = &start, &end, true
	f.save(t, "open-tomorrow", AdminInput{Kind: "pool", Pool: &pool, ExpectedRevision: pool.Revision})
	f.exec(t, "UPDATE gacha_profiles SET limited=1 WHERE user_id=?", f.user)
	f.exec(t, "INSERT INTO gacha_pity(user_id,group_id,sr,ssr,sr_limit,ssr_limit,sr_featured,ssr_featured,ssr_misses) VALUES(?,?,0,59,10,60,0,1,2)", f.user, pool.Rules.Group())
	f.s.random = func(n int) (int, error) { return n - 1, nil }
	out, err := f.act(f.user, "first-tomorrow", Action{Action: "draw", PoolID: pool.ID, Quantity: 1})
	if err != nil || out.Draw == nil || len(out.Draw.Items) != 1 || out.Draw.Items[0].CardID != "r-chatgpt" || out.Draw.Items[0].Duplicate || out.Snapshot.Owned["r-chatgpt"].Source != "draw" {
		t.Fatal(out, err)
	}
	state, err = service.State(t.Context(), f.user)
	if err != nil || state.Selected != "default" || !slices.Contains(state.Unlocked, "tomorrow") || !slices.Contains(state.Visible, "tomorrow") ||
		!slices.Contains(state.Moments, "card:r-chatgpt") || !slices.Contains(state.Moments, "illustration:r") {
		t.Fatal("first acquisition did not unlock the complete single-card theme", state, err)
	}
	if f.scalar(t, "SELECT count(*) FROM user_appearance WHERE user_id=?", f.user) != 0 {
		t.Fatal("acquisition changed the saved appearance")
	}
	other, err := service.State(t.Context(), f.other)
	if err != nil || slices.Contains(other.Unlocked, "tomorrow") {
		t.Fatal("another account inherited the unlock", other, err)
	}
	selected, err := service.Select(t.Context(), f.user, "tomorrow", "")
	if err != nil || selected.Selected != "tomorrow" {
		t.Fatal(selected, err)
	}
	restored, err := service.Select(t.Context(), f.user, "default", "")
	if err != nil || restored.Selected != "default" || !slices.Contains(restored.Unlocked, "tomorrow") {
		t.Fatal(restored, err)
	}
}
