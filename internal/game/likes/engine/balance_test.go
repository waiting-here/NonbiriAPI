package engine

import (
	"reflect"
	"slices"
	"testing"
)

func TestBalanceSpeedAndModifierOrder(t *testing.T) {
	e, s := fixture(t, "standard", Selection{Role: "ChatGPT", Harness: ptr("H08"), Skills: []string{"PUB01", "GPT44"}})
	initialBuff(e, &s, 0, "B34:状态", 1)
	speed := e.speed(&s, 0)
	for _, n := range []int64{0, 1, 2, 3, 15, 100, 1_000_001} {
		if got := e.speedCost(n, speed); got != 3*n {
			t.Fatalf("triple %d: %d", n, got)
		}
	}
	initialBuff(e, &s, 0, "B01:原版", 1)
	initialBuff(e, &s, 0, "B35:原", 1)
	initialBuff(e, &s, 0, "B17:原版", 1)
	v := e.usage(&s, 0, Choice{SkillID: "PUB01"}, false)
	// Completion precedes speed; cache and tax follow it. SOTA energy precedes speed.
	if v.Token != e.speedCost(100-15, speed)-30+e.buffs["B17:原版"].P || v.Energy != e.speedCost(10+e.buffs["B35:原"].P, speed) {
		t.Fatalf("cost order: %+v", v)
	}
	zero := e.usage(&s, 0, Choice{SkillID: "GPT44"}, false)
	if zero.Token != 0 || zero.Energy != 0 {
		t.Fatalf("zero costs changed: %+v", zero)
	}
}

func TestBalanceDS23AllTiersUseOverloadAndPreserveLongerExpiry(t *testing.T) {
	for _, level := range []string{"base", "II", "I"} {
		for _, speed := range []bool{false, true} {
			e, s := fixture(t, "standard", Selection{Role: "DeepSeek", Skills: []string{"DS01", "DS23", "PUB41"}})
			id := "DS23"
			if level != "base" {
				id = "PUB41"
				s.Players[0].Distill.Template = ptr("DS23")
				s.Players[0].Distill.Level = ptr(level)
			}
			if speed {
				initialBuff(e, &s, 0, "B34:状态", 1)
			}
			s.Players[0].API = 100_000
			s.Energy = e.param("ENERGY_CAP")
			next, record, err := e.Resolve(s, [2]Plan{plan(id), EmptyPlan()}, nil)
			duration := int64(1)
			if speed {
				duration = 2
			}
			if err != nil {
				t.Fatal(level, speed, err)
			}
			effect, _ := e.effect(&s, id, 0)
			want := effect.Likes
			if speed {
				want *= 2
			}
			if next.Players[0].Likes != want || len(casts(record)) != 1 || hasStatus(next.Players[0], "STUN") {
				t.Fatal(level, speed, next.Players[0])
			}
			st := next.Players[0].Effects[slices.IndexFunc(next.Players[0].Effects, func(st Status) bool { return st.Kind == "OVERLOAD" })]
			if st.ActiveFrom != 2 || st.Remaining != duration || *st.Expires != 1+duration {
				t.Fatal(st)
			}
			run := roundRun{e: e, s: &next, record: &RoundRecord{}}
			run.materialize(Grant{BuffID: "B33:状态", Owner: 0, Duration: ptr(int64(1))}, 2)
			after := next.Players[0].Effects[slices.IndexFunc(next.Players[0].Effects, func(st Status) bool { return st.Kind == "OVERLOAD" })]
			if *after.Expires != *st.Expires {
				t.Fatal("reapplication shortened recovery")
			}
		}
	}
	e, s := fixture(t, "quick")
	initialBuff(e, &s, 0, "B29:通用", 1)
	if !hasStatus(s.Players[0], "STUN") || e.usage(&s, 0, Choice{SkillID: "PUB01"}, false).Legal {
		t.Fatal("stun compatibility lost")
	}
}

func TestBalanceImageFailureIsLegalUnpaidAndCancelsSuffix(t *testing.T) {
	e, s := fixture(t, "standard", Selection{Role: "ChatGPT", Skills: []string{"PUB01", "GPT22", "GPT43"}})
	s.Players[0].Resources["R_IMAGE"] = 0
	before := clone(s.Players[0])
	p := plan("GPT22")
	preview, err := e.PlanPreview(s, 0, p)
	if err != nil || preview.Success || !slices.Contains(preview.Shortages, "image") || !preview.Actions[0].Preview.Legal {
		t.Fatal(preview, err)
	}
	next, record, err := e.Resolve(s, [2]Plan{p, EmptyPlan()}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(casts(record)) != 0 || next.Players[0].Likes != 0 || next.Players[0].API != before.API || next.Players[0].Sub != before.Sub || next.Players[0].Resources["R_IMAGE"] != 0 || next.Players[0].Used["GPT22"] != 0 || hasStatus(next.Players[0], "AMPLIFY") || !hasStatus(next.Players[0], "OVERLOAD") {
		t.Fatal("failed image action paid or granted", next.Players[0])
	}
	for _, event := range record.Events {
		if event.Kind == "overload" {
			shortage := event.Data["shortage"].(Shortage)
			if !reflect.DeepEqual(shortage, Shortage{"image", []ResourceShortage{{"R_IMAGE", 1, 0}}}) {
				t.Fatal(shortage)
			}
		}
	}
	// A main image-consuming insertion exercises actual preceding consumption.
	sk := e.skills["GPT22"]
	sk.Effects.Base.Kind = "INSERT"
	sk.Effects.Base.P = 1
	e.skills[sk.ID] = sk
	s.Players[0].Resources["R_IMAGE"] = 1
	p.Extra = []Choice{{SkillID: "GPT43"}}
	preview, err = e.PlanPreview(s, 0, p)
	if err != nil || !slices.Contains(preview.Actions[1].Preview.Shortages, "image") {
		t.Fatal("preceding image payment ignored", preview, err)
	}
	next, record, err = e.Resolve(s, [2]Plan{p, EmptyPlan()}, nil)
	if err != nil || len(casts(record)) != 1 || next.Players[0].Resources["R_IMAGE"] != 0 || next.Players[0].Used["GPT43"] != 0 {
		t.Fatal("failed suffix paid", next.Players[0], err)
	}
	s.Players[0].Resources["R_IMAGE"] = 0
	preview, err = e.PlanPreview(s, 0, p)
	if err != nil || !preview.Actions[1].Cancelled {
		t.Fatal("suffix not cancelled", preview, err)
	}
}

func TestBalanceHarnessLikesAreDistinctIndependentAndNonRecursive(t *testing.T) {
	e, s := fixture(t, "standard", Selection{Role: "Claude", Harness: ptr("H02"), Skills: []string{"CLA01", "CLA23"}})
	initialBuff(e, &s, 0, "B34:状态", 1)
	initialBuff(e, &s, 0, "B18:原版", 1)
	r := roundRun{e: e, s: &s, record: &RoundRecord{}, step: stepLikes(&s, "main", 0)}
	grants := []Grant{{BuffID: "B18:原版", Owner: 1, Amount: ptr(int64(2))}, {BuffID: "B18:原版", Owner: 1, Amount: ptr(int64(1))}, {BuffID: "B20:原版", Owner: 1, Amount: ptr(int64(1))}, {BuffID: "B18:原版", Owner: 0, Amount: ptr(int64(1))}}
	_, err := r.applyGrants(0, "CLA23", grants)
	if err != nil || s.Players[0].Likes != 2 {
		t.Fatal("distinct independent gains", s.Players[0].Likes, err)
	}
	n := 0
	for _, event := range r.record.Events {
		if event.Kind == "harness-like" {
			n++
		}
	}
	if n != 2 {
		t.Fatal("duplicate or recursive harness gain", n)
	}
	for _, resist := range []bool{true, false} {
		e, s = fixture(t, "standard", Selection{Role: "Claude", Harness: ptr("H02"), Skills: []string{"CLA01", "CLA23"}}, Selection{Role: "GLM", Skills: []string{"GLM01"}})
		draws := []int{124}
		if !resist {
			draws = []int{0, 124}
		}
		i := 0
		r = roundRun{e: e, s: &s, record: &RoundRecord{}, step: stepLikes(&s, "main", 0), pick: func(int) (int, error) { v := draws[i]; i++; return v, nil }}
		_, err = r.applyGrants(0, "CLA23", []Grant{{BuffID: "B18:原版", Owner: 1, Amount: ptr(int64(1))}})
		want := int64(1)
		if resist {
			want = 0
		}
		if err != nil || i != len(draws) || s.Players[0].Likes != want {
			t.Fatal("resistance changed independent reward", i, s.Players[0].Likes, err)
		}
	}
}

func TestBalancePassiveEligibilityAndInitialGold(t *testing.T) {
	for _, mode := range []string{"quick", "standard"} {
		e, s := fixture(t, mode, Selection{Role: "Gemini", Harness: ptr("H03"), Skills: []string{"GEM01", "PUB41", "GEM61"}})
		want := int64(100)
		if mode == "standard" {
			want = 500
		}
		if s.Players[0].Gold != want {
			t.Fatal(mode, s.Players[0].Gold)
		}
		if e.castPassive(&s, 0, e.skills["GEM01"]) != 2 || e.castPassive(&s, 0, e.skills["PUB41"]) != 0 {
			t.Fatal("Antigravity basic/distill eligibility")
		}
		initialBuff(e, &s, 0, "B34:状态", 1)
		if e.castPassive(&s, 0, e.skills["GEM01"]) != 4 {
			t.Fatal("Antigravity final multiplier")
		}
		e, s = fixture(t, mode, Selection{Role: "ChatGPT", Harness: ptr("H08"), Skills: []string{"PUB01", "GPT44"}})
		for _, pair := range [][2]int64{{0, 0}, {1, 0}, {0, 1}} {
			s.LikesAtStart = pair
			s.Players[0].Likes = 999
			v := e.usage(&s, 0, Choice{SkillID: "PUB01"}, false)
			expected := int64(0)
			if pair[0] < pair[1] {
				expected = 2
			}
			if v.BaseLikeBonus != expected {
				t.Fatal("Copilot did not use strict round-start lead", pair, v)
			}
			if e.usage(&s, 0, Choice{SkillID: "GPT44"}, false).BaseLikeBonus != 0 {
				t.Fatal("zero base gained Copilot bonus")
			}
		}
	}
}

func TestPriorBalanceRetainsFeesGoldImageRejectionAndDS23(t *testing.T) {
	for _, mode := range []string{"quick", "standard"} {
		old, err := NewPriorBalance(mode)
		if err != nil {
			t.Fatal(err)
		}
		selections := [2]Selection{{Role: "DeepSeek", Skills: []string{"DS01", "DS23"}}, {Role: "ChatGPT", Skills: []string{"PUB01", "GPT22"}}}
		s, err := old.Create(selections)
		if err != nil {
			t.Fatal(err)
		}
		want := int64(120)
		if mode == "standard" {
			want = 600
		}
		if s.Players[0].Gold != want {
			t.Fatal("historical gold", s.Players[0].Gold)
		}
		initialBuff(old, &s, 0, "B34:状态", 1)
		if old.speedCost(101, old.speed(&s, 0)) != 303 || old.behaviorVersion() != 2 {
			t.Fatal("historical speed/behavior")
		}
		effect, _ := old.effect(&s, "DS23", 0)
		if effect.Kind != "SELF_STUN" {
			t.Fatal("historical DS23")
		}
		s.Players[1].Resources["R_IMAGE"] = 0
		if _, err := old.ValidatePlan(s, 1, plan("GPT22")); err == nil {
			t.Fatal("historical image shortage became legal")
		}
		current, _ := New(mode)
		if current.ContentHash() == old.ContentHash() || current.behaviorVersion() != 4 {
			t.Fatal("new behavior shares old identity")
		}
	}
}

func TestBalanceAntigravityActuallyRewardsAutomaticFlashButNotDistill(t *testing.T) {
	e, s := fixture(t, "standard", Selection{Role: "Gemini", Harness: ptr("H03"), Skills: []string{"PUB01", "GEM61", "PUB41"}})
	s.Players[0].API = 100_000
	s.Energy = e.param("ENERGY_CAP")
	initialBuff(e, &s, 0, "B28:通用", 2)
	p := plan("GEM61")
	p.Extra = []Choice{{SkillID: "GEM61"}}
	_, record, err := e.Resolve(s, [2]Plan{p, EmptyPlan()}, nil)
	if err != nil {
		t.Fatal(err)
	}
	count := 0
	for _, event := range casts(record) {
		if event.Data["derived"] == true {
			count++
			if event.Data["passiveLikes"] != int64(2) || event.Score.Final != 6 {
				t.Fatal("automatic Flash missed Antigravity gain", event)
			}
		}
	}
	if count < 1 {
		t.Fatal("automatic Flash not exercised")
	}
	s.Players[0].Distill.Template = ptr("GEM01")
	s.Players[0].Distill.Level = ptr("II")
	_, record, err = e.Resolve(s, [2]Plan{plan("PUB41"), EmptyPlan()}, nil)
	if err != nil || casts(record)[0].Data["passiveLikes"] != int64(0) {
		t.Fatal("distillation inherited basic harness gain", err)
	}
}

func TestPriorBalanceKeepsHostileAttemptVersionAndNoHarnessLike(t *testing.T) {
	e, err := NewPriorBalance("standard")
	if err != nil {
		t.Fatal(err)
	}
	s, err := e.Create([2]Selection{{Role: "Claude", Harness: ptr("H02"), Skills: []string{"CLA01", "CLA23"}}, {Role: "ChatGPT", Skills: []string{"PUB01"}}})
	if err != nil {
		t.Fatal(err)
	}
	next, record, err := e.Resolve(s, [2]Plan{plan("CLA23"), EmptyPlan()}, nil)
	if err != nil || next.Players[0].Likes != 0 {
		t.Fatal("prior score reinterpreted", next.Players[0].Likes, err)
	}
	attempts := 0
	for _, event := range record.Events {
		if event.Kind == "harness-like" {
			t.Fatal("new reward added to prior rules")
		}
		if event.Kind == "effect-attempt" {
			attempts++
			if event.Data["rules_version"] != 2 {
				t.Fatal("old effect version changed", event.Data)
			}
		}
	}
	if attempts != 3 {
		t.Fatal("old attempt sequence changed", attempts)
	}
}
