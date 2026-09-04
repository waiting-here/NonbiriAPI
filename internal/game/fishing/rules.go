// Package fishing implements the deterministic, storage-neutral rules for
// the pond-fishing game. It owns configuration validation, unbiased outcome
// selection, exact rational RTP calculation, and milli-credit payout intent;
// it deliberately knows nothing about HTTP, SQLite, users, or ledgers.
package fishing

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"math"
	"math/big"
	"strconv"
	"strings"

	"github.com/waiting-here/NonbiriAPI/internal/credits"
)

// Bait identifies one frozen fishing price/probability profile.
type Bait string

const (
	BaitWorm    Bait = "worm"
	BaitLure    Bait = "lure"
	BaitPremium Bait = "premium"
)

// Tier identifies a fish rarity or a non-fish outcome class.
type Tier string

const (
	TierJunk     Tier = "junk"
	TierSmall    Tier = "small"
	TierRegular  Tier = "regular"
	TierBig      Tier = "big"
	TierGiant    Tier = "giant"
	TierLegend   Tier = "legend"
	TierTreasure Tier = "treasure"
)

var (
	// ErrInvalidConfig means the entire proposed three-bait snapshot is
	// invalid. Callers must not partially apply a snapshot that returns it.
	ErrInvalidConfig = errors.New("fishing: invalid configuration")
	// ErrUnknownBait rejects a bait outside the frozen registry.
	ErrUnknownBait = errors.New("fishing: unknown bait")
)

var baitOrder = [...]Bait{BaitWorm, BaitLure, BaitPremium}
var fishTierOrder = [...]Tier{TierSmall, TierRegular, TierBig, TierGiant, TierLegend}
var treasureOrder = [...]string{"bottle", "clover", "shell"}

const (
	MinimumBaitPriceMilli     int64 = 1
	MinimumRTPPercent               = 0
	MaximumRTPPercent               = 100
	MinimumTreasureMultiplier       = 1
	MaximumTreasureMultiplier       = 1000
)

// Species is a server-authoritative fish entry and its inclusive centimetre
// interval.
type Species struct {
	Key           string
	Tier          Tier
	MinCentimetre int
	MaxCentimetre int
}

// Config is one atomic economic snapshot. Prices are canonical decimal
// strings in milli-credits; no display-credit or floating-point value enters
// the rules package.
type Config struct {
	BaitPricesMilli     map[Bait]string
	StandardRTPPercent  int
	PremiumRTPPercent   int
	TreasureMultipliers map[string]int
}

// DefaultConfig returns a fresh copy of the frozen fishing defaults.
func DefaultConfig() Config {
	return Config{
		BaitPricesMilli: map[Bait]string{
			BaitWorm:    "2500000",
			BaitLure:    "5000000",
			BaitPremium: "7500000",
		},
		StandardRTPPercent: 90,
		PremiumRTPPercent:  88,
		TreasureMultipliers: map[string]int{
			"bottle": 2,
			"clover": 3,
			"shell":  5,
		},
	}
}

// Outcome is the complete non-economic result selected by the rules engine.
// SizeCentimetre is zero for junk and treasure.
type Outcome struct {
	Bait           Bait
	Key            string
	Tier           Tier
	SizeCentimetre int
}

// SettlementIntent contains only checked milli-credit amounts. The central
// game settlement owner remains responsible for applying it atomically.
type SettlementIntent struct {
	EntryMilli  int64
	PayoutMilli int64
}

// Result pairs a server-generated outcome with its pure settlement intent.
type Result struct {
	Outcome    Outcome
	Settlement SettlementIntent
}

// Evidence exposes exact, human-auditable rational calculations without
// leaking mutable big.Rat values from the compiled ruleset.
type Evidence struct {
	Bait                          Bait
	EntryMilli                    string
	PositiveWeight                uint64
	SampleSpace                   uint64
	TargetRTP                     string
	FishEV                        string
	TreasureEV                    string
	Scale                         string
	PreRoundRTP                   string
	RoundedRTP                    string
	ExpectedRoundingErrorMilli    string
	StrictRoundingErrorBoundMilli string
}

type tierDefinition struct {
	tier Tier
	min  int
	max  int
	base *big.Rat
	span *big.Rat
}

type baitDefinition struct {
	bait    Bait
	weights map[string]int64
}

type definitions struct {
	tiers     []tierDefinition
	species   []Species
	junk      []string
	treasures []string
	baits     []baitDefinition
}

type weightedOutcome struct {
	key    string
	tier   Tier
	weight uint64
}

type compiledBait struct {
	entry              int64
	positiveWeight     uint64
	sampleSpace        uint64
	weighted           []weightedOutcome
	target             *big.Rat
	fishEV             *big.Rat
	treasureEV         *big.Rat
	scale              *big.Rat
	fishPayoutByTierCM map[Tier]map[int]int64
	treasurePayout     map[string]int64
	roundedRTP         *big.Rat
	roundingErrorMilli *big.Rat
	roundingBoundMilli *big.Rat
}

// Ruleset is an immutable, fully validated three-bait snapshot.
type Ruleset struct {
	baits         map[Bait]*compiledBait
	speciesByTier map[Tier][]Species
	junk          []string
}

// EntryMilli returns the frozen entry price for one bait.
func (rules *Ruleset) EntryMilli(bait Bait) (int64, error) {
	if rules == nil {
		return 0, fmt.Errorf("%w: nil ruleset", ErrInvalidConfig)
	}
	compiled, ok := rules.baits[bait]
	if !ok {
		return 0, ErrUnknownBait
	}
	return compiled.entry, nil
}

// MaximumPayoutMilli returns the largest possible payout for one draw. It is
// used by the complete configuration compiler to prove the ten-draw bound.
func (rules *Ruleset) MaximumPayoutMilli(bait Bait) (int64, error) {
	if rules == nil {
		return 0, fmt.Errorf("%w: nil ruleset", ErrInvalidConfig)
	}
	compiled, ok := rules.baits[bait]
	if !ok {
		return 0, ErrUnknownBait
	}
	var maximum int64
	for _, payouts := range compiled.fishPayoutByTierCM {
		for _, payout := range payouts {
			if payout > maximum {
				maximum = payout
			}
		}
	}
	for _, payout := range compiled.treasurePayout {
		if payout > maximum {
			maximum = payout
		}
	}
	return maximum, nil
}

// Compile validates the complete candidate and returns it only if every bait,
// outcome, payout, and exact-RTP calculation is valid.
func Compile(config Config) (*Ruleset, error) {
	return compileWithDefinitions(config, frozenDefinitions())
}

// Roll selects an outcome with the frozen 1/5 junk sample space and returns
// its prevalidated settlement intent.
func (rules *Ruleset) Roll(bait Bait, source IntSource) (Result, error) {
	if rules == nil {
		return Result{}, fmt.Errorf("%w: nil ruleset", ErrInvalidConfig)
	}
	compiled, ok := rules.baits[bait]
	if !ok {
		return Result{}, ErrUnknownBait
	}

	primary, err := draw(source, compiled.sampleSpace)
	if err != nil {
		return Result{}, err
	}
	if primary < compiled.positiveWeight {
		index, err := draw(source, uint64(len(rules.junk)))
		if err != nil {
			return Result{}, err
		}
		return Result{
			Outcome:    Outcome{Bait: bait, Key: rules.junk[index], Tier: TierJunk},
			Settlement: SettlementIntent{EntryMilli: compiled.entry},
		}, nil
	}

	remaining := primary - compiled.positiveWeight
	for _, candidate := range compiled.weighted {
		width := candidate.weight * 4
		if remaining >= width {
			remaining -= width
			continue
		}
		if candidate.tier == TierTreasure {
			return Result{
				Outcome: Outcome{Bait: bait, Key: candidate.key, Tier: TierTreasure},
				Settlement: SettlementIntent{
					EntryMilli:  compiled.entry,
					PayoutMilli: compiled.treasurePayout[candidate.key],
				},
			}, nil
		}

		species := rules.speciesByTier[candidate.tier]
		speciesIndex, err := draw(source, uint64(len(species)))
		if err != nil {
			return Result{}, err
		}
		selected := species[speciesIndex]
		sizeWidth := uint64(selected.MaxCentimetre-selected.MinCentimetre) + 1
		sizeOffset, err := draw(source, sizeWidth)
		if err != nil {
			return Result{}, err
		}
		size := selected.MinCentimetre + int(sizeOffset)
		return Result{
			Outcome: Outcome{
				Bait:           bait,
				Key:            selected.Key,
				Tier:           selected.Tier,
				SizeCentimetre: size,
			},
			Settlement: SettlementIntent{
				EntryMilli:  compiled.entry,
				PayoutMilli: compiled.fishPayoutByTierCM[selected.Tier][size],
			},
		}, nil
	}

	return Result{}, fmt.Errorf("%w: probability intervals do not close", ErrInvalidConfig)
}

// RollCrypto is the production convenience path backed by crypto/rand.Reader.
func (rules *Ruleset) RollCrypto(bait Bait) (Result, error) {
	return rules.Roll(bait, CryptoSource{})
}

// RollBatch draws exactly count independent ordered outcomes. Only the frozen
// public batch sizes are accepted. Any source failure discards the whole
// in-memory batch; callers persist only after this function succeeds.
func (rules *Ruleset) RollBatch(bait Bait, count int, source IntSource) ([]Result, error) {
	if count != 1 && count != 10 {
		return nil, fmt.Errorf("%w: count must be 1 or 10", ErrInvalidConfig)
	}
	results := make([]Result, count)
	for ordinal := range results {
		result, err := rules.Roll(bait, source)
		if err != nil {
			return nil, err
		}
		results[ordinal] = result
	}
	return results, nil
}

// Evidence returns exact calculation evidence for one bait.
func (rules *Ruleset) Evidence(bait Bait) (Evidence, error) {
	if rules == nil {
		return Evidence{}, fmt.Errorf("%w: nil ruleset", ErrInvalidConfig)
	}
	compiled, ok := rules.baits[bait]
	if !ok {
		return Evidence{}, ErrUnknownBait
	}
	return Evidence{
		Bait:                          bait,
		EntryMilli:                    credits.FormatAmount(compiled.entry),
		PositiveWeight:                compiled.positiveWeight,
		SampleSpace:                   compiled.sampleSpace,
		TargetRTP:                     compiled.target.RatString(),
		FishEV:                        compiled.fishEV.RatString(),
		TreasureEV:                    compiled.treasureEV.RatString(),
		Scale:                         compiled.scale.RatString(),
		PreRoundRTP:                   new(big.Rat).Add(compiled.treasureEV, new(big.Rat).Mul(compiled.fishEV, compiled.scale)).RatString(),
		RoundedRTP:                    compiled.roundedRTP.RatString(),
		ExpectedRoundingErrorMilli:    compiled.roundingErrorMilli.RatString(),
		StrictRoundingErrorBoundMilli: compiled.roundingBoundMilli.RatString(),
	}, nil
}

// FishRoster returns a copy of the frozen 23-species registry.
func FishRoster() []Species {
	definitions := frozenDefinitions()
	return append([]Species(nil), definitions.species...)
}

// JunkRoster returns a copy of the frozen eight-item junk registry.
func JunkRoster() []string {
	definitions := frozenDefinitions()
	return append([]string(nil), definitions.junk...)
}

// TreasureRoster returns a copy of the frozen three-item treasure registry.
func TreasureRoster() []string {
	definitions := frozenDefinitions()
	return append([]string(nil), definitions.treasures...)
}

// FrozenRulesHash is the SHA-256 of a canonical, versioned representation of
// every frozen roster, weight, multiplier, RTP, and milli-credit default.
func FrozenRulesHash() string {
	sum := sha256.Sum256([]byte(canonicalRules(frozenDefinitions(), DefaultConfig())))
	return hex.EncodeToString(sum[:])
}

func canonicalRules(defs definitions, config Config) string {
	var builder strings.Builder
	builder.WriteString("fishing-rules-v1\nprices_milli=")
	for index, bait := range baitOrder {
		if index > 0 {
			builder.WriteByte(',')
		}
		builder.WriteString(string(bait))
		builder.WriteByte(':')
		builder.WriteString(config.BaitPricesMilli[bait])
	}
	builder.WriteString("\nrtp=")
	for index, bait := range baitOrder {
		if index > 0 {
			builder.WriteByte(',')
		}
		percent := config.StandardRTPPercent
		if bait == BaitPremium {
			percent = config.PremiumRTPPercent
		}
		builder.WriteString(string(bait))
		builder.WriteByte(':')
		builder.WriteString(new(big.Rat).SetFrac64(int64(percent), 100).RatString())
	}
	builder.WriteString("\nweights=")
	for baitIndex, bait := range baitOrder {
		if baitIndex > 0 {
			builder.WriteByte(';')
		}
		builder.WriteString(string(bait))
		builder.WriteByte(':')
		definition := findBait(defs.baits, bait)
		entryIndex := 0
		for _, tier := range fishTierOrder {
			if entryIndex > 0 {
				builder.WriteByte(',')
			}
			entryIndex++
			builder.WriteString(string(tier))
			builder.WriteByte('=')
			builder.WriteString(strconv.FormatInt(definition.weights[string(tier)], 10))
		}
		for _, key := range treasureOrder {
			builder.WriteByte(',')
			builder.WriteString(key)
			builder.WriteByte('=')
			builder.WriteString(strconv.FormatInt(definition.weights[key], 10))
		}
	}
	builder.WriteString("\njunk_probability=1/5\nmultipliers=")
	for index, key := range treasureOrder {
		if index > 0 {
			builder.WriteByte(',')
		}
		builder.WriteString(key)
		builder.WriteByte(':')
		builder.WriteString(strconv.Itoa(config.TreasureMultipliers[key]))
	}
	builder.WriteString("\ntiers=")
	for index, tier := range defs.tiers {
		if index > 0 {
			builder.WriteByte(';')
		}
		builder.WriteString(string(tier.tier))
		builder.WriteByte(':')
		builder.WriteString(strconv.Itoa(tier.min))
		builder.WriteString("..")
		builder.WriteString(strconv.Itoa(tier.max))
		builder.WriteByte(':')
		builder.WriteString(tier.base.RatString())
		builder.WriteByte(':')
		builder.WriteString(tier.span.RatString())
	}
	builder.WriteString("\nspecies=")
	for tierIndex, tier := range fishTierOrder {
		if tierIndex > 0 {
			builder.WriteByte(';')
		}
		builder.WriteString(string(tier))
		builder.WriteByte(':')
		written := 0
		for _, species := range defs.species {
			if species.Tier != tier {
				continue
			}
			if written > 0 {
				builder.WriteByte(',')
			}
			written++
			builder.WriteString(species.Key)
		}
	}
	builder.WriteString("\njunk=")
	builder.WriteString(strings.Join(defs.junk, ","))
	builder.WriteString("\ntreasure=")
	builder.WriteString(strings.Join(defs.treasures, ","))
	builder.WriteString("\nsize_bonus=size>=100:*3/2;cap=40;round=floor(x+1/2)")
	return builder.String()
}

func compileWithDefinitions(config Config, defs definitions) (*Ruleset, error) {
	if err := validateDefinitions(defs); err != nil {
		return nil, err
	}
	prices, err := validateConfig(config)
	if err != nil {
		return nil, err
	}

	speciesByTier := make(map[Tier][]Species, len(fishTierOrder))
	for _, species := range defs.species {
		speciesByTier[species.Tier] = append(speciesByTier[species.Tier], species)
	}
	rules := &Ruleset{
		baits:         make(map[Bait]*compiledBait, len(baitOrder)),
		speciesByTier: speciesByTier,
		junk:          append([]string(nil), defs.junk...),
	}

	definitionByBait := make(map[Bait]baitDefinition, len(defs.baits))
	for _, definition := range defs.baits {
		definitionByBait[definition.bait] = definition
	}
	for _, bait := range baitOrder {
		targetPercent := config.StandardRTPPercent
		if bait == BaitPremium {
			targetPercent = config.PremiumRTPPercent
		}
		compiled, err := compileBait(
			prices[bait],
			new(big.Rat).SetFrac64(int64(targetPercent), 100),
			definitionByBait[bait],
			defs,
			config.TreasureMultipliers,
		)
		if err != nil {
			return nil, fmt.Errorf("%w: %s: %v", ErrInvalidConfig, bait, err)
		}
		rules.baits[bait] = compiled
	}
	return rules, nil
}

func validateConfig(config Config) (map[Bait]int64, error) {
	if config.StandardRTPPercent < MinimumRTPPercent || config.StandardRTPPercent > MaximumRTPPercent ||
		config.PremiumRTPPercent < MinimumRTPPercent || config.PremiumRTPPercent > MaximumRTPPercent {
		return nil, fmt.Errorf("%w: RTP percent outside %d..%d", ErrInvalidConfig, MinimumRTPPercent, MaximumRTPPercent)
	}
	if len(config.BaitPricesMilli) != len(baitOrder) {
		return nil, fmt.Errorf("%w: bait prices must contain exactly three entries", ErrInvalidConfig)
	}
	prices := make(map[Bait]int64, len(baitOrder))
	for _, bait := range baitOrder {
		raw, ok := config.BaitPricesMilli[bait]
		if !ok {
			return nil, fmt.Errorf("%w: missing %s price", ErrInvalidConfig, bait)
		}
		price, err := credits.ParseAmount(raw)
		if err != nil || price < MinimumBaitPriceMilli {
			return nil, fmt.Errorf("%w: %s price must be a positive canonical int64", ErrInvalidConfig, bait)
		}
		prices[bait] = price
	}

	if len(config.TreasureMultipliers) != len(treasureOrder) {
		return nil, fmt.Errorf("%w: treasure multipliers must contain exactly three entries", ErrInvalidConfig)
	}
	for _, key := range treasureOrder {
		multiplier, ok := config.TreasureMultipliers[key]
		if !ok || multiplier < MinimumTreasureMultiplier || multiplier > MaximumTreasureMultiplier {
			return nil, fmt.Errorf("%w: %s multiplier must be in %d..%d", ErrInvalidConfig, key, MinimumTreasureMultiplier, MaximumTreasureMultiplier)
		}
	}
	return prices, nil
}

func compileBait(entry int64, target *big.Rat, definition baitDefinition, defs definitions, treasureMultipliers map[string]int) (*compiledBait, error) {
	positiveWeight := uint64(0)
	weighted := make([]weightedOutcome, 0, len(fishTierOrder)+len(treasureOrder))
	for _, tier := range fishTierOrder {
		weight, ok := definition.weights[string(tier)]
		if !ok || weight <= 0 {
			return nil, fmt.Errorf("missing or non-positive %s weight", tier)
		}
		converted := uint64(weight)
		if converted > uint64(math.MaxInt64)/5-positiveWeight {
			return nil, errors.New("positive weight sum overflows sample space")
		}
		positiveWeight += converted
		weighted = append(weighted, weightedOutcome{key: string(tier), tier: tier, weight: converted})
	}
	for _, key := range treasureOrder {
		weight, ok := definition.weights[key]
		if !ok || weight <= 0 {
			return nil, fmt.Errorf("missing or non-positive %s weight", key)
		}
		converted := uint64(weight)
		if converted > uint64(math.MaxInt64)/5-positiveWeight {
			return nil, errors.New("positive weight sum overflows sample space")
		}
		positiveWeight += converted
		weighted = append(weighted, weightedOutcome{key: key, tier: TierTreasure, weight: converted})
	}
	if len(definition.weights) != len(fishTierOrder)+len(treasureOrder) {
		return nil, errors.New("weight table contains an unknown or duplicate logical entry")
	}
	if positiveWeight == 0 || positiveWeight > uint64(math.MaxInt64)/5 {
		return nil, errors.New("invalid positive weight sum")
	}
	sampleSpace := positiveWeight * 5

	tierByName := make(map[Tier]tierDefinition, len(defs.tiers))
	for _, tier := range defs.tiers {
		tierByName[tier.tier] = tier
	}
	denominator := new(big.Int).SetUint64(sampleSpace)
	fishEV := new(big.Rat)
	treasureEV := new(big.Rat)
	for _, candidate := range weighted {
		probability := new(big.Rat).SetFrac(new(big.Int).SetUint64(candidate.weight*4), denominator)
		if candidate.tier == TierTreasure {
			contribution := new(big.Rat).Mul(probability, new(big.Rat).SetInt64(int64(treasureMultipliers[candidate.key])))
			treasureEV.Add(treasureEV, contribution)
			continue
		}
		tierExpected := expectedTierRaw(tierByName[candidate.tier])
		fishEV.Add(fishEV, new(big.Rat).Mul(probability, tierExpected))
	}
	if fishEV.Sign() <= 0 {
		return nil, errors.New("fish expected value must be positive")
	}
	numerator := new(big.Rat).Sub(target, treasureEV)
	scale := new(big.Rat).Quo(numerator, fishEV)
	if scale.Cmp(new(big.Rat).SetFrac64(1, 10)) < 0 || scale.Cmp(new(big.Rat).SetInt64(3)) > 0 {
		return nil, fmt.Errorf("scale %s outside 1/10..3", scale.RatString())
	}

	compiled := &compiledBait{
		entry:              entry,
		positiveWeight:     positiveWeight,
		sampleSpace:        sampleSpace,
		weighted:           weighted,
		target:             new(big.Rat).Set(target),
		fishEV:             new(big.Rat).Set(fishEV),
		treasureEV:         new(big.Rat).Set(treasureEV),
		scale:              new(big.Rat).Set(scale),
		fishPayoutByTierCM: make(map[Tier]map[int]int64, len(fishTierOrder)),
		treasurePayout:     make(map[string]int64, len(treasureOrder)),
	}
	for _, tier := range defs.tiers {
		payouts := make(map[int]int64, tier.max-tier.min+1)
		for size := tier.min; size <= tier.max; size++ {
			exact := new(big.Rat).Mul(new(big.Rat).SetInt64(entry), new(big.Rat).Mul(rawMultiplier(tier, size), scale))
			payout, err := roundNonNegativeRat(exact)
			if err != nil {
				return nil, fmt.Errorf("%s %d cm payout: %w", tier.tier, size, err)
			}
			if _, err := credits.Add(entry, payout); err != nil {
				return nil, fmt.Errorf("%s %d cm ledger addition: %w", tier.tier, size, err)
			}
			payouts[size] = payout
		}
		compiled.fishPayoutByTierCM[tier.tier] = payouts
	}
	for _, key := range treasureOrder {
		payout, err := credits.Mul(entry, int64(treasureMultipliers[key]))
		if err != nil {
			return nil, fmt.Errorf("%s payout: %w", key, err)
		}
		if _, err := credits.Add(entry, payout); err != nil {
			return nil, fmt.Errorf("%s ledger addition: %w", key, err)
		}
		compiled.treasurePayout[key] = payout
	}
	compiled.roundedRTP, compiled.roundingErrorMilli, compiled.roundingBoundMilli = roundedEvidence(compiled, defs)
	return compiled, nil
}

func validateDefinitions(defs definitions) error {
	if len(defs.tiers) != len(fishTierOrder) || len(defs.species) != 23 || len(defs.junk) != 8 || len(defs.treasures) != 3 || len(defs.baits) != len(baitOrder) {
		return fmt.Errorf("%w: incomplete frozen roster", ErrInvalidConfig)
	}
	seenTiers := make(map[Tier]bool, len(fishTierOrder))
	for _, tier := range defs.tiers {
		if tier.min <= 0 || tier.max <= tier.min || tier.base == nil || tier.span == nil || tier.base.Sign() < 0 || tier.span.Sign() < 0 || seenTiers[tier.tier] {
			return fmt.Errorf("%w: invalid tier %s", ErrInvalidConfig, tier.tier)
		}
		seenTiers[tier.tier] = true
	}
	for _, tier := range fishTierOrder {
		if !seenTiers[tier] {
			return fmt.Errorf("%w: missing tier %s", ErrInvalidConfig, tier)
		}
	}

	seenKeys := make(map[string]bool, 34)
	counts := make(map[Tier]int, len(fishTierOrder))
	for _, species := range defs.species {
		if species.Key == "" || seenKeys[species.Key] || !seenTiers[species.Tier] || species.MinCentimetre <= 0 || species.MaxCentimetre < species.MinCentimetre {
			return fmt.Errorf("%w: invalid species %q", ErrInvalidConfig, species.Key)
		}
		definition := findTier(defs.tiers, species.Tier)
		if species.MinCentimetre != definition.min || species.MaxCentimetre != definition.max {
			return fmt.Errorf("%w: species %s has a noncanonical size interval", ErrInvalidConfig, species.Key)
		}
		seenKeys[species.Key] = true
		counts[species.Tier]++
	}
	for _, tier := range fishTierOrder {
		if counts[tier] != 5 && !(tier == TierLegend && counts[tier] == 3) {
			return fmt.Errorf("%w: wrong species count for %s", ErrInvalidConfig, tier)
		}
	}
	if err := validateStringRoster(defs.junk, 8, seenKeys, "junk"); err != nil {
		return err
	}
	if err := validateStringRoster(defs.treasures, 3, seenKeys, "treasure"); err != nil {
		return err
	}
	if !equalStrings(defs.treasures, treasureOrder[:]) {
		return fmt.Errorf("%w: noncanonical treasure registry", ErrInvalidConfig)
	}

	seenBaits := make(map[Bait]bool, len(baitOrder))
	for _, bait := range defs.baits {
		if seenBaits[bait.bait] {
			return fmt.Errorf("%w: duplicate bait %s", ErrInvalidConfig, bait.bait)
		}
		seenBaits[bait.bait] = true
		if len(bait.weights) != len(fishTierOrder)+len(treasureOrder) {
			return fmt.Errorf("%w: incomplete %s weight table", ErrInvalidConfig, bait.bait)
		}
	}
	for _, bait := range baitOrder {
		if !seenBaits[bait] {
			return fmt.Errorf("%w: missing bait %s", ErrInvalidConfig, bait)
		}
	}
	return nil
}

func validateStringRoster(values []string, expected int, seen map[string]bool, kind string) error {
	if len(values) != expected {
		return fmt.Errorf("%w: wrong %s count", ErrInvalidConfig, kind)
	}
	for _, value := range values {
		if value == "" || seen[value] {
			return fmt.Errorf("%w: invalid %s %q", ErrInvalidConfig, kind, value)
		}
		seen[value] = true
	}
	return nil
}

func findTier(tiers []tierDefinition, key Tier) tierDefinition {
	for _, tier := range tiers {
		if tier.tier == key {
			return tier
		}
	}
	return tierDefinition{}
}

func findBait(baits []baitDefinition, key Bait) baitDefinition {
	for _, bait := range baits {
		if bait.bait == key {
			return bait
		}
	}
	return baitDefinition{}
}

func equalStrings(left, right []string) bool {
	if len(left) != len(right) {
		return false
	}
	for index := range left {
		if left[index] != right[index] {
			return false
		}
	}
	return true
}

func rawMultiplier(tier tierDefinition, size int) *big.Rat {
	progress := new(big.Rat).SetFrac64(int64(size-tier.min), int64(tier.max-tier.min))
	value := new(big.Rat).Add(tier.base, new(big.Rat).Mul(tier.span, progress))
	if size >= 100 {
		value.Mul(value, new(big.Rat).SetFrac64(3, 2))
	}
	capValue := new(big.Rat).SetInt64(40)
	if value.Cmp(capValue) > 0 {
		return capValue
	}
	return value
}

func expectedTierRaw(tier tierDefinition) *big.Rat {
	total := new(big.Rat)
	for size := tier.min; size <= tier.max; size++ {
		total.Add(total, rawMultiplier(tier, size))
	}
	return total.Quo(total, new(big.Rat).SetInt64(int64(tier.max-tier.min+1)))
}

func roundNonNegativeRat(value *big.Rat) (int64, error) {
	if value == nil || value.Sign() < 0 {
		return 0, errors.New("negative or nil payout")
	}
	quotient, remainder := new(big.Int).QuoRem(value.Num(), value.Denom(), new(big.Int))
	if new(big.Int).Lsh(remainder, 1).Cmp(value.Denom()) >= 0 {
		quotient.Add(quotient, big.NewInt(1))
	}
	if !quotient.IsInt64() {
		return 0, credits.ErrOverflow
	}
	return quotient.Int64(), nil
}

func roundedEvidence(compiled *compiledBait, defs definitions) (*big.Rat, *big.Rat, *big.Rat) {
	denominator := new(big.Int).SetUint64(compiled.sampleSpace)
	expectedPayout := new(big.Rat)
	fishProbability := new(big.Rat)
	for _, candidate := range compiled.weighted {
		probability := new(big.Rat).SetFrac(new(big.Int).SetUint64(candidate.weight*4), denominator)
		if candidate.tier == TierTreasure {
			expectedPayout.Add(expectedPayout, new(big.Rat).Mul(probability, new(big.Rat).SetInt64(compiled.treasurePayout[candidate.key])))
			continue
		}
		fishProbability.Add(fishProbability, probability)
		tier := findTier(defs.tiers, candidate.tier)
		averagePayout := new(big.Rat)
		for size := tier.min; size <= tier.max; size++ {
			averagePayout.Add(averagePayout, new(big.Rat).SetInt64(compiled.fishPayoutByTierCM[candidate.tier][size]))
		}
		averagePayout.Quo(averagePayout, new(big.Rat).SetInt64(int64(tier.max-tier.min+1)))
		expectedPayout.Add(expectedPayout, new(big.Rat).Mul(probability, averagePayout))
	}
	roundedRTP := new(big.Rat).Quo(new(big.Rat).Set(expectedPayout), new(big.Rat).SetInt64(compiled.entry))
	targetPayout := new(big.Rat).Mul(compiled.target, new(big.Rat).SetInt64(compiled.entry))
	errorMilli := new(big.Rat).Sub(expectedPayout, targetPayout)
	bound := new(big.Rat).Mul(fishProbability, new(big.Rat).SetFrac64(1, 2))
	return roundedRTP, errorMilli, bound
}

func frozenDefinitions() definitions {
	tiers := []tierDefinition{
		{tier: TierSmall, min: 5, max: 25, base: new(big.Rat).SetFrac64(2, 5), span: new(big.Rat).SetFrac64(3, 5)},
		{tier: TierRegular, min: 15, max: 35, base: new(big.Rat).SetInt64(1), span: new(big.Rat).SetFrac64(4, 5)},
		{tier: TierBig, min: 30, max: 80, base: new(big.Rat).SetInt64(2), span: new(big.Rat).SetInt64(2)},
		{tier: TierGiant, min: 60, max: 150, base: new(big.Rat).SetInt64(5), span: new(big.Rat).SetInt64(6)},
		{tier: TierLegend, min: 100, max: 200, base: new(big.Rat).SetInt64(20), span: new(big.Rat).SetFrac64(20, 3)},
	}
	species := []Species{
		{Key: "whitebait", Tier: TierSmall, MinCentimetre: 5, MaxCentimetre: 25},
		{Key: "gudgeon", Tier: TierSmall, MinCentimetre: 5, MaxCentimetre: 25},
		{Key: "horse_mouth", Tier: TierSmall, MinCentimetre: 5, MaxCentimetre: 25},
		{Key: "smelt", Tier: TierSmall, MinCentimetre: 5, MaxCentimetre: 25},
		{Key: "loach", Tier: TierSmall, MinCentimetre: 5, MaxCentimetre: 25},
		{Key: "crucian", Tier: TierRegular, MinCentimetre: 15, MaxCentimetre: 35},
		{Key: "tilapia", Tier: TierRegular, MinCentimetre: 15, MaxCentimetre: 35},
		{Key: "yellow_catfish", Tier: TierRegular, MinCentimetre: 15, MaxCentimetre: 35},
		{Key: "ayu", Tier: TierRegular, MinCentimetre: 15, MaxCentimetre: 35},
		{Key: "stream_carp", Tier: TierRegular, MinCentimetre: 15, MaxCentimetre: 35},
		{Key: "common_carp", Tier: TierBig, MinCentimetre: 30, MaxCentimetre: 80},
		{Key: "snakehead", Tier: TierBig, MinCentimetre: 30, MaxCentimetre: 80},
		{Key: "catfish", Tier: TierBig, MinCentimetre: 30, MaxCentimetre: 80},
		{Key: "mandarin_fish", Tier: TierBig, MinCentimetre: 30, MaxCentimetre: 80},
		{Key: "rainbow_trout", Tier: TierBig, MinCentimetre: 30, MaxCentimetre: 80},
		{Key: "grass_carp", Tier: TierGiant, MinCentimetre: 60, MaxCentimetre: 150},
		{Key: "silver_carp", Tier: TierGiant, MinCentimetre: 60, MaxCentimetre: 150},
		{Key: "bighead_carp", Tier: TierGiant, MinCentimetre: 60, MaxCentimetre: 150},
		{Key: "black_carp", Tier: TierGiant, MinCentimetre: 60, MaxCentimetre: 150},
		{Key: "japanese_eel", Tier: TierGiant, MinCentimetre: 60, MaxCentimetre: 150},
		{Key: "yellowcheek", Tier: TierLegend, MinCentimetre: 100, MaxCentimetre: 200},
		{Key: "taimen", Tier: TierLegend, MinCentimetre: 100, MaxCentimetre: 200},
		{Key: "koi", Tier: TierLegend, MinCentimetre: 100, MaxCentimetre: 200},
	}
	return definitions{
		tiers:   tiers,
		species: species,
		junk:    []string{"boot", "seaweed", "plastic_bag", "branch", "old_tire", "glasses", "phone_case", "fry"},
		treasures: []string{
			"bottle", "clover", "shell",
		},
		baits: []baitDefinition{
			{bait: BaitWorm, weights: map[string]int64{"small": 500, "regular": 340, "big": 55, "giant": 45, "legend": 5, "bottle": 13, "clover": 7, "shell": 3}},
			{bait: BaitLure, weights: map[string]int64{"small": 400, "regular": 380, "big": 90, "giant": 80, "legend": 12, "bottle": 18, "clover": 10, "shell": 4}},
			{bait: BaitPremium, weights: map[string]int64{"small": 340, "regular": 400, "big": 115, "giant": 110, "legend": 18, "bottle": 22, "clover": 13, "shell": 5}},
		},
	}
}
