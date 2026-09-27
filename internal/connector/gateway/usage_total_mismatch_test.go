package gateway

import "testing"

func TestUsageMismatchSurvivesAnInvalidIndependentGroup(t *testing.T) {
	for _, raw := range []string{
		`{"inputTokens":{"total":99,"noCache":2,"cacheRead":0,"cacheWrite":0},"outputTokens":{"total":-1}}`,
		`{"inputTokens":{"total":-1},"outputTokens":{"total":99,"text":2,"reasoning":1}}`,
	} {
		usage, err := parseUsage([]byte(raw))
		if err == nil || usage.Present || !usage.TotalMismatch {
			t.Fatalf("independent discrepancy lost: %+v, %v", usage, err)
		}
	}
}
