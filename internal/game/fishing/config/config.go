package config

import (
	"encoding/json"
	"fmt"
	"maps"
	"strconv"

	"github.com/waiting-here/NonbiriAPI/internal/game"
	"github.com/waiting-here/NonbiriAPI/internal/game/fishing"
)

const (
	FishingEnabledKey                  = "game_fishing_enabled"
	FishingRakePlatformBPKey           = "game_fishing_rake_platform_bp"
	FishingRakeWelfareBPKey            = "game_fishing_rake_welfare_bp"
	FishingRakeThursdayBPKey           = "game_fishing_rake_thursday_bp"
	FishingWormPriceMilliKey           = "game_fishing_bait_worm_price_milli"
	FishingLurePriceMilliKey           = "game_fishing_bait_lure_price_milli"
	FishingPremiumPriceMilliKey        = "game_fishing_bait_premium_price_milli"
	FishingStandardRTPKey              = "game_fishing_rtp"
	FishingPremiumRTPKey               = "game_fishing_rtp_premium"
	FishingBlueFishChanceBPSKey        = "game_fishing_blue_fish_chance_bps"
	FishingTreasureBottleMultiplierKey = "game_fishing_treasure_bottle_mult"
	FishingTreasureCloverMultiplierKey = "game_fishing_treasure_clover_mult"
	FishingTreasureShellMultiplierKey  = "game_fishing_treasure_shell_mult"
)

type FishingWireConfig struct {
	BlueFishChanceBPS   int                        `json:"blue_fish_chance_bps"`
	RakeBP              fishing.RakeBasisPoints    `json:"rake_bp"`
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
type FishingRakePatch struct {
	Platform *int `json:"platform,omitempty"`
	Welfare  *int `json:"welfare,omitempty"`
	Thursday *int `json:"thursday,omitempty"`
}
type FishingConfigPatch struct {
	BlueFishChanceBPS   *int                    `json:"blue_fish_chance_bps,omitempty"`
	RakeBP              *FishingRakePatch       `json:"rake_bp,omitempty"`
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
type Snapshot struct {
	GamesEnabled   bool
	FishingEnabled bool
	Fishing        fishing.Config
	Rules          *fishing.Ruleset
}

func CompileConfig(raw map[string]string) (Snapshot, error) {
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
	standardRTP, err := game.RawInt(raw, FishingStandardRTPKey, fishingConfig.StandardRTPPercent, fishing.MinimumRTPPercent, fishing.MaximumRTPPercent)
	if err != nil {
		return Snapshot{}, err
	}
	premiumRTP, err := game.RawInt(raw, FishingPremiumRTPKey, fishingConfig.PremiumRTPPercent, fishing.MinimumRTPPercent, fishing.MaximumRTPPercent)
	if err != nil {
		return Snapshot{}, err
	}
	fishingConfig.StandardRTPPercent = standardRTP
	fishingConfig.PremiumRTPPercent = premiumRTP
	chance, err := game.RawInt(raw, FishingBlueFishChanceBPSKey, fishingConfig.BlueFishChanceBPS, 0, fishing.MaximumBlueFishChanceBPS)
	if err != nil {
		return Snapshot{}, err
	}
	fishingConfig.BlueFishChanceBPS = chance
	for species, key := range map[string]string{
		"bottle": FishingTreasureBottleMultiplierKey,
		"clover": FishingTreasureCloverMultiplierKey,
		"shell":  FishingTreasureShellMultiplierKey,
	} {
		value, parseErr := game.RawInt(raw, key, fishingConfig.TreasureMultipliers[species], fishing.MinimumTreasureMultiplier, fishing.MaximumTreasureMultiplier)
		if parseErr != nil {
			return Snapshot{}, parseErr
		}
		fishingConfig.TreasureMultipliers[species] = value
	}
	for _, item := range []struct {
		key    string
		target *int
	}{
		{FishingRakePlatformBPKey, &fishingConfig.RakeBP.Platform},
		{FishingRakeWelfareBPKey, &fishingConfig.RakeBP.Welfare},
		{FishingRakeThursdayBPKey, &fishingConfig.RakeBP.Thursday},
	} {
		value, err := game.RawInt(raw, item.key, *item.target, 0, 9999)
		if err != nil {
			return Snapshot{}, err
		}
		*item.target = value
	}
	rules, err := fishing.Compile(fishingConfig)
	if err != nil {
		return Snapshot{}, fmt.Errorf("%w: %v", game.ErrInvalidConfig, err)
	}
	for _, bait := range []fishing.Bait{fishing.BaitWorm, fishing.BaitLure, fishing.BaitPremium} {
		entry, _ := rules.EntryMilli(bait)
		maximum, _ := rules.MaximumPayoutMilli(bait)
		if entry > game.MaxMoneyMilli/10 || maximum > game.MaxMoneyMilli/10 {
			return Snapshot{}, fmt.Errorf("%w: fishing ten-draw bound", game.ErrInvalidConfig)
		}
	}

	master, err := game.RawBool(raw, game.GamesEnabledKey, false)
	if err != nil {
		return Snapshot{}, err
	}
	enabled, err := game.RawBool(raw, FishingEnabledKey, false)
	if err != nil || enabled && !master {
		return Snapshot{}, game.ErrInvalidConfig
	}
	return Snapshot{GamesEnabled: master, FishingEnabled: enabled, Fishing: fishingConfig, Rules: rules}, nil
}

func mustEntry(rules *fishing.Ruleset, bait fishing.Bait) int64 {
	value, _ := rules.EntryMilli(bait)
	return value
}

func (snapshot Snapshot) Wire() FishingWireConfig {
	var result FishingWireConfig
	result.Enabled = snapshot.FishingEnabled
	result.BlueFishChanceBPS = snapshot.Fishing.BlueFishChanceBPS
	result.RakeBP = snapshot.Fishing.RakeBP
	result.BaitPrices = FishingBaitPrices{
		Worm:    game.FormatAmount(mustEntry(snapshot.Rules, fishing.BaitWorm)),
		Lure:    game.FormatAmount(mustEntry(snapshot.Rules, fishing.BaitLure)),
		Premium: game.FormatAmount(mustEntry(snapshot.Rules, fishing.BaitPremium)),
	}
	result.RTPPercent = FishingRTPPercent{Standard: snapshot.Fishing.StandardRTPPercent, Premium: snapshot.Fishing.PremiumRTPPercent}
	result.TreasureMultipliers = FishingTreasureMultipliers{
		Bottle: snapshot.Fishing.TreasureMultipliers["bottle"], Clover: snapshot.Fishing.TreasureMultipliers["clover"], Shell: snapshot.Fishing.TreasureMultipliers["shell"],
	}
	return result
}
func wireRaw(config FishingWireConfig) (map[string]string, error) {
	raw := map[string]string{}
	setBool := func(key string, value bool) { raw[key] = game.BoolRaw(value) }
	setBool(FishingEnabledKey, config.Enabled)
	amounts := []struct{ key, value string }{{FishingWormPriceMilliKey, config.BaitPrices.Worm}, {FishingLurePriceMilliKey, config.BaitPrices.Lure}, {FishingPremiumPriceMilliKey, config.BaitPrices.Premium}}
	for _, item := range amounts {
		milli, err := game.ParseAmount(item.value)
		if err != nil {
			return nil, fmt.Errorf("%w: %s", game.ErrInvalidConfig, item.key)
		}
		raw[item.key] = strconv.FormatInt(milli, 10)
	}
	raw[FishingRakePlatformBPKey] = strconv.Itoa(config.RakeBP.Platform)
	raw[FishingRakeWelfareBPKey] = strconv.Itoa(config.RakeBP.Welfare)
	raw[FishingRakeThursdayBPKey] = strconv.Itoa(config.RakeBP.Thursday)
	raw[FishingStandardRTPKey] = strconv.Itoa(config.RTPPercent.Standard)
	raw[FishingPremiumRTPKey] = strconv.Itoa(config.RTPPercent.Premium)
	raw[FishingBlueFishChanceBPSKey] = strconv.Itoa(config.BlueFishChanceBPS)
	raw[FishingTreasureBottleMultiplierKey] = strconv.Itoa(config.TreasureMultipliers.Bottle)
	raw[FishingTreasureCloverMultiplierKey] = strconv.Itoa(config.TreasureMultipliers.Clover)
	raw[FishingTreasureShellMultiplierKey] = strconv.Itoa(config.TreasureMultipliers.Shell)
	return raw, nil
}

type Codec struct{}
type compiled struct {
	snapshot Snapshot
	raw      map[string]string
}

func (Codec) Keys() []string {
	return []string{FishingRakePlatformBPKey, FishingRakeWelfareBPKey, FishingRakeThursdayBPKey, FishingEnabledKey, FishingWormPriceMilliKey, FishingLurePriceMilliKey, FishingPremiumPriceMilliKey, FishingStandardRTPKey, FishingPremiumRTPKey, FishingBlueFishChanceBPSKey, FishingTreasureBottleMultiplierKey, FishingTreasureCloverMultiplierKey, FishingTreasureShellMultiplierKey}
}
func (Codec) Compile(raw map[string]string) (game.ConfigValue, error) {
	snapshot, err := CompileConfig(raw)
	if err != nil {
		return nil, err
	}
	canonical, err := wireRaw(snapshot.Wire())
	if err != nil {
		return nil, err
	}
	return compiled{snapshot: snapshot, raw: canonical}, nil
}
func (Codec) Merge(current game.ConfigValue, body json.RawMessage, master bool) (game.ConfigValue, error) {
	value, ok := current.(compiled)
	if !ok {
		return nil, game.ErrInvalidConfig
	}
	var patch FishingConfigPatch
	if err := game.DecodeConfigPatch(body, &patch); err != nil {
		return nil, err
	}
	var next FishingWireConfig
	if err := game.MergeConfigFields(value.Wire(), body, &next); err != nil {
		return nil, err
	}
	raw, err := wireRaw(next)
	if err != nil {
		return nil, err
	}
	raw[game.GamesEnabledKey] = game.BoolRaw(master)
	return (Codec{}).Compile(raw)
}
func (value compiled) Enabled() bool          { return value.snapshot.FishingEnabled }
func (value compiled) NeedsReady() bool       { return false }
func (value compiled) Raw() map[string]string { return maps.Clone(value.raw) }
func (value compiled) Wire() json.RawMessage  { return game.ConfigJSON(value.snapshot.Wire()) }
func (value compiled) UserWire(available func(mode, spec string) bool) json.RawMessage {
	return game.ConfigJSON(struct {
		Enabled           bool              `json:"enabled"`
		Available         bool              `json:"available"`
		BaitPrices        FishingBaitPrices `json:"bait_prices"`
		BlueFishChanceBPS int               `json:"blue_fish_chance_bps"`
	}{value.snapshot.FishingEnabled, available("", ""), value.snapshot.Wire().BaitPrices, value.snapshot.Fishing.BlueFishChanceBPS})
}
