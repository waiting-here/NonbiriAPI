package upstreamerror

import (
	"strings"
	"testing"
)

const googleOverloadJSON = `{"error":{"code":503,"message":"This model is currently experiencing high demand. Spikes in demand are usually temporary. Please try again later.","status":"UNAVAILABLE"}}`

func TestGoogleOverloadRequiresCompleteScopedEvidence(t *testing.T) {
	for _, tc := range []struct {
		name, base, body       string
		google, charity, match bool
	}{
		{"native", "https://relay.example/v1beta", googleOverloadJSON, true, true, true},
		{"official compatible", "https://generativelanguage.googleapis.com/v1beta/openai", googleOverloadJSON, false, true, true},
		{"older message", "", strings.Replace(googleOverloadJSON, "This model is currently experiencing high demand. Spikes in demand are usually temporary.", "The model is overloaded.", 1), true, true, true},
		{"personal", "", googleOverloadJSON, true, false, false},
		{"unknown provider", "https://elsewhere.example/v1", googleOverloadJSON, false, true, false},
		{"lookalike host", "https://generativelanguage.googleapis.com.evil.invalid/v1", googleOverloadJSON, false, true, false},
		{"generic unavailable", "", `{"error":{"code":503,"status":"UNAVAILABLE","message":"Service unavailable"}}`, true, true, false},
		{"permission", "", strings.Replace(googleOverloadJSON, "Spikes in demand are usually temporary.", "API key permission denied.", 1), true, true, false},
		{"different code", "", strings.Replace(googleOverloadJSON, "503", "403", 1), true, true, false},
		{"different status", "", strings.Replace(googleOverloadJSON, "UNAVAILABLE", "PERMISSION_DENIED", 1), true, true, false},
		{"duplicate field", "", strings.Replace(googleOverloadJSON, `"code":503`, `"code":503,"code":503`, 1), true, true, false},
		{"truncated", "", googleOverloadJSON[:len(googleOverloadJSON)-1], true, true, false},
		{"trailing object", "", googleOverloadJSON + ` {}`, true, true, false},
		{"oversized", "", strings.Replace(googleOverloadJSON, "Spikes", strings.Repeat("x", MaxBodyBytes)+"Spikes", 1), true, true, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c := testContext("opaque-secret")
			c.BaseURL, c.GoogleAPI, c.CharityCall = tc.base, tc.google, tc.charity
			d := c.Parse([]byte(tc.body))
			want := int64(0)
			if tc.match {
				want = 60
			}
			if got := d.TemporaryBackoff().Delay(503); got != want {
				t.Fatalf("delay=%d want=%d", got, want)
			}
			for _, status := range []int{0, 200, 400, 401, 403, 429, 500, 502, 504} {
				if d.TemporaryBackoff().Delay(status) != 0 {
					t.Fatal("non-503 response exempted", status)
				}
			}
			if tc.match && d.Backoff().Valid() {
				t.Fatal("overload became a long quota wait")
			}
		})
	}
}

func TestGoogleOverloadEvidenceSurvivesPublicRedaction(t *testing.T) {
	c := Context{CharityCall: true, GoogleAPI: true, ContainsSecret: func(b []byte) bool { return len(b) > 0 }}
	if d := c.Parse([]byte(googleOverloadJSON)); d.TemporaryBackoff().Delay(503) != 60 {
		t.Fatal("public redaction erased private classification")
	}
}
