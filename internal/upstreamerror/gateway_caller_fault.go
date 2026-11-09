package upstreamerror

import (
	"encoding/json"
	"regexp"
	"strconv"
)

// GatewayCallerInput contains only the two facts needed to attribute a native
// Gateway rejection. It never retains message content or a request body.
type GatewayCallerInput struct {
	EndsWithAssistant bool
	MaxOutputTokens   int64
}

var gatewayOutputRange = regexp.MustCompile(`^Unable to submit request because it has a maxOutputTokens value of ([0-9]{1,10}) but the supported range is from ([0-9]{1,10}) \(inclusive\) to ([0-9]{1,10}) \(exclusive\)\. Update the value and try again\.$`)

func (c Context) gatewayCallerFault(fields map[string]json.RawMessage) bool {
	if c.GatewayCallerInput == (GatewayCallerInput{}) || c.responseStatus != 400 || machineCode(fields) != "AI_APICallError" {
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
		cause.StatusCode != 400 || cause.Message != message || cause.IsRetryable == nil || *cause.IsRetryable {
		return false
	}
	if message == "Requests ending with a model turn are not supported." {
		return c.GatewayCallerInput.EndsWithAssistant
	}
	if c.GatewayCallerInput.MaxOutputTokens <= 0 {
		return false
	}
	parts := gatewayOutputRange.FindStringSubmatch(message)
	if parts == nil {
		return false
	}
	value, _ := strconv.ParseInt(parts[1], 10, 64)
	minimum, _ := strconv.ParseInt(parts[2], 10, 64)
	maximum, _ := strconv.ParseInt(parts[3], 10, 64)
	return value == c.GatewayCallerInput.MaxOutputTokens && minimum < maximum && (value < minimum || value >= maximum)
}
