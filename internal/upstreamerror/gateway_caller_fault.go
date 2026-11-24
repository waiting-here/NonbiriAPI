package upstreamerror

import (
	"encoding/json"
	"regexp"
)

var gatewayOutputRange = regexp.MustCompile(`^Unable to submit request because it has a maxOutputTokens value of ([0-9]{1,10}) but the supported range is from ([0-9]{1,10}) \(inclusive\) to ([0-9]{1,10}) \(exclusive\)\. Update the value and try again\.$`)

func (c Context) gatewayCallerFault(fields map[string]json.RawMessage) bool {
	if c.responseStatus != 400 || machineCode(fields) != "AI_APICallError" {
		return false
	}
	var kind, message string
	var cause struct {
		Type        string `json:"type"`
		Message     string `json:"message"`
		StatusCode  int    `json:"statusCode"`
		IsRetryable *bool  `json:"isRetryable"`
	}
	if json.Unmarshal(fields["type"], &kind) != nil || kind != "AI_APICallError" ||
		json.Unmarshal(fields["message"], &message) != nil ||
		json.Unmarshal(fields["param"], &cause) != nil || cause.Type != kind ||
		cause.StatusCode != 400 || cause.Message != message || (cause.IsRetryable != nil && *cause.IsRetryable) {
		return false
	}
	return reportedTurnOrBudget(message)
}

func reportedTurnOrBudget(message string) bool {
	return message == "Requests ending with a model turn are not supported." || gatewayOutputRange.MatchString(message)
}
