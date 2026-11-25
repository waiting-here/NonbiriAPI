package official

import (
	"encoding/json"
	"testing"

	"github.com/waiting-here/NonbiriAPI/internal/game/likes/engine"
)

func TestLikesPlansCompleteBothModesWithLockedSetup(t *testing.T) {
	for _, mode := range []string{"quick", "standard"} {
		t.Run(mode, func(t *testing.T) {
			rules, err := loadLikes(mode)
			if err != nil {
				t.Fatal(err)
			}
			base := engine.Selection{Role: "Claude", Skills: []string{"PUB21"}}
			example, _ := json.Marshal(base)
			selection, err := rules.setup([]json.RawMessage{example}, "")
			if err != nil || selection.Role != base.Role || selection.Harness != nil || selection.Skills[0] != "PUB21" || len(selection.Skills) < 2 {
				t.Fatalf("unexpected setup %#v: %v", selection, err)
			}
			e := rules.engine
			state, err := e.Create([2]engine.Selection{base, selection})
			if err != nil {
				t.Fatal(err)
			}
			for state.Result == nil {
				var plans [2]engine.Plan
				for seat := range 2 {
					plans[seat] = engine.EmptyPlan()
					if state.Blocked[seat] {
						continue
					}
					view, err := e.Project(state, seat, false, nil)
					if err != nil {
						t.Fatal(err)
					}
					var choices []json.RawMessage
					for _, id := range append([]string{""}, state.Players[seat].Loadout...) {
						p := engine.EmptyPlan()
						if id != "" {
							p.Main = &engine.Choice{SkillID: id}
						}
						if _, err := e.ValidateManualPlan(state, seat, p); err == nil {
							raw, _ := json.Marshal(likesAction{"plan", p})
							choices = append(choices, raw)
						}
					}
					action, err := rules.choosePlan(seat, view, choices)
					if err != nil {
						t.Fatal(err)
					}
					plans[seat], err = e.ValidateManualPlan(state, seat, action.Plan)
					if err != nil {
						t.Fatalf("round %d, seat %d: %v", state.Round, seat, err)
					}
				}
				state, _, err = e.Resolve(state, plans, func(int) (int, error) { return 0, nil })
				if err != nil {
					t.Fatal(err)
				}
				if state.AwaitingNextRound {
					state, _, err = e.BeginNextRound(state)
					if err != nil {
						t.Fatal(err)
					}
				}
			}
			if state.Result.Scores[1] == 0 {
				t.Fatal("companion did not score")
			}
		})
	}
}
