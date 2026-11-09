package upstreamerror

import (
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
)

func TestInputLengthFaultRequiresSpecificEvidence(t *testing.T) {
	for _, tc := range []struct {
		name, code, param, message string
		want                       bool
	}{
		{"messages characters", "invalid_value", `"messages"`, "system/developer 消息字符数超限：6001，最多 5000", true},
		{"input characters", "invalid_value", `"input"`, "输入内容字符数超过限制", true},
		{"prompt length", "invalid_value", `"prompt"`, "提示词长度超出上限", true},
		{"input too long", "invalid_value", `"input"`, "Input is too long.", true},
		{"messages length", "invalid_value", `"messages"`, "Messages length exceeds the maximum of 5000 characters.", true},
		{"prompt tokens", "invalid_value", `"prompt"`, "Prompt exceeds the maximum 5000 tokens.", true},
		{"context limit", "invalid_value", `"messages"`, "This model's maximum context length is 4096 tokens; requested 5000.", true},
		{"context code", "context_length_exceeded", "", "Request exceeds the context limit.", true},
		{"context null", "context_length_exceeded", "null", "Request exceeds the context limit.", true},
		{"input code", "input_too_long", `"input"`, "Input limit.", true},
		{"input missing param", "input_too_long", "", "Input limit.", true},
		{"prompt code", "prompt_too_long", `"prompt"`, "Prompt limit.", true},
		{"prompt null", "prompt_too_long", "null", "Prompt limit.", true},
		{"string input", "string_above_max_length", `"input"`, "String limit.", true},
		{"string messages", "string_above_max_length", `"messages"`, "String limit.", true},
		{"string prompt", "string_above_max_length", `"prompt"`, "String limit.", true},
		{"string missing", "string_above_max_length", "", "String limit.", false},
		{"string null", "string_above_max_length", "null", "String limit.", false},
		{"string model", "string_above_max_length", `"model"`, "String limit.", false},
		{"context other param", "context_length_exceeded", `"max_tokens"`, "Request exceeds context limit.", false},
		{"context model", "context_length_exceeded", `"model"`, "Context limit.", false},
		{"context numeric param", "context_length_exceeded", `42`, "Context limit.", false},
		{"context empty param", "context_length_exceeded", `""`, "Context limit.", false},
		{"nested messages", "invalid_value", `"messages[0].content"`, "Input is too long.", false},
		{"generic invalid value", "invalid_value", `"messages"`, "The messages structure is invalid.", false},
		{"role error", "invalid_value", `"messages"`, "The system role is unsupported.", false},
		{"output limit", "invalid_value", `"messages"`, "Maximum output length exceeded.", false},
		{"quota", "invalid_value", `"input"`, "Input exceeds billing quota.", false},
		{"model error", "invalid_value", `"model"`, "Input is too long.", false},
		{"missing generic param", "invalid_value", "", "Input is too long.", false},
		{"null generic param", "invalid_value", "null", "Input is too long.", false},
		{"unknown code", "input_too_long.Unknown", `"input"`, "Input is too long.", false},
		{"auth code", "authentication_error", `"input"`, "Input is too long.", false},
		{"rate limit code", "rate_limit_exceeded", `"input"`, "Input is too long.", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			fields := map[string]any{"type": "invalid_request_error", "code": tc.code, "message": tc.message}
			if tc.param != "" {
				fields["param"] = json.RawMessage(tc.param)
			}
			body, err := json.Marshal(map[string]any{"error": fields})
			if err != nil {
				t.Fatal(err)
			}
			c := Context{CharityCall: true, InputLengthErrors: true, ContainsSecret: func([]byte) bool { return false }}
			for _, status := range []int{400, 422} {
				detail := c.ReadResponse(t.Context(), &http.Response{StatusCode: status, Header: http.Header{"Content-Type": {"application/json; charset=utf-8"}}, Body: io.NopCloser(strings.NewReader(string(body)))})
				if got := detail.CallerFault().Matches(status, tc.code); got != tc.want {
					t.Fatalf("status=%d classified=%t want=%t", status, got, tc.want)
				}
			}
		})
	}
}

func TestInputLengthFaultRetainsResponseBoundary(t *testing.T) {
	const valid = `{"error":{"type":"invalid_request_error","code":"invalid_value","param":"messages","message":"system/developer 消息字符数超限：6001，最多 5000","details":{"group":"system","actual":6001,"limit":5000,"unit":"unicode_code_point"}}}`
	for _, tc := range []struct {
		name, body, media  string
		status             int
		personal, disabled bool
	}{
		{"ordinary caller", valid, "application/json", 400, true, false},
		{"unsupported path", valid, "application/json", 400, false, true},
		{"accepted", valid, "application/json", 200, false, false},
		{"auth", valid, "application/json", 401, false, false},
		{"rate limit", valid, "application/json", 429, false, false},
		{"server", valid, "application/json", 500, false, false},
		{"missing media", valid, "", 400, false, false},
		{"wrong media", valid, "text/plain", 400, false, false},
		{"wrong type", strings.Replace(valid, "invalid_request_error", "authentication_error", 1), "application/json", 400, false, false},
		{"missing message", strings.Replace(valid, `"system/developer 消息字符数超限：6001，最多 5000"`, `null`, 1), "application/json", 400, false, false},
		{"duplicate", strings.Replace(valid, `"param":`, `"param":"model","param":`, 1), "application/json", 400, false, false},
		{"trailing", valid + `{}`, "application/json", 400, false, false},
		{"truncated", valid[:len(valid)-1], "application/json", 400, false, false},
		{"oversize", strings.Replace(valid, "system/developer", strings.Repeat("x", MaxBodyBytes), 1), "application/json", 400, false, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c := Context{CharityCall: !tc.personal, InputLengthErrors: !tc.disabled, ContainsSecret: func([]byte) bool { return false }}
			detail := c.ReadResponse(t.Context(), &http.Response{StatusCode: tc.status, Header: http.Header{"Content-Type": {tc.media}}, Body: io.NopCloser(strings.NewReader(tc.body))})
			if detail.CallerFault().Matches(tc.status, detail.Code()) {
				t.Fatal("ineligible response supplied input-length evidence")
			}
		})
	}
	c := Context{CharityCall: true, InputLengthErrors: true, ContainsSecret: func([]byte) bool { return false }}
	if c.Parse([]byte(valid)).CallerFault().Matches(400, "invalid_value") {
		t.Fatal("non-HTTP input error supplied evidence")
	}
	if detail := c.ReadResponse(t.Context(), &http.Response{StatusCode: 400, Header: http.Header{"Content-Type": {"application/json"}}, Body: io.NopCloser(strings.NewReader(valid))}, 32); detail.CallerFault().Matches(400, "invalid_value") {
		t.Fatal("response summary budget was ignored")
	}
}

func TestInputLengthFaultKeepsOnlyKnownCodeWhenDetailsAreSuppressed(t *testing.T) {
	for _, tc := range []struct {
		name, param, message string
		want                 bool
	}{
		{"input length", "messages", "system/developer 消息字符数超限：6001，最多 5000", true},
		{"ordinary parameter", "temperature", "Invalid value.", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			body, err := json.Marshal(map[string]any{"error": map[string]string{"type": "invalid_request_error", "code": "invalid_value", "param": tc.param, "message": tc.message}})
			if err != nil {
				t.Fatal(err)
			}
			c := Context{CharityCall: true, InputLengthErrors: true, ContainsSecret: func(value []byte) bool { return len(value) != 0 }, UnchangedCallerParameter: func(string) bool { return true }}
			detail := c.ReadResponse(t.Context(), &http.Response{StatusCode: 400, Header: http.Header{"Content-Type": {"application/json"}}, Body: io.NopCloser(strings.NewReader(string(body)))})
			if detail.CallerFault().Matches(400, "invalid_value") != tc.want || (detail.Code() == "invalid_value") != tc.want || strings.Contains(detail.Message(), tc.message) {
				t.Fatalf("detail=%+v", detail)
			}
		})
	}
}
