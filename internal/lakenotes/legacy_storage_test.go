package lakenotes

import (
	"bytes"
	"encoding/json"
	"os"
	"reflect"
	"testing"

	"github.com/waiting-here/NonbiriAPI/internal/db"
	"github.com/waiting-here/NonbiriAPI/internal/lakenotes/rules"
)

func TestLegacyCheckpointsPreserveEncounterAndResume(t *testing.T) {
	raw, err := os.ReadFile("testdata/legacy-casts.json")
	if err != nil {
		t.Fatal(err)
	}
	var cases []struct {
		Name          string
		Profile, Cast json.RawMessage
	}
	if err = json.Unmarshal(raw, &cases); err != nil {
		t.Fatal(err)
	}
	for _, saved := range cases {
		t.Run(saved.Name, func(t *testing.T) {
			profile, err := rules.DecodeProfile(saved.Profile)
			if err != nil {
				t.Fatal("legacy profile", err)
			}
			state, err := decodeStoredCast(1, priorCastRulesID, saved.Cast)
			if err != nil {
				t.Fatal("legacy checkpoint", err)
			}
			if state.RulesID != rules.RulesID || state.BitePreparationRemaining != 0 {
				t.Fatal("unexpected converted checkpoint", state.RulesID)
			}
			var old struct {
				Plan struct{ FishKind string }
				Tick uint64
			}
			if err = json.Unmarshal(saved.Cast, &old); err != nil {
				t.Fatal(err)
			}
			if state.Plan.FishKind != old.Plan.FishKind || state.Tick != old.Tick {
				t.Fatal("encounter or progress replaced")
			}
			if state.Terminal() {
				if state.Reward == nil || len(profile.Basket) != 1 {
					t.Fatal("settled reward lost")
				}
				nextProfile, nextState, err := rules.Advance(profile, state, []bool{false})
				if err != nil || !reflect.DeepEqual(profile, nextProfile) || !reflect.DeepEqual(state, nextState) {
					t.Fatal("terminal checkpoint applied twice", err)
				}
				return
			}
			f := newFixture(t)
			view := f.enter(t, f.period(t, "0"))
			started, err := f.service.Start(f.ctx(f.user), f.user, testKey(810), StartInput{view.Revision})
			if err != nil {
				t.Fatal(err)
			}
			id := started.Value.Cast.ID
			coins, err := db.ParseU128Decimal(string(profile.Coins))
			if err != nil {
				t.Fatal(err)
			}
			if _, err = f.database.Exec("UPDATE lake_notes_profiles SET rules_id=?,profile=?,coin_mag=? WHERE user_id=?", priorCastRulesID, []byte(saved.Profile), db.EncodeU128(coins), f.user); err != nil {
				t.Fatal(err)
			}
			if _, err = f.database.Exec("UPDATE lake_notes_casts SET storage_version=1,rules_id=?,snapshot=?,phase=?,last_tick=?,paused=1,held=0,active_elapsed_ns=?,active_started_at_ns=NULL,lease_until_ns=NULL WHERE id=?", priorCastRulesID, []byte(saved.Cast), state.Phase, state.Tick, int64(state.Tick)*int64(1e9)/60, id); err != nil {
				t.Fatal(err)
			}
			draws := f.random.draws.Load()
			current, err := f.service.Cast(f.ctx(f.user), f.user, id)
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(current.Cast.State.Plan, state.Plan) || current.Cast.State.Tick != state.Tick || current.Cast.State.Progress != state.Progress || current.Cast.State.Motion != state.Motion {
				t.Fatal("saved encounter changed")
			}
			var after []byte
			if err = f.database.QueryRow("SELECT snapshot FROM lake_notes_casts WHERE id=?", id).Scan(&after); err != nil || !bytes.Equal(after, saved.Cast) {
				t.Fatal("read rewrote checkpoint", err)
			}
			resumed, err := f.service.Resume(f.ctx(f.user), f.user, id, testKey(811), control(current.Cast))
			if err != nil {
				t.Fatal(err)
			}
			advanced, err := f.service.Checkpoint(f.ctx(f.user), f.user, id, testKey(812), checkpoint(resumed.Value.Cast, 1))
			if err != nil || advanced.Value.Cast.AckTick != state.Tick+1 {
				t.Fatal("resume failed", err)
			}
			var version int
			var identity string
			if err = f.database.QueryRow("SELECT storage_version,rules_id FROM lake_notes_casts WHERE id=?", id).Scan(&version, &identity); err != nil || version != castStorageVersion || identity != rules.RulesID || f.random.draws.Load() != draws {
				t.Fatal("conversion rerolled or not stored", err)
			}
		})
	}
}
