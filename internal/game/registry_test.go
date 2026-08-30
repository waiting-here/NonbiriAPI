package game

import (
	"bytes"
	"encoding/base64"
	"errors"
	"math/rand"
	"reflect"
	"strings"
	"testing"

	"github.com/waiting-here/NonbiriAPI/internal/game/fishing"
)

func TestRegistryClosedCapabilities(t *testing.T) {
	want := map[string]bool{FishingID: true, LinkLinkID: true, RPSID: true}
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
	for _, candidate := range []Module{{ID: "unknown", Version: 1}, {ID: FishingID, Version: 2}, {ID: "", Version: 0}} {
		if _, err := Resolve(candidate.ID, candidate.Version); !errors.Is(err, ErrUnknownGame) {
			t.Fatalf("resolve %#v = %v", candidate, err)
		}
	}
	for _, spec := range []string{LinkLinkSpec6x8, LinkLinkSpec8x8, LinkLinkSpec10x10} {
		if err := ResolveSpec(LinkLinkID, spec); err != nil {
			t.Fatal(err)
		}
	}
	if !errors.Is(ResolveSpec(LinkLinkID, "12x12"), ErrUnknownSpec) {
		t.Fatal("unknown LinkLink spec was accepted")
	}
	for _, mode := range []string{RPSModeQuick, RPSModeStandard, RPSModeDeathmatch} {
		if err := ResolveMode(RPSID, mode); err != nil {
			t.Fatal(err)
		}
	}
	if !errors.Is(ResolveMode(RPSID, "other"), ErrUnknownMode) {
		t.Fatal("unknown RPS mode was accepted")
	}

	modules := Modules()
	modules[1].Specs[0] = "mutated"
	again, _ := Resolve(LinkLinkID, LinkLinkVersion)
	if again.Specs[0] != LinkLinkSpec6x8 {
		t.Fatal("registry leaked mutable slice storage")
	}
}

func TestRuntimeContractsRejectCrossGameAndUnknownCapabilities(t *testing.T) {
	fishingID := testOpaqueID("fb_", 1)
	operationID := testOpaqueID("op_", 2)
	valid := []interface{ Validate() error }{
		StartContract{Game: FishingID, Version: FishingVersion},
		StartContract{Game: LinkLinkID, Version: LinkLinkVersion, Spec: LinkLinkSpec8x8},
		StartContract{Game: RPSID, Version: RPSVersion, Mode: RPSModeQuick},
		TerminalContract{Game: FishingID, Version: FishingVersion, ResourceID: fishingID, OperationID: operationID},
		ContinuationContract{Game: FishingID, Version: FishingVersion, ResourceID: fishingID, DueAt: 253402300799},
		AggregateContract{Game: FishingID, Version: FishingVersion, Board: "total", UserID: 1},
		AggregateContract{Game: RPSID, Version: RPSVersion, Board: "net_profit", Mode: RPSModeStandard, UserID: 1},
	}
	for _, contract := range valid {
		if err := contract.Validate(); err != nil {
			t.Fatalf("valid contract %T rejected: %v", contract, err)
		}
	}

	invalid := []interface{ Validate() error }{
		StartContract{Game: FishingID, Version: FishingVersion, Mode: RPSModeQuick},
		StartContract{Game: LinkLinkID, Version: LinkLinkVersion},
		StartContract{Game: RPSID, Version: RPSVersion, Mode: "other"},
		TerminalContract{Game: FishingID, Version: FishingVersion, ResourceID: testOpaqueID("rps_", 1), OperationID: operationID},
		ContinuationContract{Game: FishingID, Version: FishingVersion, ResourceID: fishingID, DueAt: -1},
		AggregateContract{Game: FishingID, Version: FishingVersion, Board: "net_profit", UserID: 1},
		AggregateContract{Game: LinkLinkID, Version: LinkLinkVersion, Board: "single", UserID: 1},
	}
	for _, contract := range invalid {
		if err := contract.Validate(); err == nil {
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
	if err != nil || evidence.EntryMilli != "2500000" || evidence.TargetRTP != "9/10" {
		t.Fatalf("default fishing evidence = %#v, %v", evidence, err)
	}
	for _, malformed := range []string{"true", "false", " 1", "", "2"} {
		if _, err = CompileConfig(map[string]string{GamesEnabledKey: malformed}); !errors.Is(err, ErrInvalidConfig) {
			t.Fatalf("switch %q error = %v", malformed, err)
		}
	}
	if _, err = CompileConfig(map[string]string{FishingEnabledKey: "1"}); !errors.Is(err, ErrInvalidConfig) {
		t.Fatalf("orphan fishing switch error = %v", err)
	}
}

func TestCompileConfigHostileBounds(t *testing.T) {
	cases := []map[string]string{
		{FishingWormPriceMilliKey: "02500000"},
		{FishingStandardRTPKey: "90.0"},
		{FishingPremiumRTPKey: "101"},
		{FishingTreasureBottleMultiplierKey: "0"},
		{FishingTreasureShellMultiplierKey: "1001"},
		{LinkLinkSpecEnabledKey(LinkLinkSpec6x8): "1"},
		{RPSModeBPKey(RPSModeQuick, "platform"): "9900", RPSModeBPKey(RPSModeQuick, "welfare"): "100"},
		{RPSEnabledKey: "1", GamesEnabledKey: "1", RPSModeEnabledKey(RPSModeStandard): "1", RPSModeBaseKey(RPSModeStandard): "1800000000000001"},
		{RPSModeTimeKey(RPSModeQuick, "queue"): "29"},
		{RPSModeTimeKey(RPSModeQuick, "gesture"): "21"},
		{RPSModeTimeKey(RPSModeQuick, "dealer"): "4"},
	}
	for _, raw := range cases {
		if _, err := CompileConfig(raw); !errors.Is(err, ErrInvalidConfig) {
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
	if raw[FishingWormPriceMilliKey] != "2501" || compiled.Fishing.BaitPricesMilli[fishing.BaitLure] != "5000000" {
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
			var candidate GamesConfig
			candidate, decodeErr = patch.Merge(current)
			if decodeErr == nil {
				_, _, decodeErr = CompileGamesConfig(candidate)
			}
		}
		if decodeErr == nil {
			t.Fatalf("hostile patch was accepted: %s", body)
		}
	}
	if _, err = (GamesConfigPatch{ExpectedRevision: "2", MasterEnabled: boolPointer(true)}).Merge(current); !errors.Is(err, ErrRevisionConflict) {
		t.Fatalf("stale revision error = %v", err)
	}
}

func TestCanonicalWireAmountsRoundTrip(t *testing.T) {
	values := []int64{0, 1, 10, 999, 1000, 1001, 123456789, MaxMoneyMilli}
	rng := rand.New(rand.NewSource(42))
	for range 10_000 {
		values = append(values, rng.Int63n(MaxMoneyMilli+1))
	}
	for _, value := range values {
		wire := FormatAmount(value)
		parsed, err := ParseAmount(wire)
		if err != nil || parsed != value {
			t.Fatalf("amount %d -> %q -> %d, %v", value, wire, parsed, err)
		}
	}
	for _, hostile := range []string{"", "+1", "-1", "01", ".1", "1.", "1.0", "1.20", "0.000", "1.2345", "1e3", "9000000000000.001"} {
		if _, err := ParseAmount(hostile); !errors.Is(err, ErrInvalidConfig) {
			t.Fatalf("noncanonical amount %q error = %v", hostile, err)
		}
	}
}

func FuzzCanonicalWireAmount(f *testing.F) {
	for _, seed := range []string{"0", "1", "1.001", "9000000000000", "01", "1.0", "-0", "1e3"} {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, value string) {
		milli, err := ParseAmount(value)
		if err != nil {
			return
		}
		if milli < 0 || milli > MaxMoneyMilli || FormatAmount(milli) != value {
			t.Fatalf("accepted noncanonical amount %q as %d", value, milli)
		}
	})
}

func TestSiteConfigKeysReturnsCopy(t *testing.T) {
	first, second := SiteConfigKeys(), SiteConfigKeys()
	if len(first) != 45 || len(second) != 45 {
		t.Fatalf("key lengths = %d, %d", len(first), len(second))
	}
	first[0] = "mutated"
	if second[0] != GamesEnabledKey || SiteConfigKeys()[0] != GamesEnabledKey {
		t.Fatal("configuration keys leaked mutable storage")
	}
	joined := strings.Join(second, ",")
	for _, key := range []string{LinkLinkSpecPriceKey(LinkLinkSpec10x10), RPSModeTimeKey(RPSModeDeathmatch, "follower")} {
		if !strings.Contains(joined, key) {
			t.Fatalf("missing game config key %s", key)
		}
	}
}

func testOpaqueID(prefix string, fill byte) string {
	return prefix + base64.RawURLEncoding.EncodeToString(bytes.Repeat([]byte{fill}, 16))
}

func boolPointer(value bool) *bool { return &value }
