package connector

import (
	"errors"
	"slices"
	"testing"

	contract "github.com/waiting-here/NonbiriAPI/internal/connector/contract"
)

func nativeRouteServices(t *testing.T, services ...contract.NativeService) contract.NativeServiceSet {
	t.Helper()
	set, err := contract.ParseNativeServices(services)
	if err != nil {
		t.Fatal(err)
	}
	return set
}

func TestNativeServiceRoutesPrioritizeNative(t *testing.T) {
	services := nativeRouteServices(t, contract.NativeServiceResponses, contract.NativeServiceChatCompletions)
	for _, test := range []struct {
		op   contract.Operation
		want []ServiceRoute
	}{
		{contract.OperationChatCompletions, []ServiceRoute{
			{contract.TypeOpenAICompatible, contract.TypeOpenAICompatible, contract.NativeServiceChatCompletions, true},
			{contract.TypeOpenAICompatible, contract.TypeOpenAIResponses, contract.NativeServiceResponses, false},
		}},
		{contract.OperationResponses, []ServiceRoute{
			{contract.TypeOpenAICompatible, contract.TypeOpenAIResponses, contract.NativeServiceResponses, true},
			{contract.TypeOpenAICompatible, contract.TypeOpenAIChatCompletions, contract.NativeServiceChatCompletions, false},
		}},
	} {
		t.Run(string(test.op), func(t *testing.T) {
			// The endpoint's rerank format remains valid for this text-only subset.
			routes, err := ResolveServiceRoutes(contract.TypeOpenAICompatible, services, contract.RerankFormatCohere, test.op)
			if err != nil || !slices.Equal(routes, test.want) {
				t.Fatalf("routes = %+v, error = %v, want %+v", routes, err, test.want)
			}
		})
	}
}

func TestNativeServiceRoutesRespectAuthorizedSubset(t *testing.T) {
	for _, test := range []struct {
		name    string
		kind    contract.Type
		service contract.NativeService
		op      contract.Operation
		wire    contract.Type
		native  bool
	}{
		{"disabled chat uses only responses conversion", contract.TypeOpenAICompatible, contract.NativeServiceResponses, contract.OperationChatCompletions, contract.TypeOpenAIResponses, false},
		{"disabled responses uses only chat conversion", contract.TypeOpenAICompatible, contract.NativeServiceChatCompletions, contract.OperationResponses, contract.TypeOpenAIChatCompletions, false},
		{"chat alias conversion", contract.TypeOpenAIChatCompletions, contract.NativeServiceChatCompletions, contract.OperationResponses, contract.TypeOpenAIChatCompletions, false},
		{"responses alias conversion", contract.TypeOpenAIResponses, contract.NativeServiceResponses, contract.OperationChatCompletions, contract.TypeOpenAIResponses, false},
		{"responses does not imply compact", contract.TypeOpenAICompatible, contract.NativeServiceResponses, contract.OperationResponsesCompact, "", false},
		{"compact does not imply responses", contract.TypeOpenAIResponses, contract.NativeServiceResponsesCompact, contract.OperationResponses, "", false},
		{"chat does not imply embeddings", contract.TypeOpenAICompatible, contract.NativeServiceChatCompletions, contract.OperationEmbeddings, "", false},
		{"messages conversion", contract.TypeAnthropicCompatible, contract.NativeServiceAnthropicMessages, contract.OperationChatCompletions, contract.TypeAnthropicCompatible, false},
		{"no responses to messages conversion", contract.TypeAnthropicCompatible, contract.NativeServiceAnthropicMessages, contract.OperationResponses, "", false},
		{"gemini conversion", contract.TypeGoogleGemini, contract.NativeServiceGeminiGenerateContent, contract.OperationChatCompletions, contract.TypeGoogleGemini, false},
		{"no responses to gemini conversion", contract.TypeGoogleGemini, contract.NativeServiceGeminiGenerateContent, contract.OperationResponses, "", false},
		{"gateway text does not imply embeddings", contract.TypeAISDKGatewayV4, contract.NativeServiceGatewayText, contract.OperationEmbeddings, "", false},
		{"gateway embeddings do not imply text", contract.TypeAISDKGatewayV3, contract.NativeServiceEmbeddings, contract.OperationChatCompletions, "", false},
		{"gateway service is not an ingress operation", contract.TypeAISDKGatewayV4, contract.NativeServiceGatewayText, "gateway_text", "", false},
		{"systemone has no chat conversion", contract.TypeTypeSafeSystemOne, contract.NativeServiceSystemOne, contract.OperationChatCompletions, "", false},
		{"unselected model", contract.TypeOpenAICompatible, "", contract.OperationChatCompletions, "", false},
	} {
		t.Run(test.name, func(t *testing.T) {
			services := nativeRouteServices(t)
			if test.service != "" {
				services = nativeRouteServices(t, test.service)
			}
			routes, err := ResolveServiceRoutes(test.kind, services, "", test.op)
			if test.wire == "" {
				if !errors.Is(err, ErrNoServiceRoute) || len(routes) != 0 {
					t.Fatalf("unauthorized service restored: %+v, %v", routes, err)
				}
				return
			}
			want := ServiceRoute{test.kind, test.wire, test.service, test.native}
			if err != nil || len(routes) != 1 || routes[0] != want {
				t.Fatalf("route = %+v, error = %v, want %+v", routes, err, want)
			}
		})
	}
}

func TestNativeServiceRoutesSelectExistingWires(t *testing.T) {
	for _, kind := range []contract.Type{contract.TypeOpenAICompatible, contract.TypeOpenAIChatCompletions, contract.TypeOpenAIResponses, contract.TypeOpenAIDecisions} {
		for _, test := range []struct {
			service contract.NativeService
			op      contract.Operation
			format  contract.RerankFormat
			wire    contract.Type
		}{
			{contract.NativeServiceChatCompletions, contract.OperationChatCompletions, "", contract.TypeOpenAICompatible},
			{contract.NativeServiceEmbeddings, contract.OperationEmbeddings, "", contract.TypeOpenAICompatible},
			{contract.NativeServiceResponsesCompact, contract.OperationResponsesCompact, "", contract.TypeOpenAIResponses},
			{contract.NativeServiceResponsesInputTokens, contract.OperationResponsesInputTokens, "", contract.TypeOpenAIResponses},
			{contract.NativeServiceDecisions, contract.OperationDecisions, "", contract.TypeOpenAIDecisions},
			{contract.NativeServiceRerank, contract.OperationRerank, contract.RerankFormatGeneric, contract.TypeOpenAICompatible},
			{contract.NativeServiceRerank, contract.OperationRerank, contract.RerankFormatCohere, contract.TypeCohereRerank},
			{contract.NativeServiceRerank, contract.OperationRerank, contract.RerankFormatJina, contract.TypeJinaRerank},
			{contract.NativeServiceRerank, contract.OperationRerank, contract.RerankFormatVoyage, contract.TypeVoyageRerank},
			{contract.NativeServiceRerank, contract.OperationRerank, contract.RerankFormatSiliconFlow, contract.TypeSiliconFlowRerank},
		} {
			t.Run(string(kind)+"/"+string(test.op)+"/"+string(test.format), func(t *testing.T) {
				routes, err := ResolveServiceRoutes(kind, nativeRouteServices(t, test.service), test.format, test.op)
				want := ServiceRoute{kind, test.wire, test.service, true}
				if err != nil || len(routes) != 1 || routes[0] != want {
					t.Fatalf("existing wire not selected: %+v, %v", routes, err)
				}
			})
		}
	}
	rerank := nativeRouteServices(t, contract.NativeServiceRerank)
	if routes, err := ResolveServiceRoutes(contract.TypeOpenAICompatible, rerank, "", contract.OperationRerank); !errors.Is(err, contract.ErrInvalidRerankFormat) || len(routes) != 0 {
		t.Fatal("rerank format was inferred")
	}
}

func TestNativeServiceRoutesKeepGatewayVersionAndFourServices(t *testing.T) {
	for _, kind := range []contract.Type{contract.TypeAISDKGatewayV3, contract.TypeAISDKGatewayV4} {
		t.Run(string(kind), func(t *testing.T) {
			services, err := contract.SupportedNativeServices(kind)
			if err != nil || len(services.Services()) != 4 {
				t.Fatalf("gateway services = %v, %v", services.Services(), err)
			}
			for _, test := range []struct {
				op      contract.Operation
				service contract.NativeService
				native  bool
			}{
				{contract.OperationChatCompletions, contract.NativeServiceGatewayText, false},
				{contract.OperationResponses, contract.NativeServiceGatewayText, false},
				{contract.OperationImagesGenerations, contract.NativeServiceImagesGenerations, true},
				{contract.OperationEmbeddings, contract.NativeServiceEmbeddings, true},
				{contract.OperationRerank, contract.NativeServiceRerank, true},
			} {
				routes, err := ResolveServiceRoutes(kind, services, "", test.op)
				want := ServiceRoute{kind, kind, test.service, test.native}
				if err != nil || len(routes) != 1 || routes[0] != want {
					t.Fatalf("version or native identity changed for %s: %+v, %v", test.op, routes, err)
				}
			}
			if routes, err := ResolveServiceRoutes(kind, services, "", contract.OperationDecisions); !errors.Is(err, ErrNoServiceRoute) || len(routes) != 0 {
				t.Fatal("fifth Gateway service was enabled")
			}
		})
	}
}

func TestNativeServiceRoutesUseRegisteredDrivers(t *testing.T) {
	registry := NewDefaultRegistry()
	for _, kind := range registry.Types() {
		services, err := contract.SupportedNativeServices(kind)
		if err != nil {
			t.Fatalf("registered connector has no native service contract: %s", kind)
		}
		format := contract.RerankFormat("")
		if kind.OpenAIServiceFamily() {
			format = contract.RerankFormatGeneric
		}
		for _, operation := range contract.AllOperations() {
			routes, err := ResolveServiceRoutes(kind, services, format, operation)
			if errors.Is(err, ErrNoServiceRoute) {
				continue
			}
			if err != nil {
				t.Fatal(err)
			}
			for _, route := range routes {
				descriptor, ok := registry.Descriptor(route.WireType)
				if !ok || !descriptor.Capabilities.Has(contract.OperationCapability(operation)) || !services.Has(route.NativeService) || route.ConfiguredType != kind {
					t.Fatalf("route bypasses registered operation or permission: %+v, %s", route, operation)
				}
			}
		}
	}
}
