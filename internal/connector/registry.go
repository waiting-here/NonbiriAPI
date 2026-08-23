// Package connector owns the process-wide closed-world connector registry and
// the one-attempt execution boundary. Public ingress remains the validated
// OpenAI Chat Completions request; protocol connectors translate it without
// turning vendor JSON into a core domain model.
package connector

import (
	"context"
	"errors"
	"net/http"
	"reflect"
	"sort"

	"github.com/waiting-here/NonbiriAPI/internal/backend"
	connectorcontract "github.com/waiting-here/NonbiriAPI/internal/connector/contract"
	"github.com/waiting-here/NonbiriAPI/internal/connector/openai"
)

// ResponseSink is the existing caller response boundary. Connectors may write
// only through it; retry remains governed by the returned commit bit.
type ResponseSink = http.ResponseWriter

type AttemptInput struct {
	Target       connectorcontract.Target
	Credential   *connectorcontract.ShortLivedSecret
	Ingress      *openai.ChatRequest
	Policy       connectorcontract.AttemptPolicy
	Sink         ResponseSink
	Observer     *SafeObserver
	TraceID      string
	AttemptIndex int
}

type Connector interface {
	Type() connectorcontract.Type
	Capabilities() connectorcontract.CapabilitySet
	Attempt(context.Context, AttemptInput) connectorcontract.AttemptResult
}

type ModelDiscoverer interface {
	Discover(context.Context, connectorcontract.DiscoveryInput) connectorcontract.DiscoveryResult
}

// OpenAIDriver is the compatibility seam around the existing OpenAI wire
// adapter. The registry descriptor wraps it in the neutral Connector contract;
// each additional protocol supplies its own constructor instead of widening
// this interface into a universal protocol driver.
type OpenAIDriver interface {
	ConnectorType() connectorcontract.Type
	Attempt(context.Context, http.ResponseWriter, openai.Target, *openai.ChatRequest, string) openai.AttemptResult
}

type Dependencies struct {
	Backend backend.Backend
	OpenAI  OpenAIDriver
}

type Constructor func(Dependencies) Connector

type Descriptor struct {
	Type         connectorcontract.Type
	Capabilities connectorcontract.CapabilitySet
	New          Constructor
	Discoverer   ModelDiscoverer
}

type Registry struct {
	descriptors map[connectorcontract.Type]Descriptor
}

func NewRegistry(descriptors ...Descriptor) (*Registry, error) {
	if len(descriptors) == 0 {
		return nil, errors.New("connector: at least one descriptor is required")
	}
	registry := &Registry{descriptors: make(map[connectorcontract.Type]Descriptor, len(descriptors))}
	for _, descriptor := range descriptors {
		if !validTypeText(string(descriptor.Type)) || normalizeType(descriptor.Type) != descriptor.Type {
			return nil, errors.New("connector: descriptor type is invalid")
		}
		if descriptor.New == nil {
			return nil, errors.New("connector: descriptor constructor is required")
		}
		if uint64(descriptor.Capabilities)&^uint64(connectorcontract.KnownCapabilities) != 0 {
			return nil, errors.New("connector: descriptor capabilities are invalid")
		}
		hasDiscovery := descriptor.Capabilities.Has(connectorcontract.CapabilityModelDiscovery)
		discovererPresent := !nilDiscoverer(descriptor.Discoverer)
		if hasDiscovery != discovererPresent {
			return nil, errors.New("connector: descriptor discovery capability is inconsistent")
		}
		if !discovererPresent {
			descriptor.Discoverer = nil
		}
		if _, duplicate := registry.descriptors[descriptor.Type]; duplicate {
			return nil, errors.New("connector: duplicate descriptor type")
		}
		registry.descriptors[descriptor.Type] = descriptor
	}
	return registry, nil
}

func NewDefaultRegistry() *Registry {
	registry, err := NewRegistry(openAIDescriptor())
	if err != nil {
		panic(err)
	}
	return registry
}

func (r *Registry) Supported(t connectorcontract.Type) bool {
	if r == nil || !validTypeText(string(t)) {
		return false
	}
	_, ok := r.descriptors[normalizeType(t)]
	return ok
}

func (r *Registry) MustValidate(t connectorcontract.Type) (connectorcontract.Type, error) {
	if !validTypeText(string(t)) {
		return "", errors.New("unsupported connector type")
	}
	normalized := normalizeType(t)
	if r == nil || !r.Supported(normalized) {
		return "", errors.New("unsupported connector type")
	}
	return normalized, nil
}

func (r *Registry) Descriptor(t connectorcontract.Type) (Descriptor, bool) {
	validated, err := r.MustValidate(t)
	if err != nil {
		return Descriptor{}, false
	}
	descriptor, ok := r.descriptors[validated]
	return descriptor, ok
}

func (r *Registry) Types() []connectorcontract.Type {
	if r == nil {
		return nil
	}
	types := make([]connectorcontract.Type, 0, len(r.descriptors))
	for connectorType := range r.descriptors {
		types = append(types, connectorType)
	}
	sort.Slice(types, func(i, j int) bool { return types[i] < types[j] })
	return types
}

func (r *Registry) NewConnector(t connectorcontract.Type, dependencies Dependencies) (Connector, error) {
	descriptor, ok := r.Descriptor(t)
	if !ok {
		return nil, errors.New("connector: connector type is not registered")
	}
	instance := descriptor.New(dependencies)
	if nilConnector(instance) {
		return nil, errors.New("connector: constructor rejected dependencies")
	}
	if instance.Type() != descriptor.Type {
		return nil, errors.New("connector: constructor returned mismatched type")
	}
	if instance.Capabilities() != descriptor.Capabilities {
		return nil, errors.New("connector: constructor returned mismatched capabilities")
	}
	return instance, nil
}

func openAIDescriptor() Descriptor {
	return Descriptor{
		Type:         connectorcontract.TypeOpenAICompatible,
		Capabilities: openAICapabilities(),
		New: func(dependencies Dependencies) Connector {
			driver := dependencies.OpenAI
			if !nilOpenAIDriver(driver) && driver.ConnectorType() != connectorcontract.TypeOpenAICompatible {
				return nil
			}
			if nilOpenAIDriver(driver) {
				adapter, err := openai.NewAdapter(openai.AdapterConfig{Backend: dependencies.Backend})
				if err != nil {
					return nil
				}
				driver = adapter
			}
			return &openAIConnector{driver: driver}
		},
		Discoverer: openai.ModelDiscoverer{},
	}
}

type openAIConnector struct {
	driver OpenAIDriver
}

func (*openAIConnector) Type() connectorcontract.Type {
	return connectorcontract.TypeOpenAICompatible
}

func (*openAIConnector) Capabilities() connectorcontract.CapabilitySet { return openAICapabilities() }

func openAICapabilities() connectorcontract.CapabilitySet {
	return connectorcontract.CapabilitySet(
		connectorcontract.CapabilityText |
			connectorcontract.CapabilitySystem |
			connectorcontract.CapabilityDeveloper |
			connectorcontract.CapabilityImages |
			connectorcontract.CapabilityTools |
			connectorcontract.CapabilityToolChoice |
			connectorcontract.CapabilityParallelTools |
			connectorcontract.CapabilityStream |
			connectorcontract.CapabilitySampling |
			connectorcontract.CapabilityUnknownOpenAIFields |
			connectorcontract.CapabilityModelDiscovery,
	)
}

func (c *openAIConnector) Attempt(ctx context.Context, input AttemptInput) connectorcontract.AttemptResult {
	result := connectorcontract.AttemptResult{Failure: connectorcontract.FailureInternal, Diagnostic: "forwarding attempt unavailable"}
	if c == nil || nilOpenAIDriver(c.driver) || ctx == nil || input.Sink == nil || input.Ingress == nil || input.Credential == nil || input.Target.Type() != c.Type() {
		if input.Credential != nil {
			input.Credential.Clear()
		}
		return result
	}
	plaintext, ciphertext, ok := input.Credential.Take()
	if !ok {
		input.Credential.Clear()
		return result
	}
	defer clear(plaintext)
	defer clear(ciphertext)
	input.Observer.TryObserve(Observation{
		Kind: ObservationAttemptStarted, Connector: c.Type(),
		TraceID: input.TraceID, AttemptIndex: input.AttemptIndex,
	})
	result = c.driver.Attempt(ctx, input.Sink, openai.NewTarget(
		input.Target.BaseURL(),
		input.Target.UpstreamModel(),
		openai.NewCredential(plaintext, ciphertext),
	), input.Ingress, input.Policy.SafetyIdentifier)
	input.Observer.TryObserve(Observation{
		Kind: ObservationAttemptFinished, Connector: c.Type(), Success: result.Success,
		Committed: result.Committed, Failure: result.Failure, Usage: result.Usage,
		Diagnostic: result.Diagnostic, TraceID: input.TraceID, AttemptIndex: input.AttemptIndex,
	})
	return result
}

func normalizeType(t connectorcontract.Type) connectorcontract.Type {
	return connectorcontract.Type(trimASCIISpace(string(t)))
}

func validTypeText(value string) bool {
	if value == "" {
		return false
	}
	for _, r := range value {
		if r < 0x20 || r == 0x7f {
			return false
		}
	}
	return true
}

func trimASCIISpace(value string) string {
	start, end := 0, len(value)
	for start < end && value[start] == ' ' {
		start++
	}
	for end > start && value[end-1] == ' ' {
		end--
	}
	return value[start:end]
}

func nilConnector(value Connector) bool        { return nilInterface(value) }
func nilDiscoverer(value ModelDiscoverer) bool { return nilInterface(value) }
func nilOpenAIDriver(value OpenAIDriver) bool  { return nilInterface(value) }

func nilInterface(value any) bool {
	if value == nil {
		return true
	}
	reflected := reflect.ValueOf(value)
	switch reflected.Kind() {
	case reflect.Chan, reflect.Func, reflect.Interface, reflect.Map, reflect.Pointer, reflect.Slice:
		return reflected.IsNil()
	default:
		return false
	}
}
