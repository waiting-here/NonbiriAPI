package smartbackoff

import (
	"bytes"
	"context"
	"encoding/json"
	"regexp"
	"strings"
	"unicode"
	"unicode/utf8"
)

type Rule struct {
	Kind   string `json:"kind"`
	Value  string `json:"value"`
	Period string `json:"period"`
}

func (r *Rule) UnmarshalJSON(data []byte) error {
	var value struct {
		Kind   string          `json:"kind"`
		Value  string          `json:"value"`
		Period json.RawMessage `json:"period"`
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&value); err != nil {
		return err
	}
	period := "auto"
	if len(value.Period) != 0 {
		if bytes.Equal(bytes.TrimSpace(value.Period), []byte("null")) || json.Unmarshal(value.Period, &period) != nil || period == "" {
			return ErrInvalid
		}
	}
	*r = Rule{Kind: value.Kind, Value: value.Value, Period: period}
	return nil
}

var machineRule = regexp.MustCompile(`^[A-Za-z0-9_.:-]{1,64}$`)

func NormalizeRules(input []Rule) ([]Rule, error) {
	if input == nil || len(input) > 8 {
		return nil, ErrInvalid
	}
	out := make([]Rule, len(input))
	for i, rule := range input {
		if rule.Period == "" {
			rule.Period = "auto"
		}
		switch rule.Period {
		case "auto", "five_hour", "day", "week", "month":
		default:
			return nil, ErrInvalid
		}
		switch rule.Kind {
		case "code":
			if !machineRule.MatchString(rule.Value) {
				return nil, ErrInvalid
			}
		case "message":
			rule.Value = strings.TrimSpace(rule.Value)
			if !utf8.ValidString(rule.Value) || utf8.RuneCountInString(rule.Value) < 1 || utf8.RuneCountInString(rule.Value) > 160 {
				return nil, ErrInvalid
			}
			for _, r := range rule.Value {
				if unicode.IsControl(r) || unicode.Is(unicode.Cf, r) {
					return nil, ErrInvalid
				}
			}
		default:
			return nil, ErrInvalid
		}
		out[i] = rule
	}
	return out, nil
}

type rulesKey struct{}

// WithRules freezes the rules selected by the dispatch transaction.
func WithRules(ctx context.Context, rules []Rule) context.Context {
	return context.WithValue(ctx, rulesKey{}, append([]Rule(nil), rules...))
}

func RulesFromContext(ctx context.Context) []Rule {
	rules, _ := ctx.Value(rulesKey{}).([]Rule)
	return append([]Rule(nil), rules...)
}
