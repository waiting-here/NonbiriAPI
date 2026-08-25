package connector

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/waiting-here/NonbiriAPI/internal/connector/anthropic"
	connectorcontract "github.com/waiting-here/NonbiriAPI/internal/connector/contract"
	"github.com/waiting-here/NonbiriAPI/internal/connector/openai"
)

type legacyOpenAIPolicyDriver struct{ calls atomic.Int32 }

func (*legacyOpenAIPolicyDriver) ConnectorType() connectorcontract.Type {
	return connectorcontract.TypeOpenAICompatible
}

func (d *legacyOpenAIPolicyDriver) Attempt(context.Context, http.ResponseWriter, openai.Target, *openai.ChatRequest, string) connectorcontract.AttemptResult {
	d.calls.Add(1)
	return connectorcontract.AttemptResult{Success: true}
}

type legacyAnthropicPolicyDriver struct{ calls atomic.Int32 }

func (*legacyAnthropicPolicyDriver) ConnectorType() connectorcontract.Type {
	return connectorcontract.TypeAnthropicCompatible
}

func (d *legacyAnthropicPolicyDriver) Attempt(context.Context, http.ResponseWriter, anthropic.Target, *openai.ChatRequest, string) connectorcontract.AttemptResult {
	d.calls.Add(1)
	return connectorcontract.AttemptResult{Success: true}
}

type registryTestConnector struct {
	connectorType connectorcontract.Type
	capabilities  connectorcontract.CapabilitySet
}

func (c registryTestConnector) Type() connectorcontract.Type { return c.connectorType }
func (c registryTestConnector) Capabilities() connectorcontract.CapabilitySet {
	return c.capabilities
}
func (registryTestConnector) Attempt(context.Context, AttemptInput) connectorcontract.AttemptResult {
	return connectorcontract.AttemptResult{Success: true}
}

type registryTestDiscoverer struct{}

func (registryTestDiscoverer) Discover(context.Context, connectorcontract.DiscoveryInput) connectorcontract.DiscoveryResult {
	return connectorcontract.DiscoveryResult{}
}

func registryDescriptor(connectorType connectorcontract.Type) Descriptor {
	return Descriptor{
		Type:         connectorType,
		Capabilities: connectorcontract.CapabilitySet(connectorcontract.CapabilityText),
		New: func(Dependencies) Connector {
			return registryTestConnector{
				connectorType: connectorType,
				capabilities:  connectorcontract.CapabilitySet(connectorcontract.CapabilityText),
			}
		},
	}
}

func TestRegistryRejectsInvalidDescriptorsAndConstructors(t *testing.T) {
	valid := registryDescriptor("test-compatible")
	tests := []struct {
		name        string
		descriptors []Descriptor
	}{
		{name: "empty"},
		{name: "duplicate", descriptors: []Descriptor{valid, valid}},
		{name: "nil constructor", descriptors: []Descriptor{{Type: "test-compatible"}}},
		{name: "padded descriptor", descriptors: []Descriptor{{Type: " test-compatible", New: valid.New}}},
		{name: "unknown capability", descriptors: []Descriptor{{Type: "test-compatible", Capabilities: 1 << 63, New: valid.New}}},
		{name: "discoverer missing capability", descriptors: []Descriptor{{Type: "test-compatible", New: valid.New, Discoverer: registryTestDiscoverer{}}}},
		{name: "capability missing discoverer", descriptors: []Descriptor{{Type: "test-compatible", Capabilities: connectorcontract.CapabilitySet(connectorcontract.CapabilityModelDiscovery), New: valid.New}}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if registry, err := NewRegistry(test.descriptors...); err == nil || registry != nil {
				t.Fatalf("invalid registry accepted: registry=%v err=%v", registry, err)
			}
		})
	}
}

func TestRegistryLookupIsClosedWorldAndConstructorTypeChecked(t *testing.T) {
	descriptor := registryDescriptor("test-compatible")
	registry, err := NewRegistry(descriptor)
	if err != nil {
		t.Fatal(err)
	}
	if registry.Supported("unknown") {
		t.Fatal("unknown connector was admitted")
	}
	if _, err := registry.MustValidate("unknown"); err == nil {
		t.Fatal("unknown connector validated")
	}
	if got, err := registry.MustValidate(" test-compatible "); err != nil || got != "test-compatible" {
		t.Fatalf("normalized lookup = %q, %v", got, err)
	}
	connector, err := registry.NewConnector("test-compatible", Dependencies{})
	if err != nil || connector.Type() != "test-compatible" {
		t.Fatalf("constructor = %v, %v", connector, err)
	}

	mismatch := descriptor
	mismatch.New = func(Dependencies) Connector {
		return registryTestConnector{connectorType: "other", capabilities: descriptor.Capabilities}
	}
	mismatchRegistry, err := NewRegistry(mismatch)
	if err != nil {
		t.Fatal(err)
	}
	if instance, err := mismatchRegistry.NewConnector("test-compatible", Dependencies{}); err == nil || instance != nil {
		t.Fatalf("mismatched constructor accepted: %v, %v", instance, err)
	}

	capabilityMismatch := descriptor
	capabilityMismatch.Capabilities = connectorcontract.CapabilitySet(connectorcontract.CapabilityStream)
	capabilityRegistry, err := NewRegistry(capabilityMismatch)
	if err != nil {
		t.Fatal(err)
	}
	if instance, err := capabilityRegistry.NewConnector("test-compatible", Dependencies{}); err == nil || instance != nil {
		t.Fatalf("mismatched connector capabilities accepted: %v, %v", instance, err)
	}

	nilResult := descriptor
	nilResult.New = func(Dependencies) Connector { return nil }
	nilRegistry, err := NewRegistry(nilResult)
	if err != nil {
		t.Fatal(err)
	}
	if instance, err := nilRegistry.NewConnector("test-compatible", Dependencies{}); err == nil || instance != nil {
		t.Fatalf("nil constructor result accepted: %v, %v", instance, err)
	}
}

func TestRegistryNormalizesTypedNilDiscovererToUnsupported(t *testing.T) {
	var typedNil *registryTestDiscoverer
	descriptor := registryDescriptor("test-compatible")
	descriptor.Discoverer = typedNil
	registry, err := NewRegistry(descriptor)
	if err != nil {
		t.Fatal(err)
	}
	stored, ok := registry.Descriptor("test-compatible")
	if !ok || stored.Discoverer != nil || stored.Capabilities.Has(connectorcontract.CapabilityModelDiscovery) {
		t.Fatalf("typed nil discoverer was not normalized: %+v ok=%v", stored, ok)
	}
}

func TestDefaultRegistryDescriptorDrivesExecutionAndDiscovery(t *testing.T) {
	registry := NewDefaultRegistry()
	types := registry.Types()
	if len(types) != 2 || types[0] != connectorcontract.TypeAnthropicCompatible || types[1] != connectorcontract.TypeOpenAICompatible {
		t.Fatalf("default registry types=%v", types)
	}
	for _, connectorType := range types {
		descriptor, ok := registry.Descriptor(connectorType)
		if !ok || descriptor.New == nil || descriptor.Discoverer == nil || descriptor.Supports == nil || !descriptor.Capabilities.Has(connectorcontract.CapabilityModelDiscovery) {
			t.Fatalf("default descriptor %q incomplete: %+v ok=%v", connectorType, descriptor, ok)
		}
	}
	if connector, err := registry.NewConnector(connectorcontract.TypeOpenAICompatible, Dependencies{}); err == nil || connector != nil {
		t.Fatalf("connector constructed without shared backend/driver: %v %v", connector, err)
	}
}

func TestDefaultRegistryFiltersByProtocolFidelity(t *testing.T) {
	registry := NewDefaultRegistry()
	tests := []struct {
		name      string
		body      string
		openAI    bool
		anthropic bool
	}{
		{name: "common text", body: `{"model":"p/m","messages":[{"role":"user","content":"hi"}]}`, openAI: true, anthropic: true},
		{name: "stream null", body: `{"model":"p/m","messages":[{"role":"user","content":"hi"}],"stream":null}`, anthropic: true},
		{name: "unknown OpenAI field", body: `{"model":"p/m","messages":[{"role":"user","content":"hi"}],"n":1}`, openAI: true},
		{name: "Anthropic image subset", body: `{"model":"p/m","messages":[{"role":"user","content":[{"type":"image_url","image_url":{"url":"https://images.example/a.png"}}]}]}`, openAI: true, anthropic: true},
		{name: "unsupported image detail", body: `{"model":"p/m","messages":[{"role":"user","content":[{"type":"image_url","image_url":{"url":"https://images.example/a.png","detail":"low"}}]}]}`, openAI: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			request, err := openai.DecodeChatRequest(strings.NewReader(test.body), openai.MaxRequestBodyBytes)
			if err != nil {
				t.Fatal(err)
			}
			defer request.Clear()
			if got := registry.SupportsRequest(connectorcontract.TypeOpenAICompatible, request); got != test.openAI {
				t.Fatalf("OpenAI support=%v want=%v", got, test.openAI)
			}
			if got := registry.SupportsRequest(connectorcontract.TypeAnthropicCompatible, request); got != test.anthropic {
				t.Fatalf("Anthropic support=%v want=%v", got, test.anthropic)
			}
			if registry.SupportsRequest("unknown", request) {
				t.Fatal("unknown connector supported request")
			}
		})
	}
}

func TestLegacyOpenAIDriverCannotSilentlyIgnoreExperimentalPolicies(t *testing.T) {
	driver := &legacyOpenAIPolicyDriver{}
	protocol := &openAIConnector{driver: driver}
	for _, policy := range []connectorcontract.AttemptPolicy{
		{ForceStoreFalse: true},
		{FlattenToolCalls: true},
	} {
		credential := connectorcontract.NewShortLivedSecret([]byte("secret"), []byte("cipher"))
		result := protocol.Attempt(context.Background(), AttemptInput{
			Target:     connectorcontract.NewTarget(connectorcontract.TypeOpenAICompatible, "https://upstream.example", "up/model"),
			Credential: credential,
			Ingress:    &openai.ChatRequest{Model: "p/m"},
			Policy:     policy,
			Sink:       httptest.NewRecorder(),
		})
		if result.Failure != connectorcontract.FailureInternal || result.Diagnostic != "connector policy unsupported" || driver.calls.Load() != 0 {
			t.Fatalf("policy=%+v result=%+v driver_calls=%d", policy, result, driver.calls.Load())
		}
		if _, _, ok := credential.Take(); ok {
			t.Fatalf("policy=%+v credential remained takeable after fail-closed rejection", policy)
		}
	}

	result := protocol.Attempt(context.Background(), AttemptInput{
		Target:     connectorcontract.NewTarget(connectorcontract.TypeOpenAICompatible, "https://upstream.example", "up/model"),
		Credential: connectorcontract.NewShortLivedSecret([]byte("secret"), []byte("cipher")),
		Ingress:    &openai.ChatRequest{Model: "p/m"},
		Policy:     connectorcontract.AttemptPolicy{},
		Sink:       httptest.NewRecorder(),
	})
	if !result.Success || driver.calls.Load() != 1 {
		t.Fatalf("legacy zero-policy fallback result=%+v driver_calls=%d", result, driver.calls.Load())
	}
}

func TestAnthropicConnectorRejectsOpenAIOnlyPoliciesBeforeCredentialTake(t *testing.T) {
	driver := &legacyAnthropicPolicyDriver{}
	protocol := &anthropicConnector{driver: driver}
	for _, policy := range []connectorcontract.AttemptPolicy{
		{ForceStoreFalse: true},
		{FlattenToolCalls: true},
	} {
		credential := connectorcontract.NewShortLivedSecret([]byte("secret"), []byte("cipher"))
		result := protocol.Attempt(context.Background(), AttemptInput{
			Target:     connectorcontract.NewTarget(connectorcontract.TypeAnthropicCompatible, "https://upstream.example", "up/model"),
			Credential: credential,
			Ingress:    &openai.ChatRequest{Model: "p/m"},
			Policy:     policy,
			Sink:       httptest.NewRecorder(),
		})
		if result.Failure != connectorcontract.FailureInternal || result.Diagnostic != "connector policy incompatible" || driver.calls.Load() != 0 {
			t.Fatalf("policy=%+v result=%+v driver_calls=%d", policy, result, driver.calls.Load())
		}
		if _, _, ok := credential.Take(); ok {
			t.Fatalf("policy=%+v credential remained takeable after rejection", policy)
		}
	}
}
