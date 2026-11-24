package upstreamerror

import "encoding/json"

// CallerFault identifies a caller-input error reported by the upstream.
// The zero value does not exempt a failed attempt.
// It retains no parameter name, value, request body or upstream message.
type CallerFault struct {
	status int
	code   string
}

func (f CallerFault) Matches(status int, code string) bool {
	return (f.status == 400 || f.status == 422) && f.status == status && f.code == code
}

func (c Context) callerFault(fields map[string]json.RawMessage) CallerFault {
	if !c.CharityCall || c.responseStatus != 400 && c.responseStatus != 422 {
		return CallerFault{}
	}
	if c.gatewayCallerFault(fields) {
		return CallerFault{status: c.responseStatus, code: "AI_APICallError"}
	}
	if c.googleCallerFault(fields) {
		return CallerFault{status: c.responseStatus, code: "400"}
	}
	var kind, code, param, message string
	if json.Unmarshal(fields["type"], &kind) != nil ||
		json.Unmarshal(fields["code"], &code) != nil ||
		json.Unmarshal(fields["message"], &message) != nil || message == "" {
		return CallerFault{}
	}
	if inputLengthError(kind, code, fields["param"], message) {
		return CallerFault{status: c.responseStatus, code: code}
	}
	if json.Unmarshal(fields["param"], &param) != nil ||
		!parameterErrorKind(c.responseStatus, kind, code) {
		return CallerFault{}
	}
	// Resource references, model selection, identity, storage and service tiers
	// can fail because of upstream configuration or permissions. Unknown names
	// and unrecognized nested paths remain outside this generation list.
	switch param {
	case "temperature", "top_p", "top_k", "frequency_penalty", "presence_penalty",
		"seed", "stop", "n", "logprobs", "top_logprobs", "logit_bias",
		"max_tokens", "max_completion_tokens", "max_output_tokens", "reasoning_effort",
		"reasoning", "reasoning.effort", "thinking", "thinking.type", "thinking.budget_tokens",
		"verbosity", "response_format", "dimensions", "encoding_format",
		"size", "quality", "style", "output_format", "output_compression", "partial_images",
		"aspect_ratio", "resolution", "steps", "cfg_scale", "negative_prompt", "async",
		"speed", "language":
		return CallerFault{status: c.responseStatus, code: code}
	}
	return CallerFault{}
}

func parameterErrorKind(status int, kind, code string) bool {
	switch kind {
	case "invalid_request_error":
		switch code {
		case "invalid_value", "invalid_type", "unsupported_parameter", "unsupported_value":
			return true
		}
	case "BadRequest":
		// Ark reports parameter validation with this exact type/code pair.
		// A generic InvalidParameter without an eligible param is still refused.
		return status == 400 && code == "InvalidParameter"
	}
	return false
}
