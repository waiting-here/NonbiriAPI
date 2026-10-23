package upstreamerror

import (
	"io"
	"net/http"
	"strings"
	"testing"
)

func TestCallerFaultRequiresStructuredParameterEvidence(t *testing.T) {
	const valid = `{"error":{"type":"invalid_request_error","code":"unsupported_parameter","param":"temperature","message":"Parameter is not supported."}}`
	const ark = "{\"error\":{\"type\":\"BadRequest\",\"code\":\"InvalidParameter\",\"param\":\"reasoning_effort\",\"message\":\"The parameter `reasoning_effort` specified in the request are not valid: value `min` is invalid.\"}}"
	for _, tc := range []struct {
		name, body, media       string
		status                  int
		personal, changed, want bool
	}{
		{"unsupported", valid, "application/json", 400, false, false, true},
		{"unprocessable", valid, "application/json; charset=utf-8", 422, false, false, true},
		{"value", strings.Replace(valid, "unsupported_parameter", "invalid_value", 1), "application/json", 400, false, false, true},
		{"type", strings.Replace(valid, "unsupported_parameter", "invalid_type", 1), "application/json", 400, false, false, true},
		{"unsupported value", strings.Replace(valid, "unsupported_parameter", "unsupported_value", 1), "application/json", 400, false, false, true},
		{"ark parameter", ark, "application/json", 400, false, false, true},
		{"ark changed parameter", ark, "application/json", 400, false, true, false},
		{"ark other status", ark, "application/json", 422, false, false, false},
		{"ark missing parameter", strings.Replace(ark, `"param":"reasoning_effort",`, "", 1), "application/json", 400, false, false, false},
		{"ark empty parameter", strings.Replace(ark, `"param":"reasoning_effort"`, `"param":""`, 1), "application/json", 400, false, false, false},
		{"ark resource parameter", strings.Replace(ark, `"param":"reasoning_effort"`, `"param":"model"`, 1), "application/json", 400, false, false, false},
		{"ark endpoint fault", strings.Replace(ark, "InvalidParameter", "InvalidEndpoint.ClosedEndpoint", 1), "application/json", 400, false, false, false},
		{"ark unknown suffix", strings.Replace(ark, "InvalidParameter", "InvalidParameter.Unknown", 1), "application/json", 400, false, false, false},
		{"mixed type and code", strings.Replace(ark, "BadRequest", "invalid_request_error", 1), "application/json", 400, false, false, false},
		{"personal", valid, "application/json", 400, true, false, false},
		{"changed", valid, "application/json", 400, false, true, false},
		{"accepted", valid, "application/json", 200, false, false, false},
		{"auth", valid, "application/json", 401, false, false, false},
		{"quota", valid, "application/json", 429, false, false, false},
		{"server", valid, "application/json", 500, false, false, false},
		{"html", valid, "text/html", 400, false, false, false},
		{"no media", valid, "", 400, false, false, false},
		{"ambiguous code", strings.Replace(valid, "unsupported_parameter", "invalid_request_error", 1), "application/json", 400, false, false, false},
		{"unknown code", strings.Replace(valid, "unsupported_parameter", "invalid_model", 1), "application/json", 400, false, false, false},
		{"wrong type", strings.Replace(valid, "invalid_request_error", "authentication_error", 1), "application/json", 400, false, false, false},
		{"model", strings.Replace(valid, "temperature", "model", 1), "application/json", 400, false, false, false},
		{"project policy", strings.Replace(valid, "temperature", "service_tier", 1), "application/json", 400, false, false, false},
		{"unknown param", strings.Replace(valid, "temperature", "custom_field", 1), "application/json", 400, false, false, false},
		{"nested param", strings.Replace(valid, "temperature", "reasoning.effort", 1), "application/json", 400, false, false, false},
		{"null param", strings.Replace(valid, `"temperature"`, `null`, 1), "application/json", 400, false, false, false},
		{"duplicate", strings.Replace(valid, `"param":`, `"param":"model","param":`, 1), "application/json", 400, false, false, false},
		{"trailing", valid + `{}`, "application/json", 400, false, false, false},
		{"oversize", strings.Replace(valid, "Parameter is not supported.", strings.Repeat("x", MaxBodyBytes), 1), "application/json", 400, false, false, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c := Context{CharityCall: !tc.personal, ContainsSecret: func([]byte) bool { return false }, UnchangedCallerParameter: func(string) bool { return !tc.changed }}
			response := &http.Response{StatusCode: tc.status, Header: http.Header{"Content-Type": {tc.media}}, Body: io.NopCloser(strings.NewReader(tc.body))}
			detail := c.ReadResponse(t.Context(), response)
			if got := detail.CallerFault().Matches(tc.status, detail.Code()); got != tc.want {
				t.Fatalf("classified=%t want=%t", got, tc.want)
			}
			if detail.CallerFault().Matches(503, detail.Code()) || detail.CallerFault().Matches(tc.status, "another_code") {
				t.Fatal("evidence detached from response")
			}
		})
	}
	c := Context{CharityCall: true, ContainsSecret: func([]byte) bool { return false }, UnchangedCallerParameter: func(string) bool { return true }}
	if c.Parse([]byte(valid)).CallerFault().Matches(400, "unsupported_parameter") || (CallerFault{}).Matches(400, "") {
		t.Fatal("non-HTTP error or zero value minted evidence")
	}
}
