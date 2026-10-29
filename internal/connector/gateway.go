package connector

import (
	"context"
	"net/http"

	contract "github.com/waiting-here/NonbiriAPI/internal/connector/contract"
	"github.com/waiting-here/NonbiriAPI/internal/connector/gateway"
	"github.com/waiting-here/NonbiriAPI/internal/connector/openai"
	"github.com/waiting-here/NonbiriAPI/internal/gatewaypolicy"
)

// GatewayAttributionProvider resolves the administrator's current setting for
// each dispatch. The generated pseudonym remains confined to the attempt.
type GatewayAttributionProvider interface {
	GatewayUserAttributionEnabled(context.Context) (bool, error)
}

func gatewayCapabilities() contract.CapabilitySet {
	return contract.CapabilitySet(contract.CapabilityText | contract.CapabilitySystem | contract.CapabilityImages | contract.CapabilityTools | contract.CapabilityToolChoice | contract.CapabilityStream | contract.CapabilitySampling | contract.CapabilityReasoningEffort | contract.CapabilityStorage | contract.CapabilityPromptCache | contract.CapabilityModelDiscovery | contract.CapabilityEmbeddings | contract.CapabilityImagesGenerations | contract.CapabilityRerank | contract.CapabilityResponses)
}
func gatewayDescriptor() Descriptor {
	return gatewayDescriptorWithModels(gatewaypolicy.Config{})
}

func gatewayDescriptorWithModels(models gatewaypolicy.Config) Descriptor {
	return gatewayDescriptorForType(contract.TypeAISDKGatewayV3, models)
}

func gatewayDescriptorForType(kind contract.Type, models gatewaypolicy.Config) Descriptor {
	checkChat := func(target contract.Target, request *openai.ChatRequest, policy contract.AttemptPolicy) error {
		if err := checkGatewayPolicy(policy); err != nil {
			return err
		}
		model := models.Lookup(target.BaseURL(), target.UpstreamModel())
		if policy.GatewayModel != nil {
			model = *policy.GatewayModel
		}
		body, err := gateway.CompileWithModelForType(kind, request, "", model, policy.NativeExtensions)
		clear(body)
		return err
	}
	return Descriptor{Type: kind, Capabilities: gatewayCapabilities(), Discoverer: gateway.ModelDiscoverer{},
		Supports:          func(request *openai.ChatRequest) bool { return gateway.SupportsRequestForType(kind, request) },
		SupportsEmbedding: func(request *openai.EmbeddingRequest) bool { return gateway.SupportsEmbeddingForType(kind, request) },
		SupportsImage:     gateway.SupportsImage,
		SupportsInference: func(request *openai.InferenceRequest) bool {
			if request.Operation() != contract.OperationResponses {
				return gateway.SupportsInference(request)
			}
			chat, err := request.AsGatewayChatRequest()
			if err != nil {
				return false
			}
			defer chat.Clear()
			return gateway.SupportsRequestForType(kind, chat)
		},
		CheckTarget: checkChat,
		CheckEmbeddingTarget: func(target contract.Target, request *openai.EmbeddingRequest, policy contract.AttemptPolicy) error {
			if err := checkGatewayPolicy(policy); err != nil {
				return err
			}
			return gateway.CheckEmbeddingTarget(target, request, policy)
		},
		CheckImageTarget: func(target contract.Target, request *openai.ImageRequest, policy contract.AttemptPolicy) error {
			if err := checkGatewayPolicy(policy); err != nil {
				return err
			}
			return gateway.CheckImageTarget(target, request, policy)
		},
		CheckInferenceTarget: func(target contract.Target, request *openai.InferenceRequest, policy contract.AttemptPolicy) error {
			if err := checkGatewayPolicy(policy); err != nil {
				return err
			}
			if request.Operation() != contract.OperationResponses {
				return gateway.CheckInferenceTarget(target, request, policy)
			}
			if policy.FlattenToolCalls {
				return &contract.RequestRejection{Stage: "protocol preflight", Field: "tools", Reason: "tool flattening is unavailable for Responses conversion"}
			}
			chat, err := request.AsGatewayChatRequest()
			if err != nil {
				return err
			}
			defer chat.Clear()
			return checkChat(target, chat, policy)
		},
		New: func(dependencies Dependencies) Connector {
			adapter, err := gateway.NewAdapterWithModels(dependencies.Backend, models)
			if err != nil {
				return nil
			}
			return &gatewayConnector{kind: kind, adapter: adapter, attribution: dependencies.GatewayAttribution, responseLimit: dependencies.Backend.MaxResponseBytes()}
		}}
}

func checkGatewayPolicy(policy contract.AttemptPolicy) error {
	if policy.ForceStoreFalse {
		return &contract.RequestRejection{Stage: "model preflight", Field: "store", Reason: "OpenAI key storage policy is incompatible with this connector"}
	}
	return nil
}

type gatewayConnector struct {
	kind          contract.Type
	adapter       *gateway.Adapter
	attribution   GatewayAttributionProvider
	responseLimit int64
}

func (c *gatewayConnector) Type() contract.Type {
	if c.kind == "" {
		return contract.TypeAISDKGatewayV3
	}
	return c.kind
}
func (*gatewayConnector) Capabilities() contract.CapabilitySet { return gatewayCapabilities() }
func (c *gatewayConnector) Attempt(ctx context.Context, input AttemptInput) contract.AttemptResult {
	defer input.Credential.Clear()
	result := contract.AttemptResult{Failure: contract.FailureInternal, Diagnostic: "gateway attempt unavailable"}
	if c == nil || c.adapter == nil || ctx == nil || input.Sink == nil || !input.validOperation() || !c.Capabilities().Has(contract.OperationCapability(input.Operation)) || input.Target.Type() != c.Type() {
		return result
	}
	if input.Policy.ForceStoreFalse {
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
	switch input.Operation {
	case contract.OperationChatCompletions, contract.OperationEmbeddings:
		result = c.adapter.AttemptWithPolicy(ctx, input.Sink, input.Target, input.Credential, input.Ingress, input.Embedding, attribution, input.Policy)
	case contract.OperationImagesGenerations:
		result = c.adapter.AttemptImageWithPolicy(ctx, input.Sink, input.Target, input.Credential, input.Image, attribution, input.Policy)
	case contract.OperationRerank:
		result = c.adapter.AttemptInferenceWithPolicy(ctx, input.Sink, input.Target, input.Credential, input.Inference, attribution, input.Policy)
	case contract.OperationResponses:
		result = c.attemptResponses(ctx, input, attribution)
	}
	if input.Observer != nil {
		input.Observer.TryObserve(Observation{Kind: ObservationAttemptFinished, Connector: c.Type(), TraceID: input.TraceID, AttemptIndex: input.AttemptIndex,
			Success: result.Success, Committed: result.Committed, Failure: result.Failure, Usage: result.Usage, Diagnostic: result.Diagnostic, GatewayUserAttributionSent: result.GatewayUserAttributionSent != nil && *result.GatewayUserAttributionSent})
	}
	return result
}

func (c *gatewayConnector) attemptResponses(ctx context.Context, input AttemptInput, attribution string) contract.AttemptResult {
	failed := contract.AttemptResult{Failure: contract.FailureInternal, Diagnostic: "response conversion unavailable"}
	if input.Policy.FlattenToolCalls {
		return failed
	}
	chat, err := input.Inference.AsGatewayChatRequest()
	if err != nil {
		return failed
	}
	defer chat.Clear()
	projection, err := openai.NewGatewayResponsesChatProjection(ctx, input.Sink, input.Inference, c.responseLimit)
	if err != nil {
		return failed
	}
	defer projection.Clear()
	result := c.adapter.AttemptWithPolicy(ctx, projection, input.Target, input.Credential, chat, nil, attribution, input.Policy)
	result.Committed = projection.Committed()
	result.ClientStatus = 0
	if result.Committed {
		result.ClientStatus = http.StatusOK
	}
	if projection.Rejected() {
		result.Success = false
		result.Failure = contract.FailureInternal
		result.FailureOrigin = contract.OriginPlatform
		if result.StreakDisposition != contract.StreakSuccess {
			result.StreakDisposition = contract.StreakNeutral
		}
		result.Diagnostic = "response conversion failed"
	} else if ctx.Err() != nil && result.Failure == contract.FailureSink {
		result.Failure = contract.FailureCanceled
		result.FailureOrigin = contract.OriginClientCancel
		if result.StreakDisposition != contract.StreakSuccess {
			result.StreakDisposition = contract.StreakNeutral
		}
		result.Diagnostic = "request canceled"
	}
	if result.Success && !projection.Complete() {
		result.Success = false
		result.Failure = contract.FailureInternal
		result.FailureOrigin = contract.OriginPlatform
		if result.StreakDisposition != contract.StreakSuccess {
			result.StreakDisposition = contract.StreakNeutral
		}
		result.Diagnostic = "response conversion was incomplete"
	}
	return result
}
