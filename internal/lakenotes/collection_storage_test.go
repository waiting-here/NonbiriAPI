package lakenotes

import (
	"encoding/json"
	"reflect"
	"testing"

	"github.com/waiting-here/NonbiriAPI/internal/lakenotes/rules"
)

func TestCollectionProtocolPreservesPriorActiveCast(t *testing.T) {
	f := newFixture(t)
	f.enable(t)
	profile := f.profile(t)
	started, err := f.service.Start(f.ctx(f.user), f.user, testKey(940), StartInput{profile.Revision})
	if err != nil {
		t.Fatal(err)
	}
	id := started.Value.Cast.ID
	paused, err := f.service.Pause(f.ctx(f.user), f.user, id, testKey(941), control(started.Value.Cast))
	if err != nil {
		t.Fatal(err)
	}
	state := paused.Value.Cast.State
	raw, err := rules.EncodeCast(state)
	if err != nil {
		t.Fatal(err)
	}
	var fields map[string]json.RawMessage
	if err = json.Unmarshal(raw, &fields); err != nil {
		t.Fatal(err)
	}
	fields["rules_id"], _ = json.Marshal(collectionPriorRulesID)
	raw, err = json.Marshal(fields)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = f.database.Exec("UPDATE lake_notes_casts SET rules_id=?,snapshot=? WHERE id=?", collectionPriorRulesID, raw, id); err != nil {
		t.Fatal(err)
	}
	if _, err = f.database.Exec("UPDATE lake_notes_profiles SET rules_id=? WHERE user_id=?", collectionPriorRulesID, f.user); err != nil {
		t.Fatal(err)
	}
	draws := f.random.draws.Load()
	current, err := f.service.Cast(f.ctx(f.user), f.user, id)
	if err != nil || !reflect.DeepEqual(current.Cast.State, state) || !reflect.DeepEqual(current.Profile.Profile, paused.Value.Profile.Profile) {
		t.Fatalf("prior cast or profile changed: %v", err)
	}
	resumed, err := f.service.Resume(f.ctx(f.user), f.user, id, testKey(942), control(current.Cast))
	if err != nil {
		t.Fatal(err)
	}
	advanced, err := f.service.Checkpoint(f.ctx(f.user), f.user, id, testKey(943), checkpoint(resumed.Value.Cast, 1))
	if err != nil || advanced.Value.Cast.AckTick != state.Tick+1 || f.random.draws.Load() != draws {
		t.Fatalf("prior cast resume: %v", err)
	}
	var identity string
	if err = f.database.QueryRow("SELECT rules_id FROM lake_notes_casts WHERE id=?", id).Scan(&identity); err != nil || identity != rules.RulesID {
		t.Fatalf("converted checkpoint: %s %v", identity, err)
	}
}
