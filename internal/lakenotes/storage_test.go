package lakenotes

import (
	"bytes"
	"errors"
	"testing"

	"github.com/waiting-here/NonbiriAPI/internal/lakenotes/rules"
)

func TestProfileStorageSurvivesCompatibleRuleIdentityChange(t *testing.T) {
	f := newFixture(t)
	profile := f.enter(t, f.period(t, "0"))
	const priorRules = "lake-notes-prior-compatible-catalog"
	if _, err := f.database.Exec("UPDATE lake_notes_profiles SET rules_id=? WHERE user_id=?", priorRules, f.user); err != nil {
		t.Fatal(err)
	}
	var before, after []byte
	if err := f.database.QueryRow("SELECT profile FROM lake_notes_profiles WHERE user_id=?", f.user).Scan(&before); err != nil {
		t.Fatal(err)
	}
	current, err := f.service.Profile(f.ctx(f.user), f.user)
	if err != nil || current.RulesID != rules.RulesID || current.Revision != profile.Revision {
		t.Fatal("compatible stored profile rejected", current, err)
	}
	var storedRules string
	if err := f.database.QueryRow("SELECT profile,rules_id FROM lake_notes_profiles WHERE user_id=?", f.user).Scan(&after, &storedRules); err != nil ||
		!bytes.Equal(before, after) || storedRules != priorRules {
		t.Fatal("read rewrote saved state", err)
	}
	result, err := f.service.Action(f.ctx(f.user), f.user, testKey(710), ActionInput{ExpectedProfileRevision: current.Revision, Action: rules.Action{Name: "rest"}})
	if err != nil || result.Value.Profile.Profile.Coins != current.Profile.Coins {
		t.Fatal("normal action changed coins", result, err)
	}
	var version int
	if err := f.database.QueryRow("SELECT rules_id,storage_version FROM lake_notes_profiles WHERE user_id=?", f.user).Scan(&storedRules, &version); err != nil ||
		storedRules != rules.RulesID || version != profileStorageVersion {
		t.Fatal("new write did not stamp current storage", storedRules, version, err)
	}
	if _, err := f.database.Exec("UPDATE lake_notes_profiles SET storage_version=2 WHERE user_id=?", f.user); err != nil {
		t.Fatal(err)
	}
	if _, err := f.service.Profile(f.ctx(f.user), f.user); !errors.Is(err, ErrInvariant) {
		t.Fatal("unknown profile format accepted", err)
	}
}

func TestCastStorageRequiresKnownFormatAndExactRules(t *testing.T) {
	f := newFixture(t)
	profile := f.enter(t, f.period(t, "0"))
	started, err := f.service.Start(f.ctx(f.user), f.user, testKey(720), StartInput{profile.Revision})
	if err != nil {
		t.Fatal(err)
	}
	id := started.Value.Cast.ID
	draws := f.random.draws.Load()
	var before, after []byte
	if err := f.database.QueryRow("SELECT snapshot FROM lake_notes_casts WHERE id=?", id).Scan(&before); err != nil {
		t.Fatal(err)
	}
	for _, saved := range []struct {
		version int
		rulesID string
	}{{2, rules.RulesID}, {1, "unknown-rules"}} {
		if _, err := f.database.Exec("UPDATE lake_notes_casts SET storage_version=?,rules_id=? WHERE id=?", saved.version, saved.rulesID, id); err != nil {
			t.Fatal(err)
		}
		if _, err := f.service.Cast(f.ctx(f.user), f.user, id); !errors.Is(err, ErrInvariant) {
			t.Fatal("unrecognized checkpoint accepted", saved, err)
		}
		if err := f.database.QueryRow("SELECT snapshot FROM lake_notes_casts WHERE id=?", id).Scan(&after); err != nil ||
			!bytes.Equal(before, after) || f.random.draws.Load() != draws {
			t.Fatal("checkpoint replaced or rerolled", err)
		}
	}
	if _, err := f.database.Exec("UPDATE lake_notes_casts SET storage_version=1,rules_id=? WHERE id=?", rules.RulesID, id); err != nil {
		t.Fatal(err)
	}
	current, err := f.service.Cast(f.ctx(f.user), f.user, id)
	if err != nil || current.Cast.AckTick != started.Value.Cast.AckTick || current.Cast.State.Plan != started.Value.Cast.State.Plan {
		t.Fatal("saved checkpoint changed", current, err)
	}
}
