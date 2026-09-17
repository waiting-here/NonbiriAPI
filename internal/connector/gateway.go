package connector

import (
	"context"

	contract "github.com/waiting-here/NonbiriAPI/internal/connector/contract"
	"github.com/waiting-here/NonbiriAPI/internal/connector/gateway"
)

// GatewayAttributionProvider resolves the administrator's current setting for
// each dispatch. The generated pseudonym remains confined to the attempt.
type GatewayAttributionProvider interface {
	GatewayUserAttributionEnabled(context.Context) (bool, error)
}

func gatewayCapabilities() contract.CapabilitySet {
	return contract.CapabilitySet(contract.CapabilityText | contract.CapabilitySystem | contract.CapabilityImages | contract.CapabilityTools | contract.CapabilityToolChoice | contract.CapabilityStream | contract.CapabilitySampling | contract.CapabilityModelDiscovery | contract.CapabilityEmbeddings)
}
func gatewayDescriptor() Descriptor {
	return Descriptor{Type: contract.TypeAISDKGatewayV3, Capabilities: gatewayCapabilities(), Discoverer: gateway.ModelDiscoverer{}, Supports: gateway.SupportsRequest, SupportsEmbedding: gateway.SupportsEmbedding,
		New: func(dependencies Dependencies) Connector {
			adapter, err := gateway.NewAdapter(dependencies.Backend)
			if err != nil {
				return nil
			}
			return &gatewayConnector{adapter: adapter, attribution: dependencies.GatewayAttribution}
		}}
}

type gatewayConnector struct {
	adapter     *gateway.Adapter
	attribution GatewayAttributionProvider
}

func (*gatewayConnector) Type() contract.Type                  { return contract.TypeAISDKGatewayV3 }
func (*gatewayConnector) Capabilities() contract.CapabilitySet { return gatewayCapabilities() }
func (c *gatewayConnector) Attempt(ctx context.Context, input AttemptInput) contract.AttemptResult {
	defer input.Credential.Clear()
	result := contract.AttemptResult{Failure: contract.FailureInternal, Diagnostic: "gateway attempt unavailable"}
	if c == nil || c.adapter == nil || ctx == nil || input.Sink == nil || !input.validOperation() || input.Target.Type() != c.Type() {
		return result
	}
	if input.Policy.ForceStoreFalse || input.Policy.FlattenToolCalls {
		result.Diagnostic = "connector policy incompatible"
		return result
	}
	attribution := ""
	if !nilInterface(c.attribution) {
		enabled, err := c.attribution.GatewayUserAttributionEnabled(ctx)
		if err != nil {
			result.Diagnostic = "connector configuration unavailable"
			return result
		}
		if enabled {
			if input.Policy.SafetyIdentifier == "" {
				return result
			}
			attribution = input.Policy.SafetyIdentifier
		}
	}
	if input.Observer != nil {
		input.Observer.TryObserve(Observation{Kind: ObservationAttemptStarted, Connector: c.Type(), TraceID: input.TraceID, AttemptIndex: input.AttemptIndex})
	}
	result = c.adapter.Attempt(ctx, input.Sink, input.Target, input.Credential, input.Ingress, input.Embedding, attribution)
	if input.Observer != nil {
		input.Observer.TryObserve(Observation{Kind: ObservationAttemptFinished, Connector: c.Type(), TraceID: input.TraceID, AttemptIndex: input.AttemptIndex,
			Success: result.Success, Committed: result.Committed, Failure: result.Failure, Usage: result.Usage, Diagnostic: result.Diagnostic, GatewayUserAttributionSent: result.GatewayUserAttributionSent != nil && *result.GatewayUserAttributionSent})
	}
	return result
}
