package charityrouting

import (
	"strconv"

	"github.com/waiting-here/NonbiriAPI/internal/credits"
)

type LongContextInput struct {
	InputThreshold string            `json:"input_threshold"`
	UserPrice      *string           `json:"user_price,omitempty"`
	UserPrices     *TokenPricesInput `json:"user_prices,omitempty"`
}
type ContextPenaltyInput struct {
	InputThreshold string `json:"input_threshold"`
	Fine           string `json:"fine"`
}
type AdminLongContext struct {
	InputThreshold string            `json:"input_threshold"`
	UserPrice      *string           `json:"user_price,omitempty"`
	UserPrices     *AdminTokenPrices `json:"user_prices,omitempty"`
}
type AdminContextPenalty struct {
	InputThreshold string `json:"input_threshold"`
	Fine           string `json:"fine"`
}
type StewardLongContext struct {
	InputThreshold string              `json:"input_threshold"`
	UserPrice      *string             `json:"user_price,omitempty"`
	UserPrices     *StewardTokenPrices `json:"user_prices,omitempty"`
}
type StewardContextPenalty struct {
	InputThreshold string `json:"input_threshold"`
	Fine           string `json:"fine"`
}
type CapabilityLongContext struct {
	InputThreshold            string                 `json:"input_threshold"`
	UserPriceMilli            *string                `json:"user_price_milli"`
	DiscountedUserPriceMilli  *string                `json:"discounted_user_price_milli"`
	UserPricesMilli           *CapabilityTokenPrices `json:"user_prices_milli"`
	DiscountedUserPricesMilli *CapabilityTokenPrices `json:"discounted_user_prices_milli"`
}
type CapabilityContextPenalty struct {
	InputThreshold string `json:"input_threshold"`
	FineMilli      string `json:"fine_milli"`
}

type longContextWire struct {
	InputThreshold requiredField[string]          `json:"input_threshold"`
	UserPrice      requiredField[string]          `json:"user_price"`
	UserPrices     requiredField[tokenPricesWire] `json:"user_prices"`
}
type contextPenaltyWire struct {
	InputThreshold requiredField[string] `json:"input_threshold"`
	Fine           requiredField[string] `json:"fine"`
}

func parseContextPricing(wire pricingWire, input *PricingInput, canonical map[string]any) error {
	if wire.LongContext.Set {
		canonical["long_context"] = nil
		if wire.LongContext.Value != nil {
			w := *wire.LongContext.Value
			if !w.InputThreshold.Set {
				return ErrInvalidRequest
			}
			long := &LongContextInput{InputThreshold: w.InputThreshold.Value}
			v := map[string]any{"input_threshold": w.InputThreshold.Value}
			if input.Mode == "per_request" {
				if !w.UserPrice.Set || w.UserPrices.Set {
					return ErrInvalidRequest
				}
				long.UserPrice = &w.UserPrice.Value
				v["user_price"] = w.UserPrice.Value
			} else {
				if w.UserPrice.Set || !w.UserPrices.Set {
					return ErrInvalidRequest
				}
				var err error
				long.UserPrices, v["user_prices"], err = parseTokenPrices(w.UserPrices.Value)
				if err != nil {
					return err
				}
			}
			input.LongContext = long
			canonical["long_context"] = v
		}
	}
	if wire.ContextPenalty.Set {
		canonical["context_penalty"] = nil
		if wire.ContextPenalty.Value != nil {
			w := *wire.ContextPenalty.Value
			if !w.InputThreshold.Set || !w.Fine.Set {
				return ErrInvalidRequest
			}
			input.ContextPenalty = &ContextPenaltyInput{InputThreshold: w.InputThreshold.Value, Fine: w.Fine.Value}
			canonical["context_penalty"] = input.ContextPenalty
		}
	}
	return nil
}
func validateContextPricing(input PricingInput) (credits.ContextPricing, error) {
	var out credits.ContextPricing
	threshold := func(v string) (int64, error) {
		n, err := credits.ParseAmount(v)
		if err != nil || n <= 0 {
			return 0, ErrInvalidRequest
		}
		return n, nil
	}
	if l := input.LongContext; l != nil {
		n, err := threshold(l.InputThreshold)
		if err != nil {
			return out, err
		}
		price := &credits.LongContextPrice{InputThreshold: n}
		if input.Mode == "per_request" {
			if l.UserPrice == nil || l.UserPrices != nil {
				return out, ErrInvalidRequest
			}
			price.RequestPrice, err = parseWireAmount(*l.UserPrice)
		} else {
			if l.UserPrice != nil || l.UserPrices == nil {
				return out, ErrInvalidRequest
			}
			v := l.UserPrices
			for i, text := range []string{v.UncachedInput, v.CacheWriteInput, v.CacheReadInput, v.Output} {
				price.UserPrices[i], err = parseWireAmount(text)
				if err != nil {
					return out, err
				}
			}
		}
		if err != nil {
			return out, err
		}
		out.LongContext = price
	}
	if p := input.ContextPenalty; p != nil {
		n, err := threshold(p.InputThreshold)
		if err != nil {
			return out, err
		}
		fine, err := parseWireAmount(p.Fine)
		if err != nil {
			return out, err
		}
		out.Penalty = &credits.ContextPenalty{InputThreshold: n, Fine: fine}
	}
	return out, nil
}
func adminContextPricing(pricing *AdminPricing, encoded string) error {
	p, err := credits.DecodeContextPricing(encoded)
	if err != nil {
		return ErrInvariant
	}
	if l := p.LongContext; l != nil {
		v := &AdminLongContext{InputThreshold: strconv.FormatInt(l.InputThreshold, 10)}
		if pricing.Mode == "per_request" {
			v.UserPrice = new(formatWireAmount(l.RequestPrice))
		} else {
			v.UserPrices = &AdminTokenPrices{UncachedInput: formatWireAmount(l.UserPrices[0]),
				CacheWriteInput: formatWireAmount(l.UserPrices[1]), CacheReadInput: formatWireAmount(l.UserPrices[2]), Output: formatWireAmount(l.UserPrices[3])}
		}
		pricing.LongContext = v
	}
	if p.Penalty != nil {
		pricing.ContextPenalty = &AdminContextPenalty{InputThreshold: strconv.FormatInt(p.Penalty.InputThreshold, 10), Fine: formatWireAmount(p.Penalty.Fine)}
	}
	return nil
}
func stewardContextPricing(p AdminPricing, out *StewardPricing) {
	if l := p.LongContext; l != nil {
		out.LongContext = &StewardLongContext{InputThreshold: l.InputThreshold, UserPrice: copyString(l.UserPrice)}
		if v := l.UserPrices; v != nil {
			out.LongContext.UserPrices = &StewardTokenPrices{UncachedInput: v.UncachedInput, CacheWriteInput: v.CacheWriteInput, CacheReadInput: v.CacheReadInput, Output: v.Output}
		}
	}
	if p.ContextPenalty != nil {
		out.ContextPenalty = &StewardContextPenalty{InputThreshold: p.ContextPenalty.InputThreshold, Fine: p.ContextPenalty.Fine}
	}
}
func capabilityContextPricing(out *CapabilityPricing, encoded string, discount int) error {
	p, err := credits.DecodeContextPricing(encoded)
	if err != nil {
		return ErrInvariant
	}
	if l := p.LongContext; l != nil {
		v, err := capabilityPricing(out.Mode, l.RequestPrice, l.UserPrices, discount)
		if err != nil {
			return err
		}
		out.LongContext = &CapabilityLongContext{
			InputThreshold: strconv.FormatInt(l.InputThreshold, 10), UserPriceMilli: v.UserPriceMilli,
			DiscountedUserPriceMilli: v.DiscountedUserPriceMilli, UserPricesMilli: v.UserPricesMilli,
			DiscountedUserPricesMilli: v.DiscountedUserPricesMilli,
		}
	}
	if p.Penalty != nil {
		out.ContextPenalty = &CapabilityContextPenalty{InputThreshold: strconv.FormatInt(p.Penalty.InputThreshold, 10), FineMilli: strconv.FormatInt(p.Penalty.Fine, 10)}
	}
	return nil
}
