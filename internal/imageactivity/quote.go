package imageactivity

import (
	"context"
	"database/sql"
	"encoding/json"
	"strconv"

	"github.com/waiting-here/NonbiriAPI/internal/limitedactivities"
)

// linkedSubmission completes the declared size tuple before ordinary rule
// defaults and combination validation. Auto suppresses ratio/resolution
// defaults, which must not turn it into an invented numeric size.
func linkedSubmission(input SubmitInput, model modelSnapshot) (SubmitInput, []ParameterRule, error) {
	rules := append([]ParameterRule(nil), model.input.Parameters...)
	if model.size == nil {
		return input, rules, nil
	}
	fields := map[ParameterKey]*json.RawMessage{
		Size: &input.Size, AspectRatio: &input.AspectRatio, Resolution: &input.Resolution,
	}
	values := map[ParameterKey]string{}
	for key, raw := range fields {
		if len(*raw) == 0 {
			continue
		}
		value, err := scalar(*raw)
		text, ok := value.(string)
		if err != nil || !ok {
			return input, nil, ErrInvalid
		}
		values[key] = text
	}
	if values[Size] != "auto" {
		for _, rule := range rules {
			if _, present := values[rule.Key]; present || len(rule.Default) == 0 {
				continue
			}
			if rule.Key == Size && (values[AspectRatio] != "" || values[Resolution] != "") ||
				model.size.Mode == WidthHeight && (rule.Key == AspectRatio || rule.Key == Resolution) {
				continue
			}
			if _, linked := fields[rule.Key]; linked {
				value, err := scalar(rule.Default)
				if err != nil {
					return input, nil, err
				}
				if text, ok := value.(string); ok {
					values[rule.Key] = text
				}
			}
		}
	}
	size := SizeInput{Ratio: values[AspectRatio], Resolution: values[Resolution], Size: values[Size]}
	if size.Size == "auto" {
		if size.Ratio != "" || size.Resolution != "" {
			return input, nil, ErrInvalid
		}
		size.Size, size.Auto = "", true
	}
	resolved, err := model.size.ResolveSize(size)
	if err != nil {
		return input, nil, err
	}
	for key, raw := range fields {
		if value, present := resolved.Values[key]; present {
			*raw, _ = json.Marshal(value)
		} else {
			*raw = nil
			for index := range rules {
				if rules[index].Key == key {
					rules[index].Default = nil
				}
			}
		}
	}
	return input, rules, nil
}

type QuoteResult struct {
	ModelRevision      string       `json:"model_revision"`
	PricingRevision    string       `json:"pricing_revision"`
	EffectiveSelection ResolvedSize `json:"effective_selection"`
	Unit               Price        `json:"unit"`
	Total              Price        `json:"total"`
	Basis              string       `json:"basis"`
	PriceKey           string       `json:"price_key"`
}

func selectedPrice(model modelSnapshot, params map[ParameterKey]any, n int) (ResolvedSize, PriceQuote, error) {
	selection := ResolvedSize{Values: map[ParameterKey]any{}}
	if model.size != nil {
		var input SizeInput
		for _, item := range []struct {
			key ParameterKey
			set func(string)
		}{
			{AspectRatio, func(v string) { input.Ratio = v }},
			{Resolution, func(v string) { input.Resolution = v }},
			{Size, func(v string) { input.Size = v }},
		} {
			if value, ok := params[item.key]; ok {
				text, valid := value.(string)
				if !valid {
					return selection, PriceQuote{}, ErrInvalid
				}
				item.set(text)
			}
		}
		if input.Size == "auto" {
			input.Size, input.Auto = "", true
		}
		resolved, err := model.size.ResolveSize(input)
		if err != nil {
			return selection, PriceQuote{}, err
		}
		for _, key := range []ParameterKey{Size, AspectRatio, Resolution} {
			expected, hasExpected := resolved.Values[key]
			actual, hasActual := params[key]
			if hasActual && (!hasExpected || !scalarEqual(actual, expected)) {
				return selection, PriceQuote{}, ErrInvalid
			}
			if hasExpected {
				params[key] = expected
			}
		}
		selection = resolved
	}
	price, err := QuotePricing(model.pricing, selection.Selection, n)
	return selection, price, err
}

// Quote only reads the current policy. It never reserves currency, creates a
// task, records a prompt, or invokes an upstream generation endpoint.
func (s *Service) Quote(ctx context.Context, user int64, input SubmitInput) (QuoteResult, error) {
	var out QuoteResult
	now, err := s.now()
	if err != nil {
		return out, err
	}
	tx, err := s.config.Database.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return out, err
	}
	defer tx.Rollback()
	if err = s.authorizeUserTx(ctx, tx, user, now); err != nil {
		return out, err
	}
	if err = s.config.Gate.AuthorizeUserActivity(ctx, tx, user); err != nil {
		return out, err
	}
	if _, err = s.config.Admission(ctx, tx, user, limitedactivities.PictureBook, now); err != nil {
		return out, err
	}
	upstream, err := currentUpstreamTx(ctx, tx)
	if err != nil {
		return out, err
	}
	model, err := modelTx(ctx, tx, input.ModelID, 0)
	if err != nil {
		return out, err
	}
	if model.controlID != upstream.controlID || !model.input.Enabled || model.readiness == "pending" {
		return out, ErrUnavailable
	}
	if isFixedAdapter(upstream) {
		if err = validateFixedNumbers(input, model.input.Parameters); err != nil {
			return out, err
		}
	}
	input.ExpectedModelRevision = strconv.FormatInt(model.revision, 10)
	input, rules, err := linkedSubmission(normalizeSubmitText(input), model)
	if err != nil {
		return out, err
	}
	params, n, err := normalizeSubmit(input, rules, model.input.Combinations)
	if err != nil {
		return out, err
	}
	selection, price, err := selectedPrice(model, params, n)
	if err != nil {
		return out, err
	}
	out = QuoteResult{
		ModelRevision:      strconv.FormatInt(model.revision, 10),
		PricingRevision:    strconv.FormatInt(model.pricingRevision, 10),
		EffectiveSelection: selection,
		Unit:               price.Unit,
		Total:              price.Total,
		Basis:              price.Basis,
		PriceKey:           price.PriceKey,
	}
	return out, tx.Commit()
}
