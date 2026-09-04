package adminapi

import (
	"encoding/json"
	"strconv"
	"strings"
	"testing"

	"github.com/waiting-here/NonbiriAPI/internal/credits"
	"github.com/waiting-here/NonbiriAPI/internal/db"
	"github.com/waiting-here/NonbiriAPI/internal/game"
	"github.com/waiting-here/NonbiriAPI/internal/game/fishing"
	"github.com/waiting-here/NonbiriAPI/internal/ratelimit"
)

func TestGenerationTwoAdminConfigTypedBoundaries(t *testing.T) {
	maxMoney := formatAdminWireAmount(db.MaxMoneyMilli)
	overMoney := formatAdminWireAmount(db.MaxMoneyMilli + 1)
	for _, key := range []string{KeyLevelThreshold2Milli, KeyActivityWelfareThreshold, KeyGameRPSQuickB} {
		stored, errValue := validateSiteConfigValue(key, json.RawMessage(strconv.Quote(maxMoney)))
		if errValue.Code != "" || stored != strconv.FormatInt(db.MaxMoneyMilli, 10) {
			t.Fatalf("%s MaxMoney rejected: stored=%q err=%+v", key, stored, errValue)
		}
		if _, errValue := validateSiteConfigValue(key, json.RawMessage(strconv.Quote(overMoney))); errValue.Code != "invalid_request" {
			t.Fatalf("%s over-MaxMoney err=%+v", key, errValue)
		}
	}
	if stored, errValue := validateSiteConfigValue(KeyCharityTokenReserveMilli, json.RawMessage(strconv.Quote(maxMoney))); errValue.Code != "" || stored != strconv.FormatInt(db.MaxMoneyMilli, 10) {
		t.Fatalf("optional amount MaxMoney rejected: stored=%q err=%+v", stored, errValue)
	}
	if _, errValue := validateSiteConfigValue(KeyCharityTokenReserveMilli, json.RawMessage(strconv.Quote(overMoney))); errValue.Code != "invalid_request" {
		t.Fatalf("optional amount over-MaxMoney err=%+v", errValue)
	}
	for _, tc := range []struct {
		key, wire, stored, projected string
	}{
		{KeyLevelThreshold2Milli, "1", "1000", "1"},
		{KeyLevelThreshold2Milli, "0.001", "1", "0.001"},
		{KeyGameFishingBaitWormPrice, "0.001", "1", "0.001"},
	} {
		stored, errValue := validateSiteConfigValue(tc.key, json.RawMessage(strconv.Quote(tc.wire)))
		if errValue.Code != "" || stored != tc.stored {
			t.Fatalf("%s wire amount %q: stored=%q err=%+v, want %q", tc.key, tc.wire, stored, errValue, tc.stored)
		}
		if projected := typedSiteConfigValue(tc.key, tc.stored); projected != tc.projected {
			t.Fatalf("%s raw amount %q projects %v, want %q", tc.key, tc.stored, projected, tc.projected)
		}
	}
	for _, invalid := range []string{"01", "1.", "1.0000", "+1", "-1", "1e3", "0.0009"} {
		if _, errValue := validateSiteConfigValue(KeyLevelThreshold2Milli, json.RawMessage(strconv.Quote(invalid))); errValue.Code != "invalid_request" {
			t.Fatalf("invalid wire amount %q err=%+v", invalid, errValue)
		}
	}

	name64 := strings.Repeat("界", 64)
	if stored, errValue := validateSiteConfigValue(KeyLevelDisplayName1, json.RawMessage(strconv.Quote(name64))); errValue.Code != "" || stored != name64 {
		t.Fatalf("64-rune level name rejected: stored=%q err=%+v", stored, errValue)
	}
	for _, invalid := range []string{strings.Repeat("界", 65), "bad\nname", "bad\x00name"} {
		if _, errValue := validateSiteConfigValue(KeyLevelDisplayName1, json.RawMessage(strconv.Quote(invalid))); errValue.Code != "invalid_request" {
			t.Fatalf("invalid level name %q err=%+v", invalid, errValue)
		}
	}
	if _, errValue := validateSiteConfigValue(KeyAnnouncementEpoch, json.RawMessage(strconv.Quote("b1e_AAAAAAAAAAAAAAAAAAAAAA"))); errValue.Code != "conflict" {
		t.Fatalf("read-only announcement epoch err=%+v", errValue)
	}
	if knownSiteConfigKey("default_locale") {
		t.Fatal("deleted default_locale remains a known admin key")
	}
}

func TestSiteConfigCatalogCoversEveryKnownKey(t *testing.T) {
	wantKnown := db.GenerationTwoKnownSiteConfigKeys()
	if len(wantKnown) != len(knownSiteConfig) {
		t.Fatalf("Generation 2 known config count=%d, admin count=%d", len(wantKnown), len(knownSiteConfig))
	}
	for _, key := range wantKnown {
		if _, ok := knownSiteConfig[key]; !ok {
			t.Fatalf("Generation 2 key %q missing from admin registry", key)
		}
	}
	if len(catalogMetadataByKey) != len(knownSiteConfig) {
		t.Fatalf("catalog metadata count=%d, known config count=%d", len(catalogMetadataByKey), len(knownSiteConfig))
	}
	entries, err := buildSiteConfigCatalog(map[string]string{
		"alert_prefs_email": "enabled",
		"alert_prefs_":      "invalid bare namespace",
		"unknown_row":       "ignored",
	})
	if err != nil {
		t.Fatalf("build catalog: %v", err)
	}
	if len(entries) != len(knownSiteConfig)+1 {
		t.Fatalf("catalog entries=%d, want %d", len(entries), len(knownSiteConfig)+1)
	}
	seen := make(map[string]bool, len(entries))
	for i, entry := range entries {
		if i > 0 && (entries[i-1].Group > entry.Group ||
			(entries[i-1].Group == entry.Group && entries[i-1].Key >= entry.Key)) {
			t.Fatalf("catalog is not sorted by group/key at %q then %q", entries[i-1].Key, entry.Key)
		}
		if seen[entry.Key] {
			t.Fatalf("duplicate catalog key %q", entry.Key)
		}
		seen[entry.Key] = true
		for field, text := range map[string]localizedCatalogText{
			"title": entry.Title, "description": entry.Description,
			"null_semantics": entry.NullSemantics,
			"zero_semantics": entry.ZeroSemantics, "empty_semantics": entry.EmptySemantics,
		} {
			if strings.TrimSpace(text.Zh) == "" || strings.TrimSpace(text.En) == "" {
				t.Fatalf("%s has incomplete %s: %+v", entry.Key, field, text)
			}
		}
		if entry.Unit != nil && (strings.TrimSpace(entry.Unit.Zh) == "" || strings.TrimSpace(entry.Unit.En) == "") {
			t.Fatalf("%s has incomplete unit: %+v", entry.Key, entry.Unit)
		}
		if entry.Group == "" || entry.ValueType == "" ||
			(entry.WriteEndpoint == "" && entry.Key != KeyMaintenanceMode && entry.Key != KeyAnnouncementEpoch) {
			t.Fatalf("%s has incomplete typed metadata: %+v", entry.Key, entry)
		}
		for _, gate := range entry.IndependentGates {
			if strings.TrimSpace(gate) == "" {
				t.Fatalf("%s has incomplete independent gate: %+v", entry.Key, gate)
			}
		}
	}
	if !seen["alert_prefs_email"] || seen["alert_prefs_"] || seen["unknown_row"] {
		t.Fatalf("dynamic/unknown projection mismatch: seen=%v", seen)
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
	if anthropic.ValueType != "integer" || !anthropic.Nullable || !anthropic.NullWritable ||
		anthropic.RawDefault != nil || anthropic.Step != 1 ||
		anthropic.EffectiveFallback != 65536 || anthropic.Minimum != 1 || anthropic.Maximum != 2147483647 {
		t.Fatalf("anthropic catalog semantics=%+v", anthropic)
	}
	if !strings.Contains(anthropic.NullSemantics.En, "65536") {
		t.Fatalf("anthropic null semantics=%+v", anthropic.NullSemantics)
	}
	if strings.TrimSpace(anthropic.ZeroSemantics.En) == "" {
		t.Fatalf("anthropic zero semantics missing: %+v", anthropic.ZeroSemantics)
	}
	timezone := byKey[KeySiteTimezoneOffsetMinutes]
	if timezone.ValueType != "integer" || !timezone.Nullable || timezone.NullWritable ||
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
	if got := byKey[KeyGameFishingBaitPremiumPrice].RawDefault; got != "7500" {
		t.Fatalf("premium price wire default=%v, want string 7500", got)
	}
	for _, key := range []string{
		KeyGameFishingBaitWormPrice, KeyGameFishingBaitLurePrice, KeyGameFishingBaitPremiumPrice,
	} {
		entry := byKey[key]
		if entry.Minimum != "0.001" ||
			!strings.Contains(entry.ZeroSemantics.En, "at least one") {
			t.Fatalf("%s zero/minimum semantics=%+v", key, entry)
		}
	}
}

func TestBetaCatalogRawDefaultsAndSpecializedEndpoints(t *testing.T) {
	entries, err := buildSiteConfigCatalog(nil)
	if err != nil {
		t.Fatalf("build catalog: %v", err)
	}
	byKey := make(map[string]siteConfigCatalogEntry, len(entries))
	for _, entry := range entries {
		byKey[entry.Key] = entry
	}
	wantDefaults := map[string]any{
		KeyOAuthStartRateLimit:       ratelimit.DefaultOAuthStartRateLimit,
		KeyOAuthStartRateWindowSecs:  ratelimit.DefaultOAuthStartRateWindowSeconds,
		KeyOAuthStartRatePenaltySecs: ratelimit.DefaultOAuthStartRatePenaltySeconds,
		KeyMaintenanceMode:           true,
		KeyRegistrationOpen:          false,
		KeyCheckinMode:               db.CheckinModeDisabled,
		KeyCheckinAwardMinMilli:      formatAdminWireAmount(db.DefaultCheckinAwardMinMilli),
		KeyCheckinAwardMaxMilli:      formatAdminWireAmount(db.DefaultCheckinAwardMaxMilli),
		KeyCreditsCapMilli:           formatAdminWireAmount(db.DefaultCreditsCapMilli),
		KeyActivitiesEnabled:         false,
		KeyActivityWelfareEnabled:    false,
		KeyActivityWelfareThreshold:  "0",
		KeyActivityWelfareCap:        "0",
		KeyActivityThursdayEnabled:   false,
		KeyGameLinkLinkEnabled:       false,
		KeyGameLinkLink6x8Price:      "0",
		KeyGameRPSEnabled:            false,
		KeyGameRPSQuickB:             "0",
		KeyReportPendingTTLSeconds:   86400,
	}
	for key, want := range wantDefaults {
		if got := byKey[key].RawDefault; got != want {
			t.Fatalf("%s raw_default=%v (%T), want %v (%T)", key, got, got, want, want)
		}
	}
	if byKey[KeyMaintenanceMode].EffectiveFallback != false ||
		byKey[KeyRegistrationOpen].EffectiveFallback != true {
		t.Fatalf("fresh raw defaults must not replace inherited fallbacks: maintenance=%+v registration=%+v",
			byKey[KeyMaintenanceMode], byKey[KeyRegistrationOpen])
	}
	if byKey[KeyAnnouncementEpoch].RawDefault != nil || byKey[KeyAnnouncementEpoch].WriteEndpoint != "" {
		t.Fatalf("announcement epoch catalog=%+v", byKey[KeyAnnouncementEpoch])
	}
	for level, key := range []string{KeyLevelDisplayName1, KeyLevelDisplayName2, KeyLevelDisplayName3, KeyLevelDisplayName4, KeyLevelDisplayName5} {
		entry := byKey[key]
		if entry.RawDefault != "" || entry.EffectiveFallback != "Lv. "+strconv.Itoa(level+1) || entry.Maximum != 64 {
			t.Fatalf("%s catalog=%+v", key, entry)
		}
	}
	for _, mode := range []string{"quick", "standard", "deathmatch"} {
		prefix := "game_rps_" + mode + "_"
		for _, cut := range []string{"platform", "welfare", "thursday"} {
			entry := byKey[prefix+cut+"_bp"]
			if entry.RawDefault != 100 || entry.Minimum != 0 || entry.Maximum != 9999 || entry.Unit == nil || entry.Unit.En != "basis points" {
				t.Fatalf("%s cut catalog=%+v", prefix+cut+"_bp", entry)
			}
		}
		for suffix, want := range map[string]int{"queue_seconds": 120, "gesture_seconds": 20, "dealer_seconds": 15, "follower_seconds": 15} {
			if got := byKey[prefix+suffix].RawDefault; got != want {
				t.Fatalf("%s raw_default=%v, want %d", prefix+suffix, got, want)
			}
		}
	}
	for key := range knownSiteConfig {
		entry := byKey[key]
		if isGameConfigKey(key) && entry.WriteEndpoint != "/admin/api/games/config" {
			t.Fatalf("game key %s write_endpoint=%q", key, entry.WriteEndpoint)
		}
		if isActivityConfigKey(key) && entry.WriteEndpoint != "/admin/api/activities/config" {
			t.Fatalf("activity key %s write_endpoint=%q", key, entry.WriteEndpoint)
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
	if len(registryKeys) != 45 {
		t.Fatalf("game registry keys=%d, want 45", len(registryKeys))
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
		if spec, ok := knownSiteConfig[key]; ok && (spec.kind == kindAmount || spec.kind == kindOptionalAmount) {
			raw, err := credits.ParseAmount(expected.(string))
			if err != nil {
				t.Fatalf("parse raw default %s: %v", key, err)
			}
			expected = formatAdminWireAmount(raw)
		}
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
	for key, spec := range knownSiteConfig {
		key, spec := key, spec
		t.Run(key, func(t *testing.T) {
			entry, ok := byKey[key]
			if !ok {
				t.Fatal("catalog entry missing")
			}
			if strings.TrimSpace(entry.EmptySemantics.En) == "" ||
				strings.Contains(entry.EmptySemantics.En, "see the description") ||
				strings.Contains(entry.NullSemantics.En, "see the description") {
				t.Fatalf("empty/null semantics are not concrete: %+v", entry)
			}
			if key == KeyAnthropicDefaultMaxTokens {
				if !entry.NullWritable || !entry.Nullable ||
					!strings.Contains(entry.EmptySemantics.En, "sends JSON null") {
					t.Fatalf("anthropic reset semantics=%+v", entry)
				}
			} else if strings.TrimSpace(entry.ZeroSemantics.En) == "" ||
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
			if wantZero && strings.Contains(entry.ZeroSemantics.En, "rejected") {
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
		})
	}

	for _, key := range []string{KeyDefaultRPMPerUser, KeyGlobalRPM} {
		if byKey[key].Unit == nil || byKey[key].Unit.En != "requests/minute" {
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

func TestSiteConfigCatalogJSONCoreAndStableEmptyArrays(t *testing.T) {
	entries, err := buildSiteConfigCatalog(nil)
	if err != nil {
		t.Fatalf("build catalog: %v", err)
	}
	for _, entry := range entries {
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
		if len(item) != 19 {
			t.Fatalf("catalog entry has %d fields, want frozen 19: %#v", len(item), item)
		}
	}
}
