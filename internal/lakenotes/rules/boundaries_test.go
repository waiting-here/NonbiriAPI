package rules

import (
	"bytes"
	"encoding/json"
	"math"
	"os"
	"reflect"
	"testing"
)

func TestOriginalActionConformance(t *testing.T) {
	raw, e := os.ReadFile("testdata/actions.json")
	if e != nil {
		t.Fatal(e)
	}
	var fixture struct {
		Actions []struct {
			Name         string
			Profile      Profile
			Action       Action
			FinalProfile Profile
			CoinDelta    string
		}
	}
	if e = json.Unmarshal(raw, &fixture); e != nil {
		t.Fatal(e)
	}
	if len(fixture.Actions) != 89 {
		t.Fatal("action fixture count")
	}
	for _, v := range fixture.Actions {
		t.Run(v.Name, func(t *testing.T) {
			r, e := ApplyAction(v.Profile, v.Action)
			if e != nil {
				t.Fatal(e)
			}
			if !reflect.DeepEqual(r.Profile, v.FinalProfile) || r.CoinDelta != v.CoinDelta {
				t.Fatalf("action differs: got %+v want %+v", r, v.FinalProfile)
			}
		})
	}
}

func TestAmountBoundaries(t *testing.T) {
	maximum := "340282366920938463463374607431768211455"
	for _, s := range []string{"", "00", "01", "-1", "+1", "1.0", "1e3", " 1", "340282366920938463463374607431768211456"} {
		if _, e := ParseAmount(s); e == nil {
			t.Fatalf("accepted %q", s)
		}
	}
	m, e := ParseAmount(maximum)
	if e != nil {
		t.Fatal(e)
	}
	if _, e = m.Add("1"); e != ErrOverflow {
		t.Fatal(e)
	}
	if _, e = Amount("0").Sub("1"); e != ErrFunds {
		t.Fatal(e)
	}
	p := InitialProfile()
	p.Coins = "100000001"
	if e := ValidateProfile(p); e != nil {
		t.Fatal("large assets must not be clipped", e)
	}
	p.DebrisStock["line"] = 9999
	r, e := ApplyAction(p, Action{Name: "sell_all_debris"})
	if e != nil || r.Profile.Coins != "100029998" {
		t.Fatal(r, e)
	}
	p.Coins = m
	before := CloneProfile(p)
	if _, e = ApplyAction(p, Action{Name: "sell_all_debris"}); e != ErrOverflow {
		t.Fatal(e)
	}
	if !reflect.DeepEqual(p, before) {
		t.Fatal("rejected overflow changed input")
	}
}

func TestInitialProfileAndActionRejection(t *testing.T) {
	p := InitialProfile()
	if e := ValidateProfile(p); e != nil {
		t.Fatal(e)
	}
	if p.ClockMinutes != 360 || p.Day != 1 || p.Caught() != "0" || p.Streak != "0" || p.Coins != "0" || p.XP != "0" || p.NextCatchID != 1 || len(p.OwnedGear) != 1 || len(p.Contracts) != 0 {
		t.Fatal(p)
	}
	index := 3
	locked := true
	for _, a := range []Action{{Name: "buy_gear", ID: "unknown"}, {Name: "buy_gear", ID: "bambooPole"}, {Name: "rest", ID: "lake"}, {Name: "respec", Locked: &locked}, {Name: "save_gear_loadout", Index: &index}, {Name: "choose_skill", ID: "tracker"}, {Name: "select_bait", ID: "basic"}, {Name: "sell_fish", FishIDs: []uint64{1, 1}}, {Name: "set_fish_lock", FishIDs: []uint64{1}}, {Name: "claim_contract", ID: "1-perfect"}, {Name: "unsupported"}} {
		before := CloneProfile(p)
		if _, e := ApplyAction(p, a); e == nil {
			t.Fatalf("accepted %#v", a)
		}
		if !reflect.DeepEqual(p, before) {
			t.Fatal("failed action mutated input")
		}
	}
}

func TestContractBoardRetentionAndLimits(t *testing.T) {
	p := InitialProfile()
	p.EnsureContractBoard()
	if len(p.Contracts) != 5 || p.Contracts[0].Kind != "gold" || p.Contracts[1].Kind != "lake_carp" || p.Contracts[2].Location != "coast" {
		t.Fatal(p.Contracts)
	}
	for _, id := range []string{"1-delivery1", "1-delivery2", "1-perfect"} {
		r, e := ApplyAction(p, Action{Name: "accept_contract", ID: id})
		if e != nil {
			t.Fatal(e)
		}
		p = r.Profile
	}
	if _, e := ApplyAction(p, Action{Name: "accept_contract", ID: "1-cleanup"}); e == nil {
		t.Fatal("fourth active contract accepted")
	}
	p.Day = 2
	p.EnsureContractBoard()
	if len(p.Contracts) != 8 {
		t.Fatal(p.Contracts)
	}
	r, e := ApplyAction(p, Action{Name: "cancel_contract", ID: "1-perfect"})
	if e != nil || len(r.Profile.Contracts) != 7 {
		t.Fatal(r, e)
	}
	p = r.Profile
	p.Day = 3
	p.EnsureContractBoard()
	if len(p.Contracts) != 7 {
		t.Fatal("expired inactive board retained")
	}
}

func TestSkillsAndFreeGearUnlocks(t *testing.T) {
	p := InitialProfile()
	p.XP = "15000"
	p.Coins = "3000"
	for _, id := range []string{"steady", "control", "reader", "master"} {
		r, e := ApplyAction(p, Action{Name: "choose_skill", ID: id})
		if e != nil {
			t.Fatal(e)
		}
		p = r.Profile
	}
	if _, e := ApplyAction(p, Action{Name: "choose_skill", ID: "tracker"}); e == nil {
		t.Fatal("overwrote chosen skill")
	}
	r, e := ApplyAction(p, Action{Name: "respec"})
	if e != nil || r.Profile.Coins != "1000" || r.Profile.XP != "15000" || r.Profile.First != "" {
		t.Fatal(r, e)
	}
	p = InitialProfile()
	p.Records["gold"] = Record{Caught: "3", MaxLength: 20, BestQuality: 0, PerfectCount: "3"}
	if !p.UnlockReady("qualityBobber", 1) || p.UnlockReady("qualityBobber", 2) {
		t.Fatal("perfect unlock tiers")
	}
	p.Records["star"] = Record{Caught: "1", MaxLength: 100, BestQuality: 0, PerfectCount: "0"}
	if p.UnlockReady("curiosityLure", 1) {
		t.Fatal("changed original abyss-specific unlock")
	}
	p.Records["abyss"] = Record{Caught: "2", MaxLength: 150, BestQuality: 0, PerfectCount: "0"}
	if !p.UnlockReady("curiosityLure", 2) {
		t.Fatal("second curiosity copy")
	}
}

func TestRandomAndWaitBoundaries(t *testing.T) {
	r := CryptoRandom{Reader: bytes.NewReader(bytes.Repeat([]byte{255}, 8))}
	v, e := r.Float53()
	if e != nil || v != 1-math.Pow(2, -53) {
		t.Fatal(v, e)
	}
	for _, bad := range []float64{-1, 1, math.NaN(), math.Inf(1), 0.1} {
		if _, e := draw(&sequenceRandom{Values: []float64{bad}}); e == nil {
			t.Fatal("invalid private random", bad)
		}
	}
	p := InitialProfile()
	for _, sample := range []float64{0, 1 - math.Pow(2, -53)} {
		q, c, e := Start(p, &sequenceRandom{Values: []float64{sample, 0, 0}}, 1)
		if e != nil {
			t.Fatal(e)
		}
		if q.ClockMinutes != 360 || q.ContractsDay != 0 || q.Day != 1 || c.Plan.BiteTick < 7 || c.Plan.BiteTick > 338 {
			t.Fatal("preview committed side effects", q, c)
		}
		if c.Plan.Length != 17 || c.Plan.Challenge.Difficulty != 16 {
			t.Fatal(c.Plan)
		}
	}
	if _, _, e := Start(p, &sequenceRandom{Values: []float64{0}}, 0); e == nil {
		t.Fatal("zero motion seed")
	}
	p.OwnedGear = append(p.OwnedGear, "trainingRod")
	p.Equipped.Rod = "trainingRod"
	rng := &sequenceRandom{Values: []float64{0, 0}}
	_, c, e := Start(p, rng, 1)
	if e != nil || rng.Index != 2 || c.Plan.SizeFactor != 0 {
		t.Fatal("training rod draw order", rng, c, e)
	}
}

func TestNonfiniteAndRecoveryBoundaries(t *testing.T) {
	p := InitialProfile()
	_, c, e := Start(p, &sequenceRandom{Values: []float64{0, 0, 0}}, 1)
	if e != nil {
		t.Fatal(e)
	}
	for _, bad := range []float64{math.NaN(), math.Inf(1), math.Inf(-1)} {
		q := CloneProfile(p)
		q.ClockMinutes = bad
		if e := ValidateProfile(q); e == nil {
			t.Fatal("nonfinite clock")
		}
		next := cloneCast(c)
		next.BarVelocity = bad
		if e := ValidateCast(next); e == nil {
			t.Fatal("nonfinite cast")
		}
	}
	c.Paused = true
	if _, _, e := Advance(p, c, []bool{true}); e != ErrBusy {
		t.Fatal(e)
	}
	c.Paused = false
	q, next, e := Advance(p, c, make([]bool, 120))
	if e != nil {
		t.Fatal(e)
	}
	encoded, e := EncodeCast(next)
	if e != nil {
		t.Fatal(e)
	}
	restored, e := DecodeCast(encoded)
	if e != nil || StateHash(q, restored) != StateHash(q, next) {
		t.Fatal("bits differ after restore", e)
	}
	a, ca, e := Advance(q, next, make([]bool, 120))
	if e != nil {
		t.Fatal(e)
	}
	b, cb, e := Advance(q, restored, make([]bool, 120))
	if e != nil || !reflect.DeepEqual(a, b) || !reflect.DeepEqual(ca, cb) {
		t.Fatal("restoration changes replay", e)
	}
	if _, _, e = Advance(q, next, make([]bool, 121)); e == nil {
		t.Fatal("oversized tick segment")
	}
}

func TestRoundingAndQualityBoundaries(t *testing.T) {
	if round(0.5) != 1 || round(-0.5) != 0 || round(-1.5) != -1 {
		t.Fatal("JS rounding")
	}
	f, _ := Fish("gold")
	if CatchValue(Catch{Kind: f.Kind, Quality: 1}) != 27 || CatchValue(Catch{Kind: f.Kind, Quality: 3}) != 44 {
		t.Fatal("price rounding")
	}
	for _, level := range []int{0, 1, 5, 10, 15, 20} {
		if LevelFromXP(NewAmount(XPForLevel(level))) != level {
			t.Fatal(level)
		}
	}
	p := InitialProfile()
	p.BaitStock["basic"] = 998
	added, converted, e := p.AwardBait("basic", 5)
	if e != nil || added != 1 || converted != 20 || p.Coins != "20" {
		t.Fatal(added, converted, e)
	}
}

func TestProfileClockBitsAndPerfectEdges(t *testing.T) {
	p := InitialProfile()
	p.ClockMinutes = math.Nextafter(540, 0)
	raw, e := EncodeProfile(p)
	if e != nil {
		t.Fatal(e)
	}
	q, e := DecodeProfile(raw)
	if e != nil || math.Float64bits(p.ClockMinutes) != math.Float64bits(q.ClockMinutes) {
		t.Fatal("profile clock bits", e)
	}
	for _, test := range []struct {
		size, accuracy, miss float64
		perfect              bool
		quality              int
	}{{math.Nextafter(0.33, 0), 0.85, 0.6, true, 0}, {0.33, 0.85, 0.6, true, 2}, {0.66, 0.85, 0.6, true, 3}, {0.66, math.Nextafter(0.85, 0), 0.6, false, 2}, {0.66, 0.85, math.Nextafter(0.6, 1), false, 2}} {
		p := InitialProfile()
		_, c, e := Start(p, &sequenceRandom{Values: []float64{0, 0, 0}}, 1)
		if e != nil {
			t.Fatal(e)
		}
		c.Phase = "playing"
		c.Plan.SizeFactor = test.size
		c.EffectiveTime = 1
		c.HitTime = test.accuracy
		c.LongestMissTime = test.miss
		f := InitialFish(c.Plan.Challenge)
		c.Fish = &f
		if e := finish(&p, &c, true); e != nil {
			t.Fatal(e)
		}
		if c.Result.Perfect != test.perfect || c.Result.Quality != test.quality {
			t.Fatal(test, c.Result)
		}
	}
}

func TestRolloverPreservesActiveAndLazyBoard(t *testing.T) {
	p := InitialProfile()
	p.ClockMinutes = 1439.99
	p.EnsureContractBoard()
	r, e := ApplyAction(p, Action{Name: "accept_contract", ID: "1-perfect"})
	if e != nil {
		t.Fatal(e)
	}
	p = r.Profile
	_, c, e := Start(p, &sequenceRandom{Values: []float64{0, 0, 0}}, 1)
	if e != nil {
		t.Fatal(e)
	}
	p, c, e = Advance(p, c, []bool{false})
	if e != nil {
		t.Fatal(e)
	}
	if p.Day != 2 || len(p.Contracts) != 1 || p.Contracts[0].ID != "1-perfect" || p.Contracts[0].Status != "active" || p.ContractsDay != 1 {
		t.Fatal("rollover changed active quest or eagerly generated board", p)
	}
	p.EnsureContractBoard()
	if len(p.Contracts) != 6 || p.ContractsDay != 2 {
		t.Fatal(p.Contracts)
	}
}

func TestPrivateRewardRoundedUpperEndpoints(t *testing.T) {
	maximum := 1 - math.Pow(2, -53)
	for _, test := range []struct{ a, b, want float64 }{{25, 81, 81}, {2, 5, 5}, {0.15, 0.85, math.Nextafter(0.85, 0)}} {
		v, e := randomRange(&sequenceRandom{Values: []float64{maximum}}, test.a, test.b)
		if e != nil || v != test.want {
			t.Fatal(test, v, e)
		}
	}
	p := InitialProfile()
	p.Records["gold"] = Record{Caught: "1", MaxLength: 20, BestQuality: 0, PerfectCount: "0"}
	_, c, e := Start(p, &sequenceRandom{Values: []float64{0, 0.5, 0, 0, 0, maximum}}, 1)
	if e != nil || c.Plan.TreasureY == nil || *c.Plan.TreasureY != math.Nextafter(0.85, 0) {
		t.Fatal(c.Plan, e)
	}
	if e := ValidateCast(c); e != nil {
		t.Fatal("original rounded treasure endpoint rejected", e)
	}
}

func TestContractClaimAcrossDays(t *testing.T) {
	for _, slot := range []string{"cleanup", "delivery1"} {
		for _, nextDay := range []bool{false, true} {
			for _, currentBoard := range []bool{false, true} {
				t.Run(slot+"/"+map[bool]string{false: "same-day", true: "next-day"}[nextDay]+"/"+map[bool]string{false: "lazy", true: "board"}[currentBoard], func(t *testing.T) {
					p := InitialProfile()
					p.EnsureContractBoard()
					id := "1-" + slot
					r, e := ApplyAction(p, Action{Name: "accept_contract", ID: id})
					if e != nil {
						t.Fatal(e)
					}
					p = r.Profile
					if nextDay {
						if e = p.AdvanceClock(1440); e != nil {
							t.Fatal(e)
						}
					}
					if currentBoard {
						p.EnsureContractBoard()
					}
					if slot == "cleanup" {
						for range 4 {
							p.advanceContracts("cleanup", FishType{}, false)
						}
					} else {
						fish, _ := Fish("gold")
						for range 3 {
							if e = p.StoreCatch(fish, 20, 0, false); e != nil {
								t.Fatal(e)
							}
						}
					}
					before := CloneProfile(p)
					if e = ValidateProfile(p); e != nil {
						t.Fatal("earned quest input invalid", e)
					}
					r, e = ApplyAction(p, Action{Name: "claim_contract", ID: id})
					if e != nil {
						t.Fatal("earned quest could not be claimed", e)
					}
					if !reflect.DeepEqual(p, before) {
						t.Fatal("claim mutated its input")
					}
					wantCoins, wantBait := Amount("140"), 5
					if slot == "delivery1" {
						wantCoins, wantBait = "119", 3
						if len(r.Profile.Basket) != 0 {
							t.Fatal("delivery fish were not consumed")
						}
					}
					if r.Profile.Coins != wantCoins || r.CoinDelta != string(wantCoins) || r.Profile.BaitStock["basic"] != wantBait || r.Profile.CompletedContracts != "1" || r.Profile.ContractsDay != before.ContractsDay {
						t.Fatal("claim reward or lazy board changed", r)
					}
					wantLength := len(before.Contracts)
					if nextDay {
						wantLength--
					}
					if len(r.Profile.Contracts) != wantLength {
						t.Fatal("claim changed unrelated board entries")
					}
					for _, q := range r.Profile.Contracts {
						if q.ID == id && (nextDay || q.Status != "completed") {
							t.Fatal("incorrect completed quest retention", q)
						}
					}
					if e = ValidateProfile(r.Profile); e != nil {
						t.Fatal("claim returned invalid profile", e)
					}
					claimed := CloneProfile(r.Profile)
					if _, e = ApplyAction(r.Profile, Action{Name: "claim_contract", ID: id}); e != ErrInvalid || !reflect.DeepEqual(r.Profile, claimed) {
						t.Fatal("repeated claim rewarded or mutated state", e)
					}
					p.Coins = "340282366920938463463374607431768211455"
					before = CloneProfile(p)
					if _, e = ApplyAction(p, Action{Name: "claim_contract", ID: id}); e != ErrOverflow || !reflect.DeepEqual(p, before) {
						t.Fatal("failed claim mutated assets or quest", e)
					}
				})
			}
		}
	}
}
