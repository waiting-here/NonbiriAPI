package upstreamerror

import (
	"io"
	"net/http"
	"strings"
	"testing"
)

const dailyGoogleBody = `{"error":{"code":429,"status":"RESOURCE_EXHAUSTED","details":[{"@type":"type.googleapis.com/google.rpc.QuotaFailure","violations":[{"quotaId":"GenerateRequestsPerDayPerProjectPerModel-FreeTier","quotaMetric":"generativelanguage.googleapis.com/generate_content_free_tier_requests","quotaDimensions":{"location":"global","model":"gemini-example"},"quotaValue":"20"}]},{"@type":"type.googleapis.com/google.rpc.RetryInfo","retryDelay":"12075s"}]}}`

func TestGoogleQuotaEvidence(t *testing.T) {
	c := testContext("synthetic-secret")
	c.CharityCall, c.GoogleAPI, c.PrivateModel = true, true, "gemini-example"
	read := func(c Context, status int, body, contentType string) Detail {
		return c.ReadResponse(t.Context(), &http.Response{StatusCode: status, Header: http.Header{"Content-Type": {contentType}}, Body: io.NopCloser(strings.NewReader(body))})
	}
	minute := strings.NewReplacer("GenerateRequestsPerDayPerProjectPerModel", "GenerateContentInputTokensPerModelPerMinute", "_requests", "_input_token_count", "12075s", "17.250s").Replace(dailyGoogleBody)
	mixed := strings.Replace(dailyGoogleBody, `"quotaValue":"20"}]`, `"quotaValue":"20"},{"quotaId":"GenerateContentInputTokensPerModelPerMinute-FreeTier","quotaMetric":"generativelanguage.googleapis.com/generate_content_free_tier_input_token_count","quotaValue":"200","quotaDimensions":{"model":"gemini-example"}}]`, 1)
	for _, tc := range []struct {
		body, reason string
		delay        int64
	}{
		{dailyGoogleBody, "google_daily_requests", 12075}, {minute, "google_minute_input_tokens", 18},
		{mixed, "google_model_quota", 12075},
		{strings.Replace(dailyGoogleBody, `,"quotaDimensions":{"location":"global","model":"gemini-example"}`, "", 1), "google_daily_requests", 12075},
		{strings.Replace(dailyGoogleBody, `,{"@type":"type.googleapis.com/google.rpc.RetryInfo","retryDelay":"12075s"}`, "", 1), "google_daily_requests", 0},
		{strings.Replace(dailyGoogleBody, "PerDay", "PerMinute", 1), "google_minute_requests", 12075},
		{strings.Replace(minute, "PerModelPerMinute", "PerModelPerDay", 1), "google_daily_input_tokens", 18},
	} {
		for _, media := range []string{"application/json", "text/event-stream"} {
			d := read(c, 429, tc.body, media)
			reason, sec := d.TemporaryBackoff().ModelQuota(429, c.PrivateModel)
			if d.ProviderReason() != tc.reason || reason != tc.reason || sec != tc.delay {
				t.Fatalf("%s/%s: %q %d", tc.reason, media, reason, sec)
			}
			if _, sec := d.TemporaryBackoff().ModelQuota(429, "other-model"); sec != 0 {
				t.Fatal("other model inherited receipt")
			}
		}
	}
	for _, tc := range []struct{ before, after string }{
		{"RESOURCE_EXHAUSTED", "UNAVAILABLE"}, {`"code":429`, `"code":"429"`},
		{"12075s", "-1s"}, {"12075s", "0s"}, {"12075s", "999999999999s"}, {"12075s", "1m3s"},
		{"12075s", "2.1234567890s"},
		{`"retryDelay":"12075s"`, `"retryDelay":"10s","retryDelay":"12075s"`},
		{"gemini-example", "other"}, {"PerDayPerProjectPerModel", "PerDayPerProject"},
		{"_requests", "_input_token_count"}, {"google.rpc.QuotaFailure", "other.QuotaFailure"},
		{`"20"`, `"0"`}, {`"20"`, `-1`}, {"quotaValue", "description"},
		{`"location":"global"`, `"location":"unknown"`}, {`{"location":"global","model":"gemini-example"}`, "null"},
		{`"quotaValue":"20"}]`, `"quotaValue":"20"},{"quotaValue":"0"}]`},
	} {
		d := read(c, 429, strings.Replace(dailyGoogleBody, tc.before, tc.after, 1), "application/json")
		if d.ProviderReason() != "" {
			t.Errorf("%s -> %s: %s", tc.before, tc.after, d.ProviderReason())
		}
	}
	for _, d := range []Detail{c.Parse([]byte(dailyGoogleBody)), read(c, 503, dailyGoogleBody, "application/json"), read(c, 429, "data: "+dailyGoogleBody+"\n\n", "text/event-stream")} {
		if d.ProviderReason() != "" {
			t.Fatal("missing HTTP/body correlation")
		}
	}
	c.GoogleAPI, c.BaseURL = false, "https://generativelanguage.googleapis.com.example/v1"
	if d := read(c, 429, dailyGoogleBody, "application/json"); d.ProviderReason() != "" {
		t.Fatal("unknown provider")
	}
	c.BaseURL = "https://generativelanguage.googleapis.com/v1beta/openai"
	if d := read(c, 429, dailyGoogleBody, "application/json"); d.ProviderReason() != "google_daily_requests" {
		t.Fatal("official compatible host")
	}
}

func TestGoogleOrdinaryErrorReasons(t *testing.T) {
	c := testContext("synthetic-secret")
	c.GoogleAPI, c.CharityCall = true, true
	for _, tc := range []struct {
		status       int
		body, reason string
	}{
		{400, `{"error":{"code":400,"status":"INVALID_ARGUMENT","details":[{"@type":"type.googleapis.com/google.rpc.ErrorInfo","domain":"googleapis.com","reason":"API_KEY_INVALID"}]}}`, "google_api_key_invalid"},
		{401, `{"error":{"code":401,"status":"UNAUTHENTICATED","details":[{"@type":"type.googleapis.com/google.rpc.ErrorInfo","reason":"ACCESS_TOKEN_TYPE_UNSUPPORTED"}]}}`, "google_access_token_type_unsupported"},
		{503, googleOverloadJSON, "google_model_overloaded"},
		{503, `{"error":{"code":503,"status":"UNAVAILABLE","message":"Service unavailable"}}`, ""},
	} {
		read := func(status int, body string) Detail {
			return c.ReadResponse(t.Context(), &http.Response{StatusCode: status, Body: io.NopCloser(strings.NewReader(body))})
		}
		d := read(tc.status, tc.body)
		if d.ProviderReason() != tc.reason {
			t.Fatal(tc.status, d.ProviderReason())
		}
		if _, sec := d.TemporaryBackoff().ModelQuota(tc.status, "model"); sec != 0 {
			t.Fatal("ordinary response became model quota")
		}
		if d := read(502, tc.body); d.ProviderReason() != "" {
			t.Fatal("mismatched actual status")
		}
	}
}
