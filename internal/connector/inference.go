package connector

import (
	"context"
	"net/http"

	contract "github.com/waiting-here/NonbiriAPI/internal/connector/contract"
	"github.com/waiting-here/NonbiriAPI/internal/connector/openai"
)

type inferenceDriver interface {
	AttemptInference(context.Context, http.ResponseWriter, openai.Target, *openai.InferenceRequest, contract.Type, contract.AttemptPolicy) contract.AttemptResult
}

func inferenceCapabilities(kind contract.Type) contract.CapabilitySet {
	var result contract.CapabilitySet
	for _, operation := range contract.AllOperations() {
		if openai.InferenceSupported(kind, operation) {
			result |= contract.CapabilitySet(contract.OperationCapability(operation))
		}
	}
	return result
}

func rerankDescriptor(kind contract.Type) Descriptor {
	return Descriptor{Type: kind, Capabilities: inferenceCapabilities(kind), New: func(dependencies Dependencies) Connector {
		adapter, err := openai.NewAdapter(openai.AdapterConfig{Backend: dependencies.Backend})
		if err != nil {
			return nil
		}
		return &inferenceConnector{kind: kind, driver: adapter}
	}}
}

type inferenceConnector struct {
	kind   contract.Type
	driver inferenceDriver
}

func (c *inferenceConnector) Type() contract.Type { return c.kind }
func (c *inferenceConnector) Capabilities() contract.CapabilitySet {
	if c.kind == contract.TypeTypeSafeSystemOne {
		return inferenceCapabilities(c.kind) | contract.CapabilitySet(contract.CapabilityModelDiscovery)
	}
	return inferenceCapabilities(c.kind)
}

func systemOneDescriptor() Descriptor {
	kind := contract.TypeTypeSafeSystemOne
	d := rerankDescriptor(kind)
	d.Capabilities |= contract.CapabilitySet(contract.CapabilityModelDiscovery)
	d.Discoverer = openai.ModelDiscoverer{ConnectorType: kind}
	return d
}
func (c *inferenceConnector) Attempt(ctx context.Context, input AttemptInput) contract.AttemptResult {
	if c == nil {
		return contract.AttemptResult{Failure: contract.FailureInternal, Diagnostic: "forwarding attempt unavailable"}
	}
	return attemptInference(ctx, input, c.kind, c.driver)
}

func attemptInference(ctx context.Context, input AttemptInput, kind contract.Type, driver inferenceDriver) contract.AttemptResult {
	defer input.Credential.Clear()
	failed := contract.AttemptResult{Failure: contract.FailureInternal, Diagnostic: "forwarding attempt unavailable"}
	if driver == nil || ctx == nil || input.Sink == nil || input.Target.Type() != kind || !input.validOperation() || input.Inference == nil || input.Inference.CheckTarget(kind, input.Policy) != nil {
		return failed
	}
	key, ciphertext, ok := input.Credential.Take()
	if !ok {
		return failed
	}
	defer clear(key)
	defer clear(ciphertext)
	observation := Observation{Kind: ObservationAttemptStarted, Connector: kind, TraceID: input.TraceID, AttemptIndex: input.AttemptIndex, SafetyIdentifierApplied: input.Policy.SafetyIdentifier != ""}
	if input.Observer != nil {
		input.Observer.TryObserve(observation)
	}
	result := driver.AttemptInference(ctx, input.Sink, openai.NewTarget(input.Target.BaseURL(), input.Target.UpstreamModel(), openai.NewCredential(key, ciphertext)), input.Inference, kind, input.Policy)
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

func (r *Registry) SupportsInferenceRequest(kind contract.Type, request *openai.InferenceRequest) bool {
	if request == nil {
		return false
	}
	descriptor, ok := r.Descriptor(kind)
	if !ok || !descriptor.Capabilities.Has(contract.OperationCapability(request.Operation())) {
		return false
	}
	if descriptor.SupportsInference != nil {
		return descriptor.SupportsInference(request)
	}
	return request.CheckTarget(kind, contract.AttemptPolicy{}) == nil
}

func (r *Registry) CheckInferenceTarget(target contract.Target, request *openai.InferenceRequest, policy contract.AttemptPolicy) error {
	if request != nil {
		if descriptor, ok := r.Descriptor(target.Type()); ok && descriptor.Capabilities.Has(contract.OperationCapability(request.Operation())) && descriptor.CheckInferenceTarget != nil {
			return normalizePreflightError(descriptor.CheckInferenceTarget(target, request, policy))
		}
	}
	if !r.SupportsInferenceRequest(target.Type(), request) {
		return &contract.RequestRejection{Stage: "protocol preflight", Field: "request", Reason: "request cannot be represented by this connector"}
	}
	return request.CheckTarget(target.Type(), policy)
}
