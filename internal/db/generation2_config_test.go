package db

import (
	"context"
	"database/sql"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/waiting-here/NonbiriAPI/internal/egress"
	"github.com/waiting-here/NonbiriAPI/internal/game"
	"github.com/waiting-here/NonbiriAPI/internal/game/fishing"
	"github.com/waiting-here/NonbiriAPI/internal/ratelimit"
	_ "modernc.org/sqlite"
)

const generationTwoTestAnnouncementEpoch = "b1e_AAAAAAAAAAAAAAAAAAAAAA"

func generationTwoSeedValuesForTest(t *testing.T) map[string]string {
	t.Helper()
	values := make(map[string]string, len(generationTwoConfigCatalog)+1)
	for key, spec := range generationTwoConfigCatalog {
		if spec.seed != nil {
			values[key] = *spec.seed
		}
	}
	values[generationTwoAnnouncementEpochKey] = generationTwoTestAnnouncementEpoch
	return values
}

func TestGenerationTwoConfigCatalogSeparatesRequiredAndOptionalRows(t *testing.T) {
	required := generationTwoConfigKeys()
	known := generationTwoKnownConfigKeys()
	if !reflect.DeepEqual(required, generationTwoConfigKeys()) ||
		!reflect.DeepEqual(known, generationTwoKnownConfigKeys()) {
		t.Fatal("configuration key snapshots are not stable")
	}
	requiredSet := make(map[string]bool, len(required))
	for _, key := range required {
		requiredSet[key] = true
	}
	knownSet := make(map[string]bool, len(known))
	for _, key := range known {
		knownSet[key] = true
	}
	for _, key := range []string{
		"site_timezone_offset_minutes",
		"charity_token_reserve_milli",
		"anthropic_default_max_tokens",
	} {
		if requiredSet[key] || !knownSet[key] {
			t.Fatalf("optional key %q required=%v known=%v", key, requiredSet[key], knownSet[key])
		}
	}
	if len(known) != len(required)+3 {
		t.Fatalf("known=%d required=%d, want exactly three optional fixed rows", len(known), len(required))
	}
	if knownSet["default_locale"] || requiredSet["default_locale"] {
		t.Fatal("deleted default_locale remains in Generation 2 catalog")
	}
	for _, key := range required {
		value := generationTwoTestAnnouncementEpoch
		if key != generationTwoAnnouncementEpochKey {
			value = *generationTwoConfigCatalog[key].seed
		}
		if err := validateGenerationTwoConfigValue(key, value); err != nil {
			t.Fatalf("required default %s=%q rejected: %v", key, value, err)
		}
	}
}

func TestGenerationTwoConfigDefaultsUseAuthoritativeSubsystemValues(t *testing.T) {
	values := generationTwoSeedValuesForTest(t)
	if values["maintenance_mode"] != "1" || values["registration_open"] != "0" {
		t.Fatalf("fresh gates maintenance/registration=%q/%q", values["maintenance_mode"], values["registration_open"])
	}
	for _, key := range []string{
		"activities_enabled", "activity_welfare_enabled", "activity_thursday_enabled",
		"games_enabled", game.FishingEnabledKey, "game_linklink_enabled", "game_rps_enabled",
		"charity_enabled", "donation_accept_enabled",
	} {
		if values[key] != "0" {
			t.Fatalf("fresh fail-closed key %s=%q", key, values[key])
		}
	}

	fishingDefaults := fishing.DefaultConfig()
	wantFishing := map[string]string{
		game.FishingWormPriceMilliKey:           fishingDefaults.BaitPricesMilli[fishing.BaitWorm],
		game.FishingLurePriceMilliKey:           fishingDefaults.BaitPricesMilli[fishing.BaitLure],
		game.FishingPremiumPriceMilliKey:        fishingDefaults.BaitPricesMilli[fishing.BaitPremium],
		game.FishingStandardRTPKey:              formatGenerationTwoUint(uint64(fishingDefaults.StandardRTPPercent)),
		game.FishingPremiumRTPKey:               formatGenerationTwoUint(uint64(fishingDefaults.PremiumRTPPercent)),
		game.FishingTreasureBottleMultiplierKey: formatGenerationTwoUint(uint64(fishingDefaults.TreasureMultipliers["bottle"])),
		game.FishingTreasureCloverMultiplierKey: formatGenerationTwoUint(uint64(fishingDefaults.TreasureMultipliers["clover"])),
		game.FishingTreasureShellMultiplierKey:  formatGenerationTwoUint(uint64(fishingDefaults.TreasureMultipliers["shell"])),
	}
	for key, want := range wantFishing {
		if got := values[key]; got != want {
			t.Fatalf("Fishing default %s=%q, want %q", key, got, want)
		}
	}

	wantScalars := map[string]string{
		"oauth_start_rate_limit":           formatGenerationTwoUint(uint64(ratelimit.DefaultOAuthStartRateLimit)),
		"oauth_start_rate_window_seconds":  formatGenerationTwoUint(uint64(ratelimit.DefaultOAuthStartRateWindowSeconds)),
		"oauth_start_rate_penalty_seconds": formatGenerationTwoUint(uint64(ratelimit.DefaultOAuthStartRatePenaltySeconds)),
		"default_per_endpoint_concurrency": formatGenerationTwoUint(uint64(egress.DefaultPerEndpointConcurrency)),
		"egress_global_concurrency":        formatGenerationTwoUint(uint64(egress.DefaultGlobalConcurrency)),
		"checkin_award_min_milli":          formatGenerationTwoUint(uint64(DefaultCheckinAwardMinMilli)),
		"checkin_award_max_milli":          formatGenerationTwoUint(uint64(DefaultCheckinAwardMaxMilli)),
		"credits_cap_milli":                formatGenerationTwoUint(uint64(DefaultCreditsCapMilli)),
	}
	for key, want := range wantScalars {
		if got := values[key]; got != want {
			t.Fatalf("default %s=%q, want %q", key, got, want)
		}
	}
}

func TestGenerationTwoConfigScalarBoundaries(t *testing.T) {
	valid := []struct{ key, value string }{
		{"site_timezone_offset_minutes", "-720"},
		{"site_timezone_offset_minutes", "840"},
		{"legal_privacy_override_zh", strings.Repeat("x", generationTwoMaxLegalBytes)},
		{"level_display_name_1", strings.Repeat("界", 64)},
		{"game_rps_quick_queue_seconds", "30"},
		{"game_rps_quick_queue_seconds", "120"},
		{"game_rps_standard_gesture_seconds", "5"},
		{"game_rps_standard_gesture_seconds", "20"},
		{"game_rps_deathmatch_dealer_seconds", "5"},
		{"game_rps_deathmatch_follower_seconds", "15"},
		{"report_pending_ttl_seconds", "1"},
		{"report_pending_ttl_seconds", "259200"},
		{"charity_token_reserve_milli", "1"},
		{"charity_token_reserve_milli", formatGenerationTwoUint(uint64(MaxMoneyMilli))},
		{"alert_prefs_email", strings.Repeat("x", generationTwoMaxAlertPrefBytes)},
	}
	for _, tc := range valid {
		if err := validateGenerationTwoConfigValue(tc.key, tc.value); err != nil {
			t.Errorf("valid %s=%q rejected: %v", tc.key, tc.value, err)
		}
	}

	invalid := []struct{ key, value string }{
		{"default_locale", "zh"},
		{"unknown", "0"},
		{"site_timezone_offset_minutes", "-0"},
		{"site_timezone_offset_minutes", "15"},
		{"site_timezone_offset_minutes", "-750"},
		{"site_timezone_offset_minutes", "870"},
		{"global_rpm", "00"},
		{"global_rpm", "+1"},
		{"global_rpm", " 1"},
		{"maintenance_mode", "true"},
		{"maintenance_mode", "2"},
		{"legal_privacy_override_zh", strings.Repeat("x", generationTwoMaxLegalBytes+1)},
		{"legal_privacy_override_zh", "bad\x00value"},
		{"level_display_name_1", strings.Repeat("界", 65)},
		{"level_display_name_1", "bad\nvalue"},
		{"game_rps_quick_queue_seconds", "29"},
		{"game_rps_quick_queue_seconds", "121"},
		{"game_rps_standard_gesture_seconds", "4"},
		{"game_rps_standard_gesture_seconds", "21"},
		{"game_rps_deathmatch_dealer_seconds", "4"},
		{"game_rps_deathmatch_follower_seconds", "16"},
		{"report_pending_ttl_seconds", "0"},
		{"report_pending_ttl_seconds", "259201"},
		{"charity_token_reserve_milli", "0"},
		{"charity_token_reserve_milli", formatGenerationTwoUint(uint64(MaxMoneyMilli) + 1)},
		{"alert_prefs_", "x"},
		{"alert_prefs_email", strings.Repeat("x", generationTwoMaxAlertPrefBytes+1)},
		{"alert_prefs_email", "bad\nvalue"},
	}
	for _, tc := range invalid {
		if err := validateGenerationTwoConfigValue(tc.key, tc.value); err == nil {
			t.Errorf("invalid %s=%q accepted", tc.key, tc.value)
		}
	}
}

func TestGenerationTwoConfigEveryFixedKeyHasExactScalarContract(t *testing.T) {
	for key, spec := range generationTwoConfigCatalog {
		key, spec := key, spec
		t.Run(key, func(t *testing.T) {
			accept := func(value string) {
				t.Helper()
				if err := validateGenerationTwoConfigValue(key, value); err != nil {
					t.Fatalf("value %q rejected: %v", value, err)
				}
			}
			reject := func(value string) {
				t.Helper()
				if err := validateGenerationTwoConfigValue(key, value); err == nil {
					t.Fatalf("value %q accepted", value)
				}
			}

			switch spec.kind {
			case generationTwoConfigBool:
				accept("0")
				accept("1")
				reject("false")
				reject("2")
			case generationTwoConfigUint, generationTwoConfigAmount:
				accept(formatGenerationTwoUint(spec.minimum))
				accept(formatGenerationTwoUint(spec.maximum))
				if spec.minimum > 0 {
					reject(formatGenerationTwoUint(spec.minimum - 1))
				}
				if spec.maximum < ^uint64(0) {
					reject(formatGenerationTwoUint(spec.maximum + 1))
				}
				reject("00")
				reject("+1")
			case generationTwoConfigText:
				if spec.allowEmpty {
					accept("")
				} else {
					reject("")
				}
				if spec.maxBytes > 0 {
					accept(strings.Repeat("x", spec.maxBytes))
					reject(strings.Repeat("x", spec.maxBytes+1))
				}
				reject("bad\ntext")
			case generationTwoConfigMultiline:
				accept("line one\n\tline two\r\n")
				accept(strings.Repeat("x", spec.maxBytes))
				reject(strings.Repeat("x", spec.maxBytes+1))
				reject("bad\x00text")
			case generationTwoConfigOptionalLocale:
				for _, value := range []string{"", "zh", "en"} {
					accept(value)
				}
				reject("ZH")
				reject("fr")
			case generationTwoConfigTimezone:
				for _, value := range []string{"-720", "0", "840"} {
					accept(value)
				}
				for _, value := range []string{"-0", "-750", "15", "870"} {
					reject(value)
				}
			case generationTwoConfigEnum:
				for _, value := range spec.allowed {
					accept(value)
				}
				reject("unknown")
			case generationTwoConfigLevelName:
				accept("")
				accept(strings.Repeat("界", spec.maxRunes))
				reject(strings.Repeat("界", spec.maxRunes+1))
				reject("bad\nname")
			default:
				t.Fatalf("uncovered kind %d", spec.kind)
			}
		})
	}
}

func TestGenerationTwoConfigCombinationHealth(t *testing.T) {
	fresh := generationTwoSeedValuesForTest(t)
	if err := ValidateGenerationTwoConfigSnapshot(fresh); err != nil {
		t.Fatalf("fresh combination rejected: %v", err)
	}
	missing := cloneGenerationTwoConfigValues(fresh)
	delete(missing, "registration_open")
	if err := ValidateGenerationTwoConfigSnapshot(missing); err == nil {
		t.Fatal("snapshot missing a required row was accepted")
	}
	unknown := cloneGenerationTwoConfigValues(fresh)
	unknown["default_locale"] = "zh"
	if err := ValidateGenerationTwoConfigSnapshot(unknown); err == nil {
		t.Fatal("snapshot containing deleted default_locale was accepted")
	}

	t.Run("levels", func(t *testing.T) {
		values := cloneGenerationTwoConfigValues(fresh)
		values["level_threshold_2_milli"] = "20"
		values["level_threshold_3_milli"] = "10"
		if err := validateGenerationTwoConfigCombinations(values); err == nil {
			t.Fatal("decreasing enabled level thresholds accepted")
		}
		values["level_threshold_2_milli"] = "0"
		if err := validateGenerationTwoConfigCombinations(values); err != nil {
			t.Fatalf("disabled lower threshold rejected: %v", err)
		}
	})

	t.Run("checkin and donation", func(t *testing.T) {
		values := cloneGenerationTwoConfigValues(fresh)
		values["checkin_mode"] = CheckinModeEnabled
		if err := validateGenerationTwoConfigCombinations(values); err == nil {
			t.Fatal("check-in without timezone accepted")
		}
		values["site_timezone_offset_minutes"] = "0"
		if err := validateGenerationTwoConfigCombinations(values); err != nil {
			t.Fatalf("configured check-in rejected: %v", err)
		}
		values["donation_accept_enabled"] = "1"
		if err := validateGenerationTwoConfigCombinations(values); err == nil {
			t.Fatal("donation intake without charity accepted")
		}
	})

	t.Run("activities", func(t *testing.T) {
		values := cloneGenerationTwoConfigValues(fresh)
		values["activities_enabled"] = "1"
		if err := validateGenerationTwoConfigCombinations(values); err == nil {
			t.Fatal("activities without timezone accepted")
		}
		values["site_timezone_offset_minutes"] = "0"
		values["activity_welfare_enabled"] = "1"
		if err := validateGenerationTwoConfigCombinations(values); err == nil {
			t.Fatal("welfare with zero threshold/cap accepted")
		}
		values["activity_welfare_threshold_milli"] = "100"
		values["activity_welfare_cap_milli"] = "10"
		values["activity_thursday_enabled"] = "1"
		if err := validateGenerationTwoConfigCombinations(values); err != nil {
			t.Fatalf("complete activity scalar snapshot rejected: %v", err)
		}
		if err := ValidateGenerationTwoThursdayConfigHealth(values, false); err == nil {
			t.Fatal("Thursday without period health accepted")
		}
		if err := ValidateGenerationTwoThursdayConfigHealth(values, true); err != nil {
			t.Fatalf("healthy Thursday rejected: %v", err)
		}
	})

	t.Run("LinkLink", func(t *testing.T) {
		values := cloneGenerationTwoConfigValues(fresh)
		values["game_linklink_6x8_enabled"] = "1"
		if err := validateGenerationTwoConfigCombinations(values); err == nil {
			t.Fatal("orphan LinkLink specification accepted")
		}
		values["games_enabled"] = "1"
		values["game_linklink_enabled"] = "1"
		values["game_linklink_6x8_price_milli"] = "1"
		if err := validateGenerationTwoConfigCombinations(values); err != nil {
			t.Fatalf("complete LinkLink snapshot rejected: %v", err)
		}
	})

	t.Run("Fishing ten-outcome bound", func(t *testing.T) {
		values := cloneGenerationTwoConfigValues(fresh)
		values[game.FishingWormPriceMilliKey] = formatGenerationTwoUint(uint64(MaxMoneyMilli))
		if err := validateGenerationTwoConfigCombinations(values); err == nil {
			t.Fatal("Fishing configuration whose ten-outcome payout exceeds MaxMoney was accepted")
		}
		values[game.FishingWormPriceMilliKey] = fresh[game.FishingWormPriceMilliKey]
		if err := validateGenerationTwoConfigCombinations(values); err != nil {
			t.Fatalf("default Fishing operation bound rejected: %v", err)
		}
	})

	t.Run("RPS", func(t *testing.T) {
		values := cloneGenerationTwoConfigValues(fresh)
		values["games_enabled"] = "1"
		values["game_rps_enabled"] = "1"
		values["game_rps_standard_enabled"] = "1"
		values["game_rps_standard_b_milli"] = "1800000000000001"
		if err := validateGenerationTwoConfigCombinations(values); err == nil {
			t.Fatal("standard RPS B above the checked 5B bound accepted")
		}
		values["game_rps_standard_b_milli"] = "1800000000000000"
		if err := validateGenerationTwoConfigCombinations(values); err != nil {
			t.Fatalf("standard RPS boundary rejected: %v", err)
		}
		if err := ValidateGenerationTwoRPSConfigHealth(values, false); err == nil {
			t.Fatal("RPS without central health accepted")
		}
		if err := ValidateGenerationTwoRPSConfigHealth(values, true); err != nil {
			t.Fatalf("healthy RPS rejected: %v", err)
		}
		values["game_rps_standard_thursday_bp"] = "9800"
		if err := validateGenerationTwoConfigCombinations(values); err == nil {
			t.Fatal("RPS basis-point sum at 10000 accepted")
		}
	})
}

func TestGenerationTwoEnabledRPSSnapshotSurvivesCloseReopen(t *testing.T) {
	path := filepath.Join(t.TempDir(), "generation-two-rps-readiness.db")
	open := func() *sql.DB {
		database, err := sql.Open("sqlite", path)
		if err != nil {
			t.Fatal(err)
		}
		database.SetMaxOpenConns(1)
		return database
	}

	database := open()
	if _, err := database.Exec(`CREATE TABLE site_config(
		key TEXT NOT NULL PRIMARY KEY,
		value TEXT NOT NULL,
		updated_at INTEGER NOT NULL
	)`); err != nil {
		database.Close()
		t.Fatalf("create config validation fixture: %v", err)
	}
	tx, err := database.BeginTx(context.Background(), nil)
	if err != nil {
		database.Close()
		t.Fatal(err)
	}
	if err := insertGenerationTwoConfig(context.Background(), tx, generationTwoTestAnnouncementEpoch); err != nil {
		_ = tx.Rollback()
		database.Close()
		t.Fatalf("insert required seed: %v", err)
	}
	if err := tx.Commit(); err != nil {
		database.Close()
		t.Fatalf("commit required seed: %v", err)
	}
	for key, value := range map[string]string{
		"games_enabled":               "1",
		"game_rps_enabled":            "1",
		"game_rps_quick_enabled":      "1",
		"game_rps_quick_b_milli":      "1",
		"game_rps_standard_enabled":   "0",
		"game_rps_deathmatch_enabled": "0",
	} {
		if _, err := database.Exec(`UPDATE site_config SET value=? WHERE key=?`, value, key); err != nil {
			_ = database.Close()
			t.Fatalf("enable RPS snapshot key %s: %v", key, err)
		}
	}
	if err := database.Close(); err != nil {
		t.Fatalf("close RPS snapshot fixture: %v", err)
	}

	database = open()
	defer database.Close()
	values, err := readGenerationTwoSiteConfigSnapshot(context.Background(), database)
	if err != nil {
		t.Fatalf("read RPS snapshot after reopen: %v", err)
	}
	if err := validateGenerationTwoSiteConfigSnapshot(context.Background(), database, values); err != nil {
		t.Fatalf("enabled RPS snapshot was rejected by generic validation: %v", err)
	}
	clean := generationTwoConfigSnapshotForValidation(values)
	if err := ValidateGenerationTwoRPSConfigHealth(clean, false); err == nil {
		t.Fatal("explicitly unhealthy RPS readiness was accepted")
	}
	if err := ValidateGenerationTwoRPSConfigHealth(clean, true); err != nil {
		t.Fatalf("explicitly healthy RPS readiness rejected: %v", err)
	}
}

func TestGenerationTwoConfigOptionalDeleteSurvivesCurrentValidation(t *testing.T) {
	path := filepath.Join(t.TempDir(), "generation-two-config.db")
	open := func() *sql.DB {
		database, err := sql.Open("sqlite", path)
		if err != nil {
			t.Fatal(err)
		}
		database.SetMaxOpenConns(1)
		return database
	}

	database := open()
	for _, statement := range []string{
		`CREATE TABLE site_config(key TEXT NOT NULL PRIMARY KEY,value TEXT NOT NULL,updated_at INTEGER NOT NULL)`,
		`CREATE TABLE thursday_periods(state TEXT NOT NULL)`,
	} {
		if _, err := database.Exec(statement); err != nil {
			t.Fatalf("create config validation fixture: %v", err)
		}
	}
	tx, err := database.BeginTx(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := insertGenerationTwoConfig(context.Background(), tx, generationTwoTestAnnouncementEpoch); err != nil {
		_ = tx.Rollback()
		t.Fatalf("insert required seed: %v", err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	if _, err := database.Exec(`INSERT INTO site_config(key,value,updated_at) VALUES
		('site_timezone_offset_minutes','0',0),
		('charity_token_reserve_milli','1',0),
		('anthropic_default_max_tokens','65536',0)`); err != nil {
		t.Fatalf("insert optional rows: %v", err)
	}
	if err := database.Close(); err != nil {
		t.Fatal(err)
	}

	database = open()
	if err := validateGenerationTwoConfig(context.Background(), database); err != nil {
		t.Fatalf("current validation with optional rows: %v", err)
	}
	if _, err := database.Exec(`DELETE FROM site_config WHERE key IN
		('site_timezone_offset_minutes','charity_token_reserve_milli','anthropic_default_max_tokens')`); err != nil {
		t.Fatalf("delete optional rows: %v", err)
	}
	if err := database.Close(); err != nil {
		t.Fatal(err)
	}

	database = open()
	defer database.Close()
	if err := validateGenerationTwoConfig(context.Background(), database); err != nil {
		t.Fatalf("current validation after optional delete/reopen: %v", err)
	}
}

func cloneGenerationTwoConfigValues(values map[string]string) map[string]string {
	clone := make(map[string]string, len(values))
	for key, value := range values {
		clone[key] = value
	}
	return clone
}
