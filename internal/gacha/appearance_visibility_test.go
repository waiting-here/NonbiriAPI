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

func TestAppearanceVisibilityFollowsOwnershipAndActualOpening(t *testing.T) {
	f := newFixture(t)
	service, err := appearance.New(appearance.Config{
		Database: f.db, Users: testAuthority{f.admin}, Gate: f.gate, Moments: f.s.MomentOptionsTx, Now: func() time.Time { return time.Unix(f.at.Load(), 0) },
		Unlock: func(ctx context.Context, tx *sql.Tx, user int64, key string) (bool, error) {
			var owned bool
			err := tx.QueryRowContext(ctx, "SELECT EXISTS(SELECT 1 FROM gacha_owned WHERE user_id=? AND card_id=?)", user, strings.TrimPrefix(key, "card:")).Scan(&owned)
			return owned, err
		},
		Visible: func(ctx context.Context, tx *sql.Tx, key string) (bool, error) {
			return f.s.CardWasOfferedTx(ctx, tx, strings.TrimPrefix(key, "card:"))
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	check := func(user int64, visible, unlocked []string) {
		t.Helper()
		got, err := service.State(t.Context(), user)
		if err != nil || !slices.Equal(got.Visible, visible) || !slices.Equal(got.Unlocked, unlocked) {
			t.Fatal(got, err)
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
