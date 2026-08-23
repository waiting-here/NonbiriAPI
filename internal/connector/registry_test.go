package connector

import (
	"context"
	"testing"

	connectorcontract "github.com/waiting-here/NonbiriAPI/internal/connector/contract"
)

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
	descriptor, ok := registry.Descriptor(connectorcontract.TypeOpenAICompatible)
	if !ok || descriptor.New == nil || descriptor.Discoverer == nil || !descriptor.Capabilities.Has(connectorcontract.CapabilityModelDiscovery) {
		t.Fatalf("default descriptor incomplete: %+v ok=%v", descriptor, ok)
	}
	if connector, err := registry.NewConnector(connectorcontract.TypeOpenAICompatible, Dependencies{}); err == nil || connector != nil {
		t.Fatalf("connector constructed without shared backend/driver: %v %v", connector, err)
	}
}
