package likes

import (
	"encoding/json"
	"testing"

	"github.com/waiting-here/NonbiriAPI/internal/game/likes/engine"
)

func adapterState(t *testing.T) (*Rules, json.RawMessage) {
	t.Helper()
	r, err := NewRules()
	if err != nil {
		t.Fatal(err)
	}
	c := r.engines["quick"].Catalog()
	var loadouts [2]json.RawMessage
	for seat := range 2 {
		role := c.Roles[seat].ID
		for _, sk := range c.Skills {
			selection := engine.Selection{Role: role, Skills: []string{sk.ID}}
			if r.engines["quick"].ValidateSelection(selection) == nil {
				loadouts[seat], _ = json.Marshal(selection)
				break
			}
		}
		if loadouts[seat] == nil {
			t.Fatal("no sustainable selection")
		}
	}
	s, err := r.Create("quick", loadouts)
	if err != nil {
		t.Fatal(err)
	}
	return r, s
}
func TestLikesAdapterKeepsSettlementUntilBeginAndHidesLoadout(t *testing.T) {
	r, s := adapterState(t)
	var native engine.State
	_ = json.Unmarshal(s, &native)
	view, err := r.View("quick", s, 0, false, nil)
	if err != nil {
		t.Fatal(err)
	}
	var v engine.View
	_ = json.Unmarshal(view, &v)
	if !v.Players[1].Fog || len(v.Players[1].Loadout) != 0 || len(v.Players[0].Loadout) == 0 {
		t.Fatal("fog projection")
	}
	var actions [2]json.RawMessage
	for seat := range 2 {
		plan := engine.EmptyPlan()
		plan.Main = &engine.Choice{SkillID: native.Players[seat].Loadout[0]}
		actions[seat], _ = json.Marshal(action{Kind: "plan", Plan: &plan})
	}
	next, err := r.Resolve("quick", s, actions)
	if err != nil {
		t.Fatal(err)
	}
	info, err := r.Inspect("quick", next.State)
	if err != nil || info.Phase != "settlement" || info.Seconds != 5 || info.Round != 1 {
		t.Fatal(info, err)
	}
	if _, err := r.Accept("quick", next.State, 0, actions[0]); err == nil {
		t.Fatal("accepted during presentation")
	}
	var cues presentation
	if json.Unmarshal(next.Presentation, &cues) != nil || len(cues.Frames) != 7 {
		t.Fatal("missing semantic stages")
	}
	casts := 0
	for _, event := range cues.Events {
		if event.Kind == "cast" {
			casts++
			if event.Score == nil {
				t.Fatal("missing score explanation")
			}
		}
	}
	if casts != 2 {
		t.Fatal(casts)
	}
	if _, err := r.RoundView("quick", next.Record, 0, false); err != nil {
		t.Fatal(err)
	}
	s, _, err = r.Begin("quick", next.State)
	if err != nil {
		t.Fatal(err)
	}
	info, err = r.Inspect("quick", s)
	if err != nil || info.Phase != "plan" || info.Round != 2 || info.Seconds != 20 {
		t.Fatal(info, err)
	}
	if _, _, err := r.Begin("quick", s); err == nil {
		t.Fatal("duplicate begin")
	}
	view, err = r.View("quick", s, 0, true, nil)
	if err != nil {
		t.Fatal(err)
	}
	_ = json.Unmarshal(view, &v)
	if v.Players[1].Fog || len(v.Players[1].Loadout) == 0 {
		t.Fatal("terminal loadout missing")
	}
}
func TestLikesCommandsRejectMissingAndNullCollections(t *testing.T) {
	r, s := adapterState(t)
	for _, body := range []string{`{"kind":"plan","plan":{}}`, `{"kind":"plan","plan":{"purchases":null,"main":null,"extra":[]}}`, `{"kind":"plan","plan":{"purchases":[],"main":null,"extra":null}}`, `{"kind":"plan","plan":{"purchases":[],"main":null,"extra":[],"winner":0}}`} {
		if _, err := r.Accept("quick", s, 0, []byte(body)); err == nil {
			t.Fatal(body)
		}
	}
}

func TestManualEmptyActionRejectedButAutomaticTimeoutStillResolves(t *testing.T) {
	r, state := adapterState(t)
	var actions [2]json.RawMessage
	for seat := range 2 {
		var err error
		actions[seat], err = r.Automatic("quick", state, seat)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := r.Accept("quick", state, seat, actions[seat]); err == nil {
			t.Fatal("manual empty plan accepted")
		}
	}
	next, err := r.Resolve("quick", state, actions)
	if err != nil {
		t.Fatal("automatic timeout could not settle", err)
	}
	info, err := r.Inspect("quick", next.State)
	if err != nil || info.Phase != "settlement" {
		t.Fatal(info, err)
	}
}
