package config

import (
	"encoding/json"
	"reflect"
	"testing"

	"github.com/waiting-here/NonbiriAPI/internal/game"
)

func TestConfigurationDefaultsStrictPatchAndTiming(t *testing.T) {
	codec := Codec{}
	base, err := codec.Compile(nil)
	if err != nil || base.Enabled() || base.NeedsReady() || len(base.Raw()) != 11 || len(codec.Keys()) != 11 {
		t.Fatalf("bad defaults: %v", err)
	}
	var wire Wire
	if err := json.Unmarshal(base.Wire(), &wire); err != nil {
		t.Fatal(err)
	}
	if wire.Modes["quick"].Ticket != "5000" || wire.Modes["standard"].Ticket != "25000" || len(wire.Modes) != 2 {
		t.Fatal("bad default tickets")
	}
	roundtrip, err := codec.CompileWire(base.Wire(), false)
	if err != nil || !reflect.DeepEqual(roundtrip.Raw(), base.Raw()) {
		t.Fatalf("bad wire roundtrip: %v", err)
	}
	for _, body := range []string{
		`{"modes":{"tier1":{"enabled":true}}}`, `{"modes":{"quick":{"plan_seconds":1}}}`, `{"settlement_seconds":0}`,
		`{"enabled":true,"enabled":false}`, `{"enabled":null}`, `{"modes":{"quick":{"ticket":"0"}}}`,
		`{"modes":{"quick":{"ticket":"9000000000000.001"}}}`, `{"modes":{"quick":{"rake_bp":{"platform":9900}}}}`,
		`{"modes":{"quick":{"enabled":true}}}`, `{"modes":{}}`,
	} {
		if _, err := codec.Merge(base, json.RawMessage(body), true); err == nil {
			t.Fatalf("invalid patch accepted: %s", body)
		}
	}
	next, err := codec.Merge(base, json.RawMessage(`{"enabled":true,"modes":{"quick":{"enabled":true,"ticket":"0.001"}}}`), true)
	if err != nil || !next.NeedsReady() || base.Enabled() {
		t.Fatalf("valid patch failed: %v", err)
	}
	if _, err := codec.Merge(base, json.RawMessage(`{"enabled":true}`), false); err == nil {
		t.Fatal("master disabled")
	}
	var public struct {
		PlanSeconds       int                 `json:"plan_seconds"`
		SettlementSeconds int                 `json:"settlement_seconds"`
		QueueSeconds      int                 `json:"queue_seconds"`
		Modes             map[string]WireMode `json:"modes"`
	}
	if err := json.Unmarshal(next.UserWire(func(mode, _ string) bool { return mode == "standard" }), &public); err != nil {
		t.Fatal(err)
	}
	if public.PlanSeconds != 20 || public.SettlementSeconds != 5 || public.QueueSeconds != 120 || public.Modes["quick"].Enabled {
		t.Fatal("bad availability or phase timing")
	}
	registry := game.NewRegistry()
	if err := registry.Register(Descriptor()); err != nil {
		t.Fatal(err)
	}
	if err := registry.Seal(); err != nil {
		t.Fatal(err)
	}
}
