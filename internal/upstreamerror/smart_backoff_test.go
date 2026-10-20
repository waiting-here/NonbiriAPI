package upstreamerror

import (
	"strings"
	"testing"

	"github.com/waiting-here/NonbiriAPI/internal/smartbackoff"
)

func TestSmartBackoffOnlyClassifiesCompleteErrorObjects(t *testing.T) {
	ctx := testContext("opaque-secret")
	ctx.Backoff = smartbackoff.Config{Profile: "day", Rules: []smartbackoff.Rule{{Kind: "code", Value: "private.example.invalid", Period: "auto"}}}
	got := ctx.Parse([]byte(`{"error":{"code":"private.example.invalid","message":"opaque-secret"}}`))
	if got.Backoff().Source != "custom_rule" || got.Code() != "" {
		t.Fatalf("classification must precede public scrub: %+v", got)
	}
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
