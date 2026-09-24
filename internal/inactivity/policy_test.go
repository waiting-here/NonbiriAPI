package inactivity

import (
	"github.com/waiting-here/NonbiriAPI/internal/ledger"
	"testing"
)

func days(n int64) *int64 { return &n }
func testPolicy() Policy {
	return Policy{Enabled: true, Decay: DecayPolicy{Enabled: true, InactiveDays: days(30), IntervalDays: days(7), Assets: Assets{General: &AssetRule{Mode: "percent", Value: "1000", Floor: "100"}, Game: &AssetRule{Mode: "fixed", Value: "500", Floor: "0"}}}}
}
func TestChargeBoundariesAndPrecision(t *testing.T) {
	for _, test := range []struct {
		balance string
		rule    *AssetRule
		want    string
	}{
		{"12345", &AssetRule{"percent", "125", "0"}, "154"},
		{"1000", &AssetRule{"fixed", "900", "400"}, "600"},
		{"-200", &AssetRule{"fixed", "900", "0"}, "0"},
		{"30", &AssetRule{"percent", "10000", "100"}, "0"},
		{"170141183460469231731687303715884105727", &AssetRule{"percent", "10000", "0"}, "170141183460469231731687303715884105727"},
		{"15", nil, "0"},
	} {
		balance, err := ledger.ParseAmount(test.balance)
		if err != nil {
			t.Fatal(err)
		}
		got, err := charge(balance, test.rule)
		if err != nil || got.Decimal() != test.want {
			t.Fatalf("charge %s: %s %v", test.balance, got.Decimal(), err)
		}
	}
	for _, rule := range []AssetRule{{"percent", "10001", "0"}, {"fixed", "01", "0"}, {"fixed", "1", "-1"}, {"other", "1", "0"}} {
		p := testPolicy()
		p.Decay.Assets.General = &rule
		if Validate(p) == nil {
			t.Fatalf("accepted %+v", rule)
		}
	}
	if _, err := decodePolicy([]byte(`{"enabled":false,"enabled":true}`)); err == nil {
		t.Fatal("accepted duplicate fields")
	}
}

func TestFixedZeroRequiresAnEffectiveEnabledRule(t *testing.T) {
	p := testPolicy()
	p.Enabled = false
	p.Decay.Enabled = false
	p.Decay.Assets = Assets{General: &AssetRule{"fixed", "0", "0"}}
	if err := Validate(p); err != nil {
		t.Fatal("disabled fixed zero rejected", err)
	}
	p.Enabled = true
	p.Decay.Enabled = true
	if Validate(p) == nil {
		t.Fatal("enabled no-op accepted")
	}
	p.Decay.Assets.Game = &AssetRule{"fixed", "0", "0"}
	if Validate(p) == nil {
		t.Fatal("two enabled zero rules accepted")
	}
	p.Decay.Assets.Game.Value = "1"
	if err := Validate(p); err != nil {
		t.Fatal("effective rule with other fixed zero rejected", err)
	}
	p.Decay.Enabled = false
	p.Enabled = false
	p.Decay.Assets.General.Mode = "percent"
	if Validate(p) == nil {
		t.Fatal("disabled zero percent accepted")
	}
}
func TestIndependentGraceAndOneCatchupSchedule(t *testing.T) {
	p := testPolicy()
	now := int64(1800000000)
	d, b := grace(Configuration{}, p, now)
	if d != now+graceSeconds || b != 0 {
		t.Fatal(d, b)
	}
	old := Configuration{Policy: p, DecayGraceUntil: d}
	relaxed := testPolicy()
	relaxed.Decay.InactiveDays = days(60)
	relaxed.Decay.Assets.General.Value = "500"
	d, b = grace(old, relaxed, now+day)
	if d != old.DecayGraceUntil || b != 0 {
		t.Fatal("relaxation removed grace")
	}
	tighter := testPolicy()
	tighter.Decay.Assets.Game.Floor = "0"
	tighter.Decay.IntervalDays = days(1)
	d, _ = grace(old, tighter, now+day)
	if d != now+day+graceSeconds {
		t.Fatal("tightening did not extend grace")
	}
	old.Enabled = false
	d, _ = grace(old, p, now+20*day)
	if d != now+27*day {
		t.Fatal("re-enable missed grace")
	}
	c := Configuration{Policy: p}
	state := ActivityState{ObservationStartedAt: now - 200*day, LastDecayAt: &now}
	decay, _ := due(c, state)
	if decay == nil || *decay != now+7*day {
		t.Fatal("missed periods caught up together")
	}
}
