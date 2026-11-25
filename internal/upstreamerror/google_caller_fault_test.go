package upstreamerror

import (
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"
)

func TestGoogleInvalidArgumentCallerFault(t *testing.T) {
	const turn = "Requests ending with a model turn are not supported."
	const budget = "Unable to submit request because it has a maxOutputTokens value of 9000 but the supported range is from 1 (inclusive) to 8193 (exclusive). Update the value and try again."
	envelope := func(message, extra string) string {
		return fmt.Sprintf(`{"error":{"code":400,"status":"INVALID_ARGUMENT","message":%q%s}}`, message, extra)
	}
	const badFields = `,"details":[{"@type":"type.googleapis.com/google.rpc.BadRequest","fieldViolations":[{"field":"generationConfig.temperature"},{"field":"generation_config.max_output_tokens"}]}]`
	const invalidKey = `,"details":[{"@type":"type.googleapis.com/google.rpc.ErrorInfo","reason":"API_KEY_INVALID","domain":"googleapis.com"}]`
	for _, tc := range []struct {
		name, body string
		want       bool
	}{
		{"reported error", envelope(turn, ""), true},
		{"reported output limit", envelope(budget, ""), true},
		{"input over limit", envelope("The input token count (9000) exceeds the maximum number of tokens allowed (8192).", ""), true},
		{"inconsistent input limit", envelope("The input token count (1000) exceeds the maximum number of tokens allowed (8192).", ""), true},
		{"caller generation fields", envelope("Invalid generation settings.", badFields), true},
		{"mixed resource violation", envelope(turn, strings.Replace(badFields, "generationConfig.temperature", "model", 1)), false},
		{"invalid API key", envelope("API key not valid. Please pass a valid API key.", invalidKey), false},
		{"conflicting credential detail", envelope(turn, invalidKey), false},
		{"unknown reason", envelope(turn, strings.Replace(invalidKey, "API_KEY_INVALID", "BILLING_DISABLED", 1)), false},
		{"generic invalid argument", envelope("Request contains an invalid argument.", ""), false},
		{"wrong status", strings.Replace(envelope(turn, ""), "INVALID_ARGUMENT", "UNAUTHENTICATED", 1), false},
		{"string code", strings.Replace(envelope(turn, ""), "400", `"400"`, 1), false},
		{"mismatched numeric code", strings.Replace(envelope(turn, ""), "400", "503", 1), false},
		{"duplicate field", strings.Replace(envelope(turn, ""), `"code":400`, `"code":401,"code":400`, 1), false},
		{"conflicting envelope", envelope(turn, `,"type":"authentication_error"`), false},
		{"malformed details", envelope(turn, `,"details":{}`), false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c := Context{CharityCall: true, GoogleAPI: true, ContainsSecret: func([]byte) bool { return false }}
			read := func(status int, media string) Detail {
				return c.ReadResponse(t.Context(), &http.Response{StatusCode: status, Header: http.Header{"Content-Type": {media}}, Body: io.NopCloser(strings.NewReader(tc.body))})
			}
			d := read(400, "application/json; charset=utf-8")
			if d.CallerFault().Matches(400, d.Code()) != tc.want || tc.want && d.Code() != "400" {
				t.Fatalf("code=%q caller fault=%v, want %v", d.Code(), d.CallerFault(), tc.want)
			}
			if read(422, "application/json").CallerFault().Matches(422, "400") || read(400, "text/plain").CallerFault().Matches(400, "400") {
				t.Fatal("caller fault escaped the HTTP status or JSON boundary")
			}
			c.CharityCall = false
			if read(400, "application/json").CallerFault().Matches(400, "400") {
				t.Fatal("personal request changed charity accounting")
			}
		})
	}
}
