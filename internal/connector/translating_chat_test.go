package connector

import (
	"strings"
	"testing"

	contract "github.com/waiting-here/NonbiriAPI/internal/connector/contract"
	"github.com/waiting-here/NonbiriAPI/internal/connector/openai"
)

func TestContinuationCannotCrossProtocolCandidates(t *testing.T) {
	registry := NewDefaultRegistry()
	for _, tc := range []struct {
		kind    contract.Type
		message string
	}{
		{contract.TypeGoogleGemini, `{"role":"assistant","content":"answer","extra_content":{"google":{"thought_signature":"YWJj"}}}`},
		{contract.TypeOpenAIResponses, `{"role":"assistant","content":"answer","extra_content":{"openai":{"reasoning":[{"type":"reasoning","id":"rs_a","summary":[],"encrypted_content":"opaque"}]}}}`},
	} {
		request, err := openai.DecodeChatRequest(strings.NewReader(`{"model":"public","messages":[`+tc.message+`,{"role":"user","content":"continue"}]}`), 0)
		if err != nil {
			t.Fatal(err)
		}
		defer request.Clear()
		for _, kind := range registry.Types() {
			if got := registry.SupportsRequest(kind, request); got != (kind == tc.kind) {
				t.Errorf("source %s target %s support %t", tc.kind, kind, got)
			}
		}
	}
}
func TestNewConnectorOperationsRemainDisjoint(t *testing.T) {
	registry := NewDefaultRegistry()
	for _, kind := range []contract.Type{contract.TypeGoogleGemini, contract.TypeOpenAIResponses} {
		descriptor, _ := registry.Descriptor(kind)
		if descriptor.Capabilities.Has(contract.CapabilityEmbeddings) || descriptor.Capabilities.Has(contract.CapabilityImagesGenerations) || descriptor.Capabilities.Has(contract.CapabilityDecisions) {
			t.Fatal(kind)
		}
	}
	decision, _ := registry.Descriptor(contract.TypeOpenAIDecisions)
	if decision.Capabilities.Has(contract.CapabilityText) || !decision.Capabilities.Has(contract.CapabilityDecisions) {
		t.Fatal(decision.Capabilities)
	}
}

func TestDecisionDirectPreflightRejectsChatOnlyPolicy(t *testing.T) {
	r, err := openai.DecodeDecisionRequest(strings.NewReader(`{"model":"m","input":"x","questions":[{"type":"predicate","instructions":""}]}`), 0)
	if err != nil {
		t.Fatal(err)
	}
	defer r.Clear()
	registry := NewDefaultRegistry()
	target := contract.NewTarget(contract.TypeOpenAIDecisions, "https://example.test/v1", "m")
	for _, policy := range []contract.AttemptPolicy{{FlattenToolCalls: true}, {ForceStoreFalse: true}} {
		if err := registry.CheckDecisionTarget(target, r, policy); err == nil {
			t.Fatal("chat-only policy admitted")
		}
	}
	for _, kind := range registry.Types() {
		if !kind.Valid() {
			t.Fatalf("registered type cannot be persisted: %s", kind)
		}
	}
	if contract.Type("unknown").Valid() {
		t.Fatal("unknown type accepted")
	}
}
