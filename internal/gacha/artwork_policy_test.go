package gacha

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"os"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/waiting-here/NonbiriAPI/internal/appearance"
	"github.com/waiting-here/NonbiriAPI/internal/artwork"
	"github.com/waiting-here/NonbiriAPI/internal/authz"
	"github.com/waiting-here/NonbiriAPI/internal/db"
)

func syntheticArtwork(t *testing.T, rated ...string) *artwork.Catalog {
	t.Helper()
	data, err := os.ReadFile("../artwork/catalog.json")
	if err != nil {
		t.Fatal(err)
	}
	var doc struct {
		Version  int                           `json:"version"`
		Artworks map[string]artwork.Definition `json:"artworks"`
		Assets   []artwork.AssetRule           `json:"assets"`
	}
	if err = json.Unmarshal(data, &doc); err != nil {
		t.Fatal(err)
	}
	for _, key := range rated {
		d, ok := doc.Artworks[key]
		if !ok {
			t.Fatal(key)
		}
		d.Rating = artwork.R18
		doc.Artworks[key] = d
	}
	data, err = json.Marshal(doc)
	if err != nil {
		t.Fatal(err)
	}
	catalog, err := artwork.Parse(data)
	if err != nil {
		t.Fatal(err)
	}
	return catalog
}
func setArtworkPolicy(t *testing.T, f *fixture, disabled bool) {
	t.Helper()
	value := "0"
	if disabled {
		value = "1"
	}
	f.exec(t, "INSERT INTO site_config(key,value,updated_at) VALUES('artwork_r18_disabled',?,0) ON CONFLICT(key) DO UPDATE SET value=excluded.value", value)
}
func TestArtworkPolicyPreservesPreferencesAndRejectsNewUnavailableSelections(t *testing.T) {
	f := newFixture(t)
	classified := syntheticArtwork(t, "card:l-chatgpt", "illustration:a")
	service, err := appearance.New(appearance.Config{
		Database: f.db, Users: testAuthority{f.admin}, Gate: f.gate, Artwork: classified, Moments: f.s.MomentOptionsTx,
		Now: func() time.Time { return time.Unix(f.at.Load(), 0) },
		Unlock: func(ctx context.Context, tx *sql.Tx, user int64, key string) (bool, error) {
			var owned bool
			err := tx.QueryRowContext(ctx, "SELECT EXISTS(SELECT 1 FROM gacha_owned WHERE user_id=? AND card_id=?)", user, strings.TrimPrefix(key, "card:")).Scan(&owned)
			return owned, err
		},
		Visible: func(context.Context, *sql.Tx, []string) (map[string]bool, error) { return map[string]bool{}, nil },
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"l-chatgpt", "a-chatgpt"} {
		f.exec(t, "INSERT INTO gacha_owned(user_id,card_id,obtained_at,source) VALUES(?,?,?,'shop')", f.user, id, f.at.Load())
	}
	f.exec(t, "DELETE FROM site_config WHERE key='artwork_r18_disabled'")
	if _, err = service.Select(t.Context(), f.user, "celestial", ""); !errors.Is(err, authz.ErrForbidden) {
		t.Fatal("missing policy should disable derived appearance", err)
	}
	for _, moment := range []string{"card:l-chatgpt", "card:a-chatgpt"} {
		if _, err = service.Select(t.Context(), f.user, "heart-moment", moment); !errors.Is(err, authz.ErrForbidden) {
			t.Fatal("unavailable card or scenery selected", moment, err)
		}
	}
	setArtworkPolicy(t, f, false)
	selected, err := service.Select(t.Context(), f.user, "celestial", "")
	if err != nil {
		t.Fatal(err)
	}
	setArtworkPolicy(t, f, true)
	state, err := service.State(t.Context(), f.user)
	if err != nil || !reflect.DeepEqual(state, selected) {
		t.Fatal("read changed saved appearance", state, selected, err)
	}
	export := func() appearance.State {
		t.Helper()
		tx, err := f.db.BeginTx(t.Context(), &sql.TxOptions{ReadOnly: true})
		if err != nil {
			t.Fatal(err)
		}
		defer tx.Rollback()
		out, err := service.ExportUserTx(t.Context(), tx, f.user)
		if err != nil {
			t.Fatal(err)
		}
		return out
	}
	if got := export(); got.Selected != "celestial" || !slices.Contains(got.Unlocked, "celestial") {
		t.Fatal("export lost preference or ownership", got)
	}
	if !slices.Contains(state.Moments, "card:l-chatgpt") || !slices.Contains(state.Unlocked, "celestial") {
		t.Fatal("policy changed entitlement", state)
	}
	setArtworkPolicy(t, f, false)
	if got, err := service.State(t.Context(), f.user); err != nil || got.Selected != "celestial" {
		t.Fatal("preference did not restore", got, err)
	}
	selected, err = service.Select(t.Context(), f.user, "heart-moment", "card:l-chatgpt")
	if err != nil {
		t.Fatal(err)
	}
	setArtworkPolicy(t, f, true)
	if got := export(); got.Selected != "heart-moment" || got.Moment != "card:l-chatgpt" {
		t.Fatal("export lost saved moment", got)
	}
	if got, err := service.State(t.Context(), f.user); err != nil || !reflect.DeepEqual(got, selected) {
		t.Fatal("read changed saved moment", got, err)
	}
	if got, err := service.Select(t.Context(), f.user, "default", ""); err != nil || got.Selected != "default" || got.Moment != "card:l-chatgpt" {
		t.Fatal("unrelated old moment blocked safe appearance", got, err)
	}
	if _, err = service.Select(t.Context(), f.user, "heart-moment", ""); !errors.Is(err, authz.ErrForbidden) {
		t.Fatal("implicit unavailable saved moment accepted", err)
	}
	setArtworkPolicy(t, f, false)
	if got, err := service.Select(t.Context(), f.user, "heart-moment", ""); err != nil || got.Moment != "card:l-chatgpt" {
		t.Fatal("saved moment did not restore", got, err)
	}
}

func TestArtworkPolicyOnlyRejectsNewPoolAndPresetCovers(t *testing.T) {
	f := newFixture(t)
	f.s.config.Artwork = syntheticArtwork(t, "cover:l")
	f.exec(t, "DELETE FROM site_config WHERE key='artwork_r18_disabled'")
	if err := f.s.SyncCatalog(t.Context()); err != nil {
		t.Fatal("policy blocked catalog sync", err)
	}
	id, err := db.GenerateOpaqueID("gp_")
	if err != nil {
		t.Fatal(err)
	}
	pool := Pool{ID: id, Name: "Rated cover", Kind: "limited", Cover: "l", Rules: spRules(), Featured: []string{"l-chatgpt"}}
	save := func(key string, input AdminInput) (Configuration, error) {
		return f.s.AdminSave(t.Context(), f.admin, "artwork-"+key+"-0123456789abcdef", input)
	}
	before := f.scalar(t, "SELECT count(*) FROM gacha_config_events")
	if _, err = save("new-denied", AdminInput{Kind: "pool", Pool: &pool}); !errors.Is(err, authz.ErrForbidden) {
		t.Fatal("new rated pool accepted", err)
	}
	if f.scalar(t, "SELECT count(*) FROM gacha_pools") != 0 || f.scalar(t, "SELECT count(*) FROM gacha_config_events") != before {
		t.Fatal("rejected selection persisted")
	}
	setArtworkPolicy(t, f, false)
	config, err := save("new-allowed", AdminInput{Kind: "pool", Pool: &pool})
	if err != nil {
		t.Fatal(err)
	}
	pool = config.Pools[1]
	setArtworkPolicy(t, f, true)
	pool.Name = "Renamed"
	pool.Paused = true
	config, err = save("retained-cover", AdminInput{Kind: "pool", Pool: &pool, ExpectedRevision: pool.Revision})
	if err != nil {
		t.Fatal("retained cover blocked edit", err)
	}
	pool = config.Pools[1]
	if pool.Cover != "l" || !slices.Equal(pool.Featured, []string{"l-chatgpt"}) {
		t.Fatal("policy changed gameplay", pool)
	}
	pool.Cover = "permanent"
	config, err = save("safe-cover", AdminInput{Kind: "pool", Pool: &pool, ExpectedRevision: pool.Revision})
	if err != nil {
		t.Fatal(err)
	}
	pool = config.Pools[1]
	pool.Cover = "l"
	if _, err = save("changed-denied", AdminInput{Kind: "pool", Pool: &pool, ExpectedRevision: pool.Revision}); !errors.Is(err, authz.ErrForbidden) {
		t.Fatal("new rated cover accepted on existing pool", err)
	}
	var preset Preset
	for _, p := range config.Presets {
		if p.ID == "celestial" {
			preset = p
		}
	}
	if preset.ID == "" {
		t.Fatal("missing built-in preset")
	}
	preset.Name = "Renamed template"
	if _, err = save("retained-preset", AdminInput{Kind: "preset", Preset: &preset, ExpectedRevision: preset.Revision}); err != nil {
		t.Fatal("retained preset cover blocked edit", err)
	}
	preset.ID, err = db.GenerateOpaqueID("gt_")
	if err != nil {
		t.Fatal(err)
	}
	preset.Revision = 0
	if _, err = save("new-preset-denied", AdminInput{Kind: "preset", Preset: &preset}); !errors.Is(err, authz.ErrForbidden) {
		t.Fatal("new rated preset accepted", err)
	}
	preset.Pool.Cover = "permanent"
	if _, err = save("new-preset-safe", AdminInput{Kind: "preset", Preset: &preset}); err != nil {
		t.Fatal(err)
	}
}
