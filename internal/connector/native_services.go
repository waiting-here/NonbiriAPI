package connector

import (
	"errors"

	contract "github.com/waiting-here/NonbiriAPI/internal/connector/contract"
)

var ErrNoServiceRoute = errors.New("no authorized native service can represent this operation")

// ServiceRoute is a structural choice, not proof that request parameters can
// be represented. The selected wire still requires its existing target checks.
type ServiceRoute struct {
	ConfiguredType contract.Type
	WireType       contract.Type
	NativeService  contract.NativeService
	NativeMatch    bool
}

// ResolveServiceRoutes lists native matches before existing finite conversions.
// It performs no I/O or retry; callers select within one binding before dispatch.
func ResolveServiceRoutes(kind contract.Type, services contract.NativeServiceSet, format contract.RerankFormat, operation contract.Operation) ([]ServiceRoute, error) {
	if services.Empty() {
		return nil, ErrNoServiceRoute
	}
	if err := contract.ValidateNativeServices(kind, services, format); err != nil {
		return nil, err
	}
	known := false
	for _, current := range contract.AllOperations() {
		known = known || operation == current
	}
	if !known {
		return nil, ErrNoServiceRoute
	}
	routes := []ServiceRoute{}
	add := func(service contract.NativeService, wire contract.Type, native bool) {
		if services.Has(service) {
			routes = append(routes, ServiceRoute{ConfiguredType: kind, WireType: wire, NativeService: service, NativeMatch: native})
		}
	}
	service := contract.NativeService(operation)
	if services.Has(service) {
		wire := kind
		if kind.OpenAIServiceFamily() {
			wire = contract.TypeOpenAICompatible
			switch operation {
			case contract.OperationResponses, contract.OperationResponsesCompact, contract.OperationResponsesInputTokens:
				wire = contract.TypeOpenAIResponses
			case contract.OperationDecisions:
				wire = contract.TypeOpenAIDecisions
			case contract.OperationRerank:
				wire = rerankWire(format)
			}
		}
		add(service, wire, true)
	}
	switch operation {
	case contract.OperationChatCompletions:
		add(contract.NativeServiceResponses, contract.TypeOpenAIResponses, false)
		add(contract.NativeServiceAnthropicMessages, contract.TypeAnthropicCompatible, false)
		add(contract.NativeServiceGeminiGenerateContent, contract.TypeGoogleGemini, false)
		if kind.AISDKGateway() {
			add(contract.NativeServiceGatewayText, kind, false)
		}
	case contract.OperationResponses:
		add(contract.NativeServiceChatCompletions, contract.TypeOpenAIChatCompletions, false)
		if kind.AISDKGateway() {
			add(contract.NativeServiceGatewayText, kind, false)
		}
	}
	if len(routes) == 0 {
		return nil, ErrNoServiceRoute
	}
	return routes, nil
}

func rerankWire(format contract.RerankFormat) contract.Type {
	switch format {
	case contract.RerankFormatCohere:
		return contract.TypeCohereRerank
	case contract.RerankFormatJina:
		return contract.TypeJinaRerank
	case contract.RerankFormatVoyage:
		return contract.TypeVoyageRerank
	case contract.RerankFormatSiliconFlow:
		return contract.TypeSiliconFlowRerank
	default:
		// The selection validator admits only generic at this point.
		return contract.TypeOpenAICompatible
	}
}
