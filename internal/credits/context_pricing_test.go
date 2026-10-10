package credits

import (
	"math"
	"testing"
)

func TestContextInputThresholdIncludesCachesWithoutOverflow(t *testing.T) {
	for _, tc := range []struct {
		usage     TokenUsage
		threshold int64
		want      bool
	}{
		{TokenUsage{UncachedInput: 5, CacheWriteInput: 2, CacheReadInput: 3}, 10, true},
		{TokenUsage{UncachedInput: 5, CacheWriteInput: 2, CacheReadInput: 2, Output: 9000}, 10, false},
		{TokenUsage{UncachedInput: math.MaxInt64, CacheReadInput: math.MaxInt64}, math.MaxInt64, true},
		{TokenUsage{UncachedInput: -1, CacheReadInput: 20}, 10, false},
	} {
		if got := tc.usage.InputReaches(tc.threshold); got != tc.want {
			t.Fatal(tc, got)
		}
	}
}
