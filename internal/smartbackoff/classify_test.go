package smartbackoff

import (
	"encoding/json"
	"strings"
	"testing"
)

func errorFields(t *testing.T, code, message string) map[string]json.RawMessage {
	t.Helper()
	raw, _ := json.Marshal(map[string]string{"code": code, "message": message})
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(raw, &fields); err != nil {
		t.Fatal(err)
	}
	return fields
}
func TestClassificationRequiresBoundedQuotaEvidence(t *testing.T) {
	config := Config{Profile: "five_hour_week_month"}
	for _, tc := range []struct{ name, code, message, period string }{
		{"numeric quota", "1308", "Usage quota exhausted; retry after reset.", "unknown"},
		{"five hours", "quota_exceeded", "5-hour usage limit exceeded. Please wait for reset.", "five_hour"},
		{"weekly", "permission_error", "Weekly quota reached, try again after reset.", "week"},
		{"Chinese", "1317", "本周额度已耗尽，请等待下次重置。", "week"},
		{"monthly", "custom", "Monthly quota exhausted; reset next month.", "month"},
		{"five hour with billing explanation", "1318", "5-hour quota exhausted; monthly spending limit reached.", "five_hour"},
		{"weekly with balance explanation", "1319", "7-day quota exhausted; account balance insufficient.", "week"},
		{"official Chinese usage", "1308", "已达到5小时使用上限，请等待重置", "five_hour"},
		{"explicit unknown reset", "other", "Usage quota exhausted; resets later.", "unknown"},
		{"vague retry", "other", "Usage quota exhausted; try again later.", ""},
		{"tpm reset", "other", "Usage quota exhausted; TPM resets later.", ""},
		{"spending reset", "other", "Monthly spending quota exhausted; resets later.", ""},
		{"mixed windows", "1310", "Weekly/monthly usage quota exhausted; resets later.", "unknown"},
		{"shared code tpm", "1308", "Tokens per minute quota exceeded; try again after reset", ""},
		{"shared code context", "1316", "Context length quota exceeded", ""},
		{"shared code permissions", "1316", "5-hour quota exhausted; permission denied", ""},
		{"wrong documented window", "1316", "Weekly quota exhausted; reset later", ""},
		{"generic", "insufficient_quota", "Quota exhausted.", ""},
		{"tpm", "Throttling.AllocationQuota", "Allocated quota exceeded for tokens per minute.", ""},
		{"voice", "Throttling.AllocationQuota", "Maximum voice quota reached.", ""},
		{"payment", "1113", "Weekly quota exhausted because account balance is insufficient.", ""},
		{"expired", "1309", "Monthly quota expired, please renew payment.", ""},
		{"permission", "1311", "Weekly quota exceeded; model permission denied.", ""},
		{"overload", "overloaded_error", "Overloaded; try again later.", ""},
		{"context", "context_length_exceeded", "Context length limit exceeded.", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := Classify(config, errorFields(t, tc.code, tc.message))
			if got.Period != tc.period || (tc.period != "" && got.Source != "builtin") {
				t.Fatalf("got %+v", got)
			}
		})
	}
	if got := Classify(Config{}, errorFields(t, "1308", "Usage quota exhausted.")); got.Valid() {
		t.Fatal("zero config classified")
	}
	if got := Classify(Config{Profile: "day"}, errorFields(t, "1316", "Weekly quota exhausted.")); got.Valid() {
		t.Fatal("inapplicable window classified")
	}
}
func TestCustomRulesPrecedencePeriodAndResetTime(t *testing.T) {
	config := Config{Profile: "five_hour_week_month", Rules: []Rule{{Kind: "code", Value: "custom.quota", Period: "week"}, {Kind: "message", Value: "again", Period: "month"}}}
	got := Classify(config, errorFields(t, "custom.quota", "Try again"))
	if got.Period != "week" || got.Source != "custom_rule" {
		t.Fatal(got)
	}
	fields := errorFields(t, "custom.quota", "5-hour quota exhausted; reset at 2030-04-05T12:00:00+08:00")
	got = Classify(config, fields)
	if got.Period != "five_hour" || got.Source != "builtin" || got.ResetAt != 1901592000 {
		t.Fatal(got)
	}
	config.Rules = []Rule{{Kind: "code", Value: "permission_error", Period: "auto"}}
	fields = errorFields(t, "other", "Quota unavailable")
	fields["type"] = json.RawMessage(`"permission_error"`)
	got = Classify(config, fields)
	if got.Period != "unknown" || got.Source != "custom_rule" {
		t.Fatal(got)
	}
	fields["type"] = json.RawMessage(`"PERMISSION_ERROR"`)
	if Classify(config, fields).Valid() {
		t.Fatal("machine match lost case sensitivity")
	}
	fields = errorFields(t, "permission_error", "reset at 2030-04-05 12:00:00")
	if Classify(config, fields).ResetAt != 0 {
		t.Fatal("guessed reset timezone")
	}
	otherProvider := Config{Profile: "day", Rules: []Rule{{Kind: "code", Value: "1316", Period: "auto"}}}
	if got := Classify(otherProvider, errorFields(t, "1316", "Daily quota exhausted; reset later")); got.Period != "day" || got.Source != "custom_rule" {
		t.Fatal("builtin period contaminated custom rule", got)
	}
	// An explicit rule can cover provider-specific meanings of otherwise permanent codes.
	config.Rules = []Rule{{Kind: "code", Value: "1113", Period: "week"}}
	if Classify(config, errorFields(t, "1113", "Unavailable")).Source != "custom_rule" {
		t.Fatal("explicit rule ignored")
	}
}
func TestRuleNormalizationUsesUnicodeCharactersAndClosedPeriods(t *testing.T) {
	for _, tc := range []struct {
		name   string
		config Config
		valid  bool
	}{
		{"Chinese160", Config{Profile: "day", Rules: []Rule{{Kind: "message", Value: strings.Repeat("额", 160)}}}, true},
		{"Chinese161", Config{Profile: "day", Rules: []Rule{{Kind: "message", Value: strings.Repeat("额", 161)}}}, false},
		{"controls", Config{Profile: "day", Rules: []Rule{{Kind: "message", Value: "a\nb"}}}, false},
		{"code", Config{Profile: "day", Rules: []Rule{{Kind: "code", Value: "  X.Quota:7-1  "}}}, true},
		{"nonascii code", Config{Profile: "day", Rules: []Rule{{Kind: "code", Value: "额度"}}}, false},
		{"period mismatch", Config{Profile: "day", Rules: []Rule{{Kind: "code", Value: "quota", Period: "week"}}}, false},
		{"dormant", Config{Profile: "none", Rules: []Rule{{Kind: "code", Value: "quota", Period: "week"}}}, true},
		{"too many", Config{Profile: "day", Rules: make([]Rule, 9)}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := Normalize(tc.config)
			if (err == nil) != tc.valid {
				t.Fatal(got, err)
			}
			if err == nil && got.Rules[0].Period == "" {
				t.Fatal("missing auto")
			}
		})
	}
}

func TestResetTimeMustBelongToTheResetHint(t *testing.T) {
	config := Config{Profile: "day"}
	got := Classify(config, errorFields(t, "quota", "Request at 2030-04-04T01:00:00Z. Daily quota exhausted; resets at 2030-04-05T04:00:00Z"))
	if got.ResetAt != 1901592000 {
		t.Fatal("request timestamp was used", got)
	}
	got = Classify(config, errorFields(t, "quota", "Daily quota exhausted; resets at 2030-04-05T04:00:00Z or resets at 2030-04-06T04:00:00Z"))
	if got.ResetAt != 0 {
		t.Fatal("ambiguous reset was guessed", got)
	}
}

func TestRuleJSONPeriodOmissionAndClosedFields(t *testing.T) {
	for _, tc := range []struct {
		body  string
		valid bool
	}{
		{`{"kind":"code","value":"quota"}`, true},
		{`{"kind":"code","value":"quota","period":"auto"}`, true},
		{`{"kind":"code","value":"quota","period":null}`, false},
		{`{"kind":"code","value":"quota","period":""}`, false},
		{`{"kind":"code","value":"quota","period":1}`, false},
		{`{"kind":"code","value":"quota","unexpected":true}`, false},
	} {
		var rule Rule
		err := json.Unmarshal([]byte(tc.body), &rule)
		if (err == nil) != tc.valid {
			t.Fatalf("%s got=%+v err=%v", tc.body, rule, err)
		}
		if err == nil && rule.Period != "auto" {
			t.Fatal("missing explicit default", rule)
		}
	}
}
