package config

import (
	"bytes"
	"encoding/base64"
	"errors"
	"math/rand"
	"reflect"
	"strings"
	"testing"

	"github.com/waiting-here/NonbiriAPI/internal/game"
	compat "github.com/waiting-here/NonbiriAPI/internal/game/compat"
	"github.com/waiting-here/NonbiriAPI/internal/game/fishing"
	fishingconfig "github.com/waiting-here/NonbiriAPI/internal/game/fishing/config"
	linklinkconfig "github.com/waiting-here/NonbiriAPI/internal/game/linklink/config"
	rpsconfig "github.com/waiting-here/NonbiriAPI/internal/game/rps/config"
)

func TestRegistryClosedCapabilities(t *testing.T) {
	want := map[string]bool{game.FishingID: true, game.LinkLinkID: true, game.RPSID: true}
	for _, module := range Modules() {
		want[module.ID] = false
		if _, err := Resolve(module.ID, module.Version); err != nil {
			t.Fatalf("resolve %s: %v", module.ID, err)
		}
	}
	for id, missing := range want {
		if missing {
			t.Fatalf("registry is missing %s", id)
		}
	}
	for _, candidate := range []game.ModuleDescriptor{{ID: "unknown", Version: 1}, {ID: game.FishingID, Version: 2}, {ID: "", Version: 0}} {
		if _, err := Resolve(candidate.ID, candidate.Version); !errors.Is(err, game.ErrUnknownGame) {
			t.Fatalf("resolve %#v = %v", candidate, err)
		}
	}
	for _, spec := range []string{game.LinkLinkSpec6x8, game.LinkLinkSpec8x8, game.LinkLinkSpec10x10} {
		if err := ResolveSpec(game.LinkLinkID, spec); err != nil {
			t.Fatal(err)
		}
	}
	if !errors.Is(ResolveSpec(game.LinkLinkID, "12x12"), game.ErrUnknownSpec) {
		t.Fatal("unknown LinkLink spec was accepted")
	}
	for _, mode := range []string{game.RPSModeQuick, game.RPSModeStandard, game.RPSModeDeathmatch} {
		if err := ResolveMode(game.RPSID, mode); err != nil {
			t.Fatal(err)
		}
	}
	if !errors.Is(ResolveMode(game.RPSID, "other"), game.ErrUnknownMode) {
		t.Fatal("unknown RPS mode was accepted")
	}

	modules := Modules()
	modules[1].Specs[0] = "mutated"
	again, _ := Resolve(game.LinkLinkID, game.LinkLinkVersion)
	if again.Specs[0] != game.LinkLinkSpec6x8 {
		t.Fatal("registry leaked mutable slice storage")
	}
}

func TestRuntimeContractsRejectCrossGameAndUnknownCapabilities(t *testing.T) {
	fishingID := testOpaqueID("fb_", 1)
	operationID := testOpaqueID("op_", 2)
	valid := []interface {
		Validate(game.ModuleDescriptor) error
	}{
		game.StartContract{Game: game.FishingID, Version: game.FishingVersion},
		game.StartContract{Game: game.LinkLinkID, Version: game.LinkLinkVersion, Spec: game.LinkLinkSpec8x8},
		game.StartContract{Game: game.RPSID, Version: game.RPSVersion, Mode: game.RPSModeQuick},
		game.TerminalContract{Game: game.FishingID, Version: game.FishingVersion, ResourceID: fishingID, OperationID: operationID},
		game.ContinuationContract{Game: game.FishingID, Version: game.FishingVersion, ResourceID: fishingID, DueAt: 253402300799},
		game.AggregateContract{Game: game.FishingID, Version: game.FishingVersion, Board: "total", UserID: 1},
		game.AggregateContract{Game: game.RPSID, Version: game.RPSVersion, Board: "net_profit", Mode: game.RPSModeStandard, UserID: 1},
	}
	for _, contract := range valid {
		if err := validateContract(contract); err != nil {
			t.Fatalf("valid contract %T rejected: %v", contract, err)
		}
	}

	invalid := []interface {
		Validate(game.ModuleDescriptor) error
	}{
		game.StartContract{Game: game.FishingID, Version: game.FishingVersion, Mode: game.RPSModeQuick},
		game.StartContract{Game: game.LinkLinkID, Version: game.LinkLinkVersion},
		game.StartContract{Game: game.RPSID, Version: game.RPSVersion, Mode: "other"},
		game.TerminalContract{Game: game.FishingID, Version: game.FishingVersion, ResourceID: testOpaqueID("rps_", 1), OperationID: operationID},
		game.ContinuationContract{Game: game.FishingID, Version: game.FishingVersion, ResourceID: fishingID, DueAt: -1},
		game.AggregateContract{Game: game.FishingID, Version: game.FishingVersion, Board: "net_profit", UserID: 1},
		game.AggregateContract{Game: game.LinkLinkID, Version: game.LinkLinkVersion, Board: "single", UserID: 1},
	}
	for _, contract := range invalid {
		if err := validateContract(contract); err == nil {
			t.Fatalf("invalid contract %T was accepted", contract)
		}
	}
}

func TestCompileConfigCompleteDefaultsAndStrictSwitches(t *testing.T) {
	snapshot, err := CompileConfig(nil)
	if err != nil {
		t.Fatal(err)
	}
	if snapshot.GamesEnabled || snapshot.FishingEnabled || snapshot.Rules == nil || len(snapshot.LinkLink.Specs) != 3 || len(snapshot.RPS.Modes) != 3 {
		t.Fatalf("default snapshot = %#v", snapshot)
	}
	evidence, err := snapshot.Rules.Evidence(fishing.BaitWorm)
	if err != nil || evidence.EntryMilli != "2500000" || evidence.TargetRTP != "1" {
		t.Fatalf("default fishing evidence = %#v, %v", evidence, err)
	}
	for _, malformed := range []string{"true", "false", " 1", "", "2"} {
		if _, err = CompileConfig(map[string]string{game.GamesEnabledKey: malformed}); !errors.Is(err, game.ErrInvalidConfig) {
			t.Fatalf("switch %q error = %v", malformed, err)
		}
	}
	if _, err = CompileConfig(map[string]string{fishingconfig.FishingEnabledKey: "1"}); !errors.Is(err, game.ErrInvalidConfig) {
		t.Fatalf("orphan fishing switch error = %v", err)
	}
}

func TestCompileConfigHostileBounds(t *testing.T) {
	cases := []map[string]string{
		{fishingconfig.FishingWormPriceMilliKey: "02500000"},
		{fishingconfig.FishingStandardRTPKey: "90.0"},
		{fishingconfig.FishingPremiumRTPKey: "101"},
		{fishingconfig.FishingTreasureBottleMultiplierKey: "0"},
		{fishingconfig.FishingTreasureShellMultiplierKey: "1001"},
		{linklinkconfig.LinkLinkSpecEnabledKey(game.LinkLinkSpec6x8): "1"},
		{rpsconfig.RPSModeBPKey(game.RPSModeQuick, "platform"): "9900", rpsconfig.RPSModeBPKey(game.RPSModeQuick, "welfare"): "100"},
		{rpsconfig.RPSEnabledKey: "1", game.GamesEnabledKey: "1", rpsconfig.RPSModeEnabledKey(game.RPSModeStandard): "1", rpsconfig.RPSModeBaseKey(game.RPSModeStandard): "1800000000000001"},
		{rpsconfig.RPSModeTimeKey(game.RPSModeQuick, "queue"): "29"},
		{rpsconfig.RPSModeTimeKey(game.RPSModeQuick, "gesture"): "21"},
		{rpsconfig.RPSModeTimeKey(game.RPSModeQuick, "dealer"): "4"},
	}
	for _, raw := range cases {
		if _, err := CompileConfig(raw); !errors.Is(err, game.ErrInvalidConfig) {
			t.Fatalf("raw %#v error = %v", raw, err)
		}
	}
}

func TestGamesConfigNestedPatchAndCanonicalAmounts(t *testing.T) {
	snapshot, err := CompileConfig(nil)
	if err != nil {
		t.Fatal(err)
	}
	current := snapshot.GamesConfig("1")
	patch, err := DecodeGamesConfigPatch([]byte(`{"expected_revision":"1","fishing":{"bait_prices":{"worm":"2.501"}}}`))
	if err != nil {
		t.Fatal(err)
	}
	merged, err := patch.Merge(current)
	if err != nil {
		t.Fatal(err)
	}
	compiled, raw, err := CompileGamesConfig(merged)
	if err != nil {
		t.Fatal(err)
	}
	if raw[fishingconfig.FishingWormPriceMilliKey] != "2501" || compiled.Fishing.BaitPricesMilli[fishing.BaitLure] != "5000000" {
		t.Fatalf("compiled raw values = %#v", raw)
	}
	if current.Fishing.BaitPrices.Worm == merged.Fishing.BaitPrices.Worm {
		t.Fatal("nested partial did not change its requested leaf")
	}
	if !reflect.DeepEqual(current.RPS, merged.RPS) || !reflect.DeepEqual(current.LinkLink, merged.LinkLink) {
		t.Fatal("nested partial changed an unrelated subtree")
	}

	hostile := []string{
		`{"expected_revision":"1"}`,
		`{"expected_revision":"1","fishing":{}}`,
		`{"expected_revision":"1","fishing":null}`,
		`{"expected_revision":"1","fishing":{"bait_prices":{"worm":"1.0"}}}`,
		`{"expected_revision":"1","fishing":{"bait_prices":{"worm":"1.230"}}}`,
		`{"expected_revision":"1","fishing":{"bait_prices":{"worm":"0.000"}}}`,
		`{"expected_revision":"1","rps":{"modes":{"quick":{"queue_capacity":4096}}}}`,
		`{"expected_revision":"1","fishing":{"enabled":true,"enabled":false}}`,
		`{"expected_revision":"1","linklink":{"specs":{"12x12":{"enabled":true}}}}`,
	}
	for _, body := range hostile {
		patch, decodeErr := DecodeGamesConfigPatch([]byte(body))
		if decodeErr == nil {
			var candidate compat.GamesConfig
			candidate, decodeErr = patch.Merge(current)
			if decodeErr == nil {
				_, _, decodeErr = CompileGamesConfig(candidate)
			}
		}
		if decodeErr == nil {
			t.Fatalf("hostile patch was accepted: %s", body)
		}
	}
	if _, err = (compat.GamesConfigPatch{ExpectedRevision: "2", MasterEnabled: boolPointer(true)}).Merge(current); !errors.Is(err, game.ErrRevisionConflict) {
		t.Fatalf("stale revision error = %v", err)
	}
}

func TestCanonicalWireAmountsRoundTrip(t *testing.T) {
	values := []int64{0, 1, 10, 999, 1000, 1001, 123456789, game.MaxMoneyMilli}
	rng := rand.New(rand.NewSource(42))
	for range 10_000 {
		values = append(values, rng.Int63n(game.MaxMoneyMilli+1))
	}
	for _, value := range values {
		wire := game.FormatAmount(value)
		parsed, err := game.ParseAmount(wire)
		if err != nil || parsed != value {
			t.Fatalf("amount %d -> %q -> %d, %v", value, wire, parsed, err)
		}
	}
	for _, hostile := range []string{"", "+1", "-1", "01", ".1", "1.", "1.0", "1.20", "0.000", "1.2345", "1e3", "9000000000000.001"} {
		if _, err := game.ParseAmount(hostile); !errors.Is(err, game.ErrInvalidConfig) {
			t.Fatalf("noncanonical amount %q error = %v", hostile, err)
		}
	}
}

func FuzzCanonicalWireAmount(f *testing.F) {
	for _, seed := range []string{"0", "1", "1.001", "9000000000000", "01", "1.0", "-0", "1e3"} {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, value string) {
		milli, err := game.ParseAmount(value)
		if err != nil {
			return
		}
		if milli < 0 || milli > game.MaxMoneyMilli || game.FormatAmount(milli) != value {
			t.Fatalf("accepted noncanonical amount %q as %d", value, milli)
		}
	})
}

func TestSiteConfigKeysReturnsCopy(t *testing.T) {
	first, second := SiteConfigKeys(), SiteConfigKeys()
	if len(first) != 48 || len(second) != 48 {
		t.Fatalf("key lengths = %d, %d", len(first), len(second))
	}
	first[0] = "mutated"
	if second[0] != game.GamesEnabledKey || SiteConfigKeys()[0] != game.GamesEnabledKey {
		t.Fatal("configuration keys leaked mutable storage")
	}
	joined := strings.Join(second, ",")
	for _, key := range []string{linklinkconfig.LinkLinkSpecPriceKey(game.LinkLinkSpec10x10), rpsconfig.RPSModeTimeKey(game.RPSModeDeathmatch, "follower")} {
		if !strings.Contains(joined, key) {
			t.Fatalf("missing game config key %s", key)
		}
	}
}

func testOpaqueID(prefix string, fill byte) string {
	return prefix + base64.RawURLEncoding.EncodeToString(bytes.Repeat([]byte{fill}, 16))
}

func boolPointer(value bool) *bool { return &value }
