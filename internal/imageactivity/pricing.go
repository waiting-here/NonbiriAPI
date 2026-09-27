package imageactivity

import (
	"fmt"
	"strconv"
)

// PricingPolicy is a complete, immutable per-image price schedule. Amounts are
// whole sketch-paper and brush units at the API boundary; parsePayment converts
// them to the existing milli-unit ledger representation.
type PricingPolicy struct {
	Default  Price       `json:"default"`
	Fallback string      `json:"fallback"`
	Tiers    []TierPrice `json:"tiers"`
	Sizes    []SizePrice `json:"sizes"`
}

type TierPrice struct {
	Tier string `json:"tier"`
	Price
}

type SizePrice struct {
	Width  int `json:"width"`
	Height int `json:"height"`
	Price
}

// PriceSelection is produced by validated capability resolution. A tier is
// supplied only when the capability explicitly assigns it; dimensions never
// imply a tier. Auto is its own selection and has no numeric dimensions.
type PriceSelection struct {
	Width  int    `json:"width,omitempty"`
	Height int    `json:"height,omitempty"`
	Tier   string `json:"tier,omitempty"`
	Auto   bool   `json:"auto,omitempty"`
}

type PriceQuote struct {
	Unit     Price  `json:"unit"`
	Total    Price  `json:"total"`
	Basis    string `json:"basis"`
	PriceKey string `json:"price_key"`
}

func ValidatePricingPolicy(p PricingPolicy) error {
	if p.Fallback != "default" && p.Fallback != "unavailable" || len(p.Tiers) > 64 || len(p.Sizes) > 2048 {
		return ErrInvalid
	}
	if _, err := parsePayment(p.Default, 1); err != nil {
		return ErrInvalid
	}
	tiers := make(map[string]bool, len(p.Tiers))
	for _, tier := range p.Tiers {
		if !safeText(tier.Tier, 128) || len(tier.Tier) == 0 || tiers[tier.Tier] {
			return ErrInvalid
		}
		if _, err := parsePayment(tier.Price, 1); err != nil {
			return ErrInvalid
		}
		tiers[tier.Tier] = true
	}
	sizes := make(map[[2]int]bool, len(p.Sizes))
	for _, size := range p.Sizes {
		key := [2]int{size.Width, size.Height}
		if size.Width < 1 || size.Width > maxDimension || size.Height < 1 || size.Height > maxDimension || sizes[key] {
			return ErrInvalid
		}
		if _, err := parsePayment(size.Price, 1); err != nil {
			return ErrInvalid
		}
		sizes[key] = true
	}
	return nil
}

// QuotePricing applies exact dimensions, then a declared tier, then fallback.
// The caller must validate the selection against the effective capability
// before quoting: a price is not evidence that a size is supported.
func QuotePricing(p PricingPolicy, selection PriceSelection, n int) (PriceQuote, error) {
	var out PriceQuote
	if err := ValidatePricingPolicy(p); err != nil || n < 1 || n > 16 {
		return out, ErrInvalid
	}
	if selection.Auto {
		if selection.Width != 0 || selection.Height != 0 || selection.Tier != "" && selection.Tier != "auto" {
			return out, ErrInvalid
		}
		selection.Tier = "auto"
	} else if (selection.Width == 0) != (selection.Height == 0) || selection.Width < 0 || selection.Height < 0 || selection.Width > maxDimension || selection.Height > maxDimension || len(selection.Tier) > 128 || selection.Tier != "" && !safeText(selection.Tier, 128) {
		return out, ErrInvalid
	}
	unit := p.Default
	basis := "default"
	key := ""
	found := false
	if !selection.Auto && selection.Width > 0 {
		for _, size := range p.Sizes {
			if size.Width == selection.Width && size.Height == selection.Height {
				unit, basis, key, found = size.Price, "size", fmt.Sprintf("%dx%d", size.Width, size.Height), true
				break
			}
		}
	}
	if !found && selection.Tier != "" {
		for _, tier := range p.Tiers {
			if tier.Tier == selection.Tier {
				unit, basis, key, found = tier.Price, "tier", tier.Tier, true
				break
			}
		}
	}
	if !found && p.Fallback == "unavailable" {
		return out, ErrUnavailable
	}
	if selection.Auto {
		basis, key = "auto", "auto"
	}
	total, err := parsePayment(unit, n)
	if err != nil {
		return out, ErrInvalid
	}
	out = PriceQuote{Unit: unit, Total: paymentPrice(total), Basis: basis, PriceKey: key}
	return out, nil
}

func legacyPricing(p Price) PricingPolicy {
	return PricingPolicy{Default: p, Fallback: "default", Tiers: []TierPrice{}, Sizes: []SizePrice{}}
}

func (s PriceSelection) sizeKey() string {
	if s.Width == 0 || s.Height == 0 {
		return ""
	}
	return strconv.Itoa(s.Width) + "x" + strconv.Itoa(s.Height)
}
