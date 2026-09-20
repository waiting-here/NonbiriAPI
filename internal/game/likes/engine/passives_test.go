package engine

import (
	"errors"
	"reflect"
	"testing"
)

func casts(record RoundRecord) []Event {
	var found []Event
	for _, event := range record.Events {
		if event.Kind == "cast" {
			found = append(found, event)
		}
	}
	return found
}

func characterPart(event Event) int64 {
	for _, part := range event.Score.Parts {
		if part.Key == "character" {
			return part.Amount
		}
	}
	return 0
}

func TestCharacterMainUsesSharedSnapshotAndNominalBase(t *testing.T) {
	for _, mode := range []string{"quick", "standard"} {
		for leader := range 2 {
			e, s := fixture(t, mode, Selection{Role: "Claude", Harness: ptr("H01"), Skills: []string{"CLA01", "CLA23"}}, Selection{Role: "Claude", Harness: ptr("H01"), Skills: []string{"CLA01", "CLA23"}})
			s.Players[leader].Likes = 1
			s.LikesAtStart = [2]int64{s.Players[0].Likes, s.Players[1].Likes}
			next, record, err := e.Resolve(s, [2]Plan{plan("CLA01"), plan("CLA01")}, nil)
			if err != nil {
				t.Fatal(err)
			}
			for _, event := range casts(record) {
				want := int64(0)
				if *event.Seat == leader {
					want = 1
				}
				if characterPart(event) != want || event.Data["step"].(StepLikesSnapshot).Likes != s.LikesAtStart {
					t.Fatalf("seat order changed main snapshot: %+v", event)
				}
			}
			if next.Players[leader].Likes != 6 || next.Players[other(leader)].Likes != 4 {
				t.Fatal("wrong base phase")
			}
			plans := [2]Plan{EmptyPlan(), EmptyPlan()}
			plans[leader] = plan("CLA23")
			_, record, err = e.Resolve(s, plans, nil)
			if err != nil || characterPart(casts(record)[0]) != 0 || casts(record)[0].Score.Final != 0 {
				t.Fatal("nominal zero acquired passive eligibility", err)
			}
		}
	}
}

func TestExtraAndFlashSnapshotsAdvanceWithoutChangingRoundStart(t *testing.T) {
	for source := range 2 {
		selections := []Selection{{Role: "GLM", Skills: []string{"GLM01"}}, {Role: "GLM", Skills: []string{"GLM01"}}}
		selections[source] = Selection{Role: "Gemini", Skills: []string{"PUB01", "GEM61", "PUB62"}}
		e, s := fixture(t, "quick", selections...)
		s.Players[other(source)].Likes = 1
		s.LikesAtStart[other(source)] = 1
		s.Players[source].API = 100_000
		s.Energy = e.param("ENERGY_CAP")
		plans := [2]Plan{plan("GLM01"), plan("GLM01")}
		plans[source] = plan("GEM61")
		plans[source].Extra = []Choice{{SkillID: "PUB62"}}
		_, record, err := e.Resolve(s, plans, func(bound int) (int, error) {
			if bound != 150 {
				t.Fatalf("extra used round-start or partially committed scores: %d", bound)
			}
			return 0, nil
		})
		if err != nil {
			t.Fatal(err)
		}
		attempts := 0
		for _, event := range record.Events {
			if event.Kind != "effect-attempt" {
				continue
			}
			attempts++
			step := event.Data["step"].(StepLikesSnapshot)
			if step.Kind != "extra" || step.Index != 0 || step.Likes[source] != 5 || step.Likes[other(source)] != 4 {
				t.Fatalf("extra snapshot: %+v", step)
			}
		}
		// Degradation still uses the opponent's round-start lead, giving four
		// layers even though that opponent is behind by the extra step.
		if attempts != 4 {
			t.Fatalf("round-start degradation changed: %d", attempts)
		}
	}
	e, s := fixture(t, "standard", Selection{Role: "Gemini", Skills: []string{"PUB01", "GEM61"}}, Selection{Role: "Gemini", Skills: []string{"PUB01", "GEM61"}})
	for seat := range 2 {
		s.Players[seat].API = 100_000
		initialBuff(e, &s, seat, "B28:通用", 2)
	}
	s.Energy = e.param("ENERGY_CAP")
	p := plan("GEM61")
	p.Extra = []Choice{{SkillID: "GEM61"}}
	_, record, err := e.Resolve(s, [2]Plan{p, p}, nil)
	if err != nil {
		t.Fatal(err)
	}
	seen := map[int][]StepLikesSnapshot{}
	for _, event := range casts(record) {
		step := event.Data["step"].(StepLikesSnapshot)
		if step.Kind == "flash" {
			seen[step.Index] = append(seen[step.Index], step)
			if characterPart(event) != 1 || event.Score.Final != 4 {
				t.Fatal("Flash did not get basic character bonus")
			}
		}
	}
	if len(seen) != 4 || len(seen) > MaxFlashPerSeat {
		t.Fatalf("dense Flash chain incomplete: %v", seen)
	}
	for batch := range 4 {
		pair := seen[batch]
		if len(pair) != 2 || pair[0] != pair[1] || pair[0].Likes != [2]int64{10 + int64(batch)*4, 10 + int64(batch)*4} {
			t.Fatalf("Flash observed partial commit: %+v", pair)
		}
	}
}

func TestCharacterBonusBeforeSuppressionAndMultiplierButDistillStaysSpecial(t *testing.T) {
	e, s := fixture(t, "quick", Selection{Role: "Gemini", Skills: []string{"PUB01", "PUB41"}})
	initialBuff(e, &s, 0, "B18:原版", 1)
	initialBuff(e, &s, 0, "B34:状态", 1)
	s.Players[0].API = 100_000
	next, record, err := e.Resolve(s, [2]Plan{plan("PUB01"), EmptyPlan()}, nil)
	if err != nil {
		t.Fatal(err)
	}
	c := casts(record)[0]
	if characterPart(c) != 1 || c.Score.Original != 3 || c.Score.Intrinsic != 4 || c.Score.BeforeMultiplier != 1 || c.Score.Multiplier != 2 || next.Players[0].Likes != 2 {
		t.Fatalf("wrong modifier order: %+v", c.Score)
	}
	s.Players[0].Distill.Template, s.Players[0].Distill.Level = ptr("GEM01"), ptr("II")
	_, record, err = e.Resolve(s, [2]Plan{plan("PUB41"), EmptyPlan()}, nil)
	if err != nil || characterPart(casts(record)[0]) != 0 {
		t.Fatal("distilled basic became a basic cast", err)
	}
}

func TestLayerResistanceBoundariesAndSOTASuccessChain(t *testing.T) {
	for _, test := range []struct {
		name          string
		tape          []int
		success, sota int64
	}{
		{"all-resisted", []int{100, 124}, 0, 0},
		{"partial-and-sota-resisted", []int{99, 100, 124}, 1, 0},
		{"partial-and-sota-success", []int{100, 0, 99}, 1, 1},
		{"full", []int{0, 99, 0}, 2, 1},
	} {
		t.Run(test.name, func(t *testing.T) {
			e, s := fixture(t, "quick", Selection{Role: "Claude", Harness: ptr("H02"), Skills: []string{"CLA01", "CLA23"}}, Selection{Role: "GLM", Skills: []string{"GLM01"}})
			i := 0
			next, record, err := e.Resolve(s, [2]Plan{plan("CLA23"), EmptyPlan()}, func(n int) (int, error) {
				if n != 125 || i >= len(test.tape) {
					t.Fatalf("unexpected draw %d/%d", i, n)
				}
				v := test.tape[i]
				i++
				return v, nil
			})
			if err != nil || i != len(test.tape) {
				t.Fatal(err, i)
			}
			layers := map[string]int64{}
			for _, st := range next.Players[1].Effects {
				layers[st.Kind] += st.Layers
			}
			if layers["BASE_SUPPRESS"] != test.success || layers["SOTA_FANATICISM"] != test.sota {
				t.Fatal(layers)
			}
			if !hasStatus(next.Players[0], "BASE_SUPPRESS") {
				t.Fatal("own side effect was resisted")
			}
			attempts := 0
			for _, event := range record.Events {
				if event.Kind == "effect-attempt" {
					attempts++
					if event.Data["draw"].(*int64) == nil {
						t.Fatal("probabilistic attempt omitted draw")
					}
				}
			}
			if attempts != len(test.tape) {
				t.Fatal("recursive or missing SOTA", attempts)
			}
			applications := casts(record)[0].Data["applications"].([]Application)
			if applications[0].Success != test.success || applications[0].Resisted != 2-test.success {
				t.Fatal(applications)
			}
			// Identical injected draws produce every event, layer and index again.
			i = 0
			again, replay, err := e.Resolve(s, [2]Plan{plan("CLA23"), EmptyPlan()}, func(int) (int, error) { v := test.tape[i]; i++; return v, nil })
			if err != nil || !reflect.DeepEqual(next, again) || !reflect.DeepEqual(record, replay) {
				t.Fatal("replay differs", err)
			}
		})
	}
}

func TestDeepSeekHitsGLMWithoutDrawAndFailureLeavesInputIntact(t *testing.T) {
	for _, scores := range [][2]int64{{0, 0}, {0, 1}, {1, 0}} {
		e, s := fixture(t, "quick", Selection{Role: "DeepSeek", Harness: ptr("H02"), Skills: []string{"PUB01", "PUB62"}}, Selection{Role: "GLM", Skills: []string{"GLM01"}})
		for seat := range 2 {
			s.Players[seat].Likes = scores[seat]
		}
		s.LikesAtStart = scores
		s.Players[0].API = 100_000
		_, record, err := e.Resolve(s, [2]Plan{plan("PUB62"), EmptyPlan()}, func(int) (int, error) { t.Fatal("guaranteed hit consumed randomness"); return 0, nil })
		if err != nil || len(record.Draws) != 0 {
			t.Fatal(err)
		}
		for _, event := range record.Events {
			if event.Kind == "effect-attempt" && (event.Data["success"] != true || event.Data["draw"].(*int64) != nil) {
				t.Fatal(event)
			}
		}
	}
	e, s := fixture(t, "quick", Selection{Role: "Claude", Skills: []string{"CLA01", "CLA21"}}, Selection{Role: "GLM", Skills: []string{"GLM01"}})
	before := clone(s)
	_, _, err := e.Resolve(s, [2]Plan{plan("CLA21"), EmptyPlan()}, func(int) (int, error) { return 0, errors.New("unavailable") })
	if !errors.Is(err, ErrRandom) || !reflect.DeepEqual(s, before) {
		t.Fatal("random failure partially committed", err)
	}
}

func TestOverflowStillAttemptsAndRefreshesOnlyOnSuccess(t *testing.T) {
	e, s := fixture(t, "quick", Selection{Role: "Claude", Harness: ptr("H02"), Skills: []string{"CLA01", "CLA23"}}, Selection{Role: "GLM", Skills: []string{"GLM01"}})
	initialBuff(e, &s, 1, "B36:原版", 4)
	initialBuff(e, &s, 1, "B35:原", 4)
	for i := range s.Players[1].Effects {
		if s.Players[1].Effects[i].Kind == "SOTA_FANATICISM" {
			s.Players[1].Effects[i].RefreshedTurn = ptr(int64(0))
		}
	}
	for _, success := range []bool{false, true} {
		draws := 0
		next, _, err := e.Resolve(s, [2]Plan{plan("CLA23"), EmptyPlan()}, func(int) (int, error) {
			draws++
			if success {
				return 0, nil
			}
			return 124, nil
		})
		if err != nil {
			t.Fatal(err)
		}
		want := 2
		if success {
			want = 3
		}
		if draws != want {
			t.Fatal("cap skipped attempts", draws)
		}
		for _, st := range next.Players[1].Effects {
			if st.Kind == "SOTA_FANATICISM" {
				expected := int64(0)
				if success {
					expected = 1
				}
				if optional(st.RefreshedTurn, -1) != expected {
					t.Fatal("incorrect refresh", st)
				}
			}
		}
	}
}
