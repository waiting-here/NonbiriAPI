package upstreamerror

import (
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
)

func gatewayErrorBody(message string) string {
	body, _ := json.Marshal(map[string]any{"error": map[string]any{
		"type": "AI_APICallError", "message": message,
		"param": map[string]any{"error": message, "message": message, "statusCode": 400, "name": "AI_APICallError", "type": "AI_APICallError", "isRetryable": false},
	}})
	return string(body)
}

func TestGatewayCallerFaultRequiresSpecificRequestEvidence(t *testing.T) {
	const turnMessage = "Requests ending with a model turn are not supported."
	const rangeMessage = "Unable to submit request because it has a maxOutputTokens value of 100000 but the supported range is from 1 (inclusive) to 65537 (exclusive). Update the value and try again."
	turn, budget := gatewayErrorBody(turnMessage), gatewayErrorBody(rangeMessage)
	for _, tc := range []struct {
		name, body, media string
		status            int
		personal, want    bool
	}{
		{"model turn", turn, "application/json", 400, false, true},
		{"output budget", budget, "application/json; charset=utf-8", 400, false, true},
		{"other numeric limit", strings.ReplaceAll(budget, "65537", "8193"), "application/json", 400, false, true},
		{"range upper boundary", strings.ReplaceAll(budget, "100000", "65537"), "application/json", 400, false, true},
		{"inside reported range", strings.ReplaceAll(budget, "100000", "65536"), "application/json", 400, false, true},
		{"invalid reported range", strings.ReplaceAll(budget, "65537", "1"), "application/json", 400, false, true},
		{"generic request rejection", gatewayErrorBody("Invalid request."), "application/json", 400, false, false},
		{"provider failure", gatewayErrorBody("Model is not available."), "application/json", 400, false, false},
		{"unrelated range", strings.ReplaceAll(budget, "maxOutputTokens", "thinkingBudget"), "application/json", 400, false, false},
		{"wrong type", strings.ReplaceAll(turn, "AI_APICallError", "GatewayInternalServerError"), "application/json", 400, false, false},
		{"conflicting code", strings.Replace(turn, `"error":{`, `"error":{"code":"invalid_api_key",`, 1), "application/json", 400, false, false},
		{"wrong nested status", strings.Replace(turn, `"statusCode":400`, `"statusCode":500`, 1), "application/json", 400, false, false},
		{"wrong nested message", strings.Replace(turn, turnMessage, "Another error.", 1), "application/json", 400, false, false},
		{"retryable", strings.Replace(turn, `"isRetryable":false`, `"isRetryable":true`, 1), "application/json", 400, false, false},
		{"missing retryability", strings.Replace(turn, `"isRetryable":false,`, "", 1), "application/json", 400, false, true},
		{"duplicate status", strings.Replace(turn, `"statusCode":400`, `"statusCode":500,"statusCode":400`, 1), "application/json", 400, false, false},
		{"truncated", turn[:len(turn)-1], "application/json", 400, false, false},
		{"oversized", turn + strings.Repeat(" ", MaxBodyBytes), "application/json", 400, false, false},
		{"personal", turn, "application/json", 400, true, false},
		{"accepted", turn, "application/json", 200, false, false},
		{"other client status", turn, "application/json", 422, false, false},
		{"authentication", turn, "application/json", 401, false, false},
		{"rate limit", budget, "application/json", 429, false, false},
		{"server error", turn, "application/json", 500, false, false},
		{"HTML", turn, "text/html", 400, false, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c := Context{CharityCall: !tc.personal, ContainsSecret: func([]byte) bool { return false }}
			detail := c.ReadResponse(t.Context(), &http.Response{StatusCode: tc.status, Header: http.Header{"Content-Type": {tc.media}}, Body: io.NopCloser(strings.NewReader(tc.body))})
			if got := detail.CallerFault().Matches(tc.status, "AI_APICallError"); got != tc.want {
				t.Fatalf("caller fault=%t want=%t; code=%s", got, tc.want, detail.Code())
			}
			if tc.want && (detail.Code() != "AI_APICallError" || detail.Message() == "" || detail.CallerFault().Matches(422, "AI_APICallError")) {
				t.Fatal("classification changed diagnostics or escaped its HTTP status")
			}
		})
	}
	c := Context{CharityCall: true, ContainsSecret: func([]byte) bool { return false }}
	if c.Parse([]byte(turn)).CallerFault().Matches(400, "AI_APICallError") {
		t.Fatal("non-HTTP event minted evidence")
	}
}
