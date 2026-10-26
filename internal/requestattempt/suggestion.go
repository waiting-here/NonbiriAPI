package requestattempt

import "strings"

// Suggestions are derived from the same bounded server-authored reason. A
// stored suggestion is never trusted or echoed when decoding an older record.
func rejectionSuggestion(field, reason string) string {
	switch reason {
	case "request validation failed without a recorded field":
		return "The specific cause was not recorded. Share the request ID with support if the problem persists."
	case "no usable model resource is available for this request", "shared model resources are temporarily at capacity":
		return "No upstream request was sent. Retry later or choose another available model."
	case "query parameters are not supported":
		return "Remove URL query parameters and send supported fields in the JSON body."
	case "expected application/json with optional UTF-8 charset", "content encoding is not supported":
		return "Send an uncompressed UTF-8 JSON body with Content-Type: application/json."
	case "request body could not be read":
		return "Send the complete request body and retry the request."
	case "expected one valid UTF-8 JSON object", "expected one JSON object", "trailing JSON values are not allowed", "duplicate top-level field":
		return "Send one valid JSON object with each top-level field appearing once."
	case "conflicting output budgets":
		return "Use one output-token limit, or give both limit fields the same value."
	case "model identity is invalid or ambiguous", "model was not found or is unavailable", "account level does not allow this model":
		return "Choose an available model from the model list for this account."
	case "required field is missing":
		return "Add the required field using the format expected by this API."
	}
	if validContentLengthReason(reason) {
		return "Add more message text to meet the minimum content length."
	}
	if strings.Contains(reason, "connector") || strings.Contains(reason, "unsupported") || strings.Contains(reason, "not enabled") || strings.Contains(reason, "not verified") || strings.Contains(reason, "configured") || strings.Contains(reason, "flattened") {
		return "Remove or adjust the unsupported option, or choose a model that supports it."
	}
	switch field {
	case "messages", "messages[]", "messages[].content[]", "messages[].role", "messages[].tool_calls", "messages[].tool_call_id", "tool_calls", "tool_call_id":
		return "Check message roles, content blocks and matching tool-call IDs."
	case "tools", "tool_choice":
		return "Check function definitions and select a tool declared in the request."
	case "max_tokens", "max_completion_tokens", "max_completion_tokens/max_tokens":
		return "Set a positive integer within the model limit and keep output-token limits consistent."
	case "stream", "stream_options":
		return "Use a supported streaming value and valid stream options."
	default:
		return "Correct the field type or value according to the stated requirement."
	}
}
