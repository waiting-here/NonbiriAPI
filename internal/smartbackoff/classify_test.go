package smartbackoff

import (
	"encoding/json"
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
	for _, tc := range []struct{ name, code, message, period string }{
		{"numeric quota", "1308", "Usage quota exhausted; retry after reset.", "unknown"},
		{"five hours", "quota_exceeded", "5-hour usage limit exceeded. Please wait for reset.", "five_hour"},
		{"weekly", "permission_error", "Weekly quota reached, try again after reset.", "week"},
		{"Chinese", "1317", "本周额度已耗尽，请等待下次重置。", "week"},
		{"daily", "quota", "Daily quota exhausted; reset tomorrow.", "day"},
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
			got := Classify(errorFields(t, tc.code, tc.message))
			if got.Period != tc.period || (tc.period != "" && got.Source != "builtin") {
				t.Fatalf("got %+v", got)
			}
		})
	}
}
func TestResetTimeMustBelongToTheResetHint(t *testing.T) {
	got := Classify(errorFields(t, "quota", "Request at 2030-04-04T01:00:00Z. Daily quota exhausted; resets at 2030-04-05T04:00:00Z"))
	if got.ResetAt != 1901592000 {
		t.Fatal("request timestamp was used", got)
	}
	got = Classify(errorFields(t, "quota", "Daily quota exhausted; resets at 2030-04-05T04:00:00Z or resets at 2030-04-06T04:00:00Z"))
	if got.ResetAt != 0 {
		t.Fatal("ambiguous reset was guessed", got)
	}
}

func TestClassificationResetEvidenceAndContext(t *testing.T) {
	fields := errorFields(t, "custom.quota", "5-hour quota exhausted; reset at 2030-04-05T12:00:00+08:00")
	got := Classify(fields)
	if got.Period != "five_hour" || got.Source != "builtin" || got.ResetAt != 1901592000 {
		t.Fatal(got)
	}
	fields = errorFields(t, "quota", "Daily quota exhausted; reset later")
	fields["reset_at"] = json.RawMessage(`"2030-04-05T04:00:00Z"`)
	fields["reset_time"] = json.RawMessage(`"2030-04-06T04:00:00Z"`)
	if Classify(fields).ResetAt != 0 {
		t.Fatal("conflicting resets were guessed")
	}
	if Classify(nil).Valid() || IsCharityCall(t.Context()) || !IsCharityCall(WithCharityCall(t.Context(), true)) || IsCharityCall(WithCharityCall(WithCharityCall(t.Context(), true), false)) {
		t.Fatal("classification must be explicitly scoped to charity")
	}
}
