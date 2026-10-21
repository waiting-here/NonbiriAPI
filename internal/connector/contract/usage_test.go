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

func TestNormalizeUsageDetailsBoundsPersistedMetadata(t *testing.T) {
	u := Usage{Present: true, UncachedInputTokens: 3, OutputTokens: 2, SearchUnitsMicros: -1, SearchUnitsPresent: true, Modalities: ModalityUsage{InputPresent: true, Input: [3][4]int64{{1, 1}}, OutputReason: "raw untrusted text", Output: [4]int64{1, 2}}}
	n := NormalizeUsageDetails(u)
	if !n.Present || n.UncachedInputTokens != 3 || n.SearchUnitsPresent || n.SearchUnitsMicros != 0 || n.Modalities.InputPresent || n.Modalities.InputReason != ModalityMismatch || n.Modalities.OutputReason != ModalityInvalid || n.Modalities.Input != [3][4]int64{} || n.Modalities.Output != [4]int64{} {
		t.Fatal(n)
	}
	u.Modalities = ModalityUsage{InputPresent: true, Input: [3][4]int64{{3}}, InputReason: "raw", OutputPresent: true, Output: [4]int64{2}, OutputReason: "raw"}
	n = NormalizeUsageDetails(u)
	if !n.Modalities.InputPresent || !n.Modalities.OutputPresent || n.Modalities.InputReason != "" || n.Modalities.OutputReason != "" {
		t.Fatal(n)
	}
	u.Present = false
	if n := NormalizeUsageDetails(u); n.Modalities != (ModalityUsage{}) {
		t.Fatal(n)
	}
}
