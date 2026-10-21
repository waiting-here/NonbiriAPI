package charityrouting

import (
	"strconv"

	"github.com/waiting-here/NonbiriAPI/internal/credits"
)

type ModalityPricingInput struct {
	InputEnabled  bool              `json:"input_enabled"`
	OutputEnabled bool              `json:"output_enabled"`
	Text          *TokenPricesInput `json:"text,omitempty"`
	Image         *TokenPricesInput `json:"image,omitempty"`
	Audio         *TokenPricesInput `json:"audio,omitempty"`
	Video         *TokenPricesInput `json:"video,omitempty"`
}

type AdminModalityPricing struct {
	InputEnabled  bool              `json:"input_enabled"`
	OutputEnabled bool              `json:"output_enabled"`
	Text          *AdminTokenPrices `json:"text,omitempty"`
	Image         *AdminTokenPrices `json:"image,omitempty"`
	Audio         *AdminTokenPrices `json:"audio,omitempty"`
	Video         *AdminTokenPrices `json:"video,omitempty"`
}

type StewardModalityPricing struct {
	InputEnabled  bool                `json:"input_enabled"`
	OutputEnabled bool                `json:"output_enabled"`
	Text          *StewardTokenPrices `json:"text,omitempty"`
	Image         *StewardTokenPrices `json:"image,omitempty"`
	Audio         *StewardTokenPrices `json:"audio,omitempty"`
	Video         *StewardTokenPrices `json:"video,omitempty"`
}

type CapabilityModalityPricing struct {
	InputEnabled  bool                   `json:"input_enabled"`
	OutputEnabled bool                   `json:"output_enabled"`
	Text          *CapabilityTokenPrices `json:"text,omitempty"`
	Image         *CapabilityTokenPrices `json:"image,omitempty"`
	Audio         *CapabilityTokenPrices `json:"audio,omitempty"`
	Video         *CapabilityTokenPrices `json:"video,omitempty"`
}

type modalityPricingWire struct {
	InputEnabled  requiredField[bool]            `json:"input_enabled"`
	OutputEnabled requiredField[bool]            `json:"output_enabled"`
	Text          nullableField[tokenPricesWire] `json:"text"`
	Image         nullableField[tokenPricesWire] `json:"image"`
	Audio         nullableField[tokenPricesWire] `json:"audio"`
	Video         nullableField[tokenPricesWire] `json:"video"`
}

func parseModalityPricing(w *modalityPricingWire) (*ModalityPricingInput, error) {
	if w == nil {
		return nil, nil
	}
	if !w.InputEnabled.Set || !w.OutputEnabled.Set {
		return nil, ErrInvalidRequest
	}
	out := &ModalityPricingInput{InputEnabled: w.InputEnabled.Value, OutputEnabled: w.OutputEnabled.Value}
	inputs := []nullableField[tokenPricesWire]{w.Text, w.Image, w.Audio, w.Video}
	slots := []**TokenPricesInput{&out.Text, &out.Image, &out.Audio, &out.Video}
	for i, input := range inputs {
		if input.Value != nil {
			value, _, err := parseTokenPrices(*input.Value)
			if err != nil {
				return nil, err
			}
			*slots[i] = value
		}
	}
	return out, nil
}
func validateModalityPricing(p *ModalityPricingInput) (*credits.ModalityPricing, error) {
	if p == nil {
		return nil, nil
	}
	out := &credits.ModalityPricing{InputEnabled: p.InputEnabled, OutputEnabled: p.OutputEnabled}
	inputs := []*TokenPricesInput{p.Text, p.Image, p.Audio, p.Video}
	slots := []**credits.TokenPrices{&out.Text, &out.Image, &out.Audio, &out.Video}
	for i, input := range inputs {
		if input == nil {
			continue
		}
		values, err := tokenPriceValues(input)
		if err != nil {
			return nil, err
		}
		*slots[i] = &credits.TokenPrices{UncachedInput: values[0], CacheWriteInput: values[1], CacheReadInput: values[2], Output: values[3]}
	}
	return out, nil
}
func tokenPriceValues(p *TokenPricesInput) ([4]int64, error) {
	var out [4]int64
	if p == nil {
		return out, ErrInvalidRequest
	}
	for i, value := range []string{p.UncachedInput, p.CacheWriteInput, p.CacheReadInput, p.Output} {
		n, err := parseWireAmount(value)
		if err != nil {
			return out, err
		}
		out[i] = n
	}
	return out, nil
}
func adminModalityPricing(p *credits.ModalityPricing) *AdminModalityPricing {
	if p == nil {
		return nil
	}
	out := &AdminModalityPricing{InputEnabled: p.InputEnabled, OutputEnabled: p.OutputEnabled}
	slots := []**AdminTokenPrices{&out.Text, &out.Image, &out.Audio, &out.Video}
	for i, v := range []*credits.TokenPrices{p.Text, p.Image, p.Audio, p.Video} {
		if v != nil {
			*slots[i] = &AdminTokenPrices{formatWireAmount(v.UncachedInput), formatWireAmount(v.CacheWriteInput), formatWireAmount(v.CacheReadInput), formatWireAmount(v.Output)}
		}
	}
	return out
}
func stewardModalityPricing(p *AdminModalityPricing) *StewardModalityPricing {
	if p == nil {
		return nil
	}
	out := &StewardModalityPricing{InputEnabled: p.InputEnabled, OutputEnabled: p.OutputEnabled}
	slots := []**StewardTokenPrices{&out.Text, &out.Image, &out.Audio, &out.Video}
	for i, v := range []*AdminTokenPrices{p.Text, p.Image, p.Audio, p.Video} {
		if v != nil {
			*slots[i] = &StewardTokenPrices{v.UncachedInput, v.CacheWriteInput, v.CacheReadInput, v.Output}
		}
	}
	return out
}
func capabilityModalityPricing(p *credits.ModalityPricing, discount int) (*CapabilityModalityPricing, error) {
	if p == nil {
		return nil, nil
	}
	out := &CapabilityModalityPricing{InputEnabled: p.InputEnabled, OutputEnabled: p.OutputEnabled}
	slots := []**CapabilityTokenPrices{&out.Text, &out.Image, &out.Audio, &out.Video}
	for i, v := range []*credits.TokenPrices{p.Text, p.Image, p.Audio, p.Video} {
		if v == nil {
			continue
		}
		var values [4]string
		for j, n := range []int64{v.UncachedInput, v.CacheWriteInput, v.CacheReadInput, v.Output} {
			price, err := credits.ApplyDiscountPercent(n, discount)
			if err != nil {
				return nil, ErrInvariant
			}
			values[j] = strconv.FormatInt(price, 10)
		}
		*slots[i] = &CapabilityTokenPrices{values[0], values[1], values[2], values[3]}
	}
	return out, nil
}
