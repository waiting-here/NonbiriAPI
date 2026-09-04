package game

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"math/big"
	"strconv"
	"strings"

	"github.com/waiting-here/NonbiriAPI/internal/credits"
	"github.com/waiting-here/NonbiriAPI/internal/game/fishing"
)

const (
	GamesEnabledKey                    = "games_enabled"
	FishingEnabledKey                  = "game_fishing_enabled"
	FishingWormPriceMilliKey           = "game_fishing_bait_worm_price_milli"
	FishingLurePriceMilliKey           = "game_fishing_bait_lure_price_milli"
	FishingPremiumPriceMilliKey        = "game_fishing_bait_premium_price_milli"
	FishingStandardRTPKey              = "game_fishing_rtp"
	FishingPremiumRTPKey               = "game_fishing_rtp_premium"
	FishingTreasureBottleMultiplierKey = "game_fishing_treasure_bottle_mult"
	FishingTreasureCloverMultiplierKey = "game_fishing_treasure_clover_mult"
	FishingTreasureShellMultiplierKey  = "game_fishing_treasure_shell_mult"

	LinkLinkEnabledKey = "game_linklink_enabled"
	RPSEnabledKey      = "game_rps_enabled"

	MaxMoneyMilli         int64 = 9_000_000_000_000_000
	RPSQueueCapacity            = 4096
	RPSStandardMultiplier       = 5
	RPSFreeTieReminder          = 3
	RPSFreeTieLimit             = 6
)

var linkLinkSpecs = [...]string{LinkLinkSpec6x8, LinkLinkSpec8x8, LinkLinkSpec10x10}
var rpsModes = [...]string{RPSModeQuick, RPSModeStandard, RPSModeDeathmatch}

func LinkLinkSpecEnabledKey(spec string) string { return "game_linklink_" + spec + "_enabled" }
func LinkLinkSpecPriceKey(spec string) string   { return "game_linklink_" + spec + "_price_milli" }
func RPSModeEnabledKey(mode string) string      { return "game_rps_" + mode + "_enabled" }
func RPSModeBaseKey(mode string) string         { return "game_rps_" + mode + "_b_milli" }
func RPSModeBPKey(mode, cut string) string      { return "game_rps_" + mode + "_" + cut + "_bp" }
func RPSModeTimeKey(mode, phase string) string  { return "game_rps_" + mode + "_" + phase + "_seconds" }

var configKeys = buildConfigKeys()

func buildConfigKeys() []string {
	keys := []string{
		GamesEnabledKey, FishingEnabledKey,
		FishingWormPriceMilliKey, FishingLurePriceMilliKey, FishingPremiumPriceMilliKey,
		FishingStandardRTPKey, FishingPremiumRTPKey,
		FishingTreasureBottleMultiplierKey, FishingTreasureCloverMultiplierKey,
		FishingTreasureShellMultiplierKey, LinkLinkEnabledKey, RPSEnabledKey,
	}
	for _, spec := range linkLinkSpecs {
		keys = append(keys, LinkLinkSpecEnabledKey(spec), LinkLinkSpecPriceKey(spec))
	}
	for _, mode := range rpsModes {
		keys = append(keys, RPSModeEnabledKey(mode), RPSModeBaseKey(mode))
		for _, cut := range []string{"platform", "welfare", "thursday"} {
			keys = append(keys, RPSModeBPKey(mode, cut))
		}
		for _, phase := range []string{"queue", "gesture", "dealer", "follower"} {
			keys = append(keys, RPSModeTimeKey(mode, phase))
		}
	}
	return keys
}

// SiteConfigKeys returns a defensive copy of every game-owned raw key.
func SiteConfigKeys() []string { return append([]string(nil), configKeys...) }

type LinkLinkSpecConfig struct {
	Enabled    bool
	PriceMilli int64
}

type LinkLinkConfig struct {
	Enabled bool
	Specs   map[string]LinkLinkSpecConfig
}

type PumpsBP struct {
	Platform int `json:"platform"`
	Welfare  int `json:"welfare"`
	Thursday int `json:"thursday"`
}

type RPSModeConfig struct {
	Enabled         bool
	BaseMilli       int64
	PumpsBP         PumpsBP
	QueueSeconds    int
	GestureSeconds  int
	DealerSeconds   int
	FollowerSeconds int
}

type RPSConfig struct {
	Enabled bool
	Modes   map[string]RPSModeConfig
}

// ConfigSnapshot is one fully compiled immutable runtime snapshot.
type ConfigSnapshot struct {
	GamesEnabled   bool
	FishingEnabled bool
	Fishing        fishing.Config
	Rules          *fishing.Ruleset
	LinkLink       LinkLinkConfig
	RPS            RPSConfig
}

// CompileConfig converts the complete raw site_config view to a snapshot.
// Missing rows use fresh-seed defaults; malformed rows reject the whole view.
func CompileConfig(raw map[string]string) (ConfigSnapshot, error) {
	fishingConfig := fishing.DefaultConfig()
	for bait, key := range map[fishing.Bait]string{
		fishing.BaitWorm:    FishingWormPriceMilliKey,
		fishing.BaitLure:    FishingLurePriceMilliKey,
		fishing.BaitPremium: FishingPremiumPriceMilliKey,
	} {
		if value, ok := raw[key]; ok {
			fishingConfig.BaitPricesMilli[bait] = value
		}
	}
	standardRTP, err := rawInt(raw, FishingStandardRTPKey, fishing.DefaultConfig().StandardRTPPercent, fishing.MinimumRTPPercent, fishing.MaximumRTPPercent)
	if err != nil {
		return ConfigSnapshot{}, err
	}
	premiumRTP, err := rawInt(raw, FishingPremiumRTPKey, fishing.DefaultConfig().PremiumRTPPercent, fishing.MinimumRTPPercent, fishing.MaximumRTPPercent)
	if err != nil {
		return ConfigSnapshot{}, err
	}
	fishingConfig.StandardRTPPercent = standardRTP
	fishingConfig.PremiumRTPPercent = premiumRTP
	for species, key := range map[string]string{
		"bottle": FishingTreasureBottleMultiplierKey,
		"clover": FishingTreasureCloverMultiplierKey,
		"shell":  FishingTreasureShellMultiplierKey,
	} {
		value, parseErr := rawInt(raw, key, fishing.DefaultConfig().TreasureMultipliers[species], fishing.MinimumTreasureMultiplier, fishing.MaximumTreasureMultiplier)
		if parseErr != nil {
			return ConfigSnapshot{}, parseErr
		}
		fishingConfig.TreasureMultipliers[species] = value
	}
	rules, err := fishing.Compile(fishingConfig)
	if err != nil {
		return ConfigSnapshot{}, fmt.Errorf("%w: %v", ErrInvalidConfig, err)
	}
	for _, bait := range []fishing.Bait{fishing.BaitWorm, fishing.BaitLure, fishing.BaitPremium} {
		entry, _ := rules.EntryMilli(bait)
		maximum, _ := rules.MaximumPayoutMilli(bait)
		if entry > MaxMoneyMilli/10 || maximum > MaxMoneyMilli/10 {
			return ConfigSnapshot{}, fmt.Errorf("%w: fishing ten-draw bound", ErrInvalidConfig)
		}
	}

	master, err := rawBool(raw, GamesEnabledKey, false)
	if err != nil {
		return ConfigSnapshot{}, err
	}
	fishingEnabled, err := rawBool(raw, FishingEnabledKey, false)
	if err != nil {
		return ConfigSnapshot{}, err
	}
	linkEnabled, err := rawBool(raw, LinkLinkEnabledKey, false)
	if err != nil {
		return ConfigSnapshot{}, err
	}
	rpsEnabled, err := rawBool(raw, RPSEnabledKey, false)
	if err != nil {
		return ConfigSnapshot{}, err
	}
	if (fishingEnabled || linkEnabled || rpsEnabled) && !master {
		return ConfigSnapshot{}, fmt.Errorf("%w: enabled game requires master", ErrInvalidConfig)
	}

	link := LinkLinkConfig{Enabled: linkEnabled, Specs: make(map[string]LinkLinkSpecConfig, 3)}
	for _, spec := range linkLinkSpecs {
		enabled, parseErr := rawBool(raw, LinkLinkSpecEnabledKey(spec), false)
		if parseErr != nil {
			return ConfigSnapshot{}, parseErr
		}
		price, parseErr := rawAmount(raw, LinkLinkSpecPriceKey(spec), 0, 0)
		if parseErr != nil {
			return ConfigSnapshot{}, parseErr
		}
		if enabled && (!linkEnabled || price == 0) {
			return ConfigSnapshot{}, fmt.Errorf("%w: enabled LinkLink spec", ErrInvalidConfig)
		}
		link.Specs[spec] = LinkLinkSpecConfig{Enabled: enabled, PriceMilli: price}
	}

	rps := RPSConfig{Enabled: rpsEnabled, Modes: make(map[string]RPSModeConfig, 3)}
	for _, mode := range rpsModes {
		enabled, parseErr := rawBool(raw, RPSModeEnabledKey(mode), false)
		if parseErr != nil {
			return ConfigSnapshot{}, parseErr
		}
		base, parseErr := rawAmount(raw, RPSModeBaseKey(mode), 0, 0)
		if parseErr != nil {
			return ConfigSnapshot{}, parseErr
		}
		modeConfig := RPSModeConfig{Enabled: enabled, BaseMilli: base}
		modeConfig.PumpsBP.Platform, parseErr = rawInt(raw, RPSModeBPKey(mode, "platform"), 100, 0, 9999)
		if parseErr != nil {
			return ConfigSnapshot{}, parseErr
		}
		modeConfig.PumpsBP.Welfare, parseErr = rawInt(raw, RPSModeBPKey(mode, "welfare"), 100, 0, 9999)
		if parseErr != nil {
			return ConfigSnapshot{}, parseErr
		}
		modeConfig.PumpsBP.Thursday, parseErr = rawInt(raw, RPSModeBPKey(mode, "thursday"), 100, 0, 9999)
		if parseErr != nil {
			return ConfigSnapshot{}, parseErr
		}
		modeConfig.QueueSeconds, parseErr = rawInt(raw, RPSModeTimeKey(mode, "queue"), 120, 30, 120)
		if parseErr != nil {
			return ConfigSnapshot{}, parseErr
		}
		modeConfig.GestureSeconds, parseErr = rawInt(raw, RPSModeTimeKey(mode, "gesture"), 20, 5, 20)
		if parseErr != nil {
			return ConfigSnapshot{}, parseErr
		}
		modeConfig.DealerSeconds, parseErr = rawInt(raw, RPSModeTimeKey(mode, "dealer"), 15, 5, 15)
		if parseErr != nil {
			return ConfigSnapshot{}, parseErr
		}
		modeConfig.FollowerSeconds, parseErr = rawInt(raw, RPSModeTimeKey(mode, "follower"), 15, 5, 15)
		if parseErr != nil {
			return ConfigSnapshot{}, parseErr
		}
		if modeConfig.PumpsBP.Platform+modeConfig.PumpsBP.Welfare+modeConfig.PumpsBP.Thursday >= 10000 {
			return ConfigSnapshot{}, fmt.Errorf("%w: RPS basis points", ErrInvalidConfig)
		}
		if enabled && (!rpsEnabled || base == 0) {
			return ConfigSnapshot{}, fmt.Errorf("%w: enabled RPS mode", ErrInvalidConfig)
		}
		if mode == RPSModeStandard && base > MaxMoneyMilli/RPSStandardMultiplier {
			return ConfigSnapshot{}, fmt.Errorf("%w: RPS standard 5B bound", ErrInvalidConfig)
		}
		rps.Modes[mode] = modeConfig
	}
	return ConfigSnapshot{
		GamesEnabled: master, FishingEnabled: fishingEnabled,
		Fishing: fishingConfig, Rules: rules, LinkLink: link, RPS: rps,
	}, nil
}

func rawBool(raw map[string]string, key string, fallback bool) (bool, error) {
	value, ok := raw[key]
	if !ok {
		return fallback, nil
	}
	switch value {
	case "0":
		return false, nil
	case "1":
		return true, nil
	}
	return false, fmt.Errorf("%w: %s", ErrInvalidConfig, key)
}

func rawInt(raw map[string]string, key string, fallback, minimum, maximum int) (int, error) {
	value, ok := raw[key]
	if !ok {
		return fallback, nil
	}
	parsed, err := strconv.ParseInt(value, 10, 32)
	if err != nil || strconv.FormatInt(parsed, 10) != value || parsed < int64(minimum) || parsed > int64(maximum) {
		return 0, fmt.Errorf("%w: %s", ErrInvalidConfig, key)
	}
	return int(parsed), nil
}

func rawAmount(raw map[string]string, key string, fallback, minimum int64) (int64, error) {
	value, ok := raw[key]
	if !ok {
		return fallback, nil
	}
	parsed, err := credits.ParseAmount(value)
	if err != nil || parsed < minimum || parsed > MaxMoneyMilli {
		return 0, fmt.Errorf("%w: %s", ErrInvalidConfig, key)
	}
	return parsed, nil
}

// GamesConfig is the complete administrator wire snapshot.
type GamesConfig struct {
	Revision      string             `json:"revision"`
	MasterEnabled bool               `json:"master_enabled"`
	Fishing       FishingWireConfig  `json:"fishing"`
	LinkLink      LinkLinkWireConfig `json:"linklink"`
	RPS           RPSWireConfig      `json:"rps"`
}

type FishingWireConfig struct {
	Enabled             bool                       `json:"enabled"`
	BaitPrices          FishingBaitPrices          `json:"bait_prices"`
	RTPPercent          FishingRTPPercent          `json:"rtp_percent"`
	TreasureMultipliers FishingTreasureMultipliers `json:"treasure_multipliers"`
}
type FishingBaitPrices struct {
	Worm    string `json:"worm"`
	Lure    string `json:"lure"`
	Premium string `json:"premium"`
}
type FishingRTPPercent struct {
	Standard int `json:"standard"`
	Premium  int `json:"premium"`
}
type FishingTreasureMultipliers struct {
	Bottle int `json:"bottle"`
	Clover int `json:"clover"`
	Shell  int `json:"shell"`
}
type LinkLinkWireConfig struct {
	Enabled bool                        `json:"enabled"`
	Specs   map[string]LinkLinkWireSpec `json:"specs"`
}
type LinkLinkWireSpec struct {
	Enabled bool   `json:"enabled"`
	Price   string `json:"price"`
	Seconds int    `json:"seconds,omitempty"`
}
type RPSWireConfig struct {
	Enabled bool                   `json:"enabled"`
	Modes   map[string]RPSWireMode `json:"modes"`
}
type RPSWireMode struct {
	Enabled         bool    `json:"enabled"`
	Base            string  `json:"base"`
	PumpsBP         PumpsBP `json:"pumps_bp"`
	QueueSeconds    int     `json:"queue_seconds"`
	GestureSeconds  int     `json:"gesture_seconds"`
	DealerSeconds   int     `json:"dealer_seconds"`
	FollowerSeconds int     `json:"follower_seconds"`
	QueueCapacity   int     `json:"queue_capacity"`
}

func (snapshot ConfigSnapshot) GamesConfig(revision string) GamesConfig {
	result := GamesConfig{Revision: revision, MasterEnabled: snapshot.GamesEnabled}
	result.Fishing.Enabled = snapshot.FishingEnabled
	result.Fishing.BaitPrices = FishingBaitPrices{
		Worm:    formatWireAmount(mustEntry(snapshot.Rules, fishing.BaitWorm)),
		Lure:    formatWireAmount(mustEntry(snapshot.Rules, fishing.BaitLure)),
		Premium: formatWireAmount(mustEntry(snapshot.Rules, fishing.BaitPremium)),
	}
	result.Fishing.RTPPercent = FishingRTPPercent{Standard: snapshot.Fishing.StandardRTPPercent, Premium: snapshot.Fishing.PremiumRTPPercent}
	result.Fishing.TreasureMultipliers = FishingTreasureMultipliers{
		Bottle: snapshot.Fishing.TreasureMultipliers["bottle"], Clover: snapshot.Fishing.TreasureMultipliers["clover"], Shell: snapshot.Fishing.TreasureMultipliers["shell"],
	}
	result.LinkLink = LinkLinkWireConfig{Enabled: snapshot.LinkLink.Enabled, Specs: make(map[string]LinkLinkWireSpec, 3)}
	for _, spec := range linkLinkSpecs {
		value := snapshot.LinkLink.Specs[spec]
		result.LinkLink.Specs[spec] = LinkLinkWireSpec{Enabled: value.Enabled, Price: formatWireAmount(value.PriceMilli)}
	}
	result.RPS = RPSWireConfig{Enabled: snapshot.RPS.Enabled, Modes: make(map[string]RPSWireMode, 3)}
	for _, mode := range rpsModes {
		value := snapshot.RPS.Modes[mode]
		result.RPS.Modes[mode] = RPSWireMode{Enabled: value.Enabled, Base: formatWireAmount(value.BaseMilli), PumpsBP: value.PumpsBP, QueueSeconds: value.QueueSeconds, GestureSeconds: value.GestureSeconds, DealerSeconds: value.DealerSeconds, FollowerSeconds: value.FollowerSeconds, QueueCapacity: RPSQueueCapacity}
	}
	return result
}

func mustEntry(rules *fishing.Ruleset, bait fishing.Bait) int64 {
	value, _ := rules.EntryMilli(bait)
	return value
}

func formatWireAmount(milli int64) string {
	whole, fraction := milli/1000, milli%1000
	if fraction == 0 {
		return strconv.FormatInt(whole, 10)
	}
	return strings.TrimRight(strconv.FormatInt(whole, 10)+"."+strconv.FormatInt(fraction+1000, 10)[1:], "0")
}

// FormatAmount projects a bounded milli-credit primitive to the canonical
// display-credit wire grammar.
func FormatAmount(milli int64) string { return formatWireAmount(milli) }

func parseWireAmount(value string) (int64, error) {
	if value == "" || len(value) > 32 || strings.HasPrefix(value, "+") || strings.HasPrefix(value, "-") {
		return 0, ErrInvalidConfig
	}
	parts := strings.Split(value, ".")
	if len(parts) > 2 || parts[0] == "" || (len(parts[0]) > 1 && parts[0][0] == '0') {
		return 0, ErrInvalidConfig
	}
	for _, c := range parts[0] {
		if c < '0' || c > '9' {
			return 0, ErrInvalidConfig
		}
	}
	fraction := ""
	if len(parts) == 2 {
		fraction = parts[1]
		if len(fraction) < 1 || len(fraction) > 3 {
			return 0, ErrInvalidConfig
		}
	}
	for _, c := range fraction {
		if c < '0' || c > '9' {
			return 0, ErrInvalidConfig
		}
	}
	whole, err := strconv.ParseInt(parts[0], 10, 64)
	if err != nil {
		return 0, ErrInvalidConfig
	}
	for len(fraction) < 3 {
		fraction += "0"
	}
	frac := int64(0)
	if fraction != "" {
		frac, _ = strconv.ParseInt(fraction, 10, 64)
	}
	if whole > (MaxMoneyMilli-frac)/1000 {
		return 0, ErrInvalidConfig
	}
	milli := whole*1000 + frac
	if formatWireAmount(milli) != value {
		return 0, ErrInvalidConfig
	}
	return milli, nil
}

// ParseAmount converts the canonical display-credit wire grammar to a
// bounded milli-credit primitive.
func ParseAmount(value string) (int64, error) { return parseWireAmount(value) }

// CompileGamesConfig validates a complete typed wire snapshot and returns
// both the runtime snapshot and canonical raw storage values.
func CompileGamesConfig(config GamesConfig) (ConfigSnapshot, map[string]string, error) {
	if !canonicalPositiveU128(config.Revision) {
		return ConfigSnapshot{}, nil, fmt.Errorf("%w: revision", ErrInvalidConfig)
	}
	raw := make(map[string]string, len(configKeys))
	setBool := func(key string, value bool) {
		if value {
			raw[key] = "1"
		} else {
			raw[key] = "0"
		}
	}
	setBool(GamesEnabledKey, config.MasterEnabled)
	setBool(FishingEnabledKey, config.Fishing.Enabled)
	amounts := []struct{ key, value string }{{FishingWormPriceMilliKey, config.Fishing.BaitPrices.Worm}, {FishingLurePriceMilliKey, config.Fishing.BaitPrices.Lure}, {FishingPremiumPriceMilliKey, config.Fishing.BaitPrices.Premium}}
	for _, item := range amounts {
		milli, err := parseWireAmount(item.value)
		if err != nil {
			return ConfigSnapshot{}, nil, fmt.Errorf("%w: %s", ErrInvalidConfig, item.key)
		}
		raw[item.key] = strconv.FormatInt(milli, 10)
	}
	raw[FishingStandardRTPKey] = strconv.Itoa(config.Fishing.RTPPercent.Standard)
	raw[FishingPremiumRTPKey] = strconv.Itoa(config.Fishing.RTPPercent.Premium)
	raw[FishingTreasureBottleMultiplierKey] = strconv.Itoa(config.Fishing.TreasureMultipliers.Bottle)
	raw[FishingTreasureCloverMultiplierKey] = strconv.Itoa(config.Fishing.TreasureMultipliers.Clover)
	raw[FishingTreasureShellMultiplierKey] = strconv.Itoa(config.Fishing.TreasureMultipliers.Shell)
	setBool(LinkLinkEnabledKey, config.LinkLink.Enabled)
	if len(config.LinkLink.Specs) != 3 {
		return ConfigSnapshot{}, nil, fmt.Errorf("%w: LinkLink specs", ErrInvalidConfig)
	}
	for _, spec := range linkLinkSpecs {
		value, ok := config.LinkLink.Specs[spec]
		if !ok || value.Seconds != 0 {
			return ConfigSnapshot{}, nil, fmt.Errorf("%w: LinkLink spec", ErrInvalidConfig)
		}
		setBool(LinkLinkSpecEnabledKey(spec), value.Enabled)
		milli, err := parseWireAmount(value.Price)
		if err != nil {
			return ConfigSnapshot{}, nil, err
		}
		raw[LinkLinkSpecPriceKey(spec)] = strconv.FormatInt(milli, 10)
	}
	setBool(RPSEnabledKey, config.RPS.Enabled)
	if len(config.RPS.Modes) != 3 {
		return ConfigSnapshot{}, nil, fmt.Errorf("%w: RPS modes", ErrInvalidConfig)
	}
	for _, mode := range rpsModes {
		value, ok := config.RPS.Modes[mode]
		if !ok || value.QueueCapacity != RPSQueueCapacity {
			return ConfigSnapshot{}, nil, fmt.Errorf("%w: RPS mode", ErrInvalidConfig)
		}
		setBool(RPSModeEnabledKey(mode), value.Enabled)
		milli, err := parseWireAmount(value.Base)
		if err != nil {
			return ConfigSnapshot{}, nil, err
		}
		raw[RPSModeBaseKey(mode)] = strconv.FormatInt(milli, 10)
		raw[RPSModeBPKey(mode, "platform")] = strconv.Itoa(value.PumpsBP.Platform)
		raw[RPSModeBPKey(mode, "welfare")] = strconv.Itoa(value.PumpsBP.Welfare)
		raw[RPSModeBPKey(mode, "thursday")] = strconv.Itoa(value.PumpsBP.Thursday)
		raw[RPSModeTimeKey(mode, "queue")] = strconv.Itoa(value.QueueSeconds)
		raw[RPSModeTimeKey(mode, "gesture")] = strconv.Itoa(value.GestureSeconds)
		raw[RPSModeTimeKey(mode, "dealer")] = strconv.Itoa(value.DealerSeconds)
		raw[RPSModeTimeKey(mode, "follower")] = strconv.Itoa(value.FollowerSeconds)
	}
	snapshot, err := CompileConfig(raw)
	if err != nil {
		return ConfigSnapshot{}, nil, err
	}
	return snapshot, raw, nil
}

func canonicalPositiveU128(value string) bool {
	if value == "" || (len(value) > 1 && value[0] == '0') {
		return false
	}
	for _, c := range value {
		if c < '0' || c > '9' {
			return false
		}
	}
	n, ok := new(big.Int).SetString(value, 10)
	return ok && n.Sign() > 0 && n.BitLen() <= 128
}

// GamesConfigPatch is a strict nested partial. Decoding must use
// DecodeGamesConfigPatch so duplicate keys, nulls, empty objects and read-only
// fields are rejected before merge.
type GamesConfigPatch struct {
	ExpectedRevision string               `json:"expected_revision"`
	MasterEnabled    *bool                `json:"master_enabled,omitempty"`
	Fishing          *FishingConfigPatch  `json:"fishing,omitempty"`
	LinkLink         *LinkLinkConfigPatch `json:"linklink,omitempty"`
	RPS              *RPSConfigPatch      `json:"rps,omitempty"`
}
type FishingConfigPatch struct {
	Enabled             *bool                   `json:"enabled,omitempty"`
	BaitPrices          *FishingBaitPricesPatch `json:"bait_prices,omitempty"`
	RTPPercent          *FishingRTPPatch        `json:"rtp_percent,omitempty"`
	TreasureMultipliers *FishingTreasurePatch   `json:"treasure_multipliers,omitempty"`
}
type FishingBaitPricesPatch struct {
	Worm    *string `json:"worm,omitempty"`
	Lure    *string `json:"lure,omitempty"`
	Premium *string `json:"premium,omitempty"`
}
type FishingRTPPatch struct {
	Standard *int `json:"standard,omitempty"`
	Premium  *int `json:"premium,omitempty"`
}
type FishingTreasurePatch struct {
	Bottle *int `json:"bottle,omitempty"`
	Clover *int `json:"clover,omitempty"`
	Shell  *int `json:"shell,omitempty"`
}
type LinkLinkConfigPatch struct {
	Enabled *bool                         `json:"enabled,omitempty"`
	Specs   *map[string]LinkLinkSpecPatch `json:"specs,omitempty"`
}
type LinkLinkSpecPatch struct {
	Enabled *bool   `json:"enabled,omitempty"`
	Price   *string `json:"price,omitempty"`
}
type RPSConfigPatch struct {
	Enabled *bool                    `json:"enabled,omitempty"`
	Modes   *map[string]RPSModePatch `json:"modes,omitempty"`
}
type RPSModePatch struct {
	Enabled         *bool         `json:"enabled,omitempty"`
	Base            *string       `json:"base,omitempty"`
	PumpsBP         *PumpsBPPatch `json:"pumps_bp,omitempty"`
	QueueSeconds    *int          `json:"queue_seconds,omitempty"`
	GestureSeconds  *int          `json:"gesture_seconds,omitempty"`
	DealerSeconds   *int          `json:"dealer_seconds,omitempty"`
	FollowerSeconds *int          `json:"follower_seconds,omitempty"`
}
type PumpsBPPatch struct {
	Platform *int `json:"platform,omitempty"`
	Welfare  *int `json:"welfare,omitempty"`
	Thursday *int `json:"thursday,omitempty"`
}

func DecodeGamesConfigPatch(data []byte) (GamesConfigPatch, error) {
	if len(data) == 0 || len(data) > 65536 {
		return GamesConfigPatch{}, ErrInvalidConfig
	}
	if err := validateJSONObject(data); err != nil {
		return GamesConfigPatch{}, err
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	var patch GamesConfigPatch
	if err := decoder.Decode(&patch); err != nil {
		return GamesConfigPatch{}, fmt.Errorf("%w: %v", ErrInvalidConfig, err)
	}
	if err := decoder.Decode(new(any)); err != io.EOF {
		return GamesConfigPatch{}, ErrInvalidConfig
	}
	if !canonicalPositiveU128(patch.ExpectedRevision) || !patch.hasMutation() {
		return GamesConfigPatch{}, ErrInvalidConfig
	}
	return patch, nil
}

func validateJSONObject(data []byte) error {
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.UseNumber()
	if err := walkJSON(decoder, true); err != nil {
		return fmt.Errorf("%w: %v", ErrInvalidConfig, err)
	}
	if _, err := decoder.Token(); err != io.EOF {
		return ErrInvalidConfig
	}
	return nil
}

func walkJSON(decoder *json.Decoder, requireObject bool) error {
	token, err := decoder.Token()
	if err != nil {
		return err
	}
	delim, ok := token.(json.Delim)
	if !ok || (requireObject && delim != '{') {
		if token == nil {
			return fmt.Errorf("null")
		}
		return nil
	}
	switch delim {
	case '{':
		seen := map[string]bool{}
		count := 0
		for decoder.More() {
			keyToken, _ := decoder.Token()
			key, ok := keyToken.(string)
			if !ok || seen[key] {
				return fmt.Errorf("duplicate key")
			}
			seen[key] = true
			count++
			if err := walkJSON(decoder, false); err != nil {
				return err
			}
		}
		if _, err := decoder.Token(); err != nil {
			return err
		}
		if count == 0 {
			return fmt.Errorf("empty object")
		}
	case '[':
		for decoder.More() {
			if err := walkJSON(decoder, false); err != nil {
				return err
			}
		}
		_, err = decoder.Token()
		return err
	default:
		return fmt.Errorf("invalid JSON")
	}
	return nil
}

func (patch GamesConfigPatch) hasMutation() bool {
	return patch.MasterEnabled != nil || patch.Fishing != nil || patch.LinkLink != nil || patch.RPS != nil
}

func (patch GamesConfigPatch) Merge(current GamesConfig) (GamesConfig, error) {
	if patch.ExpectedRevision != current.Revision {
		return GamesConfig{}, ErrRevisionConflict
	}
	if patch.MasterEnabled != nil {
		current.MasterEnabled = *patch.MasterEnabled
	}
	if p := patch.Fishing; p != nil {
		if p.Enabled != nil {
			current.Fishing.Enabled = *p.Enabled
		}
		if p.BaitPrices != nil {
			if p.BaitPrices.Worm != nil {
				current.Fishing.BaitPrices.Worm = *p.BaitPrices.Worm
			}
			if p.BaitPrices.Lure != nil {
				current.Fishing.BaitPrices.Lure = *p.BaitPrices.Lure
			}
			if p.BaitPrices.Premium != nil {
				current.Fishing.BaitPrices.Premium = *p.BaitPrices.Premium
			}
		}
		if p.RTPPercent != nil {
			if p.RTPPercent.Standard != nil {
				current.Fishing.RTPPercent.Standard = *p.RTPPercent.Standard
			}
			if p.RTPPercent.Premium != nil {
				current.Fishing.RTPPercent.Premium = *p.RTPPercent.Premium
			}
		}
		if p.TreasureMultipliers != nil {
			if p.TreasureMultipliers.Bottle != nil {
				current.Fishing.TreasureMultipliers.Bottle = *p.TreasureMultipliers.Bottle
			}
			if p.TreasureMultipliers.Clover != nil {
				current.Fishing.TreasureMultipliers.Clover = *p.TreasureMultipliers.Clover
			}
			if p.TreasureMultipliers.Shell != nil {
				current.Fishing.TreasureMultipliers.Shell = *p.TreasureMultipliers.Shell
			}
		}
	}
	if p := patch.LinkLink; p != nil {
		if p.Enabled != nil {
			current.LinkLink.Enabled = *p.Enabled
		}
		if p.Specs != nil {
			for spec, v := range *p.Specs {
				old, ok := current.LinkLink.Specs[spec]
				if !ok {
					return GamesConfig{}, ErrUnknownSpec
				}
				if v.Enabled != nil {
					old.Enabled = *v.Enabled
				}
				if v.Price != nil {
					old.Price = *v.Price
				}
				current.LinkLink.Specs[spec] = old
			}
		}
	}
	if p := patch.RPS; p != nil {
		if p.Enabled != nil {
			current.RPS.Enabled = *p.Enabled
		}
		if p.Modes != nil {
			for mode, v := range *p.Modes {
				old, ok := current.RPS.Modes[mode]
				if !ok {
					return GamesConfig{}, ErrUnknownMode
				}
				if v.Enabled != nil {
					old.Enabled = *v.Enabled
				}
				if v.Base != nil {
					old.Base = *v.Base
				}
				if v.QueueSeconds != nil {
					old.QueueSeconds = *v.QueueSeconds
				}
				if v.GestureSeconds != nil {
					old.GestureSeconds = *v.GestureSeconds
				}
				if v.DealerSeconds != nil {
					old.DealerSeconds = *v.DealerSeconds
				}
				if v.FollowerSeconds != nil {
					old.FollowerSeconds = *v.FollowerSeconds
				}
				if v.PumpsBP != nil {
					if v.PumpsBP.Platform != nil {
						old.PumpsBP.Platform = *v.PumpsBP.Platform
					}
					if v.PumpsBP.Welfare != nil {
						old.PumpsBP.Welfare = *v.PumpsBP.Welfare
					}
					if v.PumpsBP.Thursday != nil {
						old.PumpsBP.Thursday = *v.PumpsBP.Thursday
					}
				}
				current.RPS.Modes[mode] = old
			}
		}
	}
	return current, nil
}
