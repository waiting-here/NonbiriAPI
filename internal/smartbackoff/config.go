// Package smartbackoff owns bounded quota classification and per-key recovery.
package smartbackoff

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"regexp"
	"strings"
	"unicode"
	"unicode/utf8"
)

var ErrInvalid = errors.New("invalid smart backoff configuration")
var ErrUnavailable = errors.New("smart backoff is waiting for recovery")

type Rule struct {
	Kind   string `json:"kind"`
	Value  string `json:"value"`
	Period string `json:"period"`
}

// UnmarshalJSON preserves omission without accepting null or an empty enum.
// Keep the decoder closed even when this rule is nested in a request field.
func (rule *Rule) UnmarshalJSON(data []byte) error {
	var wire struct {
		Kind   string          `json:"kind"`
		Value  string          `json:"value"`
		Period json.RawMessage `json:"period"`
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&wire); err != nil {
		return err
	}
	period := "auto"
	if len(wire.Period) > 0 {
		if err := json.Unmarshal(wire.Period, &period); err != nil {
			return err
		}
		if bytes.Equal(bytes.TrimSpace(wire.Period), []byte("null")) || period == "" {
			return ErrInvalid
		}
	}
	*rule = Rule{Kind: wire.Kind, Value: wire.Value, Period: period}
	return nil
}

type Config struct {
	Profile string `json:"smart_backoff_profile"`
	Rules   []Rule `json:"smart_backoff_rules"`
}
type Status struct {
	State   string `json:"state"`
	Reason  string `json:"reason"`
	Source  string `json:"source"`
	RetryAt int64  `json:"retry_at"`
}
type Projection struct {
	Config
	Backoff *Status `json:"smart_backoff"`
}
type Patch struct {
	Profile *string
	Rules   *[]Rule
}

var codePattern = regexp.MustCompile(`^[A-Za-z0-9_.:-]{1,64}$`)

func Default() Config { return Config{Profile: "none", Rules: []Rule{}} }
func Supports(profile, period string) bool {
	if profile != "five_hour_week" && profile != "five_hour_week_month" && profile != "day" && profile != "day_month" {
		return false
	}
	switch period {
	case "auto", "unknown":
		return true
	case "five_hour":
		return profile == "five_hour_week" || profile == "five_hour_week_month"
	case "day":
		return profile == "day" || profile == "day_month"
	case "week":
		return profile == "five_hour_week" || profile == "five_hour_week_month"
	case "month":
		return profile == "five_hour_week_month" || profile == "day_month"
	}
	return false
}
func Normalize(value Config) (Config, error) {
	switch value.Profile {
	case "none", "five_hour_week", "five_hour_week_month", "day", "day_month":
	default:
		return Config{}, ErrInvalid
	}
	if len(value.Rules) > 8 {
		return Config{}, ErrInvalid
	}
	rules := make([]Rule, len(value.Rules))
	for i, r := range value.Rules {
		r.Value = strings.TrimSpace(r.Value)
		if r.Period == "" {
			r.Period = "auto"
		}
		switch r.Period {
		case "auto", "five_hour", "day", "week", "month":
		default:
			return Config{}, ErrInvalid
		}
		if value.Profile != "none" && !Supports(value.Profile, r.Period) {
			return Config{}, ErrInvalid
		}
		switch r.Kind {
		case "code":
			if !codePattern.MatchString(r.Value) {
				return Config{}, ErrInvalid
			}
		case "message":
			if !utf8.ValidString(r.Value) || utf8.RuneCountInString(r.Value) < 1 || utf8.RuneCountInString(r.Value) > 160 {
				return Config{}, ErrInvalid
			}
			for _, ch := range r.Value {
				if unicode.IsControl(ch) || unicode.Is(unicode.Cf, ch) {
					return Config{}, ErrInvalid
				}
			}
		default:
			return Config{}, ErrInvalid
		}
		rules[i] = r
	}
	value.Rules = rules
	return value, nil
}
func Merge(current Config, patch Patch) (Config, error) {
	if patch.Profile != nil {
		current.Profile = *patch.Profile
	}
	if patch.Rules != nil {
		current.Rules = *patch.Rules
	}
	return Normalize(current)
}
func RulesJSON(rules []Rule) string {
	if rules == nil {
		rules = []Rule{}
	}
	b, _ := json.Marshal(rules)
	return string(b)
}

type contextKey struct{}

func WithConfig(ctx context.Context, config Config) context.Context {
	return context.WithValue(ctx, contextKey{}, config)
}
func FromContext(ctx context.Context) Config {
	if value, ok := ctx.Value(contextKey{}).(Config); ok {
		return value
	}
	return Default()
}
