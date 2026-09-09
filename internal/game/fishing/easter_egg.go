package fishing

import (
	"context"
	"errors"
	"math/big"
)

const (
	MaximumLengthDigits = 128
	// A failed random source must not hold a game transaction indefinitely.
	// Exhausting this work budget fails the entire draw; it never awards a
	// fish at a truncated maximum length.
	easterEggDrawBudget = 65_536
)

var ErrEasterEggBudget = errors.New("fishing: presentation draw budget exhausted")

// DecorateBatch runs after every economic outcome has been selected. Each
// legend has an independent 1/10 chance of an Easter egg. Its length is 201+N,
// where P(N>=n)=(99/100)^n. Rewards and original species are never changed.
// No partial result is returned on cancellation, random failure or exhaustion.
func DecorateBatch(ctx context.Context, results []Result, source IntSource) ([]Result, error) {
	if ctx == nil {
		return nil, errors.New("fishing: nil context")
	}
	decorated := append([]Result(nil), results...)
	remaining := easterEggDrawBudget
	next := func(bound uint64) (uint64, error) {
		if err := ctx.Err(); err != nil {
			return 0, err
		}
		if remaining == 0 {
			return 0, ErrEasterEggBudget
		}
		remaining--
		return draw(source, bound)
	}
	for index := range decorated {
		outcome := &decorated[index].Outcome
		if outcome.BlueFatFishLengthCM != "" {
			return nil, errors.New("fishing: outcome already decorated")
		}
		if outcome.Tier != TierLegend {
			continue
		}
		if outcome.SizeCentimetre < 100 || outcome.SizeCentimetre > 200 ||
			outcome.Key != "yellowcheek" && outcome.Key != "taimen" && outcome.Key != "koi" {
			return nil, errors.New("fishing: invalid legend outcome")
		}
		selected, err := next(10)
		if err != nil {
			return nil, err
		}
		if selected != 0 {
			continue
		}
		length := big.NewInt(201)
		for {
			continued, err := next(100)
			if err != nil {
				return nil, err
			}
			if continued == 0 {
				break
			}
			length.Add(length, big.NewInt(1))
		}
		outcome.BlueFatFishLengthCM = length.String()
		if !ValidBlueFatFishLength(outcome.BlueFatFishLengthCM) {
			return nil, ErrEasterEggBudget
		}
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return decorated, nil
}

// ValidBlueFatFishLength accepts the exact storage/wire decimal representation.
// It must not pass through floating point or a machine-sized integer.
func ValidBlueFatFishLength(value string) bool {
	if len(value) < 3 || len(value) > MaximumLengthDigits || value[0] == '0' {
		return false
	}
	for _, digit := range value {
		if digit < '0' || digit > '9' {
			return false
		}
	}
	return len(value) > 3 || value >= "201"
}

// CompareLengths compares canonical positive decimal integers exactly.
func CompareLengths(left, right string) int {
	if len(left) < len(right) || len(left) == len(right) && left < right {
		return -1
	}
	if left == right {
		return 0
	}
	return 1
}
