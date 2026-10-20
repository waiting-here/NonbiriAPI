package upstreamerror

import (
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
