package gacha

import (
	"context"
	"database/sql"
	"errors"
	"testing"
)

func TestCatalogSyncAddsOnlyMissingTemplatesWithoutSchemaOrPlayerChanges(t *testing.T) {
	f := newFixture(t)
	f.enter(t)
	f.exec(t, "INSERT INTO gacha_owned(user_id,card_id,obtained_at,source) VALUES(?,?,?,'draw')", f.user, "l-chatgpt", f.at.Load())
	f.exec(t, "INSERT INTO gacha_pity(user_id,group_id,sr,ssr,sr_limit,ssr_limit,sr_featured,ssr_featured) VALUES(?,'permanent',3,40,10,60,0,0)", f.user)
	before, err := f.s.State(t.Context(), f.user)
	if err != nil {
		t.Fatal(err)
	}
	config, err := f.s.AdminConfig(t.Context(), f.admin)
	if err != nil {
		t.Fatal(err)
	}
	for _, preset := range config.Presets {
		if preset.ID != "celestial" {
			continue
		}
		preset.Name, preset.Pool.Name = "Edited template", "Edited pool"
		f.save(t, "edit-built-in", AdminInput{Kind: "preset", ExpectedRevision: preset.Revision, Preset: &preset})
	}
	var edited string
	if err := f.db.QueryRow("SELECT config FROM gacha_presets WHERE id='celestial'").Scan(&edited); err != nil {
		t.Fatal(err)
	}
	f.exec(t, "DELETE FROM gacha_presets WHERE id IN ('theatre','tomorrow','sweet-hide-and-seek')")
	version := f.scalar(t, "SELECT version FROM schema_state")
	ddlVersion := f.scalar(t, "PRAGMA schema_version")
	events := f.scalar(t, "SELECT count(*) FROM gacha_config_events")
	reloaded, err := New(f.s.config)
	if err != nil {
		t.Fatal(err)
	}
	if err = reloaded.SyncCatalog(t.Context()); err != nil {
		t.Fatal(err)
	}
	if f.scalar(t, "SELECT count(*) FROM gacha_presets WHERE id='tomorrow'") != 1 {
		t.Fatal("restart did not add the new built-in template")
	}
	if f.scalar(t, "SELECT count(*) FROM gacha_presets WHERE id='sweet-hide-and-seek'") != 1 {
		t.Fatal("default artwork policy blocked the restricted template")
	}
	content := presetCatalog{Version: defaultPresets.Version + 1, Presets: append([]Preset(nil), defaultPresets.Presets...)}
	newPreset := content.Presets[0]
	newPreset.ID, newPreset.Name = "future-template", "Future template"
	content.Presets = append(content.Presets, newPreset)
	for range 2 {
		if err := syncPresets(t.Context(), f.db, content); err != nil {
			t.Fatal(err)
		}
	}
	if f.scalar(t, "SELECT version FROM schema_state") != version || f.scalar(t, "PRAGMA schema_version") != ddlVersion {
		t.Fatal("content update changed schema")
	}
	if f.scalar(t, "SELECT count(*) FROM gacha_presets") != int64(len(content.Presets)) || f.scalar(t, "SELECT count(*) FROM gacha_pools") != 0 {
		t.Fatal("templates missing or live pools created")
	}
	if f.scalar(t, "SELECT count(*) FROM gacha_presets WHERE json_extract(config,'$.published')=1") != 0 || f.scalar(t, "SELECT count(*) FROM gacha_config_events") != events {
		t.Fatal("content update published a pool or rewrote history")
	}
	var afterEdit string
	if err := f.db.QueryRow("SELECT config FROM gacha_presets WHERE id='celestial'").Scan(&afterEdit); err != nil || afterEdit != edited {
		t.Fatal("administrator template overwritten", err)
	}
	after, err := reloaded.State(t.Context(), f.user)
	if err != nil || jsonText(after) != jsonText(before) {
		t.Fatal("content update changed player facts", err)
	}
	canceled, cancel := context.WithCancel(t.Context())
	cancel()
	if err := syncPresets(canceled, f.db, content); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
}

func TestSinglePoolReadUsesSettingsAndPublicationBoundaries(t *testing.T) {
	f := newFixture(t)
	pool := f.pool(t, "Current", DefaultRules(), "h-chatgpt")
	tx, err := f.db.BeginTx(t.Context(), &sql.TxOptions{ReadOnly: true})
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	settings, err := readSettings(t.Context(), tx)
	if err != nil {
		t.Fatal(err)
	}
	got, err := readPool(t.Context(), tx, pool.ID, settings)
	if err != nil || got.ID != pool.ID || got.Revision != pool.Revision || got.Rules != pool.Rules {
		t.Fatal(got, err)
	}
	if !got.Open(*pool.StartsAt) || got.Open(*pool.EndsAt) {
		t.Fatal("opening interval changed")
	}
	got.Paused = true
	if got.Open(*pool.StartsAt) {
		t.Fatal("paused pool opened")
	}
	got.Paused, got.Published = false, false
	if got.Open(*pool.StartsAt) {
		t.Fatal("draft pool opened")
	}
	permanent, err := readPool(t.Context(), tx, "permanent", settings)
	if err != nil || permanent.Rules != settings.rules || permanent.Revision != settings.revision {
		t.Fatal(permanent, err)
	}
	if _, err := readPool(t.Context(), tx, "missing", settings); !errors.Is(err, ErrClosed) {
		t.Fatal(err)
	}
	if len(settings.rates) != 0 {
		t.Fatal("draw admission calculated display probabilities")
	}
	pools, err := readPools(t.Context(), tx, f.at.Load(), false, settings)
	if err != nil || len(pools) != 2 || len(settings.rates) != 1 {
		t.Fatal("identical pool rules recalculated", err)
	}
}
