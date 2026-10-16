package connector

import (
	"context"
	"encoding/json"

	"github.com/waiting-here/NonbiriAPI/internal/connector/chatwire"
	contract "github.com/waiting-here/NonbiriAPI/internal/connector/contract"
	"github.com/waiting-here/NonbiriAPI/internal/connector/gemini"
	"github.com/waiting-here/NonbiriAPI/internal/connector/openai"
	"github.com/waiting-here/NonbiriAPI/internal/connector/responses"
)

func translatingCapabilities(kind contract.Type) contract.CapabilitySet {
	capabilities := contract.CapabilitySet(contract.CapabilityText | contract.CapabilitySystem | contract.CapabilityDeveloper | contract.CapabilityImages | contract.CapabilityTools | contract.CapabilityToolChoice | contract.CapabilityParallelTools | contract.CapabilityStream | contract.CapabilitySampling | contract.CapabilityModelDiscovery)
	if kind == contract.TypeOpenAIResponses {
		capabilities |= contract.CapabilitySet(contract.CapabilityReasoningEffort | contract.CapabilityStorage | contract.CapabilityPromptCache | contract.CapabilityUnknownOpenAIFields)
	}
	return capabilities
}
func translatingDescriptor(kind contract.Type) Descriptor {
	protocol := chatwire.Protocol{Type: kind, Header: "Authorization", Compile: responses.Compile, URL: responses.URL, Decoder: responses.NewDecoder, ErrorEvent: responses.ErrorEvent}
	var discoverer ModelDiscoverer = openai.ModelDiscoverer{ConnectorType: kind}
	supports := responses.SupportsRequest
	if kind == contract.TypeGoogleGemini {
		protocol.Header = "X-Goog-Api-Key"
		protocol.Compile = gemini.Compile
		protocol.URL = gemini.URL
		protocol.Decoder = gemini.NewDecoder
		protocol.ErrorEvent = nil
		protocol.LowercaseIdentity = true
		discoverer = gemini.ModelDiscoverer{}
		supports = gemini.SupportsRequest
	}
	return Descriptor{Type: kind, Capabilities: translatingCapabilities(kind), Discoverer: discoverer, Supports: supports,
		New: func(dependencies Dependencies) Connector {
			adapter, err := chatwire.New(dependencies.Backend, protocol)
			if err != nil {
				return nil
			}
			return &translatingConnector{adapter: adapter, kind: kind}
		},
		CheckTarget: func(target contract.Target, request *openai.ChatRequest, policy contract.AttemptPolicy) error {
			if target.Type() != kind || kind == contract.TypeGoogleGemini && policy.ForceStoreFalse {
				return chatwire.Rejection("store")
			}
			body, err := protocol.Compile(request, target.UpstreamModel(), policy)
			clear(body)
			return err
		},
	}
}

type translatingConnector struct {
	adapter *chatwire.Adapter
	kind    contract.Type
}

func (c *translatingConnector) Type() contract.Type { return c.kind }
func (c *translatingConnector) Capabilities() contract.CapabilitySet {
	return translatingCapabilities(c.kind)
}
func (c *translatingConnector) Attempt(ctx context.Context, input AttemptInput) contract.AttemptResult {
	defer input.Credential.Clear()
	if c == nil || c.adapter == nil || ctx == nil || input.Sink == nil || input.Target.Type() != c.kind || input.Operation != contract.OperationChatCompletions || !input.validOperation() {
		return contract.AttemptResult{Failure: contract.FailureInternal, Diagnostic: "forwarding attempt unavailable"}
	}
	observation := Observation{Kind: ObservationAttemptStarted, Connector: c.kind, TraceID: input.TraceID, AttemptIndex: input.AttemptIndex, SafetyIdentifierApplied: input.Policy.SafetyIdentifier != ""}
	if input.Observer != nil {
		input.Observer.TryObserve(observation)
	}
	result := c.adapter.Attempt(ctx, input.Sink, input.Target, input.Credential, input.Ingress, input.Policy)
	if input.Observer != nil {
		observation.Kind = ObservationAttemptFinished
		observation.Success = result.Success
		observation.Committed = result.Committed
		observation.Failure = result.Failure
		observation.Usage = result.Usage
		observation.Diagnostic = result.Diagnostic
		input.Observer.TryObserve(observation)
	}
	return result
}

// A continuation is scoped to its source protocol even in mixed bindings.
func continuationCompatible(kind contract.Type, request *openai.ChatRequest) bool {
	raw, ok := request.RawField("messages")
	if !ok {
		return false
	}
	defer clear(raw)
	var messages []map[string]json.RawMessage
	if json.Unmarshal(raw, &messages) != nil {
		return false
	}
	compatible := func(raw json.RawMessage) bool {
		var extra map[string]json.RawMessage
		if json.Unmarshal(raw, &extra) != nil {
			return true
		}
		if _, ok := extra["google"]; ok && kind != contract.TypeGoogleGemini {
			return false
		}
		var openai map[string]json.RawMessage
		if json.Unmarshal(extra["openai"], &openai) == nil {
			if _, ok := openai["reasoning"]; ok && kind != contract.TypeOpenAIResponses {
				return false
			}
		}
		return true
	}
	for _, message := range messages {
		if !compatible(message["extra_content"]) {
			return false
		}
		var calls []map[string]json.RawMessage
		if json.Unmarshal(message["tool_calls"], &calls) == nil {
			for _, call := range calls {
				if !compatible(call["extra_content"]) {
					return false
				}
			}
		}
	}
	return true
}
