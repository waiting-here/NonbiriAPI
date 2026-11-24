package upstreamerror

import (
	"encoding/json"
	"regexp"
	"strings"
)

var googleInputTokenLimit = regexp.MustCompile(`^The input token count \(([0-9]{1,18})\) exceeds the maximum number of tokens allowed \(([0-9]{1,18})\)\.?$`)

func (c Context) googleCallerFault(fields map[string]json.RawMessage) bool {
	var status, message string
	if c.responseStatus != 400 || string(fields["code"]) != "400" ||
		json.Unmarshal(fields["status"], &status) != nil || status != "INVALID_ARGUMENT" ||
		json.Unmarshal(fields["message"], &message) != nil || strings.TrimSpace(message) == "" ||
		fields["type"] != nil || fields["param"] != nil {
		return false
	}
	// INVALID_ARGUMENT also covers bad API keys. Every reported violation must
	// refer to caller input, and a conflicting machine reason takes precedence.
	var details []map[string]json.RawMessage
	if raw := fields["details"]; raw != nil && (json.Unmarshal(raw, &details) != nil || details == nil) {
		return false
	}
	fieldError := false
	for _, detail := range details {
		var kind string
		if json.Unmarshal(detail["@type"], &kind) != nil {
			return false
		}
		switch kind {
		case googleRPC + "BadRequest":
			var violations []map[string]json.RawMessage
			if json.Unmarshal(detail["fieldViolations"], &violations) != nil || len(violations) == 0 {
				return false
			}
			for _, violation := range violations {
				var field string
				if json.Unmarshal(violation["field"], &field) != nil || !googleCallerField(field) {
					return false
				}
			}
			fieldError = true
		case googleRPC + "ErrorInfo":
			var reason string
			if json.Unmarshal(detail["reason"], &reason) != nil || reason != "INVALID_ARGUMENT" {
				return false
			}
		case googleRPC + "LocalizedMessage", googleRPC + "RequestInfo", googleRPC + "Help":
			// These details add context but cannot establish a caller error.
		default:
			return false
		}
	}
	return fieldError || reportedTurnOrBudget(message) || googleInputTokenLimit.MatchString(message)
}

var googleContentField = regexp.MustCompile(`^contents\[[0-9]+\]\.(?:role|parts(?:\[[0-9]+\](?:\.text)?)?)$`)

func googleCallerField(field string) bool {
	field = strings.TrimPrefix(field, "GenerateContentRequest.")
	switch field {
	case "generationConfig.maxOutputTokens", "generation_config.max_output_tokens",
		"generationConfig.temperature", "generation_config.temperature",
		"generationConfig.topP", "generation_config.top_p", "generationConfig.topK", "generation_config.top_k",
		"generationConfig.presencePenalty", "generation_config.presence_penalty",
		"generationConfig.frequencyPenalty", "generation_config.frequency_penalty", "generationConfig.seed", "generation_config.seed":
		return true
	}
	return googleContentField.MatchString(field)
}
