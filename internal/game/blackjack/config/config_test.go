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
