package connector

import (
	"strings"
	"testing"

	contract "github.com/waiting-here/NonbiriAPI/internal/connector/contract"
	"github.com/waiting-here/NonbiriAPI/internal/connector/openai"
)

func TestInferenceExplicitConnectorMatrix(t *testing.T) {
	registry := NewDefaultRegistry()
	chat, err := openai.DecodeChatRequest(strings.NewReader(`{"model":"m","messages":[{"role":"user","content":"hi"}]}`), 0)
	if err != nil {
		t.Fatal(err)
	}
	defer chat.Clear()
	for _, kind := range []contract.Type{contract.TypeOpenAICompatible, contract.TypeOpenAIChatCompletions, contract.TypeOpenAIResponses} {
		if !registry.SupportsRequest(kind, chat) {
			t.Fatalf("Chat rejected on %s", kind)
		}
		request, err := openai.DecodeInferenceRequest(strings.NewReader(`{"model":"m","input":"hi"}`), contract.OperationResponses, 0)
		if err != nil {
			t.Fatal(err)
		}
		if !registry.SupportsInferenceRequest(kind, request) {
			t.Fatalf("Responses rejected on %s", kind)
		}
		request.Clear()
		descriptor, _ := registry.Descriptor(kind)
		if !descriptor.Capabilities.Has(contract.CapabilityResponses) {
			t.Fatal("Responses capability absent")
		}
	}
	request, err := openai.DecodeInferenceRequest(strings.NewReader(`{"model":"m","state":"hi","questions":{"q":{"type":"noul"}}}`), contract.OperationSystemOne, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer request.Clear()
	for _, kind := range registry.Types() {
		if registry.SupportsInferenceRequest(kind, request) != (kind == contract.TypeTypeSafeSystemOne) {
			t.Fatalf("System One crossed type %s", kind)
		}
	}
	descriptor, _ := registry.Descriptor(contract.TypeOpenAIChatCompletions)
	if descriptor.Capabilities.Has(contract.CapabilityEmbeddings) || descriptor.Capabilities.Has(contract.CapabilityImagesGenerations) || descriptor.Capabilities.Has(contract.CapabilityResponsesCompact) {
		t.Fatal("Chat-only type advertises unrelated operations")
	}
}
