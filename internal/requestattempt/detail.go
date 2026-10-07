package requestattempt

import (
	"context"
	"encoding/json"
	"fmt"
)

// RejectionDetail contains server-authored checks, never caller values.
type RejectionDetail struct {
	Field  string `json:"field"`
	Reason string `json:"reason"`
}

var rejectionFieldsAllowed = map[string]bool{
	"messages[]": true, "messages[].content[]": true,
	"cache_control": true, "tools[].cache_control": true, "messages[].content[].cache_control": true, "providerOptions.anthropic.cacheControl": true,
	"request":                          true,
	"query":                            true,
	"Content-Type":                     true,
	"Content-Encoding":                 true,
	"body":                             true,
	"model":                            true,
	"stream":                           true,
	"stream_options":                   true,
	"input":                            true,
	"encoding_format":                  true,
	"dimensions":                       true,
	"user":                             true,
	"messages":                         true,
	"reasoning_effort":                 true,
	"store":                            true,
	"max_completion_tokens/max_tokens": true,
	"max_completion_tokens":            true,
	"max_tokens":                       true,
	"tool_choice":                      true,
	"temperature":                      true,
	"top_p":                            true,
	"top_k":                            true,
	"presence_penalty":                 true,
	"frequency_penalty":                true,
	"seed":                             true,
	"stop":                             true,
	"logprobs":                         true,
	"n":                                true,
	"request controls":                 true,
	"messages[role=tool].content":      true,
	"messages[role=tool].content[].cache_control": true,
}

var rejectionReasonsAllowed = map[string]bool{
	"expected an array of message objects":        true,
	"expected a message object":                   true,
	"content block type and text must be strings": true,

	"expected an ephemeral cache marker":                     true,
	"cache lifetime must be 5m or 1h":                        true,
	"cache mapping is not enabled for this model":            true,
	"duplicate cache controls":                               true,
	"no eligible cache block":                                true,
	"final cache block has a conflicting lifetime":           true,
	"at most four cache breakpoints are allowed":             true,
	"1h cache breakpoints must precede 5m cache breakpoints": true,

	"messages could not be restored from flattened tool calls":  true,
	"messages do not match the configured role policy":          true,
	"no connector supports the required request features":       true,
	"request adaptation contains an unsupported field or value": true,

	"query parameters are not supported":                                                  true,
	"expected application/json with optional UTF-8 charset":                               true,
	"content encoding is not supported":                                                   true,
	"request body could not be read":                                                      true,
	"request body exceeds the configured limit":                                           true,
	"expected one valid UTF-8 JSON object":                                                true,
	"too many top-level fields":                                                           true,
	"invalid top-level field name":                                                        true,
	"duplicate top-level field":                                                           true,
	"trailing JSON values are not allowed":                                                true,
	"required field is missing":                                                           true,
	"expected a nonempty model name of at most 133 characters without control characters": true,
	"expected a boolean or null":                                                          true,
	"expected false; embeddings do not support streaming":                                 true,
	"expected a nonempty string, token array, or batch of at most 2048 inputs":            true,
	"expected float or base64":                                                            true,
	"expected a positive integer up to 2147483647":                                        true,
	"expected a string of at most 512 characters without control characters":              true,
	"expected an object or null":                                                          true,
	"GET model discovery does not accept a request body":                                  true,
	"model identity is invalid or ambiguous":                                              true,
	"model was not found or is unavailable":                                               true,
	"account level does not allow this model":                                             true,
	"unsupported operation":                                                               true,
	"unsupported request feature or unknown field":                                        true,
	"request cannot be represented by this connector":                                     true,
	"output budget exceeds the configured model limit":                                    true,
	"unsupported effort value":                                                            true,
	"effort is not enabled for this model":                                                true,
	"expected a boolean":                                                                  true,
	"only false is allowed by the configured omission policy":                             true,
	"storage control is not verified for this model":                                      true,
	"unsupported top-level field":                                                         true,
	"expected an integer from 1 to 2147483647":                                            true,
	"conflicting output budgets":                                                          true,
	"native extension conflicts with translated controls":                                 true,
	"configured provider cannot require a tool call":                                      true,
	"configured provider requires a value from 0 to 1":                                    true,
	"configured provider omits top_p when temperature is present":                         true,
	"configured provider would omit this field":                                           true,
	"expected text or text blocks":                                                        true,
	"only text blocks are supported":                                                      true,
	"an intermediate tool-result cache position cannot be represented":                    true,
	"OpenAI key storage policy is incompatible with this connector":                       true,
	"stream options cannot be converted":                                                  true,
	"role cannot be passed through to this connector":                                     true,
}

func NewDetail(field, reason string) *RejectionDetail {
	if !rejectionReasonsAllowed[reason] && !validContentLengthReason(reason) {
		return nil
	}
	if !rejectionFieldsAllowed[field] {
		field = "request"
	}
	return &RejectionDetail{Field: field, Reason: reason}
}
func Detail(ctx context.Context, detail *RejectionDetail) {
	if detail == nil {
		return
	}
	safe := NewDetail(detail.Field, detail.Reason)
	if safe == nil {
		return
	}
	if a := get(ctx); a != nil {
		a.mu.Lock()
		defer a.mu.Unlock()
		a.fact.Detail = safe
	}
}
func EncodeDetail(detail *RejectionDetail) string {
	if detail == nil {
		return ""
	}
	safe := NewDetail(detail.Field, detail.Reason)
	if safe == nil {
		return ""
	}
	data, _ := json.Marshal(safe)
	return string(data)
}
func DecodeDetail(data string) *RejectionDetail {
	if len(data) > 512 {
		return nil
	}
	var detail RejectionDetail
	if json.Unmarshal([]byte(data), &detail) != nil {
		if validContentLengthReason(data) {
			return &RejectionDetail{Field: "messages", Reason: data}
		}
		return nil
	}
	return NewDetail(detail.Field, detail.Reason)
}

func validContentLengthReason(reason string) bool {
	var actual, minimum int
	n, err := fmt.Sscanf(reason, "content has %d characters; minimum is %d", &actual, &minimum)
	return err == nil && n == 2 && actual >= 0 && actual < minimum && minimum <= 1<<20 && reason == fmt.Sprintf("content has %d characters; minimum is %d", actual, minimum)
}
