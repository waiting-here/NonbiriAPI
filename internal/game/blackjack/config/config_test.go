package config

import (
	"encoding/json"
	"strconv"
	"testing"

	"github.com/waiting-here/NonbiriAPI/internal/game"
)

func TestDefaultStakesAndStrictConfiguration(t *testing.T) {
	s, err := CompileConfig(nil)
	if err != nil || s.Enabled || s.DefaultStake != 5_000_000 || s.Rake != (Rates{100, 100, 100}) {
		t.Fatalf("defaults: %+v %v", s, err)
	}
	for n := int64(1); n <= 50; n++ {
		if !s.Accepts(n * 1_000_000) {
			t.Fatal("missing default stake")
		}
	}
	for _, n := range []int64{0, 999_999, 1_000_001, 50_000_001, 51_000_000} {
		if s.Accepts(n) {
			t.Fatal("out-of-range stake")
		}
	}
	for _, raw := range []map[string]string{
		{EnabledKey: "true"}, {AmountKey("stake_step"): "0"}, {AmountKey("default_stake"): "1"},
		{AmountKey("max_stake"): strconv.FormatInt(MaxStakeMilli+1, 10)}, {RakeKey("platform"): "9800"},
	} {
		if _, err := CompileConfig(raw); err == nil {
			t.Fatalf("bad configuration: %v", raw)
		}
	}
	c := Codec{}
	current, err := c.Compile(nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, patch := range []string{`{"unknown":true}`, `{"rake_bp":{"unknown":1}}`, `{"min_stake":null}`, `{"enabled":true,"enabled":false}`} {
		if _, err := c.Merge(current, json.RawMessage(patch), true); err == nil {
			t.Fatalf("invalid patch: %s", patch)
		}
	}
	next, err := c.Merge(current, json.RawMessage(`{"enabled":true,"default_stake":"10000"}`), true)
	if err != nil || !next.Enabled() || !next.NeedsReady() || next.Raw()[AmountKey("default_stake")] != "10000000" {
		t.Fatalf("merge: %v", err)
	}
	registry := game.NewRegistry()
	if err := registry.Register(Descriptor()); err != nil {
		t.Fatal(err)
	}
	if err := registry.Seal(); err != nil {
		t.Fatal(err)
	}
}

func TestQuickStakesNormalizeAndValidateWithLimits(t *testing.T) {
	c := Codec{}
	current, err := c.Compile(nil)
	if err != nil {
		t.Fatal(err)
	}
	next, err := c.Merge(current, json.RawMessage(`{"quick_stakes":["10000","1000","5000"]}`), false)
	if err != nil || next.Raw()[QuickStakesKey] != `["1000","5000","10000"]` {
		t.Fatal(next, err)
	}
	for _, patch := range []string{`{"quick_stakes":null}`, `{"quick_stakes":"1000"}`, `{"quick_stakes":[null]}`, `{"quick_stakes":[1000]}`, `{"quick_stakes":["1000.0"]}`, `{"quick_stakes":["01000"]}`, `{"quick_stakes":[" 1000"]}`, `{"quick_stakes":["0"]}`, `{"quick_stakes":["51000"]}`, `{"quick_stakes":["1000","1000"]}`, `{"quick_stakes":["1001"]}`, `{"quick_stakes":["1000","2000","3000","4000","5000","6000","7000","8000","9000"]}`, `{"min_stake":"2000"}`} {
		if _, err := c.Merge(current, json.RawMessage(patch), false); err == nil {
			t.Fatal("invalid quick stakes accepted", patch)
		}
	}
	next, err = c.Merge(current, json.RawMessage(`{"min_stake":"2000","quick_stakes":[]}`), false)
	if err != nil || next.Raw()[QuickStakesKey] != "[]" {
		t.Fatal("empty quick stakes", err)
	}
	for _, values := range []string{`["5000"]`, `["1000","2000","3000","4000","5000","6000","7000","8000"]`} {
		next, err = c.Merge(current, json.RawMessage(`{"quick_stakes":`+values+`}`), false)
		if err != nil || next.Raw()[QuickStakesKey] != values {
			t.Fatal("valid quick stakes", values, err)
		}
	}
	next, err = c.Merge(current, json.RawMessage(`{"min_stake":"1.001","max_stake":"1.025","stake_step":"0.003","default_stake":"1.007","quick_stakes":["1.025","1.001"]}`), false)
	if err != nil || next.Raw()[QuickStakesKey] != `["1.001","1.025"]` {
		t.Fatal("minimum-anchored fractional steps", err)
	}
}
