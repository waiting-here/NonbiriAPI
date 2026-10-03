package connector

import (
	"errors"
	"strings"
	"testing"

	contract "github.com/waiting-here/NonbiriAPI/internal/connector/contract"
	"github.com/waiting-here/NonbiriAPI/internal/connector/openai"
	"github.com/waiting-here/NonbiriAPI/internal/gatewaypolicy"
)

func TestGatewayToolArrayCacheAdmissionUsesExactTargetPolicy(t *testing.T) {
	models, err := gatewaypolicy.Parse(`{"models":[{"base_url":"https://gateway.example/native","model":"anthropic/verified","adapter":"anthropic_effort","max_output_tokens":128000,"cache":"anthropic_explicit"}]}`)
	if err != nil {
		t.Fatal(err)
	}
	registry := NewDefaultRegistryWithGatewayModels(models)
	request, err := openai.DecodeChatRequest(strings.NewReader(`{"model":"public","messages":[{"role":"assistant","content":null,"tool_calls":[{"id":"call-a","type":"function","function":{"name":"lookup","arguments":"{}"}}]},{"role":"tool","tool_call_id":"call-a","content":[{"type":"text","text":"result","cache_control":{"type":"ephemeral","ttl":"1h"}}]}]}`), openai.MaxRequestBodyBytes)
	if err != nil {
		t.Fatal(err)
	}
	defer request.Clear()
	if !request.Requirements().Capabilities().Has(contract.CapabilityPromptCache) || !request.Requirements().Capabilities().Has(contract.CapabilityTools) || !registry.SupportsRequest(contract.TypeAISDKGatewayV3, request) {
		t.Fatal("tool text array lost capabilities or Gateway syntax admission")
	}
	descriptor, ok := registry.Descriptor(contract.TypeAISDKGatewayV3)
	if !ok {
		t.Fatal("Gateway descriptor missing")
	}
	verified := contract.NewTarget(contract.TypeAISDKGatewayV3, "https://gateway.example/native", "anthropic/verified")
	if err := descriptor.CheckTarget(verified, request, contract.AttemptPolicy{}); err != nil {
		t.Fatal(err)
	}
	for _, target := range []contract.Target{
		contract.NewTarget(contract.TypeAISDKGatewayV3, "https://other.example/native", "anthropic/verified"),
		contract.NewTarget(contract.TypeAISDKGatewayV3, "https://gateway.example/native", "anthropic/other"),
	} {
		err := descriptor.CheckTarget(target, request, contract.AttemptPolicy{})
		var rejection *contract.RequestRejection
		if !errors.As(err, &rejection) || rejection.Stage != "model preflight" || rejection.Field != "messages[role=tool].content[].cache_control" {
			t.Fatalf("wrong exact-target rejection: %v", err)
		}
	}
	if registry.SupportsRequest(contract.TypeAnthropicCompatible, request) {
		t.Fatal("unverified prompt-cache connector was admitted")
	}
}
