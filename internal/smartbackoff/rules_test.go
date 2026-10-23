package smartbackoff

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestEndpointRulesNormalizationAndExactMatching(t *testing.T) {
	rules, err := NormalizeRules([]Rule{{Kind: "code", Value: "insufficient_credits"}, {Kind: "message", Value: "  try tomorrow  ", Period: "day"}})
	if err != nil || rules[0].Period != "auto" || rules[1].Value != "try tomorrow" {
		t.Fatal(rules, err)
	}
	for _, tc := range []struct {
		code, message, period, source string
		index                         int
	}{
		{"insufficient_credits", "Insufficient credits", "unknown", "custom_rule", 1},
		{"insufficient_credits", "Monthly credits depleted", "month", "custom_rule", 1},
		{"other", "Please try tomorrow.", "day", "custom_rule", 2},
		{"INSUFFICIENT_CREDITS", "Insufficient credits", "", "", 0},
		{"other", "Please Try tomorrow.", "", "", 0},
		{"insufficient_credits", "Weekly quota exhausted; reset later.", "week", "builtin", 0},
	} {
		got := Classify(errorFields(t, tc.code, tc.message), rules...)
		if got.Period != tc.period || got.Source != tc.source || got.RuleIndex != tc.index {
			t.Fatal(tc, got)
		}
	}
	fields := errorFields(t, "outer", "Insufficient credits")
	fields["type"] = json.RawMessage(`"insufficient_credits"`)
	if got := Classify(fields, rules...); got.Source != "custom_rule" {
		t.Fatal(got)
	}
	for _, input := range [][]Rule{nil, make([]Rule, 9), {{Kind: "code", Value: "bad code"}}, {{Kind: "code", Value: strings.Repeat("a", 65)}}, {{Kind: "message", Value: "\t"}}, {{Kind: "message", Value: "text\x00"}}, {{Kind: "message", Value: "a\u200b"}}, {{Kind: "message", Value: strings.Repeat("文", 161)}}, {{Kind: "code", Value: "ok", Period: "unknown"}}} {
		if _, err := NormalizeRules(input); err == nil {
			t.Fatalf("invalid rules accepted: %+v", input)
		}
	}
	for _, raw := range []string{`{"kind":"code","value":"x","period":null}`, `{"kind":"code","value":"x","period":""}`, `{"kind":"code","value":"x","extra":true}`} {
		var rule Rule
		if json.Unmarshal([]byte(raw), &rule) == nil {
			t.Fatal("invalid rule JSON accepted", raw)
		}
	}
	ctx := WithRules(t.Context(), rules)
	rules[0].Value = "changed"
	copy := RulesFromContext(ctx)
	copy[0].Value = "changed again"
	if RulesFromContext(ctx)[0].Value != "insufficient_credits" {
		t.Fatal("dispatch rules are mutable")
	}
}

func TestEndpointRulesMaximumEscapedStorage(t *testing.T) {
	rules := make([]Rule, 8)
	for i := range rules {
		rules[i] = Rule{Kind: "message", Value: strings.Repeat("<", 160), Period: "five_hour"}
	}
	normalized, err := NormalizeRules(rules)
	if err != nil {
		t.Fatal(err)
	}
	encoded, err := json.Marshal(normalized)
	if err != nil || len(encoded) > 8192 {
		t.Fatal(len(encoded), err)
	}
	t.Logf("maximum escaped rules bytes=%d", len(encoded))
}
