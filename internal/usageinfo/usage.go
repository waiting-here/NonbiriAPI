// Package usageinfo encodes bounded, connector-neutral usage metadata.
package usageinfo

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"math"

	"github.com/waiting-here/NonbiriAPI/internal/connector/contract"
)

var (
	ErrInvalid  = errors.New("usageinfo: invalid metadata")
	ErrOverflow = errors.New("usageinfo: aggregate overflow")
)

type Pricing struct {
	Mode           string `json:"mode,omitempty"`
	InputMode      string `json:"input_mode,omitempty"`
	OutputMode     string `json:"output_mode,omitempty"`
	InputFallback  string `json:"input_fallback,omitempty"`
	OutputFallback string `json:"output_fallback,omitempty"`
}

type modalities struct {
	Input         [3][4]int64 `json:"input"`
	Output        [4]int64    `json:"output"`
	InputPresent  bool        `json:"input_present"`
	OutputPresent bool        `json:"output_present"`
	InputReason   string      `json:"input_reason,omitempty"`
	OutputReason  string      `json:"output_reason,omitempty"`
}

type details struct {
	Modalities        *modalities `json:"modalities,omitempty"`
	SearchUnitsMicros *int64      `json:"search_units_micros,omitempty"`
	Pricing           *Pricing    `json:"pricing,omitempty"`
}

// Normalize preserves aggregate counts and supplies closed fallback reasons.
func Normalize(u contract.Usage) contract.Usage {
	u = contract.NormalizeUsageDetails(u)
	if u.Present {
		if !u.Modalities.InputPresent && u.Modalities.InputReason == "" {
			u.Modalities.InputReason = contract.ModalityMissing
		}
		if !u.Modalities.OutputPresent && u.Modalities.OutputReason == "" {
			u.Modalities.OutputReason = contract.ModalityMissing
		}
	}
	return u
}

func Encode(u contract.Usage) string {
	u = Normalize(u)
	d := details{}
	if u.Present {
		m := modalities(u.Modalities)
		d.Modalities = &m
	}
	if u.SearchUnitsPresent {
		n := u.SearchUnitsMicros
		d.SearchUnitsMicros = &n
	}
	b, _ := json.Marshal(d)
	return string(b)
}

// Apply restores optional details without replacing aggregate counts or knownness.
func Apply(aggregate contract.Usage, encoded string) (contract.Usage, error) {
	d, err := decode(encoded)
	if err != nil {
		return contract.Usage{}, err
	}
	aggregate.Modalities = contract.ModalityUsage{}
	aggregate.SearchUnitsMicros, aggregate.SearchUnitsPresent = 0, false
	if d.Modalities != nil {
		aggregate.Modalities = contract.ModalityUsage(*d.Modalities)
	}
	if d.SearchUnitsMicros != nil {
		aggregate.SearchUnitsMicros, aggregate.SearchUnitsPresent = *d.SearchUnitsMicros, true
	}
	return Normalize(aggregate), nil
}

func WithPricing(encoded string, pricing Pricing) (string, error) {
	d, err := decode(encoded)
	if err != nil {
		return "", err
	}
	if !validPricing(pricing) {
		return "", ErrInvalid
	}
	d.Pricing = nil
	if pricing != (Pricing{}) {
		d.Pricing = &pricing
	}
	b, err := json.Marshal(d)
	if err != nil || len(b) > 4096 {
		return "", ErrInvalid
	}
	return string(b), nil
}

// PricingFromUsage returns only the caller's charge decision.
func PricingFromUsage(encoded string) (Pricing, error) {
	d, err := decode(encoded)
	if err != nil {
		return Pricing{}, err
	}
	if d.Pricing == nil {
		return Pricing{}, nil
	}
	return *d.Pricing, nil
}

func EncodePricing(pricing Pricing) (string, error) {
	if !validPricing(pricing) {
		return "", ErrInvalid
	}
	b, _ := json.Marshal(pricing)
	return string(b), nil
}

func DecodePricing(encoded string) (Pricing, error) {
	var p Pricing
	if err := decodeObject(encoded, 2048, &p); err != nil || !validPricing(p) {
		return Pricing{}, ErrInvalid
	}
	return p, nil
}

func validPricing(p Pricing) bool {
	if p == (Pricing{}) {
		return true
	}
	if p.Mode != "per_token" && p.Mode != "per_request" && p.Mode != "per_search_unit" {
		return false
	}
	for _, m := range []string{p.InputMode, p.OutputMode} {
		if m != "" && m != "aggregate" && m != "modality" {
			return false
		}
	}
	for _, r := range []string{p.InputFallback, p.OutputFallback} {
		if !validReason(r, true) {
			return false
		}
	}
	return true
}

func validReason(reason string, price bool) bool {
	switch reason {
	case "", contract.ModalityMissing, contract.ModalityInvalid, contract.ModalityIncomplete, contract.ModalityUnknown, contract.ModalityCacheUnknown, contract.ModalityMismatch, contract.ModalityReasoningUnknown:
		return true
	case "missing_price":
		return price
	default:
		return false
	}
}

func decode(encoded string) (details, error) {
	var d details
	if err := decodeObject(encoded, 4096, &d); err != nil {
		return details{}, err
	}
	if d.SearchUnitsMicros != nil && *d.SearchUnitsMicros < 0 {
		return details{}, ErrInvalid
	}
	if d.Pricing != nil && !validPricing(*d.Pricing) {
		return details{}, ErrInvalid
	}
	if m := d.Modalities; m != nil {
		if !validReason(m.InputReason, false) || !validReason(m.OutputReason, false) {
			return details{}, ErrInvalid
		}
		for _, row := range m.Input {
			for _, n := range row {
				if n < 0 {
					return details{}, ErrInvalid
				}
			}
		}
		for _, n := range m.Output {
			if n < 0 {
				return details{}, ErrInvalid
			}
		}
	}
	return d, nil
}

func decodeObject(encoded string, limit int, target any) error {
	if encoded == "" {
		encoded = "{}"
	}
	raw := bytes.TrimSpace([]byte(encoded))
	if len(encoded) > limit || len(raw) == 0 || raw[0] != '{' {
		return ErrInvalid
	}
	d := json.NewDecoder(bytes.NewReader(raw))
	d.DisallowUnknownFields()
	if d.Decode(target) != nil {
		return ErrInvalid
	}
	if d.Decode(new(any)) != io.EOF {
		return ErrInvalid
	}
	return nil
}

// Zero is the identity for a collection with no contributing reports.
func Zero() contract.Usage {
	return contract.Usage{Present: true, SearchUnitsPresent: true, Modalities: contract.ModalityUsage{InputPresent: true, OutputPresent: true}}
}

// AddUsage preserves legacy saturated totals on overflow, while invalidating
// the affected knownness. Missing reports never become known zero reports.
func AddUsage(left, right contract.Usage) (contract.Usage, error) {
	left, right = Normalize(left), Normalize(right)
	out := contract.Usage{Present: left.Present && right.Present, TotalMismatch: left.TotalMismatch || right.TotalMismatch}
	var resultErr error
	add := func(a, b int64) int64 {
		if a < 0 || b < 0 {
			resultErr = ErrInvalid
			out.Present = false
			return 0
		}
		if b > math.MaxInt64-a {
			resultErr = ErrOverflow
			out.Present = false
			return math.MaxInt64
		}
		return a + b
	}
	out.UncachedInputTokens = add(left.UncachedInputTokens, right.UncachedInputTokens)
	out.CacheWriteInputTokens = add(left.CacheWriteInputTokens, right.CacheWriteInputTokens)
	out.CacheReadInputTokens = add(left.CacheReadInputTokens, right.CacheReadInputTokens)
	out.OutputTokens = add(left.OutputTokens, right.OutputTokens)
	if out.Present {
		m := &out.Modalities
		m.InputPresent = left.Modalities.InputPresent && right.Modalities.InputPresent
		m.OutputPresent = left.Modalities.OutputPresent && right.Modalities.OutputPresent
		if m.InputPresent {
			for i := range m.Input {
				for j := range m.Input[i] {
					m.Input[i][j] = left.Modalities.Input[i][j] + right.Modalities.Input[i][j]
				}
			}
		} else if !left.Modalities.InputPresent {
			m.InputReason = left.Modalities.InputReason
		} else {
			m.InputReason = right.Modalities.InputReason
		}
		if m.OutputPresent {
			for i := range m.Output {
				m.Output[i] = left.Modalities.Output[i] + right.Modalities.Output[i]
			}
		} else if !left.Modalities.OutputPresent {
			m.OutputReason = left.Modalities.OutputReason
		} else {
			m.OutputReason = right.Modalities.OutputReason
		}
	}
	if left.SearchUnitsPresent && right.SearchUnitsPresent {
		if right.SearchUnitsMicros > math.MaxInt64-left.SearchUnitsMicros {
			resultErr = ErrOverflow
		} else {
			out.SearchUnitsPresent = true
			out.SearchUnitsMicros = left.SearchUnitsMicros + right.SearchUnitsMicros
		}
	}
	return Normalize(out), resultErr
}
