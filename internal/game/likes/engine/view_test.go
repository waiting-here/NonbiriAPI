package engine

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
)

func TestViewsHideUnusedLoadoutsAndPrivateState(t *testing.T) {
	e, s := fixture(t, "quick", Selection{Role: "ChatGPT", Skills: []string{"PUB01", "PUB02", "PUB41", "GPT44"}}, Selection{Role: "Claude", Skills: []string{"PUB01", "CLA21"}})
	s.Players[0].Distill.Template = ptr("PUB02")
	s.Players[0].Distill.Level = ptr("II")
	s.Players[0].SkillDecay = map[string]int64{"PUB02": 1, "PUB02:distilled": 1}
	original := clone(s)
	locked := plan("GPT44")
	view, err := e.Project(s, 1, false, nil)
	if err != nil {
		t.Fatal(err)
	}
	body, _ := json.Marshal(view)
	for _, secret := range []string{"GPT44", "PUB41", "PUB02", "draw_seq", "event_seq", "grants", "loadout\":[\"PUB01\",\"PUB02"} {
		if strings.Contains(string(body), secret) {
			t.Fatalf("private data leaked: %s", secret)
		}
	}
	if view.Players[0].Distill != nil || view.LockedPlan != nil || len(view.Players[0].Loadout) != 0 || len(view.Players[0].Used) != 0 || len(view.Players[0].SkillDecay) != 0 || !view.Players[0].Fog {
		t.Fatal("opponent saw unused slots")
	}
	own, err := e.Project(s, 0, false, &locked)
	if err != nil || own.Players[0].Distill == nil || own.LockedPlan.Main.SkillID != "GPT44" || len(own.Players[0].SkillDecay) != 2 {
		t.Fatal("own data missing")
	}
	own.Players[0].Loadout[0] = "changed"
	own.Players[0].Subscription.BurstInitial = 99
	own.Players[0].Resources["R_IMAGE"] = 99
	own.LockedPlan.Main.SkillID = "changed"
	if !reflect.DeepEqual(original, s) || locked.Main.SkillID != "GPT44" {
		t.Fatal("view shares mutable state")
	}
	s.Players[0].Revealed = []string{"PUB41"}
	view, err = e.Project(s, 1, false, nil)
	if err != nil || view.Players[0].Distill == nil || len(view.Players[0].SkillDecay) != 1 || view.Players[0].SkillDecay["PUB02:distilled"] != 1 || len(view.Players[0].Used) != 1 {
		t.Fatal("revealed distillation projection is incorrect")
	}
	terminal, err := e.Project(s, 1, true, nil)
	if err != nil || terminal.Players[0].Fog || len(terminal.Players[0].Loadout) != 4 || len(terminal.Players[0].SkillDecay) != 2 {
		t.Fatal("terminal loadout incomplete")
	}
	if _, err := e.Project(s, 2, false, nil); err == nil {
		t.Fatal("unauthenticated spectator accepted")
	}
}
