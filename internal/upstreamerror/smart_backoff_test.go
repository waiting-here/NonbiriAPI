package upstreamerror

import (
	"github.com/waiting-here/NonbiriAPI/internal/smartbackoff"
	"strings"
	"testing"
)

func TestSmartBackoffOnlyClassifiesCompleteErrorObjects(t *testing.T) {
	ctx := testContext("opaque-secret")
	ctx.CharityCall = true
	got := ctx.Parse([]byte(`{"error":{"code":"private.example.invalid","message":"Daily quota exhausted; reset later. opaque-secret"}}`))
	if got.Backoff().Source != "builtin" || got.Code() != "" {
		t.Fatalf("classification must precede public scrub: %+v", got)
	}
	ctx.CharityCall = false
	if ctx.Parse([]byte(`{"error":{"code":"quota","message":"Daily quota exhausted; reset later."}}`)).Backoff().Valid() {
		t.Fatal("personal error classified")
	}
	ctx.CharityCall = true
	for _, body := range []string{
		`{"message":"Daily quota exhausted; reset later."}`,
		`{"choices":[{"message":{"content":"Daily quota exhausted; reset later."}}]}`,
		`{"error":"Daily quota exhausted; reset later."}`,
		`{"error":{"code":"private.example.invalid","code":"other","message":"quota"}}`,
		`{"error":{"code":"private.example.invalid","message":"quota"}} {}`,
		`{"error":{"code":"private.example.invalid","message":"quota"}`,
		`{"error":{"code":"private.example.invalid","message":"` + strings.Repeat("x", MaxBodyBytes) + `"}}`,
	} {
		if value := ctx.Parse([]byte(body)); value.Backoff().Valid() {
			t.Fatalf("unproven classification for input %.100s", body)
		}
	}
}

func TestEndpointRulesRequireCompleteStructuredCharityError(t *testing.T) {
	ctx := testContext("opaque-secret")
	ctx.CharityCall = true
	ctx.Rules = []smartbackoff.Rule{{Kind: "code", Value: "insufficient_credits", Period: "auto"}}
	body := `{"error":{"code":"insufficient_credits","message":"Insufficient credits","type":"billing_error"}}`
	if got := ctx.Parse([]byte(body)).Backoff(); got.Source != "custom_rule" || got.Period != "unknown" || got.RuleIndex != 1 {
		t.Fatal(got)
	}
	for _, value := range []string{`{"code":"insufficient_credits","message":"Insufficient credits"}`, `{"error":"insufficient_credits"}`, body[:len(body)-1], body + ` {}`, `{"error":{"code":"insufficient_credits","code":"other"}}`, `{"error":{"code":"insufficient_credits","message":"` + strings.Repeat("x", MaxBodyBytes) + `"}}`} {
		if ctx.Parse([]byte(value)).Backoff().Valid() {
			t.Fatal("invalid envelope classified")
		}
	}
	ctx.CharityCall = false
	if ctx.Parse([]byte(body)).Backoff().Valid() {
		t.Fatal("non-charity classified")
	}
}
