package usageinfo

import (
	"errors"
	"math"
	"strings"
	"testing"

	"github.com/waiting-here/NonbiriAPI/internal/connector/contract"
)

func classified() contract.Usage {
	u := contract.Usage{Present: true, UncachedInputTokens: 8, CacheReadInputTokens: 2, OutputTokens: 4, SearchUnitsPresent: true, SearchUnitsMicros: 1250000}
	u.Modalities = contract.ModalityUsage{InputPresent: true, OutputPresent: true, Input: [3][4]int64{{5, 3, 0, 0}, {}, {2, 0, 0, 0}}, Output: [4]int64{1, 0, 3, 0}}
	return u
}

func TestCodecRoundTripAndLegacy(t *testing.T) {
	u := classified()
	encoded := Encode(u)
	got, err := Apply(contract.Usage{Present: true, UncachedInputTokens: 8, CacheReadInputTokens: 2, OutputTokens: 4}, encoded)
	if err != nil || got != u {
		t.Fatalf("round trip: %+v %v", got, err)
	}
	legacy, err := Apply(u, "{}")
	if err != nil || !legacy.Present || legacy.UncachedInputTokens != 8 || legacy.Modalities.InputPresent || legacy.Modalities.InputReason != contract.ModalityMissing || legacy.SearchUnitsPresent {
		t.Fatalf("legacy: %+v %v", legacy, err)
	}
	unknown := contract.Usage{SearchUnitsPresent: true, SearchUnitsMicros: 1234567}
	got, err = Apply(contract.Usage{}, Encode(unknown))
	if err != nil || got != unknown {
		t.Fatalf("unit-only round trip: %+v %v", got, err)
	}
}

func TestAddUsageCompletenessAndIndependentUnits(t *testing.T) {
	u := classified()
	got, err := AddUsage(Zero(), u)
	if err != nil || got != u {
		t.Fatalf("identity: %+v %v", got, err)
	}
	got, err = AddUsage(u, u)
	if err != nil || got.UncachedInputTokens != 16 || got.Modalities.Input[0][1] != 6 || got.SearchUnitsMicros != 2500000 {
		t.Fatalf("sum: %+v %v", got, err)
	}
	partial := u
	partial.Modalities.InputPresent = false
	partial.Modalities.InputReason = contract.ModalityIncomplete
	got, err = AddUsage(u, partial)
	if err != nil || got.Modalities.InputPresent || !got.Modalities.OutputPresent || got.Modalities.InputReason != contract.ModalityIncomplete {
		t.Fatalf("partial: %+v %v", got, err)
	}
	got, err = AddUsage(u, contract.Usage{Present: true, SearchUnitsPresent: true})
	if err != nil || got.Modalities.InputPresent || got.Modalities.OutputPresent || got.Modalities.InputReason != contract.ModalityMissing || !got.SearchUnitsPresent {
		t.Fatalf("known zero: %+v %v", got, err)
	}
	got, err = AddUsage(u, contract.Usage{SearchUnitsPresent: true, SearchUnitsMicros: 1})
	if err != nil || got.Present || got.Modalities.InputPresent || !got.SearchUnitsPresent || got.SearchUnitsMicros != 1250001 {
		t.Fatalf("unknown tokens: %+v %v", got, err)
	}
	got, err = AddUsage(u, contract.Usage{Present: true})
	if err != nil || !got.Present || got.SearchUnitsPresent || got.Modalities.InputPresent {
		t.Fatalf("unknown units: %+v %v", got, err)
	}
}

func TestOverflowInvalidatesOnlyAffectedKnownness(t *testing.T) {
	left, right := Zero(), Zero()
	left.UncachedInputTokens = math.MaxInt64
	right.UncachedInputTokens = 1
	got, err := AddUsage(left, right)
	if !errors.Is(err, ErrOverflow) || got.UncachedInputTokens != math.MaxInt64 || got.Present || !got.SearchUnitsPresent {
		t.Fatalf("token overflow: %+v %v", got, err)
	}
	left, right = Zero(), Zero()
	left.SearchUnitsMicros = math.MaxInt64
	right.SearchUnitsMicros = 1
	got, err = AddUsage(left, right)
	if !errors.Is(err, ErrOverflow) || !got.Present || got.SearchUnitsPresent || got.SearchUnitsMicros != 0 {
		t.Fatalf("unit overflow: %+v %v", got, err)
	}
}

func TestPricingAndBounds(t *testing.T) {
	p := Pricing{Mode: "per_token", InputMode: "modality", OutputMode: "aggregate", OutputFallback: "missing_price"}
	encoded, err := WithPricing(Encode(classified()), p)
	if err != nil {
		t.Fatal(err)
	}
	got, err := PricingFromUsage(encoded)
	if err != nil || got != p {
		t.Fatalf("pricing: %+v %v", got, err)
	}
	for _, invalid := range []string{"null", "[]", "{}{}", strings.Repeat(" ", 4097) + "{}", `{"donor_reward":123}`, `{"search_units_micros":-1}`, `{"pricing":{"mode":"per_token","input_fallback":"private upstream data"}}`} {
		if _, err := Apply(Zero(), invalid); !errors.Is(err, ErrInvalid) {
			t.Fatalf("accepted %q", invalid)
		}
	}
	if _, err := EncodePricing(Pricing{Mode: "token"}); !errors.Is(err, ErrInvalid) {
		t.Fatal("accepted unknown mode")
	}
	if p, err := DecodePricing(""); err != nil || p != (Pricing{}) {
		t.Fatal("legacy pricing", p, err)
	}
}
