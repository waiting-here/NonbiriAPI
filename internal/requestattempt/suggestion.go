package requestattempt

import "strings"

const unsupportedOptionSuggestion = "Omit this option. To keep it, choose a model that supports it or ask an operator to check the model's upstream bindings."

// Suggestions are derived from the same bounded server-authored reason. A
// stored suggestion is never trusted or echoed when decoding an older record.
func rejectionSuggestion(field, reason string) string {
	switch reason {
	case "expected a number from -2 to 2":
		return "Set the penalty to a number from -2 through 2."
	case "expected 1 to 4096 messages":
		return "Send a nonempty messages array with at most 4096 entries."
	case "message attribute cannot be preserved by Gemini", "message contains an unsupported attribute", "content block contains an unsupported attribute":
		return "Remove the unsupported attribute if it is optional, or choose a protocol that preserves it."
	case "expected a supported message role":
		return "Use user, assistant, tool, system or developer under the configured role policy."
	case "Gemini accepts system and developer instructions only before conversation turns":
		return "Keep initial instructions at the start. For instructions that must take effect mid-conversation, choose a protocol that supports that position."
	case "message attribute is not valid for this role":
		return "Place tool calls and reasoning on assistant messages, and tool_call_id on the matching tool result."
	case "tool results must follow their assistant call before the next turn", "tool result must match an unanswered tool call", "expected a unique nonempty tool call ID of at most 128 bytes", "assistant tool calls are missing one or more results":
		return "Return one tool message per call ID, immediately after the assistant call and before the next turn."
	case "tool result content must be a string":
		return "Send the tool output as a string, serializing any structured result as JSON."
	case "assistant reasoning content must be a string":
		return "Send reasoning_content as text or omit the empty field."
	case "invalid Google continuation metadata", "thought signature has no matching message part", "Google continuation parts do not match the assistant history":
		return "Replay the original assistant response with its matching Google parts, tool calls and signatures."
	case "expected an array of up to 128 function calls", "expected a function call with supported attributes", "expected a function object containing name and arguments":
		return "Use a tool_calls array of function entries with an ID, function name and arguments."
	case "expected a supported function name":
		return "Use 1 to 64 ASCII letters, digits, underscores, dots, colons or hyphens, starting with a letter or underscore."
	case "function arguments must be a JSON object encoded as a string":
		return "Serialize the function arguments as one JSON object, using {} for no arguments."
	case "a conversation turn needs content or an assistant tool call", "at least one conversation turn must follow the instructions":
		return "Include a conversation turn with text, supported content blocks or an assistant tool call."
	case "expected text or an array of supported content blocks", "expected a content block object", "text block must contain a string", "unsupported Gemini content block type":
		return "Send text or an array of text blocks and supported user image_url blocks."
	case "image content requires a user message":
		return "Put the image in a user message or choose a protocol that supports images in this role."
	case "expected an image URL object containing url and optional detail", "Gemini requires an inline base64 PNG, JPEG, GIF or WebP image":
		return "Use image_url.url with a base64 data URL for a PNG, JPEG, GIF or WebP image."
	case "Gemini image detail must be auto or omitted":
		return "Set image_url.detail to auto or omit it, or use a protocol supporting the requested detail."
	case "reasoning effort conflicts with native thinking settings":
		return "Keep either reasoning_effort or the native thinking level or budget."
	case "sampling parameter is not supported by this model", "native thinking setting is not supported by this model":
		return "Omit the setting or choose a model that supports it."
	case "expected a number from -2 inclusive to 2 exclusive":
		return "Set the penalty to a number greater than or equal to -2 and less than 2."
	case "expected a signed 32-bit integer":
		return "Use an integer from -2147483648 through 2147483647."
	case "unsupported top-level field":
		if field == "request" {
			return "This entry did not record the unsupported field name. Retry the request to capture its location."
		}
		if validTopLevelPosition(field) {
			return "The field name is omitted for privacy. Check its position in the top-level request fields after configured overrides; numbering starts at 1."
		}
		return unsupportedOptionSuggestion
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
	case "field cannot be represented by this connector", "Responses fields cannot be represented by the selected Chat protocol", "rerank field is not supported by the selected protocol":
		return unsupportedOptionSuggestion
	case "Responses input cannot be represented as complete Chat messages":
		return "Use complete Chat messages or choose a model with native Responses support."
	case "tool flattening is unavailable for Responses conversion":
		return "Turn off tool flattening or choose a model with a source that supports it."
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
