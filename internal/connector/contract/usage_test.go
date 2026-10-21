package contract

import (
	"math"
	"testing"
)

func TestParseSearchUnitsExact(t *testing.T) {
	for _, tc := range []struct {
		raw  string
		want int64
	}{
		{"0", 0}, {"0.000001", 1}, {"1.234567", 1234567}, {"1.2345670", 1234567}, {"1e-6", 1}, {"12.5e2", 1250000000}, {"9223372036854.775807", math.MaxInt64},
	} {
		if got, ok := ParseSearchUnits([]byte(tc.raw)); !ok || got != tc.want {
			t.Errorf("%s = %d, %v; want %d", tc.raw, got, ok, tc.want)
		}
	}
	for _, raw := range []string{"", "null", "true", `"1"`, "NaN", "Infinity", "-1", "-0", "1e-7", "1.0000001", "9223372036854.775808", "1e9999999", "01", "1 2", "[]", "{}"} {
		if got, ok := ParseSearchUnits([]byte(raw)); ok {
			t.Errorf("accepted %q as %d", raw, got)
		}
	}
}

func TestUsageRemainsComparable(t *testing.T) {
	usage := Usage{Present: true, Modalities: ModalityUsage{InputPresent: true, Input: [3][4]int64{{1, 2, 3, 4}}}}
	if copy := usage; copy != usage {
		t.Fatal("copy changed usage")
	}
}
