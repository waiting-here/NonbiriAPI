package imageactivity

import (
	"errors"
	"testing"
)

func TestPricingPrecedenceAndSelection(t *testing.T) {
	p := PricingPolicy{
		Default:  Price{"2", "1"},
		Fallback: "default",
		Tiers: []TierPrice{
			{Tier: "medium", Price: Price{"3", "1"}},
			{Tier: "auto", Price: Price{"5", "0"}},
		},
		Sizes: []SizePrice{
			{Width: 1024, Height: 768, Price: Price{"7", "0"}},
			{Width: 768, Height: 1024, Price: Price{"8", "0"}},
		},
	}
	for _, tc := range []struct {
		name      string
		selection PriceSelection
		unit      Price
		basis     string
		key       string
	}{
		{"exact beats tier", PriceSelection{Width: 1024, Height: 768, Tier: "medium"}, Price{"7", "0"}, "size", "1024x768"},
		{"portrait differs", PriceSelection{Width: 768, Height: 1024, Tier: "medium"}, Price{"8", "0"}, "size", "768x1024"},
		{"tier with unknown dimensions", PriceSelection{Tier: "medium"}, Price{"3", "1"}, "tier", "medium"},
		{"no inferred tier", PriceSelection{Width: 512, Height: 512}, Price{"2", "1"}, "default", ""},
		{"explicit auto", PriceSelection{Auto: true}, Price{"5", "0"}, "auto", "auto"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			quote, err := QuotePricing(p, tc.selection, 3)
			if err != nil {
				t.Fatal(err)
			}
			if quote.Unit != tc.unit || quote.Basis != tc.basis || quote.PriceKey != tc.key || quote.Total != (Price{paperTimes3(tc.unit.Paper), paperTimes3(tc.unit.Brush)}) {
				t.Fatalf("unexpected quote: %+v", quote)
			}
		})
	}
}

func paperTimes3(v string) string {
	switch v {
	case "0":
		return "0"
	case "1":
		return "3"
	case "2":
		return "6"
	case "3":
		return "9"
	case "5":
		return "15"
	case "7":
		return "21"
	case "8":
		return "24"
	}
	panic("unexpected fixture")
}

func TestPricingRejectsUnavailableAndInvalidPrices(t *testing.T) {
	p := PricingPolicy{Default: Price{"1", "0"}, Fallback: "unavailable", Tiers: []TierPrice{{Tier: "small", Price: Price{"2", "0"}}}}
	if _, err := QuotePricing(p, PriceSelection{Width: 1024, Height: 768}, 1); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("missing price should be unavailable: %v", err)
	}
	if quote, err := QuotePricing(p, PriceSelection{Tier: "small"}, 2); err != nil || quote.Total.Paper != "4" {
		t.Fatalf("tier quote: %+v %v", quote, err)
	}
	for _, bad := range []PricingPolicy{
		{Default: Price{"0", "0"}, Fallback: "default"},
		{Default: Price{"1.5", "0"}, Fallback: "default"},
		{Default: Price{"1", "0"}, Fallback: "default", Tiers: []TierPrice{{Tier: "x", Price: Price{"1", "0"}}, {Tier: "x", Price: Price{"2", "0"}}}},
		{Default: Price{"1", "0"}, Fallback: "default", Sizes: []SizePrice{{Width: 1, Height: 2, Price: Price{"1", "0"}}, {Width: 1, Height: 2, Price: Price{"2", "0"}}}},
	} {
		if err := ValidatePricingPolicy(bad); err == nil {
			t.Fatalf("invalid schedule accepted: %+v", bad)
		}
	}
	if _, err := QuotePricing(PricingPolicy{Default: Price{"170141183460469231731687303715884105", "0"}, Fallback: "default"}, PriceSelection{}, 16); err == nil {
		t.Fatal("milli-unit multiplication overflow accepted")
	}
	if _, err := QuotePricing(legacyPricing(Price{"1", "0"}), PriceSelection{Auto: true, Width: 256}, 1); err == nil {
		t.Fatal("numeric dimensions attached to auto selection")
	}
}
