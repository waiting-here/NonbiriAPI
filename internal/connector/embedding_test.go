package connector

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	connectorcontract "github.com/waiting-here/NonbiriAPI/internal/connector/contract"
	"github.com/waiting-here/NonbiriAPI/internal/connector/openai"
)

type embeddingTestDriver struct {
	legacyOpenAIPolicyDriver
	embeddingCalls int
	policy         connectorcontract.AttemptPolicy
}

func (d *embeddingTestDriver) AttemptEmbedding(_ context.Context, _ http.ResponseWriter, _ openai.Target, _ *openai.EmbeddingRequest, p connectorcontract.AttemptPolicy) openai.AttemptResult {
	d.embeddingCalls++
	d.policy = p
	return openai.AttemptResult{Success: true}
}

func TestOperationBoundaryRejectsAmbiguousSnapshotsBeforeDriver(t *testing.T) {
	registry := NewDefaultRegistry()
	r, err := openai.DecodeEmbeddingRequest(strings.NewReader(`{"model":"same/model","input":"a"}`), 0)
	if err != nil {
		t.Fatal(err)
	}
	defer r.Clear()
	chat := &openai.ChatRequest{Model: "same/model"}
	if !registry.SupportsOperationRequest(connectorcontract.TypeOpenAICompatible, connectorcontract.OperationEmbeddings, nil, r) || registry.SupportsOperationRequest(connectorcontract.TypeAnthropicCompatible, connectorcontract.OperationEmbeddings, nil, r) || registry.SupportsOperationRequest("unknown", connectorcontract.OperationEmbeddings, nil, r) {
		t.Fatal("operation capability filtering failed")
	}
	driver := &embeddingTestDriver{}
	protocol := &openAIConnector{driver: driver}
	for _, input := range []AttemptInput{
		{Operation: "", Ingress: chat}, {Operation: "unknown", Embedding: r},
		{Operation: connectorcontract.OperationEmbeddings, Ingress: chat, Embedding: r},
		{Operation: connectorcontract.OperationChatCompletions, Embedding: r},
		{Operation: connectorcontract.OperationEmbeddings},
	} {
		input.Target = connectorcontract.NewTarget(connectorcontract.TypeOpenAICompatible, "https://upstream.example/v1", "physical")
		input.Sink = httptest.NewRecorder()
		input.Credential = connectorcontract.NewShortLivedSecret([]byte("secret"), []byte("cipher"))
		if result := protocol.Attempt(context.Background(), input); result.Success || driver.embeddingCalls != 0 || driver.calls.Load() != 0 {
			t.Fatal("invalid operation reached driver")
		}
		if _, _, ok := input.Credential.Take(); ok {
			t.Fatal("rejected operation retained credential")
		}
	}
	input := AttemptInput{Operation: connectorcontract.OperationEmbeddings, Embedding: r,
		Target: connectorcontract.NewTarget(connectorcontract.TypeOpenAICompatible, "https://upstream.example/v1", "physical"),
		Sink:   httptest.NewRecorder(), Credential: connectorcontract.NewShortLivedSecret([]byte("secret"), nil),
		Policy: connectorcontract.AttemptPolicy{SafetyIdentifier: "anon_origin", ForceStoreFalse: true, FlattenToolCalls: true}}
	if result := protocol.Attempt(context.Background(), input); !result.Success || driver.embeddingCalls != 1 || driver.calls.Load() != 0 || driver.policy.ForceStoreFalse || driver.policy.FlattenToolCalls || driver.policy.SafetyIdentifier != "anon_origin" {
		t.Fatal("embedding dispatch applied chat policies or wrong driver")
	}
	input.Target = connectorcontract.NewTarget(connectorcontract.TypeAnthropicCompatible, "https://upstream.example/v1", "physical")
	input.Credential = connectorcontract.NewShortLivedSecret([]byte("secret"), nil)
	anthropicDriver := &legacyAnthropicPolicyDriver{}
	if result := (&anthropicConnector{driver: anthropicDriver}).Attempt(context.Background(), input); result.Success || anthropicDriver.calls.Load() != 0 {
		t.Fatal("embedding reached chat-only connector")
	}
}
