package charityrouting

import (
	"strconv"

	"github.com/waiting-here/NonbiriAPI/internal/credits"
)

type LongContextInput struct {
	DonorReward     *string               `json:"donor_reward,omitempty"`
	DonorRewards    *TokenPricesInput     `json:"donor_rewards,omitempty"`
	UserModalities  *ModalityPricingInput `json:"user_modalities,omitempty"`
	DonorModalities *ModalityPricingInput `json:"donor_modalities,omitempty"`
	InputThreshold  string                `json:"input_threshold"`
	UserPrice       *string               `json:"user_price,omitempty"`
	UserPrices      *TokenPricesInput     `json:"user_prices,omitempty"`
}
type ContextPenaltyInput struct {
	InputThreshold string `json:"input_threshold"`
	Fine           string `json:"fine"`
}
type AdminLongContext struct {
	DonorReward     *string               `json:"donor_reward,omitempty"`
	DonorRewards    *AdminTokenPrices     `json:"donor_rewards,omitempty"`
	UserModalities  *AdminModalityPricing `json:"user_modalities,omitempty"`
	DonorModalities *AdminModalityPricing `json:"donor_modalities,omitempty"`
	InputThreshold  string                `json:"input_threshold"`
	UserPrice       *string               `json:"user_price,omitempty"`
	UserPrices      *AdminTokenPrices     `json:"user_prices,omitempty"`
}
type AdminContextPenalty struct {
	InputThreshold string `json:"input_threshold"`
	Fine           string `json:"fine"`
}
type StewardLongContext struct {
	DonorReward     *string                 `json:"donor_reward,omitempty"`
	DonorRewards    *StewardTokenPrices     `json:"donor_rewards,omitempty"`
	UserModalities  *StewardModalityPricing `json:"user_modalities,omitempty"`
	DonorModalities *StewardModalityPricing `json:"donor_modalities,omitempty"`
	InputThreshold  string                  `json:"input_threshold"`
	UserPrice       *string                 `json:"user_price,omitempty"`
	UserPrices      *StewardTokenPrices     `json:"user_prices,omitempty"`
}
type StewardContextPenalty struct {
	InputThreshold string `json:"input_threshold"`
	Fine           string `json:"fine"`
}
type CapabilityLongContext struct {
	UserModalitiesMilli           *CapabilityModalityPricing `json:"user_modalities_milli,omitempty"`
	DiscountedUserModalitiesMilli *CapabilityModalityPricing `json:"discounted_user_modalities_milli,omitempty"`
	CurrentUserModalitiesMilli    *CapabilityModalityPricing `json:"current_user_modalities_milli,omitempty"`
	CurrentUserPriceMilli         *string                    `json:"current_user_price_milli"`
	CurrentUserPricesMilli        *CapabilityTokenPrices     `json:"current_user_prices_milli"`
	InputThreshold                string                     `json:"input_threshold"`
	UserPriceMilli                *string                    `json:"user_price_milli"`
	DiscountedUserPriceMilli      *string                    `json:"discounted_user_price_milli"`
	UserPricesMilli               *CapabilityTokenPrices     `json:"user_prices_milli"`
	DiscountedUserPricesMilli     *CapabilityTokenPrices     `json:"discounted_user_prices_milli"`
}
type CapabilityContextPenalty struct {
	InputThreshold string `json:"input_threshold"`
	FineMilli      string `json:"fine_milli"`
}

type longContextWire struct {
	DonorReward     requiredField[string]              `json:"donor_reward"`
	DonorRewards    requiredField[tokenPricesWire]     `json:"donor_rewards"`
	UserModalities  nullableField[modalityPricingWire] `json:"user_modalities"`
	DonorModalities nullableField[modalityPricingWire] `json:"donor_modalities"`
	InputThreshold  requiredField[string]              `json:"input_threshold"`
	UserPrice       requiredField[string]              `json:"user_price"`
	UserPrices      requiredField[tokenPricesWire]     `json:"user_prices"`
}
type contextPenaltyWire struct {
	InputThreshold requiredField[string] `json:"input_threshold"`
	Fine           requiredField[string] `json:"fine"`
}

func parseContextPricing(wire pricingWire, input *PricingInput, canonical map[string]any) error {
	var err error
	input.UserModalities, err = parseModalityPricing(wire.UserModalities.Value)
	if err != nil {
		return err
	}
	input.DonorModalities, err = parseModalityPricing(wire.DonorModalities.Value)
	if err != nil {
		return err
	}
	if wire.UserModalities.Set {
		canonical["user_modalities"] = input.UserModalities
	}
	if wire.DonorModalities.Set {
		canonical["donor_modalities"] = input.DonorModalities
	}
	if wire.LongContext.Set {
		canonical["long_context"] = nil
		if wire.LongContext.Value != nil {
			w := *wire.LongContext.Value
			if !w.InputThreshold.Set {
				return ErrInvalidRequest
			}
			if input.Mode == "per_search_unit" {
				return ErrInvalidRequest
			}
			long := &LongContextInput{InputThreshold: w.InputThreshold.Value}
			var err error
			long.UserModalities, err = parseModalityPricing(w.UserModalities.Value)
			if err != nil {
				return err
			}
			long.DonorModalities, err = parseModalityPricing(w.DonorModalities.Value)
			if err != nil {
				return err
			}
			v := map[string]any{"input_threshold": w.InputThreshold.Value}
			if input.Mode == "per_request" {
				if !w.UserPrice.Set || w.UserPrices.Set || w.DonorRewards.Set {
					return ErrInvalidRequest
				}
				long.UserPrice = &w.UserPrice.Value
				v["user_price"] = w.UserPrice.Value
				if w.DonorReward.Set {
					long.DonorReward = &w.DonorReward.Value
					v["donor_reward"] = w.DonorReward.Value
				}
			} else {
				if w.UserPrice.Set || !w.UserPrices.Set || w.DonorReward.Set {
					return ErrInvalidRequest
				}
				var err error
				long.UserPrices, v["user_prices"], err = parseTokenPrices(w.UserPrices.Value)
				if err != nil {
					return err
				}
			}
			if w.DonorRewards.Set {
				long.DonorRewards, v["donor_rewards"], err = parseTokenPrices(w.DonorRewards.Value)
				if err != nil {
					return err
				}
			}
			if w.UserModalities.Set {
				v["user_modalities"] = long.UserModalities
			}
			if w.DonorModalities.Set {
				v["donor_modalities"] = long.DonorModalities
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
	var err error
	if input.Mode != "per_token" && (input.UserModalities != nil || input.DonorModalities != nil) {
		return out, ErrInvalidRequest
	}
	out.UserModalities, err = validateModalityPricing(input.UserModalities)
	if err != nil {
		return out, err
	}
	out.DonorModalities, err = validateModalityPricing(input.DonorModalities)
	if err != nil {
		return out, err
	}
	threshold := func(v string) (int64, error) {
		n, err := credits.ParseAmount(v)
		if err != nil || n <= 0 {
			return 0, ErrInvalidRequest
		}
		return n, nil
	}
	if l := input.LongContext; l != nil {
		if input.Mode == "per_search_unit" || input.Mode != "per_token" && (l.UserModalities != nil || l.DonorModalities != nil) {
			return out, ErrInvalidRequest
		}
		n, err := threshold(l.InputThreshold)
		if err != nil {
			return out, err
		}
		price := &credits.LongContextPrice{InputThreshold: n}
		if input.Mode == "per_request" {
			if l.UserPrice == nil || l.UserPrices != nil || l.DonorRewards != nil {
				return out, ErrInvalidRequest
			}
			price.RequestPrice, err = parseWireAmount(*l.UserPrice)
			if err == nil && l.DonorReward != nil {
				var n int64
				n, err = parseWireAmount(*l.DonorReward)
				price.DonorReward = &n
			}
		} else {
			if l.UserPrice != nil || l.UserPrices == nil || l.DonorReward != nil {
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
		if l.DonorRewards != nil {
			values, e := tokenPriceValues(l.DonorRewards)
			if e != nil {
				return out, e
			}
			price.DonorPrices = &values
		}
		price.UserModalities, err = validateModalityPricing(l.UserModalities)
		if err != nil {
			return out, err
		}
		price.DonorModalities, err = validateModalityPricing(l.DonorModalities)
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
	pricing.UserModalities = adminModalityPricing(p.UserModalities)
	pricing.DonorModalities = adminModalityPricing(p.DonorModalities)
	if l := p.LongContext; l != nil {
		v := &AdminLongContext{InputThreshold: strconv.FormatInt(l.InputThreshold, 10)}
		if pricing.Mode == "per_request" {
			v.UserPrice = new(formatWireAmount(l.RequestPrice))
		} else {
			v.UserPrices = &AdminTokenPrices{UncachedInput: formatWireAmount(l.UserPrices[0]),
				CacheWriteInput: formatWireAmount(l.UserPrices[1]), CacheReadInput: formatWireAmount(l.UserPrices[2]), Output: formatWireAmount(l.UserPrices[3])}
		}
		if l.DonorReward != nil {
			v.DonorReward = new(formatWireAmount(*l.DonorReward))
		}
		if l.DonorPrices != nil {
			v.DonorRewards = &AdminTokenPrices{formatWireAmount(l.DonorPrices[0]), formatWireAmount(l.DonorPrices[1]), formatWireAmount(l.DonorPrices[2]), formatWireAmount(l.DonorPrices[3])}
		}
		v.UserModalities, v.DonorModalities = adminModalityPricing(l.UserModalities), adminModalityPricing(l.DonorModalities)
		pricing.LongContext = v
	}
	if p.Penalty != nil {
		pricing.ContextPenalty = &AdminContextPenalty{InputThreshold: strconv.FormatInt(p.Penalty.InputThreshold, 10), Fine: formatWireAmount(p.Penalty.Fine)}
	}
	return nil
}
func stewardContextPricing(p AdminPricing, out *StewardPricing) {
	out.UserModalities, out.DonorModalities = stewardModalityPricing(p.UserModalities), stewardModalityPricing(p.DonorModalities)
	if l := p.LongContext; l != nil {
		out.LongContext = &StewardLongContext{InputThreshold: l.InputThreshold, UserPrice: copyString(l.UserPrice), DonorReward: copyString(l.DonorReward), UserModalities: stewardModalityPricing(l.UserModalities), DonorModalities: stewardModalityPricing(l.DonorModalities)}
		if v := l.DonorRewards; v != nil {
			out.LongContext.DonorRewards = &StewardTokenPrices{v.UncachedInput, v.CacheWriteInput, v.CacheReadInput, v.Output}
		}
		if v := l.UserPrices; v != nil {
			out.LongContext.UserPrices = &StewardTokenPrices{UncachedInput: v.UncachedInput, CacheWriteInput: v.CacheWriteInput, CacheReadInput: v.CacheReadInput, Output: v.Output}
		}
	}
	if p.ContextPenalty != nil {
		out.ContextPenalty = &StewardContextPenalty{InputThreshold: p.ContextPenalty.InputThreshold, Fine: p.ContextPenalty.Fine}
	}
}
func capabilityContextPricing(out *CapabilityPricing, encoded string, discount, current int) error {
	p, err := credits.DecodeContextPricing(encoded)
	if err != nil {
		return ErrInvariant
	}
	out.UserModalitiesMilli, err = capabilityModalityPricing(p.UserModalities, 100)
	if err != nil {
		return err
	}
	out.DiscountedUserModalitiesMilli, err = capabilityModalityPricing(p.UserModalities, discount)
	if err != nil {
		return err
	}
	out.CurrentUserModalitiesMilli, err = capabilityModalityPricing(p.UserModalities, current)
	if err != nil {
		return err
	}
	if l := p.LongContext; l != nil {
		v, err := capabilityPricing(out.Mode, l.RequestPrice, l.UserPrices, discount, current)
		if err != nil {
			return err
		}
		v.UserModalitiesMilli, err = capabilityModalityPricing(l.UserModalities, 100)
		if err != nil {
			return err
		}
		v.DiscountedUserModalitiesMilli, err = capabilityModalityPricing(l.UserModalities, discount)
		if err != nil {
			return err
		}
		v.CurrentUserModalitiesMilli, err = capabilityModalityPricing(l.UserModalities, current)
		if err != nil {
			return err
		}
		out.LongContext = &CapabilityLongContext{
			UserModalitiesMilli: v.UserModalitiesMilli, DiscountedUserModalitiesMilli: v.DiscountedUserModalitiesMilli, CurrentUserModalitiesMilli: v.CurrentUserModalitiesMilli,
			CurrentUserPriceMilli: v.CurrentUserPriceMilli, CurrentUserPricesMilli: v.CurrentUserPricesMilli,
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
