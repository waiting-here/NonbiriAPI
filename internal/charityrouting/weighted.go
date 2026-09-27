package charityrouting

import (
	"crypto/rand"
	"database/sql"
	"io"
	"math/big"
)

const (
	minimumExpiryDenominator = int64(60)
	maxRejectedSamples       = 64
)

// A zero denominator denotes an unlimited candidate until the complete
// eligible set determines the smallest positive weight it should receive.
type weightedRuntimeCandidate struct {
	candidate   RuntimeCandidate
	denominator int64
}

func runtimeCandidateDenominator(decisionNow int64, expiresAt sql.NullInt64) (int64, bool, error) {
	if decisionNow < 0 || decisionNow > maxUnixSecond {
		return 0, false, ErrInvariant
	}
	if !expiresAt.Valid {
		return 0, true, nil
	}
	if expiresAt.Int64 < 0 || expiresAt.Int64 > maxUnixSecond {
		return 0, false, ErrInvariant
	}
	remaining := expiresAt.Int64 - decisionNow
	if remaining <= 0 {
		return 0, false, nil
	}
	if remaining < minimumExpiryDenominator {
		remaining = minimumExpiryDenominator
	}
	return remaining, true, nil
}

// integerCandidateWeights converts exact reciprocals to integer weights with
// the same ratios. At most 100 bounded UTC-second denominators enter the LCM.
func integerCandidateWeights(candidates []weightedRuntimeCandidate) ([]*big.Int, *big.Int, error) {
	if len(candidates) == 0 || len(candidates) > MaxRuntimeCandidates {
		return nil, nil, ErrInvariant
	}
	maxFinite := int64(1)
	for _, item := range candidates {
		if item.denominator < 0 || item.denominator > maxUnixSecond {
			return nil, nil, ErrInvariant
		}
		if item.denominator > maxFinite {
			maxFinite = item.denominator
		}
	}
	denominators := make([]int64, len(candidates))
	lcm := big.NewInt(1)
	gcd := new(big.Int)
	for index, item := range candidates {
		d := item.denominator
		if d == 0 {
			d = maxFinite
		}
		denominators[index] = d
		value := big.NewInt(d)
		gcd.GCD(nil, nil, lcm, value)
		lcm.Div(lcm, gcd)
		lcm.Mul(lcm, value)
	}
	weights := make([]*big.Int, len(candidates))
	total := new(big.Int)
	for index, d := range denominators {
		weights[index] = new(big.Int).Quo(lcm, big.NewInt(d))
		if weights[index].Sign() <= 0 {
			return nil, nil, ErrInvariant
		}
		total.Add(total, weights[index])
	}
	return weights, total, nil
}

func orderWeightedRuntimeCandidates(source io.Reader, candidates []weightedRuntimeCandidate) ([]RuntimeCandidate, error) {
	if len(candidates) > MaxRuntimeCandidates {
		return nil, ErrResourceLimit
	}
	if len(candidates) == 0 {
		return []RuntimeCandidate{}, nil
	}
	weights, total, err := integerCandidateWeights(candidates)
	if err != nil {
		return nil, err
	}
	pool := append([]weightedRuntimeCandidate(nil), candidates...)
	ordered := make([]RuntimeCandidate, 0, len(pool))
	for len(pool) > 1 {
		draw, err := uniformBigIntn(source, total)
		if err != nil {
			return nil, err
		}
		selected, err := weightedRuntimeCandidateIndex(weights, draw)
		if err != nil {
			return nil, err
		}
		ordered = append(ordered, pool[selected].candidate)
		total.Sub(total, weights[selected])
		copy(pool[selected:], pool[selected+1:])
		pool = pool[:len(pool)-1]
		copy(weights[selected:], weights[selected+1:])
		weights = weights[:len(weights)-1]
	}
	ordered = append(ordered, pool[0].candidate)
	return ordered, nil
}

func weightedRuntimeCandidateIndex(weights []*big.Int, draw *big.Int) (int, error) {
	if draw == nil || draw.Sign() < 0 {
		return 0, ErrInvariant
	}
	cumulative := new(big.Int)
	for index, weight := range weights {
		if weight == nil || weight.Sign() <= 0 {
			return 0, ErrInvariant
		}
		cumulative.Add(cumulative, weight)
		if draw.Cmp(cumulative) < 0 {
			return index, nil
		}
	}
	return 0, ErrInvariant
}

func uniformBigIntn(source io.Reader, upperExclusive *big.Int) (*big.Int, error) {
	if upperExclusive == nil || upperExclusive.Sign() <= 0 {
		return nil, ErrInvariant
	}
	if nilDependency(source) {
		return nil, ErrEntropyUnavailable
	}
	// crypto/rand.Int rejects values outside the requested range. Bound the
	// injected reader so a broken source cannot stall a request indefinitely.
	maxBytes := int64((upperExclusive.BitLen()+7)/8) * maxRejectedSamples
	draw, err := rand.Int(io.LimitReader(source, maxBytes), upperExclusive)
	if err != nil {
		return nil, ErrEntropyUnavailable
	}
	return draw, nil
}
