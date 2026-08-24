// Package game owns the closed game-module registry and shared runtime
// configuration contracts. It deliberately contains no HTTP or persistence
// code; repositories consume the typed snapshot after reading it inside their
// own transaction.
package game

import (
	"errors"
	"fmt"
	"strconv"

	"github.com/waiting-here/NonbiriAPI/internal/game/fishing"
)

const (
	FishingID      = "fishing"
	FishingVersion = 1

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
)

var (
	ErrUnknownGame   = errors.New("game: unknown game module")
	ErrInvalidConfig = errors.New("game: invalid configuration")
)

// configKeys is the complete, stable set read for one fishing start.
var configKeys = [...]string{
	GamesEnabledKey,
	FishingEnabledKey,
	FishingWormPriceMilliKey,
	FishingLurePriceMilliKey,
	FishingPremiumPriceMilliKey,
	FishingStandardRTPKey,
	FishingPremiumRTPKey,
	FishingTreasureBottleMultiplierKey,
	FishingTreasureCloverMultiplierKey,
	FishingTreasureShellMultiplierKey,
}

// SiteConfigKeys returns a copy of the complete configuration key set.
func SiteConfigKeys() []string {
	return append([]string(nil), configKeys[:]...)
}

// Module identifies one immutable game protocol generation.
type Module struct {
	ID      string
	Version int
}

var registry = map[string]Module{
	registryKey(FishingID, FishingVersion): {ID: FishingID, Version: FishingVersion},
}

func registryKey(id string, version int) string {
	return id + "@" + strconv.Itoa(version)
}

// Resolve returns the registered module identity. Unknown identifiers and
// versions fail closed; there is no default or protocol fallback.
func Resolve(id string, version int) (Module, error) {
	module, ok := registry[registryKey(id, version)]
	if !ok {
		return Module{}, ErrUnknownGame
	}
	return module, nil
}

// ConfigSnapshot is one fully compiled fishing economy snapshot. Rules is
// immutable and safe for concurrent use; Fishing is a fresh config value for
// each compile and consumers treat its maps as read-only. Starts build the
// snapshot from transaction-local values so an update cannot split one round
// across generations.
type ConfigSnapshot struct {
	GamesEnabled   bool
	FishingEnabled bool
	Fishing        fishing.Config
	Rules          *fishing.Ruleset
}

// CompileConfig converts raw site_config values into a complete snapshot.
// Missing economy rows use the frozen rules defaults. Toggle corruption is
// treated as disabled; malformed economy rows reject the entire snapshot.
func CompileConfig(raw map[string]string) (ConfigSnapshot, error) {
	config := fishing.DefaultConfig()
	if value, ok := raw[FishingWormPriceMilliKey]; ok {
		config.BaitPricesMilli[fishing.BaitWorm] = value
	}
	if value, ok := raw[FishingLurePriceMilliKey]; ok {
		config.BaitPricesMilli[fishing.BaitLure] = value
	}
	if value, ok := raw[FishingPremiumPriceMilliKey]; ok {
		config.BaitPricesMilli[fishing.BaitPremium] = value
	}

	var err error
	if value, ok := raw[FishingStandardRTPKey]; ok {
		config.StandardRTPPercent, err = parseCanonicalInt(value, 0, 100)
		if err != nil {
			return ConfigSnapshot{}, fmt.Errorf("%w: standard rtp", ErrInvalidConfig)
		}
	}
	if value, ok := raw[FishingPremiumRTPKey]; ok {
		config.PremiumRTPPercent, err = parseCanonicalInt(value, 0, 100)
		if err != nil {
			return ConfigSnapshot{}, fmt.Errorf("%w: premium rtp", ErrInvalidConfig)
		}
	}
	for _, candidate := range []struct {
		key     string
		species string
	}{
		{key: FishingTreasureBottleMultiplierKey, species: "bottle"},
		{key: FishingTreasureCloverMultiplierKey, species: "clover"},
		{key: FishingTreasureShellMultiplierKey, species: "shell"},
	} {
		if value, ok := raw[candidate.key]; ok {
			multiplier, parseErr := parseCanonicalInt(value, 1, 1000)
			if parseErr != nil {
				return ConfigSnapshot{}, fmt.Errorf("%w: treasure multiplier", ErrInvalidConfig)
			}
			config.TreasureMultipliers[candidate.species] = multiplier
		}
	}
	rules, err := fishing.Compile(config)
	if err != nil {
		return ConfigSnapshot{}, fmt.Errorf("%w: %v", ErrInvalidConfig, err)
	}
	return ConfigSnapshot{
		GamesEnabled:   raw[GamesEnabledKey] == "1",
		FishingEnabled: raw[FishingEnabledKey] == "1",
		Fishing:        config,
		Rules:          rules,
	}, nil
}

func parseCanonicalInt(raw string, minimum, maximum int) (int, error) {
	value, err := strconv.ParseInt(raw, 10, 32)
	if err != nil || strconv.FormatInt(value, 10) != raw || value < int64(minimum) || value > int64(maximum) {
		return 0, ErrInvalidConfig
	}
	return int(value), nil
}
