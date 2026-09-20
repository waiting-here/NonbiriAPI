package engine

import (
	"errors"
	"reflect"
	"slices"
	"testing"
)

func initialBuff(e *Engine, s *State, seat int, id string, count int) {
	r := roundRun{e: e, s: s, record: &RoundRecord{Events: []Event{}}, stage: "initial"}
	for range count {
		r.materialize(Grant{BuffID: id, Owner: seat}, s.Round)
	}
	s.Blocked[seat], s.Players[seat].Stunned = restriction(s, seat)
}

func TestOverloadQuoteIncludesCancelledExtraAndPostShoppingCharge(t *testing.T) {
	e, s := fixture(t, "quick", Selection{Role: "Gemini", Skills: []string{"PUB01", "GEM61"}}, Selection{Role: "ChatGPT", Skills: []string{"PUB01", "GPT44"}})
	declaration := plan("GEM61")
	declaration.Extra = []Choice{{SkillID: "GEM61"}}
	needed := e.skills["GEM61"].Energy * 2
	s.Energy = needed - 1
	s.Players[0].Burst = 0
	s.Players[0].Sub = 0
	s.Players[0].API = 0
	next, record, err := e.Resolve(s, [2]Plan{declaration, plan("GPT44")}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if next.Result != nil || !hasStatus(next.Players[0], "OVERLOAD") || hasStatus(next.Players[1], "OVERLOAD") || !hasStatus(next.Players[1], "SPEED_MODE") {
		t.Fatal("cancelled declared costs did not enter shared quotation")
	}
	found := false
	for _, event := range record.Events {
		if event.Kind == "overload" && event.Data["reason"] == "shared-energy" {
			found = true
			if event.Data["required"] != needed {
				t.Fatalf("extra missing from frozen quote: %+v", event.Data)
			}
		}
	}
	if !found {
		t.Fatal("shared quotation event absent")
	}
	e, s = fixture(t, "quick")
	s.Energy = 9
	p := plan("PUB01")
	p.Purchases = []Purchase{{Item: "charge"}}
	next, _, err = e.Resolve(s, [2]Plan{p, plan("")}, nil)
	if err != nil || next.Result != nil || hasStatus(next.Players[0], "OVERLOAD") || next.Energy != 109 || next.Players[0].Gold != 100 {
		t.Fatalf("charge not included before quotation: %v", err)
	}
}

func TestExistingOverloadExpiresWithoutCancellingOtherZeroCostEffects(t *testing.T) {
	e, s := fixture(t, "quick", Selection{Role: "ChatGPT", Skills: []string{"PUB01", "GPT44"}}, Selection{Role: "Claude", Skills: []string{"PUB01"}})
	initialBuff(e, &s, 0, "B33:状态", 1)
	s.Energy = 9
	next, _, err := e.Resolve(s, [2]Plan{EmptyPlan(), plan("PUB01")}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if next.Result != nil || hasStatus(next.Players[0], "OVERLOAD") || !hasStatus(next.Players[1], "OVERLOAD") {
		t.Fatal("expiring old overload was treated as a second new overload")
	}
	ready, _, err := e.BeginNextRound(next)
	if err != nil || ready.Blocked != ([2]bool{false, true}) {
		t.Fatalf("incorrect recovery: %v", err)
	}
	next, _, err = e.Resolve(ready, [2]Plan{plan("GPT44"), EmptyPlan()}, nil)
	if err != nil || !hasStatus(next.Players[0], "SPEED_MODE") || hasStatus(next.Players[0], "OVERLOAD") {
		t.Fatalf("recovered zero-cost skill cancelled: %v", err)
	}
}

func TestWinnerUsesCompletedRoundAndResultPriority(t *testing.T) {
	e, s := fixture(t, "quick")
	s.Players[0].Likes, s.Players[1].Likes = 58, 59
	s.LikesAtStart = [2]int64{58, 59}
	next, record, err := e.Resolve(s, [2]Plan{plan("PUB01"), plan("PUB01")}, nil)
	if err != nil || next.Result == nil || optional(next.Result.Winner, -1) != 1 || next.Result.Scores != ([2]int64{61, 63}) || next.Result.Reason != "target" || next.AwaitingNextRound || record.Result == nil {
		t.Fatalf("first scorer incorrectly won: %v %+v", err, next.Result)
	}
	s.Players[0].Likes = 59
	s.LikesAtStart[0] = 59
	next, _, err = e.Resolve(s, [2]Plan{plan("PUB01"), plan("PUB01")}, nil)
	if err != nil || next.Result == nil || next.Result.Winner != nil {
		t.Fatal("simultaneous target tie lost")
	}
}

func TestFlashBatchAndIndependentFailureNeverOverload(t *testing.T) {
	for _, failure := range []string{"none", "energy", "token"} {
		e, s := fixture(t, "quick", Selection{Role: "Gemini", Skills: []string{"PUB01", "GEM61"}}, Selection{Role: "Claude", Skills: []string{"PUB01"}})
		s.Players[0].API = 100_000
		s.Energy = e.param("ENERGY_CAP")
		initialBuff(e, &s, 0, "B28:通用", 2)
		p := plan("GEM61")
		p.Extra = []Choice{{SkillID: "GEM61"}}
		if failure == "energy" {
			s.Energy = e.skills["GEM61"].Energy * 2
		}
		if failure == "token" {
			s.Players[0].Burst = 0
			s.Players[0].Sub = 0
			s.Players[0].API = e.skills["GEM61"].Token * 2
		}
		next, record, err := e.Resolve(s, [2]Plan{p, EmptyPlan()}, nil)
		if err != nil {
			t.Fatalf("%s: %v", failure, err)
		}
		flashes, failed := 0, 0
		for _, event := range record.Events {
			if event.Kind == "cast" && event.Data["derived"] == true {
				flashes++
			}
			if event.Kind == "combo-skip" {
				failed++
			}
		}
		if failure == "none" && flashes != 4 || failure != "none" && (flashes != 0 || failed == 0) || hasStatus(next.Players[0], "OVERLOAD") {
			t.Fatalf("%s: flashes=%d skipped=%d", failure, flashes, failed)
		}
	}
}

func TestRandomFailureAndInvalidPlansLeaveNoPartialChanges(t *testing.T) {
	e, s := fixture(t, "quick", Selection{Role: "ChatGPT", Skills: []string{"PUB01", "PUB41"}}, Selection{Role: "Claude", Skills: []string{"PUB01"}})
	s.Players[0].Distill.Template = ptr("GPT41")
	s.Players[0].Distill.Level = ptr("I")
	s.Players[0].Distill.Learning = 0
	initialBuff(e, &s, 0, "B17:原版", 1)
	initialBuff(e, &s, 0, "B18:原版", 1)
	original := clone(s)
	if _, _, err := e.Resolve(s, [2]Plan{plan("PUB41"), EmptyPlan()}, func(int) (int, error) { return 0, errors.New("unavailable") }); !errors.Is(err, ErrRandom) {
		t.Fatalf("random error not closed: %v", err)
	}
	if !reflect.DeepEqual(s, original) {
		t.Fatal("failed resolution mutated input")
	}
	for _, invalid := range []Plan{
		{Purchases: nil, Main: &Choice{SkillID: "PUB01"}},
		{Purchases: []Purchase{}, Extra: []Choice{{SkillID: "PUB01"}}},
		{Purchases: []Purchase{{Item: "api"}, {Item: "api"}}},
		{Purchases: []Purchase{{Item: "regulator"}, {Item: "cleanse", Target: "B18:原版"}}},
		{Purchases: []Purchase{}, Main: &Choice{SkillID: "CLA21"}},
		{Purchases: []Purchase{}, Main: &Choice{SkillID: "PUB41", Targets: []string{"B17:原版"}}},
	} {
		if _, err := e.ValidatePlan(s, 0, invalid); err == nil {
			t.Fatalf("invalid plan accepted: %+v", invalid)
		}
	}
	bad := clone(s)
	bad.Blocked[0] = true
	if e.Validate(bad) == nil {
		t.Fatal("inconsistent automatic lock accepted")
	}
	for _, value := range []int64{-1, MaxResourceValue + 1, MaxSafeInteger, 1<<63 - 1} {
		bad = clone(s)
		bad.Players[0].API = value
		if _, _, err := e.Resolve(bad, [2]Plan{EmptyPlan(), EmptyPlan()}, nil); !errors.Is(err, ErrState) {
			t.Fatal("unsafe resource arithmetic accepted")
		}
	}
	if !reflect.DeepEqual(s, original) {
		t.Fatal("invalid plan mutated input")
	}
}

func TestScoreBreakdownAndPaymentFramesAreAuthoritative(t *testing.T) {
	e, s := fixture(t, "quick", Selection{Role: "ChatGPT", Harness: ptr("H01"), Skills: []string{"PUB01", "GPT41"}}, Selection{Role: "Claude", Skills: []string{"PUB01"}})
	initialBuff(e, &s, 0, "B13:原版", 1)
	initialBuff(e, &s, 0, "B18:原版", 1)
	initialBuff(e, &s, 0, "B37:通用", 2)
	p := plan("GPT41")
	p.Main.Targets = []string{"B18:原版"}
	_, record, err := e.Resolve(s, [2]Plan{p, plan("PUB01")}, nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, event := range record.Events {
		if event.Kind == "cast" {
			if event.Score == nil {
				t.Fatal("cast has no score breakdown")
			}
			score := event.Score
			sum := score.Original
			for _, part := range score.Parts {
				sum += part.Amount
			}
			if sum != score.BeforeMultiplier || score.Final != score.BeforeMultiplier*score.Multiplier+score.Passive || event.Data["likes"] != score.Final {
				t.Fatalf("breakdown contradicts result: %+v", score)
			}
		}
	}
	shopping := record.Frames[slices.IndexFunc(record.Frames, func(f Frame) bool { return f.Stage == "shopping" })]
	payment := record.Frames[slices.IndexFunc(record.Frames, func(f Frame) bool { return f.Stage == "payment" })]
	for seat := range 2 {
		sub, api, energy := int64(0), int64(0), int64(0)
		for _, event := range record.Events {
			if event.Kind == "cast" && optional(event.Seat, -1) == seat && event.Data["derived"] == false {
				sub += event.Data["subPayment"].(int64)
				api += event.Data["apiPayment"].(int64)
				energy += event.Data["energy"].(int64)
			}
		}
		if shopping.Players[seat].Burst-payment.Players[seat].Burst != sub || shopping.Players[seat].Sub-payment.Players[seat].Sub != sub || shopping.Players[seat].API-payment.Players[seat].API != api || energy <= 0 {
			t.Fatal("payment frame contradicts successful casts")
		}
	}
}
