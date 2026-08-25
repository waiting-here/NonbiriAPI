package adminapi

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/waiting-here/NonbiriAPI/internal/game"
	"github.com/waiting-here/NonbiriAPI/internal/game/fishing"
	"github.com/waiting-here/NonbiriAPI/internal/host"
	"github.com/waiting-here/NonbiriAPI/internal/ratelimit"
)

func TestSiteConfigCatalogCoversEveryKnownKey(t *testing.T) {
	if len(catalogMetadataByKey) != len(knownSiteConfig) {
		t.Fatalf("catalog metadata count=%d, known config count=%d", len(catalogMetadataByKey), len(knownSiteConfig))
	}
	entries, err := buildSiteConfigCatalog(map[string]string{
		"alert_prefs_email": "enabled",
		"unknown_row":       "ignored",
	})
	if err != nil {
		t.Fatalf("build catalog: %v", err)
	}
	if len(entries) != len(knownSiteConfig)+1 {
		t.Fatalf("catalog entries=%d, want %d", len(entries), len(knownSiteConfig)+1)
	}
	keys := make([]string, len(entries))
	seen := make(map[string]bool, len(entries))
	for i, entry := range entries {
		keys[i] = entry.Key
		if seen[entry.Key] {
			t.Fatalf("duplicate catalog key %q", entry.Key)
		}
		seen[entry.Key] = true
		for field, text := range map[string]localizedCatalogText{
			"title": entry.Title, "description": entry.Description, "unit": entry.Unit,
			"null_semantics": entry.NullSemantics,
		} {
			if strings.TrimSpace(text.Zh) == "" || strings.TrimSpace(text.En) == "" {
				t.Fatalf("%s has incomplete %s: %+v", entry.Key, field, text)
			}
		}
		for field, text := range map[string]*localizedCatalogText{
			"zero_semantics": entry.ZeroSemantics, "empty_semantics": entry.EmptySemantics,
		} {
			if text != nil && (strings.TrimSpace(text.Zh) == "" || strings.TrimSpace(text.En) == "") {
				t.Fatalf("%s has incomplete %s: %+v", entry.Key, field, text)
			}
		}
		if entry.Group == "" || entry.ValueType == "" || entry.ValueType == "unknown" || entry.WriteEndpoint == "" {
			t.Fatalf("%s has incomplete typed metadata: %+v", entry.Key, entry)
		}
		for _, gate := range entry.IndependentGates {
			if strings.TrimSpace(gate.Zh) == "" || strings.TrimSpace(gate.En) == "" {
				t.Fatalf("%s has incomplete independent gate: %+v", entry.Key, gate)
			}
		}
	}
	if !sort.StringsAreSorted(keys) {
		t.Fatalf("catalog keys are not sorted: %v", keys)
	}
	if !seen["alert_prefs_email"] || seen["unknown_row"] {
		t.Fatalf("dynamic/unknown projection mismatch: keys=%v", keys)
	}
}

func TestSiteConfigCatalogFrozenOptionalAndGameSemantics(t *testing.T) {
	entries, err := buildSiteConfigCatalog(nil)
	if err != nil {
		t.Fatalf("build catalog: %v", err)
	}
	byKey := make(map[string]siteConfigCatalogEntry, len(entries))
	for _, entry := range entries {
		byKey[entry.Key] = entry
	}
	anthropic := byKey[KeyAnthropicDefaultMaxTokens]
	if anthropic.ValueType != "optional_integer" || !anthropic.Nullable || !anthropic.NullWritable ||
		anthropic.RawDefault != nil || anthropic.Step != 1 ||
		anthropic.EffectiveFallback != 65536 || anthropic.Minimum != 1 || anthropic.Maximum != 2147483647 {
		t.Fatalf("anthropic catalog semantics=%+v", anthropic)
	}
	if !strings.Contains(anthropic.NullSemantics.En, "65536") {
		t.Fatalf("anthropic null semantics=%+v", anthropic.NullSemantics)
	}
	if anthropic.ZeroSemantics != nil {
		t.Fatalf("anthropic zero semantics=%+v, want JSON null", anthropic.ZeroSemantics)
	}
	timezone := byKey[KeySiteTimezoneOffsetMinutes]
	if timezone.ValueType != "optional_integer" || !timezone.Nullable || timezone.NullWritable ||
		timezone.RawDefault != nil || timezone.EffectiveFallback != nil ||
		timezone.Minimum != -720 || timezone.Maximum != 840 || timezone.Step != 30 {
		t.Fatalf("timezone catalog semantics=%+v", timezone)
	}
	for _, key := range []string{
		KeyGamesEnabled, KeyGameFishingEnabled,
		KeyGameFishingBaitWormPrice, KeyGameFishingBaitLurePrice, KeyGameFishingBaitPremiumPrice,
		KeyGameFishingRTP, KeyGameFishingRTPPremium,
		KeyGameFishingTreasureBottle, KeyGameFishingTreasureClover, KeyGameFishingTreasureShell,
	} {
		if got := byKey[key].WriteEndpoint; got != "/admin/api/games/config" {
			t.Fatalf("%s write endpoint=%q", key, got)
		}
	}
	if got := byKey[KeyGameFishingBaitPremiumPrice].RawDefault; got != "7500000" {
		t.Fatalf("premium price raw default=%v, want string 7500000", got)
	}
	for _, key := range []string{
		KeyGameFishingBaitWormPrice, KeyGameFishingBaitLurePrice, KeyGameFishingBaitPremiumPrice,
	} {
		entry := byKey[key]
		if entry.Minimum != "1" || entry.ZeroSemantics == nil ||
			!strings.Contains(entry.ZeroSemantics.En, "at least one") {
			t.Fatalf("%s zero/minimum semantics=%+v", key, entry)
		}
	}
}

func TestGameCatalogUsesAuthoritativeRegistryDefaults(t *testing.T) {
	entries, err := buildSiteConfigCatalog(nil)
	if err != nil {
		t.Fatalf("build catalog: %v", err)
	}
	byKey := make(map[string]siteConfigCatalogEntry, len(entries))
	for _, entry := range entries {
		byKey[entry.Key] = entry
	}
	registryKeys := game.SiteConfigKeys()
	if len(registryKeys) != 10 {
		t.Fatalf("game registry keys=%d, want 10", len(registryKeys))
	}
	for _, key := range registryKeys {
		entry, ok := byKey[key]
		if !ok || entry.WriteEndpoint != "/admin/api/games/config" {
			t.Fatalf("game registry key %q missing from catalog: %+v", key, entry)
		}
	}
	defaults := fishing.DefaultConfig()
	want := map[string]any{
		game.GamesEnabledKey:                    false,
		game.FishingEnabledKey:                  false,
		game.FishingWormPriceMilliKey:           defaults.BaitPricesMilli[fishing.BaitWorm],
		game.FishingLurePriceMilliKey:           defaults.BaitPricesMilli[fishing.BaitLure],
		game.FishingPremiumPriceMilliKey:        defaults.BaitPricesMilli[fishing.BaitPremium],
		game.FishingStandardRTPKey:              defaults.StandardRTPPercent,
		game.FishingPremiumRTPKey:               defaults.PremiumRTPPercent,
		game.FishingTreasureBottleMultiplierKey: defaults.TreasureMultipliers["bottle"],
		game.FishingTreasureCloverMultiplierKey: defaults.TreasureMultipliers["clover"],
		game.FishingTreasureShellMultiplierKey:  defaults.TreasureMultipliers["shell"],
	}
	for key, expected := range want {
		if got := byKey[key].RawDefault; got != expected {
			t.Fatalf("%s raw default=%v (%T), want %v (%T)", key, got, got, expected, expected)
		}
	}
	for _, key := range []string{game.FishingStandardRTPKey, game.FishingPremiumRTPKey} {
		entry := byKey[key]
		if entry.Minimum != fishing.MinimumRTPPercent || entry.Maximum != fishing.MaximumRTPPercent {
			t.Fatalf("%s range=%v..%v", key, entry.Minimum, entry.Maximum)
		}
	}
	for _, key := range []string{
		game.FishingTreasureBottleMultiplierKey,
		game.FishingTreasureCloverMultiplierKey,
		game.FishingTreasureShellMultiplierKey,
	} {
		entry := byKey[key]
		if entry.Minimum != fishing.MinimumTreasureMultiplier || entry.Maximum != fishing.MaximumTreasureMultiplier {
			t.Fatalf("%s range=%v..%v", key, entry.Minimum, entry.Maximum)
		}
	}
}

func TestSiteConfigCatalogSemanticsMatchTypedValidatorsForEveryKnownKey(t *testing.T) {
	entries, err := buildSiteConfigCatalog(nil)
	if err != nil {
		t.Fatalf("build catalog: %v", err)
	}
	byKey := make(map[string]siteConfigCatalogEntry, len(entries))
	for _, entry := range entries {
		byKey[entry.Key] = entry
	}
	e := newEnv(t)
	for key, spec := range knownSiteConfig {
		key, spec := key, spec
		t.Run(key, func(t *testing.T) {
			entry, ok := byKey[key]
			if !ok {
				t.Fatal("catalog entry missing")
			}
			if entry.EmptySemantics == nil || strings.TrimSpace(entry.EmptySemantics.En) == "" ||
				strings.Contains(entry.EmptySemantics.En, "see the description") ||
				strings.Contains(entry.NullSemantics.En, "see the description") {
				t.Fatalf("empty/null semantics are not concrete: %+v", entry)
			}
			if key == KeyAnthropicDefaultMaxTokens {
				if entry.ZeroSemantics != nil || !entry.NullWritable || !entry.Nullable ||
					!strings.Contains(entry.EmptySemantics.En, "sends JSON null") {
					t.Fatalf("anthropic reset semantics=%+v", entry)
				}
			} else if entry.ZeroSemantics == nil || strings.TrimSpace(entry.ZeroSemantics.En) == "" ||
				strings.Contains(entry.ZeroSemantics.En, "see the description") || entry.NullWritable {
				t.Fatalf("zero/null-write semantics are not concrete: %+v", entry)
			}

			var zeroRaw []byte
			switch spec.kind {
			case kindBool:
				zeroRaw = []byte("false")
			case kindAmount, kindOptionalAmount:
				zeroRaw = []byte(`"0"`)
			default:
				zeroRaw = []byte("0")
			}
			_, zeroErr := validateSiteConfigValue(key, zeroRaw)
			zeroAccepted := zeroErr.Code == ""
			wantZero := spec.kind == kindBool || spec.kind == kindTimezoneOffset ||
				(spec.kind == kindInt && spec.min == 0) ||
				(spec.kind == kindAmount && !isFishingBaitPriceKey(key))
			if zeroAccepted != wantZero {
				t.Fatalf("zero validator accepted=%v, want %v; entry=%+v", zeroAccepted, wantZero, entry)
			}
			if wantZero && entry.ZeroSemantics != nil && strings.Contains(entry.ZeroSemantics.En, "rejected") {
				t.Fatalf("accepted zero has rejection semantics: %+v", entry.ZeroSemantics)
			}

			_, emptyErr := validateSiteConfigValue(key, []byte(`""`))
			emptyAccepted := emptyErr.Code == ""
			wantEmpty := (spec.kind == kindText && spec.allowEmpty) ||
				spec.kind == kindMultilineText || spec.kind == kindLocaleOpt
			if emptyAccepted != wantEmpty {
				t.Fatalf("empty validator accepted=%v, want %v; entry=%+v", emptyAccepted, wantEmpty, entry)
			}
			if wantEmpty && strings.HasPrefix(entry.EmptySemantics.En, "An empty string is rejected") {
				t.Fatalf("accepted empty string has rejection semantics: %+v", entry.EmptySemantics)
			}

			wantNullableRaw := spec.kind == kindTimezoneOffset || spec.kind == kindOptionalAmount || spec.kind == kindOptionalInt
			if entry.Nullable != wantNullableRaw {
				t.Fatalf("raw nullable=%v, want %v", entry.Nullable, wantNullableRaw)
			}
			if wantNullableRaw && !strings.Contains(entry.NullSemantics.En, "null") {
				t.Fatalf("nullable raw field does not explain null: %+v", entry.NullSemantics)
			}
			if !isGameConfigKey(key) {
				rec := adminPatchRaw(t, e, nil, "/admin/api/site-config/"+key, []byte(`{"value":null}`))
				if key == KeyAnthropicDefaultMaxTokens {
					if rec.Code != http.StatusOK {
						t.Fatalf("null reset status=%d body=%s", rec.Code, rec.Body.String())
					}
				} else {
					assertErr(t, rec, http.StatusBadRequest, "invalid_request")
				}
			}
		})
	}

	for _, key := range []string{KeyDefaultRPMPerUser, KeyGlobalRPM} {
		if byKey[key].Unit.En != "requests/minute" {
			t.Fatalf("%s unit=%+v", key, byKey[key].Unit)
		}
	}
	for _, key := range []string{
		KeyLevelThreshold2Milli, KeyLevelThreshold3Milli, KeyLevelThreshold4Milli,
		KeyCheckinAwardMinMilli, KeyCheckinAwardMaxMilli,
		KeyGamesEnabled, KeyGameFishingEnabled, KeyGameFishingBaitWormPrice,
		KeyGameFishingBaitLurePrice, KeyGameFishingBaitPremiumPrice,
		KeyGameFishingRTP, KeyGameFishingRTPPremium,
		KeyGameFishingTreasureBottle, KeyGameFishingTreasureClover, KeyGameFishingTreasureShell,
	} {
		if len(byKey[key].IndependentGates) == 0 {
			t.Fatalf("%s missing cross-field/independent gate", key)
		}
	}
}

func TestOAuthStartZeroPenaltyPersistsAndLiveAppliesWithoutDefaulting(t *testing.T) {
	e := newEnv(t)
	config := ratelimit.DefaultIPThrottleConfig()
	config.Limit = ratelimit.DefaultOAuthStartRateLimit
	throttle, err := ratelimit.NewIPThrottle(config)
	if err != nil {
		t.Fatalf("new throttle: %v", err)
	}
	defer throttle.Close()
	applier := NewRuntimeApplier(nil, nil, throttle, nil)
	rec := adminPatch(t, e, applier, "/admin/api/site-config/"+KeyOAuthStartRatePenaltySecs, map[string]any{"value": 0})
	if rec.Code != http.StatusOK {
		t.Fatalf("zero penalty patch status=%d body=%s", rec.Code, rec.Body.String())
	}
	if got := throttle.Config().Penalty; got != 0 {
		t.Fatalf("live penalty=%v, want explicit zero", got)
	}
	stored, err := e.store.GetSiteConfigValue(KeyOAuthStartRatePenaltySecs)
	if err != nil || stored != "0" {
		t.Fatalf("stored penalty=(%q, %v), want canonical zero", stored, err)
	}
}

type frozenCatalogCoreEntry struct {
	Key               string                 `json:"key"`
	Group             string                 `json:"group"`
	ValueType         string                 `json:"value_type"`
	Title             localizedCatalogText   `json:"title"`
	Description       localizedCatalogText   `json:"description"`
	Unit              localizedCatalogText   `json:"unit"`
	Nullable          bool                   `json:"nullable"`
	RawDefault        any                    `json:"raw_default"`
	EffectiveFallback any                    `json:"effective_fallback"`
	Minimum           any                    `json:"minimum"`
	Maximum           any                    `json:"maximum"`
	ZeroSemantics     *localizedCatalogText  `json:"zero_semantics"`
	NullSemantics     localizedCatalogText   `json:"null_semantics"`
	IndependentGates  []localizedCatalogText `json:"independent_gates"`
	WriteEndpoint     string                 `json:"write_endpoint"`
}

func frozenCatalogCore(entry siteConfigCatalogEntry) frozenCatalogCoreEntry {
	return frozenCatalogCoreEntry{
		Key: entry.Key, Group: entry.Group, ValueType: entry.ValueType,
		Title: entry.Title, Description: entry.Description, Unit: entry.Unit,
		Nullable: entry.Nullable, RawDefault: entry.RawDefault,
		EffectiveFallback: entry.EffectiveFallback, Minimum: entry.Minimum, Maximum: entry.Maximum,
		ZeroSemantics: entry.ZeroSemantics, NullSemantics: entry.NullSemantics,
		IndependentGates: entry.IndependentGates, WriteEndpoint: entry.WriteEndpoint,
	}
}

func TestSiteConfigCatalogJSONCoreAndStableEmptyArrays(t *testing.T) {
	entries, err := buildSiteConfigCatalog(nil)
	if err != nil {
		t.Fatalf("build catalog: %v", err)
	}
	byKey := make(map[string]siteConfigCatalogEntry, len(entries))
	for _, entry := range entries {
		byKey[entry.Key] = entry
		if entry.AllowedValues == nil || entry.IndependentGates == nil {
			t.Fatalf("%s has nil JSON array", entry.Key)
		}
	}

	encoded, err := json.Marshal(struct {
		Data []siteConfigCatalogEntry `json:"data"`
	}{Data: entries})
	if err != nil {
		t.Fatalf("marshal catalog: %v", err)
	}
	var wire struct {
		Data []map[string]any `json:"data"`
	}
	if err := json.Unmarshal(encoded, &wire); err != nil {
		t.Fatalf("unmarshal catalog JSON: %v", err)
	}
	for _, item := range wire.Data {
		if item["key"] == KeySiteName {
			allowed, allowedOK := item["allowed_values"].([]any)
			gates, gatesOK := item["independent_gates"].([]any)
			if !allowedOK || len(allowed) != 0 || !gatesOK || len(gates) != 0 {
				t.Fatalf("site_name arrays are not stable empty arrays: %#v", item)
			}
		}
		if item["key"] == KeyAnthropicDefaultMaxTokens && item["zero_semantics"] != nil {
			t.Fatalf("anthropic zero_semantics=%#v, want null", item["zero_semantics"])
		}
	}

	fixturePath := filepath.Join("..", "..", "web", "test", "fixtures", "site-config-catalog-core.json")
	fixtureBytes, err := os.ReadFile(fixturePath)
	if err != nil {
		t.Fatalf("read shared catalog fixture: %v", err)
	}
	var fixture struct {
		Data []frozenCatalogCoreEntry `json:"data"`
	}
	if err := json.Unmarshal(fixtureBytes, &fixture); err != nil {
		t.Fatalf("decode shared catalog fixture: %v", err)
	}
	want := []frozenCatalogCoreEntry{frozenCatalogCore(byKey[KeySiteName])}
	gotJSON, err := json.Marshal(fixture.Data)
	if err != nil {
		t.Fatalf("marshal fixture core: %v", err)
	}
	wantJSON, err := json.Marshal(want)
	if err != nil {
		t.Fatalf("marshal backend core: %v", err)
	}
	if string(gotJSON) != string(wantJSON) {
		t.Fatalf("shared frontend fixture drifted from backend core\ngot:  %+v\nwant: %+v", fixture.Data, want)
	}
}

func TestSiteConfigCatalogRouteIsAdminOnlyAndStrict(t *testing.T) {
	e := newEnv(t)
	if err := e.store.SetSiteConfigValue("alert_prefs_email", "enabled"); err != nil {
		t.Fatalf("seed alert preference: %v", err)
	}
	rec := adminGet(t, e, "/admin/api/site-config/catalog")
	var body struct {
		Data []siteConfigCatalogEntry `json:"data"`
	}
	decodeJSON(t, rec, &body)
	if len(body.Data) != len(knownSiteConfig)+1 {
		t.Fatalf("route catalog count=%d", len(body.Data))
	}

	rec = adminGet(t, e, "/admin/api/site-config/catalog?unexpected=1")
	assertErr(t, rec, http.StatusBadRequest, "invalid_request")

	unauth := do(t, e.mount(t, nil), stationRequest(http.MethodGet, "/admin/api/site-config/catalog", host.StationAdmin, nil))
	assertErr(t, unauth, http.StatusUnauthorized, "unauthorized")
}
