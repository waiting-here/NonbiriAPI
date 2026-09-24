package engine

import "testing"

func TestPersistentFlashCacheSurvivesSkippedAndOverloadedRoundsAndRemainsDispellable(t *testing.T) {
	for _, mode := range []string{"quick", "standard"} {
		t.Run(mode, func(t *testing.T) {
			e, state := fixture(t, mode,
				Selection{Role: "Gemini", Skills: []string{"PUB01", "GEM01", "PUB22"}},
				Selection{Role: "Claude", Skills: []string{"PUB01", "CLA41"}})
			state.Players[0].API, state.Players[1].API = 10000, 10000
			resolve := func(state State, plans [2]Plan) (State, RoundRecord) {
				t.Helper()
				next, record, err := e.Resolve(state, plans, func(int) (int, error) { return 0, nil })
				if err != nil {
					t.Fatal(err)
				}
				return next, record
			}
			ready := func(state State) State {
				t.Helper()
				next, _, err := e.BeginNextRound(state)
				if err != nil {
					t.Fatal(err)
				}
				return next
			}
			cache := func(state State, want int64) {
				t.Helper()
				found := false
				for _, st := range state.Players[0].Effects {
					if st.BuffID != "B07:原版" {
						continue
					}
					found = true
					if st.Layers != want || optional(st.PersistentLayers, int64(-1)) != want {
						t.Fatalf("cache layers=%d persistent=%v; want %d", st.Layers, st.PersistentLayers, want)
					}
				}
				if found != (want > 0) {
					t.Fatalf("cache presence %v; want %d layers", found, want)
				}
			}
			for range 3 {
				state, _ = resolve(state, [2]Plan{plan("GEM01"), EmptyPlan()})
				state = ready(state)
			}
			converted, record := resolve(state, [2]Plan{plan("PUB22"), EmptyPlan()})
			cache(converted, 3)
			persisted := false
			for _, event := range record.Events {
				if event.Kind == "persist" && event.Data["buffId"] == "B07:原版" && event.Data["layers"] == int64(3) {
					persisted = true
				}
			}
			if !persisted {
				t.Fatal("real cache conversion did not emit its authoritative event")
			}
			for _, scenario := range []string{"skip", "overload", "dispel"} {
				t.Run(scenario, func(t *testing.T) {
					start := ready(converted)
					plans := [2]Plan{EmptyPlan(), EmptyPlan()}
					switch scenario {
					case "overload":
						start.Energy = 1
						plans[0] = plan("GEM01")
					case "dispel":
						plans[1] = plan("CLA41")
						plans[1].Main.Targets = []string{"B07:原版"}
					}
					next, _ := resolve(start, plans)
					want := int64(3)
					if scenario == "dispel" {
						want = 0
					}
					cache(next, want)
					if scenario == "overload" && !hasStatus(next.Players[0], "OVERLOAD") {
						t.Fatal("fixture did not exercise overload")
					}
				})
			}
		})
	}
}
