package db

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"math/big"
	"sort"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/waiting-here/NonbiriAPI/internal/egress"
	"github.com/waiting-here/NonbiriAPI/internal/game"
	"github.com/waiting-here/NonbiriAPI/internal/game/fishing"
	"github.com/waiting-here/NonbiriAPI/internal/ratelimit"
)

const (
	generationTwoAnnouncementEpochKey = "announcement_epoch"
	generationTwoAlertPrefsPrefix     = "alert_prefs_"

	generationTwoMaxConfigKeyBytes      = 128
	generationTwoMaxAlertPrefBytes      = 512
	generationTwoMaxLegalBytes          = 65536
	generationTwoMaxDonationNoticeBytes = 8192
	generationTwoMaxBanSeconds          = uint64(10 * 366 * 24 * 60 * 60)
)

// These exported defaults are the inherited alpha.3 site-config contract.
// Generation 2 keeps them in the typed catalog owner so removing a retired
// domain repository cannot silently remove or change the public defaults.
const (
	DefaultEndpointLimit    = 50
	DefaultEndpointKeyLimit = 20
	DefaultModelLimit       = 100
	DefaultBindingLimit     = 50

	CheckinModeEnabled    = "enabled"
	CheckinModeLevelGated = "level_gated"
	CheckinModeDisabled   = "disabled"

	DefaultCheckinAwardMinMilli int64 = 40_000_000
	DefaultCheckinAwardMaxMilli int64 = 60_000_000
	DefaultCreditsCapMilli      int64 = 250_000_000
)

type generationTwoConfigKind uint8

const (
	generationTwoConfigBool generationTwoConfigKind = iota
	generationTwoConfigUint
	generationTwoConfigAmount
	generationTwoConfigText
	generationTwoConfigMultiline
	generationTwoConfigOptionalLocale
	generationTwoConfigTimezone
	generationTwoConfigEnum
	generationTwoConfigLevelName
)

type generationTwoConfigSpec struct {
	kind       generationTwoConfigKind
	seed       *string
	minimum    uint64
	maximum    uint64
	maxBytes   int
	maxRunes   int
	allowEmpty bool
	allowed    []string
}

// generationTwoConfigQueryer is intentionally local to the typed catalog so
// its validation tests do not depend on the schema-manifest implementation.
// Both *sql.DB and *sql.Tx satisfy it.
type generationTwoConfigQueryer interface {
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
	QueryRowContext(context.Context, string, ...any) *sql.Row
}

func generationTwoSeed(raw string) *string { return &raw }

func generationTwoFishingDefaultAmount(config fishing.Config, bait fishing.Bait) string {
	raw, ok := config.BaitPricesMilli[bait]
	if !ok {
		panic("generation-two config: missing Fishing bait default")
	}
	return raw
}

func generationTwoFishingDefaultMultiplier(config fishing.Config, species string) string {
	value, ok := config.TreasureMultipliers[species]
	if !ok {
		panic("generation-two config: missing Fishing multiplier default")
	}
	return formatGenerationTwoUint(uint64(value))
}

func buildGenerationTwoConfigCatalog() map[string]generationTwoConfigSpec {
	fishingDefaults := fishing.DefaultConfig()
	boolSpec := func(raw string) generationTwoConfigSpec {
		return generationTwoConfigSpec{kind: generationTwoConfigBool, seed: generationTwoSeed(raw)}
	}
	uintSpec := func(raw string, minimum, maximum uint64) generationTwoConfigSpec {
		return generationTwoConfigSpec{kind: generationTwoConfigUint, seed: generationTwoSeed(raw), minimum: minimum, maximum: maximum}
	}
	amountSpec := func(raw string, minimum uint64) generationTwoConfigSpec {
		return generationTwoConfigSpec{kind: generationTwoConfigAmount, seed: generationTwoSeed(raw), minimum: minimum, maximum: uint64(MaxMoneyMilli)}
	}
	textSpec := func(raw string, maxBytes int, allowEmpty bool) generationTwoConfigSpec {
		return generationTwoConfigSpec{kind: generationTwoConfigText, seed: generationTwoSeed(raw), maxBytes: maxBytes, allowEmpty: allowEmpty}
	}
	multilineSpec := func(raw string, maxBytes int) generationTwoConfigSpec {
		return generationTwoConfigSpec{kind: generationTwoConfigMultiline, seed: generationTwoSeed(raw), maxBytes: maxBytes, allowEmpty: true}
	}

	catalog := map[string]generationTwoConfigSpec{
		"site_name":                        textSpec("", 256, true),
		"site_logo_url":                    textSpec("", 2048, true),
		"legal_privacy_override_zh":        multilineSpec("", generationTwoMaxLegalBytes),
		"legal_privacy_override_en":        multilineSpec("", generationTwoMaxLegalBytes),
		"legal_terms_override_zh":          multilineSpec("", generationTwoMaxLegalBytes),
		"legal_terms_override_en":          multilineSpec("", generationTwoMaxLegalBytes),
		"legal_authoritative_locale":       {kind: generationTwoConfigOptionalLocale, seed: generationTwoSeed("")},
		"charity_donation_notice_zh":       multilineSpec("", generationTwoMaxDonationNoticeBytes),
		"charity_donation_notice_en":       multilineSpec("", generationTwoMaxDonationNoticeBytes),
		"default_endpoint_limit":           uintSpec(formatGenerationTwoUint(uint64(DefaultEndpointLimit)), 0, 10000),
		"default_endpoint_key_limit":       uintSpec(formatGenerationTwoUint(uint64(DefaultEndpointKeyLimit)), 1, 10000),
		"default_model_limit":              uintSpec(formatGenerationTwoUint(uint64(DefaultModelLimit)), 1, 10000),
		"default_binding_limit":            uintSpec(formatGenerationTwoUint(uint64(DefaultBindingLimit)), 1, 10000),
		"default_rpm_per_user":             uintSpec(formatGenerationTwoUint(uint64(ratelimit.DefaultRPMPerUserLimit)), 1, 4096),
		"global_rpm":                       uintSpec(formatGenerationTwoUint(uint64(ratelimit.DefaultRPMGlobalLimit)), 1, 4096),
		"default_per_endpoint_concurrency": uintSpec(formatGenerationTwoUint(uint64(egress.DefaultPerEndpointConcurrency)), 1, 100000),
		"egress_global_concurrency":        uintSpec(formatGenerationTwoUint(uint64(egress.DefaultGlobalConcurrency)), 1, 100000),
		"discord_guild_id":                 textSpec("", 128, true),
		"discord_role_id":                  textSpec("", 128, true),
		"oauth_start_rate_limit":           uintSpec(formatGenerationTwoUint(uint64(ratelimit.DefaultOAuthStartRateLimit)), 0, 1000),
		"oauth_start_rate_window_seconds":  uintSpec(formatGenerationTwoUint(uint64(ratelimit.DefaultOAuthStartRateWindowSeconds)), 1, 3600),
		"oauth_start_rate_penalty_seconds": uintSpec(formatGenerationTwoUint(uint64(ratelimit.DefaultOAuthStartRatePenaltySeconds)), 0, 3600),
		// Fresh is deliberately fail-closed even though the inherited effective
		// defaults remain maintenance=false and registration=true in the admin
		// catalog when a row is absent.
		"maintenance_mode":  boolSpec("1"),
		"registration_open": boolSpec("0"),
		// These three inherited keys have a real raw-null state. They are known,
		// but intentionally have no required seed row.
		"site_timezone_offset_minutes": {kind: generationTwoConfigTimezone},
		"charity_token_reserve_milli":  {kind: generationTwoConfigAmount, minimum: 1, maximum: uint64(MaxMoneyMilli)},
		"anthropic_default_max_tokens": {kind: generationTwoConfigUint, minimum: 1, maximum: 2147483647},
		"level_threshold_2_milli":      amountSpec("0", 0),
		"level_threshold_3_milli":      amountSpec("0", 0),
		"level_threshold_4_milli":      amountSpec("0", 0),
		"level_display_name_1":         {kind: generationTwoConfigLevelName, seed: generationTwoSeed(""), maxRunes: 64, allowEmpty: true},
		"level_display_name_2":         {kind: generationTwoConfigLevelName, seed: generationTwoSeed(""), maxRunes: 64, allowEmpty: true},
		"level_display_name_3":         {kind: generationTwoConfigLevelName, seed: generationTwoSeed(""), maxRunes: 64, allowEmpty: true},
		"level_display_name_4":         {kind: generationTwoConfigLevelName, seed: generationTwoSeed(""), maxRunes: 64, allowEmpty: true},
		"level_display_name_5":         {kind: generationTwoConfigLevelName, seed: generationTwoSeed(""), maxRunes: 64, allowEmpty: true},
		"checkin_mode":                 {kind: generationTwoConfigEnum, seed: generationTwoSeed(CheckinModeDisabled), allowed: []string{CheckinModeEnabled, CheckinModeLevelGated, CheckinModeDisabled}},
		"checkin_award_min_milli":      amountSpec(formatGenerationTwoUint(uint64(DefaultCheckinAwardMinMilli)), 0),
		"checkin_award_max_milli":      amountSpec(formatGenerationTwoUint(uint64(DefaultCheckinAwardMaxMilli)), 0),
		"credits_cap_milli":            amountSpec(formatGenerationTwoUint(uint64(DefaultCreditsCapMilli)), 0),
		"charity_enabled":              boolSpec("0"),
		"donation_accept_enabled":      boolSpec("0"),
		"games_enabled":                boolSpec("0"),
		game.FishingEnabledKey:         boolSpec("0"),
		game.FishingWormPriceMilliKey: amountSpec(
			generationTwoFishingDefaultAmount(fishingDefaults, fishing.BaitWorm), uint64(fishing.MinimumBaitPriceMilli)),
		game.FishingLurePriceMilliKey: amountSpec(
			generationTwoFishingDefaultAmount(fishingDefaults, fishing.BaitLure), uint64(fishing.MinimumBaitPriceMilli)),
		game.FishingPremiumPriceMilliKey: amountSpec(
			generationTwoFishingDefaultAmount(fishingDefaults, fishing.BaitPremium), uint64(fishing.MinimumBaitPriceMilli)),
		game.FishingStandardRTPKey: uintSpec(
			formatGenerationTwoUint(uint64(fishingDefaults.StandardRTPPercent)), fishing.MinimumRTPPercent, fishing.MaximumRTPPercent),
		game.FishingPremiumRTPKey: uintSpec(
			formatGenerationTwoUint(uint64(fishingDefaults.PremiumRTPPercent)), fishing.MinimumRTPPercent, fishing.MaximumRTPPercent),
		game.FishingTreasureBottleMultiplierKey: uintSpec(
			generationTwoFishingDefaultMultiplier(fishingDefaults, "bottle"), fishing.MinimumTreasureMultiplier, fishing.MaximumTreasureMultiplier),
		game.FishingTreasureCloverMultiplierKey: uintSpec(
			generationTwoFishingDefaultMultiplier(fishingDefaults, "clover"), fishing.MinimumTreasureMultiplier, fishing.MaximumTreasureMultiplier),
		game.FishingTreasureShellMultiplierKey: uintSpec(
			generationTwoFishingDefaultMultiplier(fishingDefaults, "shell"), fishing.MinimumTreasureMultiplier, fishing.MaximumTreasureMultiplier),
		"activities_enabled":                   boolSpec("0"),
		"activity_welfare_enabled":             boolSpec("0"),
		"activity_welfare_threshold_milli":     amountSpec("0", 0),
		"activity_welfare_cap_milli":           amountSpec("0", 0),
		"activity_thursday_enabled":            boolSpec("0"),
		"game_linklink_enabled":                boolSpec("0"),
		"game_linklink_6x8_enabled":            boolSpec("0"),
		"game_linklink_8x8_enabled":            boolSpec("0"),
		"game_linklink_10x10_enabled":          boolSpec("0"),
		"game_linklink_6x8_price_milli":        amountSpec("0", 0),
		"game_linklink_8x8_price_milli":        amountSpec("0", 0),
		"game_linklink_10x10_price_milli":      amountSpec("0", 0),
		"game_rps_enabled":                     boolSpec("0"),
		"game_rps_quick_enabled":               boolSpec("0"),
		"game_rps_standard_enabled":            boolSpec("0"),
		"game_rps_deathmatch_enabled":          boolSpec("0"),
		"game_rps_quick_b_milli":               amountSpec("0", 0),
		"game_rps_standard_b_milli":            amountSpec("0", 0),
		"game_rps_deathmatch_b_milli":          amountSpec("0", 0),
		"report_pending_ttl_seconds":           uintSpec("86400", 1, 259200),
		"rpm_ban_threshold":                    uintSpec("5", 0, 4096),
		"rpm_ban_window_seconds":               uintSpec("86400", 1, generationTwoMaxBanSeconds),
		"rpm_ban_duration_seconds":             uintSpec("86400", 1, generationTwoMaxBanSeconds),
		"charity_min_chars":                    uintSpec("20", 0, 1<<20),
		"charity_violation_deduct_milli":       amountSpec("0", 0),
		"charity_violation_ban_seconds":        uintSpec("0", 0, generationTwoMaxBanSeconds),
		"charity_violation_window_seconds":     uintSpec("86400", 1, generationTwoMaxBanSeconds),
		"charity_violation_ban_threshold":      uintSpec("0", 0, 4096),
		"charity_violation_window_ban_seconds": uintSpec("0", 0, generationTwoMaxBanSeconds),
		"charity_suspend_window_seconds":       uintSpec("86400", 1, generationTwoMaxBanSeconds),
		"charity_suspend_threshold":            uintSpec("0", 0, 4096),
		"charity_suspend_duration_seconds":     uintSpec("0", 0, generationTwoMaxBanSeconds),
	}

	for _, mode := range []string{"quick", "standard", "deathmatch"} {
		for _, cut := range []string{"platform", "welfare", "thursday"} {
			catalog["game_rps_"+mode+"_"+cut+"_bp"] = uintSpec("100", 0, 9999)
		}
		catalog["game_rps_"+mode+"_queue_seconds"] = uintSpec("120", 30, 120)
		catalog["game_rps_"+mode+"_gesture_seconds"] = uintSpec("20", 5, 20)
		catalog["game_rps_"+mode+"_dealer_seconds"] = uintSpec("15", 5, 15)
		catalog["game_rps_"+mode+"_follower_seconds"] = uintSpec("15", 5, 15)
	}
	return catalog
}

var generationTwoConfigCatalog = buildGenerationTwoConfigCatalog()

// generationTwoConfigKeys returns only rows required after fresh seed.
// Optional raw-null rows may later be inserted or deleted without changing the
// database generation.
func generationTwoConfigKeys() []string {
	keys := make([]string, 0, len(generationTwoConfigCatalog)+1)
	for key, spec := range generationTwoConfigCatalog {
		if spec.seed != nil {
			keys = append(keys, key)
		}
	}
	keys = append(keys, generationTwoAnnouncementEpochKey)
	sort.Strings(keys)
	return keys
}

func generationTwoKnownConfigKeys() []string {
	keys := make([]string, 0, len(generationTwoConfigCatalog)+1)
	for key := range generationTwoConfigCatalog {
		keys = append(keys, key)
	}
	keys = append(keys, generationTwoAnnouncementEpochKey)
	sort.Strings(keys)
	return keys
}

// GenerationTwoKnownSiteConfigKeys exposes a copy for the admin catalog's
// cross-package completeness test. Dynamic alert_prefs_* rows are excluded.
func GenerationTwoKnownSiteConfigKeys() []string {
	return append([]string(nil), generationTwoKnownConfigKeys()...)
}

func insertGenerationTwoConfig(ctx context.Context, tx *sql.Tx, announcementEpoch string) error {
	if !ValidateOpaqueID(announcementEpoch, "b1e_") {
		return errors.New("invalid announcement epoch")
	}
	for _, key := range generationTwoConfigKeys() {
		value := announcementEpoch
		if key != generationTwoAnnouncementEpochKey {
			spec := generationTwoConfigCatalog[key]
			if spec.seed == nil {
				return fmt.Errorf("seed site configuration %s: missing required seed", key)
			}
			value = *spec.seed
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO site_config(key,value,updated_at) VALUES(?,?,0)`, key, value); err != nil {
			return fmt.Errorf("seed site configuration: %w", err)
		}
	}
	return nil
}

func parseGenerationTwoUint(raw string) (uint64, error) {
	var value uint64
	if raw == "" {
		return 0, errors.New("empty unsigned value")
	}
	for i := 0; i < len(raw); i++ {
		if raw[i] < '0' || raw[i] > '9' {
			return 0, errors.New("non-canonical unsigned value")
		}
		digit := uint64(raw[i] - '0')
		if value > (^uint64(0)-digit)/10 {
			return 0, errors.New("unsigned value overflow")
		}
		value = value*10 + digit
	}
	if formatGenerationTwoUint(value) != raw {
		return 0, errors.New("non-canonical unsigned value")
	}
	return value, nil
}

func formatGenerationTwoUint(value uint64) string { return fmt.Sprintf("%d", value) }

func validateGenerationTwoText(value string, maxBytes, maxRunes int, allowEmpty, allowMultiline bool) bool {
	if !utf8.ValidString(value) || (!allowEmpty && value == "") || (maxBytes > 0 && len(value) > maxBytes) {
		return false
	}
	if maxRunes > 0 && utf8.RuneCountInString(value) > maxRunes {
		return false
	}
	for _, r := range value {
		if (unicode.IsControl(r) || r == 0x7f) && !(allowMultiline && (r == '\n' || r == '\r' || r == '\t')) {
			return false
		}
	}
	return true
}

func validateGenerationTwoConfigValue(key, value string) error {
	if key == "default_locale" {
		return errors.New("deleted site configuration key")
	}
	if key == generationTwoAnnouncementEpochKey {
		if !ValidateOpaqueID(value, "b1e_") {
			return errors.New("invalid announcement epoch")
		}
		return nil
	}
	if strings.HasPrefix(key, generationTwoAlertPrefsPrefix) {
		if !validateGenerationTwoText(key, generationTwoMaxConfigKeyBytes, 0, false, false) ||
			len(key) <= len(generationTwoAlertPrefsPrefix) ||
			!validateGenerationTwoText(value, generationTwoMaxAlertPrefBytes, 0, true, false) {
			return errors.New("invalid alert preference")
		}
		return nil
	}
	spec, ok := generationTwoConfigCatalog[key]
	if !ok {
		return errors.New("unknown site configuration key")
	}
	switch spec.kind {
	case generationTwoConfigBool:
		if value != "0" && value != "1" {
			return errors.New("invalid boolean site configuration")
		}
	case generationTwoConfigUint, generationTwoConfigAmount:
		n, err := parseGenerationTwoUint(value)
		if err != nil || n < spec.minimum || n > spec.maximum {
			return errors.New("invalid numeric site configuration")
		}
	case generationTwoConfigText:
		if !validateGenerationTwoText(value, spec.maxBytes, spec.maxRunes, spec.allowEmpty, false) {
			return errors.New("invalid text site configuration")
		}
	case generationTwoConfigMultiline:
		if !validateGenerationTwoText(value, spec.maxBytes, spec.maxRunes, spec.allowEmpty, true) {
			return errors.New("invalid multiline site configuration")
		}
	case generationTwoConfigOptionalLocale:
		if value != "" && value != "zh" && value != "en" {
			return errors.New("invalid authoritative locale")
		}
	case generationTwoConfigTimezone:
		negative := strings.HasPrefix(value, "-")
		magnitudeRaw := value
		if negative {
			magnitudeRaw = strings.TrimPrefix(value, "-")
		}
		magnitude, err := parseGenerationTwoUint(magnitudeRaw)
		if err != nil || (negative && magnitude == 0) || (negative && magnitude > 720) || (!negative && magnitude > 840) || magnitude%30 != 0 {
			return errors.New("invalid site timezone")
		}
	case generationTwoConfigEnum:
		for _, allowed := range spec.allowed {
			if value == allowed {
				return nil
			}
		}
		return errors.New("invalid enum site configuration")
	case generationTwoConfigLevelName:
		if !validateGenerationTwoText(value, spec.maxBytes, spec.maxRunes, spec.allowEmpty, false) {
			return errors.New("invalid level display name")
		}
	default:
		return errors.New("unsupported site configuration kind")
	}
	return nil
}

func generationTwoConfigBoolValue(values map[string]string, key string) bool {
	return values[key] == "1"
}

func generationTwoConfigUintValue(values map[string]string, key string) (uint64, bool) {
	raw, ok := values[key]
	if !ok {
		return 0, false
	}
	value, err := parseGenerationTwoUint(raw)
	return value, err == nil
}

func validateGenerationTwoFishingOperationBound(values map[string]string, rules *fishing.Ruleset) error {
	if rules == nil {
		return errors.New("Fishing ruleset is missing")
	}
	priceKeys := map[fishing.Bait]string{
		fishing.BaitWorm:    game.FishingWormPriceMilliKey,
		fishing.BaitLure:    game.FishingLurePriceMilliKey,
		fishing.BaitPremium: game.FishingPremiumPriceMilliKey,
	}
	maxTreasureMultiplier := uint64(0)
	for _, key := range []string{
		game.FishingTreasureBottleMultiplierKey,
		game.FishingTreasureCloverMultiplierKey,
		game.FishingTreasureShellMultiplierKey,
	} {
		value, ok := generationTwoConfigUintValue(values, key)
		if !ok {
			return errors.New("invalid Fishing treasure multiplier")
		}
		if value > maxTreasureMultiplier {
			maxTreasureMultiplier = value
		}
	}
	limit := new(big.Int).SetInt64(MaxMoneyMilli)
	batchSize := big.NewInt(10)
	for bait, priceKey := range priceKeys {
		price, ok := generationTwoConfigUintValue(values, priceKey)
		if !ok {
			return errors.New("invalid Fishing bait price")
		}
		evidence, err := rules.Evidence(bait)
		if err != nil {
			return fmt.Errorf("Fishing evidence: %w", err)
		}
		scale, ok := new(big.Rat).SetString(evidence.Scale)
		if !ok || scale.Sign() < 0 {
			return errors.New("invalid Fishing scale evidence")
		}
		// The frozen fish size multiplier is capped at 40; the ruleset's
		// exact scale is compiled in [1/10,3]. Round with the same half-up
		// rule as fishing before comparing the ten-outcome batch bound.
		fishExact := new(big.Rat).Mul(new(big.Rat).SetInt(new(big.Int).SetUint64(price)), scale)
		fishExact.Mul(fishExact, new(big.Rat).SetInt64(40))
		fishPayout := new(big.Int)
		remainder := new(big.Int)
		fishPayout.QuoRem(fishExact.Num(), fishExact.Denom(), remainder)
		if new(big.Int).Lsh(remainder, 1).Cmp(fishExact.Denom()) >= 0 {
			fishPayout.Add(fishPayout, big.NewInt(1))
		}
		treasurePayout := new(big.Int).Mul(new(big.Int).SetUint64(price), new(big.Int).SetUint64(maxTreasureMultiplier))
		if treasurePayout.Cmp(fishPayout) > 0 {
			fishPayout = treasurePayout
		}
		if new(big.Int).Mul(fishPayout, batchSize).Cmp(limit) > 0 {
			return fmt.Errorf("Fishing %s ten-outcome payout exceeds MaxMoney", bait)
		}
	}
	return nil
}

func validateGenerationTwoConfigCombinations(values map[string]string) error {
	amountKeys := []string{
		"level_threshold_2_milli", "level_threshold_3_milli", "level_threshold_4_milli",
		"checkin_award_min_milli", "checkin_award_max_milli", "activity_welfare_threshold_milli",
		"activity_welfare_cap_milli",
	}
	for _, key := range amountKeys {
		if _, ok := generationTwoConfigUintValue(values, key); !ok {
			return fmt.Errorf("invalid amount combination: %s", key)
		}
	}
	minAward, _ := generationTwoConfigUintValue(values, "checkin_award_min_milli")
	maxAward, _ := generationTwoConfigUintValue(values, "checkin_award_max_milli")
	if minAward > maxAward {
		return errors.New("checkin award bounds are inverted")
	}
	var prior uint64
	for _, key := range []string{"level_threshold_2_milli", "level_threshold_3_milli", "level_threshold_4_milli"} {
		value, _ := generationTwoConfigUintValue(values, key)
		if value == 0 {
			continue
		}
		if prior != 0 && prior >= value {
			return errors.New("enabled level thresholds are not strictly increasing")
		}
		prior = value
	}
	if generationTwoConfigBoolValue(values, "donation_accept_enabled") && !generationTwoConfigBoolValue(values, "charity_enabled") {
		return errors.New("donation intake requires charity")
	}
	if values["checkin_mode"] != CheckinModeDisabled && values["site_timezone_offset_minutes"] == "" {
		return errors.New("checkin requires site timezone")
	}

	gameValues := make(map[string]string, len(game.SiteConfigKeys()))
	for _, key := range game.SiteConfigKeys() {
		gameValues[key] = values[key]
	}
	gameSnapshot, err := game.CompileConfig(gameValues)
	if err != nil {
		return fmt.Errorf("invalid Fishing configuration: %w", err)
	}
	if err := validateGenerationTwoFishingOperationBound(values, gameSnapshot.Rules); err != nil {
		return err
	}
	if generationTwoConfigBoolValue(values, game.FishingEnabledKey) && !generationTwoConfigBoolValue(values, game.GamesEnabledKey) {
		return errors.New("Fishing requires games_enabled")
	}

	if generationTwoConfigBoolValue(values, "game_linklink_enabled") && !generationTwoConfigBoolValue(values, "games_enabled") {
		return errors.New("LinkLink requires games_enabled")
	}
	for _, board := range []string{"6x8", "8x8", "10x10"} {
		price, ok := generationTwoConfigUintValue(values, "game_linklink_"+board+"_price_milli")
		if !ok {
			return errors.New("invalid LinkLink price")
		}
		if generationTwoConfigBoolValue(values, "game_linklink_"+board+"_enabled") &&
			(!generationTwoConfigBoolValue(values, "game_linklink_enabled") || price == 0) {
			return errors.New("enabled LinkLink specification requires its parent switch and positive price")
		}
	}

	if generationTwoConfigBoolValue(values, "game_rps_enabled") && !generationTwoConfigBoolValue(values, "games_enabled") {
		return errors.New("RPS requires games_enabled")
	}
	for _, mode := range []string{"quick", "standard", "deathmatch"} {
		platform, _ := generationTwoConfigUintValue(values, "game_rps_"+mode+"_platform_bp")
		welfare, _ := generationTwoConfigUintValue(values, "game_rps_"+mode+"_welfare_bp")
		thursday, _ := generationTwoConfigUintValue(values, "game_rps_"+mode+"_thursday_bp")
		if platform+welfare+thursday >= 10000 {
			return errors.New("RPS basis points must sum to less than 10000")
		}
		if !generationTwoConfigBoolValue(values, "game_rps_"+mode+"_enabled") {
			continue
		}
		base, ok := generationTwoConfigUintValue(values, "game_rps_"+mode+"_b_milli")
		if !ok || base == 0 || !generationTwoConfigBoolValue(values, "game_rps_enabled") {
			return errors.New("enabled RPS mode requires its parent switch and positive B")
		}
		if mode == "standard" && (base > 1_800_000_000_000_000 || base > uint64(MaxMoneyMilli)/5) {
			return errors.New("standard RPS B violates the checked 5B bound")
		}
	}

	activitiesEnabled := generationTwoConfigBoolValue(values, "activities_enabled")
	welfareEnabled := generationTwoConfigBoolValue(values, "activity_welfare_enabled")
	thursdayEnabled := generationTwoConfigBoolValue(values, "activity_thursday_enabled")
	if !activitiesEnabled && (welfareEnabled || thursdayEnabled) {
		return errors.New("activity subfeature requires activities_enabled")
	}
	if activitiesEnabled && values["site_timezone_offset_minutes"] == "" {
		return errors.New("activities require site timezone")
	}
	if welfareEnabled {
		_, thresholdOK := generationTwoConfigUintValue(values, "activity_welfare_threshold_milli")
		_, capOK := generationTwoConfigUintValue(values, "activity_welfare_cap_milli")
		if !thresholdOK || !capOK {
			return errors.New("welfare activity requires valid threshold and cap")
		}
	}
	return nil
}

// ValidateGenerationTwoThursdayConfigHealth is the final health gate for the
// dedicated activity-config service.
func ValidateGenerationTwoThursdayConfigHealth(values map[string]string, periodReady bool) error {
	if generationTwoConfigBoolValue(values, "activity_thursday_enabled") && !periodReady {
		return errors.New("Thursday activity requires a configured period")
	}
	return nil
}

// ValidateGenerationTwoRPSConfigHealth is the final health gate for the
// dedicated games-config service. The later RPS owner supplies the explicit
// central-pool/worker readiness result. Generic site-config validation must
// not call this gate: an enabled snapshot is structurally valid, and the
// games owner decides whether it may be activated.
func ValidateGenerationTwoRPSConfigHealth(values map[string]string, ready bool) error {
	enabled := false
	for _, mode := range []string{"quick", "standard", "deathmatch"} {
		if generationTwoConfigBoolValue(values, "game_rps_"+mode+"_enabled") {
			enabled = true
		}
	}
	if !enabled {
		return nil
	}
	if !ready {
		return errors.New("enabled RPS mode requires a healthy central pool and worker")
	}
	return nil
}

// ValidateGenerationTwoConfigSnapshot validates one complete raw site-config
// snapshot without consulting domain tables or process health. Dedicated
// activity/game services call this after merging their proposed patch, then
// call the explicit Thursday/RPS health validator above before committing.
func ValidateGenerationTwoConfigSnapshot(values map[string]string) error {
	for key, value := range values {
		if err := validateGenerationTwoConfigValue(key, value); err != nil {
			return fmt.Errorf("invalid site configuration %s: %w", key, err)
		}
	}
	for _, key := range generationTwoConfigKeys() {
		if _, ok := values[key]; !ok {
			return fmt.Errorf("missing required site configuration key: %s", key)
		}
	}
	return validateGenerationTwoConfigCombinations(values)
}

func validateGenerationTwoConfig(ctx context.Context, q generationTwoConfigQueryer) error {
	rows, err := q.QueryContext(ctx, `SELECT key,value FROM site_config ORDER BY key`)
	if err != nil {
		return err
	}
	defer rows.Close()
	seen := make(map[string]string, len(generationTwoConfigCatalog)+1)
	for rows.Next() {
		var key, value string
		if err := rows.Scan(&key, &value); err != nil {
			return err
		}
		if _, duplicate := seen[key]; duplicate {
			return errors.New("duplicate site configuration key")
		}
		seen[key] = value
	}
	if err := rows.Err(); err != nil {
		return err
	}
	if err := ValidateGenerationTwoConfigSnapshot(seen); err != nil {
		return err
	}
	if generationTwoConfigBoolValue(seen, "activity_thursday_enabled") {
		var ready int
		if err := q.QueryRowContext(ctx, `SELECT EXISTS(
			SELECT 1 FROM thursday_periods WHERE state IN ('configured','open','settling')
		)`).Scan(&ready); err != nil {
			return err
		}
		if err := ValidateGenerationTwoThursdayConfigHealth(seen, ready == 1); err != nil {
			return err
		}
	}
	return nil
}
