package charityrouting

import (
	"database/sql"
	"encoding/binary"
	"io"
)

const (
	weightWithinDay   uint64 = 8
	weightWithinWeek  uint64 = 4
	weightWithinMonth uint64 = 2
	weightLater       uint64 = 1

	secondsPerDay            = int64(86_400)
	secondsPerWeek           = int64(604_800)
	secondsPerMonth          = int64(2_592_000)
	maxEntropyReadsPerSample = 8
	maxRejectedSamples       = 64
)

type weightedRuntimeCandidate struct {
	candidate RuntimeCandidate
	weight    uint64
}

func runtimeCandidateWeight(decisionNow int64, expiresAt sql.NullInt64) (uint64, bool, error) {
	if decisionNow < 0 || decisionNow > maxUnixSecond {
		return 0, false, ErrInvariant
	}
	if !expiresAt.Valid {
		return weightLater, true, nil
	}
	if expiresAt.Int64 < 0 || expiresAt.Int64 > maxUnixSecond {
		return 0, false, ErrInvariant
	}
	remaining := expiresAt.Int64 - decisionNow
	if remaining <= 0 {
		return 0, false, nil
	}
	switch {
	case remaining <= secondsPerDay:
		return weightWithinDay, true, nil
	case remaining <= secondsPerWeek:
		return weightWithinWeek, true, nil
	case remaining <= secondsPerMonth:
		return weightWithinMonth, true, nil
	default:
		return weightLater, true, nil
	}
}

func orderWeightedRuntimeCandidates(source io.Reader, candidates []weightedRuntimeCandidate) ([]RuntimeCandidate, error) {
	if len(candidates) > MaxRuntimeCandidates {
		return nil, ErrResourceLimit
	}
	if len(candidates) == 0 {
		return []RuntimeCandidate{}, nil
	}
	pool := append([]weightedRuntimeCandidate(nil), candidates...)
	for _, candidate := range pool {
		if !validRuntimeCandidateWeight(candidate.weight) {
			return nil, ErrInvariant
		}
	}
	ordered := make([]RuntimeCandidate, 0, len(pool))
	for len(pool) > 1 {
		total, err := runtimeCandidateWeightTotal(pool)
		if err != nil {
			return nil, err
		}
		draw, err := uniformUint64n(source, total)
		if err != nil {
			return nil, err
		}
		selected, err := weightedRuntimeCandidateIndex(pool, draw)
		if err != nil {
			return nil, err
		}
		ordered = append(ordered, pool[selected].candidate)
		copy(pool[selected:], pool[selected+1:])
		pool[len(pool)-1] = weightedRuntimeCandidate{}
		pool = pool[:len(pool)-1]
	}
	ordered = append(ordered, pool[0].candidate)
	return ordered, nil
}

func runtimeCandidateWeightTotal(candidates []weightedRuntimeCandidate) (uint64, error) {
	var total uint64
	for _, candidate := range candidates {
		if !validRuntimeCandidateWeight(candidate.weight) || total > ^uint64(0)-candidate.weight {
			return 0, ErrInvariant
		}
		total += candidate.weight
	}
	if total == 0 {
		return 0, ErrInvariant
	}
	return total, nil
}

func weightedRuntimeCandidateIndex(candidates []weightedRuntimeCandidate, draw uint64) (int, error) {
	var cumulative uint64
	for index, candidate := range candidates {
		if !validRuntimeCandidateWeight(candidate.weight) || cumulative > ^uint64(0)-candidate.weight {
			return 0, ErrInvariant
		}
		cumulative += candidate.weight
		if draw < cumulative {
			return index, nil
		}
	}
	return 0, ErrInvariant
}

func validRuntimeCandidateWeight(weight uint64) bool {
	return weight == weightWithinDay || weight == weightWithinWeek ||
		weight == weightWithinMonth || weight == weightLater
}

func uniformUint64n(source io.Reader, upperExclusive uint64) (uint64, error) {
	if upperExclusive == 0 {
		return 0, ErrInvariant
	}
	if nilDependency(source) {
		return 0, ErrEntropyUnavailable
	}
	// Unsigned negation yields 2^64-upperExclusive. Its remainder is the
	// short prefix that would make modulo reduction biased.
	threshold := -upperExclusive % upperExclusive
	for attempt := 0; attempt < maxRejectedSamples; attempt++ {
		value, err := readEntropyUint64(source)
		if err != nil {
			return 0, ErrEntropyUnavailable
		}
		if value >= threshold {
			return value % upperExclusive, nil
		}
	}
	return 0, ErrEntropyUnavailable
}

func readEntropyUint64(source io.Reader) (uint64, error) {
	if nilDependency(source) {
		return 0, ErrEntropyUnavailable
	}
	var block [8]byte
	filled := 0
	for reads := 0; filled < len(block) && reads < maxEntropyReadsPerSample; reads++ {
		n, err := source.Read(block[filled:])
		if n < 0 || n > len(block)-filled || err != nil || n == 0 {
			return 0, ErrEntropyUnavailable
		}
		filled += n
	}
	if filled != len(block) {
		return 0, ErrEntropyUnavailable
	}
	return binary.LittleEndian.Uint64(block[:]), nil
}
