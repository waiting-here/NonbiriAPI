package config

import (
	"encoding/json"
	"errors"
	"reflect"
	"testing"

	"github.com/waiting-here/NonbiriAPI/internal/game"
)

func TestDefaultConfigurationAndProjectionIsolation(t *testing.T) {
	codec := Codec{}
	value, err := codec.Compile(nil)
	if err != nil || value.Enabled() || value.NeedsReady() {
		t.Fatalf("default enabled or invalid: %v", err)
	}
	var wire Wire
	if err := json.Unmarshal(value.Wire(), &wire); err != nil {
		t.Fatal(err)
	}
	for mode, ticket := range map[string]string{"tier1": "5000", "tier2": "10000", "tier3": "50000"} {
		item := wire.Modes[mode]
		if item.Enabled || item.Ticket != ticket || item.RakeBP != (RakeBP{100, 100, 100}) {
			t.Fatalf("bad defaults: %+v", item)
		}
	}
	if len(codec.Keys()) != 16 || len(value.Raw()) != 16 {
		t.Fatal("missing default keys")
	}
	raw := value.Raw()
	raw[EnabledKey] = "1"
	if value.Raw()[EnabledKey] != "0" {
		t.Fatal("raw projection mutated configuration")
	}
	again, err := codec.CompileWire(value.Wire(), false)
	if err != nil || !reflect.DeepEqual(again.Raw(), value.Raw()) {
		t.Fatalf("wire roundtrip failed: %v", err)
	}
	modes := Modes()
	modes[0] = "other"
	if Modes()[0] != "tier1" {
		t.Fatal("mode list shared mutable storage")
	}
}

func TestModeAndMasterDependencies(t *testing.T) {
	codec := Codec{}
	base, err := codec.Compile(nil)
	if err != nil {
		t.Fatal(err)
	}
	body := json.RawMessage(`{"enabled":true,"modes":{"tier2":{"enabled":true,"ticket":"0.001","rake_bp":{"platform":9999,"welfare":0,"thursday":0}}}}`)
	if _, err := codec.Merge(base, body, false); !errors.Is(err, game.ErrInvalidConfig) {
		t.Fatal("enabled under a disabled master")
	}
	next, err := codec.Merge(base, body, true)
	if err != nil || !next.Enabled() || !next.NeedsReady() {
		t.Fatalf("valid enabled mode failed: %v", err)
	}
	if base.Enabled() || base.Raw()[TicketKey("tier2")] != "10000000" {
		t.Fatal("merge mutated the previous configuration")
	}
	if _, err := codec.Merge(next, json.RawMessage(`{"enabled":false}`), true); !errors.Is(err, game.ErrInvalidConfig) {
		t.Fatal("enabled child under disabled game")
	}
	if _, err := codec.Merge(next, json.RawMessage(`{"enabled":false,"modes":{"tier2":{"enabled":false}}}`), false); err != nil {
		t.Fatalf("atomic disable failed: %v", err)
	}
	var user struct {
		Enabled       bool `json:"enabled"`
		Available     bool `json:"available"`
		QueueSeconds  int  `json:"queue_seconds"`
		JokerSeconds  int  `json:"joker_seconds"`
		BidSeconds    int  `json:"bid_seconds"`
		QueueCapacity int  `json:"queue_capacity"`
		Modes         map[string]struct {
			Enabled   bool `json:"enabled"`
			Available bool `json:"available"`
		} `json:"modes"`
	}
	if err := json.Unmarshal(next.UserWire(func(mode, _ string) bool { return mode == "tier2" }), &user); err != nil {
		t.Fatal(err)
	}
	if !user.Enabled || !user.Available || !user.Modes["tier2"].Enabled || user.Modes["tier1"].Enabled || user.QueueSeconds != 120 || user.JokerSeconds != 10 || user.BidSeconds != 20 || user.QueueCapacity != 4096 {
		t.Fatalf("invalid user projection: %+v", user)
	}
	if err := json.Unmarshal(next.UserWire(func(_, _ string) bool { return false }), &user); err != nil {
		t.Fatal(err)
	}
	if user.Enabled || user.Available || user.Modes["tier2"].Enabled || !next.Enabled() {
		t.Fatal("unavailable mode projected as playable or persisted")
	}
}

func TestRejectsInvalidPatchesAndAmounts(t *testing.T) {
	codec := Codec{}
	base, err := codec.Compile(nil)
	if err != nil {
		t.Fatal(err)
	}
	cases := []string{
		`{}`, `null`, `[]`, `{"enabled":null}`, `{"enabled":true,"enabled":false}`,
		`{"unknown":true}`, `{"queue_seconds":30}`, `{"modes":{}}`, `{"modes":{"tier4":{"enabled":true}}}`,
		`{"modes":{"tier1":null}}`, `{"modes":{"tier1":{"ticket":1}}}`,
		`{"modes":{"tier1":{"ticket":"0"}}}`, `{"modes":{"tier1":{"ticket":"-1"}}}`,
		`{"modes":{"tier1":{"ticket":"0.0001"}}}`, `{"modes":{"tier1":{"ticket":"9e12"}}}`,
		`{"modes":{"tier1":{"ticket":"9000000000000.001"}}}`,
		`{"modes":{"tier1":{"rake_bp":{"platform":10000}}}}`,
		`{"modes":{"tier1":{"rake_bp":{"platform":9900}}}}`,
		`{"modes":{"tier1":{"rake_bp":{"platform":-1}}}}`,
		`{"modes":{"tier1":{"rake_bp":{"platform":1.5}}}}`,
		`{"modes":{"tier1":{"rake_bp":{"platform":1,"platform":2}}}}`,
		`{"modes":{"tier1":{"bid_seconds":10}}}`, `{"modes":{"tier1":{"enabled":true}}}`,
	}
	for _, body := range cases {
		t.Run(body, func(t *testing.T) {
			if _, err := codec.Merge(base, json.RawMessage(body), true); !errors.Is(err, game.ErrInvalidConfig) {
				t.Fatalf("invalid patch accepted: %v", err)
			}
		})
	}
	for _, ticket := range []string{"0.001", "9000000000000"} {
		body := game.ConfigJSON(map[string]any{"modes": map[string]any{"tier1": map[string]any{"ticket": ticket}}})
		if _, err := codec.Merge(base, body, false); err != nil {
			t.Fatalf("boundary ticket %s: %v", ticket, err)
		}
	}
	if _, err := codec.Compile(map[string]string{TicketKey("tier1"): "9000000000000001"}); !errors.Is(err, game.ErrInvalidConfig) {
		t.Fatal("overflowing stored ticket accepted")
	}
	if _, err := codec.Merge(nil, json.RawMessage(`{"enabled":false}`), false); !errors.Is(err, game.ErrInvalidConfig) {
		t.Fatal("foreign snapshot accepted")
	}
}

func TestDescriptorIsIndependentAndClosed(t *testing.T) {
	registry := game.NewRegistry()
	descriptor := Descriptor()
	if err := registry.Register(descriptor); err != nil {
		t.Fatal(err)
	}
	if err := registry.Seal(); err != nil {
		t.Fatal(err)
	}
	if descriptor.ID != "bidding" || descriptor.Version != 1 || descriptor.StableOrder != 3 || len(descriptor.Onboarding) != 4 || len(descriptor.Routes) != 13 {
		t.Fatal("incorrect module contract")
	}
	for _, id := range []string{"bid_AAAAAAAAAAAAAAAAAAAAAA", "bidq_AAAAAAAAAAAAAAAAAAAAAA"} {
		if !descriptor.ValidResource(id) {
			t.Fatalf("valid resource rejected: %s", id)
		}
	}
	for _, id := range []string{"rps_AAAAAAAAAAAAAAAAAAAAAA", "bid_AA", "bidq_AAAAAAAAAAAAAAAAAAAAAAA"} {
		if descriptor.ValidResource(id) {
			t.Fatalf("invalid resource accepted: %s", id)
		}
	}
}
