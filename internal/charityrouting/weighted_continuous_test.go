package charityrouting

import (
	"bytes"
	"database/sql"
	"errors"
	"math/big"
	"testing"
)

func TestContinuousDenominatorsAndExpiryBoundary(t *testing.T) {
	for _, tc := range []struct {
		name     string
		expiry   sql.NullInt64
		want     int64
		eligible bool
	}{
		{"unlimited", sql.NullInt64{}, 0, true},
		{"expired", sql.NullInt64{Int64: routingTestNow, Valid: true}, 0, false},
		{"one second", sql.NullInt64{Int64: routingTestNow + 1, Valid: true}, 60, true},
		{"sixty seconds", sql.NullInt64{Int64: routingTestNow + 60, Valid: true}, 60, true},
		{"sixty one seconds", sql.NullInt64{Int64: routingTestNow + 61, Valid: true}, 61, true},
		{"largest valid expiry", sql.NullInt64{Int64: maxUnixSecond, Valid: true}, maxUnixSecond - routingTestNow, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, eligible, err := runtimeCandidateDenominator(routingTestNow, tc.expiry)
			if err != nil || got != tc.want || eligible != tc.eligible {
				t.Fatalf("denominator = %d, eligible = %v, error = %v; want %d, %v", got, eligible, err, tc.want, tc.eligible)
			}
		})
	}
}

func TestContinuousIntegerWeightsAreExactAndPositive(t *testing.T) {
	candidates := []weightedRuntimeCandidate{
		{candidate: RuntimeCandidate{DonationKeyID: 1}, denominator: 60},
		{candidate: RuntimeCandidate{DonationKeyID: 2}, denominator: 61},
		{candidate: RuntimeCandidate{DonationKeyID: 3}, denominator: 0},
	}
	weights, total, err := integerCandidateWeights(candidates)
	if err != nil {
		t.Fatal(err)
	}
	for i, want := range []int64{61, 60, 60} {
		if weights[i].Cmp(big.NewInt(want)) != 0 {
			t.Fatalf("weight[%d] = %s, want %d", i, weights[i], want)
		}
	}
	if total.Cmp(big.NewInt(181)) != 0 {
		t.Fatalf("total = %s, want 181", total)
	}
	for draw := int64(0); draw < 181; draw++ {
		index, err := weightedRuntimeCandidateIndex(weights, big.NewInt(draw))
		if err != nil {
			t.Fatal(err)
		}
		want := 0
		if draw >= 121 {
			want = 2
		} else if draw >= 61 {
			want = 1
		}
		if index != want {
			t.Fatalf("draw %d selected %d, want %d", draw, index, want)
		}
	}
	allUnlimited := []weightedRuntimeCandidate{
		{denominator: 0}, {denominator: 0},
	}
	weights, total, err = integerCandidateWeights(allUnlimited)
	if err != nil || total.Cmp(big.NewInt(2)) != 0 || weights[0].Cmp(big.NewInt(1)) != 0 || weights[1].Cmp(big.NewInt(1)) != 0 {
		t.Fatalf("all-unlimited weights = %v, total = %v, error = %v", weights, total, err)
	}
	large := []weightedRuntimeCandidate{{denominator: 60}, {denominator: maxUnixSecond}}
	weights, _, err = integerCandidateWeights(large)
	if err != nil || weights[0].Sign() <= 0 || weights[1].Sign() <= 0 || weights[0].Cmp(weights[1]) <= 0 {
		t.Fatalf("large-range weights = %v, error = %v", weights, err)
	}
}

func TestContinuousOrderUsesUnbiasedDrawAndFailsClosed(t *testing.T) {
	candidates := []weightedRuntimeCandidate{
		{candidate: RuntimeCandidate{DonationKeyID: 1}, denominator: 60},
		{candidate: RuntimeCandidate{DonationKeyID: 2}, denominator: 61},
		{candidate: RuntimeCandidate{DonationKeyID: 3}, denominator: 0},
	}
	// The first draw is rejected by crypto/rand.Int because 255 >= 181.
	ordered, err := orderWeightedRuntimeCandidates(bytes.NewReader([]byte{255, 121, 0}), candidates)
	if err != nil || !equalInt64s(runtimeCandidateIDs(ordered), []int64{3, 1, 2}) {
		t.Fatalf("ordered = %v, error = %v", runtimeCandidateIDs(ordered), err)
	}
	if _, err := orderWeightedRuntimeCandidates(failedEntropy{}, candidates); !errors.Is(err, ErrEntropyUnavailable) {
		t.Fatalf("failed entropy = %v", err)
	}
	stuck := &fixedByteEntropy{value: 255}
	if _, err := uniformBigIntn(stuck, big.NewInt(181)); !errors.Is(err, ErrEntropyUnavailable) || stuck.reads != maxRejectedSamples {
		t.Fatalf("bounded rejection = %v after %d reads", err, stuck.reads)
	}
	if _, err := uniformBigIntn(nil, big.NewInt(2)); !errors.Is(err, ErrEntropyUnavailable) {
		t.Fatalf("nil entropy = %v", err)
	}
	if _, err := uniformBigIntn(bytes.NewReader([]byte{0}), big.NewInt(0)); !errors.Is(err, ErrInvariant) {
		t.Fatalf("zero upper bound = %v", err)
	}
	if _, _, err := integerCandidateWeights([]weightedRuntimeCandidate{{denominator: -1}}); !errors.Is(err, ErrInvariant) {
		t.Fatalf("negative denominator = %v", err)
	}
	maximum := make([]weightedRuntimeCandidate, MaxRuntimeCandidates)
	for i := range maximum {
		maximum[i] = weightedRuntimeCandidate{candidate: RuntimeCandidate{DonationKeyID: int64(i + 1)}, denominator: int64(60 + i)}
	}
	ordered, err = orderWeightedRuntimeCandidates(&fixedByteEntropy{value: 0}, maximum)
	if err != nil || len(ordered) != MaxRuntimeCandidates {
		t.Fatalf("maximum order length = %d, error = %v", len(ordered), err)
	}
	if _, err := orderWeightedRuntimeCandidates(&fixedByteEntropy{value: 0}, append(maximum, maximum[0])); !errors.Is(err, ErrResourceLimit) {
		t.Fatalf("over-limit order = %v", err)
	}
}
