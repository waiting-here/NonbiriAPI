package gateway

import (
	"encoding/json"

	contract "github.com/waiting-here/NonbiriAPI/internal/connector/contract"
	"github.com/waiting-here/NonbiriAPI/internal/connector/openai"
	"github.com/waiting-here/NonbiriAPI/internal/upstreamerror"
)

// callerInput is evaluated only for rejected charity requests, before the wire
// body is cleared. Configured transformations cannot supply caller evidence.
func callerInput(request *openai.ChatRequest, body []byte, policy contract.AttemptPolicy) upstreamerror.GatewayCallerInput {
	proof := upstreamerror.GatewayCallerInput{}
	if request == nil || policy.HasAdaptation || len(policy.AdditionalHeaders) != 0 || len(policy.ManagedHeaders) != 0 || len(policy.NativeExtensions) != 0 {
		return proof
	}
	type turn struct {
		Role string `json:"role"`
	}
	var wire struct {
		Prompt          []turn `json:"prompt"`
		MaxOutputTokens int64  `json:"maxOutputTokens"`
	}
	if json.Unmarshal(body, &wire) != nil {
		return proof
	}
	if len(wire.Prompt) > 0 && wire.Prompt[len(wire.Prompt)-1].Role == "assistant" {
		raw, _ := request.RawField("messages")
		var original []turn
		if json.Unmarshal(raw, &original) == nil && len(original) > 0 && original[len(original)-1].Role == "assistant" {
			proof.EndsWithAssistant = true
		}
		clear(raw)
	}
	for _, name := range []string{"max_tokens", "max_completion_tokens"} {
		raw, _ := request.RawField(name)
		var value int64
		if json.Unmarshal(raw, &value) == nil && value > 0 && value == wire.MaxOutputTokens {
			proof.MaxOutputTokens = value
		}
		clear(raw)
	}
	return proof
}
