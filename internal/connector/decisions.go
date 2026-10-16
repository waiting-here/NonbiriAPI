package connector

import (
	"context"

	contract "github.com/waiting-here/NonbiriAPI/internal/connector/contract"
	"github.com/waiting-here/NonbiriAPI/internal/connector/openai"
)

func decisionsDescriptor() Descriptor {
	return Descriptor{Type: contract.TypeOpenAIDecisions, Capabilities: decisionsCapabilities(),
		New: func(dependencies Dependencies) Connector {
			adapter, err := openai.NewAdapter(openai.AdapterConfig{Backend: dependencies.Backend})
			if err != nil {
				return nil
			}
			return &decisionsConnector{adapter: adapter}
		}, Discoverer: openai.ModelDiscoverer{ConnectorType: contract.TypeOpenAIDecisions},
	}
}
func decisionsCapabilities() contract.CapabilitySet {
	return contract.CapabilitySet(contract.CapabilityDecisions | contract.CapabilityModelDiscovery)
}

type decisionsConnector struct{ adapter *openai.Adapter }

func (*decisionsConnector) Type() contract.Type                  { return contract.TypeOpenAIDecisions }
func (*decisionsConnector) Capabilities() contract.CapabilitySet { return decisionsCapabilities() }
func (c *decisionsConnector) Attempt(ctx context.Context, input AttemptInput) contract.AttemptResult {
	defer input.Credential.Clear()
	result := contract.AttemptResult{Failure: contract.FailureInternal, Diagnostic: "forwarding attempt unavailable"}
	if c == nil || c.adapter == nil || ctx == nil || input.Sink == nil || input.Target.Type() != c.Type() || input.Operation != contract.OperationDecisions || !input.validOperation() {
		return result
	}
	key, ciphertext, ok := input.Credential.Take()
	if !ok {
		return result
	}
	defer clear(key)
	defer clear(ciphertext)
	observation := Observation{Kind: ObservationAttemptStarted, Connector: c.Type(), TraceID: input.TraceID, AttemptIndex: input.AttemptIndex, SafetyIdentifierApplied: input.Policy.SafetyIdentifier != ""}
	if input.Observer != nil {
		input.Observer.TryObserve(observation)
	}
	result = c.adapter.AttemptDecision(ctx, input.Sink, openai.NewTarget(input.Target.BaseURL(), input.Target.UpstreamModel(), openai.NewCredential(key, ciphertext)), input.Decision, input.Policy)
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
func (r *Registry) SupportsDecisionRequest(kind contract.Type, request *openai.DecisionRequest) bool {
	descriptor, ok := r.Descriptor(kind)
	return request != nil && ok && descriptor.Capabilities.Has(contract.CapabilityDecisions)
}
func (r *Registry) CheckDecisionTarget(target contract.Target, request *openai.DecisionRequest, policy contract.AttemptPolicy) error {
	if !r.SupportsDecisionRequest(target.Type(), request) || len(policy.NativeExtensions) != 0 || policy.FlattenToolCalls || policy.ForceStoreFalse {
		return &contract.RequestRejection{Stage: "protocol preflight", Field: "request", Reason: "request cannot be represented by this connector"}
	}
	return nil
}
