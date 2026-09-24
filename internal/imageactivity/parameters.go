package imageactivity

import (
	"bytes"
	"encoding/json"
	"math"
	"math/big"
	"strconv"
	"strings"
	"unicode/utf16"
	"unicode/utf8"

	"github.com/waiting-here/NonbiriAPI/internal/db"
	"github.com/waiting-here/NonbiriAPI/internal/ledger"
)

func scalar(raw json.RawMessage) (any, error) {
	if len(raw) == 0 || len(raw) > 65536 || !json.Valid(raw) {
		return nil, ErrInvalid
	}
	var v any
	d := json.NewDecoder(bytes.NewReader(raw))
	d.UseNumber()
	if d.Decode(&v) != nil {
		return nil, ErrInvalid
	}
	switch n := v.(type) {
	case nil:
		return nil, nil
	case string:
		if !utf8.ValidString(n) {
			return nil, ErrInvalid
		}
		return n, nil
	case json.Number:
		f, err := n.Float64()
		if err != nil || !finite(f) {
			return nil, ErrInvalid
		}
		return f, nil
	default:
		return nil, ErrInvalid
	}
}
func textLength(value, unit string) int {
	switch unit {
	case "utf8_bytes":
		return len(value)
	case "unicode_scalars":
		return utf8.RuneCountInString(value)
	case "utf16_units":
		n := 0
		for _, r := range value {
			n += utf16.RuneLen(r)
		}
		return n
	}
	return -1
}
func ruleValue(rule ParameterRule, value any, enum bool) error {
	switch rule.Type {
	case "string":
		text, ok := value.(string)
		if !ok || !utf8.ValidString(text) {
			return ErrInvalid
		}
		length := textLength(text, rule.LengthUnit)
		if length < 0 || rule.MinLength != nil && length < *rule.MinLength || rule.MaxLength != nil && length > *rule.MaxLength {
			return ErrInvalid
		}
		if rule.Key != Prompt && rule.Key != NegativePrompt && len(text) > 512 {
			return ErrInvalid
		}
		if rule.Dimensions != nil && rule.Dimensions.checkValue(text) != nil {
			return ErrInvalid
		}
	case "integer", "number":
		n, ok := value.(float64)
		if !ok || !finite(n) || rule.Type == "integer" && !safeInteger(n) {
			return ErrInvalid
		}
		if rule.Minimum != nil && n < *rule.Minimum || rule.Maximum != nil && n > *rule.Maximum {
			return ErrInvalid
		}
		if rule.Key == N && (!safeInteger(n) || n < 1 || n > 16) {
			return ErrInvalid
		}
		if rule.Step != nil {
			base := float64(0)
			if rule.Minimum != nil {
				base = *rule.Minimum
			}
			rat := func(f float64) *big.Rat {
				v, _ := new(big.Rat).SetString(strconv.FormatFloat(f, 'f', -1, 64))
				return v
			}
			d := new(big.Rat).Sub(rat(n), rat(base))
			d.Quo(d, rat(*rule.Step))
			if !d.IsInt() {
				return ErrInvalid
			}
		}
	default:
		return ErrInvalid
	}
	if enum && len(rule.Enum) > 0 {
		found := false
		for _, raw := range rule.Enum {
			v, err := scalar(raw)
			if err != nil {
				return err
			}
			if scalarEqual(value, v) {
				found = true
				break
			}
		}
		if !found {
			return ErrInvalid
		}
	}
	return nil
}
func scalarEqual(a, b any) bool {
	switch x := a.(type) {
	case nil:
		return b == nil
	case string:
		y, ok := b.(string)
		return ok && x == y
	case float64:
		y, ok := b.(float64)
		return ok && x == y
	}
	return false
}
func validateParameters(rules []ParameterRule, combinations []CombinationRule) error {
	if len(rules) == 0 || len(rules) > 10 || len(combinations) > 64 {
		return ErrInvalid
	}
	byKey := map[ParameterKey]ParameterRule{}
	for _, r := range rules {
		if !keyValid(r.Key) {
			return ErrInvalid
		}
		if _, ok := byKey[r.Key]; ok {
			return ErrInvalid
		}
		if r.Type != "string" && r.Type != "integer" && r.Type != "number" {
			return ErrInvalid
		}
		if r.Key == Prompt || r.Key == NegativePrompt {
			if r.Type != "string" || len(r.Default) != 0 || len(r.Enum) != 0 {
				return ErrInvalid
			}
		}
		if r.Key == N && r.Type != "integer" {
			return ErrInvalid
		}
		if r.Key == N {
			for _, bound := range []*float64{r.Minimum, r.Maximum} {
				if bound != nil && (*bound < 1 || *bound > 16) {
					return ErrInvalid
				}
			}
		}
		if r.Required && !r.Supported {
			return ErrInvalid
		}
		if r.Dimensions != nil && (r.Key != Size || r.Type != "string" || !r.Supported || r.Dimensions.validate() != nil) {
			return ErrInvalid
		}
		if r.Type == "string" {
			if r.Minimum != nil || r.Maximum != nil || r.Step != nil {
				return ErrInvalid
			}
			if r.LengthUnit != "utf8_bytes" && r.LengthUnit != "unicode_scalars" && r.LengthUnit != "utf16_units" {
				return ErrInvalid
			}
			for _, n := range []*int{r.MinLength, r.MaxLength} {
				if n != nil && (*n < 0 || *n > maxPrompt) {
					return ErrInvalid
				}
			}
			if r.MinLength != nil && r.MaxLength != nil && *r.MinLength > *r.MaxLength {
				return ErrInvalid
			}
		} else {
			if r.MinLength != nil || r.MaxLength != nil || r.LengthUnit != "" {
				return ErrInvalid
			}
			for _, n := range []*float64{r.Minimum, r.Maximum, r.Step} {
				if n != nil && (!finite(*n) || r.Type == "integer" && !safeInteger(*n)) {
					return ErrInvalid
				}
			}
			if r.Minimum != nil && r.Maximum != nil && *r.Minimum > *r.Maximum || r.Step != nil && *r.Step <= 0 {
				return ErrInvalid
			}
		}
		if len(r.Enum) > 128 {
			return ErrInvalid
		}
		values := []any{}
		for _, raw := range r.Enum {
			v, err := scalar(raw)
			if err != nil || v == nil || ruleValue(r, v, false) != nil {
				return ErrInvalid
			}
			for _, old := range values {
				if scalarEqual(old, v) {
					return ErrInvalid
				}
			}
			values = append(values, v)
		}
		if len(r.Default) > 0 {
			v, err := scalar(r.Default)
			if err != nil || v == nil || !r.Supported || ruleValue(r, v, true) != nil {
				return ErrInvalid
			}
		}
		byKey[r.Key] = r
	}
	prompt, ok := byKey[Prompt]
	if !ok || !prompt.Supported || !prompt.Required {
		return ErrInvalid
	}
	count, ok := byKey[N]
	if !ok || !count.Supported {
		return ErrInvalid
	}
	for _, c := range combinations {
		if len(c.Keys) == 0 || len(c.Keys) > 8 || len(c.Allowed) == 0 || len(c.Allowed) > 128 {
			return ErrInvalid
		}
		seen := map[ParameterKey]bool{}
		for _, key := range c.Keys {
			r, ok := byKey[key]
			if !ok || !r.Supported || key == Prompt || key == NegativePrompt || seen[key] {
				return ErrInvalid
			}
			seen[key] = true
		}
		for _, tuple := range c.Allowed {
			if len(tuple) != len(c.Keys) {
				return ErrInvalid
			}
			for i, raw := range tuple {
				v, err := scalar(raw)
				if err != nil {
					return err
				}
				rule := byKey[c.Keys[i]]
				if v == nil {
					if rule.Required || len(rule.Default) > 0 || rule.Key == N {
						return ErrInvalid
					}
				} else if err = ruleValue(rule, v, true); err != nil {
					return err
				}
			}
		}
	}
	encoded, _ := json.Marshal(rules)
	if len(encoded) > 65536 {
		return ErrInvalid
	}
	encoded, _ = json.Marshal(combinations)
	if len(encoded) > 65536 {
		return ErrInvalid
	}
	return nil
}
func parsePayment(p Price, n int) (ledger.SketchPayment, error) {
	var payment ledger.SketchPayment
	if n < 1 || n > 16 {
		return payment, ErrInvalid
	}
	amounts := []*ledger.Amount{&payment.Paper, &payment.Brush}
	for i, raw := range []string{p.Paper, p.Brush} {
		value, err := db.ParseU128Decimal(raw)
		if err != nil || value.Decimal() != raw {
			return payment, ErrInvalid
		}
		a, err := ledger.AmountFromBig(new(big.Int).Mul(value.Big(), big.NewInt(int64(1000*n))))
		if err != nil {
			return payment, ErrInvalid
		}
		*amounts[i] = a
	}
	if payment.Paper.Sign() == 0 && payment.Brush.Sign() == 0 {
		return payment, ErrInvalid
	}
	return payment, nil
}
func normalizeModel(input ModelInput) (ModelInput, error) {
	input.Description = strings.ReplaceAll(input.Description, string([]byte{13, 10}), string([]byte{10}))
	if !safeText(input.DisplayName, 512) || utf8.RuneCountInString(input.DisplayName) < 1 || utf8.RuneCountInString(input.DisplayName) > 128 || len(input.Description) > 4096 || !utf8.ValidString(input.Description) {
		return input, ErrInvalid
	}
	if _, err := decimalRevision(input.ExpectedRevision, true); err != nil {
		return input, err
	}
	if _, err := parsePayment(input.Price, 1); err != nil {
		return input, err
	}
	if err := validateParameters(input.Parameters, input.Combinations); err != nil {
		return input, err
	}
	if err := validateMapping(input.Mapping, true); err != nil {
		return input, err
	}
	return input, nil
}
func normalizeSubmit(input SubmitInput, rules []ParameterRule, combinations []CombinationRule) (map[ParameterKey]any, int, error) {
	if !db.ValidateOpaqueID(input.ModelID, "imdl_") {
		return nil, 0, ErrInvalid
	}
	if _, err := decimalRevision(input.ExpectedModelRevision, false); err != nil {
		return nil, 0, err
	}
	if err := validateParameters(rules, combinations); err != nil {
		return nil, 0, err
	}
	input.Prompt = strings.ReplaceAll(input.Prompt, string([]byte{13, 10}), string([]byte{10}))
	params := map[ParameterKey]any{Prompt: input.Prompt}
	for key, raw := range map[ParameterKey]json.RawMessage{
		NegativePrompt: input.NegativePrompt, Size: input.Size, AspectRatio: input.AspectRatio,
		Resolution: input.Resolution, Seed: input.Seed, Steps: input.Steps, Guidance: input.Guidance, Quality: input.Quality,
	} {
		if len(raw) > 0 {
			v, err := scalar(raw)
			if err != nil || v == nil {
				return nil, 0, ErrInvalid
			}
			if t, ok := v.(string); ok {
				v = strings.ReplaceAll(t, string([]byte{13, 10}), string([]byte{10}))
			}
			params[key] = v
		}
	}
	if input.N != nil {
		params[N] = float64(*input.N)
	}
	supported := map[ParameterKey]bool{}
	for _, r := range rules {
		supported[r.Key] = r.Supported
		value, present := params[r.Key]
		if !present && len(r.Default) > 0 {
			v, err := scalar(r.Default)
			if err != nil {
				return nil, 0, err
			}
			value, present = v, true
			params[r.Key] = v
		}
		if !present && r.Key == N {
			value, present = float64(1), true
			params[N] = value
		}
		if r.Required && !present || present && !r.Supported {
			return nil, 0, ErrInvalid
		}
		if present {
			if err := ruleValue(r, value, true); err != nil {
				return nil, 0, err
			}
		}
	}
	for key := range params {
		if !supported[key] {
			return nil, 0, ErrInvalid
		}
	}
	negative := ""
	if n, ok := params[NegativePrompt]; ok {
		var valid bool
		negative, valid = n.(string)
		if !valid {
			return nil, 0, ErrInvalid
		}
	}
	if !utf8.ValidString(input.Prompt) || len(input.Prompt)+len(negative) > maxPrompt {
		return nil, 0, ErrInvalid
	}
	for _, c := range combinations {
		matched := false
		for _, tuple := range c.Allowed {
			equal := true
			for i, key := range c.Keys {
				want, err := scalar(tuple[i])
				if err != nil {
					return nil, 0, err
				}
				if !scalarEqual(params[key], want) {
					equal = false
					break
				}
			}
			if equal {
				matched = true
				break
			}
		}
		if !matched {
			return nil, 0, ErrInvalid
		}
	}
	nf, ok := params[N].(float64)
	if !ok || math.Trunc(nf) != nf || nf < 1 || nf > 16 {
		return nil, 0, ErrInvalid
	}
	return params, int(nf), nil
}
