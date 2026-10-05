package engine

import (
	"slices"
	"testing"
)

func TestSqueezePreservesLockedBalanceUntilCleansed(t *testing.T) {
	e, s := fixture(t, "quick")
	p := &s.Players[1]
	p.Burst = 300
	run := roundRun{e: e, s: &s, record: &RoundRecord{}}
	run.materialize(Grant{BuffID: "B38:订阅挤兑", Owner: 1, Amount: ptr(int64(2))}, 1)
	if p.Burst != 220 || p.BurstLocked != 80 || p.BurstLockedCap != 180 || p.BurstCap != 400 {
		t.Fatal(*p)
	}
	quote := e.usage(&s, 1, Choice{SkillID: "PUB01"}, false)
	e.pay(&s, 1, Action{Choice: Choice{SkillID: "PUB01"}, Preview: quote})
	if p.Burst != 120 || p.BurstLocked != 80 || p.Sub != 700 {
		t.Fatal("locked balance spent", *p)
	}
	used := []string{}
	if _, _, err := e.shop(&s, 1, Purchase{Item: "cleanse", Target: "B38:订阅挤兑"}, &used); err != nil {
		t.Fatal(err)
	}
	if p.Burst != 200 || p.BurstLocked != 0 || p.BurstLockedCap != 0 {
		t.Fatal("cleanse lost balance", *p)
	}
	if err := e.Validate(s); err != nil {
		t.Fatal(err)
	}
}

func TestSqueezeStacksRefreshesAndUpgrade(t *testing.T) {
	e, s := fixture(t, "quick")
	p := &s.Players[1]
	run := roundRun{e: e, s: &s, record: &RoundRecord{}}
	for range 8 {
		run.materialize(Grant{BuffID: "B38:订阅挤兑", Owner: 1, Amount: ptr(int64(2))}, 1)
	}
	st := &p.Effects[slices.IndexFunc(p.Effects, func(st Status) bool { return st.Kind == "SUBSCRIPTION_SQUEEZE" })]
	if st.Layers != 16 || p.Burst != 0 || p.BurstLocked != p.BurstCap {
		t.Fatal("stacking capped", *p)
	}
	e.upgradeSubscription(p)
	if p.Burst != 0 || p.BurstLocked != p.BurstCap {
		t.Fatal("upgrade bypassed lock", *p)
	}
	p.BurstLocked = 20
	p.Subscription.BurstResetAt = ptr(p.NormalTurns)
	e.resetSubscription(p)
	if p.Burst != 0 || p.BurstLocked != p.BurstCap || st.Layers != 16 {
		t.Fatal("burst refresh bypassed lock", *p)
	}
	p.Subscription.TotalResetAt = ptr(p.NormalTurns)
	e.resetSubscription(p)
	if p.Burst != p.BurstCap || p.BurstLocked != 0 || hasStatus(*p, "SUBSCRIPTION_SQUEEZE") {
		t.Fatal("total refresh retained lock", *p)
	}
	if err := e.Validate(s); err != nil {
		t.Fatal(err)
	}
}

func TestSqueezeCastVariantsAndResistance(t *testing.T) {
	for _, level := range []string{"base", "I", "II"} {
		e, s := fixture(t, "standard", Selection{Role: "DeepSeek", Skills: []string{"DS01", "DS41", "PUB41"}}, Selection{Role: "GLM", Skills: []string{"GLM01"}})
		id := "DS41"
		expected := int64(2)
		if level != "base" {
			id = "PUB41"
			s.Players[0].Distill.Template = ptr("DS41")
			s.Players[0].Distill.Level = ptr(level)
		}
		if level == "I" {
			expected = 1
		}
		s.Players[0].API = 100000
		next, _, err := e.Resolve(s, [2]Plan{plan(id), EmptyPlan()}, nil)
		if err != nil {
			t.Fatal(level, err)
		}
		if next.Players[1].BurstLockedCap != expected*90 {
			t.Fatal(level, next.Players[1])
		}
		// A leading DeepSeek has 50 hit; a trailing GLM has 50 resistance.
		// A trailing DeepSeek against a leading GLM has equal 25 values too.
		// Use Claude's distilled version to exercise a genuine resisted layer.
		e, s = fixture(t, "standard", Selection{Role: "Claude", Skills: []string{"CLA01", "PUB41"}}, Selection{Role: "GLM", Skills: []string{"GLM01"}})
		s.Players[0].Distill.Template = ptr("DS41")
		s.Players[0].Distill.Level = ptr(level)
		if level == "base" {
			s.Players[0].Distill.Level = ptr("II")
		}
		s.Players[0].API = 100000
		next, _, err = e.Resolve(s, [2]Plan{plan("PUB41"), EmptyPlan()}, func(n int) (int, error) { return n - 1, nil })
		if err != nil || hasStatus(next.Players[1], "SUBSCRIPTION_SQUEEZE") || next.Players[1].BurstLocked != 0 {
			t.Fatal("resisted lock applied", level, err)
		}
	}
}

func TestPressureUsesPreCastDebuffLayersForBothSeats(t *testing.T) {
	for leader := -1; leader < 2; leader++ {
		e, s := fixture(t, "standard", Selection{Role: "Claude", Skills: []string{"CLA01", "CLA23"}}, Selection{Role: "Claude", Skills: []string{"CLA01", "CLA23"}})
		initialBuff(e, &s, 0, "B38:订阅挤兑", 2)
		initialBuff(e, &s, 1, "B38:订阅挤兑", 3)
		initialBuff(e, &s, 1, "B18:原版", 1)
		if leader >= 0 {
			s.Players[leader].Likes = 1
			s.LikesAtStart[leader] = 1
		}
		_, rec, err := e.Resolve(s, [2]Plan{plan("CLA01"), plan("CLA01")}, nil)
		if err != nil {
			t.Fatal(err)
		}
		for _, event := range casts(rec) {
			seat := *event.Seat
			want := []int64{4, 2}[seat]
			if seat == leader {
				want *= 2
			}
			if characterPart(event) != want {
				t.Fatal("incorrect layer snapshot", leader, seat, characterPart(event), want)
			}
		}
		_, rec, err = e.Resolve(s, [2]Plan{plan("CLA23"), EmptyPlan()}, nil)
		if err != nil || characterPart(casts(rec)[0]) != 0 {
			t.Fatal("zero base triggered pressure", err)
		}
	}
}

func TestTripleSpeedIncludesImagesAndHistoricalFees(t *testing.T) {
	e, s := fixture(t, "standard", Selection{Role: "ChatGPT", Skills: []string{"PUB01", "GPT22"}})
	initialBuff(e, &s, 0, "B34:状态", 1)
	s.Players[0].Resources["R_IMAGE"] = 2
	v := e.usage(&s, 0, Choice{SkillID: "GPT22"}, false)
	if v.ResourceCosts["R_IMAGE"] != 3 || !slices.Contains(v.Shortages, "image") {
		t.Fatal(v)
	}
	s.Players[0].Resources["R_IMAGE"] = 3
	s.Players[0].ResourceCaps["R_IMAGE"] = 3
	s.Players[0].API = 100000
	s.Players[0].Burst = 0
	s.Energy = e.param("ENERGY_CAP")
	next, _, err := e.Resolve(s, [2]Plan{plan("GPT22"), EmptyPlan()}, nil)
	if err != nil || next.Players[0].Resources["R_IMAGE"] != 0 || next.Players[0].Likes != e.skills["GPT22"].Effects.Base.Likes*2 {
		t.Fatal(next.Players[0], err)
	}
	old, err := NewBalanceV3("standard")
	if err != nil {
		t.Fatal(err)
	}
	if old.speedCost(3, old.speed(&s, 0)) != 8 || old.usage(&s, 0, Choice{SkillID: "GPT22"}, false).ResourceCosts["R_IMAGE"] != 1 || old.behaviorVersion() != 3 {
		t.Fatal("historical costs changed")
	}
}
