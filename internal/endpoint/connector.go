package endpoint

import "fmt"

// ConnectorType is a registered upstream connector protocol. It is a string so
// the registry can grow new types without a breaking schema change, but every
// value must be registered before it is accepted anywhere.
type ConnectorType string

const (
	// ConnectorOpenAICompatible is the only connector type supported in
	// v1.0.0-alpha.1. Anthropic-compatible and Dify App API are added to this
	// registry in later versions; until then they are unknown and rejected.
	ConnectorOpenAICompatible ConnectorType = "openai-compatible"
)

// Registry is the single authoritative source of supported connector types.
// Unknown types are rejected with no silent fallback to another protocol, so a
// misconfigured or hostile client cannot make the platform treat an endpoint
// as a different connector than it really is. The zero-value Registry supports
// no types; construct one with NewRegistry.
type Registry struct {
	supported map[ConnectorType]struct{}
}

// NewRegistry returns a Registry seeded with the connector types supported by
// the current build. In alpha.1 that is openai-compatible only; later versions
// extend this list in one place.
func NewRegistry() *Registry {
	r := &Registry{supported: make(map[ConnectorType]struct{})}
	r.register(ConnectorOpenAICompatible)
	return r
}

func (r *Registry) register(t ConnectorType) {
	if r.supported == nil {
		r.supported = make(map[ConnectorType]struct{})
	}
	r.supported[t] = struct{}{}
}

// Supported reports whether t is a registered connector type.
func (r *Registry) Supported(t ConnectorType) bool {
	if r == nil || !validConnectorText(string(t)) {
		return false
	}
	_, ok := r.supported[normalizeConnectorType(t)]
	return ok
}

// MustValidate returns t if it is registered, or an error otherwise. It never
// substitutes a different connector type for an unknown one. An empty type is
// rejected (callers default to openai-compatible before calling).
func (r *Registry) MustValidate(t ConnectorType) (ConnectorType, error) {
	if !validConnectorText(string(t)) {
		return "", fmt.Errorf("unsupported connector type")
	}
	norm := normalizeConnectorType(t)
	if r == nil || !r.Supported(norm) {
		return "", fmt.Errorf("unsupported connector type")
	}
	return norm, nil
}

// normalizeConnectorType trims only ordinary ASCII spaces. Validation happens
// before trimming so a caller cannot hide a control character at a boundary
// and turn it into a supported connector identifier.
func normalizeConnectorType(t ConnectorType) ConnectorType {
	return ConnectorType(trimSpace(string(t)))
}

func validConnectorText(value string) bool {
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
