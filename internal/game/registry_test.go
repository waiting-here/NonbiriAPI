package game

import (
	"errors"
	"testing"

	"github.com/waiting-here/NonbiriAPI/internal/game/fishing"
)

func TestRegistryIsClosed(t *testing.T) {
	module, err := Resolve(FishingID, FishingVersion)
	if err != nil || module.ID != FishingID || module.Version != FishingVersion {
		t.Fatalf("fishing module = %#v, %v", module, err)
	}
	for _, candidate := range []Module{{ID: "unknown", Version: 1}, {ID: FishingID, Version: 2}, {ID: "", Version: 0}} {
		if _, err := Resolve(candidate.ID, candidate.Version); !errors.Is(err, ErrUnknownGame) {
			t.Fatalf("resolve %#v = %v", candidate, err)
		}
	}
}

func TestCompileConfigDefaultsAndFailClosedSwitches(t *testing.T) {
	snapshot, err := CompileConfig(nil)
	if err != nil {
		t.Fatal(err)
	}
	if snapshot.GamesEnabled || snapshot.FishingEnabled || snapshot.Rules == nil {
		t.Fatalf("default snapshot = %#v", snapshot)
	}
	evidence, err := snapshot.Rules.Evidence(fishing.BaitWorm)
	if err != nil || evidence.EntryMilli != "2500000" || evidence.TargetRTP != "9/10" {
		t.Fatalf("default worm = %#v, %v", evidence, err)
	}

	snapshot, err = CompileConfig(map[string]string{GamesEnabledKey: "1", FishingEnabledKey: "1"})
	if err != nil || !snapshot.GamesEnabled || !snapshot.FishingEnabled {
		t.Fatalf("enabled snapshot = %#v, %v", snapshot, err)
	}
	for _, malformed := range []string{"true", "false", " 1", "", "2"} {
		snapshot, err = CompileConfig(map[string]string{GamesEnabledKey: malformed, FishingEnabledKey: malformed})
		if err != nil || snapshot.GamesEnabled || snapshot.FishingEnabled {
			t.Fatalf("malformed switch %q = %#v, %v", malformed, snapshot, err)
		}
	}
}

func TestCompileConfigRejectsWholeMalformedEconomy(t *testing.T) {
	cases := []map[string]string{
		{FishingWormPriceMilliKey: "02500000"},
		{FishingStandardRTPKey: "90.0"},
		{FishingPremiumRTPKey: "101"},
		{FishingTreasureBottleMultiplierKey: "0"},
		{FishingTreasureShellMultiplierKey: "1001"},
	}
	for _, raw := range cases {
		if _, err := CompileConfig(raw); !errors.Is(err, ErrInvalidConfig) {
			t.Fatalf("raw %#v = %v", raw, err)
		}
	}
}

func TestSiteConfigKeysReturnsCopy(t *testing.T) {
	first := SiteConfigKeys()
	second := SiteConfigKeys()
	if len(first) != 10 || len(second) != 10 {
		t.Fatalf("key lengths = %d, %d", len(first), len(second))
	}
	first[0] = "mutated"
	if second[0] != GamesEnabledKey || SiteConfigKeys()[0] != GamesEnabledKey {
		t.Fatal("configuration key registry leaked mutable storage")
	}
}
