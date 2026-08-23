package fishing

import (
	"bytes"
	"crypto/sha256"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"math"
	"math/big"
	"reflect"
	"testing"
)

func TestFrozenRosterDefaultsAndHash(t *testing.T) {
	t.Parallel()

	config := DefaultConfig()
	if got, want := config.BaitPricesMilli, map[Bait]string{
		BaitWorm: "2500000", BaitLure: "5000000", BaitPremium: "7500000",
	}; !reflect.DeepEqual(got, want) {
		t.Fatalf("prices = %#v, want %#v", got, want)
	}
	if config.StandardRTPPercent != 90 || config.PremiumRTPPercent != 88 {
		t.Fatalf("RTP defaults = %d/%d, want 90/88", config.StandardRTPPercent, config.PremiumRTPPercent)
	}
	if got, want := config.TreasureMultipliers, map[string]int{"bottle": 2, "clover": 3, "shell": 5}; !reflect.DeepEqual(got, want) {
		t.Fatalf("treasure multipliers = %#v, want %#v", got, want)
	}

	wantSpecies := []string{
		"whitebait", "gudgeon", "horse_mouth", "smelt", "loach",
		"crucian", "tilapia", "yellow_catfish", "ayu", "stream_carp",
		"common_carp", "snakehead", "catfish", "mandarin_fish", "rainbow_trout",
		"grass_carp", "silver_carp", "bighead_carp", "black_carp", "japanese_eel",
		"yellowcheek", "taimen", "koi",
	}
	gotSpecies := make([]string, 0, len(FishRoster()))
	for _, species := range FishRoster() {
		gotSpecies = append(gotSpecies, species.Key)
	}
	if !reflect.DeepEqual(gotSpecies, wantSpecies) {
		t.Fatalf("species = %#v, want %#v", gotSpecies, wantSpecies)
	}
	if got, want := JunkRoster(), []string{"boot", "seaweed", "plastic_bag", "branch", "old_tire", "glasses", "phone_case", "fry"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("junk = %#v, want %#v", got, want)
	}
	if got, want := TreasureRoster(), []string{"bottle", "clover", "shell"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("treasures = %#v, want %#v", got, want)
	}

	const wantHash = "ef00da7ce34e9fdc1e211cfb962ef1c85d409088c6d6b1473294f99254f873b0"
	if got := FrozenRulesHash(); got != wantHash {
		t.Fatalf("frozen rules hash = %s, want %s", got, wantHash)
	}
	t.Logf("frozen rules sha256=%s", FrozenRulesHash())
	baselineCanonical := canonicalRules(frozenDefinitions(), DefaultConfig())
	if got := fmt.Sprintf("%x", sha256.Sum256([]byte(baselineCanonical))); got != wantHash {
		t.Fatalf("hash is not derived from the live canonical rules: %s", got)
	}
	mutatedDefinitions := cloneDefinitions(frozenDefinitions())
	mutatedDefinitions.baits[0].weights["small"]++
	if canonicalRules(mutatedDefinitions, DefaultConfig()) == baselineCanonical {
		t.Fatal("weight drift did not change canonical rules")
	}
	mutatedDefinitions = cloneDefinitions(frozenDefinitions())
	mutatedDefinitions.species[0].Key = "changed"
	if canonicalRules(mutatedDefinitions, DefaultConfig()) == baselineCanonical {
		t.Fatal("roster drift did not change canonical rules")
	}
	mutatedConfig := cloneConfig(DefaultConfig())
	mutatedConfig.BaitPricesMilli[BaitWorm] = "2500001"
	if canonicalRules(frozenDefinitions(), mutatedConfig) == baselineCanonical {
		t.Fatal("economic default drift did not change canonical rules")
	}

	// Returned maps and slices are copies, never handles into compiled state.
	config.BaitPricesMilli[BaitWorm] = "1"
	config.TreasureMultipliers["bottle"] = 999
	roster := FishRoster()
	roster[0].Key = "changed"
	if DefaultConfig().BaitPricesMilli[BaitWorm] != "2500000" || FishRoster()[0].Key != "whitebait" {
		t.Fatal("default registries leaked mutable backing state")
	}
}

func TestCompileProvesExactRTPAndRoundingBounds(t *testing.T) {
	t.Parallel()

	rules, err := Compile(DefaultConfig())
	if err != nil {
		t.Fatal(err)
	}
	wants := map[Bait]struct {
		entry  string
		weight uint64
		target string
	}{
		BaitWorm:    {entry: "2500000", weight: 968, target: "9/10"},
		BaitLure:    {entry: "5000000", weight: 994, target: "9/10"},
		BaitPremium: {entry: "7500000", weight: 1023, target: "22/25"},
	}
	for _, bait := range baitOrder {
		evidence, err := rules.Evidence(bait)
		if err != nil {
			t.Fatal(err)
		}
		want := wants[bait]
		if evidence.EntryMilli != want.entry || evidence.PositiveWeight != want.weight || evidence.SampleSpace != want.weight*5 {
			t.Errorf("%s basic evidence = %#v", bait, evidence)
		}
		if evidence.TargetRTP != want.target || evidence.PreRoundRTP != want.target {
			t.Errorf("%s exact pre-round RTP = %s/%s, want %s", bait, evidence.TargetRTP, evidence.PreRoundRTP, want.target)
		}
		t.Logf(
			"%s W=%d target=%s fishEV=%s treasureEV=%s scale=%s roundedRTP=%s roundingErrorMilli=%s boundMilli=%s",
			bait,
			evidence.PositiveWeight,
			evidence.TargetRTP,
			evidence.FishEV,
			evidence.TreasureEV,
			evidence.Scale,
			evidence.RoundedRTP,
			evidence.ExpectedRoundingErrorMilli,
			evidence.StrictRoundingErrorBoundMilli,
		)
		if mustRat(t, evidence.FishEV).Sign() <= 0 {
			t.Errorf("%s fish EV is not positive", bait)
		}
		scale := mustRat(t, evidence.Scale)
		if scale.Cmp(new(big.Rat).SetFrac64(1, 10)) < 0 || scale.Cmp(new(big.Rat).SetInt64(3)) > 0 {
			t.Errorf("%s scale %s outside frozen range", bait, scale)
		}
		errorMilli := absoluteRat(mustRat(t, evidence.ExpectedRoundingErrorMilli))
		bound := mustRat(t, evidence.StrictRoundingErrorBoundMilli)
		if errorMilli.Cmp(bound) >= 0 {
			t.Errorf("%s aggregate rounding error %s is not strictly below %s", bait, errorMilli, bound)
		}
	}

	defs := frozenDefinitions()
	for _, bait := range baitOrder {
		compiled := rules.baits[bait]
		aggregateError := new(big.Rat)
		fishProbability := new(big.Rat)
		for _, tier := range defs.tiers {
			averageError := new(big.Rat)
			for size := tier.min; size <= tier.max; size++ {
				exact := new(big.Rat).Mul(
					new(big.Rat).SetInt64(compiled.entry),
					new(big.Rat).Mul(rawMultiplier(tier, size), compiled.scale),
				)
				rounded := new(big.Rat).SetInt64(compiled.fishPayoutByTierCM[tier.tier][size])
				errorValue := absoluteRat(new(big.Rat).Sub(rounded, exact))
				if errorValue.Cmp(new(big.Rat).SetFrac64(1, 2)) >= 0 {
					t.Errorf("%s/%s/%d rounding error %s is not < 1/2 milli", bait, tier.tier, size, errorValue)
				}
				averageError.Add(averageError, new(big.Rat).Sub(rounded, exact))
			}
			averageError.Quo(averageError, new(big.Rat).SetInt64(int64(tier.max-tier.min+1)))
			weight := compiledWeight(compiled, tier.tier)
			probability := new(big.Rat).SetFrac64(int64(weight*4), int64(compiled.sampleSpace))
			fishProbability.Add(fishProbability, probability)
			aggregateError.Add(aggregateError, new(big.Rat).Mul(probability, averageError))
		}
		for key, multiplier := range DefaultConfig().TreasureMultipliers {
			want := compiled.entry * int64(multiplier)
			if got := compiled.treasurePayout[key]; got != want {
				t.Errorf("%s/%s payout = %d, want %d", bait, key, got, want)
			}
		}
		evidence, err := rules.Evidence(bait)
		if err != nil {
			t.Fatal(err)
		}
		if got := mustRat(t, evidence.ExpectedRoundingErrorMilli); got.Cmp(aggregateError) != 0 {
			t.Errorf("%s aggregate error evidence = %s, independently enumerated %s", bait, got, aggregateError)
		}
		strictBound := new(big.Rat).Mul(fishProbability, new(big.Rat).SetFrac64(1, 2))
		if got := mustRat(t, evidence.StrictRoundingErrorBoundMilli); got.Cmp(strictBound) != 0 {
			t.Errorf("%s rounding bound evidence = %s, independently derived %s", bait, got, strictBound)
		}
		if absoluteRat(new(big.Rat).Set(aggregateError)).Cmp(strictBound) >= 0 {
			t.Errorf("%s independently enumerated aggregate error %s is not strictly below %s", bait, aggregateError, strictBound)
		}
		independentRoundedRTP := new(big.Rat).Add(compiled.target, new(big.Rat).Quo(aggregateError, new(big.Rat).SetInt64(compiled.entry)))
		if got := mustRat(t, evidence.RoundedRTP); got.Cmp(independentRoundedRTP) != 0 {
			t.Errorf("%s rounded RTP evidence = %s, independently enumerated %s", bait, got, independentRoundedRTP)
		}
	}
}

func TestRawMultiplierUsesExactThresholdAndCap(t *testing.T) {
	t.Parallel()

	defs := frozenDefinitions()
	giant := findTier(defs.tiers, TierGiant)
	if got, want := rawMultiplier(giant, 99).RatString(), "38/5"; got != want {
		t.Fatalf("giant 99 cm = %s, want %s", got, want)
	}
	if got, want := rawMultiplier(giant, 100).RatString(), "23/2"; got != want {
		t.Fatalf("giant 100 cm = %s, want %s", got, want)
	}
	legend := findTier(defs.tiers, TierLegend)
	if got := rawMultiplier(legend, 200).RatString(); got != "40" {
		t.Fatalf("legend cap = %s, want 40", got)
	}
}

func TestPrimarySampleSpaceExactlyMatchesFrozenWeights(t *testing.T) {
	t.Parallel()

	rules, err := Compile(DefaultConfig())
	if err != nil {
		t.Fatal(err)
	}
	definitionsByBait := make(map[Bait]baitDefinition)
	for _, definition := range frozenDefinitions().baits {
		definitionsByBait[definition.bait] = definition
	}
	for _, bait := range baitOrder {
		compiled := rules.baits[bait]
		counts := make(map[string]uint64)
		for primary := uint64(0); primary < compiled.sampleSpace; primary++ {
			result, err := rules.Roll(bait, &sequenceSource{values: []uint64{primary, 0, 0}})
			if err != nil {
				t.Fatalf("%s draw %d: %v", bait, primary, err)
			}
			key := string(result.Outcome.Tier)
			if result.Outcome.Tier == TierTreasure {
				key = result.Outcome.Key
			}
			counts[key]++
		}
		if got, want := counts[string(TierJunk)], compiled.positiveWeight; got != want {
			t.Errorf("%s junk interval = %d, want %d", bait, got, want)
		}
		for key, weight := range definitionsByBait[bait].weights {
			if got, want := counts[key], uint64(weight)*4; got != want {
				t.Errorf("%s/%s interval = %d, want %d", bait, key, got, want)
			}
		}
	}
}

func TestSecondaryDrawsCoverInclusiveBoundaries(t *testing.T) {
	t.Parallel()

	rules, err := Compile(DefaultConfig())
	if err != nil {
		t.Fatal(err)
	}
	compiled := rules.baits[BaitWorm]

	junk, err := rules.Roll(BaitWorm, &sequenceSource{values: []uint64{0, 7}})
	if err != nil {
		t.Fatal(err)
	}
	if junk.Outcome.Key != "fry" || junk.Outcome.Tier != TierJunk || junk.Settlement.PayoutMilli != 0 {
		t.Fatalf("last junk = %#v", junk)
	}

	// The first positive interval is small; choose its final species and size.
	fish, err := rules.Roll(BaitWorm, &sequenceSource{values: []uint64{compiled.positiveWeight, 4, 20}})
	if err != nil {
		t.Fatal(err)
	}
	if fish.Outcome.Key != "loach" || fish.Outcome.SizeCentimetre != 25 || fish.Outcome.Tier != TierSmall {
		t.Fatalf("inclusive fish boundary = %#v", fish)
	}

	last := compiled.sampleSpace - 1
	treasure, err := rules.Roll(BaitWorm, &sequenceSource{values: []uint64{last}})
	if err != nil {
		t.Fatal(err)
	}
	if treasure.Outcome.Key != "shell" || treasure.Outcome.Tier != TierTreasure {
		t.Fatalf("last primary interval = %#v", treasure)
	}
}

func TestCryptoSourceSeededHighSampleIsStable(t *testing.T) {
	// This is deliberately not parallel: it is a moderately sized deterministic
	// regression sample, not a probabilistic threshold test.
	rules, err := Compile(DefaultConfig())
	if err != nil {
		t.Fatal(err)
	}
	first := sampleDigest(t, rules, []byte("alpha3-fishing-rules-seed"), 100_000)
	second := sampleDigest(t, rules, []byte("alpha3-fishing-rules-seed"), 100_000)
	if !reflect.DeepEqual(first, second) {
		t.Fatalf("same deterministic seed produced different samples: %#v vs %#v", first, second)
	}
	const wantDigest = "18b07897b0b32e6ab115f2d5e9e0392a83b03afd3f58bfe27a8a7e03ba136eaf"
	if first.digest != wantDigest {
		t.Fatalf("sample digest = %s, want %s; counts=%v", first.digest, wantDigest, first.counts)
	}
	wantCounts := map[string]int{
		"junk": 19842, "small": 33365, "regular": 30246, "big": 6830,
		"giant": 6217, "legend": 930, "treasure": 2570,
	}
	if !reflect.DeepEqual(first.counts, wantCounts) {
		t.Fatalf("sample counts = %v, want %v", first.counts, wantCounts)
	}
	t.Logf("seeded sample digest=%s counts=%v", first.digest, first.counts)
}

func TestCompileRejectsWholeInvalidSnapshot(t *testing.T) {
	t.Parallel()

	validRules, err := Compile(DefaultConfig())
	if err != nil {
		t.Fatal(err)
	}
	before, err := validRules.Evidence(BaitWorm)
	if err != nil {
		t.Fatal(err)
	}

	tests := map[string]func(*Config){
		"missing price": func(config *Config) { delete(config.BaitPricesMilli, BaitLure) },
		"extra price":   func(config *Config) { config.BaitPricesMilli[Bait("extra")] = "1" },
		"zero price":    func(config *Config) { config.BaitPricesMilli[BaitWorm] = "0" },
		"negative price": func(config *Config) {
			config.BaitPricesMilli[BaitWorm] = "-1"
		},
		"noncanonical price": func(config *Config) { config.BaitPricesMilli[BaitWorm] = "02500000" },
		"price parse overflow": func(config *Config) {
			config.BaitPricesMilli[BaitWorm] = "9223372036854775808"
		},
		"payout overflow": func(config *Config) {
			config.BaitPricesMilli[BaitWorm] = "9223372036854775807"
		},
		"negative standard RTP": func(config *Config) { config.StandardRTPPercent = -1 },
		"high premium RTP":      func(config *Config) { config.PremiumRTPPercent = 101 },
		"economically invalid RTP": func(config *Config) {
			config.StandardRTPPercent = 0
		},
		"missing multiplier": func(config *Config) { delete(config.TreasureMultipliers, "clover") },
		"extra multiplier": func(config *Config) {
			config.TreasureMultipliers["extra"] = 2
		},
		"zero multiplier": func(config *Config) { config.TreasureMultipliers["bottle"] = 0 },
		"high multiplier": func(config *Config) { config.TreasureMultipliers["shell"] = 1001 },
	}
	for name, mutate := range tests {
		t.Run(name, func(t *testing.T) {
			config := cloneConfig(DefaultConfig())
			mutate(&config)
			if _, err := Compile(config); !errors.Is(err, ErrInvalidConfig) {
				t.Fatalf("Compile() error = %v, want ErrInvalidConfig", err)
			}
		})
	}

	after, err := validRules.Evidence(BaitWorm)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(before, after) {
		t.Fatalf("failed candidate partially mutated previous rules: %#v vs %#v", before, after)
	}
}

func TestCompileRejectsBrokenRuleDefinitions(t *testing.T) {
	t.Parallel()

	tests := map[string]func(*definitions){
		"missing species": func(defs *definitions) { defs.species = defs.species[:22] },
		"duplicate species": func(defs *definitions) {
			defs.species[1].Key = defs.species[0].Key
		},
		"wrong species range": func(defs *definitions) { defs.species[0].MaxCentimetre++ },
		"missing junk":        func(defs *definitions) { defs.junk = defs.junk[:7] },
		"duplicate treasure": func(defs *definitions) {
			defs.treasures[1] = defs.treasures[0]
		},
		"missing tier": func(defs *definitions) { defs.tiers = defs.tiers[:4] },
		"zero width tier": func(defs *definitions) {
			defs.tiers[0].max = defs.tiers[0].min
		},
		"missing bait": func(defs *definitions) { defs.baits = defs.baits[:2] },
		"missing weight": func(defs *definitions) {
			delete(defs.baits[0].weights, "big")
		},
		"zero weight":     func(defs *definitions) { defs.baits[0].weights["big"] = 0 },
		"negative weight": func(defs *definitions) { defs.baits[0].weights["big"] = -1 },
		"weight overflow": func(defs *definitions) {
			defs.baits[0].weights["small"] = math.MaxInt64
		},
		"unknown weight": func(defs *definitions) {
			defs.baits[0].weights["unknown"] = 1
		},
	}
	for name, mutate := range tests {
		t.Run(name, func(t *testing.T) {
			defs := cloneDefinitions(frozenDefinitions())
			mutate(&defs)
			if _, err := compileWithDefinitions(DefaultConfig(), defs); !errors.Is(err, ErrInvalidConfig) {
				t.Fatalf("compileWithDefinitions() error = %v, want ErrInvalidConfig", err)
			}
		})
	}
}

func TestRandomFailuresFailClosed(t *testing.T) {
	t.Parallel()

	rules, err := Compile(DefaultConfig())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := rules.Roll(BaitWorm, nil); err == nil {
		t.Fatal("nil random source succeeded")
	}
	if _, err := rules.Roll(BaitWorm, errorSource{}); !errors.Is(err, io.ErrUnexpectedEOF) {
		t.Fatalf("source error = %v, want io.ErrUnexpectedEOF", err)
	}
	if _, err := rules.Roll(BaitWorm, outOfRangeSource{}); !errors.Is(err, ErrInvalidRandomValue) {
		t.Fatalf("out-of-range source error = %v, want ErrInvalidRandomValue", err)
	}
	if _, err := rules.Roll(Bait("unknown"), &sequenceSource{}); !errors.Is(err, ErrUnknownBait) {
		t.Fatalf("unknown bait error = %v, want ErrUnknownBait", err)
	}
	if _, err := (CryptoSource{Reader: errorReader{}}).Uint64n(10); !errors.Is(err, io.ErrUnexpectedEOF) {
		t.Fatalf("crypto reader error = %v, want io.ErrUnexpectedEOF", err)
	}
	if _, err := (CryptoSource{}).Uint64n(0); !errors.Is(err, ErrInvalidBound) {
		t.Fatalf("zero bound error = %v, want ErrInvalidBound", err)
	}
}

func TestRoundHalfUpDoesNotUseBankersRounding(t *testing.T) {
	t.Parallel()

	for _, test := range []struct {
		value string
		want  int64
	}{
		{value: "1/2", want: 1},
		{value: "3/2", want: 2},
		{value: "4/3", want: 1},
	} {
		got, err := roundNonNegativeRat(mustRat(t, test.value))
		if err != nil {
			t.Fatal(err)
		}
		if got != test.want {
			t.Errorf("round(%s) = %d, want %d", test.value, got, test.want)
		}
	}
	tooLarge := new(big.Rat).SetInt(new(big.Int).Add(big.NewInt(math.MaxInt64), big.NewInt(1)))
	if _, err := roundNonNegativeRat(tooLarge); err == nil {
		t.Fatal("overflowing round succeeded")
	}
}

type sequenceSource struct {
	values []uint64
	index  int
}

func (source *sequenceSource) Uint64n(upperExclusive uint64) (uint64, error) {
	if source.index >= len(source.values) {
		return 0, nil
	}
	value := source.values[source.index]
	source.index++
	return value, nil
}

type errorSource struct{}

func (errorSource) Uint64n(uint64) (uint64, error) { return 0, io.ErrUnexpectedEOF }

type outOfRangeSource struct{}

func (outOfRangeSource) Uint64n(upperExclusive uint64) (uint64, error) {
	return upperExclusive, nil
}

type errorReader struct{}

func (errorReader) Read([]byte) (int, error) { return 0, io.ErrUnexpectedEOF }

type seededReader struct {
	seed    []byte
	counter uint64
	buffer  []byte
}

func (reader *seededReader) Read(target []byte) (int, error) {
	written := 0
	for written < len(target) {
		if len(reader.buffer) == 0 {
			counter := make([]byte, 8)
			binary.BigEndian.PutUint64(counter, reader.counter)
			reader.counter++
			digest := sha256.Sum256(append(append([]byte(nil), reader.seed...), counter...))
			reader.buffer = append(reader.buffer[:0], digest[:]...)
		}
		copied := copy(target[written:], reader.buffer)
		written += copied
		reader.buffer = reader.buffer[copied:]
	}
	return written, nil
}

type sampleResult struct {
	digest string
	counts map[string]int
}

func sampleDigest(t *testing.T, rules *Ruleset, seed []byte, count int) sampleResult {
	t.Helper()
	source := CryptoSource{Reader: &seededReader{seed: append([]byte(nil), seed...)}}
	hash := sha256.New()
	counts := make(map[string]int)
	for index := 0; index < count; index++ {
		bait := baitOrder[index%len(baitOrder)]
		result, err := rules.Roll(bait, source)
		if err != nil {
			t.Fatalf("sample %d: %v", index, err)
		}
		counts[string(result.Outcome.Tier)]++
		_, _ = fmt.Fprintf(hash, "%s/%s/%s/%d/%d\n", bait, result.Outcome.Tier, result.Outcome.Key, result.Outcome.SizeCentimetre, result.Settlement.PayoutMilli)
	}
	return sampleResult{digest: fmt.Sprintf("%x", hash.Sum(nil)), counts: counts}
}

func cloneConfig(config Config) Config {
	clone := Config{
		BaitPricesMilli:     make(map[Bait]string, len(config.BaitPricesMilli)),
		StandardRTPPercent:  config.StandardRTPPercent,
		PremiumRTPPercent:   config.PremiumRTPPercent,
		TreasureMultipliers: make(map[string]int, len(config.TreasureMultipliers)),
	}
	for key, value := range config.BaitPricesMilli {
		clone.BaitPricesMilli[key] = value
	}
	for key, value := range config.TreasureMultipliers {
		clone.TreasureMultipliers[key] = value
	}
	return clone
}

func cloneDefinitions(defs definitions) definitions {
	clone := definitions{
		tiers:     append([]tierDefinition(nil), defs.tiers...),
		species:   append([]Species(nil), defs.species...),
		junk:      append([]string(nil), defs.junk...),
		treasures: append([]string(nil), defs.treasures...),
		baits:     make([]baitDefinition, len(defs.baits)),
	}
	for index, bait := range defs.baits {
		clone.baits[index] = baitDefinition{bait: bait.bait, weights: make(map[string]int64, len(bait.weights))}
		for key, value := range bait.weights {
			clone.baits[index].weights[key] = value
		}
	}
	return clone
}

func mustRat(t *testing.T, value string) *big.Rat {
	t.Helper()
	result, ok := new(big.Rat).SetString(value)
	if !ok {
		t.Fatalf("invalid rational %q", value)
	}
	return result
}

func absoluteRat(value *big.Rat) *big.Rat {
	if value.Sign() < 0 {
		return value.Neg(value)
	}
	return value
}

func compiledWeight(compiled *compiledBait, tier Tier) uint64 {
	for _, candidate := range compiled.weighted {
		if candidate.tier == tier {
			return candidate.weight
		}
	}
	return 0
}

func TestSeededReaderFulfilsArbitraryReadSizes(t *testing.T) {
	t.Parallel()

	left := &seededReader{seed: []byte("seed")}
	right := &seededReader{seed: []byte("seed")}
	whole := make([]byte, 97)
	if _, err := io.ReadFull(left, whole); err != nil {
		t.Fatal(err)
	}
	parts := make([]byte, 97)
	offset := 0
	for _, size := range []int{1, 3, 7, 16, 32, 38} {
		if _, err := io.ReadFull(right, parts[offset:offset+size]); err != nil {
			t.Fatal(err)
		}
		offset += size
	}
	if !bytes.Equal(whole, parts) {
		t.Fatal("seeded reader depends on Read chunking")
	}
}
