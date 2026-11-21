package attendance

import (
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/waiting-here/NonbiriAPI/internal/db"
	"github.com/waiting-here/NonbiriAPI/internal/dbtest"
	"github.com/waiting-here/NonbiriAPI/internal/secret"
	upgrade "github.com/waiting-here/NonbiriAPI/internal/upgrade/v1"
)

func TestAttendanceCoverValidation(t *testing.T) {
	for _, c := range []Cover{
		{Kind: "pool", Value: "permanent"}, {Kind: "collection", Value: "a"},
		{Kind: "external", Value: "https://images.example:8443/poster.webp?v=2#image"},
	} {
		got, err := normalizeCover(&c)
		if err != nil || got.Fit != "cover" || got.Value != c.Value {
			t.Fatal(c, got, err)
		}
	}
	for _, c := range []Cover{
		{Kind: "pool", Value: "missing"}, {Kind: "collection", Value: "permanent"}, {Kind: "file", Value: "a"},
		{Kind: "pool", Value: "permanent", Fit: "fill"}, {Kind: "external", Value: "http://images.example/a"},
		{Kind: "external", Value: "//images.example/a"}, {Kind: "external", Value: "https://name@images.example/a"},
		{Kind: "external", Value: "https://:443/a"}, {Kind: "external", Value: "https://a.example:99999/b"},
		{Kind: "external", Value: "https://images.example/" + strings.Repeat("a", 2048)},
	} {
		if _, err := normalizeCover(&c); !errors.Is(err, ErrInvalid) {
			t.Fatalf("accepted invalid cover %+v", c)
		}
	}
	out, err := normalizeCover(&Cover{})
	if err != nil || out != nil {
		t.Fatal(out, err)
	}
}

func TestAttendancePresentationAfterSchema55Upgrade(t *testing.T) {
	f := setup(t)
	ctx := t.Context()
	original := input("cumulative")
	createKey, signKey := f.key(), f.key()
	raw, err := json.Marshal(original)
	if err != nil || strings.Contains(string(raw), "featured") || strings.Contains(string(raw), "cover") {
		t.Fatalf("legacy request changed: %s", raw)
	}
	p, err := f.s.Create(ctx, f.admin, createKey, original)
	if err != nil {
		t.Fatal(err)
	}
	f.now.Store(p.startsAt)
	sign := SignInInput{SiteDate: p.StartDate}
	first, err := f.s.SignIn(ctx, f.user, p.ID, signKey, sign)
	if err != nil || len(first.Awards) != 1 {
		t.Fatal(first, err)
	}
	vault := f.s.config.Keys.(*secret.Vault)
	var path string
	if err = f.store.DB().QueryRow("SELECT file FROM pragma_database_list WHERE name='main'").Scan(&path); err != nil {
		t.Fatal(err)
	}
	dbtest.RestoreSchema55Fixture(t, f.store.DB())
	hash, err := db.SchemaManifestHash(ctx, f.store.DB())
	if err != nil || hash != db.Schema55ManifestHash {
		t.Fatal("source fixture identity", hash, err)
	}
	if err = f.store.Close(); err != nil {
		t.Fatal(err)
	}
	if store, err := db.Open(path, vault); err == nil {
		store.Close()
		t.Fatal("service opened an old database")
	}
	checked, err := upgrade.Run(ctx, path, vault, upgrade.Options{})
	if err != nil || checked.SourceSchemaVersion != 55 || checked.Status != "ready" {
		t.Fatal(checked, err)
	}
	converted, err := upgrade.Run(ctx, path, vault, upgrade.Options{Apply: true})
	if err != nil || converted.SourceSchemaVersion != 55 || converted.TargetSchemaVersion != 56 || converted.Status != "converted" {
		t.Fatal(converted, err)
	}
	f.store, err = db.Open(path, vault)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { f.store.Close() })
	f.s.config.Database, f.s.config.ReadDatabase = f.store.DB(), f.store.ReadDB()
	restored, err := f.s.Create(ctx, f.admin, createKey, original)
	if err != nil || restored.ID != p.ID || restored.Cover != nil || restored.Tiers[0].Featured {
		t.Fatal(restored, err)
	}

	edit := input("cumulative")
	edit.Cover = &Cover{Kind: "pool", Value: "permanent", Fit: "contain"}
	edit.Tiers[0].Featured = true
	edit.Tiers[0], edit.Tiers[1] = edit.Tiers[1], edit.Tiers[0]
	changed, err := f.s.Update(ctx, f.admin, p.ID, f.key(), Edit{Input: edit, ExpectedRevision: p.Revision})
	if err != nil || changed.ConfigEditable || changed.Tiers[0].Days != 1 || !changed.Tiers[0].Featured || changed.Cover == nil || *changed.Cover != *edit.Cover {
		t.Fatal(changed, err)
	}
	edit.Tiers[0].Rewards[0].Quantity = "99"
	if _, err := f.s.Update(ctx, f.admin, p.ID, f.key(), Edit{Input: edit, ExpectedRevision: changed.Revision}); !errors.Is(err, ErrConflict) {
		t.Fatal("modified active rewards", err)
	}
	replay, err := f.s.SignIn(ctx, f.user, p.ID, signKey, sign)
	if err != nil || !reflect.DeepEqual(replay.SignInReceipt, first.SignInReceipt) || !replay.Period.Tiers[0].Awarded || !replay.Period.Tiers[0].Featured || !replay.Period.SignedToday || f.balance(t, "general") != "2000" {
		t.Fatal(replay, err)
	}
}

func TestAttendanceCoverRespectsArtworkSwitch(t *testing.T) {
	f := setup(t)
	in := input("consecutive")
	in.Cover = &Cover{Kind: "collection", Value: "s"}
	setPolicy := func(value string) {
		f.exec(t, "UPDATE site_config SET value=? WHERE key='artwork_r18_disabled'", value)
	}
	setPolicy("1")
	if _, err := f.s.Create(t.Context(), f.admin, f.key(), in); !errors.Is(err, ErrInvalid) {
		t.Fatal("new selection should be disabled", err)
	}
	setPolicy("0")
	p := f.create(t, in)
	setPolicy("1")
	in.Description = "Edited without replacing the illustration"
	edited, err := f.s.Update(t.Context(), f.admin, p.ID, f.key(), Edit{Input: in, ExpectedRevision: p.Revision})
	if err != nil || edited.Cover == nil || edited.Cover.Value != "s" {
		t.Fatal("existing configuration was lost", edited, err)
	}
	userCover := func() *Cover {
		page, err := f.s.List(t.Context(), f.user, false, 1, 10)
		if err != nil || len(page.Items) != 1 {
			t.Fatal(page, err)
		}
		return page.Items[0].Cover
	}
	if userCover() != nil {
		t.Fatal("disabled art appeared in the user view")
	}
	setPolicy("0")
	if c := userCover(); c == nil || c.Value != "s" {
		t.Fatal("retained cover did not return", c)
	}
}
